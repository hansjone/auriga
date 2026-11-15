// @ts-nocheck
import { createSharedRuntime } from './runtime.js';
import { createEnterpriseAdapter } from './enterprise.js';
import { installSharedHost } from './host.js';
import { createSharedCarrier } from './carrier.js';
import { createSharedDownstream } from './downstream.js';
import { openSharedTask } from './task-context.js';
import { createEnterpriseOutbound } from './enterprise-transport.js';

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
  'llm',
  'aurigaSharedPorts',
];
export function apply(ctx) {
  const ports = ctx.aurigaSharedPorts;
  const runtime = createSharedRuntime({ ...ports.options, ports });
  const enterprise = createEnterpriseAdapter({
    runtime,
    ports: {
      ...ports.enterprise,
      outbound:
        ports.enterprise.outbound ??
        createEnterpriseOutbound({ config: ports.config }),
    },
    config: ports.config,
  });
  let host;
  try {
    host = installSharedHost(ctx, { runtime, enterprise, ports });
  } catch (e) {
    runtime.close();
    throw e;
  }
  let downstream, detachModels;
  try {
    if (!ports.downstream) throw new Error('common.unavailable');
    downstream = createSharedDownstream({ runtime, ports: ports.downstream });
    detachModels = ctx.llm.registerAdapter(
      ['auriga-shared'],
      downstream.adapter,
    );
    if (typeof detachModels !== 'function')
      throw new Error('common.unavailable');
  } catch (error) {
    downstream?.close();
    host.close();
    throw error;
  }
  ctx.provide('aurigaShared', {
    ...host,
    downstream,
    openTask: (execution) =>
      openSharedTask({ ctx, runtime, ports, execution, raw: true }),
  });
  let detach;
  try {
    detach = ctx.webServer.register({
      kind: 'prefix',
      path: '/auriga/shared',
      handler: createSharedCarrier({ host, runtime }),
    });
  } catch (error) {
    detachModels();
    downstream.close();
    host.close();
    throw error;
  }
  ctx.effect(() => detach, 'auriga-dsh: shared carrier');
  ctx.effect(
    () => () => {
      detachModels();
      downstream.close();
      host.close();
    },
    'auriga-dsh: shared lifetime',
  );
}
