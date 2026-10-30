import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { GuardedSocket } from '../../../src/network/transport/guarded-socket.js';

/** WebSocket stand-in whose bufferedAmount the test controls. */
class FakeSocket extends EventEmitter {
  bufferedAmount = 0;
  sent = [];
  terminated = false;
  binaryType = 'blob';
  addEventListener(type, fn) {
    this.on(type, fn);
  }
  send(data, _options, callback) {
    this.sent.push(data.byteLength);
    this.bufferedAmount += data.byteLength;
    callback?.();
  }
  terminate() {
    this.terminated = true;
  }
  close() {}
}

function guard(inner, cap = 1000, coalesceInitialWrites = false) {
  const events = [];
  const socket = new GuardedSocket(inner, {
    hardCapBytes: cap,
    coalesceInitialWrites,
    onOverflow: (info) => events.push(['overflow', info]),
    onClose: () => events.push(['close']),
  });
  return { socket, events };
}

test('frames below the cap pass through and are accounted', () => {
  const inner = new FakeSocket();
  const { socket, events } = guard(inner);
  socket.send(new ArrayBuffer(400));
  socket.send(new ArrayBuffer(600));
  assert.deepEqual(inner.sent, [400, 600]);
  assert.equal(socket.sentBytes, 1000);
  assert.equal(socket.peakBuffered, 1000);
  assert.equal(events.length, 0);
});

test('a frame that would pass the cap is dropped, the socket terminated and overflow reported once', () => {
  const inner = new FakeSocket();
  const { socket, events } = guard(inner);
  socket.send(new ArrayBuffer(900));
  socket.send(new ArrayBuffer(101));
  socket.send(new ArrayBuffer(1));
  assert.deepEqual(inner.sent, [900]);
  assert.equal(inner.terminated, true);
  assert.equal(socket.overflowed, true);
  assert.deepEqual(events, [['overflow', { buffered: 900, frame: 101 }]]);
  assert.ok(inner.bufferedAmount <= 1000);
});

test('close or error on the real socket is signalled synchronously and stops sending', () => {
  const inner = new FakeSocket();
  const { socket, events } = guard(inner);
  inner.emit('close');
  inner.emit('error');
  assert.deepEqual(events, [['close']]);
  socket.send(new ArrayBuffer(10));
  assert.deepEqual(inner.sent, []);
});

test('SDK-facing on* slots and binaryType forward to the real socket', () => {
  const inner = new FakeSocket();
  const { socket } = guard(inner);
  const fn = () => {};
  socket.onmessage = fn;
  socket.binaryType = 'arraybuffer';
  assert.equal(inner.onmessage, fn);
  assert.equal(inner.binaryType, 'arraybuffer');
  assert.equal(socket.onmessage, fn);
});

test('initial synchronous pair keeps immutable bytes and order in one frame; later writes stay separate', async () => {
  const inner = new FakeSocket();
  const frames = [];
  const send = inner.send.bind(inner);
  inner.send = (data, options, callback) => {
    frames.push([
      ...new Uint8Array(
        data.buffer ?? data,
        data.byteOffset ?? 0,
        data.byteLength,
      ),
    ]);
    send(data, options, callback);
  };
  const { socket } = guard(inner, 10, true);
  const first = new Uint8Array([1, 2]);
  socket.send(first);
  first[0] = 99;
  assert.equal(socket.bufferedAmount, 2);
  assert.deepEqual(inner.sent, []);
  socket.send(new Uint8Array([3]));
  socket.send(new Uint8Array([4]));
  await Promise.resolve();
  assert.deepEqual(frames, [[1, 2, 3], [4]]);
  assert.equal(socket.sentBytes, 4);
  assert.equal(socket.bufferedAmount, 4);
});

test('single initial write flushes once next microtask and does not combine a later turn', async () => {
  const inner = new FakeSocket();
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(4));
  await Promise.resolve();
  assert.deepEqual(inner.sent, [4]);
  socket.send(new Uint8Array(3));
  assert.deepEqual(inner.sent, [4, 3]);
  assert.equal(socket.peakBuffered, 7);
});

test('close, terminate, peer close and error discard pending snapshots and prohibit late writes', async () => {
  for (const stop of [
    (s) => s.close(),
    (s) => s.terminate(),
    (_s, i) => i.emit('close'),
    (_s, i) => i.emit('error'),
  ]) {
    const inner = new FakeSocket();
    const { socket } = guard(inner, 10, true);
    socket.send(new Uint8Array(5));
    stop(socket, inner);
    socket.send(new Uint8Array(1));
    await Promise.resolve();
    assert.deepEqual(inner.sent, []);
    assert.equal(socket.bufferedAmount, 0);
  }
});

test('pending bytes share the original hard cap, including a slow native socket', async () => {
  const inner = new FakeSocket();
  inner.bufferedAmount = 8;
  const { socket, events } = guard(inner, 10, true);
  socket.send(new Uint8Array(2));
  assert.equal(socket.bufferedAmount, 10);
  socket.send(new Uint8Array(1));
  await Promise.resolve();
  assert.deepEqual(inner.sent, [2]);
  assert.equal(socket.overflowed, true);
  assert.equal(inner.bufferedAmount, 10);
  assert.equal(events[0][0], 'overflow');
});

test('an unmergeable legal pair falls back to original writes without raising budget or rejecting', () => {
  const inner = new FakeSocket();
  const send = inner.send.bind(inner);
  inner.send = (data, options, callback) => {
    send(data, options, callback);
    inner.bufferedAmount = 0;
  };
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(8));
  socket.send(new Uint8Array(8));
  assert.deepEqual(inner.sent, [8, 8]);
  assert.equal(socket.overflowed, false);
});

test('async initial flush errors close the socket without an unhandled throw or late write', async () => {
  const inner = new FakeSocket();
  inner.send = (_data, _options, _callback) => {
    throw new Error('write failed');
  };
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(4));
  await Promise.resolve();
  socket.send(new Uint8Array(1));
  assert.equal(inner.terminated, true);
  assert.equal(socket.bufferedAmount, 0);
});

test('native borrowed view stays owned until send callback, then is explicitly detached', async () => {
  const inner = new FakeSocket();
  let borrowed, finish;
  inner.send = (data, _options, callback) => {
    borrowed = data;
    finish = callback;
  };
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array([1, 2]));
  socket.send(new Uint8Array([3]));
  let completed = false;
  void socket.initialWritesDone().then(() => {
    completed = true;
  });
  await Promise.resolve();
  assert.equal(completed, false);
  assert.deepEqual([...borrowed], [1, 2, 3]);
  finish();
  await socket.initialWritesDone();
  assert.equal(borrowed.byteLength, 0);
  assert.equal(completed, true);
});

test('error does not detach native in-flight data before actual close', async () => {
  const inner = new FakeSocket();
  let borrowed;
  inner.send = (data, _options, _callback) => {
    borrowed = data;
  };
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(2));
  socket.send(new Uint8Array(1));
  inner.emit('error');
  assert.equal(borrowed.byteLength, 3);
  inner.emit('close');
  await socket.initialWritesDone();
  assert.equal(borrowed.byteLength, 0);
});

test('a failing first flush stops an unmergeable pair before the second native write', () => {
  const inner = new FakeSocket();
  let calls = 0;
  inner.send = (_data, _options, _callback) => {
    calls++;
    throw new Error('native failed');
  };
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(8));
  assert.throws(() => socket.send(new Uint8Array(8)), /native failed/);
  socket.send(new Uint8Array(1));
  assert.equal(calls, 1);
  assert.equal(inner.terminated, true);
});

test('a large first write bypasses staging without imposing a new handshake size limit', () => {
  const inner = new FakeSocket();
  const socket = new GuardedSocket(inner, {
    hardCapBytes: 10,
    coalesceInitialWrites: true,
    initialBudgetBytes: 3,
    onClose() {},
    onOverflow() {},
  });
  socket.send(new Uint8Array(8));
  assert.deepEqual(inner.sent, [8]);
});

test('initial microtask rechecks the hard cap after native buffering changes', async () => {
  const inner = new FakeSocket();
  const { socket } = guard(inner, 10, true);
  socket.send(new Uint8Array(4));
  inner.bufferedAmount = 8;
  await Promise.resolve();
  assert.equal(socket.overflowed, true);
  assert.deepEqual(inner.sent, []);
  assert.equal(inner.terminated, true);
});
