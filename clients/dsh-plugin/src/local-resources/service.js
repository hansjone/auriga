// @ts-nocheck
import { createHash } from 'node:crypto';
import { installedPackage } from './installed.js';

export class LocalResourceFailure extends Error {
  constructor(status, code) {
    super(code);
    this.status = status;
    this.code = code;
  }
}
const fail = (status, code) => {
  throw new LocalResourceFailure(status, code);
};
const object = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
const hash = (v) =>
  createHash('sha256').update(JSON.stringify(v)).digest('hex');
// Cordis scopes a service on access; compare the provider, not its transient proxy.
const provider = (value) => value?.[Symbol.for('cordis.original')] ?? value;
const dangerous =
  /secret|password|token|key|credential|url|uri|host|origin|path|root|command|script|module|source|entry|auth|port|proxy|endpoint|trust/i;
const packageName = /^(?:@[a-z0-9._-]+\/)?[a-z0-9][a-z0-9._-]*$/i;
// Core/boot/transport/credential services and Auriga itself never become CRUD targets.
function protectedPlugin(name) {
  return (
    name.length > 214 ||
    !packageName.test(name) ||
    /^(?:auriga-dsh|cordis)(?:$|[:/])/.test(name) ||
    /^@deepseek-ai\//.test(name) ||
    /auth|identity|credential|webserver|connection|loader|profile|config-editor|settings|plugin-manager|sandbox|gateway|egress/i.test(
      name,
    )
  );
}

/** Project only explicitly declared, non-secret primitive fields. Unknown schemas fail closed. */
export function safeFields(descriptor) {
  if (!descriptor || !Array.isArray(descriptor.secrets)) return [];
  const envelope = descriptor.schema;
  const resolve = (node) =>
    typeof node === 'number' || typeof node === 'string'
      ? envelope?.refs?.[node]
      : node;
  const root = resolve(envelope?.uid === undefined ? envelope : envelope.uid);
  if (
    !root ||
    root.type !== 'object' ||
    root.meta?.role === 'secret' ||
    !object(root.dict)
  )
    return [];
  return Object.entries(root.dict)
    .slice(0, 128)
    .flatMap(([key, ref]) => {
      const node = resolve(ref);
      if (
        !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(key) ||
        dangerous.test(key) ||
        !node ||
        node.meta?.role === 'secret' ||
        descriptor.secrets.some(
          (s) =>
            !Array.isArray(s.path) || s.path.length === 0 || s.path[0] === key,
        ) ||
        !['string', 'number', 'boolean'].includes(node.type)
      )
        return [];
      const value = descriptor.value?.[key];
      if (
        typeof value !== node.type ||
        (typeof value === 'number' && !Number.isFinite(value)) ||
        (typeof value === 'string' && value.length > 4096)
      )
        return [];
      return [{ key, type: node.type, value }];
    });
}

/** Native host services are injected by Cordis; no filesystem writes or remote calls here. */
export function createLocalResources({ getHost, mode, alive = () => true }) {
  let queue = Promise.resolve();
  const check = () => {
    if (!alive()) fail(503, 'common.unavailable');
  };
  async function snapshot() {
    check();
    const host = getHost();
    if (typeof host?.pluginManager?.listPlugins !== 'function')
      fail(503, 'common.unavailable');
    const manager = host.pluginManager,
      settings = host.settings;
    const rows = await manager.listPlugins();
    check();
    if (!Array.isArray(rows) || rows.length > 4096)
      fail(503, 'common.unavailable');
    const forms =
      typeof settings?.describe === 'function'
        ? settings.describe({ redactSecrets: true })
        : [];
    check();
    return rows.map((row) => {
      if (
        typeof row.entryId !== 'string' ||
        row.entryId.length > 4096 ||
        typeof row.moduleName !== 'string' ||
        row.moduleName.length > 4096 ||
        typeof row.enabled !== 'boolean'
      )
        fail(503, 'common.unavailable');
      const form = forms.find((f) => f.ns === row.patchId);
      const fields = safeFields(form);
      const installation = installedPackage(host, row.moduleName);
      const readonly =
        mode === 'managed'
          ? 'managed'
          : protectedPlugin(row.moduleName)
            ? 'protected'
            : row.readOnlyReason
              ? 'host'
              : !installation
                ? 'unverified'
                : undefined;
      const item = {
        installation: installation ? 'present' : 'unknown',
        id: hash(row.entryId),
        name:
          row.moduleName.length <= 214 && packageName.test(row.moduleName)
            ? row.moduleName
            : 'Local plugin',
        ...(installation?.version ? { version: installation.version } : {}),
        enabled: row.enabled,
        phase: ['pending', 'loading', 'active', 'failed', 'unloading'].includes(
          row.fiberPhase,
        )
          ? row.fiberPhase
          : 'inactive',
        revision: hash([
          row.entryId,
          row.moduleName,
          row.patchId,
          row.readOnlyReason,
          row.enabled,
          row.fiberPhase,
          mode,
          installation,
          form?.revision,
          fields,
        ]),
        readonly: readonly ?? null,
        canToggle:
          !readonly &&
          typeof host.pluginManager.setPluginEnabled === 'function',
        canConfigure:
          !readonly &&
          host.settings?.writable === true &&
          typeof host.settings?.mutate === 'function' &&
          fields.length > 0 &&
          Number.isSafeInteger(form?.revision),
      };
      return { item, row, form, fields, host, manager, settings };
    });
  }
  async function target(id) {
    if (typeof id !== 'string' || !/^[a-f0-9]{64}$/.test(id))
      fail(400, 'common.validation_failed');
    const row = (await snapshot()).find((r) => r.item.id === id);
    if (!row) fail(404, 'common.not_found');
    return row;
  }
  const detail = async (id) => {
    const { item, form, fields } = await target(id);
    return {
      plugin: item,
      fields,
      configRevision: Number.isSafeInteger(form?.revision)
        ? form.revision
        : null,
    };
  };
  function change(action, input, authorized = () => true) {
    const operation = queue.then(async () => {
      check();
      const selected = await target(input.id);
      if (!authorized()) fail(401, 'common.unauthenticated');
      if (getHost() !== selected.host) fail(503, 'common.unavailable');
      if (
        provider(selected.host.pluginManager) !== provider(selected.manager) ||
        provider(selected.host.settings) !== provider(selected.settings)
      )
        fail(503, 'common.unavailable');
      if (mode === 'managed' || selected.item.readonly)
        fail(403, 'common.forbidden');
      if (input.revision !== selected.item.revision)
        fail(409, 'common.conflict');
      let application = 'applied',
        changed = true;
      check();
      if (action === 'enabled') {
        if (!selected.item.canToggle || typeof input.enabled !== 'boolean')
          fail(400, 'common.validation_failed');
        const result = await selected.manager.setPluginEnabled(
          selected.row.entryId,
          input.enabled,
        );
        if (
          ![
            'applied',
            'restart-required',
            'overridden',
            'failed',
            'cancelled',
          ].includes(result?.application) ||
          typeof result.changed !== 'boolean'
        )
          fail(503, 'common.unavailable');
        application = result.application;
        changed = result.changed;
      } else {
        if (!selected.item.canConfigure) fail(403, 'common.forbidden');
        if (input.configRevision !== selected.form.revision)
          fail(409, 'common.conflict');
        if (
          !Array.isArray(input.changes) ||
          input.changes.length < 1 ||
          input.changes.length > 128 ||
          new Set(input.changes.map((c) => c.key)).size !== input.changes.length
        )
          fail(400, 'common.validation_failed');
        const ops = input.changes.map((c) => {
          const field = selected.fields.find((f) => f.key === c.key);
          if (
            !field ||
            Object.keys(c).sort().join(',') !== 'key,value' ||
            typeof c.value !== field.type ||
            (typeof c.value === 'number' && !Number.isFinite(c.value)) ||
            (typeof c.value === 'string' && c.value.length > 4096)
          )
            fail(400, 'common.validation_failed');
          return { op: 'set', path: [c.key], value: c.value };
        });
        try {
          await selected.settings.mutate(
            selected.form.ns,
            ops,
            input.configRevision,
          );
        } catch (error) {
          if (error?.name === 'SettingsConflictError')
            fail(409, 'common.conflict');
          throw error;
        }
      }
      check();
      const item = await detail(input.id);
      if (action === 'config')
        changed = hash(selected.fields) !== hash(item.fields);
      return { application, changed, item };
    });
    queue = operation.catch(() => {});
    return operation;
  }
  return {
    list: async () => ({
      protocol: 1,
      items: (await snapshot()).map((r) => r.item),
    }),
    detail,
    change,
  };
}
