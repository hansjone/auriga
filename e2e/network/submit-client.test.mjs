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
import { postSubmit, installSubmitFetch } from './support/gateway-submit.mjs';
import {
  assemblyDeviceProvider,
  prepareDevice,
} from './support/assembly-device.mjs';
import { testRoots } from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';
import { DeviceControlUnavailable } from '../../clients/dsh-plugin/src/network/device-provider.js';

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
      let responseStatus = 200;
      let responseBody = response;
      let holdEntered;
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
            if (JSON.parse(Buffer.concat(parts).toString()).hold) {
              holdEntered();
              return;
            }
            res.writeHead(responseStatus, {
              'Content-Type': 'application/json',
              ...(responseStatus === 302
                ? { Location: 'https://untrusted.example.com/' }
                : {}),
            });
            res.end(responseBody);
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
      await t.test(
        'SEND-3 production provider retains credentials, denial, response bounds and cancellation over mTLS',
        async () => {
          const originalFetch = globalThis.fetch;
          const restore = installSubmitFetch(gateway);
          const credential = {
            accessToken: 'original-iam-device-token',
            claims: {
              tid: 'tenant',
              did: 'device',
              sub: 'owner',
              iss: gateway.https,
            },
          };
          const originalCredential = JSON.stringify(credential);
          let currentCredential = credential;
          try {
            assert.equal(
              await (
                await globalThis.fetch('data:text/plain,other-transport')
              ).text(),
              'other-transport',
            );
            const active = new globalThis.AbortController().signal;
            const sending = await prepareDevice(
              assemblyDeviceProvider({
                origin: gateway.https,
                network: {
                  realm: '2',
                  wssUrl: 'wss://broker.example.com',
                  roots: testRoots,
                },
                getDevice: async () => currentCredential,
              }),
              active,
            );
            assert.equal(
              (await sending.submit(body, active)).toString(),
              response,
            );
            assert.equal(
              received.at(-1).authorization,
              'Bearer ' + credential.accessToken,
            );
            assert.equal(received.at(-1).body, JSON.stringify(body));
            assert.equal(received.at(-1).authorized, true);
            assert.equal(JSON.stringify(credential), originalCredential);
            responseStatus = 403;
            await assert.rejects(
              sending.submit(body, active),
              (error) => error.status === 403,
            );
            responseStatus = 302;
            await assert.rejects(
              sending.submit(body, active),
              (error) => error instanceof DeviceControlUnavailable,
            );
            responseStatus = 200;
            responseBody = 'x'.repeat(65537);
            await assert.rejects(
              sending.submit(body, active),
              /network control response too large/,
            );
            responseBody = response;
            const countBefore = received.length;
            currentCredential = {
              ...credential,
              claims: { ...credential.claims, did: 'other-device' },
            };
            await assert.rejects(
              sending.submit(body, active),
              (error) =>
                error.status === 401 && error.code === 'network.revoked',
            );
            assert.equal(
              received.length,
              countBefore,
              'production identity guard must reject before HTTPS',
            );
            currentCredential = credential;
            const controller = new globalThis.AbortController();
            const entered = new Promise((resolve) => {
              holdEntered = resolve;
            });
            const pending = sending.submit(
              { ...body, hold: true },
              controller.signal,
            );
            const rejected = assert.rejects(pending, { name: 'AbortError' });
            await entered;
            controller.abort();
            await rejected;
          } finally {
            restore();
          }
          assert.equal(globalThis.fetch, originalFetch);
        },
      );
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
