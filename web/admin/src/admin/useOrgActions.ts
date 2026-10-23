import { computed, onBeforeUnmount, ref, shallowRef } from 'vue';
import { ApiError, isApiError } from '@auriga/shared';
import { adminApi } from './api';

type Translate = (key: string, params?: Record<string, string>) => string;
export function orgError(error: unknown, t: Translate) {
  if (!isApiError(error)) return t('failed');
  const key =
    error.status === 403
      ? 'forbidden'
      : error.status === 409 || error.status === 412
        ? 'conflict'
        : error.status === 429
          ? 'limited'
          : error.status >= 500
            ? 'unavailable'
            : 'failed';
  // Never echo server detail strings from invitation operations: they may contain a credential.
  return `${t(key)} (${error.problem.code})${error.retryAfter ? ` ${t('retryAfter', { value: error.retryAfter })}` : ''}`;
}
export function useOrgActions(read: string, write: string, t: Translate) {
  const permissions = ref<string[]>([]);
  const busy = ref(false);
  const error = ref('');
  const notice = ref('');
  const pending = shallowRef<{ message: string; run: () => Promise<void> }>();
  const canRead = computed(() => permissions.value.includes(read));
  const canWrite = computed(
    () => canRead.value && permissions.value.includes(write),
  );
  const locked = computed(() => busy.value || !!pending.value);
  let alive = true;
  let authVersion = 0;
  onBeforeUnmount(() => {
    alive = false;
    ++authVersion;
    pending.value = undefined;
  });
  async function authorize() {
    const version = ++authVersion;
    let membership;
    try {
      membership = await adminApi.membership();
    } catch (e) {
      if (alive && version === authVersion) {
        error.value = orgError(e, t);
        permissions.value = [];
      }
      throw e;
    }
    if (!alive || version !== authVersion) return false;
    permissions.value = membership.permissions;
    if (!canRead.value) {
      const denied = new ApiError({
        status: 403,
        code: 'common.forbidden',
        title: t('forbidden'),
      });
      error.value = orgError(denied, t);
      throw denied;
    }
    return true;
  }
  function ask(message: string, run: () => Promise<void>) {
    if (locked.value || !canWrite.value) return;
    error.value = '';
    notice.value = '';
    pending.value = { message, run };
  }
  async function confirm() {
    const action = pending.value;
    if (!action || busy.value) return;
    busy.value = true;
    try {
      if (!(await authorize())) return;
      if (!canWrite.value)
        throw new ApiError({
          status: 403,
          code: 'common.forbidden',
          title: t('forbidden'),
        });
      await action.run();
    } catch (e) {
      if (alive) {
        error.value = orgError(e, t);
        if (isApiError(e) && (e.status === 401 || e.status === 403))
          permissions.value = [];
      }
    } finally {
      if (alive) {
        busy.value = false;
        pending.value = undefined;
      }
    }
  }
  return {
    permissions,
    busy,
    error,
    notice,
    pending,
    canRead,
    canWrite,
    locked,
    authorize,
    ask,
    confirm,
    isAlive: () => alive,
  };
}
