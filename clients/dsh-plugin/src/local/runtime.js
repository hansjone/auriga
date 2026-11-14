import { createModelLifecycle } from '../ai/installation.js';
import { randomBytes, createHash } from 'node:crypto';
import { readFileSync, lstatSync } from 'node:fs';
import { join } from 'node:path';
import { desktopUpdater } from './desktop.js';
import { createVault } from './vault.js';
import { createBroker } from './broker.js';
import { connectPackagedBroker } from './broker-package.js';
import { createEnterpriseAuth } from './enterprise.js';
import { createEnterpriseEnvironment } from './enterprise-environment.js';

/** @param {string} code */
const refuse = (code = 'common.unavailable') => {
  throw Object.assign(new Error(code), { code });
};

/** Platform identity is inherited. Local owner admission never depends on IAM.
 * The encrypted legacy vault is used only for local state; old platform material
 * cannot be replayed. No import operation exists, so migration safely requires
 * a fresh shell login and removes only old authentication fields.
 * @param {{config:any,directory:string,ctx:any,connection?:any,enterpriseConfig?:()=>any}} options */
export async function createConnectRuntime({
  config,
  directory,
  ctx,
  connection,
  enterpriseConfig,
}) {
  const vault = await createVault(directory);
  const legacy = !!vault.snapshot().credential;
  if (legacy)
    await vault.update((s) => {
      s.credential = null;
      s.revocations = [];
      s.endpoint = null;
    });
  const requests = new WeakSet();
  const csrf = randomBytes(32).toString('base64url');
  /** @type {Set<()=>Promise<void>>} */ const listeners = new Set();
  /** @type {Set<(identity:any)=>Promise<void>>} */ const gates = new Set();
  /** @type {Map<string,any>} */ const sessions = new Map();
  /** @type {Map<string,string>} */ const owners = new Map();
  /** @type {any} */ let principal;
  let publishing = Promise.resolve();
  let isolationPending = false;
  let key = '',
    closed = false;
  /** @type {ReturnType<typeof createEnterpriseAuth>|undefined} */ let enterprise;
  /** @type {ReturnType<typeof createEnterpriseEnvironment>|undefined} */ let environment;
  /** @type {ReturnType<typeof createModelLifecycle>|undefined} */ let models;
  const installed =
    connection ?? (await connectPackagedBroker().catch(() => undefined));
  const broker = await createBroker({
    ipc: installed?.ipc,
    validate: installed?.validate ?? (() => refuse()),
    onInvalidate() {
      principal = undefined;
      models?.logout();
      key = '';
      sessions.clear();
      enterprise?.revokeAll();
      for (const listener of listeners)
        void Promise.resolve()
          .then(listener)
          .catch(() => {});
    },
    onChange(snapshot) {
      const next =
        snapshot?.state === 'authenticated' ? snapshot.identity : undefined;
      const fingerprint = next
        ? JSON.stringify([snapshot.shellEpoch, snapshot.revision, next])
        : '';
      if (key === fingerprint) return;
      key = fingerprint;
      isolationPending = true;
      const expected = fingerprint;
      publishing = publishing
        .catch(() => {})
        .then(async () => {
          const view = next
            ? {
                tenantId: next.tenantId,
                ownerId: next.memberId,
                hostId: next.deviceId,
              }
            : undefined;
          for (const gate of gates) await gate(view);
          if (closed || expected !== key) return;
          principal = next
            ? Object.freeze({
                tenantId: next.tenantId,
                ownerId: next.memberId,
                clientId: next.deviceId,
                providerId: next.providerId,
                empNo: next.empNo,
              })
            : undefined;
          isolationPending = false;
          if (principal) models?.login();
          else models?.logout();
          for (const listener of listeners)
            void Promise.resolve()
              .then(listener)
              .catch(() => {});
        })
        .catch(() => {
          principal = undefined;
          isolationPending = true;
        });
    },
  });
  models = createModelLifecycle({
    broker,
    origin: config.platformOrigin || 'https://unconfigured.invalid',
  });
  if (principal) models.login();
  const desktop = config.platformOrigin
    ? await desktopUpdater({ origin: config.platformOrigin }).catch(
        () => undefined,
      )
    : undefined;
  const identity = () =>
    broker.state.current()?.state === 'authenticated' ? principal : undefined;
  async function session(/** @type {string} */ id) {
    const who = identity();
    if (!who || closed || !ctx.sessions?.get(id))
      return refuse('common.unauthenticated');
    const owner = owners.get(id);
    if (owner && owner !== who.ownerId) return refuse('common.forbidden');
    owners.set(id, who.ownerId);
    let handle = sessions.get(id);
    if (!handle || Date.parse(handle.expiresAt) <= Date.now()) {
      handle = await broker.register({
        kind: 'session',
        hostSessionId: id,
        expiresAt: new Date(Date.now() + 600000).toISOString(),
      });
      sessions.set(id, handle);
    }
    return handle;
  }
  enterprise = createEnterpriseAuth({
    snapshot: broker.state.current,
    getOwner: (id) => owners.get(id) ?? null,
    config:
      enterpriseConfig ??
      (() => {
        const path = join(directory, 'enterprise.json');
        try {
          const stat = lstatSync(path);
          if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 65536)
            throw new Error();
          return JSON.parse(readFileSync(path, 'utf8'));
        } catch (error) {
          if (/** @type {any} */ (error).code === 'ENOENT') return {};
          return { allowRawAgentToken: false, outboundAllowedHosts: [] };
        }
      }),
    async credentials(_id, purpose, executionContextId) {
      if (!executionContextId) return refuse('common.unauthenticated');
      const result = await broker.credentials(executionContextId, purpose);
      const s = broker.state.current(),
        binding = result.binding;
      if (
        !s?.identity ||
        !binding ||
        ['tenantId', 'memberId', 'deviceId', 'providerId'].some(
          (k) => binding[k] !== s.identity[k],
        ) ||
        Date.parse(binding.expiresAt) <= Date.now() ||
        !Number.isSafeInteger(result.credentialVersion) ||
        result.credentialVersion < 1 ||
        (binding.credentialVersion !== undefined &&
          binding.credentialVersion !== result.credentialVersion) ||
        (s.identity.uacSessionBinding &&
          ['sourceSessionId', 'authGeneration', 'providerId'].some(
            (k) => binding[k] !== s.identity.uacSessionBinding[k],
          ))
      )
        return refuse('common.conflict');
      return result;
    },
    onRevoke(id) {
      void broker.revoke(id).catch(() => {});
    },
  });
  if (typeof ctx.inject === 'function')
    ctx.inject(['shellEnv', 'webServer'], (/** @type {any} */ child) => {
      environment = createEnterpriseEnvironment({
        auth: enterprise,
        getServer: () => child.webServer,
        async prepare(execution) {
          const id =
            execution?.agent?.session?.header?.id ??
            execution?.agent?.session?.id;
          if (
            typeof id !== 'string' ||
            !execution?.callId ||
            !execution?.rootCallId ||
            execution.signal?.aborted
          )
            return undefined;
          const parent = await session(id);
          const handle = await broker.register({
            kind: 'tool',
            hostSessionId: id,
            toolCallId: execution.callId,
            rootCallId: execution.rootCallId,
            parentExecutionContextId: parent.executionContextId,
            expiresAt: new Date(Date.now() + 600000).toISOString(),
          });
          return handle.executionContextId;
        },
      });
      child.shellEnv.register(environment.contributor);
      child.on(
        'tools/result',
        (/** @type {any} */ exec, /** @type {any} */ result) =>
          environment?.finish(exec, result, child.jobs),
      );
      child.effect(
        () => () => environment?.dispose(),
        'auriga: enterprise task lifetime',
      );
    });
  const runtime = {
    baseTools: {
      generation: () => key,
      getState: () => ({
        loggedIn: !!identity(),
        verified: false,
        reason: identity() ? 'not_ready' : 'unconfigured',
      }),
      async readLocal() {
        return null;
      },
      async probe(
        /** @type {string} */ _path,
        /** @type {AbortSignal} */ signal,
      ) {
        signal.throwIfAborted();
        const check = broker.state.fence();
        const result = await broker.call('provider_diagnostic', {});
        check();
        signal.throwIfAborted();
        return result.body;
      },
    },
    config,
    broker,
    desktop,
    enterprise,
    operations: installed?.operations ?? [],
    identity,
    // Compatibility for resource lifecycle subscriptions, with no credential store.
    vault: {
      snapshot: () => ({
        resourceIsolationPending:
          isolationPending || vault.snapshot().resourceIsolationPending,
      }),
      subscribeResourceIdentity(/** @type {()=>Promise<void>} */ listener) {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      subscribeResourceIdentityGate(
        /** @type {(identity:any)=>Promise<void>} */ gate,
      ) {
        gates.add(gate);
        return () => gates.delete(gate);
      },
    },
    nativeRequest(/** @type {any} */ peer) {
      if (peer?.principal?.kind !== 'owner') return refuse('common.forbidden');
      const req = { headers: {} };
      requests.add(req);
      return req;
    },
    bindNativeRequest(/** @type {any} */ peer, /** @type {any} */ req) {
      if (peer?.principal?.kind !== 'owner') return null;
      requests.add(req);
      return this.resolve(req);
    },
    resolve(/** @type {any} */ req) {
      return requests.has(req) ? identity() : undefined;
    },
    checkCsrf(/** @type {any} */ req) {
      return requests.has(req) && req.headers?.['x-auriga-csrf'] === csrf;
    },
    status(/** @type {any} */ req) {
      const s = broker.state.current(),
        who = requests.has(req) ? identity() : undefined;
      return {
        available: true,
        configured: !!config.platformOrigin,
        loggedIn: !!who,
        connected: false,
        communication: 'idle',
        platform: s?.state ?? 'unreachable',
        reason: s?.reason,
        brokerAvailable: !!s,
        migrationRequired: legacy && !who,
        account: who ? { subject: who.ownerId } : null,
        csrf: requests.has(req) ? csrf : undefined,
        ...(who && s.uacLoginInfo ? { uacLoginInfo: s.uacLoginInfo } : {}),
        network: { available: false, connected: false, state: 'idle' },
        models: { available: false, state: 'idle' },
        capabilities: { consent: false },
      };
    },
    prepare(/** @type {any} */ req) {
      return { body: this.status(req) };
    },
    async command(/** @type {any} */ req, /** @type {string} */ action) {
      if (!requests.has(req)) return refuse('common.forbidden');
      await broker.state.command(action);
      return this.status(req);
    },
    async resetAuthentication() {
      broker.state.disconnect();
    },
    attachNetwork() {
      return () => {};
    },
    attachModels: models.attach,
    get networkStatus() {
      return { available: false, connected: false, state: 'idle' };
    },
    get modelStatus() {
      return models.status;
    },
    get consentAvailable() {
      return false;
    },
    consentUI() {
      return refuse();
    },
    messagingUI() {
      return refuse();
    },
    isReady() {
      return !!identity();
    },
    async upload(/** @type {Buffer} */ bytes, /** @type {string} */ mediaType) {
      const opened = (
        await broker.call('upload_open', {
          sizeBytes: bytes.length,
          sha256: createHash('sha256').update(bytes).digest('hex'),
          mediaType,
        })
      ).body;
      // One chunk at a time; every chunk must be durably acknowledged.
      let ack = -1,
        error = false;
      /** @type {(()=>void)|undefined} */ let acknowledged;
      await broker.subscribeStream(opened.streamId, (event) => {
        if (!event || event.type === 'error') error = true;
        else if (event.type === 'upload_ack') ack = event.ackSequence;
        acknowledged?.();
      });
      try {
        for (
          let offset = 0, sequence = 0;
          offset < bytes.length;
          offset += 65536, sequence++
        ) {
          await broker.streamCommand({
            streamId: opened.streamId,
            action: 'upload_chunk',
            sequence,
            data: bytes.subarray(offset, offset + 65536).toString('base64'),
          });
          if (!error && ack !== sequence)
            await new Promise((resolve, reject) => {
              const timer = setTimeout(
                () => reject(new Error('common.unavailable')),
                30000,
              );
              acknowledged = () => {
                if (error || ack !== sequence) {
                  clearTimeout(timer);
                  reject(new Error('common.conflict'));
                } else {
                  clearTimeout(timer);
                  resolve(undefined);
                }
                acknowledged = undefined;
              };
            });
          if (error || ack !== sequence) return refuse();
        }
        return (
          await broker.call('upload_finish', { uploadId: opened.uploadId })
        ).body;
      } catch (error) {
        await broker
          .streamCommand({ streamId: opened.streamId, action: 'cancel' })
          .catch(() => {});
        throw error;
      }
    },
    async readStream(/** @type {string} */ streamId) {
      const check = broker.state.fence();
      return new Promise((resolve, reject) => {
        const parts = /** @type {Buffer[]} */ ([]);
        let sequence = 0,
          bytes = 0;
        const timer = setTimeout(() => {
          reject(new Error('common.unavailable'));
          void broker
            .streamCommand({ streamId, action: 'cancel' })
            .catch(() => {});
        }, 30000);
        void broker
          .subscribeStream(streamId, (event) => {
            try {
              check();
              if (!event || event.sequence !== sequence++)
                throw new Error('common.conflict');
              if (event.type === 'end') {
                clearTimeout(timer);
                resolve(Buffer.concat(parts));
                return;
              }
              if (event.type !== 'data') throw new Error('common.unavailable');
              const data = Buffer.from(event.data, 'base64');
              if (
                data.toString('base64') !== event.data ||
                data.length > 65536 ||
                (bytes += data.length) > 67108864
              )
                throw new Error('common.validation_failed');
              parts.push(data);
              void broker
                .streamCommand({
                  streamId,
                  action: 'credit',
                  creditBytes: data.length,
                })
                .catch(reject);
            } catch (error) {
              clearTimeout(timer);
              reject(error);
              void broker
                .streamCommand({ streamId, action: 'cancel' })
                .catch(() => {});
            }
          })
          .then(() =>
            broker.streamCommand({
              streamId,
              action: 'credit',
              creditBytes: 1048576,
            }),
          )
          .catch((error) => {
            clearTimeout(timer);
            reject(error);
          });
      });
    },
    close() {
      closed = true;
      environment?.dispose();
      enterprise?.close();
      models?.close();
      broker.close();
      desktop?.close();
      vault.close();
    },
  };
  return runtime;
}
