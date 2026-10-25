// Explicit source audit: first npm ci --ignore-scripts in deploy/compose/real-dsh.
// Not part of the standalone plugin's dependency-free default test glob.
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runInNewContext } from 'node:vm';
import {
  readRuntimeFingerprint,
  verifyRuntimeFingerprint,
} from '../../../../src/network/host/native/runtime.js';
import { hasNativePromptRequest } from '../../../../src/network/host/native/upstream.js';
import {
  createNativeHostPort,
  createNativeSourceResolver,
} from '../../../../src/network/host/native/index.js';
import { fixture } from './fixtures.js';

const root = new URL(
  '../../../../../../deploy/compose/real-dsh/node_modules/',
  import.meta.url,
);
const artifact = (name) => new URL(`@deepseek-ai/${name}/lib/index.js`, root);
const { SessionController } = await import(
  artifact('dsh-api-session-controller').href
);
const { SessionStore } = await import(artifact('dsh-session').href);
const { Context } = await import(artifact('cordis').href);

test('public native factory enables without an isolation service after functional artifact checks', async () => {
  const f = fixture();
  const host = await createNativeHostPort({
    ctx: f.ctx,
    runtime: {
      manifestPath: fileURLToPath(
        new URL('@deepseek-ai/dsh/package.json', root),
      ),
      controllerPath: fileURLToPath(artifact('dsh-api-session-controller')),
      persistencePath: fileURLToPath(artifact('dsh-session-persistence-jsonl')),
      sessionPath: fileURLToPath(artifact('dsh-session')),
      agentLoopPath: fileURLToPath(artifact('dsh-agent-loop')),
    },
  });
  assert.equal(host.getDiagnostics().isolation.message, '未隔离（开发阶段）');
  assert.equal(await host.hasDurableProvider('session-a'), false);
  await assert.rejects(
    host.createSession({ id: 'session-a', cwd: '/work/project', preset: null }),
    { code: 'network.host_incompatible' },
  );
});

test('public gate still rejects wrong versions, changed source bytes and missing artifacts without isolation', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-native-gate-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const { writeFile } = await import('node:fs/promises');
  const manifestPath = join(dir, 'package.json');
  const controllerPath = join(dir, 'controller.js');
  await writeFile(manifestPath, JSON.stringify({ version: '0.0.0' }));
  await writeFile(controllerPath, 'changed source');
  const runtime = {
    manifestPath: fileURLToPath(new URL('@deepseek-ai/dsh/package.json', root)),
    controllerPath: fileURLToPath(artifact('dsh-api-session-controller')),
    persistencePath: fileURLToPath(artifact('dsh-session-persistence-jsonl')),
    sessionPath: fileURLToPath(artifact('dsh-session')),
    agentLoopPath: fileURLToPath(artifact('dsh-agent-loop')),
  };
  for (const invalid of [
    { ...runtime, manifestPath },
    { ...runtime, controllerPath },
    { ...runtime, sessionPath: join(dir, 'missing') },
  ]) {
    await assert.rejects(
      createNativeHostPort({ ctx: fixture().ctx, runtime: invalid }),
      { code: 'network.host_incompatible' },
    );
  }
});

test('public factory creates, cold-restores and flushes through real JSONL without isolation services', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-native-enabled-'));
  const jsonlRoot = join(dir, 'sessions');
  const cwd = join(dir, 'workspace');
  await mkdir(jsonlRoot);
  await mkdir(cwd);
  const { default: JsonlPersistence } = await import(
    artifact('dsh-session-persistence-jsonl').href
  );
  const owner = new Context();
  const provider = new JsonlPersistence(owner, {
    root: jsonlRoot,
    compression: 'none',
  });
  const store = new SessionStore(owner);
  const agents = new Map(),
    detachers = new Map();
  let created = 0,
    resumed = 0;
  t.after(async () => {
    for (const handle of [...provider.tracker.openHandles])
      await handle.close();
    for (const detach of detachers.values()) await detach();
    await rm(dir, { recursive: true, force: true });
  });
  const snapshot = async (id) => {
    const reader = await provider.open(id, 'read');
    try {
      return { meta: reader.header, events: (await reader.read()).events };
    } finally {
      await reader.close();
    }
  };
  const attach = (id, meta, events = []) => {
    const session = store.prepare(id, { meta, seed: events });
    detachers.set(id, store.enter(session));
    const agent = { id, session, inbox: { nextTurn: [], nextStep: [] } };
    agents.set(id, agent);
    return agent;
  };
  const ctx = {
    sessionPersistence: owner.get('sessionPersistence'),
    sessions: store,
    agents: { get: (id) => agents.get(id), isOwnedBy: () => false },
    workspaceRegistry: { archivedSessionIds: [] },
    agentPresets: { resolve: async (id) => ({ id: id ?? 'standard' }) },
    sessionProjections: { stateOf: (session) => session.header.agentPreset },
    sessionQuery: {
      observeSession: async (id) => {
        const data = await snapshot(id);
        return {
          header: data.meta,
          events: data.events,
          inheritedEventCount: 0,
          [Symbol.dispose]() {},
        };
      },
    },
  };
  const controller = Object.create(SessionController.prototype);
  controller.ctx = ctx;
  controller.commands = {
    create: async (input) => {
      created++;
      const agent = attach(input.sessionId, {
        cwd: input.cwd,
        agentPreset: input.agentPreset,
      });
      await provider.create(agent.session.header);
      return { sessionId: input.sessionId, agentPreset: input.agentPreset };
    },
  };
  controller.agents = {
    resolveAgent: async (id) => {
      if (agents.has(id)) return { agent: agents.get(id) };
      resumed++;
      const data = await snapshot(id);
      const agent = attach(id, data.meta, data.events);
      await provider.open(id, 'write');
      return { agent };
    },
  };
  ctx.sessionController = controller;
  const runtime = {
    manifestPath: fileURLToPath(new URL('@deepseek-ai/dsh/package.json', root)),
    controllerPath: fileURLToPath(artifact('dsh-api-session-controller')),
    persistencePath: fileURLToPath(artifact('dsh-session-persistence-jsonl')),
    sessionPath: fileURLToPath(artifact('dsh-session')),
    agentLoopPath: fileURLToPath(artifact('dsh-agent-loop')),
  };
  const host = await createNativeHostPort({ ctx, runtime });
  assert.deepEqual(
    await host.createSession({ id: 'native-created', cwd, preset: null }),
    { id: 'native-created', cwd },
  );
  assert.equal(await host.hasDurableProvider('native-created'), true);
  assert.equal(await host.flush('native-created'), true);
  const writer = provider.tracker.writers.get('native-created');
  await writer.close();
  assert.equal(await host.hasDurableProvider('native-created'), false);
  assert.equal(await host.flush('native-created'), false);
  await detachers.get('native-created')();
  detachers.delete('native-created');
  agents.delete('native-created');
  assert.equal((await host.getSession('native-created')).cwd, cwd);
  assert.equal(await host.flush('native-created'), true);
  assert.equal(created, 1);
  assert.equal(resumed, 1);
  assert.equal(host.getDiagnostics().isolation.message, '未隔离（开发阶段）');
  const resolveSource = await createNativeSourceResolver({
    ctx,
    runtime,
    lookupBinding: async () => ({
      local: '1',
      bindingId: 'binding-a',
      hostSessionId: 'native-created',
      cwd,
      state: 'bound',
      lifecycle: 'open',
      kind: 'session',
    }),
  });
  assert.equal(
    (await resolveSource({ agent: agents.get('native-created') })).local,
    '1',
  );
});

test('actual locked npm artifacts match every recorded source digest', async () => {
  const fingerprint = await readRuntimeFingerprint({
    manifestPath: fileURLToPath(new URL('@deepseek-ai/dsh/package.json', root)),
    controllerPath: fileURLToPath(artifact('dsh-api-session-controller')),
    persistencePath: fileURLToPath(artifact('dsh-session-persistence-jsonl')),
    sessionPath: fileURLToPath(artifact('dsh-session')),
    agentLoopPath: fileURLToPath(artifact('dsh-agent-loop')),
  });
  assert.equal(verifyRuntimeFingerprint(fingerprint), true);
});

test('actual native controller keeps request identity/queue and only checks signal before admission', async () => {
  const request = {
    sessionId: 'native-a',
    requestId: 'r',
    mode: 'queue',
    content: [{ type: 'text', text: 'body' }],
  };
  let seen;
  const controller = {
    commands: {
      prompt: async (value) => {
        seen = value;
        return { accepted: true };
      },
    },
  };
  assert.deepEqual(
    await SessionController.prototype.prompt.call(
      controller,
      request,
      new AbortController().signal,
    ),
    { accepted: true },
  );
  assert.equal(seen, request);
  const canceled = new AbortController();
  canceled.abort();
  seen = undefined;
  assert.throws(
    () =>
      SessionController.prototype.prompt.call(
        controller,
        request,
        canceled.signal,
      ),
    { name: 'AbortError' },
  );
  assert.equal(seen, undefined);
});

test('actual native inspect reads attached and cold state without create/resume', async () => {
  const header = { id: 'native-a', cwd: '/work/native' };
  const events = [];
  const ctx = {
    sessions: {
      get: () => ({
        header,
        inheritedEventCount: 0,
        snapshotEvents: () => events,
      }),
    },
  };
  assert.equal(
    (await SessionController.prototype.inspect.call({ ctx }, 'native-a')).meta,
    header,
  );
  let disposed = false,
    observed;
  ctx.sessions.get = () => undefined;
  ctx.sessionQuery = {
    observeSession: async (id, options) => {
      observed = { id, options };
      return {
        header,
        events,
        inheritedEventCount: 0,
        [Symbol.dispose]() {
          disposed = true;
        },
      };
    },
  };
  const cold = await SessionController.prototype.inspect.call(
    { ctx },
    'native-a',
  );
  assert.equal(cold.meta, header);
  assert.equal(observed.options.projectionMode, 'none');
  assert.equal(disposed, true);
});

test('actual SessionStore flush distinguishes no listener, awaiting listener and rejection', async () => {
  const ctx = new Context();
  const store = new SessionStore(ctx);
  const session = store.prepare('native-flush', {
    meta: { cwd: process.cwd() },
  });
  const detach = store.enter(session);
  try {
    assert.equal(await store.flush(session), false);
    let release, entered;
    const started = new Promise((resolve) => {
      entered = resolve;
    });
    const remove = ctx.on(
      'session/flush',
      () =>
        new Promise((resolve) => {
          release = resolve;
          entered();
        }),
    );
    let finished = false;
    const flushing = store.flush(session).then((result) => {
      finished = true;
      return result;
    });
    await started;
    assert.equal(finished, false);
    release();
    assert.equal(await flushing, true);
    remove();
    const off = ctx.on('session/flush', () => {
      throw new Error('storage failed');
    });
    await assert.rejects(store.flush(session), /storage failed/);
    off();
  } finally {
    await detach();
  }
});

test('execute exact upstream inbox getter and request lookup source, without inventing queue fields', async () => {
  const controllerSource = await readFile(
    artifact('dsh-api-session-controller'),
    'utf8',
  );
  const lookup = controllerSource.slice(
    controllerSource.indexOf('function hasPromptRequest('),
    controllerSource.indexOf('\nfunction imageBlockIn('),
  );
  assert.match(lookup, /inbox\.nextTurn/);
  const nativeLookup = runInNewContext(`(${lookup})`);
  const loopSource = await readFile(artifact('dsh-agent-loop'), 'utf8');
  const start = loopSource.indexOf('var ReactLoopInbox = class {');
  assert.ok(start >= 0);
  const end = loopSource.indexOf('\n};', start) + 3;
  const Inbox = runInNewContext(
    `(()=>{${loopSource.slice(start, end)};return ReactLoopInbox;})()`,
  );
  const state = { 'next-turn': [], 'next-step': [] };
  const session = { id: 'native-inbox', snapshotEvents: () => [] };
  const inbox = new Inbox({ stateOf: () => state }, session, { emit() {} });
  const agent = { session, inbox };
  for (const target of ['next-turn', 'next-step']) {
    state[target].push({ source: { kind: 'user', rpcId: 'r' } });
    assert.equal(nativeLookup(agent, 'r'), true);
    assert.equal(hasNativePromptRequest(agent, 'r'), true);
    state[target].length = 0;
  }
  assert.equal(nativeLookup(agent, 'r'), false);
  assert.equal(hasNativePromptRequest(agent, 'r'), false);
  assert.match(
    loopSource,
    /followup\(input\)\s*\{\s*this\.send\(input, "next-turn", true\);/,
  );
});
