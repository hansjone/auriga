import { after, before, describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers';
import { NatsLink } from '../../../src/network/transport/nats-link.js';
import { TransportError } from '../../../src/network/transport/errors.js';
import {
  createCaSocketFactory,
  connectPeer,
  waitFor,
} from './support/client.js';
import { natsBinary, skipNoServer, str, text } from './support/env.js';
import { startNatsServer } from './support/nats-server.js';
import { startTcpProxy } from './support/tcp-proxy.js';
import { createWsSocketFactory } from '../../../src/network/transport/ws-socket.js';

const TOKEN = 'link-test-token';
const UP = 'auriga.net.up.node1';
const DOWN = 'auriga.net.down.node1';

describe('NatsLink without a broker', () => {
  test('only the initial SDK synchronous pair is coalesced through the production guard', async () => {
    const frames = [];
    const fakeNats = {
      tokenAuthenticator: () => () => ({ auth_token: 'probe' }),
      async wsconnect(options) {
        const { socket } = await options.wsFactory('wss://example.invalid/');
        socket.send(new Uint8Array([1, 2]));
        socket.send(new Uint8Array([3]));
        socket.send(new Uint8Array([4]));
        throw new Error('configuration probe complete');
      },
    };
    const link = new NatsLink({
      url: 'wss://example.invalid/',
      auth: { token: 'probe' },
      upSubject: UP,
      downSubject: DOWN,
      inboxPrefix: 'probe',
      nats: fakeNats,
      createSocket: () => ({
        bufferedAmount: 0,
        addEventListener() {},
        terminate() {},
        close() {},
        send(data, _options, done) {
          frames.push([...new Uint8Array(data)]);
          done?.();
        },
      }),
    });
    await assert.rejects(link.open());
    assert.deepEqual(frames, [[1, 2, 3], [4]]);
  });
  test('wsconnect is configured without SDK reconnect, with the private inbox prefix, and the authenticator never throws', async () => {
    let captured;
    let calls = 0;
    const fakeNats = {
      // the first call is the link's dry run; later calls (inside the SDK) blow up
      tokenAuthenticator: () => () => {
        if (calls++ > 0) throw new Error('signing failed');
        return { auth_token: 'x' };
      },
      wsconnect: async (opts) => {
        captured = opts;
        throw new Error('stop here');
      },
    };
    const link = new NatsLink({
      url: 'wss://example.invalid',
      auth: { token: 'x' },
      upSubject: UP,
      downSubject: DOWN,
      inboxPrefix: 'auriga.net.reply.n1',
      nats: /** @type {any} */ (fakeNats),
    });
    await assert.rejects(link.open());
    assert.equal(captured.reconnect, false);
    assert.equal(captured.inboxPrefix, 'auriga.net.reply.n1');
    assert.equal(captured.noEcho, true);
    assert.deepEqual(captured.authenticator('nonce'), {});
    assert.equal(typeof captured.wsFactory, 'function');
  });

  test('unusable credentials fail with ticket_invalid before any socket exists', async () => {
    const link = new NatsLink({
      url: 'wss://example.invalid',
      auth: { jwt: 'a.b.c', nkeySeed: 'not-a-seed' },
      upSubject: UP,
      downSubject: DOWN,
      inboxPrefix: 'p',
    });
    await assert.rejects(
      link.open(),
      (e) => e instanceof TransportError && e.code === 'ticket_invalid',
    );
    await assert.rejects(
      link.send(text('x')),
      (e) => e.code === 'not_connected',
    );
  });

  test('createSocket without terminate() fails closed at open (socket_incapable)', async () => {
    const fakeNats = {
      tokenAuthenticator: () => () => ({ auth_token: 'x' }),
      wsconnect: async (opts) => {
        // Exercise the real wsFactory path without dialing a broker.
        await opts.wsFactory('wss://example.invalid/');
        throw new Error('wsFactory should have rejected');
      },
    };
    const link = new NatsLink({
      url: 'wss://example.invalid',
      auth: { token: 'x' },
      upSubject: UP,
      downSubject: DOWN,
      inboxPrefix: 'p',
      createSocket: () => ({
        close() {},
        send() {},
        addEventListener() {},
        bufferedAmount: 0,
        readyState: 0,
      }),
      nats: /** @type {any} */ (fakeNats),
    });
    await assert.rejects(
      link.open(),
      (e) => e instanceof TransportError && e.code === 'socket_incapable',
    );
    assert.equal(link.state, 'closed');
  });
});

describe(
  'NatsLink against nats-server (WSS, test CA chain)',
  { skip: skipNoServer },
  () => {
    /** @type {Awaited<ReturnType<typeof startNatsServer>>} */
    let server;
    /** @type {Awaited<ReturnType<typeof startTcpProxy>>} */
    let proxy;
    const open = [];

    before(async () => {
      server = await startNatsServer({
        binary: /** @type {string} */ (natsBinary),
        authConfig: `authorization { token: "${TOKEN}" }`,
      });
      proxy = await startTcpProxy(server.port);
    });
    after(async () => {
      await Promise.all(open.map((closer) => closer().catch(() => {})));
      await proxy.close();
      await server.close();
    });

    const proxied = () => `wss://localhost:${proxy.port}`;

    /** @param {Partial<ConstructorParameters<typeof NatsLink>[0]>} extra */
    async function openLink(extra = {}) {
      const received = [];
      const closed = [];
      const link = new NatsLink({
        url: proxied(),
        auth: { token: TOKEN },
        upSubject: UP,
        downSubject: DOWN,
        inboxPrefix: 'auriga.net.reply.node1',
        createSocket: createCaSocketFactory(server.rootPem),
        onMessage: (payload) => received.push(str(payload)),
        onClosed: (error) => closed.push(error),
        ...extra,
      });
      open.push(() => link.close());
      await link.open();
      return { link, received, closed };
    }

    async function openPeer() {
      const peer = await connectPeer(server, { token: TOKEN });
      open.push(() => peer.close());
      return peer;
    }

    test('exchanges messages both ways over exactly one physical connection', async () => {
      const peer = await openPeer();
      const fromDsh = [];
      peer.subscribe(UP, { callback: (_e, m) => fromDsh.push(m.string()) });
      await peer.flush();
      const before = proxy.connections;
      const { link, received } = await openLink();
      assert.equal(proxy.connections - before, 1);
      assert.equal(link.state, 'ready');

      for (let i = 0; i < 20; i++) await link.send(text(`up-${i}`));
      for (let i = 0; i < 20; i++) peer.publish(DOWN, text(`down-${i}`));
      await peer.flush();
      await waitFor(
        () => fromDsh.length === 20 && received.length === 20,
        'both directions',
      );
      assert.deepEqual(
        fromDsh,
        Array.from({ length: 20 }, (_, i) => `up-${i}`),
      );
      assert.deepEqual(
        received,
        Array.from({ length: 20 }, (_, i) => `down-${i}`),
      );
      assert.equal(link.stats.queueBytes, 0);
      await link.close();
    });

    test('production ws factory connects with native terminate()', async () => {
      const createSocket = createWsSocketFactory({ ca: server.rootPem });
      assert.equal(
        typeof (await import('ws')).default.prototype.terminate,
        'function',
      );
      const { link } = await openLink({ createSocket });
      assert.equal(link.state, 'ready');
      await link.close();
    });

    test('TLS verification stays on: a client without the test CA cannot connect', async () => {
      const link = new NatsLink({
        url: proxied(),
        auth: { token: TOKEN },
        upSubject: UP,
        downSubject: DOWN,
        inboxPrefix: 'p',
        // Still has terminate() so the check is TLS, not capability.
        createSocket: createWsSocketFactory({ rejectUnauthorized: true }),
        connectTimeoutMs: 3000,
      });
      await assert.rejects(link.open());
      assert.equal(link.state, 'closed');
    });

    test('a wrong token is rejected and leaves nothing running', async () => {
      const link = new NatsLink({
        url: proxied(),
        auth: { token: 'wrong' },
        upSubject: UP,
        downSubject: DOWN,
        inboxPrefix: 'p',
        createSocket: createCaSocketFactory(server.rootPem),
      });
      // The broker's -ERR and the socket close race inside the SDK, so the cause is
      // reported as unauthorized or, when the close wins, as connection_lost.
      await assert.rejects(link.open(), (e) =>
        ['unauthorized', 'connection_lost'].includes(e.code),
      );
      assert.equal(link.state, 'closed');
    });

    test('oversize outbound payloads fail before touching the queue or socket', async () => {
      const { link } = await openLink({ limits: { maxMessageBytes: 1000 } });
      await assert.rejects(
        link.send(new Uint8Array(1001)),
        (e) => e.code === 'message_too_large' && e.outcome === 'not_sent',
      );
      await assert.rejects(
        link.send(/** @type {any} */ ('text')),
        (e) => e.code === 'invalid_payload',
      );
      assert.equal(link.stats.queueMessages, 0);
      await link.close();
    });

    test('tracing preserves the exact 64 KiB broker payload budget and sends no headers', async () => {
      const peer = await openPeer();
      const received = [];
      peer.subscribe(UP, {
        callback: (_error, message) => received.push(message),
      });
      await peer.flush();
      const { link } = await openLink();
      const frame = JSON.stringify({
        request_id: 'f20d474e-07d3-48db-9f32-930a43329c02',
        type: 'fetch',
      });
      const payload = text(
        frame + ' '.repeat(65536 - Buffer.byteLength(frame)),
      );
      await link.send(payload);
      await waitFor(() => received.length === 1, 'maximum payload delivered');
      assert.equal(received[0].data.byteLength, 65536);
      assert.equal(received[0].headers, undefined);
      assert.deepEqual(received[0].data, payload);
      await link.close();
    });

    test('an inbound message over the payload limit fails the link with protocol_error', async () => {
      const peer = await openPeer();
      const { link, received, closed } = await openLink({
        limits: { maxMessageBytes: 1000 },
      });
      peer.publish(DOWN, new Uint8Array(2000));
      await peer.flush();
      await waitFor(() => closed.length === 1, 'link closed');
      assert.equal(closed[0].code, 'protocol_error');
      assert.equal(received.length, 0);
      assert.equal(link.state, 'closed');
    });

    test('drainAndClose delivers everything already accepted before closing', async () => {
      const peer = await openPeer();
      const got = [];
      peer.subscribe(UP, { callback: (_e, m) => got.push(m.string()) });
      await peer.flush();
      const { link, closed } = await openLink();
      const sends = Array.from({ length: 50 }, (_, i) =>
        link.send(text(`m${i}`)),
      );
      await link.drainAndClose(3000);
      await Promise.all(sends);
      await waitFor(() => got.length === 50, 'drained messages');
      assert.equal(closed.length, 1);
      assert.equal(
        closed[0],
        undefined,
        'requested close is not reported as a failure',
      );
      await assert.rejects(
        link.send(text('late')),
        (e) => e.code === 'not_connected',
      );
    });

    test('network cut while publishing: sending stops at once, nothing queues in the SDK, nothing is replayed', async () => {
      const peer = await openPeer();
      const got = [];
      peer.subscribe(UP, { callback: (_e, m) => got.push(m.string()) });
      await peer.flush();
      const { link, closed } = await openLink();

      let accepted = 0;
      let rejectedWhileDown = 0;
      const endAt = Date.now() + 1500;
      let i = 0;
      proxy.cut();
      while (Date.now() < endAt) {
        try {
          await link.send(text(`p${i++}`));
          accepted += 1;
        } catch (error) {
          assert.equal(error.code, 'not_connected');
          rejectedWhileDown += 1;
        }
        await new Promise((resolve) => setImmediate(resolve));
      }
      assert.equal(link.state, 'closed');
      assert.equal(closed.length, 1);
      assert.ok(
        rejectedWhileDown >= 50,
        `kept publishing: ${rejectedWhileDown} sends while down`,
      );
      assert.ok(
        accepted < 50,
        `only sends racing the cut may be accepted, got ${accepted}`,
      );
      assert.equal(link.stats.queueBytes, 0);
      await new Promise((resolve) => setTimeout(resolve, 300));
      assert.ok(got.length <= accepted);
      assert.equal(new Set(got).size, got.length, 'no duplicates');
    });

    test('stalled peer: queue and socket stay under their ceilings, overload is explicit, the link then fails and releases everything', async (t) => {
      const stallMs = 2000;
      const { link, closed } = await openLink({
        limits: { writeStallMs: stallMs },
      });
      const bound = link.memoryBound;
      const before = process.memoryUsage();
      proxy.stallUpstream();

      const payload = new Uint8Array(60 * 1024);
      const outcomes = {
        accepted: 0,
        queue_full: 0,
        not_connected: 0,
        other: {},
      };
      let maxQueueBytes = 0;
      let maxQueueMessages = 0;
      let maxSocket = 0;
      const settled = [];
      const startedAt = Date.now();
      while (
        Date.now() - startedAt < stallMs + 3000 &&
        link.state === 'ready'
      ) {
        for (let n = 0; n < 8; n++) {
          settled.push(
            link.send(payload).then(
              () => outcomes.accepted++,
              (e) => {
                if (e.code in outcomes) outcomes[e.code]++;
                else outcomes.other[e.code] = (outcomes.other[e.code] ?? 0) + 1;
              },
            ),
          );
        }
        const s = link.stats;
        maxQueueBytes = Math.max(maxQueueBytes, s.queueBytes);
        maxQueueMessages = Math.max(maxQueueMessages, s.queueMessages);
        maxSocket = Math.max(maxSocket, s.socketBuffered, s.peakSocketBuffered);
        await new Promise((resolve) => setTimeout(resolve, 2));
      }
      await Promise.all(settled);
      await waitFor(() => closed.length === 1, 'link failure');
      const after = process.memoryUsage();
      proxy.resumeUpstream();

      assert.equal(closed[0].code, 'write_stalled');
      assert.equal(link.state, 'closed');
      assert.ok(outcomes.queue_full > 0, 'overload was reported, not absorbed');
      assert.ok(maxQueueBytes <= link.limits.queueMaxBytes);
      assert.ok(maxQueueMessages <= link.limits.queueMaxMessages);
      assert.ok(maxSocket <= link.limits.socketHardCapBytes);
      assert.ok(maxSocket > 0, 'the socket buffer was actually exercised');
      assert.equal(link.stats.queueBytes, 0, 'queue released on failure');
      const growth = {
        rss: after.rss - before.rss,
        external: after.external - before.external,
        arrayBuffers: after.arrayBuffers - before.arrayBuffers,
      };
      t.diagnostic(
        JSON.stringify({
          bound,
          maxQueueBytes,
          maxQueueMessages,
          maxSocket,
          outcomes,
          growth,
        }),
      );
      // 2x: undici keeps a masked copy of each buffered frame; plus allocator slack.
      assert.ok(
        growth.arrayBuffers + growth.external <=
          2 * bound.perLinkTotal + 8 * 1024 * 1024,
      );
    });
  },
);
