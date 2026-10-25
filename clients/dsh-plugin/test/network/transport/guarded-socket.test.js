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
  send(data) {
    this.sent.push(data.byteLength);
    this.bufferedAmount += data.byteLength;
  }
  terminate() {
    this.terminated = true;
  }
  close() {}
}

function guard(inner, cap = 1000) {
  const events = [];
  const socket = new GuardedSocket(inner, {
    hardCapBytes: cap,
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
