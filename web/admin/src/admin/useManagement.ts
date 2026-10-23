import { computed, onBeforeUnmount, ref, shallowRef } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from './api';

type Page<T> = { items: T[]; nextCursor?: string };
type Action<T> = { message: string; run: () => Promise<T>; success: string };

/** Local UI state only. The server remains authoritative for every permission and mutation. */
export function useManagement<T extends { id: string }>(options: {
  read: string;
  write: string;
  list: (cursor?: string) => Promise<Page<T>>;
  detail: (item: T, canWrite: boolean) => Promise<T>;
  t: (key: string, params?: Record<string, string>) => string;
}) {
  const { t } = options;
  const items = shallowRef<T[]>([]);
  const selected = shallowRef<T>();
  const permissions = ref<string[]>([]);
  const loading = ref(false);
  const detailLoading = ref(false);
  const busy = ref(false);
  const error = ref('');
  const detailError = ref('');
  const notice = ref('');
  const pending = shallowRef<Action<T>>();
  const cursor = ref<string>();
  const history = ref<(string | undefined)[]>([]);
  const nextCursor = ref<string>();
  const canWrite = computed(() => permissions.value.includes(options.write));
  const locked = computed(
    () => loading.value || detailLoading.value || busy.value || !!pending.value,
  );
  let listVersion = 0;
  let detailVersion = 0;
  let alive = true;

  function message(e: unknown) {
    if (!isApiError(e)) return t('failed');
    const key =
      e.status === 403
        ? 'forbidden'
        : e.status === 409 || e.status === 412
          ? 'conflict'
          : e.status === 429
            ? 'limited'
            : e.status >= 500
              ? 'unavailable'
              : '';
    const base = key
      ? t(key)
      : e.problem.detail || e.problem.title || t('failed');
    return `${base} (${e.problem.code})${e.retryAfter ? ` ${t('retryAfter', { value: e.retryAfter })}` : ''}`;
  }

  async function load(target?: string, reset = false) {
    if (busy.value || pending.value) return;
    const version = ++listVersion;
    ++detailVersion;
    const selectedId = reset ? undefined : selected.value?.id;
    selected.value = undefined;
    detailLoading.value = false;
    detailError.value = '';
    loading.value = true;
    error.value = '';
    notice.value = '';
    permissions.value = [];
    items.value = [];
    nextCursor.value = undefined;
    if (reset) {
      history.value = [];
      cursor.value = undefined;
      target = undefined;
    }
    try {
      const membership = await adminApi.membership();
      if (!alive || version !== listVersion) return false;
      permissions.value = membership.permissions;
      if (!membership.permissions.includes(options.read)) {
        error.value = t('forbidden');
        return false;
      }
      const result = await options.list(target);
      if (!alive || version !== listVersion) return false;
      items.value = result.items;
      nextCursor.value = result.nextCursor;
      cursor.value = target;
      const item = result.items.find((row) => row.id === selectedId);
      if (item) await select(item);
      return true;
    } catch (e) {
      if (alive && version === listVersion) error.value = message(e);
      return false;
    } finally {
      if (alive && version === listVersion) loading.value = false;
    }
  }

  async function select(item: T) {
    if (busy.value || pending.value) return;
    const version = ++detailVersion;
    selected.value = undefined;
    detailLoading.value = true;
    detailError.value = '';
    notice.value = '';
    try {
      const result = await options.detail(item, canWrite.value);
      if (alive && version === detailVersion) selected.value = result;
    } catch (e) {
      if (alive && version === detailVersion) detailError.value = message(e);
    } finally {
      if (alive && version === detailVersion) detailLoading.value = false;
    }
  }

  function close() {
    if (locked.value) return;
    ++detailVersion;
    selected.value = undefined;
    detailError.value = '';
    notice.value = '';
  }
  function reset() {
    if (busy.value || pending.value) return;
    ++listVersion;
    ++detailVersion;
    items.value = [];
    selected.value = undefined;
    loading.value = true;
    detailLoading.value = false;
    cursor.value = undefined;
    nextCursor.value = undefined;
    history.value = [];
    error.value = '';
    detailError.value = '';
    notice.value = '';
  }
  function ask(action: Action<T>) {
    if (!locked.value && canWrite.value && selected.value) {
      detailError.value = '';
      notice.value = '';
      pending.value = action;
    }
  }
  async function confirm() {
    const action = pending.value;
    if (!action || busy.value) return;
    busy.value = true;
    try {
      const membership = await adminApi.membership();
      if (!alive) return;
      permissions.value = membership.permissions;
      if (!canWrite.value || !membership.permissions.includes(options.read)) {
        detailError.value = t('forbidden');
        selected.value = undefined;
        items.value = [];
        return;
      }
      const result = await action.run();
      if (!alive) return;
      selected.value = result;
      items.value = items.value.map((item) =>
        item.id === result.id ? result : item,
      );
      notice.value = action.success;
    } catch (e) {
      if (alive) {
        detailError.value = message(e);
        if (isApiError(e) && e.status === 403) permissions.value = [];
      }
    } finally {
      if (alive) {
        busy.value = false;
        pending.value = undefined;
      }
    }
  }
  async function next() {
    if (locked.value || !nextCursor.value) return;
    const old = cursor.value;
    if (await load(nextCursor.value)) history.value.push(old);
  }
  async function previous() {
    if (locked.value || !history.value.length) return;
    const target = history.value[history.value.length - 1];
    if (await load(target)) history.value.pop();
  }
  onBeforeUnmount(() => {
    alive = false;
    ++listVersion;
    ++detailVersion;
  });
  return {
    items,
    selected,
    loading,
    detailLoading,
    busy,
    locked,
    error,
    detailError,
    notice,
    pending,
    canWrite,
    history,
    nextCursor,
    load: (reset = false) => load(cursor.value, reset),
    select,
    close,
    reset,
    ask,
    confirm,
    next,
    previous,
  };
}
