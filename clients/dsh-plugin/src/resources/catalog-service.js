import { desiredState } from './selection-state.js';
import { existsSync } from 'node:fs';
/* global FormData, Blob */
import { join, resolve } from 'node:path';
import { catalogApi, catalogIdentity } from './catalog-api.js';
import { nativePluginPolicy } from './native-policy.js';
import { pluginState } from './plugin-state.js';
import { performance } from 'node:perf_hooks';
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
import {
  diagnoseResource,
  resourceStage,
  resourceFailure,
} from './diagnostics.js';
import { validateArchive } from './archive.js';
/** @param {{runtime:any,current:()=>any,home?:string,privateSkills:any,host:any,pluginHost?:any,trustFile?:string,roots?:()=>Promise<any>,fetchImpl?:typeof fetch,desktop?:ReturnType<typeof desktopSkills>,now?:()=>number}} options */
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
  const plugins =
    !cloud && options.home
      ? pluginState({ home: options.home, who })
      : undefined;
  const now = options.now ?? (() => performance.now());
  /** @type {{packageId:string,versionId:string,phase:string,attempt?:{index:number,total:number}}|undefined} */
  let activePlugin;
  /** @template T @param {any} row @param {string} phase @param {(progress:(phase:string,attempt?:{index:number,total:number})=>void)=>Promise<T>} work */
  async function pluginProgress(row, phase, work) {
    const { kind, packageId, versionId } = row.resource.identity;
    if (kind !== 'plugin') return work(() => {});
    const progress = (
      /** @type {string} */ phase,
      /** @type {{index:number,total:number}|undefined} */ attempt = undefined,
    ) => {
      activePlugin = {
        packageId,
        versionId,
        phase,
        ...(attempt && { attempt }),
      };
    };
    progress(phase);
    try {
      return await work(progress);
    } finally {
      activePlugin = undefined;
    }
  }
  let nextRefresh = 0,
    retryAfter = 0,
    failures = 0,
    fastFollow = false,
    backoffBypassed = false;
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
        if (row.pluginFailure) continue;
        const recorded = local?.installations.find(
          (/** @type {any} */ i) =>
            i.resource.identity.packageId === row.resource.identity.packageId &&
            i.installationRevision === row.installationRevision &&
            i.selectionRevision === row.selectionRevision &&
            i.generation === row.generation &&
            canonicalize(i.resource.identity) ===
              canonicalize(row.resource.identity),
        );
        if (
          row.resource.identity.kind === 'plugin' &&
          recorded?.state !== 'failed'
        )
          continue;
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
  /** @param {any} error */
  const selectionChanged = (error) =>
    [
      'common.unauthenticated',
      'common.forbidden',
      'common.conflict',
      'common.precondition_failed',
    ].includes(error?.code);
  /** @param {any} row @param {string} state */
  const pluginReport = (row, state) => ({
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
  });
  /** @param {any} row @param {'prepare'|'host'} phase @param {{reason:string,trace_id:string}} value @param {boolean} [recorded] */
  async function recordPluginFailure(row, phase, value, recorded = false) {
    api.check();
    const diagnostic = { reason: value.reason, trace_id: value.trace_id };
    if (!recorded && plugins) {
      try {
        await plugins.writeFailure(row, phase, diagnostic);
      } catch (error) {
        // The disk can be full. The process still holds the failure for this selection.
        diagnoseResource(error, 'storage');
      }
    }
    api.check();
    if (row.state !== 'failed') {
      try {
        await api.report(pluginReport(row, 'failed'));
      } catch (error) {
        api.check();
        if (selectionChanged(error)) throw error;
        diagnoseResource(error, 'platform');
      }
    }
    row.state = 'failed';
    row.pluginFailure = diagnostic;
  }
  /** @param {any[]} rows */
  async function restorePreparationFailures(rows) {
    for (const row of rows) {
      if (
        row.resource.identity.kind !== 'plugin' ||
        desiredState(row.resource) !== 'ENABLED' ||
        row.pluginFailure
      )
        continue;
      const attempt = plugins?.readFailure(row);
      const previous =
        attempt?.phase === 'prepare' && plugins?.failure(attempt, row);
      if (previous) await recordPluginFailure(row, 'prepare', previous, true);
    }
  }
  /** @param {any[]} rows */
  const preparationCandidates = (rows) =>
    rows.filter((row) => !row.pluginFailure);
  /** Revalidate and stage the exact current server selection; never resolve latest on retry.
   * @param {any[]} rows */
  async function prepareRows(rows) {
    if (!desktop) refuse('resource.storage_unavailable');
    desktop.available();
    const downloads = [];
    const pluginNames = new Set();
    await restorePreparationFailures(rows);
    const enabled = rows.filter(
      (r) => !r.pluginFailure && desiredState(r.resource) === 'ENABLED',
    );
    const roots = enabled.length ? await trust() : undefined;
    const { verifyTrust } = await import('./unified-trust.js');
    for (const installation of enabled) {
      const plugin = installation.resource.identity.kind === 'plugin';
      try {
        const download = await pluginProgress(installation, 'downloading', () =>
            resourceStage('download', () => api.download(installation)),
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
        await pluginProgress(installation, 'verifying', () =>
          resourceStage('verification', async () =>
            verifyTrust(
              'revocation',
              { release, list: parseStrictJson(download.revocations) },
              roots,
            ),
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
      } catch (error) {
        if (!plugin || occupied(error) || selectionChanged(error)) throw error;
        await recordPluginFailure(
          installation,
          'prepare',
          diagnoseResource(error, 'verification').body,
        );
      }
    }

    const candidates = preparationCandidates(rows);
    if (!candidates.length) return candidates;
    const failedIds = new Set(
      rows
        .filter((row) => row.pluginFailure)
        .map((row) => row.resource.identity.packageId),
    );
    if (failedIds.size) {
      const host = options.pluginHost;
      const previous = desktop.read(who)?.installations ?? [];
      for (const old of previous.filter((/** @type {any} */ row) =>
        failedIds.has(row.resource.identity.packageId),
      )) {
        if (!host) refuse('resource.apply_failed');
        const name = desktop.pluginName(who, old);
        if (pluginNames.has(name)) refuse('resource.state_conflict');
        const failure = rows.find(
          (row) =>
            row.resource.identity.packageId === old.resource.identity.packageId,
        );
        if (plugins?.waiting(plugins.read(failure, name), failure)) return [];
        api.check();
        const current = await host.observe(name);
        if (current?.enabled) {
          const selected = (await api.list()).find(
            (row) =>
              row.resource.identity.packageId ===
              old.resource.identity.packageId,
          );
          if (
            !selected ||
            selected.installationRevision !== failure.installationRevision ||
            selected.selectionRevision !== failure.selectionRevision ||
            selected.generation !== failure.generation ||
            canonicalize(selected.resource.identity) !==
              canonicalize(failure.resource.identity)
          )
            refuse('common.conflict');
          api.check();
          const stopped = await host.enable(name, false);
          api.check();
          if (stopped.restartRequired) {
            await plugins?.write(failure, name, false, 'disable');
            return [];
          }
          if ((await host.observe(name))?.enabled) return [];
        }
      }
    }
    api.check();
    desktop?.acceptRevocations(
      who,
      downloads.map((d) => d.revocations),
    );
    api.check();
    desktop.prepare(who, candidates, downloads, failedIds);
    return candidates;
  }
  /** Public host observations, never the selection's 202, determine application.
   * @param {any[]} rows */
  async function syncPlugins(rows) {
    if (cloud || !desktop) return;
    const host = options.pluginHost;
    for (const row of rows.filter(
      (r) => r.resource.identity.kind === 'plugin' && !r.pluginFailure,
    )) {
      if (!host) refuse('resource.apply_failed');
      if (!plugins) refuse('resource.storage_unavailable');
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
        local.generation !== row.generation ||
        canonicalize(local.resource.identity) !==
          canonicalize(row.resource.identity)
      )
        continue;
      const enabled =
        desiredState(row.resource) === 'ENABLED' &&
        !['expired', 'revoked', 'disabled', 'removed', 'failed'].includes(
          local.state,
        );
      let state = local.state;
      /** @type {{reason:string,trace_id:string}|undefined} */ let diagnostic;
      let recordedFailure = false;
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
      /** @param {string} action @param {boolean} installed @param {()=>Promise<any>} work */
      async function operation(action, installed, work) {
        let outcome;
        try {
          outcome = await work();
        } catch (error) {
          if (!(/** @type {any} */ (error).nativePluginOperation)) throw error;
          diagnostic = diagnoseResource(error, 'plugin').body;
          return false;
        }
        api.check();
        await plugins?.write(
          row,
          /** @type {string} */ (name),
          installed,
          outcome.restartRequired ? action : null,
        );
        return !outcome.restartRequired;
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
        const proof = plugins?.read(row, name);
        const previousFailure = plugins.readFailure(row);
        diagnostic = plugins.failure(previousFailure, row);
        if (!diagnostic && row.state === 'failed') continue;
        recordedFailure = !!diagnostic;
        let ready = !diagnostic && !plugins.waiting(proof, row);
        if (
          ready &&
          (!proof?.installed ||
            !actual?.installed ||
            actual.version !== row.resource.identity.version)
        ) {
          api.check();
          if (actual?.enabled)
            ready = await operation('disable', proof?.installed === true, () =>
              host.enable(name, false),
            );
          api.check();
          if (ready)
            ready = await operation('install', true, () =>
              pluginProgress(row, 'installing_dependencies', (progress) =>
                host.install(bundle.file, name, progress),
              ),
            );
        }
        api.check();
        // Installation may take time; the independent permit and bytes must still hold.
        await checkSelection();
        if (!desktop.pluginBundle(who, row))
          refuse('resource.signature_invalid');
        actual = await host.observe(name);
        const retryInactive = !!(
          previousFailure &&
          actual?.enabled &&
          !actual.active
        );
        if (ready && retryInactive) {
          ready = await operation('disable', true, () =>
            host.enable(name, false),
          );
          await checkSelection();
        }
        if (ready && (!actual?.enabled || retryInactive))
          ready = await operation('enable', true, () =>
            pluginProgress(row, 'enabling', () => host.enable(name, true)),
          );
        actual = await host.observe(name);
        api.check();
        state =
          ready &&
          actual?.installed &&
          actual.enabled &&
          actual.active === true &&
          actual.version === row.resource.identity.version
            ? 'applied'
            : diagnostic
              ? 'failed'
              : ready
                ? 'failed'
                : 'pending_restart';
        if (state === 'failed' && !diagnostic)
          diagnostic = diagnoseResource(
            resourceFailure(
              'resource.apply_failed',
              'plugin_not_active',
              'plugin',
            ),
            'plugin',
          ).body;
      } else {
        name = /** @type {string} */ (desktop.pluginName(who, row));
        const proof = plugins?.read(row, name);
        if (
          desiredState(row.resource) !== 'ENABLED' &&
          !['expired', 'revoked'].includes(local.state)
        )
          diagnostic = plugins.failure(plugins.readFailure(row), row);
        recordedFailure = !!diagnostic;
        let ready = !diagnostic && !plugins.waiting(proof, row);
        const actual = await host.observe(name);
        if (ready && actual?.enabled)
          ready = await operation('disable', proof?.installed === true, () =>
            host.enable(name, false),
          );
        api.check();
        if (
          ready &&
          desiredState(row.resource) === 'REMOVED' &&
          actual?.installed
        )
          ready = await operation('remove', false, () => host.remove(name));
        const observed = await host.observe(name);
        if (
          diagnostic ||
          !ready ||
          observed?.enabled ||
          (desiredState(row.resource) === 'REMOVED' && observed?.installed)
        )
          state = diagnostic || ready ? 'failed' : 'pending_restart';
      }
      const report = pluginReport(row, state);
      api.check();
      await checkSelection();
      if (diagnostic) {
        await recordPluginFailure(row, 'host', diagnostic, recordedFailure);
        continue;
      }
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
      // A missing guardian disables shared writes, not read-only ownership lookup.
      const reader =
        desktop ??
        (options.home ? desktopSkills({ home: options.home }) : undefined);
      if (!reader) refuse('resource.storage_unavailable');
      names = reader.policyNames(who);
    } catch {
      // An unreadable exemption list must not disable catalog plugins and then
      // reinstall/claim them applied later in this same reconciliation.
      throw resourceFailure(
        'resource.storage_unavailable',
        'storage_unavailable',
        'storage',
      );
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
  /** @type {Map<string,{fingerprint:string,etag?:string,retryPackageId?:string,expires:number}>} */
  const submissions = new Map();
  /** @typedef {{manifest:any,archive:Uint8Array,reason:string,key:string,packageId?:string,changeNotes?:string}} PreparedSubmission */
  /** @template T @param {'package'|'upload'|'submit'} stage @param {()=>Promise<T>} action */
  async function submissionStage(stage, action) {
    try {
      return await action();
    } catch (error) {
      throw Object.assign(
        error instanceof Error ? error : new Error('common.unavailable'),
        {
          code:
            /** @type {{code?:string}} */ (error)?.code ?? 'common.unavailable',
          stage,
        },
      );
    }
  }
  /** @param {PreparedSubmission} input @param {()=>Promise<void>} [assertCurrent] */
  async function sendPrepared(input, assertCurrent) {
    if (assertCurrent) await assertCurrent();
    api.check();
    if (
      !input ||
      typeof input.key !== 'string' ||
      !/^[A-Za-z0-9_-]{1,64}$/.test(input.key) ||
      typeof input.reason !== 'string' ||
      !input.reason.trim() ||
      input.reason.length > 1024 ||
      (input.changeNotes !== undefined &&
        (typeof input.changeNotes !== 'string' ||
          !input.changeNotes.trim() ||
          input.changeNotes.length > 4096)) ||
      (input.packageId !== undefined && !uuid(input.packageId)) ||
      !(input.archive instanceof Uint8Array) ||
      !['skill', 'plugin'].includes(input.manifest?.kind) ||
      !schemaValid('package', 'Manifest', input.manifest)
    )
      refuse('common.validation_failed');
    const bytes = Buffer.from(JSON.stringify(input.manifest));
    const { kind, name, version, artifacts } = parseStrictJson(bytes, true);
    if (bytes.length > 262144 || bytes.length + input.archive.length > 67108864)
      refuse('common.validation_failed');
    const { key, reason, packageId, changeNotes } = input;
    const manifestDigest = sha(bytes);
    if (
      artifacts.length !== 1 ||
      artifacts[0].sizeBytes !== input.archive.length ||
      artifacts[0].digest !== sha(input.archive)
    )
      refuse('resource.digest_mismatch');

    const archive = new Blob([new Uint8Array(input.archive)], {
      type: 'application/gzip',
    });
    const base = '/api/resource/v1/catalog/packages';
    const fingerprint = canonicalize([
      packageId ?? null,
      manifestDigest,
      reason,
      changeNotes ?? null,
    ]);
    const time = now();
    for (const [key, attempt] of submissions) {
      if (attempt.expires <= time) submissions.delete(key);
    }
    let attempt = submissions.get(key);
    if (attempt && attempt.fingerprint !== fingerprint)
      refuse('common.conflict');
    if (!attempt) {
      if (submissions.size >= 256) refuse('common.rate_limited');
      attempt = { fingerprint, expires: time + 86400000 };
      submissions.set(key, attempt);
    }
    const targetId = packageId ?? attempt.retryPackageId;
    const pack = await submissionStage('package', () =>
      targetId
        ? api.request(base + '/' + targetId, { assertCurrent })
        : api.request(base, {
            body: { name, kind, visibility: 'tenant' },
            etag: '"0"',
            key: key + 'p',
            assertCurrent,
          }),
    );
    if (
      !uuid(pack.value?.id) ||
      (targetId !== undefined && pack.value.id !== targetId) ||
      pack.value.tenantId !== who.tenantId ||
      pack.value.ownerId !== who.ownerId ||
      pack.value.kind !== kind ||
      pack.value.name !== name
    )
      refuse('common.forbidden');
    // Attach reads the current revision again on retry. The server receipt
    // fingerprints the original If-Match, including after a lost response.
    attempt.etag ??= submissionEtag(pack);
    const form = new FormData();
    form.append(
      'metadata',
      new Blob(
        [
          JSON.stringify({
            version,
            expectedDigest: manifestDigest,
            ...(changeNotes ? { changeNotes } : {}),
          }),
        ],
        {
          type: 'application/json',
        },
      ),
    );
    form.append(
      'manifest',
      new Blob([bytes], { type: 'application/json' }),
      'auriga-resource.json',
    );
    form.append('artifacts', archive, 'payload.tgz');

    let uploaded;
    try {
      const etag = attempt.etag;
      uploaded = await submissionStage('upload', () =>
        api.request(base + '/' + pack.value.id + '/versions', {
          form,
          etag,
          key: key + 'v',
          assertCurrent,
        }),
      );
    } catch (error) {
      if (
        /** @type {{code?:string}} */ (error).code ===
        'common.precondition_failed'
      ) {
        attempt.etag = undefined;
        attempt.retryPackageId = pack.value.id;
      }
      throw error;
    }
    /** @param {any} ver @returns {string} */
    function receiptPayloadDigest(ver) {
      const list = ver?.manifest?.artifacts;
      if (
        !Array.isArray(list) ||
        list.length !== 1 ||
        list[0]?.role !== 'payload' ||
        typeof list[0]?.digest !== 'string' ||
        !list[0].digest
      )
        refuse('resource.digest_mismatch');
      return list[0].digest;
    }
    const uploadedPayloadDigest = receiptPayloadDigest(uploaded.value);
    if (
      !uuid(uploaded.value?.id) ||
      uploaded.value.packageId !== pack.value.id ||
      uploaded.value.kind !== kind ||
      uploaded.value.version !== version ||
      uploaded.value.state !== 'draft' ||
      uploaded.value.digest !== manifestDigest ||
      uploadedPayloadDigest !== artifacts[0].digest
    )
      refuse('resource.digest_mismatch');
    const review = await submissionStage('submit', () =>
      api.request(
        base +
          '/' +
          pack.value.id +
          '/versions/' +
          uploaded.value.id +
          '/submissions',
        {
          body: { reason },
          etag: submissionEtag(uploaded),
          key: key + 's',
          assertCurrent,
        },
      ),
    );
    const result = review.value;
    const submittedPayloadDigest = receiptPayloadDigest(result?.version);
    if (submittedPayloadDigest !== artifacts[0].digest)
      refuse('resource.digest_mismatch');
    if (
      !uuid(result?.reviewId) ||
      result.authorId !== who.ownerId ||
      result.status !== 'pending' ||
      result.version?.id !== uploaded.value.id ||
      result.version?.packageId !== pack.value.id ||
      result.version?.kind !== kind ||
      result.version?.version !== version ||
      result.version?.digest !== manifestDigest ||
      result.version?.state !== 'pending_review'
    )
      refuse('common.validation_failed');
    return {
      packageId: pack.value.id,
      versionId: uploaded.value.id,
      state: result.version.state,
      digest: submittedPayloadDigest,
      manifestDigest,
    };
  }
  /** @param {{value:any,etag:string|null}} response */
  function submissionEtag({ value, etag }) {
    const revision = value?.revision;
    if (!Number.isSafeInteger(revision) || revision < 0)
      refuse('common.validation_failed');
    const expected = '"' + revision + '"';
    if (etag !== null && etag !== expected) refuse('common.validation_failed');
    return expected;
  }
  return {
    who,
    cloud,
    pluginOperation() {
      api.check();
      return activePlugin ?? null;
    },
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
        await enforcePersonalPolicy();
        let hostBusy = false;
        let waiting = false,
          waitExpired = false,
          localApplying = false,
          personal,
          synchronized;
        try {
          if (!cloud && desktop) await restorePreparationFailures(items);
          let candidates = preparationCandidates(items);
          try {
            waiting = !!(!cloud && desktop?.awaitingGuardian(who, candidates));
          } catch (error) {
            if (!(/** @type {any} */ (error).guardianWaitExpired)) throw error;
            desktop?.failGuardianWait(who, candidates);
            waitExpired = true;
          }
          if (!waiting && !waitExpired) {
            try {
              await finish();
            } catch (error) {
              if (!occupied(error)) throw error;
              pendingRestart = true;
            }
            if (
              !cloud &&
              !pendingRestart &&
              desktop?.needsPreparation(who, candidates)
            ) {
              candidates = await prepareRows(items);
              if (candidates.length) await finish();
            }
            await syncPlugins(items);
            waiting = !!(!cloud && desktop?.awaitingGuardian(who, candidates));
          }
          personal = await enforcePersonalPolicy();
          if (!waiting) synchronized = await syncReports(items);
          if (!waiting && !pendingRestart)
            localApplying = !!desktop
              ?.read(who)
              ?.installations.some(
                (/** @type {any} */ r) =>
                  desiredState(r.resource) === 'ENABLED' &&
                  r.state === 'applying' &&
                  (r.resource.identity.kind !== 'plugin' ||
                    !items.some(
                      (item) =>
                        item.resource.identity.packageId ===
                          r.resource.identity.packageId &&
                        item.state !== 'applying',
                    )),
              );
        } catch (error) {
          if (/** @type {any} */ (error).hostLockOccupied !== true) throw error;
          hostBusy = true;
          synchronized = undefined;
        }
        personal ??= await enforcePersonalPolicy();
        synchronized ??= {
          items: items.map((r) =>
            desiredState(r.resource) === 'ENABLED' && !r.pluginFailure
              ? { ...r, state: 'applying' }
              : r,
          ),
          reportPending: hostBusy,
        };
        fastFollow = hostBusy || waiting || localApplying;
        nextRefresh = now() + (fastFollow ? 1000 : 60000);
        failures = 0;
        retryAfter = 0;
        return {
          identity: who,
          cloud,
          sharedAvailable: cloud || !!desktop,
          sharedUnavailableReason:
            storageReason ??
            (!cloud && !desktop ? 'guardian_missing' : undefined),
          pendingRestart,
          hostBusy,
          allowPersonalPlugins: api.personalPluginsAllowed(),
          policyCacheUnavailable: api.policyCacheUnavailable(),
          blockedPersonalPlugins: personal.blocked,
          personalPluginPolicyPendingRestart: personal.restartRequired === true,
          ...synchronized,
        };
      });
    },
    /** @param {{changed?:boolean}} [event] */
    async reconcile({ changed = false } = {}) {
      if (cloud) return;
      if (now() < retryAfter) {
        if (!changed || backoffBypassed) return;
        backoffBypassed = true;
      } else backoffBypassed = false;
      if (!changed && now() < nextRefresh) return;
      try {
        const result = await this.list();
        if (result.offline)
          throw Object.assign(new Error('offline'), {
            code: 'common.unavailable',
          });
      } catch (error) {
        retryAfter =
          now() + Math.min(300000, 60000 * 2 ** Math.min(failures++, 3));
        throw error;
      }
    },
    /** @param {{packageId:string,versionId:string,action:string,revision:number,key:string,confirmation?:string}} input @param {boolean} [deferPlugin] */
    apply(input, deferPlugin = false) {
      return serial(async () => {
        fastFollow = true;
        nextRefresh = now() + 1000;
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
        // A successful explicit action proves recovery; stale network backoff
        // must not delay this new installation's guardian follow-up.
        failures = 0;
        retryAfter = 0;
        if (cloud) return { installation: row, accepted: true, cloud: true }; // D alone downloads and materializes cloud files.
        desktop?.switchIdentity(who);
        if (
          deferPlugin &&
          identity.kind === 'plugin' &&
          ['install', 'enable'].includes(input.action)
        )
          return { installation: row, accepted: true, cloud: false };
        const rows = await api.list();
        if (
          input.action === 'remove' &&
          !rows.some((r) => r.resource.identity.packageId === input.packageId)
        )
          rows.push(row);
        if (
          !rows.some(
            (r) =>
              canonicalize(r.resource.identity) === canonicalize(identity) &&
              r.installationRevision === row.installationRevision,
          )
        )
          refuse('common.conflict');
        try {
          const candidates = await prepareRows(rows);
          const selected = rows.find(
            (item) => item.resource.identity.packageId === input.packageId,
          );
          if (selected?.pluginFailure || !candidates.length)
            return {
              installation: selected?.pluginFailure ? selected : row,
              accepted: true,
              cloud: false,
            };
          await finish();
        } catch (error) {
          if (!occupied(error)) throw error;
          if (identity.kind === 'plugin')
            throw resourceFailure(
              'resource.storage_unavailable',
              'storage_unavailable',
              'storage',
            );
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
        await enforcePersonalPolicy();
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
              row.report?.state === 'applied'
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
        if (cloud) return { state, source: observed };
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
        desktop?.acknowledge(who, report);
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
        api.check();
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
        return sendPrepared({ ...input, manifest, archive });
      });
    },
    /** Trusted host callers submit only a prepared immutable payload.
     * @param {PreparedSubmission} input @param {()=>Promise<void>} [assertCurrent] */
    submitPrepared(input, assertCurrent) {
      return serial(() => sendPrepared(input, assertCurrent));
    },
    /** List catalog packages owned by the current authenticated member.
     * @param {'plugin'|'skill'|undefined} [kind] @param {()=>Promise<void>} [assertCurrent] */
    async ownedPackages(kind, assertCurrent) {
      if (assertCurrent) await assertCurrent();
      api.check();
      const items = [];
      let cursor;
      const seen = new Set();
      for (let page = 0; page < 20; page++) {
        const result = await api.request(
          '/api/resource/v1/catalog/packages?' +
            new URLSearchParams({
              ...(kind ? { kind } : {}),
              limit: '200',
              ...(cursor ? { cursor } : {}),
            }),
          { assertCurrent },
        );
        if (
          !Array.isArray(result.value?.items) ||
          result.value.items.length > 200
        )
          refuse('common.validation_failed');
        for (const pack of result.value.items) {
          if (
            ['plugin', 'skill'].includes(pack.kind) &&
            (!kind || pack.kind === kind) &&
            pack.ownerId === who.ownerId &&
            pack.tenantId === who.tenantId &&
            uuid(pack.id) &&
            !seen.has(pack.id)
          ) {
            seen.add(pack.id);
            let publishedVersion =
              pack.latestVersion?.publishedVersion ??
              pack.market?.version ??
              null;
            let latestVersion = pack.latestVersion?.version ?? null;
            let latestState = pack.latestVersion?.state ?? null;
            let description =
              pack.latestVersion?.description ?? pack.market?.description ?? '';
            let versionsComplete = !!pack.latestVersion;
            if (!pack.latestVersion) {
              try {
                let vCursor;
                const vSeen = new Set();
                /** @type {any} */
                let newest;
                for (let vPage = 0; vPage < 20; vPage++) {
                  const verRes = await api.request(
                    `/api/resource/v1/catalog/packages/${pack.id}/versions?` +
                      new URLSearchParams({
                        limit: '50',
                        ...(vCursor ? { cursor: vCursor } : {}),
                      }),
                    { assertCurrent },
                  );
                  const vers = Array.isArray(verRes.value?.items)
                    ? verRes.value.items
                    : [];
                  if (vers.length) newest = vers.at(-1);
                  if (pack.defaultVersionId && !publishedVersion) {
                    const def = vers.find(
                      (/** @type {any} */ v) =>
                        v.id === pack.defaultVersionId &&
                        v.state === 'published',
                    );
                    if (def?.version) publishedVersion = def.version;
                  }
                  const nextV = verRes.value?.nextCursor;
                  if (!nextV) {
                    versionsComplete = true;
                    break;
                  }
                  if (vSeen.has(nextV)) break;
                  vSeen.add(nextV);
                  vCursor = nextV;
                }
                if (versionsComplete && newest) {
                  latestVersion = newest.version ?? latestVersion;
                  latestState = newest.state ?? latestState;
                  description = newest.manifest?.description ?? description;
                }
                if (pack.defaultVersionId && !publishedVersion) {
                  const defRes = await api.request(
                    `/api/resource/v1/catalog/packages/${pack.id}/versions/${pack.defaultVersionId}`,
                    { assertCurrent },
                  );
                  if (
                    defRes.value?.state === 'published' &&
                    typeof defRes.value?.version === 'string'
                  )
                    publishedVersion = defRes.value.version;
                }
              } catch {
                /* Keep package-level metadata when per-package version read fails. */
              }
            }
            items.push({
              packageId: pack.id,
              kind: pack.kind,
              name: pack.name,
              visibility: pack.visibility,
              enabled: !!pack.enabled,
              defaultVersionId: pack.defaultVersionId ?? null,
              publishedVersion,
              latestVersion,
              latestState,
              versionsComplete,
              description,
            });
          }
        }
        if (!result.value.nextCursor) return { items, complete: true };
        if (result.value.nextCursor === cursor)
          refuse('common.validation_failed');
        cursor = result.value.nextCursor;
      }
      return { items, complete: false };
    },
  };
}
