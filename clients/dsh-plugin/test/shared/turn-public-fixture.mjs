import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { canonical } from '../../src/shared/protocol.js';
import {
  placementBinding,
  createSharedRuntime,
} from '../../src/shared/runtime.js';
import { createProductionSharedPorts } from '../../src/shared/production-ports.js';
import { createSharedDownstream } from '../../src/shared/downstream.js';
import { installSharedExecutionEnvironment } from '../../src/shared/task-environment.js';
import { SHARED_PROFILE } from '../../src/shared/host.js';

export async function publicTurn(
  t,
  { toolEnvironment = true, mode = 'shared' } = {},
) {
  if (!['shared', 'dedicated'].includes(mode))
    throw new Error('Unknown fixture mode');
  const hostRequire = createRequire(
    join(process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT, 'package.json'),
  );
  const load = (name) =>
    import(pathToFileURL(hostRequire.resolve('@deepseek-ai/' + name)).href);
  const [
    { Context },
    sessions,
    projections,
    agents,
    prompt,
    tools,
    llm,
    loop,
    env,
    { Peer },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-session'),
    load('dsh-session-projection'),
    load('dsh-agent'),
    load('dsh-system-prompt'),
    load('dsh-tools'),
    load('dsh-llm'),
    load('dsh-agent-loop'),
    load('dsh-shell-env'),
    load('dsh-client-connection'),
  ]);
  const ctx = new Context();
  for (const [module, config] of [
    [sessions, {}],
    [projections, {}],
    [agents, {}],
    [prompt, { includeHarnessIdentity: false, includeRuntimeContext: false }],
    [tools, { mode: 'native' }],
    [llm, {}],
    [loop, { agents: [], maxParallelToolCalls: 1 }],
    [env, {}],
  ])
    await ctx.plugin(module.default ?? module, config);
  // The web listener is outside this no-network composition; only the owned
  // loopback URL is needed by the actual execution environment contributor.
  ctx.provide('webServer', { port: 41234 });
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode,
    epoch: 1,
    revision: 1,
    sessionId: randomUUID(),
    platformSessionId: randomUUID(),
    authGeneration: 1,
  };
  const pair = generateKeyPairSync('ed25519'),
    keys = new Map([['fixture', pair.publicKey]]);
  const state = {
    company: false,
    version: 7,
    active: new Map(),
    tokens: new Map(),
    events: [],
    models: 0,
  };
  const token = (request) => {
    const now = Math.floor(Date.now() / 1000);
    const claims = {
      schemaVersion: 1,
      binding,
      taskId: request.taskId,
      audience: request.audience,
      scopes: request.scopes,
      credentialVersion: request.scopes.includes('task.execute')
        ? 100
        : state.version,
      issuedAt: now,
      expiresAt: now + 600,
      jti: randomUUID(),
    };
    const raw = [
      { alg: 'EdDSA', kid: 'fixture', typ: 'auriga-shared-capability+jwt' },
      claims,
    ]
      .map((part) => Buffer.from(canonical(part)).toString('base64url'))
      .join('.');
    const value =
      raw +
      '.' +
      sign(null, Buffer.from(raw), pair.privateKey).toString('base64url');
    state.tokens.set(value, claims);
    return value;
  };
  const bridge = {
    async call(operation, body) {
      state.events.push({ operation, body });
      if (operation === 'lookup') return { binding: { ...binding } };
      if (operation === 'resolve')
        return {
          state: 'ready',
          binding: placementBinding(binding),
          authGeneration: 1,
        };
      if (operation === 'resourceSnapshot')
        return {
          view: {
            schemaVersion: 1,
            binding: placementBinding(binding),
            viewId: randomUUID(),
            source: 'platform',
            authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
            resources: [],
          },
        };
      if (operation === 'beginTask') {
        if (body.scopes.includes('task.execute'))
          assert.equal(body.parentCapability, undefined);
        else {
          const parent = state.tokens.get(body.parentCapability);
          assert.ok(
            parent && state.active.has(parent.taskId),
            'child requires live ordinary opaque token',
          );
          assert.deepEqual(parent.scopes, ['task.execute']);
        }
        await state.beforeBegin?.(body);
        state.active.set(body.taskId, body);
        return { token: token(body) };
      }
      if (operation === 'issueCapability') {
        assert.ok(state.active.has(body.taskId));
        return { token: token(body) };
      }
      if (operation === 'authorizeCapability')
        return {
          active: state.active.has(body.capability.taskId),
          credentialVersion: body.capability.scopes.includes('task.execute')
            ? 100
            : state.version,
          allowRawAgentToken: true,
          enterpriseCredentialState:
            'SHARED_ENTERPRISE_CREDENTIAL_STATE_' +
            (state.company ? 'AVAILABLE' : 'ABSENT'),
        };
      if (operation === 'readCredentials')
        return {
          empNo: '00001234',
          token: 'synthetic-company-material',
          credentialVersion: state.version,
        };
      if (operation === 'finishTask') {
        await state.beforeFinish?.(body);
        if (state.finishEcho) return state.finishEcho(body);
        state.active.delete(body.taskId);
        return { fenced: true, binding: body.binding, taskId: body.taskId };
      }
      throw new Error('unexpected fixture operation ' + operation);
    },
    async *stream(operation, request, signal) {
      assert.equal(operation, 'model');
      state.models++;
      yield* state.model(request, signal);
    },
    close() {},
  };
  const options = {
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    identityKeys: keys,
    capabilityKeys: Object.fromEntries(
      ['enterprise', 'model', 'message'].map((name) => [
        'auriga.shared.' + name,
        keys,
      ]),
    ),
  };
  const ports = createProductionSharedPorts(ctx, {
    options,
    bridge,
    Peer,
    sdk: { Peer, LlmAdapter: llm.LlmAdapter },
    profileValidator: () => true,
    settings: () => ({
      profile: SHARED_PROFILE,
      skillRoots: [],
      model: 'fixture',
      enterprise: {},
    }),
  });
  const runtime = createSharedRuntime({
    ...options,
    ports,
    expectedMode: mode,
  });
  const environment =
    toolEnvironment &&
    installSharedExecutionEnvironment(ctx, {
      runtime,
      ports,
    });
  const downstream = createSharedDownstream({
    runtime,
    ports: ports.downstream,
    sdk: ports.sdk,
  });
  const detach = ctx.llm.registerAdapter(['auriga-shared'], downstream.adapter);
  const handle = await ctx.agents.create({
    sessionId: binding.sessionId,
    meta: { cwd: '/fixture/work' },
    agentOptions: { provider: 'auriga-shared', model: 'fixture' },
  });
  const agent = ctx.agents.get(binding.sessionId);
  t.after(async () => {
    await handle.dispose();
    await environment?.close?.();
    downstream.close();
    detach();
    if (state.expectCloseFailure)
      await assert.rejects(ports.close(), state.expectCloseFailure);
    else await ports.close();
    runtime.close();
    await ctx.fiber.dispose();
  });
  return {
    ctx,
    binding,
    state,
    ports,
    runtime,
    environment,
    downstream,
    agent,
    prompt(text = 'fixture') {
      agent.followup(
        llm.createUserMessage({
          content: [{ type: 'text', text }],
          source: { kind: 'user' },
        }),
      );
      return agent.whenIdle();
    },
  };
}
