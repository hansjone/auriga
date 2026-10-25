import { CODES, HostSeamError } from '../errors.js';

/** Single isolation decision point. User deferred same-machine isolation on
 * 2026-10-25. Restore only after a new user decision and a verified host
 * isolation implementation; never use a config/env bypass. */
export function getNativeIsolationDecision() {
  return Object.freeze({
    required: false,
    isolated: false,
    mode: 'development_unisolated',
    message: '未隔离（开发阶段）',
  });
}

export function incompatible() {
  return new HostSeamError(CODES.hostIncompatible);
}

const read = [
  'sessionController.inspect',
  'sessionPersistence.stat',
  'workspaceRegistry.archivedSessionIds',
];
const live = [
  'sessionController.resolveAgent',
  'agents.get',
  'agents.isOwnedBy',
  'sessions.get',
];
const durability = [
  ...read,
  ...live,
  'sessions.flush',
  'sessionPersistence.flush',
  'sessionPersistence.open',
  'sessionPersistence.identity',
];
const requests = [...read, 'agents.get', 'sessionController.projections'];
const requirements = {
  read,
  requests,
  durability,
  create: [
    ...durability,
    'sessionController.create',
    'agentPresets.resolve',
    'sessionProjections.stateOf',
  ],
  prompt: [...durability, ...requests, 'sessionController.prompt'],
  source: [...read, ...live],
};

/** Side-effect-free startup probe. No version, package path, backend class or
 * private implementation fields. Reprobe on every operation for service unload.
 * Mutating behavior is checked against the addressed session when used, never
 * by creating a throwaway conversation at plugin startup.
 * @param {{ctx: import('./types.js').NativeContext}} options
 * @returns {import('./types.js').HostOperationAuthority} */
export function createNativeAuthority({ ctx }) {
  /** @type {Partial<Record<import('./types.js').NativeCapability, string>>} */
  const failures = {};
  /** @param {string} path */
  const present = (path) => {
    try {
      const [service, method] = path.split('.');
      const value = /** @type {any} */ (ctx)?.[service]?.[method];
      if (path === 'workspaceRegistry.archivedSessionIds')
        return (
          Array.isArray(value) && value.every((id) => typeof id === 'string')
        );
      if (path === 'sessionPersistence.identity')
        return typeof value === 'symbol';
      return typeof value === 'function';
    } catch {
      return false;
    }
  };
  /** @param {import('./types.js').NativeCapability} capability */
  const probe = (capability) => {
    const missing = [...new Set(requirements[capability])].filter(
      (p) => !present(p),
    );
    return {
      available: missing.length === 0,
      missing,
      lastFailure: failures[capability] ?? null,
    };
  };
  // Capture the startup baseline without invoking any state-changing API.
  for (const key of Object.keys(requirements))
    probe(/** @type {import('./types.js').NativeCapability} */ (key));
  return {
    async assertReady(capability = 'read') {
      const isolation = getNativeIsolationDecision();
      if (
        !probe(capability).available ||
        (isolation.required && !isolation.isolated)
      )
        throw incompatible();
    },
    reportFailure(capability, reason) {
      failures[capability] = reason;
    },
    getDiagnostics() {
      return {
        isolation: getNativeIsolationDecision(),
        capabilities: Object.fromEntries(
          Object.keys(requirements).map((key) => [
            key,
            probe(/** @type {import('./types.js').NativeCapability} */ (key)),
          ]),
        ),
      };
    },
    async assertDurableProvider(provider, id) {
      if (!probe('durability').available || provider !== ctx.sessionPersistence)
        throw incompatible();
      const identity = provider.identity;
      const snapshot = await provider.stat(id);
      if (
        provider !== ctx.sessionPersistence ||
        provider.identity !== identity ||
        snapshot?.header?.id !== id
      )
        throw incompatible();
    },
  };
}
