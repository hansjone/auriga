// @ts-nocheck
import { createSharedRuntime } from './runtime.js';
import { createEnterpriseAdapter } from './enterprise.js';
import { installSharedHost } from './host.js';
import { createSharedCarrier } from './carrier.js';
import { createSharedDownstream } from './downstream.js';
import { installSharedExecutionEnvironment } from './task-environment.js';
import { createSharedProfileHandler } from './profile.js';
import { createSharedMessageManager } from './message-manager.js';

// R selects this entry instead of the dedicated-owner plugin for shared pools.
// Missing trusted composition never falls back to the owner adapter.
export const name = 'auriga-dsh-shared';
export const inject = [
  'webServer',
  'connection',
  'remoteAccess',
  'tools',
  'skills',
  'workingDirectory',
  'shellEnv',
  'sessions',
  'sessionPersistence',
  'agents',
  'llm',
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
  const messaging = createSharedMessageManager(ctx, { runtime, ports });
  const taskEnvironment = installSharedExecutionEnvironment(ctx, {
    runtime,
    ports,
  });
  let host;
  try {
    host = installSharedHost(ctx, {
      runtime,
      enterprise,
      ports,
      messaging,
      taskEnvironment,
    });
  } catch (e) {
    void taskEnvironment.close();
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
    void taskEnvironment.close();
    downstream?.close();
    host.close();
    throw error;
  }
  ctx.provide('aurigaShared', {
    ...host,
    downstream,
    messaging,
    openTask: (execution) => taskEnvironment.open(execution),
  });
  // Message trust/cache outages degrade messages only. They cannot prevent
  // the model adapter or existing controlled tools from starting.
  void messaging.start().catch(() =>
    ctx.logger?.warn?.('Auriga shared message receiving is unavailable', {
      code: 'common.unavailable',
    }),
  );
  const detachDisposed = ctx.on('agent/disposed', ({ agent }) =>
    messaging.retire(agent),
  );
  let detach, detachProfile;
  try {
    detach = ctx.webServer.register({
      kind: 'prefix',
      path: '/auriga/shared',
      handler: createSharedCarrier({ host, runtime }),
    });
    detachProfile = ctx.webServer.register({
      kind: 'prefix',
      path: '/_auriga/shared-profile',
      handler: createSharedProfileHandler(ports),
    });
  } catch (error) {
    void taskEnvironment.close();
    detach?.();
    detachModels();
    downstream.close();
    detachDisposed?.();
    void messaging.close();
    host.close();
    throw error;
  }
  ctx.effect(() => detach, 'auriga-dsh: shared carrier');
  ctx.effect(() => detachProfile, 'auriga-dsh: shared runtime profile');
  ctx.effect(
    () => async () => {
      detachDisposed?.();
      await messaging.close();
      await taskEnvironment.close();
      detachModels();
      downstream.close();
      host.close();
    },
    'auriga-dsh: shared lifetime',
  );
}
