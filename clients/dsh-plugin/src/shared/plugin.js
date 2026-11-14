// @ts-nocheck
import { createSharedRuntime } from './runtime.js';
import { createEnterpriseAdapter } from './enterprise.js';
import { installSharedHost } from './host.js';
import { createSharedCarrier } from './carrier.js';

// R selects this entry instead of the dedicated-owner plugin for shared pools.
// Missing trusted composition never falls back to the owner adapter.
export const name = 'auriga-dsh-shared';
export const inject = [
  'webServer',
  'connection',
  'remoteAccess',
  'tools',
  'sessions',
  'agents',
  'aurigaSharedPorts',
];
export function apply(ctx) {
  const ports = ctx.aurigaSharedPorts;
  const runtime = createSharedRuntime({ ...ports.options, ports });
  const enterprise = createEnterpriseAdapter({
    runtime,
    ports: ports.enterprise,
    config: ports.config,
  });
  let host;
  try {
    host = installSharedHost(ctx, { runtime, enterprise, ports });
  } catch (e) {
    runtime.close();
    throw e;
  }
  ctx.provide('aurigaShared', host);
  const detach = ctx.webServer.register({
    kind: 'prefix',
    path: '/auriga/shared',
    handler: createSharedCarrier({ host, runtime }),
  });
  ctx.effect(() => detach, 'auriga-dsh: shared carrier');
  ctx.effect(() => () => host.close(), 'auriga-dsh: shared lifetime');
}
