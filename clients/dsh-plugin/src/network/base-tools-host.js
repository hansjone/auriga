import { registerBaseTools } from './base-tools.js';

/** Installation-wide tools survive absent profiles, credentials and connection delegates.
 * @param {any} ctx @param {()=>any} current */
export function installBaseToolsHost(ctx, current) {
  return ctx.inject(
    [
      'tools',
      'sessions',
      'sessionController',
      'agents',
      'sessionPersistence',
      'workspaceRegistry',
    ],
    (/** @type {any} */ host) => {
      let closed = false;
      const source = () => {
        if (closed) throw new Error('network.unauthorized');
        return current();
      };
      const unavailable = () => ({
        reason: 'unconfigured',
        loggedIn: false,
        verified: false,
      });
      /** @param {'readLocal'|'probe'} method @param {any[]} args */
      const call = async (method, args) => {
        const port = source();
        if (!port) {
          if (method === 'readLocal') return null;
          throw new Error('unconfigured');
        }
        const generation = port.generation();
        const result = await port[method](...args);
        if (source() !== port || generation !== port.generation())
          throw new Error('network.unauthorized');
        return result;
      };
      const dispose = registerBaseTools({
        ctx: host,
        getState: () => {
          const port = source();
          const generation = port?.generation();
          return {
            ...(port?.getState() ?? unavailable()),
            assertCurrent: () => {
              if (source() !== port || generation !== port?.generation())
                throw new Error('network.unauthorized');
            },
          };
        },
        readLocal: () => call('readLocal', []),
        probe: (path, signal) => call('probe', [path, signal]),
      });
      host.effect(
        () => () => {
          closed = true;
          dispose();
        },
        'auriga-dsh: installation network tools',
      );
    },
  );
}
