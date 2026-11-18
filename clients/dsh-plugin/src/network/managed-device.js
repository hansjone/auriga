import { createConnectRuntime } from '../local/runtime.js';
import { createManagedFileConnection } from '../managed/file-connection.js';

/** Managed hosts use their private device file and the shared fixed executor.
 * An explicitly supplied native connection remains supported by composition.
 * @param {{directory:string,tenantId:string,ownerId:string,instanceId:string,ctx:any,
 * platformOrigin?:string,connection?:any,file?:string,modelFile?:string,allowColdObservation?:(fileLoaded?:boolean)=>boolean,authorizeLocal?:(context:any)=>Promise<boolean>}} options */
export function createManagedNetwork(options) {
  /** @type {Awaited<ReturnType<typeof createConnectRuntime>>|undefined} */ let runtime;
  /** @type {Promise<void>|undefined} */ let opening;
  let closed = false;
  /** @type {Awaited<ReturnType<typeof createManagedFileConnection>>|undefined} */ let fileConnection;
  let requireVerifiedFile = false;
  const coldAllowed = () =>
    !requireVerifiedFile && options.allowColdObservation?.() !== false;
  /** @type {ReturnType<typeof setInterval>|undefined} */ let timer;
  const lifetime = new AbortController();
  const valid = () => {
    const who = runtime?.identity();
    return (
      !closed &&
      who?.tenantId === options.tenantId &&
      who?.ownerId === options.ownerId
    );
  };
  function refresh() {
    if (closed || opening || (runtime && !fileConnection?.stale))
      return opening;
    if (fileConnection) requireVerifiedFile = true;
    const work = (async () => {
      if (runtime) {
        const previous = runtime;
        runtime = undefined;
        await previous.close();
      }
      if (closed) return;
      try {
        const connection =
          options.connection ??
          (options.file
            ? await createManagedFileConnection({
                file: options.file,
                modelFile: options.modelFile,
                directory: options.directory,
                tenantId: options.tenantId,
                ownerId: options.ownerId,
                instanceId: options.instanceId,
                platformOrigin: options.platformOrigin ?? '',
              })
            : undefined);
        fileConnection = options.connection ? undefined : connection;
        if (fileConnection) options.allowColdObservation?.(true);
        const candidate = await createConnectRuntime({
          directory: options.directory,
          config: connection?.config ?? {
            platformOrigin: options.platformOrigin ?? '',
          },
          ctx: options.ctx,
          connection,
          signal: lifetime.signal,
          expectedIdentity: {
            tenantId: options.tenantId,
            ownerId: options.ownerId,
          },
          authorizeLocal: options.authorizeLocal,
        });
        if (closed) {
          await candidate.close();
          return;
        }
        runtime = candidate;
      } catch {
        fileConnection?.ipc.close();
      }
    })();
    const pending = work.finally(() => {
      if (opening === pending) opening = undefined;
    });
    opening = pending;
    return pending;
  }
  return {
    start() {
      if (closed) return Promise.resolve();
      if (!timer) {
        timer = setInterval(() => {
          void refresh();
        }, 1000);
        timer.unref();
      }
      return refresh();
    },
    get ready() {
      return opening ?? Promise.resolve();
    },
    get sessionAvailable() {
      return valid();
    },
    get sessionRevoked() {
      // Keep denying while a previously loaded credential has been removed,
      // replaced or expired, even after refresh detaches its user runtime.
      return (
        fileConnection?.stale === true ||
        runtime?.broker.state.current()?.state === 'revoked'
      );
    },
    // The connection identifies one exact credential file. Refreshing its
    // access token preserves the scope, while replacing the file retires it.
    observationScope() {
      if (closed) return undefined;
      if (fileConnection) {
        const state = fileConnection.observationState;
        if (state === 'denied') requireVerifiedFile = true;
        return state === 'verified' || (state === 'pending' && coldAllowed())
          ? fileConnection
          : undefined;
      }
      if (
        !coldAllowed() ||
        runtime?.broker.state.current()?.state === 'revoked'
      )
        return undefined;
      return (options.connection && runtime) || lifetime;
    },
    get resourceSession() {
      return valid() ? runtime : undefined;
    },
    get baseTools() {
      return runtime?.baseTools;
    },
    get status() {
      return valid()
        ? runtime?.networkStatus
        : { available: false, connected: false, state: 'unavailable' };
    },
    get networkControl() {
      return valid() ? runtime?.networkControl : undefined;
    },
    get account() {
      return valid() ? { subject: options.ownerId } : undefined;
    },
    get consentAvailable() {
      return valid() && runtime?.consentAvailable === true;
    },
    consentUI(/** @type {any} */ context) {
      if (!runtime || !valid()) throw new Error('common.unauthenticated');
      return runtime.consentUI(context);
    },
    messagingUI(/** @type {any} */ context) {
      if (!runtime || !valid()) throw new Error('common.unauthenticated');
      return runtime.messagingUI(context);
    },
    attach(/** @type {any} */ provider) {
      return runtime?.attachNetwork(provider) ?? (() => {});
    },
    async attachModels(/** @type {any} */ llm) {
      await opening;
      return runtime?.attachModels(llm) ?? (() => {});
    },
    async logout() {
      if (!runtime || !valid()) return;
      await runtime.broker.state.command('logout');
    },
    async close() {
      closed = true;
      clearInterval(timer);
      lifetime.abort();
      const closing = runtime?.close();
      await opening?.catch(() => {});
      await closing;
      await runtime?.close();
      runtime = undefined;
    },
  };
}
