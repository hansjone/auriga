// @ts-nocheck
import { createLocalResources } from './service.js';
import { createLocalResourceHandler, LOCAL_RESOURCES_PATH } from './http.js';

export function installLocalResources(ctx, mode) {
  let host,
    live = true;
  ctx.effect(
    () => () => {
      live = false;
      host = undefined;
    },
    'auriga: local resources lifetime',
  );
  ctx.inject?.(['pluginManager'], (scope) => {
    host = scope;
    scope.effect(() => () => {
      if (host === scope) host = undefined;
    });
  });
  const service = createLocalResources({
    mode,
    getHost: () => host,
    alive: () => live,
  });
  ctx.effect(
    () =>
      ctx.webServer.register({
        kind: 'prefix',
        path: LOCAL_RESOURCES_PATH,
        handler: createLocalResourceHandler({
          auth: ctx.connection,
          service,
          alive: () => live,
        }),
      }),
    'auriga: local resources route',
  );
}
