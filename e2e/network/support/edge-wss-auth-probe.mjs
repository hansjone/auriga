/* global Buffer, performance, clearTimeout */
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createNatsBinaryPreparer } from '../../../clients/dsh-plugin/test/network/transport/support/nats-binary.js';
import { startNatsServer } from '../../../clients/dsh-plugin/test/network/transport/support/nats-server.js';
import { NatsLink } from '../../../clients/dsh-plugin/src/network/transport/nats-link.js';
import { createWsSocketFactory } from '../../../clients/dsh-plugin/src/network/transport/ws-socket.js';
import { createWireObserver, brokerRejected } from './edge-wss-wire.mjs';

// Diagnostic only: real Broker rejects a wrong static token. No IAM/PG,
// callout success, application READY, or full-business verdict is supplied.
export async function probeAuthClose(url, rootPem) {
  const wire = createWireObserver();
  const events = [];
  const started = performance.now();
  const record = (kind, fields = {}) =>
    events.push({
      kind,
      elapsedMillis: Math.round(performance.now() - started),
      ...fields,
    });
  let closed = Promise.resolve();
  const link = new NatsLink({
    url,
    auth: { token: 'probe-denied' },
    upSubject: 'probe.up',
    downSubject: 'probe.down',
    inboxPrefix: 'probe.reply',
    connectTimeoutMs: 3000,
    createSocket(target) {
      const socket = createWsSocketFactory({
        ca: rootPem,
        origin: 'https://localhost',
      })(target);
      const observeSend = socket.send.bind(socket);
      socket.send = function (data, options, callback) {
        const bytes = Buffer.from(
          data.buffer ?? data,
          data.byteOffset ?? 0,
          data.byteLength,
        );
        record('native-write', {
          bytes: bytes.length,
          initialPair:
            bytes.toString().startsWith('CONNECT ') &&
            bytes.toString().endsWith('\r\nPING\r\n'),
        });
        return observeSend(data, options, callback);
      };
      for (const name of ['close', 'terminate']) {
        const original = socket[name].bind(socket);
        socket[name] = (...args) => {
          record('client-' + name);
          return original(...args);
        };
      }
      socket.on('upgrade', (response) => {
        record('upgrade', { verified: response.socket.authorized });
        response.socket.on('end', () => record('transport-end'));
        response.socket.on('error', (error) =>
          record('transport-error', { code: error.code ?? 'unknown' }),
        );
      });
      socket.on('message', (data) => {
        wire.message(data);
        record('message', {
          frames: wire.evidence().frames,
          authError: wire.evidence().authError,
        });
      });
      socket.on('error', (error) =>
        record('socket-error', { code: error.code ?? 'unknown' }),
      );
      closed = new Promise((done) =>
        socket.once('close', (code, reason) => {
          wire.close(code, reason);
          record('socket-close', {
            code,
            reason: wire.evidence().close.reason,
          });
          done();
        }),
      );
      return socket;
    },
  });
  let errorCode;
  try {
    await link.open();
    record('UNEXPECTED-AUTHORIZED');
  } catch (error) {
    errorCode = error.code;
    record('link-rejected', { code: errorCode });
  } finally {
    await link.close();
    await Promise.race([closed, delay(1000)]);
  }
  return {
    errorCode,
    rejected: brokerRejected(wire.evidence()),
    wire: wire.evidence(),
    events,
  };
}

async function probeManual(url, rootPem, mode) {
  const wire = createWireObserver();
  let sent = false;
  const socket = createWsSocketFactory({
    ca: rootPem,
    origin: 'https://localhost',
  })(url);
  socket.binaryType = 'arraybuffer';
  const result = await new Promise((done) => {
    const timer = setTimeout(() => socket.terminate(), 3000);
    socket.on('error', () => {});
    socket.on('message', (data) => {
      wire.message(data);
      if (sent || !wire.evidence().info) return;
      sent = true;
      const connect = `CONNECT ${JSON.stringify({ lang: 'nats.ws', version: 'probe', protocol: 1, verbose: false, pedantic: false, auth_token: 'probe-denied' })}\r\n`;
      if (mode === 'one-frame') socket.send(connect + 'PING\r\n');
      else {
        socket.send(connect, { fin: mode !== 'fragmented' });
        if (mode === 'two-frames' || mode === 'fragmented')
          socket.send('PING\r\n');
      }
    });
    socket.once('close', (code, reason) => {
      clearTimeout(timer);
      wire.close(code, reason);
      done({
        mode,
        rejected: brokerRejected(wire.evidence()),
        wire: wire.evidence(),
      });
    });
  });
  return result;
}

if (['--direct', '--compare', '--direct-fixed'].includes(process.argv[2])) {
  const target = resolve('e2e/network/target');
  await mkdir(target, { recursive: true });
  const binary = await createNatsBinaryPreparer({
    stageRoot: join(target, 'nats-cache'),
  })();
  const broker = await startNatsServer({
    binary,
    authConfig: 'authorization { token: "probe-allowed" }',
  });
  const results = [];
  try {
    if (process.argv[2] !== '--compare')
      for (
        let index = 0;
        index < (process.argv[2] === '--direct' ? 60 : 120);
        index++
      )
        results.push(await probeAuthClose(broker.url, broker.rootPem));
    else
      for (const mode of [
        'connect-only',
        'one-frame',
        'two-frames',
        'fragmented',
      ])
        for (let index = 0; index < 120; index++)
          results.push(await probeManual(broker.url, broker.rootPem, mode));
  } finally {
    await broker.close();
  }
  const path = join(
    await mkdtemp(join(target, 'edge-wss-auth-probe-')),
    'manifest.json',
  );
  await writeFile(
    path,
    JSON.stringify(
      {
        node: process.version,
        platform: process.platform,
        broker: '2.15.1',
        attempts: results.length,
        realRejections: results.filter((v) => v.rejected).length,
        results,
      },
      null,
      2,
    ),
  );
  console.log(
    JSON.stringify({
      path,
      attempts: results.length,
      realRejections: results.filter((v) => v.rejected).length,
      modes: ['connect-only', 'one-frame', 'two-frames', 'fragmented'].map(
        (mode) => ({
          mode,
          attempts: results.filter((v) => v.mode === mode).length,
          rejected: results.filter((v) => v.mode === mode && v.rejected).length,
        }),
      ),
    }),
  );
  if (
    process.argv[2] === '--direct-fixed' &&
    results.some(
      (v) =>
        !v.rejected ||
        v.events.filter((e) => e.kind === 'native-write').length !== 1 ||
        !v.events.some((e) => e.kind === 'native-write' && e.initialPair),
    )
  )
    process.exitCode = 1;
}
