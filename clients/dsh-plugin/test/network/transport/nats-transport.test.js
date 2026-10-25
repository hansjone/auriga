import { after, before, describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import { NatsTransport } from '../../../src/network/transport/nats-transport.js';
import {
  createCaSocketFactory,
  connectPeer,
  waitFor,
} from './support/client.js';
import { natsBinary, skipNoServer, str, text } from './support/env.js';
import { createJwtAuth, startNatsServer } from './support/nats-server.js';
import { startTcpProxy } from './support/tcp-proxy.js';

const UP = 'auriga.net.up.node1';
const DOWN = 'auriga.net.down.node1';
const FAST_BACKOFF = { baseMs: 20, maxMs: 200, stableMs: 10_000 };

describe(
  'NatsTransport supervisor against nats-server (real JWT expiry)',
  { skip: skipNoServer },
  () => {
    const jwtAuth = createJwtAuth();
    /** @type {Awaited<ReturnType<typeof startNatsServer>>} */
    let server;
    /** @type {Awaited<ReturnType<typeof startTcpProxy>>} */
    let proxy;
    const cleanups = [];

    before(async () => {
      server = await startNatsServer({
        binary: /** @type {string} */ (natsBinary),
        authConfig: jwtAuth.config,
      });
      proxy = await startTcpProxy(server.port);
    });
    after(async () => {
      await Promise.all(cleanups.map((fn) => fn().catch(() => {})));
      await proxy.close();
      await server.close();
    });

    async function peerWithUpSink() {
      const peer = await connectPeer(
        server,
        jwtAuth.mintUser({ ttlMs: 10 * 60_000 }),
      );
      cleanups.push(() => peer.close());
      const got = [];
      peer.subscribe(UP, { callback: (_e, m) => got.push(m.string()) });
      await peer.flush();
      return { peer, got };
    }

    /**
     * @param {{ ttlMs: number, claimedValidityMs?: number }} ticketing
     * @param {Partial<ConstructorParameters<typeof NatsTransport>[0]>} extra
     */
    function makeTransport({ ttlMs, claimedValidityMs = ttlMs }, extra = {}) {
      const tickets = [];
      const states = [];
      const received = [];
      const transport = new NatsTransport({
        url: `wss://localhost:${proxy.port}`,
        upSubject: UP,
        downSubject: DOWN,
        inboxPrefix: 'auriga.net.reply.node1',
        createSocket: createCaSocketFactory(server.rootPem),
        getTicket: async (ctx) => {
          tickets.push(ctx);
          return {
            auth: jwtAuth.mintUser({ ttlMs }),
            expiresAtMs: Date.now() + claimedValidityMs,
          };
        },
        onState: (s) => states.push(s),
        onMessage: (payload, epoch) =>
          received.push({ text: str(payload), epoch }),
        backoff: FAST_BACKOFF,
        refreshLeadMs: [400, 600],
        ...extra,
      });
      cleanups.push(() => transport.stop());
      return { transport, tickets, states, received };
    }

    test('real JWT expiry kicks the connection; the supervisor redeems a new ticket asynchronously and resumes', async () => {
      const { peer, got } = await peerWithUpSink();
      // The ticket claims a long validity but the JWT really expires in 1.5 s, so no
      // proactive refresh happens and the server's expiry disconnect is what we see.
      const { transport, tickets, states, received } = makeTransport({
        ttlMs: 1500,
        claimedValidityMs: 600_000,
      });
      await transport.start();
      assert.equal(transport.state, 'ready');
      const firstEpoch = transport.epoch;
      await transport.send(text('before'));

      await waitFor(
        () => states.some((s) => s.reason === 'ticket_expired'),
        'server-side expiry',
        8000,
      );
      await waitFor(
        () => transport.state === 'ready' && transport.epoch > firstEpoch,
        'reconnect',
        8000,
      );
      assert.deepEqual(
        tickets.map((t) => t.reason).slice(0, 2),
        ['start', 'lost'],
        'every unexpected loss redeems a fresh ticket',
      );
      assert.ok(tickets[1].attempt >= 0);
      await transport.send(text('after'));
      peer.publish(DOWN, text('down-after'));
      await peer.flush();
      await waitFor(
        () => got.includes('before') && got.includes('after'),
        'up messages',
      );
      await waitFor(
        () => received.some((m) => m.text === 'down-after'),
        'down message',
      );
      assert.equal(new Set(got).size, got.length);
      await transport.stop();
    });

    test('proactive refresh swaps to a new link before expiry without failing any send; at most two links ever exist', async () => {
      const { got } = await peerWithUpSink();
      const { transport, tickets, states } = makeTransport({ ttlMs: 4000 });
      await transport.start();
      const firstEpoch = transport.epoch;

      let maxLinks = 0;
      let sent = 0;
      const failures = [];
      const until = Date.now() + 5000;
      while (Date.now() < until) {
        maxLinks = Math.max(maxLinks, transport.links.length);
        try {
          await transport.send(text(`s${sent}`));
          sent += 1;
        } catch (error) {
          failures.push(error.code);
        }
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      assert.ok(transport.epoch > firstEpoch, 'refreshed to a new epoch');
      assert.ok(tickets.some((t) => t.reason === 'refresh'));
      assert.ok(
        !states.some((s) => s.state === 'reconnecting'),
        'never fell back to reconnecting',
      );
      assert.deepEqual(failures, [], 'no send failed across the swap');
      assert.ok(maxLinks <= 2, `saw ${maxLinks} links`);
      await waitFor(() => transport.links.length === 1, 'old link closed');
      await waitFor(() => got.length === sent, 'all sends delivered');
      assert.deepEqual(
        got,
        Array.from({ length: sent }, (_, i) => `s${i}`),
        'exactly once, in order',
      );
      await transport.stop();
    });

    test('network cut: sends fail immediately while down, a new ticket is redeemed, nothing sent during the outage is replayed', async () => {
      const { got } = await peerWithUpSink();
      const { transport, tickets, states } = makeTransport({ ttlMs: 60_000 });
      await transport.start();
      await transport.send(text('pre'));
      const epoch = transport.epoch;

      // Hold the network down: new connections are refused by stalling... simplest is to
      // cut and keep publishing for the whole outage window.
      proxy.cut();
      const outcomes = { accepted: 0, not_connected: 0 };
      const outageEnd = Date.now() + 800;
      let i = 0;
      while (Date.now() < outageEnd) {
        try {
          await transport.send(text(`outage-${i++}`));
          outcomes.accepted++;
        } catch (error) {
          assert.equal(error.code, 'not_connected');
          outcomes.not_connected++;
        }
        await new Promise((resolve) => setImmediate(resolve));
      }
      await waitFor(
        () => transport.state === 'ready' && transport.epoch > epoch,
        'reconnect',
        8000,
      );
      assert.ok(states.some((s) => s.state === 'reconnecting'));
      assert.equal(tickets.at(-1).reason, 'lost');
      assert.ok(outcomes.not_connected > 0);
      await transport.send(text('post'));
      await waitFor(() => got.includes('post'), 'post-reconnect delivery');
      // Everything the peer saw was either before the cut, accepted during the race with
      // the cut, or after reconnect. Rejected sends never appear later.
      const outage = got.filter((m) => m.startsWith('outage-'));
      assert.ok(outage.length <= outcomes.accepted);
      assert.equal(new Set(got).size, got.length);
      await transport.stop();
    });

    test('ticket redemption failures back off and retry; sends fail while nothing is connected', async () => {
      let calls = 0;
      const { transport, states } = makeTransport(
        { ttlMs: 60_000 },
        {
          getTicket: async () => {
            calls += 1;
            if (calls <= 3) throw new Error('IAM unavailable');
            return {
              auth: jwtAuth.mintUser({ ttlMs: 60_000 }),
              expiresAtMs: Date.now() + 60_000,
            };
          },
        },
      );
      const started = transport.start();
      await assert.rejects(
        transport.send(text('x')),
        (e) => e.code === 'not_connected',
      );
      await started;
      assert.equal(calls, 4);
      assert.equal(transport.state, 'ready');
      assert.ok(states.filter((s) => s.state === 'reconnecting').length >= 3);
      await transport.stop();
    });

    test('stop() closes every link and ends all timers', async () => {
      const { transport } = makeTransport({ ttlMs: 60_000 });
      await transport.start();
      const link = transport.link;
      await transport.stop();
      assert.equal(transport.state, 'stopped');
      assert.equal(link?.state, 'closed');
      assert.equal(transport.links.length, 0);
      await assert.rejects(
        transport.send(text('x')),
        (e) => e.code === 'not_connected',
      );
    });

    test('default transport-wide memory bound (two links) is reported', (t) => {
      const { transport } = makeTransport({ ttlMs: 60_000 });
      const bound = transport.memoryBound;
      t.diagnostic(JSON.stringify(bound));
      assert.equal(bound.maxLinks, 2);
      assert.equal(bound.total, 2 * bound.perLinkTotal);
    });
  },
);
