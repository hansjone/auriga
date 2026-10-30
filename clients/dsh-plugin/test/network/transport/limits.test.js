import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_LIMITS,
  pubWireBytes,
  resolveLimits,
  worstCaseBufferBound,
} from '../../../src/network/transport/limits.js';
import { SendQueue } from '../../../src/network/transport/send-queue.js';
import { TransportError } from '../../../src/network/transport/errors.js';

test('pubWireBytes matches the NATS PUB frame byte for byte', () => {
  for (const len of [0, 1, 9, 10, 99, 100, 65536]) {
    const frame = Buffer.concat([
      Buffer.from(`PUB auriga.net.up.n1 ${len}\r\n`),
      Buffer.alloc(len),
      Buffer.from('\r\n'),
    ]);
    assert.equal(pubWireBytes('auriga.net.up.n1'.length, len), frame.length);
  }
});

test('worst-case bound is the sum of queue, one hand-off frame and the socket hard cap', () => {
  const subjectBytes = 40;
  const limits = resolveLimits({}, { subjectBytes });
  const bound = worstCaseBufferBound(limits, { subjectBytes });
  const maxFrame = pubWireBytes(subjectBytes, DEFAULT_LIMITS.maxMessageBytes);
  assert.equal(bound.perLink.queue, 1024 * 1024);
  assert.equal(bound.perLink.handoff, maxFrame);
  assert.equal(bound.perLink.socket, 512 * 1024);
  assert.equal(bound.perLinkTotal, 1024 * 1024 + maxFrame + 512 * 1024);
  assert.equal(bound.maxLinks, 2);
  assert.equal(bound.total, bound.perLinkTotal * 2);
});

test('resolveLimits rejects configurations that break the proof', () => {
  const ctx = { subjectBytes: 20 };
  assert.throws(
    () => resolveLimits({ queueMaxBytes: 1000 }, ctx),
    /at least one maximum-size/,
  );
  assert.throws(
    () => resolveLimits({ socketHighWaterBytes: 1000 }, ctx),
    /admit at least one/,
  );
  assert.throws(
    () =>
      resolveLimits(
        { socketHardCapBytes: DEFAULT_LIMITS.socketHighWaterBytes },
        ctx,
      ),
    /4 KiB above/,
  );
  assert.throws(
    () => resolveLimits({ queueMaxMessages: 0 }, ctx),
    /positive integer/,
  );
  assert.throws(
    () => resolveLimits({ writeStallMs: 1.5 }, ctx),
    /positive integer/,
  );
});

test('SendQueue refuses beyond either ceiling and never grows past maxBytes', () => {
  const queue = new SendQueue({ maxMessages: 3, maxBytes: 100 });
  queue.offer({ wireBytes: 40 });
  queue.offer({ wireBytes: 40 });
  assert.throws(
    () => queue.offer({ wireBytes: 40 }),
    (e) => e instanceof TransportError && e.code === 'queue_full',
  );
  queue.offer({ wireBytes: 20 });
  assert.throws(() => queue.offer({ wireBytes: 1 }), /queue full/);
  assert.equal(queue.length, 3);
  assert.equal(queue.bytes, 100);
  assert.equal(queue.shift().wireBytes, 40);
  assert.equal(queue.bytes, 60);
  queue.offer({ wireBytes: 40 });
  assert.equal(queue.drain().length, 3);
  assert.equal(queue.bytes, 0);
  assert.equal(queue.shift(), undefined);
});
