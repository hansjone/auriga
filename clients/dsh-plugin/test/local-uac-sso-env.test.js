import test from 'node:test';
import assert from 'node:assert/strict';
import { createCipheriv, createHash, randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  decryptChromiumValue,
  domainMatches,
  isChromiumV20,
  resolveCookieDomain,
  readLocalBrowserCookieSets,
} from '../src/local/uac/browser-cookies.js';
import { createUacAdapter } from '../src/local/uac/adapter.js';
import { createLocalUacIdentity } from '../src/local/uac/session.js';
import { createEnterpriseEnvironment } from '../src/local/enterprise-environment.js';

function encryptV10(plaintext, key, hostKey) {
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  const plainBuf = hostKey
    ? Buffer.concat([
        createHash('sha256').update(hostKey, 'utf8').digest(),
        Buffer.from(plaintext, 'utf8'),
      ])
    : Buffer.from(plaintext, 'utf8');
  const body = Buffer.concat([cipher.update(plainBuf), cipher.final()]);
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

test('browser-cookies: cross-domain/parent-domain matching and Chromium v10/v24/v20 decryption', () => {
  assert.equal(
    resolveCookieDomain(undefined, 'https://uac.corp.com.cn/api'),
    '.corp.com.cn',
  );
  // Explicit host is preserved so RFC 6265 parent-domain cookies match when cookies.domain != provider.baseUrl
  assert.equal(
    resolveCookieDomain('aip.corp.example.com', 'https://uac.other.example'),
    'aip.corp.example.com',
  );
  assert.equal(domainMatches('.corp.com.cn', '.corp.com.cn'), true);
  assert.equal(domainMatches('aip.corp.com.cn', '.corp.com.cn'), true);
  // Parent-domain cookie (.corp.com.cn) matches explicit portal host (aip.corp.com.cn)
  assert.equal(domainMatches('.corp.com.cn', 'aip.corp.com.cn'), true);
  assert.equal(domainMatches('aip.corp.com.cn', 'aip.corp.com.cn'), true);
  // Rejects unrelated domains and single-label TLDs
  assert.equal(domainMatches('evil-corp.com.cn', '.corp.com.cn'), false);
  assert.equal(domainMatches('evil-corp.com.cn', 'aip.corp.com.cn'), false);
  assert.equal(domainMatches('.cn', 'aip.corp.com.cn'), false);

  const key = randomBytes(32);
  const enc = encryptV10('tok-secret-123', key);
  assert.equal(
    decryptChromiumValue(enc, key, '.corp.com.cn'),
    'tok-secret-123',
  );
  // Chromium 130+ (DB v24+) prepends SHA256(host_key) to plaintext
  const encV24 = encryptV10('tok-v24-secret', key, '.corp.com.cn');
  assert.equal(
    decryptChromiumValue(encV24, key, '.corp.com.cn'),
    'tok-v24-secret',
  );
  const v20 = Buffer.concat([Buffer.from('v20', 'ascii'), randomBytes(40)]);
  assert.equal(isChromiumV20(v20), true);
  assert.equal(decryptChromiumValue(v20, key, '.corp.com.cn'), null);
});

for (const scenario of [
  { reader: { maps: [], v20Detected: true }, reason: 'v20_app_bound' },
  {
    reader: { maps: [], v20Detected: false, unreadable: true },
    reason: 'browser_store_unreadable',
  },
  {
    reader: {
      maps: [
        new Map([
          ['PORTAL_USER', 'A1001'],
          ['PORTAL_TOKEN', 'synthetic-secret'],
        ]),
      ],
      v20Detected: false,
    },
    reason: 'dns_address_mismatch',
  },
])
  test(
    'SSO exposes a safe QR fallback diagnostic: ' + scenario.reason,
    async (t) => {
      const dir = await mkdtemp(join(tmpdir(), 'auriga-uac-diagnostic-'));
      t.after(() => rm(dir, { recursive: true, force: true }));
      const events = [];
      const identity = createLocalUacIdentity({
        directory: dir,
        loadConfig: async () => ({
          adapter: syntheticAdapter(),
          provider: {
            baseUrl: 'https://identity.example.com',
            systemCode: 'EXAMPLE',
          },
          revalidation: {
            strategy: 'token-verify',
            intervalSeconds: 7200,
            retryIntervalSeconds: 30,
            maxRetries: 3,
          },
        }),
        readBrowserCookieSets: async () => scenario.reader,
        // Legacy injection is a fail-safe when this test is run against pre-change code.
        readBrowserCookies: async () => new Map(),
        createAdapter: (settings, provider) => ({
          ...createUacAdapter(settings, provider),
          verify: async () => ({
            success: false,
            failureClass: 'upstream_fault',
            faultReason: 'dns_address_mismatch',
          }),
        }),
        log: (_level, _message, fields) => events.push(fields),
      });
      t.after(() => identity.close());
      await identity.ready;
      assert.equal(identity.status().ssoDiagnostic, scenario.reason);
      assert.equal(identity.status().state, 'signed_out');
      assert.ok(events.some((event) => event.reason === scenario.reason));
      assert.doesNotMatch(
        JSON.stringify(events),
        /synthetic-secret|identity\.example\.com|A1001/,
      );
      assert.equal((await identity.start()).state, 'waiting');
    },
  );

test(
  'browser cookie reader reports v20 and unreadable synthetic stores without reading real profiles',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const { mkdir, writeFile } = await import('node:fs/promises');
    const { DatabaseSync } = await import('node:sqlite');
    const base = await mkdtemp(join(tmpdir(), 'auriga-browser-diagnostic-'));
    t.after(() => rm(base, { recursive: true, force: true }));
    const before = {
      LOCALAPPDATA: process.env.LOCALAPPDATA,
      APPDATA: process.env.APPDATA,
    };
    process.env.LOCALAPPDATA = base;
    process.env.APPDATA = base;
    t.after(() => {
      for (const [key, value] of Object.entries(before)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    });
    const root = join(base, 'Microsoft/Edge/User Data');
    await mkdir(join(root, 'Default/Network'), { recursive: true });
    await writeFile(join(root, 'Local State'), '{}');
    const file = join(root, 'Default/Network/Cookies');
    const db = new DatabaseSync(file);
    db.exec(
      'CREATE TABLE cookies(host_key TEXT,name TEXT,value TEXT,encrypted_value BLOB,expires_utc INTEGER,last_access_utc INTEGER)',
    );
    db.prepare('INSERT INTO cookies VALUES(?,?,?,?,0,0)').run(
      '.example.com',
      'TOKEN',
      '',
      Buffer.from('v20synthetic'),
    );
    db.close();
    const first = await readLocalBrowserCookieSets({
      domain: 'portal.example.com',
      names: ['TOKEN'],
    });
    assert.equal(first.v20Detected, true);
    assert.equal(first.maps.length, 0);
    await writeFile(file, 'invalid synthetic SQLite');
    const second = await readLocalBrowserCookieSets({
      domain: 'portal.example.com',
      names: ['TOKEN'],
    });
    assert.equal(second.unreadable, true);
    assert.equal(second.maps.length, 0);
  },
);

test('local UAC emits observable diagnostic logs for unconfigured and SSO fallback states', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-uac-logs-'));
  try {
    const events = [];
    const unconf = createLocalUacIdentity({
      directory: dir,
      log: (level, _msg, fields) => events.push({ level, ...fields }),
    });
    await unconf.ready;
    assert.ok(
      events.some(
        (e) => e.stage === 'configuration' && e.result === 'unconfigured',
      ),
    );
    await unconf.close();

    const ssoEvents = [];
    const fallback = createLocalUacIdentity({
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
      readBrowserCookies: async () => new Map(),
      log: (level, _msg, fields) => ssoEvents.push({ level, ...fields }),
    });
    await fallback.ready;
    assert.ok(
      ssoEvents.some(
        (e) =>
          e.stage === 'sso' &&
          e.result === 'fallback_qr' &&
          e.reason === 'no_readable_cookies',
      ),
    );
    await fallback.close();
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
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

test('local UAC silent SSO projects employee profile and injects DSH_AURIGA_ID/TOKEN without platform', async () => {
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
        const real = createUacAdapter(settings, provider, {
          http: {
            validate() {},
            async post(url, body) {
              assert.equal(url.href, 'https://identity.example.com/info');
              assert.equal(body.employee, '0010329667');
              assert.equal(body.credential, 'verified-sso-cookie-token');
              return {
                status: 200,
                body: Buffer.from(
                  JSON.stringify({
                    ok: true,
                    rows: [
                      {
                        employee: '0010329667',
                        employeeName: 'Alice Example',
                        department: 'R&D / Platform',
                        avatarUrl: 'https://profile.example.com/alice.png',
                      },
                    ],
                  }),
                ),
              };
            },
          },
        });
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
    assert.equal(st.displayName, 'Alice Example');
    assert.equal(st.avatarUrl, 'https://profile.example.com/alice.png');
    assert.equal(st.authMethod, 'sso');
    assert.equal(st.token, undefined);
    assert.equal(JSON.stringify(st).includes('sso-cookie-token'), false);

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
