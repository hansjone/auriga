import { qrPath } from './uac/qr.js';
import { bindLocalUacProof } from './uac/platform-proof.js';
import { networkInstallation } from '../network/installation.js';
import { createNetworkLifecycle } from '../network/lifecycle.js';
import { createBrokerNetworkProvider } from '../network/broker-provider.js';
import { createControlInstallation } from '../network/trust/installation-control.js';
import { createHostContexts } from './host-contexts.js';
import { createTeam } from './team.js';
import { createModelLifecycle } from '../ai/installation.js';
import { randomBytes, createHash } from 'node:crypto';
import { readFileSync, lstatSync } from 'node:fs';
import { join } from 'node:path';
import { desktopUpdater, desktopStartupFailure } from './desktop.js';
import { createVault } from './vault.js';
import { revokeLegacyCredentials } from './legacy-revocation.js';
import { createBroker } from './broker.js';
import { connectPackagedBroker } from './broker-package.js';
import { createEnterpriseAuth } from './enterprise.js';
import { createEnterpriseEnvironment } from './enterprise-environment.js';
import {
  memberName,
  memberAvatar,
  memberProfileBinding,
} from './member-profile.js';
import {
  connectionReason,
  DIAGNOSTIC_PATH,
  readDeviceLedger,
} from '../network/base-tools.js';

/** @param {string} code */
const refuse = (code = 'common.unavailable') => {
  throw Object.assign(new Error(code), { code });
};

/** Platform identity is inherited. Local owner admission never depends on IAM.
 * The encrypted legacy vault is used only for local state; old platform material
 * cannot be replayed. No import operation exists, so migration safely requires
 * a fresh shell login and removes only old authentication fields.
 * @param {{config:any,directory:string,ctx:any,connection?:any,enterpriseConfig?:()=>any,expectedIdentity?:{tenantId:string,ownerId:string},signal?:AbortSignal,authorizeLocal?:(context:any)=>Promise<boolean>,localUac?:any,proxyController?:any}} options */
export async function createConnectRuntime({
  config,
  directory,
  ctx,
  connection,
  enterpriseConfig,
  expectedIdentity,
  signal,
  authorizeLocal,
  localUac,
  proxyController,
}) {
  config = Object.freeze({
    ...config,
    ...(config.network ? { network: networkInstallation(config.network) } : {}),
  });
  const vault = await createVault(directory);
  const savedAuthentication = vault.snapshot();
  const legacy =
    !!savedAuthentication.credential ||
    !!savedAuthentication.endpoint ||
    (savedAuthentication.revocations?.length ?? 0) > 0 ||
    (savedAuthentication.legacyRevocations?.length ?? 0) > 0;
  if (legacy) {
    const pending = await revokeLegacyCredentials(savedAuthentication, config);
    await vault.update((s) => {
      s.credential = null;
      s.revocations = [];
      s.endpoint = null;
      s.legacyRevocations = pending;
    });
  }
  const requests = new WeakSet();
  const csrf = randomBytes(32).toString('base64url');
  /** @type {Set<()=>Promise<void>>} */ const listeners = new Set();
  /** @type {Set<(identity:any)=>Promise<void>>} */ const gates = new Set();

  /** @type {any} */ let principal;
  /** @type {string|undefined} */ let retainedCatalogGeneration;
  /** @type {{displayName?:string,avatarUrl?:string}|undefined} */ let memberProfile;
  /** @type {{reachable:boolean,lastSuccessAt:string}|undefined} */ let probeEvidence;
  /** @type {ReturnType<typeof createHostContexts>|undefined} */ let contexts;
  /** @type {ReturnType<typeof createHostContexts>|undefined} */ let enterpriseContexts;
  /** @type {ReturnType<typeof createTeam>|undefined} */ let team;
  /** @type {Awaited<ReturnType<typeof createVault>>|undefined} */ let teamVault;
  /** @type {ReturnType<typeof createVault>|undefined} */ let openingTeamVault;
  /** @type {ReturnType<typeof createTeam>|undefined} */ let startingTeam;
  /** @type {Awaited<ReturnType<typeof createVault>>|undefined} */ let startingVault;
  /** @type {Promise<any>|undefined} */ let closing;
  /** @type {Set<Promise<void>>} */ const storageDrains = new Set();
  function closeStorage(
    /** @type {Awaited<ReturnType<typeof createVault>>|undefined} */ saved,
  ) {
    if (!saved) return;
    const drain = saved.close();
    storageDrains.add(drain);
    void drain.finally(() => storageDrains.delete(drain));
    return drain;
  }
  let teamGeneration = 0,
    communication = 'idle';
  /** @type {Promise<void>|undefined} */ let teamStarting;
  let publishing = Promise.resolve();
  let isolationPending = false;
  let key = '',
    closed = false;
  /** @type {ReturnType<typeof createEnterpriseAuth>|undefined} */ let enterprise;
  /** @type {ReturnType<typeof createEnterpriseEnvironment>|undefined} */ let environment;
  /** @type {ReturnType<typeof createModelLifecycle>|undefined} */ let models;
  /** @type {ReturnType<typeof createNetworkLifecycle>|undefined} */ let network;
  const installed =
    connection ??
    (await connectPackagedBroker({ config, dataHome: directory }).catch(
      () => undefined,
    ));
  const desktop = config.platformOrigin
    ? await desktopUpdater({
        origin: config.platformOrigin,
        proxyController,
      }).catch(desktopStartupFailure)
    : undefined;
  /** @type {any} */ let latestShell;
  /** @type {{current:(()=>any)|null}} */ const shellNow = { current: null };
  const broker = await createBroker({
    signal,
    ipc: installed?.ipc,
    validate: installed?.validate ?? (() => refuse()),
    stateFactory: installed?.stateFactory,
    checkBusinessAccess() {
      if (desktop?.status().required) return refuse('desktop.update_required');
    },
    onInvalidate() {
      // Revision bumps notify here before onChange. Only an explicit signed_out
      // (or onChange identity switch) may drop the retained principal/catalog.
      queueMicrotask(() => {
        if (closed) return;
        const s = shellNow.current?.() ?? latestShell;
        if (principal && s?.state !== 'signed_out') {
          for (const listener of listeners)
            void Promise.resolve()
              .then(listener)
              .catch(() => {});
          return;
        }
        if (!principal) {
          for (const listener of listeners)
            void Promise.resolve()
              .then(listener)
              .catch(() => {});
          return;
        }
        principal = undefined;
        memberProfile = undefined;
        probeEvidence = undefined;
        retainedCatalogGeneration = undefined;
        models?.logout();
        network?.logout();
        key = '';
        contexts?.invalidate();
        enterpriseContexts?.invalidate();
        teamGeneration++;
        startingTeam?.close();
        void closeStorage(startingVault);
        team?.close();
        team = undefined;
        void closeStorage(teamVault);
        teamVault = undefined;
        communication = 'idle';
        for (const listener of listeners)
          void Promise.resolve()
            .then(listener)
            .catch(() => {});
      });
    },
    onChange(snapshot) {
      latestShell = snapshot;
      // Keep the last verified principal for every non-logout shell state so
      // marketplace/account UI stay resident until the user signs out.
      if (principal && snapshot && snapshot.state !== 'signed_out') {
        if (snapshot.state !== 'authenticated') {
          for (const listener of listeners)
            void Promise.resolve()
              .then(listener)
              .catch(() => {});
          return;
        }
      } else if (principal && snapshot?.state === 'signed_out') {
        /* fall through to clear */
      } else if (!snapshot && principal) {
        for (const listener of listeners)
          void Promise.resolve()
            .then(listener)
            .catch(() => {});
        return;
      }
      const candidate =
        snapshot?.state === 'authenticated' ? snapshot.identity : undefined;
      const next =
        candidate &&
        (!expectedIdentity ||
          (candidate.tenantId === expectedIdentity.tenantId &&
            candidate.memberId === expectedIdentity.ownerId))
          ? candidate
          : undefined;
      const fingerprint = next
        ? JSON.stringify([snapshot.shellEpoch, snapshot.revision, next])
        : '';
      if (key === fingerprint) return;
      key = fingerprint;
      memberProfile = undefined;
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
          if (!principal) retainedCatalogGeneration = undefined;
          isolationPending = false;
          if (principal) {
            models?.login();
            network?.login();
            void beginTeam();
            if (contexts) void refreshProfile(expected);
          } else {
            models?.logout();
            network?.logout();
          }
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
  shellNow.current = () => broker.state.current();
  const stopLocalProof = installed?.ipc
    ? bindLocalUacProof({ identity: localUac, broker })
    : () => {};
  models = createModelLifecycle({
    broker,
    origin: config.platformOrigin || 'https://unconfigured.invalid',
  });
  if (principal) models.login();
  const recoveringPlatform = (/** @type {any} */ s) =>
    Boolean(
      principal &&
        s &&
        ['unreachable', 'reauth_required'].includes(s.state),
    );
  // Keep presentation identity until explicit signed_out; broker.call still waits
  // for authenticated before issuing platform RPCs.
  const identity = () => {
    if (!principal) return undefined;
    if (broker.state.current()?.state === 'signed_out') return undefined;
    return principal;
  };
  // Fence by verified member/device identity, not the rotating shell authorization
  // UUID. Token self-heal remints authorization and must not bust the marketplace
  // cache or force a full catalog prefetch.
  const catalogGeneration = () => {
    const who = identity();
    if (!who) {
      retainedCatalogGeneration = undefined;
      return undefined;
    }
    retainedCatalogGeneration = createHash('sha256')
      .update('auriga-desktop-catalog-identity:')
      .update(
        JSON.stringify([
          who.tenantId,
          who.ownerId,
          who.clientId,
          who.providerId,
        ]),
      )
      .digest('hex');
    return retainedCatalogGeneration;
  };
  contexts = createHostContexts({ broker, identity, ctx });
  // A native desktop session uses the current platform login for each new tool.
  // Keep these handles separate from the existing Team and network sessions.
  const taskContexts = createHostContexts({
    broker,
    identity,
    ctx,
    pinSessionOwner: !!expectedIdentity,
  });
  enterpriseContexts = taskContexts;
  const sessionContexts = contexts;
  const controlInstallation = createControlInstallation({
    config,
    directory: join(directory, 'network'),
    getScope: () => {
      const who = identity();
      return (
        who && {
          tenant_id: who.tenantId,
          realm: config.network?.realm,
          target_id: who.clientId,
        }
      );
    },
  });
  const networkProvider = config.network
    ? createBrokerNetworkProvider({
        broker,
        contexts,
        ctx,
        issuer: config.issuer ?? config.platformOrigin,
        origin: config.platformOrigin,
        installation: config.network,
        loadInstallation: controlInstallation.prepare,
      })
    : undefined;
  network = createNetworkLifecycle({
    directory: join(directory, 'network'),
    ctx,
    installation: config.network,
    offlineControl: controlInstallation,
    defaultProvider: networkProvider,
    getAccess: async () => {
      broker.state.fence();
      return '';
    },
    authorizeLocal: async (context) =>
      !!identity() &&
      (authorizeLocal
        ? await authorizeLocal(context)
        : typeof context === 'object' &&
          context !== null &&
          requests.has(context)),
  });
  if (identity()) network.login();

  async function refreshProfile(/** @type {string} */ expected) {
    const who = identity();
    if (!who) return;
    try {
      const check = broker.state.fence();
      const result = await broker.call('profile_read', {});
      check();
      if (
        closed ||
        expected !== key ||
        who !== identity() ||
        result.body.memberId !== who.ownerId
      )
        return;
      const displayName = memberName(result.body.displayName);
      const avatarUrl = memberAvatar(result.body.avatarUrl);
      memberProfile =
        displayName || avatarUrl
          ? {
              ...(displayName ? { displayName } : {}),
              ...(avatarUrl ? { avatarUrl } : {}),
            }
          : undefined;
      for (const listener of listeners)
        void Promise.resolve()
          .then(listener)
          .catch(() => {});
    } catch {
      // Presentation failure cannot change the shell's identity authority.
    }
  }
  async function beginTeam() {
    if (
      !contexts ||
      closed ||
      !identity() ||
      team ||
      teamStarting ||
      typeof ctx.tools?.register !== 'function'
    )
      return;
    const expected = teamGeneration,
      who = identity();
    communication = 'connecting';
    const work = (async () => {
      const partition = createHash('sha256')
        .update(
          JSON.stringify([
            who.tenantId,
            who.ownerId,
            who.clientId,
            who.providerId,
          ]),
        )
        .digest('hex');
      openingTeamVault = createVault(join(directory, 'team', partition));
      let saved;
      try {
        saved = await openingTeamVault;
      } catch {
        if (expected === teamGeneration) communication = 'unavailable';
        return;
      } finally {
        openingTeamVault = undefined;
      }
      startingVault = saved;
      let candidate;
      try {
        if (closed || expected !== teamGeneration || !identity()) {
          void closeStorage(saved);
          return;
        }
        const check = broker.state.fence();
        candidate = createTeam({
          broker,
          vault: saved,
          ctx,
          getSessionContext: sessionContexts.session,
          getToolContext: sessionContexts.tool,
          finishToolContext: sessionContexts.finish,
          readStream: (id) => runtime.readStream(id),
          assertActive: check,
          label: config.deviceName ?? 'Auriga DSH',
          onState(value) {
            if (expected === teamGeneration)
              communication = value.connected ? 'ready' : 'unavailable';
          },
        });
        startingTeam = candidate;
        await candidate.start();
        check();
        if (closed || expected !== teamGeneration) {
          candidate.close();
          void closeStorage(saved);
          return;
        }
        team = candidate;
        teamVault = saved;
        communication = candidate.connected ? 'ready' : 'idle';
      } catch {
        candidate?.close();
        void closeStorage(saved);
        if (expected === teamGeneration) communication = 'unavailable';
      } finally {
        if (startingTeam === candidate) startingTeam = undefined;
        if (startingVault === saved) startingVault = undefined;
      }
    })();
    teamStarting = work;
    try {
      await work;
    } finally {
      if (teamStarting === work) teamStarting = undefined;
    }
  }
  enterprise = createEnterpriseAuth({
    snapshot: broker.state.current,
    binding: broker.state.authorization,
    ownerProof: (id) => taskContexts.ownerProof(id),
    getOwner: (id) => taskContexts.owner(id),
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
    async credentials(_id, purpose, executionContextId, execution, retained) {
      if (
        executionContextId &&
        !taskContexts.valid(executionContextId) &&
        execution
      )
        executionContextId = (await taskContexts.tool(execution, retained))
          .executionContextId;
      if (!executionContextId || !taskContexts.valid(executionContextId))
        return refuse('common.unauthenticated');
      const result = await broker.credentials(executionContextId, purpose);
      if (!taskContexts.valid(executionContextId))
        return refuse('common.unauthenticated');
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
      return { ...result, executionContextId };
    },
    onRevoke(id) {
      void taskContexts.finish(id);
    },
  });
  if (typeof ctx.inject === 'function')
    ctx.inject(['shellEnv', 'webServer'], (/** @type {any} */ child) => {
      if (
        typeof child.shellEnv?.register !== 'function' ||
        typeof child.on !== 'function' ||
        typeof child.effect !== 'function'
      )
        return;
      const toolEnvironment = createEnterpriseEnvironment({
        auth: enterprise,
        getServer: () => child.webServer,
        authorization: broker.state.authorization,
        localUacCredentials: () => localUac?.credentials?.() ?? null,
        retain: (id) => taskContexts.retain(id),
        finishContext: (id) => void taskContexts.finish(id),
        async prepare(execution) {
          try {
            const tool = await taskContexts.tool(execution);
            await team?.activateSession(tool.hostSessionId).catch(() => {});
            return tool.executionContextId;
          } catch {
            return undefined;
          }
        },
      });
      environment = toolEnvironment;
      child.shellEnv.register(toolEnvironment.contributor);
      child.on(
        'tools/execute',
        (/** @type {any} */ exec, /** @type {()=>any} */ next) =>
          toolEnvironment.run(exec, next),
      );
      child.on(
        'tools/result',
        (/** @type {any} */ exec, /** @type {any} */ result) =>
          toolEnvironment.finish(exec, result, child.jobs),
      );
      child.effect(
        () => () => toolEnvironment.dispose(),
        'auriga: enterprise task lifetime',
      );
    });
  const runtime = {
    baseTools: {
      generation: () => key,
      getState: () => ({
        loggedIn: !!identity(),
        verified: !!identity(),
        memberId: identity()?.ownerId,
        displayName: memberProfile?.displayName,
        origin: config.platformOrigin || null,
        reachable: probeEvidence?.reachable ?? null,
        lastSuccessAt: probeEvidence?.lastSuccessAt ?? null,
        network: network.status,
        remoteAvailable: communication === 'ready',
        reason: identity()
          ? 'not_ready'
          : config.platformOrigin
            ? 'unauthorized'
            : 'unconfigured',
      }),
      async readLocal() {
        const who = identity();
        if (!who) return null;
        const check = broker.state.fence();
        // The shell identity supplies only the existing local cache namespace.
        const saved = await readDeviceLedger(join(directory, 'network'), {
          claims: {
            iss: config.issuer ?? config.platformOrigin,
            tid: who.tenantId,
            did: who.clientId,
          },
        });
        check();
        return saved;
      },
      async probe(
        /** @type {string} */ path,
        /** @type {AbortSignal} */ signal,
      ) {
        signal.throwIfAborted();
        if (path !== DIAGNOSTIC_PATH) return refuse('common.validation_failed');
        const check = broker.state.fence();
        try {
          const result = await broker.call('provider_diagnostic', {});
          check();
          signal.throwIfAborted();
          probeEvidence = {
            reachable: true,
            lastSuccessAt: new Date().toISOString(),
          };
          return {
            httpStatus: result.statusCode,
            apiReady: result.body.available === true,
            reason: result.body.available === true ? null : 'unknown',
          };
        } catch (error) {
          check();
          signal.throwIfAborted();
          const status = /** @type {any} */ (error).status;
          if (!Number.isInteger(status) || status < 100 || status > 599)
            throw error;
          const reason = connectionReason(error);
          probeEvidence = {
            reachable: true,
            lastSuccessAt: new Date().toISOString(),
          };
          return {
            httpStatus: status,
            apiReady: false,
            reason: reason === 'hq_unreachable' ? 'unknown' : reason,
          };
        }
      },
    },
    config,
    broker,
    desktop,
    enterprise,
    operations: installed?.operations ?? [],
    identity,
    catalogGeneration,
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
        who = requests.has(req) ? identity() : undefined,
        recovering = recoveringPlatform(s);
      // Unreachable snapshots omit identity; rebuild a presentation binding from
      // the retained principal so account rows do not disappear during self-heal.
      const bindingSource =
        s?.state === 'authenticated'
          ? s
          : recovering && principal
            ? {
                state: 'authenticated',
                shellEpoch: s.shellEpoch,
                revision: s.revision,
                identity: {
                  tenantId: principal.tenantId,
                  memberId: principal.ownerId,
                  deviceId: principal.clientId,
                  providerId: principal.providerId,
                  ...(principal.empNo ? { empNo: principal.empNo } : {}),
                },
              }
            : s;
      return {
        available: true,
        configured: !!config.platformOrigin,
        loggedIn: !!who,
        connected: !!who && !recovering && communication === 'ready',
        recovering,
        communication,
        connection: who && team ? team.summary : null,
        platform: s?.state ?? 'unreachable',
        reason: s?.reason,
        ...(requests.has(req) && s?.inlineLogin
          ? {
              inlineLogin: {
                ...s.inlineLogin,
                ...(s.inlineLogin.step.type === 'qr'
                  ? { qr: qrPath(s.inlineLogin.step.content) }
                  : {}),
              },
            }
          : {}),
        ...(requests.has(req) && s?.deviceAttempt
          ? { deviceAttempt: s.deviceAttempt }
          : {}),
        ...(s?.loginError ? { loginError: s.loginError } : {}),
        ...(s?.localUacProof ? { localUacProof: true } : {}),
        ...(requests.has(req) &&
        s &&
        ['signed_out', 'revoked'].includes(s.state) &&
        localUac?.status?.().state === 'authenticated'
          ? { canRetryLocalUac: true }
          : {}),
        ...(requests.has(req) &&
        s?.state === 'reauth_required' &&
        !recovering &&
        localUac?.status?.().state === 'authenticated'
          ? { canRetryLocalUac: true }
          : {}),
        brokerAvailable: !!s,
        migrationRequired: legacy && !who,
        account: who
          ? {
              subject: who.ownerId,
              profileBinding: memberProfileBinding(bindingSource),
              catalogGeneration: catalogGeneration(),
              ...(s?.identity?.empNo
                ? { empNo: s.identity.empNo }
                : principal?.empNo
                  ? { empNo: principal.empNo }
                  : {}),
              ...(memberProfile ? memberProfile : {}),
            }
          : null,
        csrf: requests.has(req) ? csrf : undefined,
        ...(who && s?.uacLoginInfo ? { uacLoginInfo: s.uacLoginInfo } : {}),
        network: network.status,
        models: models?.status ?? { available: false, state: 'idle' },
        capabilities: { consent: !!who && network.consentAvailable },
      };
    },
    prepare(/** @type {any} */ req) {
      return { body: this.status(req) };
    },
    /** One verified snapshot for cross-route presentation correlation only. */
    platformIdentity() {
      const s = broker.state.current();
      if (s?.state === 'authenticated' && s.identity?.empNo)
        return {
          empNo: s.identity.empNo,
          profileBinding: memberProfileBinding(s),
        };
      if (recoveringPlatform(s) && principal?.empNo)
        return {
          empNo: principal.empNo,
          profileBinding: memberProfileBinding({
            state: 'authenticated',
            shellEpoch: s.shellEpoch,
            revision: s.revision,
            identity: {
              tenantId: principal.tenantId,
              memberId: principal.ownerId,
              deviceId: principal.clientId,
              providerId: principal.providerId,
              empNo: principal.empNo,
            },
          }),
        };
      return undefined;
    },
    /** Platform-verified employee number, used only to show local UAC association. */
    platformEmployee() {
      const s = broker.state.current();
      if (s?.state === 'authenticated') return s.identity?.empNo;
      if (recoveringPlatform(s)) return principal?.empNo;
      return undefined;
    },
    async command(
      /** @type {any} */ req,
      /** @type {string} */ action,
      /** @type {any} */ input = undefined,
    ) {
      if (!requests.has(req)) return refuse('common.forbidden');
      if (action === 'reconnect') {
        if (!broker.state.current()) await broker.state.connect();
        if (identity()) void beginTeam();
      } else await broker.state.command(action, input);
      return this.status(req);
    },
    async resetAuthentication() {
      // Trusted platform switch / rebind: drop resident identity immediately.
      principal = undefined;
      memberProfile = undefined;
      probeEvidence = undefined;
      retainedCatalogGeneration = undefined;
      models?.logout();
      network?.logout();
      key = '';
      contexts?.invalidate();
      enterpriseContexts?.invalidate();
      teamGeneration++;
      startingTeam?.close();
      void closeStorage(startingVault);
      team?.close();
      team = undefined;
      void closeStorage(teamVault);
      teamVault = undefined;
      communication = 'idle';
      broker.close();
      for (const listener of listeners)
        void Promise.resolve()
          .then(listener)
          .catch(() => {});
    },
    attachNetwork: network.attach,
    attachModels: models.attach,
    get networkStatus() {
      return network.status;
    },
    get modelStatus() {
      return models.status;
    },
    get consentAvailable() {
      return network.consentAvailable;
    },
    consentUI: network.consentUI,
    messagingUI: network.messagingUI,
    networkControl: {
      get available() {
        return controlInstallation.available;
      },
      importPackage: (/** @type {any} */ input) =>
        network.importControlOffline(input),
    },
    isReady() {
      return (
        !!identity() && communication === 'ready' && !desktop?.status().required
      );
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
      if (closed) return closing;
      closed = true;
      stopLocalProof();
      clearInterval(teamRetry);
      contexts?.close();
      taskContexts.close();
      teamGeneration++;
      startingTeam?.close();
      team?.close();
      const storage = [
        closeStorage(teamVault),
        closeStorage(startingVault),
        openingTeamVault?.then(closeStorage),
        closeStorage(vault),
        ...storageDrains,
      ];
      environment?.dispose();
      enterprise?.close();
      models?.close();
      const networkClosed = network?.close();
      networkProvider?.close();
      broker.close();
      desktop?.close();
      closing = Promise.allSettled([...storage, networkClosed]);
      return closing;
    },
  };
  if (typeof ctx.inject === 'function')
    ctx.inject(
      ['agents', 'sessionPersistence', 'workspaceRegistry'],
      (/** @type {any} */ native) => {
        if (typeof native.effect !== 'function') return;
        contexts.setHost(native);
        taskContexts.setHost(native);
        native.effect(
          () => () => {
            contexts?.setHost(undefined);
            taskContexts.setHost(undefined);
          },
          'auriga: native execution source',
        );
      },
    );
  const teamRetry = setInterval(() => {
    void beginTeam();
  }, 5000);
  teamRetry.unref();
  void beginTeam();
  void refreshProfile(key);
  return runtime;
}
