import { openNetworkRuntime, isRevocationError } from './runtime.js';
import { networkIsolation } from './isolation.js';

/** Provider lifetime and login lifetime are independent. Late register/prepare
 * responses cannot resurrect an old login or a disposed native provider.
 * @param {{directory:string,ctx:any,installation?:import('./runtime-types.js').NetworkInstallation,defaultProvider?:import('./runtime-types.js').DeviceNetworkProvider,getAccess:()=>Promise<string>,authorizeLocal:(context:unknown)=>Promise<boolean>,open?:typeof openNetworkRuntime,onRevoked?:()=>void,backoff?:{baseMs:number,maxMs:number}}} options */
export function createNetworkLifecycle(options) {
  const installation = options.installation;
  let authenticated = false;
  let disposed = false;
  let generation = 0;
  /** @type {import('./runtime-types.js').NetworkProvider|undefined} */
  let provider = options.defaultProvider;
  let attachment = 0;
  /** @type {Awaited<ReturnType<typeof openNetworkRuntime>>|undefined} */
  let runtime;
  /** @type {AbortController|undefined} */
  let controller;
  let tail = Promise.resolve();
  let cleanup = Promise.resolve();
  let phase = provider ? 'idle' : 'unconfigured';
  function retire() {
    generation++;
    controller?.abort();
    controller = undefined;
    const previous = runtime;
    runtime = undefined;
    const stopping = previous?.close();
    // An uncooperative pending prepare owns no connection. Do not let its late
    // response block logout or a new login; valid() will retire its candidate.
    cleanup = Promise.allSettled([cleanup, stopping]).then(() => {});
    tail = cleanup;
  }
  function begin() {
    retire();
    if (disposed || !authenticated || !provider) {
      phase = disposed ? 'stopped' : provider ? 'idle' : 'unconfigured';
      return;
    }
    const expected = generation;
    const currentProvider = provider;
    const abort = new AbortController();
    controller = abort;
    phase = 'connecting';
    const valid = () =>
      !disposed &&
      authenticated &&
      expected === generation &&
      !abort.signal.aborted;
    tail = tail.then(async () => {
      let attempt = 0;
      while (valid()) {
        let candidate;
        let ended = false;
        /** @type {()=>void} */
        let notifyClosed = () => {};
        const endedPromise = new Promise((resolve) => {
          notifyClosed = () => {
            ended = true;
            abort.signal.removeEventListener('abort', notifyClosed);
            resolve(undefined);
          };
        });
        abort.signal.addEventListener('abort', notifyClosed, { once: true });
        try {
          const accessToken = await options.getAccess();
          if (!valid()) return;
          const prepared = await currentProvider.prepare({
            accessToken,
            signal: abort.signal,
            installation,
          });
          if (!valid()) return;
          const input = {
            directory: options.directory,
            ctx: options.ctx,
            getAccess: options.getAccess,
            authorizeLocal: options.authorizeLocal,
            onClosed() {
              if (ended) return;
              if (valid()) {
                runtime = undefined;
                phase = 'unavailable';
              }
              notifyClosed();
            },
            onRevoked: () => {
              if (valid()) {
                options.onRevoked?.();
                authenticated = false;
                begin();
              }
            },
          };
          candidate = await (options.open ?? openNetworkRuntime)(
            currentProvider.kind === 'device-connection'
              ? {
                  ...input,
                  kind: 'device-connection',
                  signal: abort.signal,
                  prepared:
                    /** @type {import('./runtime-types.js').PreparedConnection} */ (
                      prepared
                    ),
                }
              : {
                  ...input,
                  prepared:
                    /** @type {import('./runtime-types.js').PreparedNetwork} */ (
                      prepared
                    ),
                  ports: currentProvider.ports,
                },
          );
          if (!valid()) {
            await candidate.close();
            return;
          }
          runtime = candidate;
          await Promise.race([
            candidate.start(),
            endedPromise.then(() => {
              throw new Error('network runtime closed during start');
            }),
          ]);
          if (!valid()) {
            await candidate.close();
            return;
          }
          if (!ended) phase = 'running';
          await endedPromise;
          if (!valid()) return;
          throw new Error('network runtime closed');
        } catch (error) {
          abort.signal.removeEventListener('abort', notifyClosed);
          if (!valid()) return;
          await candidate?.close();
          if (!valid()) return;
          if (isRevocationError(error)) {
            options.onRevoked?.();
            authenticated = false;
            begin();
            return;
          }
          runtime = undefined;
          phase = 'unavailable';
          // Initial register/prepare uses the same bounded exponential retry
          // principle as transport ticket refresh. No synchronous retry loop.
          await new Promise((resolve) => {
            const wake = () => {
              clearTimeout(timer);
              abort.signal.removeEventListener('abort', wake);
              resolve(undefined);
            };
            const timer = setTimeout(
              wake,
              Math.min(
                options.backoff?.maxMs ?? 30000,
                (options.backoff?.baseMs ?? 500) * 2 ** Math.min(attempt++, 6),
              ),
            );
            timer.unref?.();
            abort.signal.addEventListener('abort', wake, { once: true });
            if (abort.signal.aborted) wake();
          });
        }
      }
    });
  }
  return {
    /** Cordis-owned trusted provider; not a serialized plugin configuration.
     * @param {import('./runtime-types.js').NetworkProvider} value */
    attach(value) {
      if (disposed) throw new Error('network lifecycle closed');
      provider = value;
      const token = ++attachment;
      begin();
      return () => {
        if (provider === value && token === attachment) {
          provider = options.defaultProvider;
          begin();
        }
      };
    },
    login() {
      if (!authenticated && !disposed) {
        authenticated = true;
        begin();
      }
    },
    logout() {
      authenticated = false;
      begin();
    },
    get status() {
      return (
        runtime?.status ?? {
          available: !!provider,
          isolation: networkIsolation,
          connected: false,
          state: phase,
        }
      );
    },
    consentUI(/** @type {unknown} */ context) {
      if (!runtime || !authenticated || disposed)
        throw new Error('network consent unavailable');
      return runtime.consentUI(context);
    },
    async close() {
      disposed = true;
      authenticated = false;
      begin();
      await tail;
    },
  };
}
