import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { NatsTransport } from '../../../clients/dsh-plugin/src/network/transport/nats-transport.js';
import { createHostAdapter } from '../../../clients/dsh-plugin/src/network/host/adapter.js';
import { createFileLedgerStore } from '../../../clients/dsh-plugin/src/network/host/store.js';
import {
  openLedger,
  digestOf,
} from '../../../clients/dsh-plugin/src/network/host/ledger.js';
import { encodeReceipt } from '../../../clients/dsh-plugin/src/network/session/frames.js';
import {
  createFakeHost,
  createFixturePlatform,
} from '../../../clients/dsh-plugin/test/network/host/fixtures.js';
import {
  connectPeer,
  createCaSocketFactory,
  waitFor,
} from '../../../clients/dsh-plugin/test/network/transport/support/client.js';
import {
  findNatsServer,
  startNatsServer,
  createJwtAuth,
} from '../../../clients/dsh-plugin/test/network/transport/support/nats-server.js';

const encode = (value) => Buffer.from(JSON.stringify(value));
const address = (node) => ({
  version: 1,
  realm: '2',
  node,
  local: '1',
  type: 'session',
});

// Test inputs are HostAdapter Pages, not signed gateway frames. The isolated
// fixture owns trust here; no fake mailbox, commit transaction or cursor exists.
export function delivery(source, target, recipientId) {
  const message = {
    messageId: `message-${recipientId}`,
    from: source.address,
    to: target.address,
    replyTo: source.address,
    threadId: 'fixture-thread',
    threadSequence: '1',
    inReplyTo: target.requestId,
    kind: 'reply',
    intent: 'request',
    messageClass: 'unicast',
    senderKind: 'agent',
    body: { text: `fixture unicast ${recipientId}` },
  };
  return {
    verified: true,
    tenant: target.identity.tenant,
    nodeIncarnation: target.identity.nodeIncarnation,
    epoch: '5',
    bootId: 'fixture-boot',
    items: [
      {
        deliveryId: `delivery-${recipientId}`,
        mailSeq: '1',
        messageDigest: digestOf(message),
        message,
        recipients: [
          {
            recipientId,
            local: '1',
            type: 'session',
            attemptId: `attempt-${recipientId}`,
          },
        ],
      },
    ],
  };
}

export async function openHarness(t) {
  const cleanups = [];
  t.after(async () => {
    const errors = [];
    for (const close of cleanups.reverse()) {
      try {
        await close();
      } catch (error) {
        errors.push(error);
      }
    }
    if (errors.length)
      throw new AggregateError(errors, 'network fixture cleanup failed');
  });
  const binary = findNatsServer();
  assert.ok(
    binary,
    '需要仓库锁定的 nats-server；设置 AURIGA_NATS_SERVER 或 AURIGA_TEST_CACHE（不自动下载、不静默跳过）',
  );
  const pinSource = await readFile(
    new URL(
      '../../../services/test-support/src/main/java/auriga/testsupport/NatsServer.java',
      import.meta.url,
    ),
    'utf8',
  );
  const version = /String VERSION = "([\d.]+)"/.exec(pinSource)?.[1];
  assert.ok(version, 'cannot read repository NATS version pin');
  const probe = spawnSync(binary, ['-v'], {
    encoding: 'utf8',
    windowsHide: true,
    timeout: 5_000,
  });
  assert.ifError(probe.error);
  assert.equal(probe.status, 0);
  assert.equal(
    /v(\d+\.\d+\.\d+)/.exec(probe.stdout + probe.stderr)?.[1],
    version,
    'NATS binary must match repository version',
  );
  t.diagnostic(
    `nats-server=${version}; node=${process.version}; fixture=host-transport (no gateway/collab)`,
  );
  const auth = createJwtAuth();
  const server = await startNatsServer({ binary, authConfig: auth.config });
  cleanups.push(() => server.close());
  const peer = await connectPeer(server, auth.mintUser({ ttlMs: 600_000 }));
  cleanups.push(() => peer.close());
  const root = await mkdtemp(join(tmpdir(), 'auriga-network-e2e-'));
  cleanups.push(() => {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()));
    assert.ok(basename(root).startsWith('auriga-network-e2e-'));
    return rm(root, { recursive: true, force: true });
  });
  const prefix = `auriga.net.fixture.${randomUUID()}`;
  const receipts = [];
  const routed = [];
  const receiveErrors = [];
  peer.subscribe(`${prefix}.up.*`, {
    callback(error, message) {
      if (error) {
        receiveErrors.push(error);
        return;
      }
      try {
        const frame = JSON.parse(message.string());
        if (frame.verified === true) {
          // Stateless test relay only. This does not implement gateway SEND,
          // IAM authorization, mailbox persistence, FETCH or receipt commits.
          const node = frame.items[0].message.to.node;
          assert.ok(node === '63' || node === '64');
          routed.push(frame);
          peer.publish(`${prefix}.down.${node}`, encode(frame));
        } else {
          assert.equal(frame.type, 'receipt');
          receipts.push(frame);
        }
      } catch (failure) {
        receiveErrors.push(failure);
      }
    },
  });
  await peer.flush();

  async function endpoint(node, remoteNode) {
    const e = {
      identity: {
        tenant: 'tenant-fixture',
        realm: '2',
        node,
        nodeIncarnation: `inc-${node}`,
      },
      address: address(node),
      requestId: `request-${node}`,
      sessionId: `session-${node}`,
      cwd: join(root, `project-${node}`),
      host: createFakeHost(),
      platform: createFixturePlatform(),
      results: [],
      dropNextReceipt: false,
      crashAfterAcceptance: false,
      ledger: null,
      transport: null,
    };
    e.host.addSession(e.sessionId, { cwd: e.cwd });
    e.host.addSession('other-session');
    const store = createFileLedgerStore({ dir: join(root, `ledger-${node}`) });
    async function boot(initialize) {
      e.ledger = await openLedger({ store, identity: e.identity, initialize });
      e.adapter = createHostAdapter({
        ledger: e.ledger,
        host: e.host,
        network: e.platform.network,
        faults: {
          at(point) {
            if (point === 'after_host_accepted' && e.crashAfterAcceptance) {
              e.crashAfterAcceptance = false;
              throw new Error('fixture crash after_host_accepted');
            }
          },
        },
      });
      e.transport = new NatsTransport({
        url: server.url,
        upSubject: `${prefix}.up.${node}`,
        downSubject: `${prefix}.down.${node}`,
        inboxPrefix: `${prefix}.reply.${node}`,
        createSocket: createCaSocketFactory(server.rootPem),
        getTicket: async () => ({
          auth: auth.mintUser({ ttlMs: 600_000 }),
          expiresAtMs: Date.now() + 600_000,
        }),
        onMessage(payload) {
          void accept(payload);
        },
      });
      await e.transport.start();
    }
    async function accept(payload) {
      const result = {};
      try {
        const page = JSON.parse(Buffer.from(payload).toString('utf8'));
        const accepted = await e.adapter.deliver(page);
        assert.equal(accepted.length, 1);
        result.receipt = accepted[0];
        if (e.dropNextReceipt) {
          e.dropNextReceipt = false;
          result.dropped = true;
        } else {
          const r = accepted[0];
          const frame = encodeReceipt({
            requestId: randomUUID(),
            operationId: randomUUID(),
            epoch: page.epoch,
            deliveryId: r.deliveryId,
            mailSeq: page.items[0].mailSeq,
            results: [{ ...r, expectedStateVersion: String(r.ledgerRevision) }],
          });
          await e.transport.send(frame);
        }
      } catch (error) {
        result.error = error;
      }
      e.results.push(result);
    }
    cleanups.push(async () => {
      await e.transport?.stop();
      await e.ledger?.close();
    });
    await boot(true);
    const local = await e.adapter.bindExistingSession({
      opId: `bind-${node}`,
      hostSessionId: e.sessionId,
      cwd: e.cwd,
      receiveMode: 'replies_only',
      executeMode: 'prompt',
    });
    assert.equal(local, '1');
    await e.adapter.reserveOutboundRequest({
      requestId: e.requestId,
      local,
      threadId: 'fixture-thread',
      peer: address(remoteNode),
    });
    await e.adapter.activateOutboundRequest(e.requestId);
    e.restart = async () => {
      await e.transport.stop();
      e.ledger.abandon();
      e.host.crash();
      await boot(false);
    };
    return e;
  }

  const a = await endpoint('63', '64');
  const b = await endpoint('64', '63');
  return {
    a,
    b,
    receipts,
    routed,
    async deliver(target, page) {
      const index = target.results.length;
      const receiptIndex = receipts.length;
      const source = target === a ? b : a;
      await source.transport.send(encode(page));
      const result = await waitFor(
        () => target.results[index],
        'host delivery result',
      );
      if (!result.error && !result.dropped) {
        await waitFor(() => receipts.length > receiptIndex, 'outgoing RECEIPT');
        result.wire = receipts[receiptIndex];
      }
      assert.deepEqual(receiveErrors, []);
      return result;
    },
    assertAccepted(result, page) {
      assert.ifError(result.error);
      const item = page.items[0];
      const recipient = item.recipients[0];
      assert.equal(result.receipt.status, 'host_accepted');
      assert.equal(result.wire.type, 'receipt');
      assert.equal(result.wire.delivery_id, item.deliveryId);
      assert.equal(result.wire.mail_seq, item.mailSeq);
      assert.equal(result.wire.results[0].recipient_id, recipient.recipientId);
      assert.equal(result.wire.results[0].status, 'host_accepted');
      assert.equal(result.wire.results[0].accepted_by, 'session');
      assert.equal(result.wire.results[0].acceptance_mode, 'prompt');
      assert.equal(result.wire.results[0].message_digest, item.messageDigest);
      assert.equal(
        result.wire.results[0].host_request_id,
        recipient.recipientId,
      );
    },
  };
}
