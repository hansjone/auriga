import { onBeforeUnmount, ref, shallowRef } from 'vue';

/** Page cursors are opaque server values; old responses never replace newer filters. */
export function useCursorList<T>(
  fetchPage: (cursor?: string) => Promise<{ items: T[]; nextCursor?: string }>,
  message: (e: unknown) => string,
) {
  const items = shallowRef<T[]>([]);
  const loading = ref(false);
  const error = ref('');
  const cursor = ref<string>();
  const nextCursor = ref<string>();
  const history = ref<(string | undefined)[]>([]);
  let version = 0;
  function clear(preserveError = false) {
    ++version;
    items.value = [];
    nextCursor.value = undefined;
    cursor.value = undefined;
    history.value = [];
    if (!preserveError) error.value = '';
    loading.value = false;
  }
  onBeforeUnmount(clear);
  async function load(target?: string) {
    const request = ++version;
    loading.value = true;
    error.value = '';
    items.value = [];
    nextCursor.value = undefined;
    try {
      const result = await fetchPage(target);
      if (request !== version) return false;
      items.value = result.items;
      nextCursor.value = result.nextCursor;
      cursor.value = target;
      return true;
    } catch (e) {
      if (request === version) error.value = message(e);
      return false;
    } finally {
      if (request === version) loading.value = false;
    }
  }
  async function next() {
    if (loading.value || !nextCursor.value) return;
    const old = cursor.value;
    if (await load(nextCursor.value)) history.value.push(old);
  }
  async function previous() {
    if (loading.value || !history.value.length) return;
    if (await load(history.value[history.value.length - 1]))
      history.value.pop();
  }
  return {
    items,
    loading,
    error,
    cursor,
    nextCursor,
    history,
    clear,
    load,
    refresh: () => load(cursor.value),
    next,
    previous,
  };
}
