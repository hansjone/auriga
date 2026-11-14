import { createConnectRuntime } from '../local/runtime.js';

/** Managed hosts inherit the same authenticated broker; no instance/device token
 * file is parsed, refreshed, exchanged or sent by the plugin.
 * @param {{directory:string,tenantId:string,ownerId:string,instanceId:string,ctx:any,
 * platformOrigin?:string,connection?:any,authorizeLocal?:(context:any)=>Promise<boolean>}} options */
export function createManagedNetwork(options) {
  /** @type {Awaited<ReturnType<typeof createConnectRuntime>>|undefined} */ let runtime;
  /** @type {Promise<void>|undefined} */ let opening;
  let closed = false;
  const valid = () => {
    const who = runtime?.identity();
    return (
      !closed &&
      who?.tenantId === options.tenantId &&
      who?.ownerId === options.ownerId
    );
  };
  return {
    start() {
      if (closed) return opening ?? Promise.resolve();
      if (opening) return opening;
      opening = (async () => {
        const candidate = await createConnectRuntime({
          directory: options.directory,
          config: { platformOrigin: options.platformOrigin ?? '' },
          ctx: options.ctx,
          connection: options.connection,
          expectedIdentity: {
            tenantId: options.tenantId,
            ownerId: options.ownerId,
          },
        });
        if (closed) {
          await candidate.close();
          return;
        }
        runtime = candidate;
      })();
      return opening;
    },
    get ready() {
      return opening ?? Promise.resolve();
    },
    get sessionAvailable() {
      return valid();
    },
    get sessionRevoked() {
      return runtime?.broker.state.current()?.state === 'revoked';
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
      const closing = runtime?.close();
      await opening?.catch(() => {});
      await closing;
      await runtime?.close();
      runtime = undefined;
    },
  };
}
