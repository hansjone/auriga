import { desiredState } from './selection-state.js';
import { existsSync } from 'node:fs';
/* global FormData, Blob */
import { join, resolve } from 'node:path';
import { catalogApi } from './catalog-api.js';
import { desktopSkills, sha } from './desktop-skills.js';
import { packSkill } from './skill-package.js';
import { canonicalize, parseStrictJson } from './json.js';
import { schemaValid, requireSchema } from './schema.js';
import { readResourceRoots, refuse, uuid } from './trust.js';
/** @param {{runtime:any,current:()=>any,home?:string,privateSkills:any,host:any,trustFile?:string,roots?:()=>Promise<any>,fetchImpl?:typeof fetch,desktop?:ReturnType<typeof desktopSkills>}} options */
export function catalogService(options) {
  const api = catalogApi(options),
    who = api.who,
    cloud = !!options.runtime.resourceIdentity;
  const desktop = cloud
    ? undefined
    : (options.desktop ??
      (options.home &&
      existsSync(join(options.home, 'auriga/guardian/state.json'))
        ? desktopSkills({ home: options.home })
        : undefined));
  /** @type {Promise<any>} */ let tail = Promise.resolve();
  /** @template T @param {()=>Promise<T>} work */
  function serial(work) {
    const result = tail.then(work);
    tail = result.catch(() => {});
    return result;
  }
  async function trust() {
    const roots = await (
      options.roots ?? (() => readResourceRoots(options.trustFile))
    )();
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
  let pending = true;
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
    const roots = rows.some((r) => desiredState(r.resource) === 'ENABLED')
      ? await trust()
      : undefined;
    const { verifyTrust } = await import('./unified-trust.js');
    for (const installation of rows.filter(
      (r) => desiredState(r.resource) === 'ENABLED',
    )) {
      const download = await api.download(installation),
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
      verifyTrust(
        'revocation',
        { release, list: parseStrictJson(download.revocations) },
        roots,
      );
      if (
        manifest.kind !== 'skill' ||
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
    desktop?.prepare(who, rows, downloads);
  }
  return {
    who,
    cloud,
    async list() {
      return serial(async () => {
        let pendingRestart = false;
        try {
          await finish();
        } catch (error) {
          if (!occupied(error)) throw error;
          pendingRestart = true;
        }
        const items = await api.list();
        if (
          !cloud &&
          !pendingRestart &&
          desktop?.needsPreparation(who, items)
        ) {
          await prepareRows(items);
          await finish();
        }
        if (!cloud)
          pending = !!desktop
            ?.read(who)
            ?.installations.some(
              (/** @type {any} */ row) =>
                desiredState(row.resource) === 'ENABLED' &&
                row.state === 'applying',
            );
        return {
          identity: who,
          cloud,
          sharedAvailable: cloud || !!desktop,
          pendingRestart,
          ...(await syncReports(items)),
        };
      });
    },
    async reconcile() {
      if (cloud || !pending) return;
      await finish();
      const local = desktop?.read(who);
      pending = !!local?.installations.some(
        (/** @type {any} */ row) =>
          desiredState(row.resource) === 'ENABLED' && row.state === 'applying',
      );
      if (pending && desktop?.needsPreparation(who, local.installations))
        await this.list();
    },
    /** @param {{packageId:string,versionId:string,action:string,revision:number,key:string}} input */
    apply(input) {
      return serial(async () => {
        if (
          !uuid(input.packageId) ||
          !uuid(input.versionId) ||
          !['install', 'enable', 'disable', 'remove'].includes(input.action) ||
          !Number.isSafeInteger(input.revision) ||
          input.revision < 0 ||
          !/^[A-Za-z0-9_-]{1,64}$/.test(input.key)
        )
          refuse('common.validation_failed');
        if (!cloud && !desktop) refuse('resource.storage_unavailable');
        if (!cloud) desktop?.available();
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
            version.kind !== 'skill' ||
            version.state !== 'published'
          )
            refuse('common.validation_failed');
          identity = {
            tenantId: who.tenantId,
            kind: 'skill',
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
        pending = true;
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
        const synchronized = await syncReports(await api.list());
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
