/* global ReadableStream */
import { randomUUID } from 'node:crypto';
import { createShellState } from './shell-state.js';

const unavailable = (code = 'common.unavailable') =>
  Object.assign(new Error(code), { code });

/** Fixed platform client. Desktop uses C's OS-authenticated IPC; managed native
 * composition supplies its file-bound state. Both validate the frozen business
 * and host DTOs from the same producer artifact.
 * @param {{ipc:any,validate:(name:string,value:any)=>any,onInvalidate?:()=>void,onChange?:(snapshot:any)=>void,signal?:AbortSignal,stateFactory?:(options:any)=>any,checkBusinessAccess?:()=>void,authWaitMs?:number}} options */
export async function createBroker({
  ipc,
  validate,
  onInvalidate,
  onChange,
  signal,
  stateFactory,
  checkBusinessAccess,
  authWaitMs = 8000,
}) {
  /** @type {any} */ let receiver;
  /** @type {any} */ let disconnected;
  let closed = false;
  const shutdown = new AbortController();
  async function receive(/** @type {()=>Promise<any>} */ work) {
    /** @type {ReturnType<typeof setTimeout>|undefined} */ let timer;
    let abort = () => {};
    try {
      return await new Promise((resolve, reject) => {
        abort = () => reject(unavailable());
        shutdown.signal.addEventListener('abort', abort, { once: true });
        timer = setTimeout(abort, 30000);
        timer.unref();
        if (shutdown.signal.aborted) return abort();
        Promise.resolve()
          .then(() => {
            shutdown.signal.throwIfAborted();
            return work();
          })
          .then(resolve, reject);
      });
    } finally {
      clearTimeout(timer);
      shutdown.signal.removeEventListener('abort', abort);
    }
  }
  const transport = ipc
    ? {
        async send(/** @type {any} */ command) {
          validate('ShellAuthCommand', command);
          if (command.action !== 'subscribe') {
            const event = await receive(() => ipc.call(command));
            validate('ShellAuthEvent', event);
            return event;
          }
          return receive(
            () =>
              new Promise((resolve, reject) => {
                let first = true;
                Promise.resolve(
                  ipc.subscribe(command, (/** @type {any} */ event) => {
                    if (!event) {
                      disconnected?.();
                      reject(unavailable());
                      return;
                    }
                    try {
                      validate('ShellAuthEvent', event);
                    } catch {
                      disconnected?.();
                      reject(unavailable());
                      return;
                    }
                    if (first) {
                      first = false;
                      resolve(event);
                    } else receiver?.(event);
                  }),
                ).catch(reject);
              }),
          );
        },
        subscribe(/** @type {any} */ next, /** @type {any} */ off) {
          receiver = next;
          disconnected = off;
          return () => {
            receiver = disconnected = undefined;
          };
        },
      }
    : undefined;
  /** @type {Set<(snapshot:any)=>void>} */ const changeListeners = new Set();
  /** @type {any} */ let lastAuthenticatedSnapshot;
  /** @type {string|null} */ let lastAuthenticatedKey = null;
  const handleStateChange = (/** @type {any} */ next) => {
    if (next?.state === 'authenticated') {
      lastAuthenticatedSnapshot = next;
      lastAuthenticatedKey = state?.authorization?.() ?? null;
    } else if (
      next &&
      !['unreachable', 'reauth_required'].includes(next.state)
    ) {
      // Keep the last verified identity across transient loss so callers can
      // wait for shell-auth self-heal instead of flashing unauthenticated.
      lastAuthenticatedSnapshot = undefined;
      lastAuthenticatedKey = null;
    }
    for (const listener of [...changeListeners]) listener(next);
    onChange?.(next);
  };
  const state = stateFactory
    ? stateFactory({ onInvalidate, onChange: handleStateChange })
    : createShellState({
        transport,
        onInvalidate,
        onChange: handleStateChange,
      });
  const stop = () => {
    closed = true;
    shutdown.abort();
    state.close();
  };
  signal?.addEventListener('abort', stop, { once: true });
  if (signal?.aborted) stop();
  const sameAuthenticatedIdentity = (
    /** @type {any} */ a,
    /** @type {any} */ b,
  ) =>
    a?.state === 'authenticated' &&
    b?.state === 'authenticated' &&
    a.shellEpoch === b.shellEpoch &&
    a.identity?.tenantId === b.identity?.tenantId &&
    a.identity?.memberId === b.identity?.memberId &&
    a.identity?.deviceId === b.identity?.deviceId &&
    a.identity?.providerId === b.identity?.providerId &&
    a.identity?.uacSessionBinding?.sourceSessionId ===
      b.identity?.uacSessionBinding?.sourceSessionId &&
    a.identity?.uacSessionBinding?.authGeneration ===
      b.identity?.uacSessionBinding?.authGeneration;
  // Stop consumers immediately on shell invalidation even when the IPC producer
  // never settles. The producer still owns cancellation of its remote work.
  async function pending(
    /** @type {()=>Promise<any>} */ work,
    { allowRevisionAdvance = false } = {},
  ) {
    const snapBefore = state.current();
    const anchorAuthKey = state.authorization?.() ?? null;
    const check = state.fence();
    const sameOrTransientUnreachable = (/** @type {any} */ next) => {
      if (sameAuthenticatedIdentity(snapBefore, next)) return true;
      return (
        !stateFactory &&
        sameRecoveringSession(next, anchorAuthKey, snapBefore)
      );
    };
    const verify = () => {
      try {
        check();
      } catch (error) {
        if (allowRevisionAdvance && sameOrTransientUnreachable(state.current()))
          return;
        throw error;
      }
    };
    let off = () => {};
    /** @type {(next:any)=>void} */ let onStateChange = () => {};
    /** @type {ReturnType<typeof setTimeout>|undefined} */ let timer;
    try {
      return await new Promise((resolve, reject) => {
        onStateChange = (next) => {
          if (allowRevisionAdvance && !sameOrTransientUnreachable(next)) {
            reject(unavailable('common.unauthenticated'));
          }
        };
        if (allowRevisionAdvance) changeListeners.add(onStateChange);
        off = state.subscribeInvalidation(() => {
          if (allowRevisionAdvance && state.current()) return;
          reject(unavailable('common.unauthenticated'));
        });
        timer = setTimeout(() => reject(unavailable()), 30000);
        timer.unref();
        verify();
        Promise.resolve(work()).then((result) => {
          try {
            verify();
            resolve(result);
          } catch (error) {
            reject(error);
          }
        }, reject);
      });
    } finally {
      changeListeners.delete(onStateChange);
      off();
      clearTimeout(timer);
    }
  }
  const transientRecoverable = (/** @type {any} */ snap) =>
    ['unreachable', 'reauth_required'].includes(snap?.state);
  const sameRecoveringSession = (
    /** @type {any} */ snap,
    /** @type {string|null} */ anchorKey,
    /** @type {any} */ anchorSnap,
  ) => {
    if (!transientRecoverable(snap) || !anchorSnap || !lastAuthenticatedSnapshot)
      return false;
    const retained =
      anchorSnap.state === 'authenticated'
        ? anchorSnap
        : lastAuthenticatedSnapshot;
    if (
      lastAuthenticatedSnapshot.identity?.tenantId !==
        retained.identity?.tenantId ||
      lastAuthenticatedSnapshot.identity?.memberId !==
        retained.identity?.memberId ||
      lastAuthenticatedSnapshot.identity?.deviceId !==
        retained.identity?.deviceId ||
      lastAuthenticatedSnapshot.shellEpoch !== retained.shellEpoch
    )
      return false;
    // Unreachable keeps the authorization key; reauth_required clears it in
    // shell-state while the local refresh_token may still self-heal.
    if (snap.state === 'unreachable') {
      return (
        Boolean(anchorKey) &&
        state.authorization?.() === anchorKey &&
        anchorKey === lastAuthenticatedKey
      );
    }
    return Boolean(lastAuthenticatedKey) && lastAuthenticatedKey === anchorKey;
  };
  async function waitForAuthenticated(timeoutMs = authWaitMs) {
    if (closed) throw unavailable('common.unauthenticated');
    const initial = state.current();
    if (initial?.state === 'authenticated') {
      return {
        anchorKey: state.authorization?.() ?? null,
        anchorSnap: initial,
      };
    }
    if (stateFactory) throw unavailable('common.unauthenticated');
    const anchorSnap = lastAuthenticatedSnapshot;
    const anchorKey =
      (initial?.state === 'unreachable'
        ? state.authorization?.()
        : null) ?? lastAuthenticatedKey;
    if (
      !transientRecoverable(initial) ||
      !anchorKey ||
      !anchorSnap ||
      !sameRecoveringSession(initial, anchorKey, anchorSnap)
    ) {
      throw unavailable('common.unauthenticated');
    }
    return await new Promise((resolve, reject) => {
      let done = false;
      /** @type {ReturnType<typeof setTimeout>|undefined} */ let timer;
      const finish = (/** @type {() => void} */ fn) => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        changeListeners.delete(check);
        offInvalidation();
        fn();
      };
      const check = () => {
        if (closed)
          return finish(() => reject(unavailable('common.unauthenticated')));
        const cur = state.current();
        if (cur?.state === 'authenticated') {
          if (sameAuthenticatedIdentity(anchorSnap, cur)) {
            // Freeze the recovered identity; never re-read current() later or a
            // back-to-back account switch could become the call anchor.
            return finish(() =>
              resolve({
                anchorKey: state.authorization?.() ?? anchorKey,
                anchorSnap: cur,
              }),
            );
          }
          return finish(() => reject(unavailable('common.unauthenticated')));
        }
        if (!sameRecoveringSession(cur, anchorKey, anchorSnap)) {
          return finish(() => reject(unavailable('common.unauthenticated')));
        }
      };
      changeListeners.add(check);
      const offInvalidation = state.subscribeInvalidation(() => {
        if (!state.current()) check();
      });
      timer = setTimeout(() => {
        const cur = state.current();
        finish(() =>
          reject(
            unavailable(
              sameRecoveringSession(cur, anchorKey, anchorSnap)
                ? 'common.unavailable'
                : 'common.unauthenticated',
            ),
          ),
        );
      }, timeoutMs);
      check();
    });
  }
  const envelope = (requireAccess = true) => {
    if (requireAccess) checkBusinessAccess?.();
    const s = state.current();
    if (s?.state !== 'authenticated' || closed)
      throw unavailable(
        !stateFactory && transientRecoverable(s)
          ? 'common.unavailable'
          : 'common.unauthenticated',
      );
    return {
      protocolVersion: 1,
      requestId: randomUUID(),
      shellEpoch: s.shellEpoch,
      expectedRevision: s.revision,
      expiresAt: new Date(
        Math.min(Date.now() + 30000, Date.parse(s.expiresAt)),
      ).toISOString(),
    };
  };
  async function host(
    /** @type {string} */ path,
    /** @type {string} */ requestSchema,
    /** @type {string} */ resultSchema,
    /** @type {any} */ input,
  ) {
    const check = state.fence(),
      command = { ...envelope(input.action !== 'revoke'), ...input };
    validate(requestSchema, command);
    const event = await pending(() =>
      ipc.call({
        requestId: command.requestId,
        path,
        body: command,
      }),
    );
    check();
    const result = event?.body ?? event;
    validate(resultSchema, result);
    if (
      result.requestId !== command.requestId ||
      result.shellEpoch !== command.shellEpoch ||
      result.revision !== command.expectedRevision
    )
      throw unavailable('common.conflict');
    if (result.error)
      throw Object.assign(unavailable(result.error.code), {
        reason: result.error.reason,
      });
    return result;
  }
  let uacSequence = 0;
  const broker = {
    state,
    async localUac(/** @type {any} */ credential) {
      const current = state.current();
      if (!ipc || closed || !current) throw unavailable();
      const command = {
        protocolVersion: 1,
        requestId: randomUUID(),
        shellEpoch: current.shellEpoch,
        sequence: ++uacSequence,
        expiresAt: new Date(Date.now() + 25000).toISOString(),
        credential,
      };
      validate('ShellAuthLocalUacCommand', command);
      const event = await receive(() =>
        ipc.call({
          requestId: command.requestId,
          path: '/host/local-uac',
          body: command,
        }),
      );
      const result = event?.body;
      if (
        result?.requestId !== command.requestId ||
        result?.shellEpoch !== command.shellEpoch
      )
        throw unavailable('common.conflict');
    },
    async call(/** @type {string} */ operation, /** @type {any} */ input) {
      const anchor = await waitForAuthenticated();
      const matchesAnchor = (/** @type {any} */ snap) =>
        sameAuthenticatedIdentity(anchor.anchorSnap, snap) &&
        (stateFactory || state.authorization?.() === anchor.anchorKey);
      for (let attempt = 0; attempt < 2; attempt++) {
        const snapBefore = state.current();
        if (!matchesAnchor(snapBefore))
          throw unavailable('common.unauthenticated');
        const check = state.fence(),
          command = { ...envelope(), call: { operation, input } };
        validate('ShellAuthBusinessCall', command);
        const result = await pending(() => ipc.call(command), {
          allowRevisionAdvance: true,
        });
        validate('ShellAuthBusinessResult', result);
        if (
          result.requestId !== command.requestId ||
          result.operation !== operation
        )
          throw unavailable('common.conflict');
        if (
          attempt === 0 &&
          result.error?.code === 'common.conflict' &&
          result.error.retryable === true &&
          result.error.upstreamStatus === undefined &&
          snapBefore?.shellEpoch
        ) {
          const snapAfter = state.current();
          if (
            matchesAnchor(snapAfter) &&
            snapAfter.revision !== snapBefore.revision
          ) {
            continue;
          }
        }
        try {
          check();
        } catch (error) {
          const cur = state.current();
          const stillSameSession =
            matchesAnchor(cur) ||
            (!stateFactory &&
              sameRecoveringSession(
                cur,
                anchor.anchorKey,
                anchor.anchorSnap,
              ));
          if (!stillSameSession) throw error;
        }
        if (result.error)
          throw Object.assign(unavailable(result.error.code), {
            status: result.error.upstreamStatus,
            retryable: result.error.retryable,
            ...(result.error.rule ? { rule: result.error.rule } : {}),
          });
        return result.output;
      }
      throw unavailable('common.conflict');
    },
    async register(/** @type {any} */ context) {
      const result = await host(
        '/host/contexts',
        'ShellAuthHostContextCommand',
        'ShellAuthHostContextResult',
        { action: 'register', context },
      );
      if (
        result.action !== 'register' ||
        result.context.hostSessionId !== context.hostSessionId ||
        result.context.kind !== context.kind ||
        Date.parse(result.context.expiresAt) > Date.parse(context.expiresAt)
      )
        throw unavailable('common.conflict');
      return result.context;
    },
    async revoke(/** @type {string} */ executionContextId) {
      return host(
        '/host/contexts',
        'ShellAuthHostContextCommand',
        'ShellAuthHostContextResult',
        { action: 'revoke', executionContextId },
      );
    },
    async credentials(
      /** @type {string} */ executionContextId,
      /** @type {'raw'|'outbound'} */ purpose,
    ) {
      const result = await host(
        '/host/credentials',
        'ShellAuthHostCredentialsRequest',
        'ShellAuthHostCredentialsResult',
        { executionContextId, purpose },
      );
      if (
        result.executionContextId !== executionContextId ||
        result.purpose !== purpose
      )
        throw unavailable('common.conflict');
      return result.credentials;
    },
    async streamCommand(/** @type {any} */ input) {
      const check = state.fence(),
        s = envelope(input.action !== 'cancel');
      const command = {
        shellEpoch: s.shellEpoch,
        expectedRevision: s.expectedRevision,
        ...input,
      };
      validate('ShellAuthStreamCommand', command);
      const result = await ipc.streamCommand(command);
      check();
      return result;
    },
    async subscribeStream(
      /** @type {string} */ streamId,
      /** @type {(value:any)=>void} */ callback,
    ) {
      const check = state.fence(),
        s = envelope();
      await ipc.subscribeStream(
        {
          streamId,
          shellEpoch: s.shellEpoch,
          expectedRevision: s.expectedRevision,
        },
        (/** @type {any} */ event) => {
          try {
            check();
            validate('ShellAuthStreamEvent', event);
            if (
              event.streamId !== streamId ||
              event.shellEpoch !== s.shellEpoch ||
              event.revision !== s.expectedRevision
            )
              throw unavailable('common.conflict');
            callback(event);
          } catch {
            callback(null);
          }
        },
      );
    },
    openStream(
      /** @type {string} */ streamId,
      /** @type {AbortSignal} */ signal,
    ) {
      const check = state.fence();
      let sequence = 0,
        owed = 0,
        ended = false;
      const finish = () => {
        ended = true;
        signal.removeEventListener('abort', abort);
      };
      const abort = () => {
        void broker
          .streamCommand({ streamId, action: 'cancel' })
          .catch(() => {});
      };
      signal.addEventListener('abort', abort, { once: true });
      return new ReadableStream({
        async start(controller) {
          try {
            await broker.subscribeStream(streamId, (event) => {
              try {
                check();
                signal.throwIfAborted();
                if (ended) return;
                if (!event || event.sequence !== sequence++)
                  throw unavailable('common.conflict');
                if (event.type === 'end') {
                  finish();
                  controller.close();
                  return;
                }
                if (event.type !== 'data') throw unavailable();
                const bytes = Buffer.from(event.data, 'base64');
                if (
                  bytes.toString('base64') !== event.data ||
                  bytes.length > 65536
                )
                  throw unavailable('common.validation_failed');
                controller.enqueue(new Uint8Array(bytes));
                owed += bytes.length;
                if ((controller.desiredSize ?? 0) > 0) {
                  const creditBytes = owed;
                  owed = 0;
                  void broker
                    .streamCommand({ streamId, action: 'credit', creditBytes })
                    .catch((error) => {
                      if (!ended) {
                        finish();
                        controller.error(error);
                      }
                    });
                }
              } catch (error) {
                if (!ended) {
                  finish();
                  controller.error(error);
                  abort();
                }
              }
            });
            signal.throwIfAborted();
            await broker.streamCommand({
              streamId,
              action: 'credit',
              creditBytes: 65536,
            });
          } catch (error) {
            if (!ended) {
              finish();
              controller.error(error);
              abort();
            }
          }
        },
        async pull() {
          if (owed && !ended) {
            const creditBytes = owed;
            owed = 0;
            await broker.streamCommand({
              streamId,
              action: 'credit',
              creditBytes,
            });
          }
        },
        cancel() {
          finish();
          abort();
        },
      });
    },
    close() {
      stop();
      signal?.removeEventListener('abort', stop);
      ipc?.close();
    },
  };
  await state.connect();
  return broker;
}
