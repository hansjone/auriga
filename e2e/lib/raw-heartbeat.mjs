import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

export function readRawHeartbeats(
  file,
  instanceId,
  { after = 0, before = Infinity } = {},
) {
  assert.ok(file, 'real mode requires RAW_HEARTBEAT_FILE');
  // appendFileSync uses complete JSONL records; ignore a trailing in-flight
  // write until its newline arrives on the next poll.
  const contents = readFileSync(file, 'utf8');
  return contents
    .slice(0, contents.lastIndexOf('\n') + 1)
    .split('\n')
    .filter(Boolean)
    .map((line) => JSON.parse(line))
    .filter((entry) => {
      const time = Date.parse(entry.receivedAt);
      assert.ok(Number.isFinite(time), 'capture receipt time missing');
      return entry.instanceId === instanceId && time >= after && time <= before;
    });
}

export function assertRawHeartbeat(entry, runtime) {
  assert.equal(
    entry.upstreamStatus,
    200,
    'workspace did not accept actual heartbeat',
  );
  assert.equal(
    entry.pluginVersion,
    runtime.pluginVersion,
    'unexpected plugin sender',
  );
  assert.equal(entry.dshVersion, runtime.dshVersion, 'unexpected DSH sender');
  assert.equal(entry.health?.ok, true, 'actual host heartbeat is unhealthy');
  for (const key of [
    'runningTurns',
    'openTerminals',
    'activeImBots',
    'connectedClients',
  ]) {
    assert.equal(entry.activity?.[key], 0, `actual ${key} is unknown or busy`);
  }
  assert.ok(
    Number.isFinite(Date.parse(entry.activity?.lastInteractionAt)),
    'raw interaction timestamp missing',
  );
}

export function assertRawQuiet(entries, runtime, interactionAt) {
  assert.ok(
    entries.length >= 2,
    'quiet window needs at least two accepted actual heartbeats',
  );
  for (const entry of entries) {
    assertRawHeartbeat(entry, runtime);
    assert.equal(
      entry.activity.lastInteractionAt,
      interactionAt,
      'health polling/refresh advanced raw interaction time',
    );
  }
}
