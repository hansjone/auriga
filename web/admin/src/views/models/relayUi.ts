import { onBeforeUnmount, ref } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
export function useRelayUi(t: (key: string) => string) {
  const loading = ref(false),
    busy = ref(false),
    error = ref(''),
    notice = ref(''),
    permissions = ref<string[]>([]);
  const lifetime = new globalThis.AbortController();
  let alive = true,
    version = 0;
  onBeforeUnmount(() => {
    alive = false;
    version++;
    lifetime.abort();
  });
  const can = (permission: string) => permissions.value.includes(permission);
  const message = (e: unknown) =>
    !isApiError(e)
      ? t('failed')
      : t(
          e.problem.code === 'iam.step_up_required'
            ? 'stepup'
            : e.status === 403 || e.status === 401
              ? 'forbidden'
              : e.status === 400
                ? 'invalid'
                : e.status === 409 || e.status === 412
                  ? 'conflict'
                  : e.status === 429
                    ? 'limited'
                    : 'unavailable',
        );
  async function membership() {
    const data = await adminApi.membership();
    lifetime.signal.throwIfAborted();
    permissions.value = data.permissions;
    return data.permissions;
  }
  async function read<T>(permission: string, request: () => Promise<T>) {
    const current = ++version;
    loading.value = true;
    error.value = '';
    try {
      const p = await membership();
      if (!p.includes(permission)) {
        permissions.value = [];
        throw new Error('permission');
      }
      const result = await request();
      if (alive && current === version) return result;
    } catch (e) {
      if (alive && current === version) {
        error.value =
          e instanceof Error && e.message === 'permission'
            ? t('forbidden')
            : message(e);
        if (isApiError(e) && (e.status === 401 || e.status === 403))
          permissions.value = [];
      }
    } finally {
      if (alive && current === version) loading.value = false;
    }
  }
  async function write<T>(
    permission: string,
    request: () => Promise<T>,
    saved: (result: T) => void,
    reload: () => Promise<void>,
  ) {
    if (busy.value) return;
    busy.value = true;
    error.value = '';
    notice.value = '';
    try {
      const p = await membership();
      if (!p.includes(permission)) {
        error.value = t('forbidden');
        return;
      }
      const result = await request();
      if (!alive) return;
      saved(result);
      notice.value = t('saved');
      await reload();
      if (error.value) notice.value = t('readbackFailed');
    } catch (e) {
      if (alive) {
        error.value = message(e);
        if (isApiError(e) && (e.status === 401 || e.status === 403))
          permissions.value = [];
      }
    } finally {
      if (alive) busy.value = false;
    }
  }
  return {
    loading,
    busy,
    error,
    notice,
    permissions,
    can,
    read,
    write,
    message,
    signal: lifetime.signal,
  };
}
