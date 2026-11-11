import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { mkdir, readFile, open, lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { createFileLedgerStore } from '../network/host/store.js';
import { openResourceGate } from './gate.js';
import { createMemberApi, installation } from './member-api.js';
import { validateArchive } from './archive.js';
import { canonicalize } from './json.js';
import {
  readResourceRoots,
  verifyRevocations,
  isRevoked,
  uuid,
  refuse,
  instant,
  signed,
  exact,
} from './trust.js';
const BOOT = `${process.pid}:${performance.timeOrigin}`;
/** @typedef {ReturnType<import('./host.js').resourceHost>} Host */
/** @param {{directory:string,runtime:any,host:Host,trustFile?:string,roots?:()=>Promise<{releaseKeys:ReadonlyMap<string,import('node:crypto').KeyObject>,revocationKeys:ReadonlyMap<string,import('node:crypto').KeyObject>}>,fetchImpl?:typeof fetch,alive?:()=>boolean,now?:()=>number}} options */
export async function openMemberResources(options) {
  const { host, directory, now = Date.now } = options;
  const api = createMemberApi(options),
    identity = api.identity;
  const root = join(
    directory,
    identity.tenantId,
    identity.ownerId,
    identity.clientId,
  );
  const store = await createFileLedgerStore({
    dir: join(root, 'manifest'),
  }).open();
  /** @type {Awaited<ReturnType<typeof openResourceGate>>} */
  let gate;
  try {
    gate = await openResourceGate(
      await createFileLedgerStore({ dir: join(root, 'release') }).open(),
    );
  } catch (error) {
    await store.close();
    throw error;
  }
  /** @type {{schemaVersion:number, records:Record<string,any>, operations:Record<string,any>, revocation?:any}} */
  let state = {
    schemaVersion: 1,
    records: Object.create(null),
    operations: Object.create(null),
  };
  try {
    const loaded = /** @type {any} */ (await store.load());
    if (loaded !== undefined) {
      if (
        loaded.schemaVersion !== 1 ||
        !loaded.records ||
        !loaded.operations ||
        Array.isArray(loaded.records) ||
        Array.isArray(loaded.operations)
      )
        refuse('resource.storage_unavailable');
      for (const [id, value] of Object.entries(loaded.records)) {
        const r = /** @type {any} */ (value);
        if (
          !uuid(id) ||
          r.row?.packageId !== id ||
          typeof r.name !== 'string' ||
          !/^(@[a-z0-9._-]+\/)?[a-z0-9._-]+$/.test(r.name)
        )
          refuse('resource.storage_unavailable');
        installation(r.row, identity.clientId);
      }
      for (const [key, value] of Object.entries(loaded.operations)) {
        const op = /** @type {any} */ (value);
        const input = op?.input;
        if (
          !exact(input, ['packageId', 'versionId', 'action', 'key']) ||
          !uuid(input.packageId) ||
          !uuid(input.versionId) ||
          !['install', 'enable', 'disable', 'remove'].includes(input.action) ||
          input.key !== key ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(key) ||
          op.fingerprint !== canonicalize(input)
        )
          refuse('resource.storage_unavailable');
        if (op.row) installation(op.row, identity.clientId);
      }
      loaded.records = Object.assign(Object.create(null), loaded.records);
      loaded.operations = Object.assign(Object.create(null), loaded.operations);
      state = loaded;
    }
  } catch (error) {
    await store.close();
    await gate.close();
    throw error;
  }
  const roots = options.roots ?? (() => readResourceRoots(options.trustFile));
  /** @type {NodeJS.Timeout|undefined} */ let deadline;
  /** @type {NodeJS.Timeout|undefined} */ let deadlineForce;
  let expiring = false;
  function armDeadline() {
    clearTimeout(deadline);
    const active = Object.values(state.records).filter(
      (r) => r.localEnabled || r.activationPending,
    );
    if (!active.length || closed) return;
    let until = now();
    try {
      until = Number(instant(state.revocation.list.expiresAt) / 1000000n);
      for (const r of active) {
        for (const e of [r.envelope, r.pendingEnvelope].filter(Boolean)) {
          until = Math.min(until, Number(instant(e.notAfter) / 1000000n));
          if (isRevoked(state.revocation.list, e)) until = now();
        }
      }
    } catch {
      until = now();
    }
    deadline = setTimeout(
      () => {
        expiring = true;
        // This deadline does not wait behind a download or a package-manager job.
        // Give a live host one second to persist and hot-disable, then stop it.
        deadlineForce = setTimeout(() => {
          if (!closed) host.stop();
        }, 1000);
        deadlineForce.unref();
        void Promise.all(
          active.map(
            async (r) => (await host.enable(r.name, false)).restartRequired,
          ),
        )
          .then((results) => {
            clearTimeout(deadlineForce);
            if (closed) return;
            if (results.some(Boolean)) host.stop();
            else
              void serial(async () => {
                for (const r of active) {
                  r.localEnabled = false;
                  r.activationPending = false;
                }
                expiring = false;
                await save();
              }).catch(() => {
                if (!closed) host.stop();
              });
          })
          .catch(() => {
            clearTimeout(deadlineForce);
            if (!closed) host.stop();
          });
      },
      Math.max(0, until - now()),
    );
    deadline.unref();
  }
  let closed = false,
    poisoned = false;
  let tail = Promise.resolve();
  async function save() {
    try {
      await store.commit(state);
      armDeadline();
    } catch {
      poisoned = true;
      if (Object.values(state.records).some((r) => r.localEnabled)) host.stop();
      refuse('resource.storage_unavailable');
    }
  }
  /** @template T @param {()=>Promise<T>} work */
  function serial(work) {
    const result = tail.then(async () => {
      if (closed || poisoned) refuse('resource.storage_unavailable');
      return work();
    });
    tail = result.then(
      () => {},
      () => {},
    );
    return result;
  }
  /** @param {any} list @param {Awaited<ReturnType<typeof roots>>} trusted */
  async function revocations(list, trusted) {
    state.revocation = verifyRevocations(
      list,
      identity.tenantId,
      trusted.revocationKeys,
      now(),
      state.revocation,
    );
    await save();
    return state.revocation.list;
  }
  /** @param {any} ref @param {any} download */
  async function verifyDownload(ref, download) {
    const trusted = await roots(),
      list = await revocations(download.revocations, trusted),
      envelope = download.envelope;
    if (
      isRevoked(list, envelope) ||
      ![
        'any',
        process.platform === 'win32'
          ? 'windows'
          : process.platform === 'linux'
            ? 'linux'
            : 'unsupported',
      ].includes(envelope.statement.os) ||
      ![
        'any',
        process.arch === 'arm64'
          ? 'arm64'
          : process.arch === 'x64'
            ? 'amd64'
            : 'unsupported',
      ].includes(envelope.statement.arch)
    )
      refuse();
    await gate.admit(Buffer.from(JSON.stringify(envelope)), download.archive, {
      expected: {
        tenantId: identity.tenantId,
        packageId: ref.pack.id,
        versionId: ref.version.id,
        releaseSequence: ref.version.releaseSequence,
        statement: {
          ...envelope.statement,
          purpose: 'plugin',
          name: ref.pack.name,
          version: ref.version.version,
          digest: ref.version.digest,
        },
        sizeBytes: ref.version.sizeBytes,
      },
      releaseKeys: trusted.releaseKeys,
      now: now(),
    });
    validateArchive(download.archive, {
      name: ref.pack.name,
      version: ref.version.version,
    });
    const folder = join(root, 'archives');
    await mkdir(folder, { recursive: true, mode: 0o700 });
    if ((await lstat(folder)).isSymbolicLink())
      refuse('resource.storage_unavailable');
    const file = join(folder, ref.version.digest.slice(7) + '.tgz');
    let handle;
    try {
      handle = await open(file, 'wx', 0o400);
      await handle.writeFile(download.archive);
      await handle.sync();
    } catch (error) {
      if (/** @type {any} */ (error).code !== 'EEXIST') throw error;
      if (
        (await lstat(file)).isSymbolicLink() ||
        'sha256:' +
          createHash('sha256')
            .update(await readFile(file))
            .digest('hex') !==
          ref.version.digest
      )
        refuse('resource.storage_unavailable');
    } finally {
      await handle?.close();
    }
    return file;
  }
  /** @param {any} pack @param {string} action */
  function policy(pack, action) {
    if (
      !['l1', 'l2', 'l3', 'l4'].includes(pack.level) ||
      (pack.level === 'l1' && ['disable', 'remove'].includes(action)) ||
      (pack.level === 'l2' && action === 'remove') ||
      (pack.level === 'l4' &&
        (pack.ownerId !== identity.ownerId || pack.visibility !== 'private'))
    )
      refuse('resource.level_locked');
  }
  /** @param {any} record */
  async function report(record) {
    const source = record.row;
    record.reportKey ??= randomUUID();
    await save();
    let observed;
    try {
      observed = await api.report(source, record.reportState, record.reportKey);
    } catch (error) {
      if (
        record.localEnabled &&
        [
          'common.conflict',
          'common.forbidden',
          'common.unauthenticated',
          'common.not_found',
        ].includes(/** @type {any} */ (error).code)
      ) {
        let restart = true;
        try {
          restart = (await host.enable(record.name, false)).restartRequired;
          record.localEnabled = false;
          await save();
        } finally {
          if (restart) host.stop();
        }
      }
      throw error;
    }
    record.row = observed;
    record.reportState = undefined;
    record.reportKey = undefined;
    const op = state.operations[record.operation];
    if (op) {
      op.complete = true;
      op.result = projection(record);
    }
    await save();
  }
  /** @param {any} op */
  async function perform(op) {
    if (expiring) refuse('resource.signature_invalid');
    api.check();
    if (op.restartRequired && op.boot === BOOT) return;
    const { packageId, versionId, action, key } = op.input;
    const ref = await api.reference(packageId, versionId);
    policy(ref.pack, action);
    let file, verifiedEnvelope;
    if (['install', 'enable'].includes(action)) {
      if (!ref.pack.enabled || ref.version.state !== 'published')
        refuse('resource.state_conflict');
      // Ticket issuance/consumption never retries automatically. A new attempt
      // receives fresh keys even when a selection intent is being recovered.
      const download = await api.download(ref);
      file = await verifyDownload(ref, download);
      verifiedEnvelope = download.envelope;
    }
    if (!op.row) {
      const rows = await api.list(),
        selected = rows.find((r) => r.packageId === packageId);
      op.ifMatch ??= selected?.revision ?? 0;
      await save();
      op.row = await api.select(ref, action, op.ifMatch, 'select-' + key);
      await save();
    }
    installation(op.row, identity.clientId);
    const latest = (await api.list()).find((r) => r.packageId === packageId);
    if (
      !latest ||
      latest.revision !== op.row.revision ||
      latest.versionId !== versionId ||
      latest.digest !== ref.version.digest
    )
      refuse('common.conflict');
    if (op.row.versionId !== versionId || op.row.digest !== ref.version.digest)
      refuse('common.conflict');
    const previous = state.records[packageId];
    const record = (state.records[packageId] = {
      ...previous,
      row: op.row,
      name: ref.pack.name,
      version: ref.version.version,
      operation: key,
      restartRequired: false,
    });
    if (verifiedEnvelope) record.pendingEnvelope = verifiedEnvelope;
    await save();
    try {
      api.check();
      let actual = await host.observe(record.name);
      /** @param {{restartRequired:boolean}} outcome */
      async function changed(outcome) {
        if (!outcome.restartRequired) return false;
        op.restartRequired = true;
        op.boot = BOOT;
        record.restartRequired = true;
        record.activationPending = false;
        await save();
        return true;
      }
      if (action === 'install' || action === 'enable') {
        if (
          actual?.enabled &&
          (record.localDigest !== ref.version.digest ||
            record.localVersion !== ref.version.version)
        ) {
          if (await changed(await host.enable(record.name, false))) return;
          record.localEnabled = false;
          await save();
          actual = await host.observe(record.name);
        }
        if (
          record.localDigest !== ref.version.digest ||
          record.localVersion !== ref.version.version ||
          !actual?.installed
        ) {
          api.check();
          record.activationPending = true;
          await save();
          if (!file) refuse('resource.apply_failed');
          if (await changed(await host.install(file, record.name))) return;
          record.localDigest = ref.version.digest;
          record.localVersion = ref.version.version;
          await save();
        }
        api.check();
        if (!file || !verifiedEnvelope) refuse('resource.signature_invalid');
        // Recheck the cached bytes, roots and current expiry immediately before
        // activation, even if package installation took a long time.
        const trusted = await roots();
        const list = verifyRevocations(
          state.revocation.list,
          identity.tenantId,
          trusted.revocationKeys,
          now(),
          state.revocation,
        ).list;
        if (isRevoked(list, verifiedEnvelope)) refuse();
        await gate.admit(
          Buffer.from(JSON.stringify(verifiedEnvelope)),
          await readFile(file),
          {
            expected: {
              tenantId: identity.tenantId,
              packageId,
              versionId,
              releaseSequence: ref.version.releaseSequence,
              statement: {
                ...verifiedEnvelope.statement,
                name: ref.pack.name,
                version: ref.version.version,
                digest: ref.version.digest,
              },
              sizeBytes: ref.version.sizeBytes,
            },
            releaseKeys: trusted.releaseKeys,
            now: now(),
          },
        );
        record.activationPending = true;
        await save();
        api.check();
        if (expiring) refuse('resource.signature_invalid');
        if (await changed(await host.enable(record.name, true))) return;
        actual = await host.observe(record.name);
        if (
          !actual?.installed ||
          !actual.enabled ||
          actual.version !== ref.version.version
        )
          refuse('resource.apply_failed');
        record.localEnabled = true;
        record.envelope = verifiedEnvelope;
        record.pendingEnvelope = undefined;
        record.activationPending = false;
      } else {
        if (
          actual?.enabled &&
          (await changed(await host.enable(record.name, false)))
        )
          return;
        if (
          action === 'remove' &&
          actual?.installed &&
          (await changed(await host.remove(record.name)))
        )
          return;
        actual = await host.observe(record.name);
        if (actual?.enabled || (action === 'remove' && actual?.installed))
          refuse('resource.apply_failed');
        record.localEnabled = false;
        if (action === 'remove') {
          record.localVersion = null;
          record.localDigest = null;
        }
      }
      op.restartRequired = false;
      record.restartRequired = false;
      record.reportState = 'applied';
      await save();
    } catch (error) {
      if (record.activationPending) {
        let restart = true;
        try {
          restart = (await host.enable(record.name, false)).restartRequired;
          record.localEnabled = false;
          record.activationPending = false;
        } finally {
          if (restart) host.stop();
        }
      }
      record.reportState = 'failed';
      await save();
      try {
        await report(record);
      } catch {
        /* Durable pending report is retried on a later sync. */
      }
      throw error;
    }
    await report(record);
    op.complete = true;
    op.row = record.row;
    op.result = projection(record);
    await save();
  }
  /** Never expose tickets, tokens, root keys, archive paths or manager diagnostics.
   * @param {any} r */
  function projection(r) {
    return {
      ...r.row,
      name: r.name,
      version: r.version,
      localVersion: r.localVersion ?? null,
      localEnabled: r.localEnabled === true,
      restartRequired: r.restartRequired === true,
      syncPending: !!r.reportState,
    };
  }
  /** @param {any} r */
  async function observedProjection(r) {
    const value = projection(r);
    try {
      const actual = await host.observe(r.name);
      const matches = actual?.installed && actual.version === r.localVersion;
      value.localVersion = matches ? r.localVersion : null;
      value.localEnabled = matches && actual.enabled === true;
    } catch {
      value.localVersion = null;
      value.localEnabled = false;
    }
    return value;
  }
  async function sync() {
    const remote = await api.list();
    for (const record of Object.values(state.records))
      if (record.reportState) {
        const row = remote.find((r) => r.packageId === record.row.packageId);
        if (
          row &&
          row.revision === record.row.revision &&
          row.digest === record.row.digest &&
          row.versionId === record.row.versionId
        )
          await report(record);
      }
    for (const op of Object.values(state.operations))
      if (!op.complete && op.restartRequired && op.boot !== BOOT)
        await perform(op);
    const items = [];
    for (const row of remote) {
      const record = state.records[row.packageId];
      if (record) {
        const value = await observedProjection(record);
        if (row.revision > value.revision) {
          Object.assign(value, row);
          try {
            const ref = await api.reference(row.packageId, row.versionId);
            value.version = ref.version.version;
          } catch {
            value.version = row.versionId;
          }
        }
        items.push(value);
      } else {
        let name = row.packageId,
          version = row.versionId;
        try {
          const ref = await api.reference(row.packageId, row.versionId);
          name = ref.pack.name;
          version = ref.version.version;
        } catch {
          /* Do not infer local bytes from a remote member report. */
        }
        items.push({
          ...row,
          name,
          version,
          localVersion: null,
          localEnabled: false,
          restartRequired: false,
          syncPending: false,
        });
      }
    }
    for (const [id, record] of Object.entries(state.records))
      if (!remote.some((r) => r.packageId === id))
        items.push(await observedProjection(record));
    return { identity, items, reportSource: 'member', offline: false };
  }
  async function monitor(refresh = true) {
    const enabled = Object.values(state.records).filter((r) => r.localEnabled);
    if (!enabled.length) return;
    try {
      if (refresh) {
        const trusted = await roots();
        await revocations(await api.revocations(), trusted);
      }
    } catch {
      /* A trusted list remains valid only until its original expiry. */
    }
    for (const record of enabled) {
      let stop = false;
      try {
        const trusted = await roots();
        const list = verifyRevocations(
          state.revocation?.list,
          identity.tenantId,
          trusted.revocationKeys,
          now(),
          state.revocation,
        ).list;
        signed(record.envelope, trusted.releaseKeys);
        if (
          !record.envelope ||
          isRevoked(list, record.envelope) ||
          instant(record.envelope.notAfter) <= BigInt(now()) * 1000000n
        )
          stop = true;
      } catch {
        stop = true;
      }
      if (stop) {
        let restart = true;
        try {
          restart = (await host.enable(record.name, false)).restartRequired;
          record.localEnabled = false;
          await save();
        } catch {
          /* A host unable to disable must stop before its stale code continues. */
        }
        if (restart) host.stop();
      }
    }
  }
  let watching = false,
    refreshed = 0;
  const timer = setInterval(() => {
    if (watching) return;
    watching = true;
    void serial(async () => {
      const refresh = now() - refreshed >= 30000;
      await monitor(refresh);
      if (refresh) {
        refreshed = now();
        await sync();
      }
    })
      .catch(() => {})
      .finally(() => {
        watching = false;
      });
  }, 1000);
  timer?.unref();
  armDeadline();
  return {
    identity,
    list: () =>
      serial(async () => {
        try {
          return await sync();
        } catch (error) {
          if (
            [
              'common.unauthenticated',
              'common.forbidden',
              'common.not_found',
            ].includes(/** @type {any} */ (error).code) ||
            !Object.keys(state.records).length
          )
            throw error;
          return {
            identity,
            items: await Promise.all(
              Object.values(state.records).map(observedProjection),
            ),
            reportSource: 'member',
            offline: true,
          };
        }
      }),
    /** @param {{packageId:string,versionId:string,action:string,key:string}} input */
    apply(input) {
      return serial(async () => {
        if (
          !exact(input, ['packageId', 'versionId', 'action', 'key']) ||
          !uuid(input.packageId) ||
          !uuid(input.versionId) ||
          !['install', 'enable', 'disable', 'remove'].includes(input.action) ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(input.key)
        )
          refuse('common.validation_failed');
        const fingerprint = canonicalize(input);
        let op = state.operations[input.key];
        if (op && op.fingerprint !== fingerprint) refuse('common.conflict');
        if (!op) {
          for (const [key, receipt] of Object.entries(state.operations))
            if (receipt.complete && receipt.createdAt < now() - 86400000)
              delete state.operations[key];
          if (Object.keys(state.operations).length >= 4096)
            refuse('common.rate_limited');
          op = state.operations[input.key] = {
            input: { ...input },
            fingerprint,
            createdAt: now(),
          };
          await save();
        }
        if (op.complete) return op.result;
        await perform(op);
        const record = state.records[input.packageId];
        if (!record) refuse('resource.apply_failed');
        return projection(record);
      });
    },
    monitor: () => serial(monitor),
    async close() {
      closed = true;
      clearInterval(timer);
      clearTimeout(deadline);
      clearTimeout(deadlineForce);
      await tail;
      await gate.close();
      await store.close();
    },
  };
}
