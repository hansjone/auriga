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
