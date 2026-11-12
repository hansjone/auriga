import { onBeforeUnmount, ref, shallowRef } from 'vue';
import { adminApi } from './api';
import { orgError } from './useOrgActions';
type T = (key: string) => string;
/** Writes are never replayed after an unknown result; reload the authoritative queue first. */
export function useOpsRuntime<V>(
  read: string,
  fetchValue: () => Promise<V>,
  t: T,
) {
  const value = shallowRef<V>();
  const permissions = ref<string[]>([]);
  const busy = ref(false);
  const error = ref('');
  const notice = ref('');
  const uncertain = ref(false);
  const pending = shallowRef<{
    message: string;
    permission: string;
    run: (signal: AbortSignal) => Promise<unknown>;
  }>();
  const controller = new globalThis.AbortController();
  let alive = true;
  async function authorize() {
    const membership = await adminApi.membership();
    if (!alive) return false;
    permissions.value = membership.permissions;
    if (!membership.permissions.includes(read)) {
      value.value = undefined;
      error.value = t('forbidden');
      return false;
    }
    return true;
  }
  async function load() {
    if (busy.value || pending.value) return;
    busy.value = true;
    error.value = '';
    value.value = undefined;
    try {
      if (!(await authorize())) return;
      const result = await fetchValue();
      if (alive) {
        value.value = result;
        uncertain.value = false;
      }
    } catch (e) {
      if (alive) error.value = orgError(e, t);
    } finally {
      if (alive) busy.value = false;
    }
  }
  function ask(
    message: string,
    permission: string,
    run: (signal: AbortSignal) => Promise<unknown>,
  ) {
    if (
      !alive ||
      busy.value ||
      pending.value ||
      uncertain.value ||
      !permissions.value.includes(permission)
    )
      return;
    pending.value = { message, permission, run };
  }
  async function confirm() {
    const action = pending.value;
    if (!action || busy.value) return;
    busy.value = true;
    error.value = '';
    notice.value = '';
    try {
      if (!(await authorize()) || !alive) return;
      if (!permissions.value.includes(action.permission)) {
        error.value = t('forbidden');
        return;
      }
      uncertain.value = true;
      await action.run(controller.signal);
      if (!alive) return;
      notice.value = t('accepted');
      const result = await fetchValue();
      if (alive) {
        value.value = result;
        uncertain.value = false;
      }
    } catch (e) {
      if (alive) {
        value.value = undefined;
        error.value = orgError(e, t);
      }
    } finally {
      if (alive) {
        pending.value = undefined;
        busy.value = false;
      }
    }
  }
  onBeforeUnmount(() => {
    alive = false;
    controller.abort();
    pending.value = undefined;
  });
  return {
    value,
    permissions,
    busy,
    error,
    notice,
    uncertain,
    pending,
    load,
    ask,
    confirm,
  };
}
