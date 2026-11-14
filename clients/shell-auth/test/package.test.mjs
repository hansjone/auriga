import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  generateKeyPairSync,
  randomUUID,
  randomBytes,
  sign,
  createHash,
} from 'node:crypto';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  rmSync,
} from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { once } from 'node:events';
import { buildBroker } from '../build.mjs';

test(
  'self-contained installed broker starts control page, completes signed RFC8628 login and restores DPAPI credentials after update',
  { skip: process.platform !== 'win32', timeout: 30000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-installed-auth-'));
    let desktop;
    t.after(async () => {
      await desktop?.close();
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    const output = join(directory, 'version', 'auriga', 'shell-auth'),
      root = join(directory, 'installation'),
      dataHome = join(directory, 'private');
    const manifest = buildBroker(output);
    for (const file of manifest.files)
      assert.equal(
        createHash('sha256')
          .update(readFileSync(join(output, file.path)))
          .digest('hex'),
        file.sha256,
      );
    const bundledNetwork = await import(
      pathToFileURL(join(output, 'network-core.bundle.mjs')).href
    );
    assert.equal(typeof bundledNetwork.NatsLink, 'function');
    mkdirSync(root);
    writeFileSync(
      join(root, 'desktop-origin.json'),
      JSON.stringify({ origin: 'https://example.com' }),
    );
    const keys = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
    const kid = randomUUID(),
      jwk = {
        ...keys.publicKey.export({ format: 'jwk' }),
        kid,
        alg: 'ES256',
        use: 'sig',
      };
    const source = {
      sourceSessionId: randomUUID(),
      providerId: randomUUID(),
      authGeneration: 1,
    };
    const claims = {
      iss: 'https://example.com',
      sub: randomUUID(),
      tid: randomUUID(),
      did: randomUUID(),
      aud: 'auriga',
      ptyp: 'device',
      ctx: 'device',
      scope: 'auriga.connect',
      iat: Math.floor(Date.now() / 1000),
      exp: Math.floor(Date.now() / 1000) + 600,
      uacSessionBinding: source,
    };
    const body = [
      Buffer.from(
        JSON.stringify({ alg: 'ES256', kid, typ: 'at+jwt' }),
      ).toString('base64url'),
      Buffer.from(JSON.stringify(claims)).toString('base64url'),
    ].join('.');
    const accessToken =
      body +
      '.' +
      sign('sha256', Buffer.from(body), {
        key: keys.privateKey,
        dsaEncoding: 'ieee-p1363',
      }).toString('base64url');
    const userCode = [...randomBytes(8)]
      .map((value) => 'BCDFGHJKLMNPQRSTVWXZ'[value % 20])
      .join('')
      .replace(/^(.{4})/, '$1-');
    const deviceCode = randomUUID(),
      correlationId = randomUUID(),
      memberId = randomUUID();
    const originalFetch = globalThis.fetch;
    const seen = [];
    globalThis.fetch = async (url, options = {}) => {
      const parsed = new URL(url);
      if (parsed.protocol === 'http:') return originalFetch(url, options);
      assert.equal(parsed.origin, 'https://example.com');
      const path = parsed.pathname;
      seen.push(path);
      if (path === '/oauth2/device_authorization')
        return Response.json(
          {
            device_code: deviceCode,
            user_code: userCode,
            verification_uri: 'https://example.com/device',
            expires_in: 300,
            interval: 5,
          },
          { headers: { 'X-Auriga-Correlation-Id': correlationId } },
        );
      if (path === '/oauth2/token')
        return Response.json({
          access_token: accessToken,
          refresh_token: randomUUID(),
          token_type: 'Bearer',
          scope: 'auriga.connect',
        });
      if (path === '/oauth2/jwks') return Response.json({ keys: [jwk] });
      if (path === '/api/iam/v1/me/device-session')
        return Response.json({
          accountId: claims.sub,
          tenantId: claims.tid,
          memberId,
          deviceId: claims.did,
          providerId: source.providerId,
          clientId: 'auriga-dsh',
          authorizationMode: 'standard_device',
          expiresAt: new Date(Date.now() + 600000).toISOString(),
          uacSessionBinding: source,
        });
      throw new Error('unexpected installed broker route');
    };
    t.after(() => {
      globalThis.fetch = originalFetch;
    });
    const { startDesktopAuth } = await import(
      pathToFileURL(join(output, 'desktop.mjs')).href
    );
    const opened = [];
    desktop = await startDesktopAuth({
      root,
      dataHome,
      openBrowser: async (url) => {
        opened.push(url);
      },
    });
    t.diagnostic('installed broker started');
    assert.equal(desktop.snapshot().state, 'signed_out');
    const launch = new URL(opened[0]);
    const page = await originalFetch(launch.origin);
    assert.ok((await page.text()).includes('page.js'));
    const headers = {
      Origin: launch.origin,
      'Content-Type': 'application/json',
      'Sec-Fetch-Site': 'same-origin',
      'Sec-Fetch-Mode': 'cors',
      'Sec-Fetch-Dest': 'empty',
    };
    const session = await (
      await originalFetch(new URL('/loopback/exchange', launch), {
        method: 'POST',
        headers,
        body: JSON.stringify({
          ticket: launch.hash.slice(1),
          clientNonce: randomBytes(32).toString('base64url'),
        }),
      })
    ).json();
    const message = async (action) =>
      (
        await originalFetch(new URL('/loopback/messages', launch), {
          method: 'POST',
          headers: {
            ...headers,
            'X-Auriga-Shell-Token': session.pageToken,
            'X-Auriga-Shell-CSRF': session.csrf,
            'X-Auriga-Shell-Nonce': session.clientNonce,
          },
          body: JSON.stringify({
            protocolVersion: 1,
            requestId: randomUUID(),
            shellEpoch:
              action === 'snapshot'
                ? '00000000-0000-0000-0000-000000000000'
                : desktop.broker.epoch,
            expectedRevision:
              action === 'snapshot' ? 0 : desktop.broker.revision,
            action,
          }),
        })
      ).json();
    await message('open_login');
    const attempt = (await message('snapshot')).snapshot.deviceAttempt;
    assert.equal(attempt.userCode, userCode);
    assert.equal(opened[1], 'https://example.com/device');
    while (desktop.snapshot().state === 'authenticating')
      await once(desktop.broker, 'changed');
    assert.equal(desktop.snapshot().state, 'authenticated');
    assert.equal(desktop.snapshot().identity.memberId, memberId);
    assert.ok(
      !readFileSync(
        join(dataHome, 'auriga', 'shell-auth', 'credentials.dpapi'),
      ).includes(Buffer.from(accessToken)),
    );
    const oldEpoch = desktop.broker.epoch;
    t.diagnostic('closing first broker');
    await desktop.close();
    t.diagnostic('first broker closed');
    desktop = await startDesktopAuth({
      root,
      dataHome,
      openBrowser: async (url) => {
        opened.push(url);
      },
    });
    t.diagnostic('restored broker started');
    assert.equal(desktop.snapshot().state, 'authenticated');
    assert.equal(
      opened.length,
      2,
      'restored login must not open another control page',
    );
    assert.notEqual(desktop.broker.epoch, oldEpoch);
    assert.ok(!seen.some((path) => path.includes('/instance/')));
    const logs = readFileSync(
      join(dataHome, 'auriga', 'logs', 'auriga-shell-auth.jsonl'),
      'utf8',
    );
    assert.ok(logs.includes(correlationId));
    for (const secret of [accessToken, deviceCode, userCode])
      assert.ok(!logs.includes(secret));
  },
);
