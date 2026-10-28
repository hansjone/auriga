import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { readFile, mkdtemp, mkdir, rm } from 'node:fs/promises';
import https from 'node:https';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';
import { postSubmit } from './support/gateway-submit.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));

test(
  'submit driver uses explicit client credentials and isolated server trust without changing request/result bytes',
  { timeout: 60000 },
  async (t) => {
    const dir = await mkdtemp(path.join(os.tmpdir(), 'auriga-submit-driver-'));
    let server;
    try {
      if (process.platform === 'win32') privatePath(dir, true);
      const trusted = path.join(dir, 'trusted');
      const unrelated = path.join(dir, 'unrelated');
      // Existing stdlib-only generator supplies disposable test certificates.
      // The production assembly's distinct CA and real handler are tested in Go.
      for (const target of [trusted, unrelated]) {
        await mkdir(target);
        execFileSync(
          'go',
          ['run', path.join(root, 'scripts/beta-ui/materialize.go'), target],
          { cwd: root, windowsHide: true, timeout: 30000, stdio: 'pipe' },
        );
      }
      const received = [];
      const response =
        ' { "status": "platform_stored", "opaque": "original bytes" }\n';
      server = https.createServer(
        {
          cert: await readFile(path.join(trusted, 'platform.pem')),
          key: await readFile(path.join(trusted, 'platform.key')),
          ca: await readFile(path.join(trusted, 'ca.pem')),
          requestCert: true,
          rejectUnauthorized: true,
          minVersion: 'TLSv1.2',
        },
        (req, res) => {
          const parts = [];
          req.on('data', (part) => parts.push(part));
          req.on('end', () => {
            received.push({
              method: req.method,
              path: req.url,
              authorization: req.headers.authorization,
              type: req.headers['content-type'],
              body: Buffer.concat(parts).toString(),
              authorized: req.socket.authorized,
            });
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(response);
          });
        },
      );
      await new Promise((resolve, reject) => {
        server.once('error', reject);
        server.listen(0, '127.0.0.1', resolve);
      });
      const gateway = {
        https: `https://127.0.0.1:${server.address().port}`,
        ca: path.join(trusted, 'ca.pem'),
        submitServerName: 'platform.beta.internal',
        submitClientCertFile: path.join(trusted, 'edge.pem'),
        submitClientKeyFile: path.join(trusted, 'edge.key'),
      };
      const body = {
        kind: 'message',
        body: { text: '原始请求', optional: { preserved: true } },
        idempotency: {
          producer_epoch: '18446744073709551615',
          key: 'unit-key',
        },
      };
      assert.deepEqual(await postSubmit(gateway, body, 'unit-device-token'), {
        status: 200,
        body: response,
      });
      assert.deepEqual(received, [
        {
          method: 'POST',
          path: '/api/ai/v1/network/submit',
          authorization: 'Bearer unit-device-token',
          type: 'application/json',
          body: JSON.stringify(body),
          authorized: true,
        },
      ]);
      for (const [name, change] of [
        ['missing-credentials', { submitClientKeyFile: undefined }],
        [
          'missing-key-file',
          { submitClientKeyFile: path.join(dir, 'missing') },
        ],
        [
          'wrong-client-ca',
          {
            submitClientCertFile: path.join(unrelated, 'edge.pem'),
            submitClientKeyFile: path.join(unrelated, 'edge.key'),
          },
        ],
        ['wrong-server-ca', { ca: path.join(unrelated, 'ca.pem') }],
        ['wrong-server-name', { submitServerName: 'untrusted.example.com' }],
        ['plaintext', { https: gateway.https.replace('https:', 'http:') }],
      ]) {
        await t.test(name, async () => {
          await assert.rejects(
            postSubmit({ ...gateway, ...change }, body, 'unit-device-token'),
          );
          assert.equal(
            received.length,
            1,
            'failed TLS/configuration must never reach HTTP',
          );
        });
      }
    } finally {
      if (server?.listening)
        await new Promise((resolve) => {
          server.close(resolve);
          server.closeAllConnections();
        });
      await rm(dir, { recursive: true, force: true });
    }
  },
);
