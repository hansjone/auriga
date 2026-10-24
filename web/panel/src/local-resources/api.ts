import type {
  LocalList,
  LocalDetail,
  LocalChange,
  LocalPlugin,
} from '../../../../clients/dsh-plugin/src/local-resources/generated';
import { ResourceError, type PanelContext } from '../resources';
export type {
  LocalPlugin,
  LocalDetail,
} from '../../../../clients/dsh-plugin/src/local-resources/generated';

const record = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === 'object' && !Array.isArray(v);
const digest = (v: unknown) =>
  typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
function plugin(v: unknown): v is LocalPlugin {
  return (
    record(v) &&
    ['present', 'unknown'].includes(String(v.installation)) &&
    digest(v.id) &&
    digest(v.revision) &&
    typeof v.name === 'string' &&
    v.name.length > 0 &&
    v.name.length <= 214 &&
    (v.version === undefined ||
      (typeof v.version === 'string' && v.version.length <= 128)) &&
    typeof v.enabled === 'boolean' &&
    [
      'pending',
      'loading',
      'active',
      'failed',
      'unloading',
      'inactive',
    ].includes(String(v.phase)) &&
    [null, 'managed', 'protected', 'host', 'unverified'].includes(
      v.readonly as null | string,
    ) &&
    typeof v.canToggle === 'boolean' &&
    typeof v.canConfigure === 'boolean'
  );
}
function detail(v: unknown): v is LocalDetail {
  return (
    record(v) &&
    plugin(v.plugin) &&
    (v.configRevision === null ||
      (Number.isSafeInteger(v.configRevision) &&
        Number(v.configRevision) >= 0)) &&
    Array.isArray(v.fields) &&
    v.fields.length <= 128 &&
    v.fields.every(
      (f) =>
        record(f) &&
        typeof f.key === 'string' &&
        /^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(f.key) &&
        ['string', 'number', 'boolean'].includes(String(f.type)) &&
        typeof f.value === f.type &&
        (typeof f.value !== 'number' || Number.isFinite(f.value)) &&
        (typeof f.value !== 'string' || f.value.length <= 4096),
    )
  );
}

export function localApi(context: PanelContext) {
  async function call<T>(
    path: string,
    signal: AbortSignal,
    valid: (v: unknown) => boolean,
    body?: object,
  ): Promise<T> {
    if (context.protocol !== 1 || !context.localResources)
      throw new ResourceError('missing');
    const response = await context.localResources(path, {
      method: body ? 'POST' : 'GET',
      signal,
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    if (!response.ok)
      throw new ResourceError(
        response.status === 401
          ? 'auth'
          : response.status === 403
            ? 'forbidden'
            : response.status === 409
              ? 'conflict'
              : 'unavailable',
      );
    try {
      const value: unknown = await response.json();
      if (!valid(value)) throw new Error();
      return value as T;
    } catch {
      throw new ResourceError('invalid');
    }
  }
  return {
    list: (signal: AbortSignal) =>
      call<LocalList>(
        '',
        signal,
        (v) =>
          record(v) &&
          v.protocol === 1 &&
          Array.isArray(v.items) &&
          v.items.length <= 4096 &&
          v.items.every(plugin),
      ),
    detail: (id: string, signal: AbortSignal) =>
      call<LocalDetail>(
        '/detail',
        signal,
        (v) => detail(v) && v.plugin.id === id,
        { id },
      ),
    enable: (item: LocalDetail, enabled: boolean, signal: AbortSignal) =>
      call<LocalChange>('/enabled', signal, change(item.plugin.id), {
        id: item.plugin.id,
        revision: item.plugin.revision,
        enabled,
      }),
    configure: (
      item: LocalDetail,
      changes: { key: string; value: string | number | boolean }[],
      signal: AbortSignal,
    ) =>
      call<LocalChange>('/config', signal, change(item.plugin.id), {
        id: item.plugin.id,
        revision: item.plugin.revision,
        configRevision: item.configRevision,
        changes,
      }),
  };
}
function change(id: string) {
  return (v: unknown) =>
    record(v) &&
    typeof v.changed === 'boolean' &&
    [
      'applied',
      'restart-required',
      'overridden',
      'failed',
      'cancelled',
    ].includes(String(v.application)) &&
    detail(v.item) &&
    v.item.plugin.id === id;
}
