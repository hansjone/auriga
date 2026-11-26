import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { buildPayload, tempBase } from './helpers.mjs';
import { install } from '../runtime/core.mjs';
import {
  createDesktopReporter,
  rememberUpdateAttempt,
  finishUpdateAttempt,
} from '../runtime/desktop-report.mjs';
import { readUpdateState } from '../runtime/update-state.mjs';

function fixture(t, fetcher) {
  const base = tempBase(t);
  const root = join(base, 'app');
  install({
    root,
    payload: buildPayload(base).dir,
    dataHome: join(base, 'data'),
    smoke: () => {},
  });
  writeFileSync(
    join(root, 'desktop-origin.json'),
    JSON.stringify({ origin: 'https://portal.example.com' }),
  );
  const broker = new EventEmitter();
  const identity = {
    tenantId: 'tenant',
    memberId: 'member',
    deviceId: 'device',
  };
  broker.platform = {
    config: { platformOrigin: 'https://portal.example.com' },
  };
  broker.guard = () => ({ identity });
  broker.assert = () => {};
  broker.tokenFor = async (audience) => {
    assert.equal(audience, 'auriga');
    return 'test-platform-device';
  };
  let state = 'authenticated',
    scheduled;
  const auth = { broker, snapshot: () => ({ state }) };
  const reporter = createDesktopReporter({
    root,
    getAuth: () => auth,
    fetcher,
    schedule: (callback) => {
      scheduled = callback;
      return { unref() {} };
    },
    unschedule: () => {},
  });
  t.after(() => reporter.close());
  return {
    root,
    broker,
    identity,
    auth,
    reporter,
    setState: (value) => {
      state = value;
    },
    poll: () => scheduled(),
  };
}

test('offline reporting retries the same durable sequence and never throws into startup', async (t) => {
  const calls = [];
  const f = fixture(t, async (url, request) => {
    calls.push({ url, request });
    if (calls.length === 1)
      throw new Error('offline private/path token=not-reported');
    return new Response(null, { status: 204 });
  });
  f.setState('signed_out');
  f.reporter.start();
  await new Promise(setImmediate);
  assert.equal(calls.length, 0);
  f.setState('authenticated');
  f.broker.emit('changed');
  await new Promise(setImmediate);
  f.poll();
  await new Promise(setImmediate);
  await f.reporter.tick();
  assert.equal(calls.length, 2);
  assert.equal(calls[0].request.body, calls[1].request.body);
  const body = JSON.parse(calls[1].request.body);
  assert.equal(body.sequence, 1);
  assert.equal(body.clientVersion, '1.0.0');
  assert.equal(calls[0].request.credentials, 'omit');
  assert.equal(calls[0].request.redirect, 'error');
  assert.equal(
    calls[0].request.headers.Authorization,
    'Bearer test-platform-device',
  );
  assert.doesNotMatch(JSON.stringify(body), /private|token|dataHome/);
});

test('shutdown aborts reporting promptly and the next login retries the durable facts', async (t) => {
  const sent = [];
  const fetcher = async (_url, request) => {
    sent.push(JSON.parse(request.body));
    if (sent.length === 1)
      await new Promise((_resolve, reject) =>
        request.signal.addEventListener('abort', () =>
          reject(new Error('aborted')),
        ),
      );
    return new Response(null, { status: 204 });
  };
  const f = fixture(t, fetcher);
  f.reporter.start();
  await new Promise(setImmediate);
  f.reporter.close();
  await new Promise(setImmediate);
  const restarted = createDesktopReporter({
    root: f.root,
    getAuth: () => f.auth,
    fetcher,
  });
  t.after(() => restarted.close());
  await restarted.tick();
  assert.equal(sent.length, 2);
  assert.deepEqual(sent[0], sent[1]);
});

test('an unavailable platform does not block startup, and a finished update triggers safe telemetry', async (t) => {
  let release,
    count = 0;
  const f = fixture(t, async () => {
    count++;
    if (count === 1)
      await new Promise((resolve) => {
        release = resolve;
      });
    return new Response(null, { status: 204 });
  });
  assert.equal(f.reporter.start(), f.reporter);
  await new Promise(setImmediate);
  assert.equal(count, 1);
  rememberUpdateAttempt(f.root, '2.0.0', 'stable', true);
  finishUpdateAttempt(f.root, 'failed', 'C:/private', 'token=secret');
  assert.equal(readUpdateState(f.root).lastAttempt.reasonCode, 'updateFailed');
  assert.equal(readUpdateState(f.root).lastAttempt.stage, 'installation');
  release();
  await new Promise(setImmediate);
  await f.reporter.tick();
  assert.ok(count >= 2);
  const saved = readUpdateState(f.root).desktopReport;
  assert.equal(saved.facts.lastUpdate.result, 'failed');
  assert.equal(saved.facts.lastUpdate.targetVersion, '2.0.0');
  assert.equal(saved.sequence, 2);
});

test('identity change re-reports for the new device and foreign origin is never contacted', async (t) => {
  let count = 0;
  const f = fixture(t, async () => {
    count++;
    return new Response(null, { status: 204 });
  });
  await f.reporter.tick();
  f.identity.deviceId = 'new-device';
  await f.reporter.tick();
  assert.equal(count, 2);
  const file = join(f.root, 'desktop-origin.json');
  const binding = JSON.parse(readFileSync(file, 'utf8'));
  binding.origin = 'https://foreign.example.com';
  writeFileSync(file, JSON.stringify(binding));
  rememberUpdateAttempt(f.root, '3.0.0', 'stable', true);
  finishUpdateAttempt(f.root, 'succeeded');
  await f.reporter.tick();
  assert.equal(count, 2);
});
