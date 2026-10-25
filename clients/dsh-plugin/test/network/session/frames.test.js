import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  assertFrameEpoch,
  SessionError,
} from '../../../src/network/session/index.js';

test('assertFrameEpoch accepts a matching top-level epoch', () => {
  assert.doesNotThrow(() =>
    assertFrameEpoch({ type: 'head', epoch: '7' }, '7'),
  );
});

test('assertFrameEpoch accepts a matching page.epoch', () => {
  assert.doesNotThrow(() =>
    assertFrameEpoch(
      { type: 'fetch_result', page: { epoch: '7', request_id: 'r' } },
      '7',
    ),
  );
});

test('assertFrameEpoch rejects a missing epoch', () => {
  assert.throws(
    () => assertFrameEpoch({ type: 'head' }, '7'),
    (e) => e instanceof SessionError && e.code === 'missing_epoch',
  );
  assert.throws(
    () =>
      assertFrameEpoch(
        { type: 'fetch_result', page: { request_id: 'r' } },
        '7',
      ),
    (e) => e instanceof SessionError && e.code === 'missing_epoch',
  );
});

test('assertFrameEpoch rejects a stale epoch', () => {
  assert.throws(
    () => assertFrameEpoch({ type: 'head', epoch: '6' }, '7'),
    (e) => e instanceof SessionError && e.code === 'stale_epoch',
  );
  assert.throws(
    () =>
      assertFrameEpoch(
        { type: 'fetch_result', page: { epoch: '6', request_id: 'r' } },
        '7',
      ),
    (e) => e instanceof SessionError && e.code === 'stale_epoch',
  );
});
