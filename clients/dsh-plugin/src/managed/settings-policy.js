/** Managed owner's editable fields in the public DSH settings namespace.
 * Unknown namespaces/fields stay platform-owned until explicitly reviewed.
 */
const PERSONAL_FIELDS = new Map([
  ['locale', ['preference']],
  ['ui-theme', ['preference', 'fontSize']],
  ['ui-settings', ['enabled']],
  ['ui-settings-general', ['welcomeNoticeVersion']],
  [
    'ui-settings-account',
    [
      'version',
      'step',
      'purpose',
      'process',
      'completion',
      'usage',
      'developerTools',
    ],
  ],
  ['ui-conversation', ['busyEnter']],
  ['ui-chat', ['transcriptView', 'performanceUsage', 'linkOpening']],
  ['agent-default-model', ['provider', 'model', 'reasoningEffort']],
  ['agent-preset-registry', ['selectedDefault']],
  ['subagent-model-selection-settings', ['enabled', 'allowedModels']],
]);
const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype']);
const platformRef = (/** @type {unknown} */ ref) =>
  typeof ref === 'string' && /^auriga_/i.test(ref);

function refuse() {
  const error = new Error(
    '此设置由 Auriga 平台托管，不能修改；个人偏好、个人模型和 Agent 预设选择仍可修改。 / This setting is managed by Auriga; personal preferences, personal models and Agent preset selection remain editable.',
  );
  return Object.assign(error, { code: 'common.forbidden' });
}

/** @param {unknown} value @returns {value is Record<string, any>} */
function object(value) {
  return (
    value !== null &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    [Object.prototype, null].includes(Object.getPrototypeOf(value))
  );
}

/** Reject reserved credential references and prototype paths at any depth.
 * Values otherwise remain subject to the Host's JSON/schema validation.
 * @param {any} value
 * @param {Set<object>} [seen]
 */
function safeValue(value, seen = new Set()) {
  if (!value || typeof value !== 'object') return;
  if (seen.has(value)) throw refuse();
  seen.add(value);
  for (const [key, child] of Object.entries(value)) {
    if (UNSAFE_KEYS.has(key) || (key === 'apiKeyEnv' && platformRef(child)))
      throw refuse();
    safeValue(child, seen);
  }
  seen.delete(value);
}

/** @param {unknown} value */
function personalProviders(value) {
  if (!object(value) || Object.keys(value).some((key) => UNSAFE_KEYS.has(key)))
    throw refuse();
  if (Object.hasOwn(value, 'auriga-managed'))
    managedProvider(value['auriga-managed']);
}

const MODEL_FIELDS = new Set([
  'id',
  'name',
  'contextWindow',
  'maxTokens',
  'input',
  'reasoningEfforts',
]);
/** Only model presentation/capabilities are user editable. Route identity,
 * credential transport and storage compatibility remain platform supplied.
 * @param {any} value */
function managedModel(value) {
  if (
    !object(value) ||
    Object.keys(value).some((key) => !MODEL_FIELDS.has(key))
  )
    throw refuse();
}
/** @param {any} value */
function managedProvider(value) {
  if (
    !object(value) ||
    Object.keys(value).some((key) => !['models', 'displayName'].includes(key))
  )
    throw refuse();
  if (Object.hasOwn(value, 'models')) {
    if (!Array.isArray(value.models) || value.models.length > 256)
      throw refuse();
    value.models.forEach(managedModel);
  }
}
/** @param {any} op */
function managedOperation(op) {
  const path = op.path.slice(2);
  if (path[0] === 'displayName' && path.length === 1) return;
  if (path[0] !== 'models') throw refuse();
  if (path.length === 1) {
    if (op.op === 'set') managedProvider({ models: op.value });
    return;
  }
  if (!/^(0|[1-9][0-9]*)$/.test(path[1]) || Number(path[1]) > 255)
    throw refuse();
  if (path.length === 2) {
    if (op.op === 'set') managedModel(op.value);
    return;
  }
  if (!MODEL_FIELDS.has(path[2])) throw refuse();
  if (path.length > 3 && !['input', 'reasoningEfforts'].includes(path[2]))
    throw refuse();
}

/** Validate an entire write before delegating once; never partially apply ops.
 * @param {string} method @param {string} ns @param {any} input
 */
function authorize(method, ns, input) {
  const fields = PERSONAL_FIELDS.get(ns);
  if (!fields && ns !== 'llm-pi-ai') throw refuse();
  safeValue(input);
  if (method !== 'mutate') {
    if (!object(input)) throw refuse();
    if (ns === 'llm-pi-ai') {
      // replace resets all live fields, including the managed route. Only
      // merge-update of explicitly personal providers is safe here.
      if (
        method === 'replace' ||
        Object.keys(input).some((key) => key !== 'providers')
      )
        throw refuse();
      if (Object.hasOwn(input, 'providers')) personalProviders(input.providers);
    } else if (Object.keys(input).some((key) => !fields?.includes(key)))
      throw refuse();
    return;
  }
  if (!Array.isArray(input)) throw refuse();
  for (const op of input) {
    if (
      !object(op) ||
      !['set', 'unset'].includes(op.op) ||
      !Array.isArray(op.path) ||
      op.path.some(
        (/** @type {unknown} */ key) =>
          typeof key !== 'string' || UNSAFE_KEYS.has(key),
      )
    )
      throw refuse();
    if (ns === 'llm-pi-ai') {
      if (op.path.length < 2 || op.path[0] !== 'providers') throw refuse();
      if (op.path[1] === 'auriga-managed') managedOperation(op);
      if (
        op.op === 'set' &&
        op.path.at(-1) === 'apiKeyEnv' &&
        platformRef(op.value)
      )
        throw refuse();
    } else if (op.path.length) {
      if (!fields?.includes(op.path[0])) throw refuse();
    } else if (
      op.op === 'set' &&
      (!object(op.value) ||
        Object.keys(op.value).some((key) => !fields?.includes(key)))
    )
      throw refuse();
  }
}

/** Wrap only documented service methods, shared by HTTP and WebSocket RPC.
 * @param {any} service
 * @param {string[]} methods
 * @param {(method: string, args: any[]) => void} check
 */
function guard(service, methods, check) {
  if (methods.some((method) => typeof service?.[method] !== 'function'))
    throw new Error('auriga-dsh: managed settings policy unavailable');
  const restores = methods.map((method) => {
    const original = service[method];
    /** @param {any[]} args */
    const wrapped = async (...args) => {
      check(method, args);
      return original.apply(service, args);
    };
    service[method] = wrapped;
    return () => {
      if (service[method] === wrapped) service[method] = original;
    };
  });
  return () => restores.forEach((restore) => restore());
}

/** @param {any} settings */
export function guardManagedSettings(settings) {
  return guard(
    settings,
    ['update', 'replace', 'mutate', 'prepareDocument'],
    (method, args) => {
      // Native document editing bypasses per-field authorization altogether.
      if (method === 'prepareDocument') throw refuse();
      authorize(method, args[0], args[1]);
    },
  );
}

/** Managed device/model credentials are file-backed, outside this store.
 * Reserve their reference prefix as well; personal provider keys still work.
 * @param {any} credentials
 */
export function guardManagedCredentials(credentials) {
  return guard(credentials, ['set', 'unset'], (_method, args) => {
    if (platformRef(args[0])) throw refuse();
  });
}

/** Install only in managed composition; Cordis owns disposal/rebinding.
 * @param {any} ctx
 */
export function installManagedSettingsPolicy(ctx) {
  ctx.inject?.(['settings'], (/** @type {any} */ scope) => {
    scope.effect(
      () => guardManagedSettings(scope.settings),
      'auriga-dsh: managed settings policy',
    );
  });
  ctx.inject?.(['credentials'], (/** @type {any} */ scope) => {
    scope.effect(
      () => guardManagedCredentials(scope.credentials),
      'auriga-dsh: managed credential policy',
    );
  });
}
