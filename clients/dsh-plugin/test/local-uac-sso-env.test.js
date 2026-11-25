import test from 'node:test';
import assert from 'node:assert/strict';
import { createCipheriv, randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  decryptChromiumValue,
  domainMatches,
  resolveCookieDomain,
} from '../src/local/uac/browser-cookies.js';
import { createUacAdapter } from '../src/local/uac/adapter.js';
import { createLocalUacIdentity } from '../src/local/uac/session.js';
import { createEnterpriseEnvironment } from '../src/local/enterprise-environment.js';

function encryptV10(plaintext, key) {
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  const body = Buffer.concat([
    cipher.update(plaintext, 'utf8'),
    cipher.final(),
  ]);
  const tag = cipher.getAuthTag();
  return Buffer.concat([Buffer.from('v10', 'ascii'), nonce, body, tag]);
}

function syntheticAdapter(overrides = {}) {
  return {
    allowedDestinations: [
      { origin: 'https://identity.example.com', addresses: ['192.0.2.10'] },
    ],
    clientIP: '',
    tokenVerifyUrl: 'https://identity.example.com/verify',
    qr: {
      path: '/scan',
      contentTemplate: 'example:${x}:${y}:${systemCode}',
      body: { a: '${x}', b: '${y}' },
      statusPointer: '/state',
      waiting: 'waiting',
      scanned: 'scanned',
      success: 'verified',
      tokenPointer: '/token',
      empNoPointer: '/employee',
    },
    department: {
      url: 'https://identity.example.com/info',
      body: { credential: '${token}', employee: '${empNo}' },
      successPointer: '/ok',
      successValue: true,
      listPointer: '/rows',
      empNoPointer: '/employee',
      departmentPointers: ['/department'],
    },
    cookies: {
      domain: '.corp.example.com',
      portal: {
        token: { cookie: 'PORTAL_TOKEN' },
        account: { cookie: 'PORTAL_USER' },
      },
      uds: {
        token: { cookie: 'UDS_TOKEN' },
        account: { cookie: 'UDS_USER' },
      },
    },
    ...overrides,
  };
}

test('browser-cookies: domain resolution, matching and Chromium v10/v20 decryption', () => {
  assert.equal(
    resolveCookieDomain(undefined, 'https://uac.corp.com.cn/api'),
    '.corp.com.cn',
  );
  assert.equal(
    resolveCookieDomain('corp.example.com', 'https://uac.other.example'),
    '.corp.example.com',
  );
  assert.equal(domainMatches('.corp.com.cn', '.corp.com.cn'), true);
  assert.equal(domainMatches('aip.corp.com.cn', '.corp.com.cn'), true);
  assert.equal(domainMatches('evil-corp.com.cn', '.corp.com.cn'), false);

  const key = randomBytes(32);
  const enc = encryptV10('tok-secret-123', key);
  assert.equal(decryptChromiumValue(enc, key), 'tok-secret-123');
  const v20 = Buffer.concat([Buffer.from('v20', 'ascii'), randomBytes(40)]);
  assert.equal(decryptChromiumValue(v20, key), null);
});

test('adapter extractSsoCredentials prefers portal pair, falls back to UDS, never mixes pairs', () => {
  const adapter = createUacAdapter(syntheticAdapter(), {
    baseUrl: 'https://identity.example.com',
    systemCode: 'SYS1',
  });
  const spec = adapter.ssoCookieSpec();
  assert.equal(spec.domain, '.corp.example.com');
  assert.deepEqual(spec.names.sort(), [
    'PORTAL_TOKEN',
    'PORTAL_USER',
    'UDS_TOKEN',
    'UDS_USER',
  ]);

  // Portal pair takes precedence
  const both = new Map([
    ['PORTAL_USER', '001234'],
    ['PORTAL_TOKEN', 'p-tok'],
    ['UDS_USER', '999999'],
    ['UDS_TOKEN', 'u-tok'],
  ]);
  assert.deepEqual(adapter.extractSsoCredentials(both), {
    source: 'portal',
    empNo: '001234',
    token: 'p-tok',
    clientIP: '',
  });

  // Mixed incomplete pairs are rejected
  const mixed = new Map([
    ['PORTAL_USER', '001234'],
    ['UDS_TOKEN', 'u-tok'],
  ]);
  assert.equal(adapter.extractSsoCredentials(mixed), null);

  // UDS pair works when portal pair is absent
  const udsOnly = new Map([
    ['UDS_USER', '009876'],
    ['UDS_TOKEN', 'u-tok-2'],
  ]);
  assert.deepEqual(adapter.extractSsoCredentials(udsOnly), {
    source: 'uds',
    empNo: '009876',
    token: 'u-tok-2',
    clientIP: '',
  });
});

test('local UAC silent SSO auto-authenticates with department and injects DSH_AURIGA_ID/TOKEN without platform', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-uac-sso-'));
  try {
    let verifyCalls = 0;
    const identity = createLocalUacIdentity({
      directory: dir,
      loadConfig: async () => ({
        adapter: syntheticAdapter(),
        provider: {
          baseUrl: 'https://identity.example.com',
          systemCode: 'SYS1',
        },
        revalidation: {
          strategy: 'token-verify',
          intervalSeconds: 7200,
          retryIntervalSeconds: 30,
          maxRetries: 3,
        },
      }),
      createAdapter: (settings, provider) => {
        const real = createUacAdapter(settings, provider);
        return {
          ...real,
          verify: async (cred) => {
            verifyCalls++;
            return {
              success: true,
              identityMatched: true,
              empNo: cred.empNo,
              token: 'verified-' + cred.token,
            };
          },
          department: async () => ({
            available: true,
            department: 'R&D / Platform',
          }),
        };
      },
      readBrowserCookies: async () =>
        new Map([
          ['PORTAL_USER', '0010329667'],
          ['PORTAL_TOKEN', 'sso-cookie-token'],
        ]),
    });

    await identity.ready;
    assert.equal(verifyCalls, 1);
    const st = identity.status();
    assert.equal(st.state, 'authenticated');
    assert.equal(st.empNo, '0010329667');
    assert.equal(st.department, 'R&D / Platform');
    assert.equal(st.authMethod, 'sso');

    // Enterprise environment injects DSH_AURIGA_ID and DSH_AURIGA_TOKEN even when platform is offline/unauthenticated
    const env = createEnterpriseEnvironment({
      auth: { mint: () => null, revoke: () => {} },
      getServer: () => null,
      authorization: () => null,
      localUacCredentials: () => identity.credentials(),
    });
    const resolved = env.contributor.resolve({ token: Symbol('exec') });
    assert.equal(resolved.DSH_AURIGA_ID, '0010329667');
    assert.equal(resolved.DSH_AURIGA_TOKEN, 'verified-sso-cookie-token');

    // After logout, credentials are immediately cleared and no longer injected
    await identity.logout();
    assert.equal(identity.status().state, 'signed_out');
    assert.equal(identity.credentials(), null);
    const afterLogout = env.contributor.resolve({ token: Symbol('exec2') });
    assert.equal(afterLogout.DSH_AURIGA_ID, '');
    assert.equal(afterLogout.DSH_AURIGA_TOKEN, '');

    await identity.close();
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
