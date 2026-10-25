import { test } from 'node:test';
import assert from 'node:assert/strict';
import { openHarness, delivery } from './support/harness.mjs';
import { skipNoServer } from '../../clients/dsh-plugin/test/network/transport/support/env.js';

test(
  'unicast fixtures travel both ways through two plugin WSS links and emit RECEIPT',
  { timeout: 30_000, skip: skipNoServer },
  async (t) => {
    const h = await openHarness(t);
    const toB = delivery(h.a, h.b, 'b-reply');
    const b = await h.deliver(h.b, toB);
    h.assertAccepted(b, toB);
    const toA = delivery(h.b, h.a, 'a-reply');
    const a = await h.deliver(h.a, toA);
    h.assertAccepted(a, toA);
    assert.equal(h.routed.length, 2);
    assert.equal(h.receipts.length, 2);
    for (const endpoint of [h.a, h.b]) {
      assert.equal(endpoint.host.counts.prompt, 1);
      assert.equal(endpoint.host.counts.create, 0);
      assert.equal(endpoint.host.counts.resume, 1);
      assert.equal(endpoint.host.history('other-session').length, 0);
      assert.equal(
        endpoint.host.sessions.get(endpoint.sessionId).cwd,
        endpoint.cwd,
      );
      // Observing a receipt on NATS is not a durable collab commit.
      assert.equal(endpoint.adapter.pendingReceipts().length, 1);
    }
  },
);

test(
  'lost RECEIPT: replay the same delivery over NATS without a second host prompt',
  { timeout: 30_000, skip: skipNoServer },
  async (t) => {
    const h = await openHarness(t);
    const page = delivery(h.a, h.b, 'lost-receipt');
    h.b.dropNextReceipt = true;
    const lost = await h.deliver(h.b, page);
    assert.equal(lost.dropped, true);
    assert.equal(h.receipts.length, 0);
    assert.equal(h.b.adapter.pendingReceipts().length, 1);
    const retried = await h.deliver(h.b, page);
    h.assertAccepted(retried, page);
    assert.equal(h.b.host.counts.prompt, 1);
    assert.equal(h.b.platform.state.calls.admit, 1);
    assert.deepEqual(
      h.b.host.history(h.b.sessionId).map((x) => x.requestId),
      ['lost-receipt'],
    );
    assert.equal(h.receipts.length, 1);
    assert.equal(h.routed.length, 2);
  },
);

test(
  'crash after durable host acceptance: reopen file ledger and WSS client, then replay once',
  { timeout: 30_000, skip: skipNoServer },
  async (t) => {
    const h = await openHarness(t);
    const page = delivery(h.a, h.b, 'restart-recipient');
    h.b.crashAfterAcceptance = true;
    const failed = await h.deliver(h.b, page);
    assert.equal(failed.error?.message, 'fixture crash after_host_accepted');
    assert.equal(h.receipts.length, 0);
    assert.equal(
      h.b.ledger.read().recipients['restart-recipient'].state,
      'HOST_ACCEPTED',
    );
    const revision = h.b.ledger.read().revision;
    const originalTransport = h.b.transport;
    await h.b.restart();
    assert.notEqual(h.b.transport, originalTransport);
    assert.equal(h.b.ledger.read().revision, revision);
    assert.equal(
      h.b.adapter.pendingReceipts()[0].recipientId,
      'restart-recipient',
    );
    assert.equal(h.b.ledger.read().bindings['1'].cwd, h.b.cwd);
    const retried = await h.deliver(h.b, page);
    h.assertAccepted(retried, page);
    assert.equal(h.b.host.counts.prompt, 1);
    assert.equal(h.b.host.counts.create, 0);
    assert.equal(h.b.platform.state.calls.admit, 1);
    assert.deepEqual(
      h.b.host.history(h.b.sessionId).map((x) => x.requestId),
      ['restart-recipient'],
    );
    assert.equal(h.receipts.length, 1);
    assert.equal(h.routed.length, 2);
  },
);
