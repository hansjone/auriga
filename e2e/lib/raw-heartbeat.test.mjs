import test from 'node:test';
import assert from 'node:assert/strict';
import { assertRawHeartbeat, assertRawQuiet } from './raw-heartbeat.mjs';

test('raw heartbeat evidence rejects unknown, unhealthy, synthetic and moving activity', () => {
  const runtime = { pluginVersion: '0.0.1', dshVersion: '0.2.0-rc.2' };
  const entry = {
    ...runtime,
    upstreamStatus: 200,
    activity: {
      runningTurns: 0,
      openTerminals: 0,
      activeImBots: 0,
      connectedClients: 0,
      lastInteractionAt: '2026-01-01T00:00:00.000Z',
    },
    health: { ok: true },
  };
  assertRawHeartbeat(entry, runtime);
  assertRawQuiet([entry, entry], runtime, entry.activity.lastInteractionAt);
  for (const bad of [
    { ...entry, upstreamStatus: 401 },
    { ...entry, pluginVersion: '0.0.0-fake' },
    { ...entry, health: { ok: false } },
    ...Object.keys(entry.activity)
      .filter((key) => key !== 'lastInteractionAt')
      .map((key) => ({
        ...entry,
        activity: { ...entry.activity, [key]: null },
      })),
    { ...entry, activity: { ...entry.activity, lastInteractionAt: undefined } },
  ])
    assert.throws(() => assertRawHeartbeat(bad, runtime));
  assert.throws(() =>
    assertRawQuiet([entry], runtime, entry.activity.lastInteractionAt),
  );
  assert.throws(() =>
    assertRawQuiet(
      [
        entry,
        {
          ...entry,
          activity: {
            ...entry.activity,
            lastInteractionAt: '2026-01-01T00:00:01.000Z',
          },
        },
      ],
      runtime,
      entry.activity.lastInteractionAt,
    ),
  );
});
