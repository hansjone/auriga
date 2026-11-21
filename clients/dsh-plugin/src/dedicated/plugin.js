// @ts-nocheck
import { createPublicKey } from 'node:crypto';
import { lstatSync } from 'node:fs';
import { canonical, fail } from '../shared/protocol.js';
import { createSharedRuntime, placementBinding } from '../shared/runtime.js';
import { createProductionSharedPorts } from '../shared/production-ports.js';
import { createSharedNodeTransport } from '../shared/node-transport.js';
import { createSharedDownstream } from '../shared/downstream.js';
import { loadHostSdk } from '../shared/host-sdk.js';
import { createEnterpriseAdapter } from '../shared/enterprise.js';
import {
  openSharedExecution,
  executionSession,
} from '../shared/task-context.js';
import { parseStrictJson } from '../resources/json.js';
import {
  readOwnedFile,
  normalizeBinding,
  sameBinding,
  RESOURCE_ROOT,
} from './intent.js';
import { createDedicatedResources, dedicatedQuiescent } from './resources.js';
import {
  DEDICATED_PROFILE,
  assertDedicatedProfile,
  createDedicatedProfileHandler,
} from './profile.js';
import { installDedicatedEnvironment } from './task-environment.js';
import { createDedicatedHost } from './host.js';
import { createDedicatedBrowser } from './browser.js';
import { createDedicatedNodeBridge } from './node-bridge.js';
import { createDedicatedEnterprisePorts } from './enterprise-ports.js';

export const name = 'auriga-dsh-dedicated';
export const inject = [
  'webServer',
  'connection',
  'remoteAccess',
  'typert',
  'typertGateway',
  'tools',
  'skills',
  'shellEnv',
  'pluginManager',
  'profileContext',
  'sessions',
  'sessionPersistence',
  'agents',
  'llm',
];
const audiences = ['auriga.shared.enterprise', 'auriga.shared.model'];
const required = [
  'resolve',
  'register',
  'lookup',
  'resourceSnapshot',
  'beginTask',
  'issueCapability',
  'finishTask',
  'authorizeCapability',
  'readCredentials',
  'enterpriseOutbound',
];
function keyMap(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    fail('common.validation_failed');
  const result = new Map();
  for (const [kid, pem] of Object.entries(value)) {
    if (!/^[A-Za-z0-9._-]{1,128}$/.test(kid) || typeof pem !== 'string')
      fail('common.validation_failed');
    const key = createPublicKey(pem);
    if (key.asymmetricKeyType !== 'ed25519') fail('common.validation_failed');
    result.set(kid, key);
  }
  if (!result.size) fail('common.unavailable');
  return result;
}

/** Installation-only composition. Neither mode, owner admission nor validator
 * is supplied by a browser/YAML executable callback. Node owns these files. */
export async function apply(ctx, config) {
  if (process.platform !== 'linux' || typeof config?.configFile !== 'string')
    fail('common.unavailable');
  const read = () => {
    const value = parseStrictJson(
      readOwnedFile(config.configFile, 262144),
      true,
    );
    value.binding = normalizeBinding(value.binding);
    const root = '/var/lib/auriga/pools/' + value.binding.physicalInstanceId;
    if (
      value.schemaVersion !== 1 ||
      value.tenantId !== value.binding.tenantId ||
      value.physicalInstanceId !== value.binding.physicalInstanceId ||
      config.configFile !== root + '/control/dedicated-host.json' ||
      value.nodeSocket !== root + '/control/shared-node.sock' ||
      typeof value.model !== 'string' ||
      !value.model ||
      !value.enterprise ||
      typeof value.enterprise !== 'object' ||
      Array.isArray(value.enterprise)
    )
      fail('common.validation_failed');
    return { ...value, skillRoots: [], profile: DEDICATED_PROFILE };
  };
  const initial = read(),
    binding = initial.binding;
  const settings = () => {
    const value = read();
    sameBinding(value.binding, binding);
    for (const key of [
      'tenantId',
      'physicalInstanceId',
      'identityKeys',
      'capabilityKeys',
    ])
      if (canonical(value[key]) !== canonical(initial[key]))
        fail('common.unavailable');
    return value;
  };
  if (!lstatSync(initial.nodeSocket).isSocket()) fail('common.unavailable');
  const options = {
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    identityKeys: keyMap(initial.identityKeys),
    capabilityKeys: Object.fromEntries(
      audiences.map((audience) => [
        audience,
        keyMap(initial.capabilityKeys?.[audience]),
      ]),
    ),
  };
  const transport = createSharedNodeTransport(initial.nodeSocket);
  const bridge = createDedicatedNodeBridge({ transport, settings, binding });
  const activeExecutions = new Set(),
    disposers = [];
  let ports,
    runtime,
    resources,
    environment,
    host,
    browser,
    downstream,
    closed = false,
    timer;
  const close = async () => {
    if (closed) return;
    closed = true;
    clearInterval(timer);
    browser?.close();
    for (const detach of disposers.reverse()) detach?.();
    await host?.close();
    await environment?.close();
    downstream?.close();
    runtime?.close();
    await resources?.close();
    await ports?.close();
    bridge.close();
  };
  try {
    const healthy = await bridge.call('health', {});
    if (
      healthy.protocol !== 'auriga.shared.node.v1' ||
      healthy.tenantId !== binding.tenantId ||
      healthy.physicalInstanceId !== binding.physicalInstanceId ||
      !Array.isArray(healthy.operations) ||
      required.some((operation) => !healthy.operations.includes(operation))
    )
      fail('common.unavailable');
    const sdk = await loadHostSdk();
    ports = createProductionSharedPorts(ctx, {
      settings,
      bridge,
      Peer: sdk.Peer,
      sdk,
      options,
      available: healthy.operations,
      profileValidator: (context, expected) =>
        assertDedicatedProfile(context, expected, resources),
    });
    // Pin *every* lookup/resolve, including native model/task request admission,
    // to the installation's complete unique member placement.
    const lookup = ports.lookup,
      resolve = ports.resolve;
    ports.lookup = async (id) => {
      settings();
      const result = await lookup(id);
      sameBinding(placementBinding(result), binding);
      settings();
      return result;
    };
    ports.resolve = async (current, platformSessionId) => {
      settings();
      sameBinding(current, binding);
      const result = await resolve(current, platformSessionId);
      sameBinding(result.binding, binding);
      settings();
      return result;
    };
    runtime = createSharedRuntime({
      ...options,
      ports,
      expectedMode: 'dedicated',
    });
    resources = createDedicatedResources(ctx, {
      binding,
      intentFile:
        '/var/lib/auriga/pools/' +
        binding.physicalInstanceId +
        '/control/dedicated-resources.json',
      artifactRoot: RESOURCE_ROOT,
      stateFile: '/home/dsh/.host/auriga-dedicated-resources/state.json',
      currentView: (current) => ports.resourceView(current),
      quiescent: () => dedicatedQuiescent(ctx, activeExecutions),
    });
    // Failure leaves the strict probe unavailable but retains a diagnostic
    // receipt; it never substitutes a fabricated ready resource set.
    await resources.reconcile();
    environment = installDedicatedEnvironment(ctx, {
      runtime,
      resources,
      activeExecutions,
      resolveTaskId: (token) => ports.enterprise.taskId(token),
      openTask: (execution) =>
        openSharedExecution({ ctx, runtime, ports, execution, raw: true }),
    });
    const enterprise = createEnterpriseAdapter({
      runtime,
      ports: createDedicatedEnterprisePorts(ports.enterprise, environment),
      config: ports.config,
    });
    disposers.push(
      ctx.webServer.register({
        kind: 'prefix',
        path: '/dsh-team',
        handler: enterprise.handler,
      }),
    );
    disposers.push(
      ctx.tools.register({
        name: 'auriga_enterprise_request',
        description: '调用平台企业应用',
        parameters: {
          type: 'object',
          properties: {
            url: { type: 'string' },
            method: { type: 'string' },
            headers: { type: 'object' },
            body: {},
            timeoutMs: { type: 'integer' },
          },
          additionalProperties: false,
        },
        output: {
          schema: { type: 'object', additionalProperties: true },
          render: (_args, value) => [
            { type: 'text', text: JSON.stringify(value) },
          ],
        },
        async execute(args, execution) {
          const task = environment.companyTask(execution);
          return enterprise.outbound({
            remoteAddress: '127.0.0.1',
            signal: execution.signal,
            body: {
              ...args,
              dshSessionId: executionSession(ctx, execution),
              taskCapability: task.token,
            },
          });
        },
      }),
    );
    disposers.push(
      ctx.on('agent/request', async ({ agent }, next) => {
        const id = executionSession(ctx, { agent });
        await resources.verify();
        await runtime.session(id);
        const result = await next();
        await resources.verify();
        await runtime.session(id);
        return result;
      }),
    );
    downstream = createSharedDownstream({
      runtime,
      ports: ports.downstream,
      sdk,
    });
    disposers.push(
      ctx.llm.registerAdapter(['auriga-shared'], downstream.adapter),
    );
    host = createDedicatedHost(ctx, { runtime, ports, binding, resources });
    browser = createDedicatedBrowser(ctx, {
      host,
      distIndex: config.distIndex,
    });
    for (const path of ['/', '/index.html'])
      disposers.push(
        ctx.webServer.register({
          kind: 'exact',
          path,
          handler: browser.handler,
        }),
      );
    disposers.push(
      ctx.webServer.register({
        kind: 'prefix',
        path: '/auriga/dedicated/browser',
        handler: browser.handler,
      }),
    );
    const profile = createDedicatedProfileHandler({
      ctx,
      resources,
      physicalInstanceId: binding.physicalInstanceId,
    });
    for (const path of [
      '/_auriga/dedicated-profile',
      '/_auriga/dedicated-resources',
    ])
      disposers.push(
        ctx.webServer.register({ kind: 'exact', path, handler: profile }),
      );
    disposers.push(
      ctx.on('connection/request', (_req, res) => {
        res.writeHead(403, { 'cache-control': 'no-store' });
        res.end();
      }),
    );
    ctx.provide('aurigaDedicated', {
      binding: Object.freeze({ ...binding }),
      resources,
    });
    timer = setInterval(() => {
      void resources.reconcile().catch(() => resources.invalidate());
    }, 1000);
    timer.unref?.();
    ctx.effect(() => close, 'auriga-dsh: dedicated lifetime');
  } catch (error) {
    await close();
    throw error;
  }
}
