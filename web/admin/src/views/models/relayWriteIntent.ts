import { onBeforeUnmount, shallowRef, watch } from 'vue';
import { isApiError } from '@auriga/shared';
import type { useRelayUi } from './relayUi';

export type RelayWriteAction = {
  message: string;
  permission: string;
  run: () => Promise<void>;
};
function definitelyRejected(error: unknown) {
  if (!isApiError(error)) return false;
  // A timeout, server failure, missing reply or an occupied durable claim does
  // not establish whether the mutation committed. Only rejection responses do.
  return (
    [400, 401, 403, 404, 405, 406, 412, 413, 415, 422, 429].includes(
      error.status,
    ) ||
    (error.status === 409 && error.problem.code === 'common.conflict')
  );
}
export function useRelayWriteIntent(ui: ReturnType<typeof useRelayUi>) {
  const unresolved = shallowRef<RelayWriteAction>();
  let alive = true,
    epoch = 0;
  function reset() {
    epoch++;
    unresolved.value = undefined;
  }
  onBeforeUnmount(() => {
    alive = false;
    reset();
  });
  watch(ui.permissions, (permissions) => {
    if (unresolved.value && !permissions.includes(unresolved.value.permission))
      reset();
  });
  function createAction<T>(
    permission: string,
    message: string,
    request: (key: string) => Promise<T>,
    saved: (result: T) => void,
    reload: () => Promise<void>,
  ): RelayWriteAction {
    const key = globalThis.crypto.randomUUID();
    const created = epoch;
    const action: RelayWriteAction = {
      permission,
      message,
      run: () =>
        ui.write(
          permission,
          async () => {
            if (!alive || created !== epoch)
              throw new DOMException('Stale relay action', 'AbortError');
            const current = epoch;
            const recovering = unresolved.value === action;
            try {
              const result = await request(key);
              if (alive && current === epoch) unresolved.value = undefined;
              return result;
            } catch (error) {
              if (alive && current === epoch)
                unresolved.value =
                  recovering || !definitelyRejected(error) ? action : undefined;
              throw error;
            }
          },
          saved,
          reload,
        ),
    };
    return action;
  }
  return { unresolved, createAction, reset };
}
