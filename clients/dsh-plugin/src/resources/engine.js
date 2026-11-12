import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { mkdir, readFile, open, lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { createFileLedgerStore } from '../network/host/store.js';
import { openResourceGate } from './gate.js';
import { createMemberApi, installation, memberIdentity } from './member-api.js';
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
/** @param {{directory:string,runtime?:any,host:Host,trustFile?:string,roots?:()=>Promise<{releaseKeys:ReadonlyMap<string,import('node:crypto').KeyObject>,revocationKeys:ReadonlyMap<string,import('node:crypto').KeyObject>}>,fetchImpl?:typeof fetch,alive?:()=>boolean,now?:()=>number,identity?:{tenantId:string,ownerId:string,clientId:string},current?:()=>any,identityReady?:()=>boolean}} options */
export async function openMemberResources(options) {
  const { host, directory, now = Date.now } = options;
  const identity = options.identity ?? memberIdentity(options.runtime);
  function activeApi() {
    const runtime = options.current ? options.current() : options.runtime;
    const client = createMemberApi({
      ...options,
      runtime,
      alive: () =>
        (!options.alive || options.alive()) &&
        (!options.current || options.current() === runtime),
    });
    if (
      client.identity.tenantId !== identity.tenantId ||
      client.identity.ownerId !== identity.ownerId ||
      client.identity.clientId !== identity.clientId
    )
      refuse('common.unauthenticated');
    return client;
  }
  function ownsScope() {
    try {
      activeApi().check();
      return true;
    } catch {
      return false;
    }
  }
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
        // Older manifests predate preservation of the original envelope bytes.
        if (r.envelope && !r.envelopeWire)
          r.envelopeWire = Buffer.from(JSON.stringify(r.envelope)).toString(
            'base64',
          );
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
  let closed = false,
    poisoned = false;
  let tail = Promise.resolve();
  let commits = Promise.resolve();
  async function save() {
    try {
      const commit = commits.then(() => store.commit(state));
      commits = commit.catch(() => {});
      await commit;
    } catch {
      poisoned = true;
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
    await gate.check(download.wire, download.archive, {
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
    await validateArchive(download.archive, {
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
      observed = await activeApi().report(
        source,
        record.reportState,
        record.reportKey,
      );
    } catch (error) {
      if (
        record.localEnabled &&
        ['common.conflict', 'common.forbidden', 'common.not_found'].includes(
          /** @type {any} */ (error).code,
        )
      ) {
        await disableRecord(record);
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
    activeApi().check();
    if (op.restartRequired && op.boot === BOOT) return;
    const { packageId, versionId, action, key } = op.input;
    const ref = await activeApi().reference(packageId, versionId);
    policy(ref.pack, action);
    let file, verifiedEnvelope, verifiedWire;
    if (['install', 'enable'].includes(action)) {
      if (!ref.pack.enabled || ref.version.state !== 'published')
        refuse('resource.state_conflict');
      // Ticket issuance/consumption never retries automatically. A new attempt
      // receives fresh keys even when a selection intent is being recovered.
      const download = await activeApi().download(ref);
      file = await verifyDownload(ref, download);
      verifiedEnvelope = download.envelope;
      verifiedWire = download.wire;
    }
    if (!op.row) {
      const rows = await activeApi().list(),
        selected = rows.find((r) => r.packageId === packageId);
      op.ifMatch ??= selected?.revision ?? 0;
      await save();
      op.row = await activeApi().select(
        ref,
        action,
        op.ifMatch,
        'select-' + key,
      );
      await save();
    }
    installation(op.row, identity.clientId);
    const latest = (await activeApi().list()).find(
      (r) => r.packageId === packageId,
    );
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
      rollback:
        previous?.rollback ??
        (previous?.localVersion
          ? {
              localVersion: previous.localVersion,
              localDigest: previous.localDigest,
              localEnabled: previous.localEnabled,
              envelope: previous.envelope,
              envelopeWire: previous.envelopeWire,
            }
          : undefined),
    });
    if (verifiedEnvelope) record.pendingEnvelope = verifiedEnvelope;
    await save();
    try {
      activeApi().check();
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
          activeApi().check();
          record.activationPending = true;
          await save();
          if (!file) refuse('resource.apply_failed');
          await verifyDownload(ref, {
            envelope: verifiedEnvelope,
            wire: verifiedWire,
            archive: await readFile(file),
            revocations: state.revocation.list,
          });
          activeApi().check();
          if (await changed(await host.install(file, record.name))) return;
          record.localDigest = ref.version.digest;
          record.localVersion = ref.version.version;
          await save();
        }
        activeApi().check();
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
        const admission = {
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
        };
        if (!verifiedWire) refuse();
        await gate.check(verifiedWire, await readFile(file), admission);
        record.activationPending = true;
        await save();
        activeApi().check();
        if (await changed(await host.enable(record.name, true))) return;
        actual = await host.observe(record.name);
        if (
          !actual?.installed ||
          !actual.enabled ||
          actual.version !== ref.version.version
        )
          refuse('resource.apply_failed');
        await gate.admit(verifiedWire, await readFile(file), {
          ...admission,
          now: now(),
        });
        record.localEnabled = true;
        record.envelope = verifiedEnvelope;
        record.envelopeWire = Buffer.from(verifiedWire).toString('base64');
        record.renewalState = 'current';
        record.blocked = false;
        record.rollback = undefined;
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
      let restored = false;
      try {
        restored = await restore(record);
      } catch {
        record.restartRequired = true;
      }
      if (!restored && record.activationPending) await disableRecord(record);
      record.activationPending = false;
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
      renewalState: r.renewalState ?? 'current',
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
    const remote = await activeApi().list();
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
            const ref = await activeApi().reference(
              row.packageId,
              row.versionId,
            );
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
          const ref = await activeApi().reference(row.packageId, row.versionId);
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
  /** @param {any} record */
  async function disableRecord(record) {
    record.renewalState = 'denied';
    record.blocked = true;
    try {
      const result = await host.enable(record.name, false);
      record.restartRequired = result.restartRequired;
      record.localEnabled = (await host.observe(record.name))?.enabled === true;
    } catch {
      record.restartRequired = true;
    }
    await save();
  }
  /** Runtime checks do not apply the admission notAfter deadline to running code. */
  async function enforceLocal() {
    for (const record of Object.values(state.records)) {
      if (!record.localVersion) continue;
      const actual = await host.observe(record.name);
      if (!actual?.enabled && !record.localEnabled) continue;
      let denied =
        record.blocked === true ||
        (record.row.level === 'l4' &&
          (options.identityReady?.() ?? true) &&
          !ownsScope());
      try {
        const trusted = await roots();
        const list = verifyRevocations(
          state.revocation?.list,
          identity.tenantId,
          trusted.revocationKeys,
          now(),
          state.revocation,
          true,
        ).list;
        signed(record.envelope, trusted.releaseKeys);
        if (
          record.envelope.tenantId !== identity.tenantId ||
          record.envelope.packageId !== record.row.packageId ||
          record.envelope.statement.digest !== record.localDigest ||
          isRevoked(list, record.envelope)
        )
          denied = true;
      } catch {
        denied = true;
      }
      if (denied) await disableRecord(record);
    }
  }
  /** @param {any} record */
  async function restore(record) {
    const old = record.rollback;
    if (!old?.localVersion || !old.envelope || !old.envelopeWire) return false;
    const trusted = await roots();
    const list = verifyRevocations(
      state.revocation?.list,
      identity.tenantId,
      trusted.revocationKeys,
      now(),
      state.revocation,
      true,
    ).list;
    if (
      isRevoked(list, old.envelope) ||
      (record.row.level === 'l4' && !ownsScope())
    )
      return false;
    const file = join(root, 'archives', old.localDigest.slice(7) + '.tgz');
    const archive = await readFile(file);
    const envelope = old.envelope;
    await gate.check(Buffer.from(old.envelopeWire, 'base64'), archive, {
      expected: {
        tenantId: identity.tenantId,
        packageId: envelope.packageId,
        versionId: envelope.versionId,
        releaseSequence: envelope.releaseSequence,
        statement: envelope.statement,
        sizeBytes: archive.length,
      },
      releaseKeys: trusted.releaseKeys,
      // Restoring the exact previously running version preserves runtime continuity.
      now: Math.min(now(), Number(instant(envelope.notAfter) / 1000000n) - 1),
    });
    await validateArchive(archive, {
      name: record.name,
      version: old.localVersion,
    });
    if ((await host.install(file, record.name)).restartRequired) return false;
    if (
      old.localEnabled &&
      (await host.enable(record.name, true)).restartRequired
    )
      return false;
    const actual = await host.observe(record.name);
    if (
      !actual?.installed ||
      actual.version !== old.localVersion ||
      actual.enabled !== old.localEnabled
    )
      return false;
    Object.assign(record, old, {
      rollback: undefined,
      activationPending: false,
      restartRequired: false,
    });
    await save();
    return true;
  }
  async function monitor(refresh = true) {
    await enforceLocal();
    if (!refresh) return;
    try {
      await revocations(await activeApi().revocations(), await roots());
    } catch {
      /* Offline runtime uses the last independently signed revocations. */
    }
    await enforceLocal();
    for (const record of Object.values(state.records)) {
      if (!record.localEnabled || record.blocked) continue;
      if (
        record.renewalState === 'current' &&
        Number(instant(record.envelope.notAfter) / 1000000n) - now() > 300000
      )
        continue;
      try {
        const ref = await activeApi().reference(
          record.row.packageId,
          record.envelope.versionId,
        );
        policy(ref.pack, 'enable');
        if (!ref.pack.enabled || ref.version.state !== 'published')
          refuse('resource.state_conflict');
        if (
          ref.version.digest !== record.localDigest ||
          ref.version.releaseSequence !== record.envelope.releaseSequence
        )
          refuse();
        const renewal = await activeApi().authorize(ref);
        await verifyDownload(ref, {
          ...renewal,
          archive: await readFile(
            join(root, 'archives', record.localDigest.slice(7) + '.tgz'),
          ),
        });
        record.envelope = renewal.envelope;
        record.envelopeWire = Buffer.from(renewal.wire).toString('base64');
        record.renewalState = 'current';
      } catch (error) {
        if (
          [
            'common.forbidden',
            'common.not_found',
            'resource.level_locked',
            'resource.state_conflict',
          ].includes(/** @type {any} */ (error).code)
        )
          await disableRecord(record);
        else record.renewalState = 'offline';
      }
      await save();
    }
    await enforceLocal();
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
  try {
    await serial(async () => {
      for (const record of Object.values(state.records))
        if (record.rollback && record.activationPending) await restore(record);
      await enforceLocal();
    });
  } catch (error) {
    clearInterval(timer);
    await gate.close();
    await store.close();
    throw error;
  }
  return {
    identity,
    list: () =>
      serial(async () => {
        activeApi().check();
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
    enforce: () => serial(enforceLocal),
    cachedRevocations: () => state.revocation?.list,
    /** @param {any} list */
    acceptRevocations(list) {
      return serial(async () => {
        const checked = verifyRevocations(
          list,
          identity.tenantId,
          (await roots()).revocationKeys,
          now(),
          state.revocation,
          true,
        );
        if (!state.revocation || checked.revision > state.revocation.revision) {
          state.revocation = checked;
          await save();
        }
      });
    },
    async close() {
      closed = true;
      clearInterval(timer);

      await tail;
      await commits;
      await gate.close();
      await store.close();
    },
  };
}
