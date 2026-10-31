import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHttpResponseTracker } from '../src/local/host.js';
import { createConnectRuntime } from '../src/local/runtime.js';

function response() {
  const value = new EventEmitter();
  value.destroyed = false;
  value.writableEnded = false;
  value.destroyCalls = 0;
  value.destroy = () => {
    value.destroyCalls++;
    value.destroyed = true;
  };
  return value;
}
test('response finish/close releases listeners and never destroys an unrelated or completed response', () => {
  const sessions = new Map([
      ['A', { key: 'A' }],
      ['B', { key: 'B' }],
    ]),
    tracker = createHttpResponseTracker(
      (req) => sessions.get(req.headers.session) ?? null,
    );
  const done = response(),
    active = response(),
    other = response();
  assert.equal(tracker.bind({ headers: { session: 'A' } }, done), true);
  assert.equal(done.listenerCount('close'), 1);
  done.writableEnded = true;
  done.emit('finish');
  assert.equal(done.listenerCount('close'), 0);
  assert.equal(done.listenerCount('finish'), 0);
  tracker.bind({ headers: { session: 'A' } }, active);
  tracker.bind({ headers: { session: 'B' } }, other);
  sessions.delete('A');
  tracker.close('A');
  assert.equal(done.destroyCalls, 0);
  assert.equal(active.destroyCalls, 1);
  assert.equal(other.destroyCalls, 0);
  active.emit('close');
  assert.equal(active.listenerCount('finish'), 0);
  assert.equal(
    tracker.bind({ headers: { session: 'anonymous' } }, response()),
    false,
  );
  tracker.close('B');
  other.emit('close');
});
test('late close of an old response cannot remove a newly tracked response for the same key', () => {
  const tracker = createHttpResponseTracker(() => ({ key: 'A' })),
    old = response(),
    next = response();
  tracker.bind({ headers: {} }, old);
  tracker.close('A');
  tracker.bind({ headers: {} }, next);
  old.emit('close');
  tracker.close('A');
  assert.equal(next.destroyCalls, 1);
  next.emit('close');
});
test('runtime tracking leaves anonymous requests to route admission and unregisters idempotently', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-http-hook-'));
  let handler,
    unregistered = 0,
    nextCalls = 0;
  const config = {
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'native-probe',
    providerId: 'team',
  };
  const ctx = {
    webServer: { protocol: 'http:' },
    on(name, callback) {
      assert.equal(name, 'connection/request');
      handler = callback;
      return () => {
        unregistered++;
      };
    },
  };
  const runtime = await createConnectRuntime({ directory, config, ctx });
  try {
    const stop = runtime.installHttpTracking();
    assert.throws(() => runtime.installHttpTracking(), /already installed/);
    const res = response();
    res.writeHead = (status) => {
      res.status = status;
    };
    res.end = () => {
      res.writableEnded = true;
      res.emit('finish');
    };
    const request = { headers: {} };
    request.headers.cookie = runtime.prepare(request).setCookie.split(';')[0];
    await handler(request, res, () => {
      nextCalls++;
    });
    assert.equal(res.status, undefined);
    assert.equal(nextCalls, 1);
    assert.equal(res.listenerCount('close'), 0);
    stop();
    stop();
    assert.equal(unregistered, 1);
    runtime.close();
    assert.equal(unregistered, 1);
    const absent = await createConnectRuntime({
      directory: join(directory, 'no-hook'),
      config,
      ctx: { webServer: { protocol: 'http:' } },
    });
    assert.throws(
      () => absent.installHttpTracking(),
      /connection\/request hook required/,
    );
    absent.close();
  } finally {
    runtime.close();
    await rm(directory, { recursive: true, force: true });
  }
});
