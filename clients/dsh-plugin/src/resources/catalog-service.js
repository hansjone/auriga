import { desiredState } from './selection-state.js';
import { existsSync } from 'node:fs';
/* global FormData, Blob */
import { join, resolve } from 'node:path';
import { catalogApi, catalogIdentity } from './catalog-api.js';
import { nativePluginPolicy } from './native-policy.js';
import { desktopSkills, sha } from './desktop-skills.js';
import { packSkill } from './skill-package.js';
import { canonicalize, parseStrictJson } from './json.js';
import { schemaValid, requireSchema } from './schema.js';
import {
  readResourceRoots,
  packagedResourceTrustFile,
  refuse,
  uuid,
} from './trust.js';
import { resourceStage, resourceFailure } from './diagnostics.js';
import { validateArchive } from './archive.js';
/** @param {{runtime:any,current:()=>any,home?:string,privateSkills:any,host:any,pluginHost?:any,trustFile?:string,roots?:()=>Promise<any>,fetchImpl?:typeof fetch,desktop?:ReturnType<typeof desktopSkills>}} options */
export function catalogService(options) {
  const who = catalogIdentity(options.runtime),
    cloud = !!options.runtime.resourceIdentity;
  const policy =
    !cloud && options.home
      ? nativePluginPolicy({
          home: options.home,
          who,
          origin: options.runtime.config.platformOrigin,
        })
      : undefined;
  const api = catalogApi({
    runtime: options.runtime,
    current: options.current,
    fetchImpl: options.fetchImpl,
    policy,
  });
  /** @type {ReturnType<typeof desktopSkills>|undefined} */ let desktop;
  /** @type {string|undefined} */ let storageReason;
  if (!cloud) {
    try {
      desktop =
        options.desktop ??
        (options.home &&
        (existsSync(join(options.home, 'auriga/guardian/state.json')) ||
          (packagedResourceTrustFile() &&
            existsSync(
              join(
                options.home,
                'auriga/guardian/supervisor.lock.d/owner.json',
              ),
            )))
          ? desktopSkills({ home: options.home })
          : undefined);
    } catch {
      storageReason = 'local_storage_path';
    }
  }
  const confirmations = new Map();
  const installedPlugins = new Map();
  /** @type {Promise<any>} */ let tail = Promise.resolve();
  /** @template T @param {()=>Promise<T>} work */
  function serial(work) {
    const result = tail.then(work);
    tail = result.catch(() => {});
    return result;
  }
  async function trust() {
    const roots = await resourceStage('trust_roots', () =>
      (options.roots ?? (() => readResourceRoots(options.trustFile)))(),
    );
    return Object.fromEntries(
      [
        ['release', roots.releaseKeys],
        ['revocation', roots.revocationKeys],
      ].map(([kind, map]) => [
        kind,
        [...map].map(([kid, key]) => ({
          kid,
          publicKeySpkiBase64: key
            .export({ format: 'der', type: 'spki' })
            .toString('base64'),
        })),
      ]),
    );
  }
  async function finish() {
    if (!desktop) return;
    api.check();
    desktop.switchIdentity(who);
    desktop.finish(who, () => {
      try {
        api.check();
        return true;
      } catch {
        return false;
      }
    });
  }
  /** Reconcile E's terminal receipts and successful content removals without claiming a server acknowledgement.
   * @param {any[]} items */
  async function syncReports(items) {
    let reportPending = false;
    if (!cloud) {
      const local = desktop?.read(who);
      for (const row of items) {
        if (row.resource.identity.kind === 'plugin') continue;
        const recorded = local?.installations.find(
          (/** @type {any} */ i) =>
            i.resource.identity.packageId === row.resource.identity.packageId &&
            i.installationRevision === row.installationRevision &&
            i.selectionRevision === row.selectionRevision &&
            i.generation === row.generation,
        );
        if (
          !recorded ||
          ![
            'expired',
            'revoked',
            'disabled',
            'removed',
            'superseded',
            'failed',
            'pending_restart',
          ].includes(recorded.state)
        )
          continue;
        if (
          recorded.state === 'pending_restart' &&
          !['applying', 'pending_restart'].includes(row.state)
        )
          continue;
        if (
          ['disabled', 'removed'].includes(recorded.state) &&
          !desktop?.confirmDisabled(who, row)
        )
          continue;
        const state = recorded.state;
        if (row.state !== state) {
          try {
            await api.report({
              clientId: who.hostId,
              identity: row.resource.identity,
              installationRevision: row.installationRevision,
              selectionRevision: row.selectionRevision,
              generation: row.generation,
              observedGeneration: row.generation,
              state,
              reportSource: 'member',
              observedAt: new Date().toISOString(),
              ...(['failed', 'expired', 'revoked'].includes(state)
                ? {
                    errorCode:
                      state === 'revoked'
                        ? 'resource.signature_invalid'
                        : 'resource.apply_failed',
                  }
                : {}),
            });
          } catch {
            reportPending = true;
          }
        }
        row.state = state;
      }
    }
    return { items, reportPending };
  }
  /** @param {any} error */
  const occupied = (error) =>
    ['EBUSY', 'EPERM', 'EACCES', 'ENOTEMPTY'].includes(error?.code);
  /** Revalidate and stage the exact current server selection; never resolve latest on retry.
   * @param {any[]} rows */
  async function prepareRows(rows) {
    if (!desktop) refuse('resource.storage_unavailable');
    desktop.available();
    const downloads = [];
    const pluginNames = new Set();
    const roots = rows.some((r) => desiredState(r.resource) === 'ENABLED')
      ? await trust()
      : undefined;
    const { verifyTrust } = await import('./unified-trust.js');
    for (const installation of rows.filter(
      (r) => desiredState(r.resource) === 'ENABLED',
    )) {
      const download = await resourceStage('download', () =>
          api.download(installation),
        ),
        manifest = parseStrictJson(download.manifest);
      const release = {
        protocol: 2,
        envelope: download.ref.envelope,
        rawManifest: download.manifest.toString('utf8'),
        artifactBytes: Object.fromEntries(
          download.ref.artifacts.map(
            (/** @type {any} */ a, /** @type {number} */ i) => [
              a.artifactId,
              download.artifacts[i].toString('base64'),
            ],
          ),
        ),
        expected: download.ref.identity,
        now: new Date().toISOString(),
      };
      await resourceStage('verification', async () =>
        verifyTrust(
          'revocation',
          { release, list: parseStrictJson(download.revocations) },
          roots,
        ),
      );
      if (
        !['skill', 'plugin'].includes(manifest.kind) ||
        manifest.kind !== download.ref.identity.kind ||
        download.ref.releaseSequence !==
          download.ref.envelope.releaseSequence ||
        sha(download.manifest) !== download.ref.identity.manifestDigest ||
        manifest.version !== download.ref.identity.version ||
        canonicalize(
          download.ref.artifacts
            .map((/** @type {any} */ a) => [
              a.artifactId,
              a.digest,
              a.sizeBytes,
            ])
            .sort(),
        ) !==
          canonicalize(
            manifest.artifacts
              .map((/** @type {any} */ a) => [a.id, a.digest, a.sizeBytes])
              .sort(),
          )
      )
        refuse('resource.signature_invalid');
      if (manifest.kind === 'plugin') {
        if (
          manifest.plugin.packageName === 'auriga-dsh' ||
          pluginNames.has(manifest.plugin.packageName)
        )
          refuse('resource.state_conflict');
        pluginNames.add(manifest.plugin.packageName);
        await validateArchive(download.artifacts[0], {
          name: manifest.plugin.packageName,
          version: manifest.version,
        });
      }
      const lease = download.ref.lease,
        list = parseStrictJson(download.revocations);
      if (
        lease.envelopeNotAfter !== release.envelope.notAfter ||
        lease.revocationsExpiresAt !== list.expiresAt ||
        lease.revocationRevision !== list.revision ||
        Date.parse(lease.expiresAt) !==
          Math.min(
            Date.parse(lease.envelopeNotAfter),
            Date.parse(lease.revocationsExpiresAt),
          )
      )
        refuse('resource.signature_invalid');
      downloads.push(download);
    }

    api.check();
    desktop?.acceptRevocations(
      who,
      downloads.map((d) => d.revocations),
    );
    api.check();
    desktop?.prepare(who, rows, downloads);
  }
  /** Public host observations, never the selection's 202, determine application.
   * @param {any[]} rows */
  async function syncPlugins(rows) {
    if (cloud || !desktop) return;
    const host = options.pluginHost;
    for (const row of rows.filter(
      (r) => r.resource.identity.kind === 'plugin',
    )) {
      if (!host) refuse('resource.apply_failed');
      const local = desktop
        .read(who)
        ?.installations.find(
          (/** @type {any} */ r) =>
            r.resource.identity.packageId === row.resource.identity.packageId,
        );
      if (
        !local ||
        local.installationRevision !== row.installationRevision ||
        local.selectionRevision !== row.selectionRevision ||
        local.generation !== row.generation
      )
        continue;
      const enabled =
        desiredState(row.resource) === 'ENABLED' &&
        !['expired', 'revoked', 'disabled', 'removed', 'failed'].includes(
          local.state,
        );
      let state = local.state;
      /** @type {string|undefined} */ let name;
      async function checkSelection() {
        const selected = (await api.list()).find(
          (r) =>
            r.resource.identity.packageId === row.resource.identity.packageId,
        );
        if (
          !selected ||
          selected.installationRevision !== row.installationRevision ||
          selected.selectionRevision !== row.selectionRevision ||
          selected.generation !== row.generation ||
          canonicalize(selected.resource.identity) !==
            canonicalize(row.resource.identity) ||
          desiredState(selected.resource) !== desiredState(row.resource)
        ) {
          if (name) await host.enable(name, false);
          refuse('common.conflict');
        }
      }
      api.check();
      if (enabled) {
        const bundle = desktop.pluginBundle(who, row);
        if (!bundle) continue;
        name = /** @type {string} */ (bundle.manifest.plugin.packageName);
        await validateArchive(bundle.bytes, {
          name,
          version: bundle.manifest.version,
        });
        let actual = await host.observe(name);
        const proof = canonicalize({
          identity: row.resource.identity,
          revision: row.installationRevision,
        });
        if (
          installedPlugins.get(name) !== proof ||
          !actual?.installed ||
          actual.version !== row.resource.identity.version
        ) {
          api.check();
          if (
            actual?.enabled &&
            (await host.enable(name, false)).restartRequired
          )
            continue;
          api.check();
          if ((await host.install(bundle.file, name)).restartRequired) continue;
          installedPlugins.set(name, proof);
        }
        api.check();
        // Installation may take time; the independent permit and bytes must still hold.
        await checkSelection();
        if (!desktop.pluginBundle(who, row))
          refuse('resource.signature_invalid');
        actual = await host.observe(name);
        if (!actual?.enabled && (await host.enable(name, true)).restartRequired)
          continue;
        actual = await host.observe(name);
        api.check();
        state =
          actual?.installed &&
          actual.enabled &&
          actual.active === true &&
          actual.version === row.resource.identity.version
            ? 'applied'
            : 'pending_restart';
      } else {
        name = desktop.pluginName(who, row);
        const actual = await host.observe(name);
        if (actual?.enabled && (await host.enable(name, false)).restartRequired)
          continue;
        api.check();
        if (
          desiredState(row.resource) === 'REMOVED' &&
          actual?.installed &&
          (await host.remove(name)).restartRequired
        )
          continue;
        const observed = await host.observe(name);
        if (
          observed?.enabled ||
          (desiredState(row.resource) === 'REMOVED' && observed?.installed)
        )
          continue;
        installedPlugins.delete(name);
      }
      const report = {
        clientId: who.hostId,
        identity: row.resource.identity,
        installationRevision: row.installationRevision,
        selectionRevision: row.selectionRevision,
        generation: row.generation,
        observedGeneration: row.generation,
        state,
        reportSource: 'member',
        observedAt: new Date().toISOString(),
        ...(['failed', 'expired', 'revoked'].includes(state)
          ? { errorCode: 'resource.apply_failed' }
          : {}),
      };
      api.check();
      await checkSelection();
      if (row.state !== state) await api.report(report);
      if (state === 'applied' && local.state !== 'applied')
        desktop.acknowledge(who, report);
      row.state = state;
    }
  }
  async function enforcePersonalPolicy() {
    if (cloud || api.personalPluginsAllowed())
      return { blocked: [], restartRequired: false };
    api.check();
    let names = [];
    try {
      names = desktop?.policyNames(who) ?? [];
    } catch {
      /* Invalid cache grants no native-policy exemptions. */
    }
    const result = await options.pluginHost?.enforcePersonalPolicy(
      false,
      names,
    );
    api.check();
    if (!result || !Array.isArray(result.blocked))
      refuse('resource.apply_failed');
    return result;
  }
  return {
    who,
    cloud,
    async list() {
      return serial(async () => {
        let pendingRestart = false;
        // Apply the persisted restriction before any potentially failing refresh.
        const cachedPersonal = await enforcePersonalPolicy();
        let items;
        try {
          items = await api.list();
        } catch (error) {
          if (
            !cloud &&
            !api.personalPluginsAllowed() &&
            /** @type {any} */ (error).code === 'common.unavailable'
          ) {
            return {
              identity: who,
              cloud: false,
              items: [],
              sharedAvailable: false,
              sharedUnavailableReason: 'platform_offline',
              offline: true,
              reportPending: true,
              allowPersonalPlugins: false,
              blockedPersonalPlugins: cachedPersonal.blocked,
              personalPluginPolicyPendingRestart:
                cachedPersonal.restartRequired === true,
            };
          }
          throw error;
        }
        let waiting = !cloud && desktop?.awaitingGuardian(who, items);
        if (!waiting) {
          try {
            await finish();
          } catch (error) {
            if (!occupied(error)) throw error;
            pendingRestart = true;
          }
          if (
            !cloud &&
            !pendingRestart &&
            desktop?.needsPreparation(who, items)
          ) {
            await prepareRows(items);
            await finish();
          }
          await syncPlugins(items);
          waiting = !cloud && desktop?.awaitingGuardian(who, items);
        }
        const personal = await enforcePersonalPolicy();
        return {
          identity: who,
          cloud,
          sharedAvailable: cloud || !!desktop,
          sharedUnavailableReason:
            storageReason ??
            (!cloud && !desktop ? 'guardian_missing' : undefined),
          pendingRestart,
          allowPersonalPlugins: api.personalPluginsAllowed(),
          blockedPersonalPlugins: personal.blocked,
          personalPluginPolicyPendingRestart: personal.restartRequired === true,
          ...(waiting
            ? {
                items: items.map((r) =>
                  desiredState(r.resource) === 'ENABLED'
                    ? { ...r, state: 'applying' }
                    : r,
                ),
                reportPending: false,
              }
            : await syncReports(items)),
        };
      });
    },
    async reconcile() {
      if (cloud) return;
      await this.list();
    },
    /** @param {{packageId:string,versionId:string,action:string,revision:number,key:string,confirmation?:string}} input */
    apply(input) {
      return serial(async () => {
        await enforcePersonalPolicy();
        if (
          !uuid(input.packageId) ||
          !uuid(input.versionId) ||
          !['install', 'enable', 'disable', 'remove'].includes(input.action) ||
          !Number.isSafeInteger(input.revision) ||
          input.revision < 0 ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(input.key)
        )
          refuse('common.validation_failed');
        if (!cloud && !desktop)
          throw resourceFailure(
            'resource.storage_unavailable',
            'guardian_missing',
            'storage',
          );
        if (!cloud)
          await resourceStage('storage', async () => desktop?.available());
        let identity;
        if (['disable', 'remove'].includes(input.action)) {
          const existing = (await api.list()).find(
            (r) =>
              r.resource.identity.packageId === input.packageId &&
              r.resource.identity.versionId === input.versionId,
          );
          if (!existing) refuse('common.not_found');
          if (existing.installationRevision !== input.revision)
            refuse('common.precondition_failed');
          if (existing.resource.installationMode === 'required')
            refuse('resource.installation_required');
          identity = existing.resource.identity;
        } else {
          const { value: version } = await api.request(
            '/api/resource/v1/catalog/packages/' +
              input.packageId +
              '/versions/' +
              input.versionId,
          );
          if (
            version.packageId !== input.packageId ||
            version.id !== input.versionId ||
            !['skill', 'plugin'].includes(version.kind) ||
            version.state !== 'published'
          )
            refuse('common.validation_failed');
          const name = version.manifest?.name;
          if (typeof name !== 'string') refuse('common.validation_failed');
          const conflicts = [];
          if (
            version.kind === 'skill' &&
            typeof options.privateSkills.list === 'function'
          )
            for (const item of await options.privateSkills.list())
              if (item.name === name && item.enabled)
                conflicts.push({ provider: 'private', name });
          if (
            version.kind === 'skill' &&
            typeof options.host.inspect === 'function'
          ) {
            const observed = await options.host.inspect(name);
            const ownsShared =
              observed.some(
                (/** @type {any} */ item) => item.provider === 'auriga-shared',
              ) &&
              (await api.list()).some(
                (r) => r.resource.identity.packageId === input.packageId,
              );
            for (const item of observed)
              if (item.provider !== 'auriga-shared' || !ownsShared)
                conflicts.push({
                  provider: String(item.provider ?? 'unknown'),
                  name,
                  ...(typeof item.path === 'string' ? { path: item.path } : {}),
                });
          }
          conflicts.sort((a, b) =>
            canonicalize(a).localeCompare(canonicalize(b)),
          );
          if (conflicts.length) {
            const proof = canonicalize({
              who,
              packageId: input.packageId,
              versionId: input.versionId,
              revision: input.revision,
              conflicts,
            });
            const prior =
              input.confirmation && confirmations.get(input.confirmation);
            if (!prior || prior.proof !== proof || prior.expires < Date.now()) {
              for (const [key, value] of confirmations)
                if (value.expires < Date.now()) confirmations.delete(key);
              if (confirmations.size >= 128) refuse('common.conflict');
              const confirmation = globalThis.crypto.randomUUID();
              confirmations.set(confirmation, {
                proof,
                expires: Date.now() + 300000,
              });
              return { confirmationRequired: true, confirmation, conflicts };
            }
          }
          identity = {
            tenantId: who.tenantId,
            kind: version.kind,
            packageId: input.packageId,
            versionId: input.versionId,
            version: version.version,
            manifestDigest: version.digest,
          };
        }
        requireSchema('UnifiedResourceIdentity', identity);
        const row = await api.select(
          { identity, action: input.action },
          input.revision,
          input.key,
        );
        if (cloud) return { installation: row, accepted: true, cloud: true }; // D alone downloads and materializes cloud files.
        desktop?.switchIdentity(who);
        const rows = await api.list();
        if (
          input.action === 'remove' &&
          !rows.some((r) => r.resource.identity.packageId === input.packageId)
        )
          rows.push(row);
        for (const candidate of rows)
          if (
            candidate.resource.identity.packageId === input.packageId &&
            desiredState(candidate.resource) !== 'ENABLED'
          )
            candidate.state =
              input.action === 'remove' ? 'removed' : 'disabled';
        if (
          !rows.some(
            (r) =>
              canonicalize(r.resource.identity) === canonicalize(identity) &&
              r.installationRevision === row.installationRevision,
          )
        )
          refuse('common.conflict');
        try {
          await prepareRows(rows);
          await finish();
        } catch (error) {
          if (!occupied(error)) throw error;
          await api.report({
            clientId: who.hostId,
            identity: row.resource.identity,
            installationRevision: row.installationRevision,
            selectionRevision: row.selectionRevision,
            generation: row.generation,
            observedGeneration: row.generation,
            state: 'pending_restart',
            reportSource: 'member',
            observedAt: new Date().toISOString(),
            errorCode: 'resource.apply_failed',
          });
          return {
            installation: { ...row, state: 'pending_restart' },
            accepted: true,
            cloud: false,
          };
        }
        const latest = await api.list();
        await syncPlugins(latest);
        const synchronized = await syncReports(latest);
        return {
          installation:
            synchronized.items.find(
              (r) => r.resource.identity.packageId === input.packageId,
            ) ?? row,
          accepted: true,
          cloud: false,
          reportPending: synchronized.reportPending,
        };
      });
    },
    /** @param {string} packageId @param {string} preset */
    observe(packageId, preset) {
      return serial(async () => {
        await finish();
        const rows = await api.list(),
          row = rows.find((r) => r.resource.identity.packageId === packageId);
        if (!row) refuse('common.not_found');
        if (desiredState(row.resource) !== 'ENABLED') refuse('common.conflict');
        const name = row.resource.envelope.statement.name,
          observed = await options.host.observe(name, preset);
        api.check();
        const expected = options.home
          ? join(options.home, 'auriga/skills-active/current', name, 'SKILL.md')
          : undefined;
        const matches =
          expected &&
          typeof observed.path === 'string' &&
          resolve(observed.path) === resolve(expected) &&
          observed.provider === 'auriga-shared';
        const disk = cloud ? undefined : desktop?.read(who),
          local = disk?.installations.find(
            (/** @type {any} */ i) =>
              i.resource.identity.packageId === packageId,
          );
        const byteProof = cloud ? undefined : desktop?.confirm(who, row);
        const confirmed =
          matches &&
          (cloud
            ? row.report?.reportSource === 'node' &&
              row.report?.observedGeneration === row.generation
            : local &&
              local.installationRevision === row.installationRevision &&
              ['pending_restart', 'applied'].includes(local.state) &&
              byteProof?.contentDigest === observed.contentDigest);
        const state =
          observed.state === 'observed'
            ? confirmed
              ? 'applied'
              : matches
                ? 'pending_restart'
                : 'overridden'
            : 'pending_restart';
        const report = {
          clientId: who.hostId,
          identity: row.resource.identity,
          installationRevision: row.installationRevision,
          selectionRevision: row.selectionRevision,
          generation: row.generation,
          observedGeneration: row.generation,
          state,
          reportSource: 'member',
          observedAt: new Date().toISOString(),
        };
        await api.report(report);
        if (!cloud) desktop?.acknowledge(who, report);
        return { state, source: observed };
      });
    },
    /** Only owned matching packages are offered as existing submission targets.
     * @param {string} snapshotId */
    async targets(snapshotId) {
      const frozen = await options.privateSkills.frozen(snapshotId),
        items = [];
      let cursor;
      const seen = new Set();
      for (let page = 0; page < 1000; page++) {
        const result = await api.request(
          '/api/resource/v1/catalog/packages?' +
            new URLSearchParams({
              kind: 'skill',
              limit: '200',
              ...(cursor ? { cursor } : {}),
            }),
        );
        if (
          !Array.isArray(result.value.items) ||
          result.value.items.length > 200
        )
          refuse('common.validation_failed');
        for (const pack of result.value.items) {
          if (
            pack.kind === 'skill' &&
            pack.ownerId === who.ownerId &&
            pack.tenantId === who.tenantId &&
            pack.name === frozen.name
          ) {
            if (!uuid(pack.id) || seen.has(pack.id))
              refuse('common.validation_failed');
            seen.add(pack.id);
            items.push({ id: pack.id, name: pack.name });
          }
        }
        if (!result.value.nextCursor) return { items };
        if (result.value.nextCursor === cursor)
          refuse('common.validation_failed');
        cursor = result.value.nextCursor;
      }
      refuse('common.validation_failed');
    },
    /** Explicit upload of an immutable snapshot, never of a subsequently edited private bundle.
     * @param {{snapshotId:string,version:string,license:string,author:string,reason:string,permissions:any,key:string,packageId?:string}} input */
    submit(input) {
      return serial(async () => {
        if (
          !input ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(input.key) ||
          typeof input.reason !== 'string' ||
          !input.reason.trim() ||
          input.reason.length > 4096
        )
          refuse('common.validation_failed');
        const frozen = await options.privateSkills.frozen(input.snapshotId),
          archive = packSkill(frozen.files, frozen);
        const manifest = {
          schemaVersion: 1,
          kind: 'skill',
          name: frozen.name,
          version: input.version,
          description: frozen.description,
          author: { displayName: input.author },
          license: input.license,
          permissions: input.permissions,
          runtimes: [],
          artifacts: [
            {
              id: 'payload',
              role: 'payload',
              digest: sha(archive),
              sizeBytes: archive.length,
              mediaType: 'application/gzip',
              os: 'any',
              arch: 'any',
            },
          ],
          signature: { mode: 'detached', algorithm: 'Ed25519' },
          skill: {
            entry: 'SKILL.md',
            contentProfile: 'text-only',
            dshVersionRange: '>=0.2.1-rc.3',
          },
        };
        if (!schemaValid('package', 'Manifest', manifest))
          refuse('common.validation_failed');
        const bytes = Buffer.from(JSON.stringify(manifest));
        let pack;
        if (input.packageId) {
          if (!uuid(input.packageId)) refuse('common.validation_failed');
          pack = await api.request(
            '/api/resource/v1/catalog/packages/' + input.packageId,
          );
          if (
            pack.value.ownerId !== who.ownerId ||
            pack.value.name !== frozen.name
          )
            refuse('common.forbidden');
        } else
          pack = await api.request('/api/resource/v1/catalog/packages', {
            body: { name: frozen.name, kind: 'skill', visibility: 'tenant' },
            etag: '"0"',
            key: input.key + 'p',
          });
        if (
          !uuid(pack.value.id) ||
          pack.value.kind !== 'skill' ||
          pack.value.tenantId !== who.tenantId ||
          pack.value.ownerId !== who.ownerId ||
          pack.value.name !== frozen.name
        )
          refuse('common.validation_failed');
        const form = new FormData();
        form.append(
          'metadata',
          new Blob(
            [
              JSON.stringify({
                version: input.version,
                expectedDigest: sha(bytes),
              }),
            ],
            { type: 'application/json' },
          ),
        );
        form.append(
          'manifest',
          new Blob([bytes], { type: 'application/json' }),
          'auriga-resource.json',
        );
        form.append(
          'artifacts',
          new Blob([archive], { type: 'application/gzip' }),
          'payload.tgz',
        );
        const version = await api.request(
          '/api/resource/v1/catalog/packages/' + pack.value.id + '/versions',
          {
            form,
            etag: pack.etag ?? '"' + pack.value.revision + '"',
            key: input.key + 'v',
          },
        );
        if (
          !uuid(version.value.id) ||
          version.value.packageId !== pack.value.id ||
          version.value.digest !== sha(bytes)
        )
          refuse('resource.digest_mismatch');
        const review = await api.request(
          '/api/resource/v1/catalog/packages/' +
            pack.value.id +
            '/versions/' +
            version.value.id +
            '/submissions',
          {
            body: { reason: input.reason },
            etag: version.etag ?? '"' + version.value.revision + '"',
            key: input.key + 's',
          },
        );
        if (
          !uuid(review.value.reviewId) ||
          review.value.authorId !== who.ownerId ||
          review.value.status !== 'pending' ||
          review.value.version?.id !== version.value.id ||
          review.value.version?.packageId !== pack.value.id ||
          review.value.version?.digest !== sha(bytes) ||
          review.value.version?.state !== 'pending_review'
        )
          refuse('common.validation_failed');
        return {
          packageId: pack.value.id,
          versionId: version.value.id,
          state: review.value.version.state,
          manifestDigest: sha(bytes),
        };
      });
    },
  };
}
