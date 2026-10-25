// Explicit source audit: first npm ci --ignore-scripts in deploy/compose/real-dsh.
// Not part of the standalone plugin's dependency-free default test glob.
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { runInNewContext } from 'node:vm';
import {
  readRuntimeFingerprint,
  verifyRuntimeFingerprint,
} from '../../../../src/network/host/native/runtime.js';
import { hasNativePromptRequest } from '../../../../src/network/host/native/upstream.js';

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
