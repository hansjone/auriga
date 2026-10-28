import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import http from 'node:http';
import https from 'node:https';
import { createRequire } from 'node:module';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomBytes } from 'node:crypto';
import { mkdir, readFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { persistenceMarker } from './dual-verdict.mjs';
const require = createRequire(
  new URL('../../../clients/dsh-plugin/package.json', import.meta.url),
);
const { WebSocket, WebSocketServer } = require('ws');
export { persistenceMarker };

// Reassemble NATS protocol commands, preserving every byte. Never record CONNECT
// or raw protocol streams: those contain the genuine one-use device ticket.
export function natsTap(forward, observe) {
  let pending = Buffer.alloc(0);
  return (bytes) => {
    pending = Buffer.concat([pending, bytes]);
    assert.ok(pending.length <= 2 * 1024 * 1024, 'bounded NATS relay');
    for (;;) {
      const end = pending.indexOf('\r\n');
      if (end < 0) return;
      const words = pending.subarray(0, end).toString().split(' '),
        op = words[0];
      const payload = ['PUB', 'HPUB', 'MSG', 'HMSG'].includes(op);
      const size = payload ? Number(words.at(-1)) : 0;
      assert.ok(
        Number.isSafeInteger(size) && size >= 0 && size <= 1048576,
        'NATS size',
      );
      const total = end + 2 + (payload ? size + 2 : 0);
      if (pending.length < total) return;
      const command = pending.subarray(0, total);
      pending = pending.subarray(total);
      let frame;
      if (payload) {
        const offset = ['HPUB', 'HMSG'].includes(op) ? Number(words.at(-2)) : 0;
        try {
          frame = JSON.parse(
            command.subarray(end + 2 + offset, end + 2 + size).toString(),
          );
        } catch {
          /* Non-JSON payloads are forwarded without recording protocol data. */
        }
      }
      if (!frame?.type || observe(frame) !== 'drop') forward(command);
    }
  };
}

export async function createDualProxy(directory) {
  await mkdir(directory, { recursive: true });
  const password = randomBytes(32).toString('hex'),
    run = promisify(execFile);
  const keytool = join(
    process.env.JAVA_HOME,
    'bin',
    process.platform === 'win32' ? 'keytool.exe' : 'keytool',
  );
  const pfx = join(directory, 'proxy.p12'),
    ca = join(directory, 'proxy-ca.pem');
  const invoke = (args) =>
    run(keytool, [...args, '-storepass:env', 'AURIGA_SEND4_TLS_PASSWORD'], {
      env: { ...process.env, AURIGA_SEND4_TLS_PASSWORD: password },
      windowsHide: true,
      timeout: 30000,
    });
  await invoke([
    '-genkeypair',
    '-alias',
    'proxy',
    '-keyalg',
    'EC',
    '-groupname',
    'secp256r1',
    '-dname',
    'CN=localhost',
    '-ext',
    'SAN=ip:127.0.0.1,dns:localhost',
    '-ext',
    'bc:c',
    '-validity',
    '1',
    '-storetype',
    'PKCS12',
    '-keystore',
    pfx,
  ]);
  await invoke([
    '-exportcert',
    '-rfc',
    '-alias',
    'proxy',
    '-keystore',
    pfx,
    '-file',
    ca,
  ]);
  const tls = { pfx: await readFile(pfx), passphrase: password };
  const frames = [],
    submissions = [],
    drops = [],
    failures = [],
    connections = new Set();
  const endpoints = new Map();
  let backend,
    gateway,
    team,
    gatewayCa,
    dropSubmit = false,
    dropReceipt = false;
  const server = https.createServer(tls, async (req, res) => {
    let target;
    try {
      const path = req.url.split('?')[0];
      target =
        path === '/api/ai/v1/network/submit'
          ? gateway?.https
          : path.startsWith('/ai/')
            ? team
            : backend;
      if (!target) {
        res.writeHead(503);
        res.end();
        return;
      }
      const chunks = [];
      let size = 0;
      for await (const chunk of req) {
        size += chunk.length;
        assert.ok(size <= 1048576);
        chunks.push(chunk);
      }
      const bytes = Buffer.concat(chunks);
      let drop = false;
      if (path === '/api/ai/v1/network/submit') {
        const request = JSON.parse(bytes.toString());
        const source = [...endpoints.values()].find(
          (v) => v.binding === request.source.source_binding,
        );
        assert.ok(source, 'known real source binding required');
        const ledger = await source.host.ledger();
        const saved = Object.values(ledger.durable.submissions).find(
          (v) => v.request.idempotency.key === request.idempotency.key,
        );
        assert.ok(
          saved &&
            JSON.stringify(saved.request) === JSON.stringify(request) &&
            saved.bindingId,
          persistenceMarker,
        );
        submissions.push({ request, bytes: bytes.toString(), durable: true });
        drop = dropSubmit;
        dropSubmit = false;
      }
      const url = new URL(req.url, target);
      const headers = { ...req.headers, host: new URL(origin).host };
      // The loopback HTTP IAM hop is a test-only installation proxy. Only fixed
      // authority headers are supplied, never model/session/tenant identity.
      for (const key of Object.keys(headers))
        if (
          key.startsWith('x-auriga-entry-') ||
          key.startsWith('auriga-forwarded-')
        )
          delete headers[key];
      if (target === backend) {
        headers['x-auriga-entry-origin'] = origin;
        headers['x-auriga-entry-role'] = 'portal';
      }
      const transport = url.protocol === 'https:' ? https : http;
      const upstream = transport.request(
        url,
        { method: req.method, headers, ca: gatewayCa },
        (response) => {
          const parts = [];
          response.on('data', (chunk) => parts.push(chunk));
          response.on('end', () => {
            if (drop && response.statusCode === 200) {
              drops.push({
                kind: 'submit-response',
                request: JSON.parse(bytes.toString()),
                result: Buffer.concat(parts).toString(),
              });
              res.destroy();
            } else {
              res.writeHead(response.statusCode, response.headers);
              res.end(Buffer.concat(parts));
            }
          });
        },
      );
      upstream.on('error', () => {
        res.writeHead(502);
        res.end();
      });
      upstream.end(bytes);
    } catch (error) {
      failures.push(error);
      res.destroy();
    }
  });
  const wss = new WebSocketServer({ noServer: true });
  server.on('upgrade', (req, socket, head) => {
    if (!gateway || !/^\/nats\/[AB]$/.test(req.url)) {
      socket.destroy();
      return;
    }
    const name = req.url.at(-1);
    wss.handleUpgrade(req, socket, head, (down) => {
      const up = new WebSocket(gateway.wss, { ca: gatewayCa });
      connections.add(down);
      connections.add(up);
      down.on('close', () => {
        connections.delete(down);
        up.close();
      });
      up.on('close', () => {
        connections.delete(up);
        down.close();
      });
      for (const ws of [up, down])
        ws.on('error', (error) =>
          failures.push(new Error('owned WSS relay: ' + error.code)),
        );
      const queue = [];
      const toUp = (bytes) => {
        if (up.readyState === WebSocket.OPEN) up.send(bytes);
        else queue.push(bytes);
      };
      up.on('open', () => {
        for (const bytes of queue) up.send(bytes);
        queue.length = 0;
      });
      const observe = (direction) => (frame) => {
        frames.push({ name, direction, frame });
        if (
          name === 'B' &&
          direction === 'up' &&
          frame.type === 'receipt' &&
          dropReceipt
        ) {
          const ledger = JSON.parse(
            readFileSync(endpoints.get('B').host.ledgerFile, 'utf8'),
          );
          const accepted =
            ledger.durable.inbound[frame.results[0].recipient_id];
          assert.equal(
            accepted.stage,
            'accepted',
            'SEND-4 durable acceptance must precede lost receipt',
          );
          assert.equal(accepted.receipt.operation_id, frame.operation_id);
          dropReceipt = false;
          drops.push({ kind: 'receipt', frame, durableStage: accepted.stage });
          return 'drop';
        }
      };
      const listen = (socket, tap) =>
        socket.on('message', (bytes) => {
          try {
            tap(bytes);
          } catch (error) {
            failures.push(error);
            down.close();
            up.close();
          }
        });
      listen(down, natsTap(toUp, observe('up')));
      listen(
        up,
        natsTap((bytes) => {
          if (down.readyState === WebSocket.OPEN) down.send(bytes);
        }, observe('down')),
      );
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const origin = 'https://127.0.0.1:' + server.address().port;
  return {
    origin,
    ca,
    frames,
    submissions,
    drops,
    failures,
    endpoints,
    attach: async (input) => {
      backend = input.backend;
      gateway = input.gateway;
      team = input.team;
      gatewayCa = await readFile(gateway.ca);
    },
    dropSubmit: () => {
      dropSubmit = true;
    },
    dropReceipt: () => {
      dropReceipt = true;
    },
    async close() {
      for (const ws of connections) ws.close();
      await new Promise((resolve, reject) =>
        wss.close((error) => (error ? reject(error) : resolve())),
      );
      await new Promise((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    },
  };
}
