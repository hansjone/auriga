import net from 'node:net';

/**
 * Loopback TCP relay. TLS passes through untouched, so the client still
 * verifies the real server certificate. `stallUpstream()` stops reading what
 * the client sends, the way a wedged peer or proxy would, so the client's
 * socket send buffer fills.
 *
 * @param {number} targetPort
 */
export async function startTcpProxy(targetPort) {
  const pairs = new Set();
  const server = net.createServer((client) => {
    const upstream = net.connect(targetPort, '127.0.0.1');
    const pair = { client, upstream, stalled: stalled };
    pairs.add(pair);
    client.pipe(upstream);
    upstream.pipe(client);
    if (stalled) client.pause();
    const done = () => {
      pairs.delete(pair);
      client.destroy();
      upstream.destroy();
    };
    client.on('error', done).on('close', done);
    upstream.on('error', done).on('close', done);
  });
  let stalled = false;
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return {
    port: server.address().port,
    get connections() {
      return pairs.size;
    },
    /** Stop reading client -> server bytes (existing and future connections). */
    stallUpstream() {
      stalled = true;
      for (const { client } of pairs) client.pause();
    },
    resumeUpstream() {
      stalled = false;
      for (const { client } of pairs) client.resume();
    },
    /** Drop every connection at once (network cut). */
    cut() {
      for (const { client, upstream } of pairs) {
        client.destroy();
        upstream.destroy();
      }
    },
    async close() {
      for (const { client, upstream } of pairs) {
        client.destroy();
        upstream.destroy();
      }
      await new Promise((resolve) => server.close(resolve));
    },
  };
}
