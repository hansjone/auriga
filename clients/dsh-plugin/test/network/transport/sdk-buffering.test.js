import { after, before, describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { wsconnect } from '@nats-io/nats-core';
import { GuardedSocket } from '../../../src/network/transport/guarded-socket.js';
import { createCaSocketFactory, waitFor } from './support/client.js';
import { natsBinary, skipNoServer } from './support/env.js';
import { startNatsServer } from './support/nats-server.js';
import { startTcpProxy } from './support/tcp-proxy.js';

// Evidence for the design's "SDK buffer hard gate": what @nats-io/nats-core 3.4.0
// does by itself, and that the transport's guard bounds it anyway.

describe(
  'raw @nats-io/nats-core 3.4.0 buffering vs the guard',
  { skip: skipNoServer },
  () => {
    /** @type {Awaited<ReturnType<typeof startNatsServer>>} */
    let server;
    /** @type {Awaited<ReturnType<typeof startTcpProxy>>} */
    let proxy;
    const closers = [];

    before(async () => {
      server = await startNatsServer({
        binary: /** @type {string} */ (natsBinary),
      });
      proxy = await startTcpProxy(server.port);
    });
    after(async () => {
      await Promise.all(closers.map((fn) => fn().catch(() => {})));
      await proxy.close();
      await server.close();
    });

    const connect = async (wrap, extra = {}) => {
      const create = createCaSocketFactory(server.rootPem);
      let raw;
      const nc = await wsconnect({
        servers: `wss://localhost:${proxy.port}`,
        reconnect: false,
        wsFactory: async (url) => {
          raw = create(url);
          return { socket: wrap ? wrap(raw) : raw, encrypted: true };
        },
        ...extra,
      });
      closers.push(async () => {
        raw.terminate();
        await nc.close();
      });
      return { nc, raw: () => raw };
    };

    test('without a guard the SDK keeps writing into a stalled socket: bufferedAmount is not capped', async (t) => {
      const { nc, raw } = await connect();
      proxy.stallUpstream();
      const payload = new Uint8Array(32 * 1024);
      let peak = 0;
      const deadline = Date.now() + 4000;
      while (Date.now() < deadline && peak < 32 * 1024 * 1024) {
        for (let i = 0; i < 64; i++) nc.publish('x.y', payload);
        await new Promise((resolve) => setTimeout(resolve, 1));
        peak = Math.max(peak, raw().bufferedAmount);
      }
      t.diagnostic(
        `unguarded peak bufferedAmount=${peak} bytes (ceiling would be 524288)`,
      );
      // 16 MiB is already 32x the guard's hard cap and the loop was still going.
      assert.ok(peak > 16 * 1024 * 1024);
      proxy.resumeUpstream();
      raw().terminate();
    });

    test('with the guard the same flood stops at the hard cap, the socket is torn down and the failure is reported', async (t) => {
      const hardCap = 512 * 1024;
      const events = [];
      let guard;
      const { nc, raw } = await connect((inner) => {
        guard = new GuardedSocket(inner, {
          hardCapBytes: hardCap,
          onOverflow: (info) => events.push(info),
          onClose: () => events.push('close'),
        });
        return guard;
      });
      proxy.stallUpstream();
      const payload = new Uint8Array(32 * 1024);
      let peak = 0;
      const deadline = Date.now() + 4000;
      while (Date.now() < deadline && !guard.overflowed) {
        for (let i = 0; i < 64; i++) {
          try {
            nc.publish('x.y', payload);
          } catch {
            // closed connection
          }
        }
        await new Promise((resolve) => setTimeout(resolve, 1));
        peak = Math.max(peak, raw().bufferedAmount, guard.peakBuffered);
      }
      await waitFor(() => guard.closed, 'guard teardown', 5000);
      t.diagnostic(
        `guarded peak bufferedAmount=${peak} bytes, hard cap ${hardCap}`,
      );
      assert.equal(guard.overflowed, true);
      assert.ok(peak <= hardCap, `peak ${peak}`);
      assert.equal(events.filter((e) => typeof e === 'object').length, 1);
      proxy.resumeUpstream();
    });

    test('SDK reconnect mode keeps publishes in an unbounded outbound buffer while disconnected (why the transport sets reconnect:false and gates publishing)', async (t) => {
      const create = createCaSocketFactory(server.rootPem);
      const sockets = [];
      const own = await startTcpProxy(server.port);
      const nc = await wsconnect({
        servers: `wss://localhost:${own.port}`,
        reconnect: true,
        maxReconnectAttempts: -1,
        reconnectTimeWait: 100,
        wsFactory: async (url) => {
          const s = create(url);
          sockets.push(s);
          return { socket: s, encrypted: true };
        },
      });
      closers.push(async () => {
        sockets.forEach((s) => s.terminate());
        await nc.close();
      });
      // Broker unreachable: the SDK stays in its reconnect loop, believing it is disconnected.
      await own.close();
      await waitFor(() => sockets[0].readyState === 3, 'disconnect');
      await new Promise((resolve) => setTimeout(resolve, 300));
      const payload = new Uint8Array(32 * 1024);
      for (let i = 0; i < 2000; i++) nc.publish('x.y', payload);
      // Read-only peek at SDK internals purely as evidence; the product code never touches it.
      const retained = /** @type {any} */ (nc).protocol?.outbound?.size?.();
      t.diagnostic(
        `SDK outbound retained while disconnected: ${retained} bytes after 2000 publishes of 32 KiB`,
      );
      assert.ok(
        retained > 32 * 1024 * 1024,
        'no ceiling on the disconnected backlog',
      );
    });
  },
);
