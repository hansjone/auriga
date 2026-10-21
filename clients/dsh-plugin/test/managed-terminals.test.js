import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import {
  createActivityProbe,
  refreshActivityFromHost,
} from '../src/managed/activity.js';
import { createManaged } from '../src/modes/managed.js';
import { makeIdentityKeys, INSTANCE_ID, OWNER_ID } from './managed-helpers.js';
import {
  terminalAgent,
  browserOwner,
  browserController,
  modelTerminals,
  modelRecord,
  terminalHost,
} from './terminal-fixtures.js';

const browserOnly = { terminalSources: 'browser-only', browsers: 0 };

async function count(ctx, opts = browserOnly) {
  const activity = createActivityProbe();
  await refreshActivityFromHost(ctx, activity, opts);
  assert.equal(activity.snapshot().lastInteractionAt, undefined);
  return activity.snapshot().openTerminals;
}

test('pinned web: an empty owner registry needs the explicit browser-only profile', async () => {
  const ctx = terminalHost(browserController());
  assert.equal(await count(ctx), 0);
  assert.equal(await count(ctx, { browsers: 0 }), null);
  assert.equal(await count(terminalHost()), null);
});

test('pinned web: retained offline owners count attached and background running terminals', async () => {
  const browser = browserController();
  const owner = browserOwner();
  owner.terminals.set('foreground', {
    info: { id: 'foreground', state: 'running', controllerId: 'window-1' },
  });
  owner.terminals.set('background', {
    info: { id: 'background', state: 'running' },
  });
  browser.owners.set('offline-session', owner);
  const activity = createActivityProbe({ now: () => new Date(0) });
  activity.touchInteraction();
  await refreshActivityFromHost(terminalHost(browser), activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, 2);
  assert.equal(
    activity.snapshot().lastInteractionAt,
    new Date(0).toISOString(),
  );
  owner.terminals.clear();
  await refreshActivityFromHost(terminalHost(browser), activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, 0);
  assert.equal(
    activity.snapshot().lastInteractionAt,
    new Date(0).toISOString(),
  );
});

test('pinned web: allocation, pending, closing and failed cleanup never look idle', async () => {
  const mutations = [
    (owner) => owner.pending.set('spawning', Promise.resolve()),
    (owner) => owner.allocations.set('failed', { info: { state: 'failed' } }),
    (owner) => {
      owner.cleanup = Promise.resolve();
    },
    (owner) => owner.lifetime.abort(),
    (owner) =>
      owner.terminals.set('closing', {
        info: { id: 'closing', state: 'running' },
        closing: Promise.resolve(),
      }),
    (owner) =>
      owner.terminals.set('retry', {
        info: { id: 'retry', state: 'running' },
        retention: { closing: true },
      }),
    (owner) =>
      owner.terminals.set('failed', {
        info: { id: 'failed', state: 'failed' },
      }),
    (owner) =>
      owner.terminals.set('exited', {
        info: { id: 'exited', state: 'exited' },
      }),
    (owner) => {
      owner.terminals.set('closed', {
        info: { id: 'closed', state: 'running' },
      });
      owner.closedIds.add('closed');
    },
  ];
  for (const mutate of mutations) {
    const browser = browserController();
    const owner = browserOwner();
    mutate(owner);
    browser.owners.set('session', owner);
    assert.equal(await count(terminalHost(browser)), null);
    browser.owners.clear();
    assert.equal(await count(terminalHost(browser)), 0);
  }
});

test('pinned web: malformed registries, failed lookup or list cannot replace old counts with zero', async () => {
  const browser = browserController();
  const invalid = [
    { ...browser, owners: {} },
    { ...browser, owners: new Map([['session', {}]]) },
    {
      ...browser,
      list() {
        throw new Error('failed');
      },
    },
    {
      ...browser,
      list() {
        return {};
      },
    },
  ];
  // list is checked even for an empty retained owner.
  browser.owners.set('session', browserOwner());
  for (const source of invalid) {
    assert.equal(await count(terminalHost(source)), null);
  }
  for (const failing of ['terminals', 'terminalController']) {
    const ctx = terminalHost(browser);
    const lookup = ctx.get;
    ctx.get = (name) => {
      if (name === failing) throw new Error('lookup failed');
      return lookup(name);
    };
    assert.equal(await count(ctx), null);
  }
});

test('browser-only still collects global and per-Agent model PTYs once per shared registry', async () => {
  const model = modelTerminals();
  const agent = terminalAgent('live-agent', () => model);
  const offline = terminalAgent('offline-agent');
  model.sessions.set('live', modelRecord(agent, 'live'));
  model.sessions.set('offline', modelRecord(offline, 'offline'));
  const browser = browserController();
  const owner = browserOwner();
  owner.terminals.set('browser', { info: { id: 'browser', state: 'running' } });
  browser.owners.set('session', owner);
  assert.equal(await count(terminalHost(browser, model, [agent])), 3);
  assert.equal(await count(terminalHost(browser, undefined, [agent])), 3);
  const sharedFacade = { ...model };
  agent.ctx.get = () => sharedFacade;
  assert.equal(await count(terminalHost(browser, model, [agent])), 3);
});

test('model PTY pending, cleanup, disposed owner, invalid registry or list failure stays unknown', async () => {
  const mutations = [
    (model, agent) => model.pendingSpawns.set(agent, new Set([{}])),
    (model) => {
      model.disposing = true;
    },
    (model, agent) => {
      model.sessions.set('closing', {
        ...modelRecord(agent, 'closing'),
        closing: Promise.resolve(),
      });
    },
    (model, agent) => {
      model.sessions.set(
        'failed-close',
        modelRecord(agent, 'failed-close', 'exited'),
      );
    },
    (model, agent) => model.disposedOwners.add(agent),
    (model) => {
      model.pendingSpawns = {};
    },
    (model) => {
      model.sessions = undefined;
    },
    (model) => {
      model.ownerCleanups = undefined;
    },
    (model) => {
      model.list = () => {
        throw new Error('failed');
      };
    },
    (model) => {
      model.list = () => [];
    },
  ];
  for (const mutate of mutations) {
    const model = modelTerminals();
    const agent = terminalAgent();
    model.sessions.set('live', modelRecord(agent, 'live'));
    mutate(model, agent);
    assert.equal(
      await count(terminalHost(browserController(), model, [agent])),
      null,
    );
  }
  const agent = terminalAgent('agent', () => {
    throw new Error('scoped lookup');
  });
  assert.equal(
    await count(terminalHost(browserController(), undefined, [agent])),
    null,
  );
  assert.equal(
    await count(
      terminalHost(browserController(), undefined, [{ status: 'idle' }]),
    ),
    null,
  );
});

test('model PTY failed close stays unknown until successful registry removal, without moving interaction', async () => {
  const model = modelTerminals();
  const agent = terminalAgent();
  const record = modelRecord(agent, 'pty-1');
  model.sessions.set(record.id, record);
  const ctx = terminalHost(browserController(), model, [agent]);
  const activity = createActivityProbe({ now: () => new Date(0) });
  activity.touchInteraction();
  await refreshActivityFromHost(ctx, activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, 1);
  // Pinned kill() resets closing after close rejects. The backend may already
  // report exited while its retained process range has not been cleaned.
  record.session.status = () => ({ kind: 'exited' });
  record.closing = undefined;
  await refreshActivityFromHost(ctx, activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, null);
  await refreshActivityFromHost(ctx, activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, null);
  model.sessions.delete(record.id);
  await refreshActivityFromHost(ctx, activity, browserOnly);
  assert.equal(activity.snapshot().openTerminals, 0);
  assert.equal(
    activity.snapshot().lastInteractionAt,
    new Date(0).toISOString(),
  );
});

test('managed browser-only profile reaches a real fetch with 0, live, unknown and recovery counts', async (t) => {
  const bodies = [];
  const receiver = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(JSON.parse(Buffer.concat(chunks).toString()));
    res.setHeader('content-type', 'application/json');
    res.end('{}');
  });
  receiver.listen(0, '127.0.0.1');
  await once(receiver, 'listening');
  t.after(() => new Promise((resolve) => receiver.close(resolve)));
  const runtime = await createManaged({
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_PLATFORM_URL: `http://127.0.0.1:${receiver.address().port}`,
    },
    identityKeys: makeIdentityKeys().keySet,
    instanceToken: 'terminal-generated-test-token',
    watchKeys: false,
    startHeartbeat: false,
    config: { imBots: 'none', terminalSources: 'browser-only' },
  });
  t.after(() => runtime.close());
  const hostServer = createServer();
  hostServer.listen(0, '127.0.0.1');
  await once(hostServer, 'listening');
  t.after(() => new Promise((resolve) => hostServer.close(resolve)));
  const browser = browserController();
  const dispose = runtime.apply({
    ...terminalHost(browser),
    webServer: { server: hostServer, register() {} },
  });
  t.after(dispose);
  await runtime.heartbeat.send();
  const owner = browserOwner();
  owner.terminals.set('running', { info: { id: 'running', state: 'running' } });
  browser.owners.set('offline', owner);
  await runtime.heartbeat.send();
  owner.pending.set('pending', Promise.resolve());
  await runtime.heartbeat.send();
  browser.owners.clear();
  await runtime.heartbeat.send();
  assert.deepEqual(
    bodies.map((body) => body.activity.openTerminals),
    [0, 1, null, 0],
  );
  assert.ok(
    bodies.every(
      (body) => body.health.ok && body.activity.lastInteractionAt === undefined,
    ),
  );
});

test('managed rejects an unknown terminal source declaration', async () => {
  await assert.rejects(
    createManaged({ config: { terminalSources: 'guess-zero' } }),
    /terminalSources/,
  );
});
