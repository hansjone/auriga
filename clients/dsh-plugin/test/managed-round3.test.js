import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createActivityProbe,
  refreshActivityFromHost,
  installActivityBridge,
} from '../src/managed/activity.js';
import { createHeartbeatClient } from '../src/managed/heartbeat.js';
import { isNativePluginManageRequest } from '../src/managed/guards.js';
import {
  terminalAgent,
  modelTerminals,
  modelRecord,
} from './terminal-fixtures.js';

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

test('set does not notify or bump lastInteractionAt when value unchanged', async () => {
  let clock = 1_700_000_000_000;
  const activity = createActivityProbe({ now: () => new Date(clock) });
  let notifies = 0;
  activity.onChange(() => {
    notifies += 1;
  });

  activity.setRunningTurns(2);
  assert.equal(notifies, 1);
  assert.equal(activity.snapshot().lastInteractionAt, undefined);
  activity.touchInteraction();
  const first = activity.snapshot().lastInteractionAt;
  assert.ok(first);

  clock += 60_000;
  activity.setRunningTurns(2);
  activity.setRunningTurns(2);
  assert.equal(notifies, 1);
  assert.equal(activity.snapshot().lastInteractionAt, first);

  // Going idle must not push the stamp forward.
  clock += 60_000;
  activity.setRunningTurns(0);
  assert.equal(notifies, 2);
  assert.equal(activity.snapshot().lastInteractionAt, first);
});

test('refreshActivityFromHost with stable counts does not notify', async () => {
  const activity = createActivityProbe();
  let notifies = 0;
  activity.onChange(() => {
    notifies += 1;
  });
  const agent = terminalAgent();
  agent.status = 'running';
  const model = modelTerminals();
  model.sessions.set('pty-1', modelRecord(agent, 'pty-1'));
  const ctx = {
    get(name) {
      if (name === 'agents') return { list: () => [{ agent }] };
      if (name === 'terminals') return model;
      if (name === 'terminalController') return undefined;
      if (name === 'dshIm')
        return {
          listDeliveryCatalog: async () => ({
            bots: [{ id: 'b1', enabled: true }],
          }),
        };
      throw new Error('missing ' + name);
    },
  };
  await refreshActivityFromHost(ctx, activity, { browsers: 1 });
  const afterFirst = notifies;
  assert.ok(afterFirst >= 1);
  const stamp = activity.snapshot().lastInteractionAt;

  await refreshActivityFromHost(ctx, activity, { browsers: 1 });
  await refreshActivityFromHost(ctx, activity, { browsers: 1 });
  assert.equal(notifies, afterFirst);
  assert.equal(activity.snapshot().lastInteractionAt, stamp);
});

test('bridge + heartbeat: stable activity does not exceed schedule rate', async () => {
  let clock = 1_700_000_000_000;
  const activity = createActivityProbe({ now: () => new Date(clock) });
  activity.touchInteraction();
  activity.setRunningTurns(1);
  const stamp = activity.snapshot().lastInteractionAt;
  assert.ok(stamp);
  clock += 5_000;
  activity.setRunningTurns(0);
  assert.equal(activity.snapshot().lastInteractionAt, stamp);

  let heartbeats = 0;
  const fetchImpl = async () => {
    heartbeats += 1;
    return {
      ok: true,
      status: 200,
      async json() {
        return {
          serverTime: new Date().toISOString(),
          protocol: { current: 1, minSupported: 1 },
          nextHeartbeatSeconds: 1,
        };
      },
    };
  };

  const agent = terminalAgent();
  const model = modelTerminals();
  const ctx = {
    get(name) {
      if (name === 'agents') return { list: () => [{ agent }] };
      if (name === 'terminals') return model;
      if (name === 'terminalController') return undefined;
      if (name === 'dshIm')
        return {
          listDeliveryCatalog: async () => ({ bots: [] }),
        };
      throw new Error('missing ' + name);
    },
  };

  // Heartbeat schedule is whole seconds (intervalMs/1000); use 1s cadence.
  const client = createHeartbeatClient({
    platformUrl: 'https://edge.example.com',
    getInstanceToken: () => 'ait',
    pluginVersion: '0.0.0',
    dshVersion: '0.2.0',
    activity,
    fetchImpl: /** @type {typeof fetch} */ (fetchImpl),
    intervalMs: 1000,
    debounceMs: 80,
    refreshActivity: async () => {
      await refreshActivityFromHost(ctx, activity, { browsers: 0 });
    },
  });

  // Settle bridge (markAllUnknown → refresh) before attaching onChange.
  const disposeBridge = installActivityBridge(ctx, activity, {
    intervalMs: 40,
    getBrowsers: () => 0,
  });
  await sleep(150);
  assert.equal(activity.snapshot().lastInteractionAt, stamp);

  client.start();

  // ~2.3s → ~2–3 scheduled sends. Old bug: refresh→notify→send every
  // debounceMs (~80ms) while bridge ticks → 20+.
  await sleep(2300);
  client.stop();
  disposeBridge();

  assert.ok(
    heartbeats <= 5,
    `expected ≤5 heartbeats on 1s schedule, got ${heartbeats}`,
  );
  assert.ok(
    heartbeats >= 2,
    `expected at least 2 scheduled heartbeats, got ${heartbeats}`,
  );
  assert.equal(activity.snapshot().lastInteractionAt, stamp);
});

test('guard blocks matrix params and trailing %00', () => {
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle;a=1'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest(
      'DELETE',
      '/api/pluginManager/removeBundle;x=y',
    ),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle%00'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest(
      'POST',
      '/api/pluginManager/setPluginEnabled%00extra',
    ),
    true,
  );
});

test('connectedClients unknown without browsers callback', async () => {
  const activity = createActivityProbe();
  const ctx = {
    get(name) {
      if (name === 'agents') return { list: () => [] };
      if (name === 'terminals') return modelTerminals();
      if (name === 'terminalController') return undefined;
      if (name === 'dshIm')
        return { listDeliveryCatalog: async () => ({ bots: [] }) };
      if (name === 'connection') return { clientCount: 99 };
      throw new Error('missing ' + name);
    },
  };
  await refreshActivityFromHost(ctx, activity);
  const snap = activity.snapshot();
  assert.equal(snap.known.connectedClients, false);
  assert.equal(snap.connectedClients, null);
});
