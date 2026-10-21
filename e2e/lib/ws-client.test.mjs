import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { startControlKeepalive } from './ws-client.mjs';

test('native transport liveness sends valid masked Pong frames and stops on close', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  const socket = new EventEmitter();
  const frames = [];
  socket.destroyed = false;
  socket.write = (frame) => frames.push(frame);
  let draining = false;
  socket.resume = () => {
    draining = true;
  };
  const stop = startControlKeepalive(socket, 1000);
  assert.equal(draining, true);
  t.mock.timers.tick(1000);
  assert.equal(frames.length, 1);
  assert.equal(frames[0][0], 0x8a, 'FIN and Pong opcode');
  assert.equal(frames[0][1], 0x80, 'MASK and zero payload length');
  assert.equal(frames[0].length, 6, 'header plus client mask');
  socket.destroyed = true;
  socket.emit('close');
  t.mock.timers.tick(10_000);
  assert.equal(frames.length, 1);
  assert.equal(socket.listenerCount('close'), 0);
  stop();
});

test('native control reader echoes a split Ping and immediately honors a peer Close', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  const socket = new EventEmitter();
  const frames = [];
  socket.destroyed = false;
  socket.write = (frame) => frames.push(frame);
  socket.resume = () => {};
  socket.destroy = () => {
    socket.destroyed = true;
    socket.emit('close');
  };
  socket.end = (frame, done) => {
    frames.push(frame);
    done();
  };
  const stop = startControlKeepalive(socket, 1000, Buffer.from([0x89]));
  socket.emit('data', Buffer.from([0x03, 1, 2, 3]));
  assert.equal(frames[0][0], 0x8a);
  const decoded = frames[0]
    .subarray(6)
    .map((byte, i) => byte ^ frames[0][2 + (i % 4)]);
  assert.deepEqual(decoded, Buffer.from([1, 2, 3]));
  socket.emit('data', Buffer.from([0x88, 0x00]));
  assert.equal(socket.destroyed, true);
  assert.equal(frames[1][0], 0x88);
  t.mock.timers.tick(10_000);
  assert.equal(frames.length, 2);
  assert.equal(socket.listenerCount('data'), 0);
  stop();
});

test('native control reader reports the peer Close status code', () => {
  const socket = new EventEmitter();
  socket.destroyed = false;
  socket.write = () => {};
  socket.resume = () => {};
  socket.destroy = () => {
    socket.destroyed = true;
    socket.emit('close');
  };
  socket.end = (_frame, done) => done();
  const codes = [];
  const stop = startControlKeepalive(socket, 1000, Buffer.alloc(0), (code) =>
    codes.push(code),
  );
  socket.emit('data', Buffer.from([0x88, 0x02, 0x03, 0xe9]));
  assert.deepEqual(codes, [1001]);
  stop();
});
