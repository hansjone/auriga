/* global ReadableStream */
import { randomUUID } from 'node:crypto';
import { createShellState } from './shell-state.js';

const unavailable = (code = 'common.unavailable') =>
  Object.assign(new Error(code), { code });

/** The only platform transport. ipc is C's OS-authenticated backend connection;
 * validate compiles the frozen producer schemas from that same broker artifact.
 * @param {{ipc:any,validate:(name:string,value:any)=>any,onInvalidate?:()=>void,onChange?:(snapshot:any)=>void}} options */
export async function createBroker({ ipc, validate, onInvalidate, onChange }) {
  /** @type {any} */ let receiver;
  /** @type {any} */ let disconnected;
  let closed = false;
  const transport = ipc
    ? {
        async send(/** @type {any} */ command) {
          validate('ShellAuthCommand', command);
          if (command.action !== 'subscribe') {
            const event = await ipc.call(command);
            validate('ShellAuthEvent', event);
            return event;
          }
          return new Promise((resolve, reject) => {
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
          });
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
  const state = createShellState({ transport, onInvalidate, onChange });
  // Stop consumers immediately on shell invalidation even when the IPC producer
  // never settles. The producer still owns cancellation of its remote work.
  async function pending(/** @type {()=>Promise<any>} */ work) {
    const check = state.fence();
    let off = () => {};
    /** @type {ReturnType<typeof setTimeout>|undefined} */ let timer;
    try {
      return await new Promise((resolve, reject) => {
        off = state.subscribeInvalidation(() =>
          reject(unavailable('common.unauthenticated')),
        );
        timer = setTimeout(() => reject(unavailable()), 30000);
        timer.unref();
        check();
        Promise.resolve()
          .then(() => {
            check();
            return work();
          })
          .then((result) => {
            try {
              check();
              resolve(result);
            } catch (error) {
              reject(error);
            }
          }, reject);
      });
    } finally {
      off();
      clearTimeout(timer);
    }
  }
  const envelope = () => {
    const s = state.current();
    if (s?.state !== 'authenticated' || closed)
      throw unavailable('common.unauthenticated');
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
      command = { ...envelope(), ...input };
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
  const broker = {
    state,
    async call(/** @type {string} */ operation, /** @type {any} */ input) {
      const check = state.fence(),
        command = { ...envelope(), call: { operation, input } };
      validate('ShellAuthBusinessCall', command);
      const result = await pending(() => ipc.call(command));
      check();
      validate('ShellAuthBusinessResult', result);
      if (
        result.requestId !== command.requestId ||
        result.operation !== operation
      )
        throw unavailable('common.conflict');
      if (result.error)
        throw Object.assign(unavailable(result.error.code), {
          status: result.error.upstreamStatus,
          retryable: result.error.retryable,
        });
      return result.output;
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
        s = envelope();
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
      closed = true;
      state.close();
      ipc?.close();
    },
  };
  await state.connect();
  return broker;
}
