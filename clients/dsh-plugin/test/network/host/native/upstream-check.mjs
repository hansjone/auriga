// Explicit baseline integration; requires the existing real-dsh lockfile install.
// Resolve public exports from that installation, never import internal paths.
import assert from 'node:assert/strict';
import test from 'node:test';
import { createRequire } from 'node:module';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import ts from 'typescript';
import {
  createNativeHostPort,
  createNativeSourceResolver,
} from '../../../../src/network/host/native/index.js';
import { fixture } from './fixtures.js';

const baseline = new URL(
  '../../../../../../deploy/compose/real-dsh/',
  import.meta.url,
);
const requireBaseline = createRequire(new URL('package.json', baseline));
const load = (name) =>
  import(pathToFileURL(requireBaseline.resolve(name)).href);
const { Context } = await load('@deepseek-ai/cordis');
const { SessionStore } = await load('@deepseek-ai/dsh-session');
const { SessionController } = await load(
  '@deepseek-ai/dsh-api-session-controller',
);
const { default: JsonlPersistence } = await load(
  '@deepseek-ai/dsh-session-persistence-jsonl',
);

async function storage(t) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-public-host-'));
  const root = join(dir, 'sessions');
  await mkdir(root);
  const owner = new Context();
  const provider = new JsonlPersistence(owner, { root, compression: 'none' });
  const store = new SessionStore(owner);
  const handles = new Map(),
    detachers = new Map();
  t.after(async () => {
    for (const handle of handles.values()) await handle.close();
    for (const detach of detachers.values()) detach();
    await rm(dir, { recursive: true, force: true });
  });
  async function create(id = 'real') {
    const session = store.prepare(id, { meta: { cwd: dir } });
    detachers.set(id, store.enter(session));
    handles.set(id, await provider.create(session.header));
    return session;
  }
  async function inspect(id) {
    const handle = await provider.open(id, 'read');
    try {
      return { meta: handle.header, events: (await handle.read()).events };
    } finally {
      await handle.close();
    }
  }
  return { dir, owner, provider, store, handles, detachers, create, inspect };
}

test('baseline is rc.2 and public controller exposes the selected service methods', () => {
  assert.equal(
    requireBaseline('@deepseek-ai/dsh/package.json').version,
    '0.2.0-rc.2',
  );
  for (const method of [
    'create',
    'inspect',
    'resolveAgent',
    'prompt',
    'projections',
  ])
    assert.equal(typeof SessionController.prototype[method], 'function');
});

test('minimal host descriptor typechecks against rc.2 public exports', () => {
  const program = ts.createProgram(
    [fileURLToPath(new URL('baseline-contract.ts', import.meta.url))],
    {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
      moduleResolution: ts.ModuleResolutionKind.Bundler,
      baseUrl: fileURLToPath(new URL('node_modules/', baseline)),
      strict: true,
      skipLibCheck: true,
      noEmit: true,
      types: [],
    },
  );
  const errors = ts.getPreEmitDiagnostics(program);
  assert.deepEqual(
    errors.map((d) => ts.flattenDiagnosticMessageText(d.messageText, ' ')),
    [],
  );
});

test('public JSONL barrier materializes a header without inspecting writer internals', async (t) => {
  const s = await storage(t);
  const session = await s.create();
  assert.equal(typeof s.provider.identity, 'symbol');
  assert.equal((await s.provider.stat(session.id)).header.id, session.id);
  assert.equal(await s.store.flush(session), true);
  assert.equal(await s.provider.flush(), undefined);
  assert.equal((await s.inspect(session.id)).meta.cwd, s.dir);
});

test('public SessionStore flush reports missing listeners rather than durability success', async () => {
  const store = new SessionStore(new Context());
  const session = store.prepare('memory', { meta: { cwd: '/work/project' } });
  const detach = store.enter(session);
  try {
    assert.equal(await store.flush(session), false);
  } finally {
    detach();
  }
});

test('public SessionStore flush awaits listeners and propagates errors', async () => {
  const ctx = new Context(),
    store = new SessionStore(ctx);
  const session = store.prepare('flush', { meta: { cwd: '/work/project' } });
  const detach = store.enter(session);
  let release, entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const dispose = ctx.on(
    'session/flush',
    () =>
      new Promise((resolve) => {
        release = resolve;
        entered();
      }),
  );
  try {
    let done = false;
    const result = store.flush(session).then((v) => {
      done = true;
      return v;
    });
    await started;
    assert.equal(done, false);
    release();
    assert.equal(await result, true);
    dispose();
    const off = ctx.on('session/flush', () => {
      throw new Error('barrier');
    });
    try {
      await assert.rejects(store.flush(session), /barrier/);
    } finally {
      off();
    }
  } finally {
    dispose();
    detach();
  }
});

test('native port composes public store/persistence with a minimal controller facade for create and restore', async (t) => {
  const s = await storage(t),
    f = fixture();
  let creates = 0,
    resumes = 0;
  f.ctx.sessions = s.store;
  f.ctx.sessionPersistence = s.provider;
  f.ctx.sessionController.inspect = s.inspect;
  f.ctx.sessionProjections.stateOf = (session) => f.presets.get(session.id);
  const attach = (session) => {
    const agent = {
      id: session.id,
      session,
      inbox: { nextTurn: [], nextStep: [] },
    };
    f.agents.set(session.id, agent);
    return agent;
  };
  f.ctx.sessionController.create = async (input) => {
    creates++;
    const session = s.store.prepare(input.sessionId, {
      meta: { cwd: input.cwd, agentPreset: input.agentPreset },
    });
    s.detachers.set(session.id, s.store.enter(session));
    s.handles.set(session.id, await s.provider.create(session.header));
    f.presets.set(session.id, input.agentPreset);
    attach(session);
    return { sessionId: session.id, agentPreset: input.agentPreset };
  };
  f.ctx.sessionController.resolveAgent = async (id) => {
    if (f.agents.has(id)) return { agent: f.agents.get(id) };
    resumes++;
    const data = await s.inspect(id);
    const session = s.store.prepare(id, { meta: data.meta, seed: data.events });
    s.detachers.set(id, s.store.enter(session));
    s.handles.set(id, await s.provider.open(id, 'write'));
    return { agent: attach(session) };
  };
  const host = await createNativeHostPort({ ctx: f.ctx });
  await host.createSession({ id: 'new', cwd: s.dir, preset: null });
  assert.equal(await host.hasDurableProvider('new'), true);
  await s.handles.get('new').close();
  s.handles.delete('new');
  s.detachers.get('new')();
  s.detachers.delete('new');
  f.agents.delete('new');
  assert.equal((await host.getSession('new')).cwd, s.dir);
  assert.equal(resumes, 0);
  assert.equal(await host.flush('new'), true);
  assert.equal(resumes, 1);
  assert.equal(creates, 1);
  assert.equal((await s.inspect('new')).meta.cwd, s.dir);
  const resolve = await createNativeSourceResolver({
    ctx: f.ctx,
    lookupBinding: async (id) => ({
      local: '1',
      bindingId: 'binding',
      hostSessionId: id,
      cwd: s.dir,
      state: 'bound',
      lifecycle: 'open',
      kind: 'session',
    }),
  });
  assert.equal(
    (await resolve({ agent: f.agents.get('new') })).hostSessionId,
    'new',
  );
});

test('public entry factory needs no package loader or isolation services', async () => {
  const f = fixture();
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(host.getDiagnostics().isolation.mode, 'development_unisolated');
  await host.createSession({ id: 'new', cwd: '/work/project', preset: null });
  assert.equal(await host.flush('new'), true);
});

test('public entry isolates missing capabilities and diagnoses behavior failures', async () => {
  const f = fixture();
  delete f.ctx.sessionController.prompt;
  const host = await createNativeHostPort({ ctx: f.ctx });
  assert.equal(host.getDiagnostics().capabilities.prompt.available, false);
  assert.equal(await host.flush('session-a'), true);
  f.ctx.sessionController.inspect = async () => ({
    meta: { id: 'wrong' },
    events: [],
  });
  await assert.rejects(host.getSession('session-a'), {
    code: 'network.host_incompatible',
  });
  assert.equal(
    host.getDiagnostics().capabilities.read.lastFailure,
    'public_inspect_failed',
  );
});
