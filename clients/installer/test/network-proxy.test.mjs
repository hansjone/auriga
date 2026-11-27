import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import http from 'node:http';
import net from 'node:net';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  BUILTIN_LOOPBACK_NO_PROXY,
  createControlledFetch,
  createProxyConfigStore,
  isLoopbackHost,
  maskProxyUrl,
  normalizeMatchPattern,
  normalizeProxyUrl,
  readBoundedJsonFile,
  resolveProxy,
  startLocalProxyServer,
  stripSystemProxyEnv,
  testProxyTarget,
  validateProxyConfig,
} from '../runtime/network-proxy.mjs';

function makeTempDir() {
  return mkdtempSync(join(tmpdir(), 'auriga-proxy-test-'));
}

test('stripSystemProxyEnv removes all inherited proxy variables regardless of case', () => {
  const cleaned = stripSystemProxyEnv({
    PATH: '/usr/bin',
    HTTP_PROXY: 'http://corp:8080',
    https_proxy: 'http://corp:8080',
    ALL_PROXY: 'socks5://corp:1080',
    NO_PROXY: 'localhost',
    no_proxy: '127.0.0.1',
    npm_config_proxy: 'http://corp:8080',
    npm_config_https_proxy: 'http://corp:8080',
    NODE_USE_ENV_PROXY: '1',
    KEEP_ME: 'ok',
  });
  assert.equal(cleaned.KEEP_ME, 'ok');
  assert.equal(cleaned.PATH, '/usr/bin');
  for (const key of [
    'HTTP_PROXY',
    'https_proxy',
    'ALL_PROXY',
    'NO_PROXY',
    'no_proxy',
    'npm_config_proxy',
    'npm_config_https_proxy',
    'NODE_USE_ENV_PROXY',
  ]) {
    assert.equal(cleaned[key], undefined);
  }
});

test('normalizeProxyUrl & normalizeMatchPattern validate HTTP/HTTPS, IPv6, credentials and percent-encoding', () => {
  assert.equal(
    normalizeProxyUrl('http://proxy.corp.local:8080'),
    'http://proxy.corp.local:8080',
  );
  assert.equal(
    normalizeProxyUrl('https://user:secret@proxy.corp.local:8443'),
    'https://user:secret@proxy.corp.local:8443',
  );
  assert.equal(normalizeProxyUrl('', { optional: true }), '');
  assert.throws(() => normalizeProxyUrl('socks5://127.0.0.1:1080'), {
    code: 'invalidProxyUrl',
  });
  assert.throws(() => normalizeProxyUrl('http://proxy:8080/path'), {
    code: 'invalidProxyUrl',
  });
  // Invalid percent encoding rejected at save time
  assert.throws(() => normalizeProxyUrl('http://user:%ZZ@proxy:8080'), {
    code: 'invalidProxyUrl',
  });
  // Bundled defaults disallow credentials
  assert.throws(
    () =>
      normalizeProxyUrl('http://user:pass@proxy:8080', {
        allowCredentials: false,
      }),
    { code: 'invalidProxyUrl' },
  );

  assert.equal(normalizeMatchPattern('Example.COM'), 'example.com');
  assert.equal(normalizeMatchPattern('.corp.local'), '*.corp.local');
  assert.equal(normalizeMatchPattern('*.GitHub.com'), '*.github.com');
  assert.equal(normalizeMatchPattern('192.0.2.0/24'), '192.0.2.0/24');
  // IPv6 literal & CIDR support
  assert.equal(normalizeMatchPattern('2001:db8::1'), '2001:db8::1');
  assert.equal(normalizeMatchPattern('[2001:db8::1]'), '2001:db8::1');
  assert.equal(normalizeMatchPattern('2001:db8::/32'), '2001:db8::/32');
  assert.throws(() => normalizeMatchPattern('https://github.com'), {
    code: 'invalidPattern',
  });
  assert.throws(() => normalizeMatchPattern('192.0.2.0/99'), {
    code: 'invalidPattern',
  });
});

test('isLoopbackHost & resolveProxy enforce priority, IPv6 rules, and self-port loop prevention', () => {
  for (const host of [
    'localhost',
    'localhost.',
    'sub.localhost.',
    '127.0.0.1',
    '127.0.0.42',
    '::1',
    '[::1]',
    '::ffff:127.0.0.1',
    '::ffff:7f00:1',
  ]) {
    assert.equal(isLoopbackHost(host), true, `expected loopback: ${host}`);
  }

  const cfg = validateProxyConfig({
    schemaVersion: 1,
    mode: 'proxy',
    globalProxy: 'http://global.proxy:8080',
    noProxy: ['*.corp.local', '192.0.2.0/24', '2001:db8::/32'],
    rules: [
      {
        id: 'r-disabled',
        enabled: false,
        pattern: 'api.github.com',
        action: 'proxy',
        proxy: 'http://unused.proxy:8080',
      },
      {
        id: 'r-special-corp',
        enabled: true,
        pattern: 'special.corp.local',
        action: 'proxy',
        proxy: 'http://special.proxy:8080',
      },
      {
        id: 'r-github',
        enabled: true,
        pattern: '*.github.com',
        action: 'proxy',
        proxy: 'http://ext.proxy:8080',
      },
      {
        id: 'r-direct',
        enabled: true,
        pattern: 'direct.example.com',
        action: 'direct',
        proxy: '',
      },
    ],
  });

  // 1. Loopback always direct
  for (const host of [
    'localhost',
    'localhost.',
    '127.0.0.1',
    '::1',
    '::ffff:7f00:1',
  ]) {
    const d = resolveProxy(host, 8080, cfg);
    assert.equal(d.action, 'direct');
    assert.equal(d.matchedBy, 'builtin_loopback');
  }

  // 2. Per-domain rule beats noProxy
  assert.deepEqual(resolveProxy('special.corp.local', 443, cfg), {
    action: 'proxy',
    proxy: 'http://special.proxy:8080',
    matchedBy: 'rule:special.corp.local',
    ruleId: 'r-special-corp',
  });

  // 3. Disabled rule skipped, wildcard rule matched
  assert.deepEqual(resolveProxy('api.github.com', 443, cfg), {
    action: 'proxy',
    proxy: 'http://ext.proxy:8080',
    matchedBy: 'rule:*.github.com',
    ruleId: 'r-github',
  });

  // 4. Explicit direct rule
  assert.equal(resolveProxy('direct.example.com', 443, cfg).action, 'direct');

  // 5. Global noProxy (wildcard, IPv4 CIDR & IPv6 CIDR)
  assert.equal(
    resolveProxy('git.corp.local', 443, cfg).matchedBy,
    'noProxy:*.corp.local',
  );
  assert.equal(
    resolveProxy('192.0.2.42', 443, cfg).matchedBy,
    'noProxy:192.0.2.0/24',
  );
  assert.equal(
    resolveProxy('[2001:db8::99]', 443, cfg).matchedBy,
    'noProxy:2001:db8::/32',
  );

  // 6. Global proxy fallback
  assert.deepEqual(resolveProxy('pypi.org', 443, cfg), {
    action: 'proxy',
    proxy: 'http://global.proxy:8080',
    matchedBy: 'globalProxy',
  });

  // 7. Direct mode ignores everything
  const directCfg = { ...cfg, mode: 'direct' };
  assert.equal(resolveProxy('pypi.org', 443, directCfg).action, 'direct');

  // 8. Loop prevention against local router port (both target and upstream proxy)
  assert.throws(
    () => resolveProxy('127.0.0.1', 43210, directCfg, { selfPort: 43210 }),
    { code: 'proxy_loop' },
  );
  assert.throws(
    () =>
      resolveProxy(
        'pypi.org',
        443,
        validateProxyConfig({
          mode: 'proxy',
          globalProxy: 'http://127.0.0.1:43210',
          noProxy: [],
          rules: [],
        }),
        { selfPort: 43210 },
      ),
    { code: 'proxy_loop' },
  );
});

test('layered store resolves user override > bundled defaults.json > builtin direct, preserves passwords and resets cleanly', () => {
  const dir = makeTempDir();
  try {
    const dataHome = join(dir, 'data');
    const defaultsFile = join(dir, 'defaults.json');
    writeFileSync(
      defaultsFile,
      JSON.stringify({
        platformOrigin: 'https://platform.corp.local',
        networkProxy: {
          schemaVersion: 1,
          mode: 'proxy',
          globalProxy: 'http://site-proxy.corp.local:8080',
          noProxy: ['*.corp.local'],
          rules: [],
        },
      }),
    );

    const parsedBounded = readBoundedJsonFile(defaultsFile);
    assert.equal(parsedBounded.networkProxy.mode, 'proxy');
    assert.throws(() => readBoundedJsonFile(defaultsFile, 16), {
      code: 'invalidProxyFile',
    });

    const store = createProxyConfigStore({ dataHome, defaultsFile });
    const initial = store.snapshot();
    assert.equal(initial.source, 'bundled');
    assert.equal(initial.hasBundledDefault, true);
    assert.equal(initial.mode, 'proxy');
    assert.equal(initial.globalProxy, 'http://site-proxy.corp.local:8080');
    assert.deepEqual(initial.builtinNoProxy, [...BUILTIN_LOOPBACK_NO_PROXY]);

    // Save user override with password -> redacted in snapshot
    const saved = store.save({
      schemaVersion: 1,
      mode: 'proxy',
      globalProxy: 'http://alice:s3cr3t@user-proxy.local:3128',
      noProxy: ['192.0.2.0/24'],
      rules: [
        {
          id: 'r1',
          enabled: true,
          pattern: '*.github.com',
          action: 'proxy',
          proxy: 'http://bob:p4ss@ext.local:8080',
        },
      ],
    });
    assert.equal(saved.source, 'user');
    assert.equal(saved.globalProxy, 'http://alice:***@user-proxy.local:3128');
    assert.equal(saved.globalProxyHasPassword, true);
    assert.equal(saved.rules[0].proxy, 'http://bob:***@ext.local:8080');
    assert.equal(
      maskProxyUrl(store.get().config.globalProxy).hasPassword,
      true,
    );

    // Re-saving with masked placeholder preserves original password
    store.save({
      schemaVersion: 1,
      mode: 'proxy',
      globalProxy: 'http://alice:***@user-proxy.local:3128',
      noProxy: ['192.0.2.0/24', '198.51.100.0/24'],
      rules: [
        {
          id: 'r1',
          enabled: true,
          pattern: '*.github.com',
          action: 'proxy',
          proxy: 'http://bob:***@ext.local:8080',
        },
      ],
    });
    assert.equal(
      store.get().config.globalProxy,
      'http://alice:s3cr3t@user-proxy.local:3128',
    );
    assert.equal(
      store.get().config.rules[0].proxy,
      'http://bob:p4ss@ext.local:8080',
    );

    // Corrupt user file safely degrades to bundled default
    mkdirSync(join(dataHome, 'auriga'), { recursive: true });
    writeFileSync(store.userFile, '{not valid json');
    const degraded = store.snapshot();
    assert.equal(degraded.degraded, true);
    assert.equal(degraded.source, 'bundled');
    assert.equal(degraded.globalProxy, 'http://site-proxy.corp.local:8080');

    // Reset restores bundled default and clears degraded flag
    const afterReset = store.reset();
    assert.equal(afterReset.degraded, false);
    assert.equal(afterReset.source, 'bundled');
    assert.equal(afterReset.globalProxy, 'http://site-proxy.corp.local:8080');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('local routing proxy forwards HTTP, real bidirectional CONNECT, rejects self-loop, and controlledFetch supports FormData, body abort & 407 probe', async () => {
  const seenA = [];
  const seenB = [];
  const connectSeenA = [];

  // Echo TCP server to verify full bidirectional CONNECT tunneling
  const echoServer = await new Promise((resolve) => {
    const s = net.createServer((sock) => {
      sock.once('data', (d) => {
        sock.end(`ECHO:${d.toString('utf8')}`);
      });
    });
    s.listen(0, '127.0.0.1', () =>
      resolve({ server: s, port: s.address().port }),
    );
  });

  // Direct HTTP server for FormData multipart & body abort tests
  const directServer = await new Promise((resolve) => {
    const s = http.createServer((req, res) => {
      if (req.url === '/upload') {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
          res.writeHead(200, { 'content-type': 'application/json' });
          res.end(
            JSON.stringify({
              contentType: req.headers['content-type'] ?? '',
              body: Buffer.concat(chunks).toString('utf8'),
            }),
          );
        });
        return;
      }
      if (req.url === '/stall') {
        res.writeHead(200, { 'content-type': 'application/octet-stream' });
        res.write('first-chunk');
        // Intentionally never end to test mid-body abort
        return;
      }
      res.writeHead(200);
      res.end('ok');
    });
    s.listen(0, '127.0.0.1', () =>
      resolve({ server: s, port: s.address().port }),
    );
  });

  const createMockProxy = (
    name,
    seen,
    connectSeen,
    { requireAuth = false } = {},
  ) =>
    new Promise((resolve) => {
      const srv = http.createServer((req, res) => {
        if (requireAuth && !req.headers['proxy-authorization']) {
          res.writeHead(407, { 'proxy-authenticate': 'Basic realm="test"' });
          res.end('Proxy Auth Required');
          return;
        }
        seen.push({
          url: req.url,
          auth: req.headers['proxy-authorization'] ?? null,
        });
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ via: name, url: req.url }));
      });
      srv.on('connect', (req, clientSocket, head) => {
        connectSeen.push({
          authority: req.url,
          auth: req.headers['proxy-authorization'] ?? null,
        });
        // Route synthetic tunnel authority to our local echo server
        const upstream = net.connect(echoServer.port, '127.0.0.1', () => {
          clientSocket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
          if (head?.length) upstream.write(head);
          upstream.pipe(clientSocket);
          clientSocket.pipe(upstream);
        });
        upstream.on('error', () =>
          clientSocket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n'),
        );
      });
      srv.listen(0, '127.0.0.1', () => {
        const addr = srv.address();
        resolve({ server: srv, port: addr.port });
      });
    });

  const proxyA = await createMockProxy('proxyA', seenA, connectSeenA);
  const proxyB = await createMockProxy('proxyB', seenB, []);
  const authProxy = await createMockProxy('authProxy', [], [], {
    requireAuth: true,
  });

  let activeConfig = validateProxyConfig({
    mode: 'proxy',
    globalProxy: `http://127.0.0.1:${proxyB.port}`,
    noProxy: [],
    rules: [
      {
        id: 'r-a',
        enabled: true,
        pattern: 'alpha.example.test',
        action: 'proxy',
        proxy: `http://u1:p1@127.0.0.1:${proxyA.port}`,
      },
    ],
  });

  const router = await startLocalProxyServer({
    getConfig: () => activeConfig,
    lookup: (hostname, opts, cb) => {
      const callback = typeof opts === 'function' ? opts : cb;
      if (hostname === 'loop-alias.example.test') {
        if (typeof opts === 'object' && opts?.all) {
          callback(null, [{ address: '127.0.0.1', family: 4 }]);
        } else {
          callback(null, '127.0.0.1', 4);
        }
        return;
      }
      if (typeof opts === 'object' && opts?.all) {
        callback(null, [{ address: '127.0.0.1', family: 4 }]);
      } else {
        callback(null, '127.0.0.1', 4);
      }
    },
  });

  const httpViaRouter = (targetUrl) =>
    new Promise((resolve, reject) => {
      const req = http.request(
        {
          hostname: '127.0.0.1',
          port: router.port,
          method: 'GET',
          path: targetUrl,
          headers: { host: new URL(targetUrl).host },
        },
        (res) => {
          const chunks = [];
          res.on('data', (c) => chunks.push(c));
          res.on('end', () => {
            const text = Buffer.concat(chunks).toString('utf8');
            resolve({ status: res.statusCode, text });
          });
        },
      );
      req.on('error', reject);
      req.end();
    });

  try {
    // Env exposes NODE_USE_ENV_PROXY=1 for Node 24+ children
    assert.equal(router.env().NODE_USE_ENV_PROXY, '1');

    // Domain A -> Proxy A with Basic auth
    const resA = await httpViaRouter('http://alpha.example.test/hello');
    assert.equal(resA.status, 200);
    assert.equal(JSON.parse(resA.text).via, 'proxyA');
    assert.equal(
      seenA[0].auth,
      `Basic ${Buffer.from('u1:p1').toString('base64')}`,
    );

    // Self-loop HTTP target rejected with 502 (both literal loopback and DNS alias)
    const loopRes = await httpViaRouter(`http://127.0.0.1:${router.port}/self`);
    assert.equal(loopRes.status, 502);
    const savedCfg = activeConfig;
    activeConfig = validateProxyConfig({
      mode: 'direct',
      globalProxy: '',
      noProxy: [],
      rules: [],
    });
    const aliasLoopRes = await httpViaRouter(
      `http://loop-alias.example.test:${router.port}/self`,
    );
    assert.equal(aliasLoopRes.status, 502);
    const aliasConnectRes = await new Promise((resolve, reject) => {
      const sock = net.connect(router.port, '127.0.0.1', () => {
        sock.write(
          `CONNECT loop-alias.example.test:${router.port} HTTP/1.1\r\nHost: loop-alias.example.test:${router.port}\r\n\r\n`,
        );
      });
      sock.once('data', (d) => {
        sock.destroy();
        resolve(d.toString('latin1'));
      });
      sock.on('error', reject);
    });
    assert.match(aliasConnectRes, /^HTTP\/1\.1 502 /);
    activeConfig = savedCfg;

    // Other domain -> Global Proxy B
    const resB = await httpViaRouter('http://beta.example.test/world');
    assert.equal(resB.status, 200);
    assert.equal(JSON.parse(resB.text).via, 'proxyB');

    // Hot reload: switch alpha.example.test to Proxy B without restarting router
    activeConfig = validateProxyConfig({
      mode: 'proxy',
      globalProxy: `http://127.0.0.1:${proxyB.port}`,
      noProxy: [],
      rules: [],
    });
    const resA2 = await httpViaRouter('http://alpha.example.test/after-reload');
    assert.equal(JSON.parse(resA2.text).via, 'proxyB');

    // Controlled fetch preserves FormData multipart encoding
    const controlled = createControlledFetch(() => activeConfig);
    const form = new globalThis.FormData();
    form.append('meta', JSON.stringify({ name: 'pkg-1' }));
    form.append(
      'file',
      new globalThis.Blob(['hello-bytes'], { type: 'text/plain' }),
      'a.txt',
    );
    const upRes = await controlled(
      `http://127.0.0.1:${directServer.port}/upload`,
      {
        method: 'POST',
        body: form,
      },
    );
    assert.equal(upRes.status, 200);
    const upJson = await upRes.json();
    assert.match(upJson.contentType, /^multipart\/form-data; boundary=/);
    assert.match(upJson.body, /pkg-1/);
    assert.match(upJson.body, /hello-bytes/);

    // Controlled fetch honors AbortSignal AFTER response headers (during body read)
    const ac = new globalThis.AbortController();
    const stallRes = await controlled(
      `http://127.0.0.1:${directServer.port}/stall`,
      { signal: ac.signal },
    );
    const reader = stallRes.body.getReader();
    const first = await reader.read();
    assert.equal(Buffer.from(first.value).toString('utf8'), 'first-chunk');
    setTimeout(() => ac.abort(new Error('idle-timeout')), 30);
    await assert.rejects(() => reader.read(), /idle-timeout|aborted/i);

    // testProxyTarget classifies HTTP 407 as proxy_auth_required (unreachable)
    const authCfg = validateProxyConfig({
      mode: 'proxy',
      globalProxy: `http://127.0.0.1:${authProxy.port}`,
      noProxy: [],
      rules: [],
    });
    const probe407 = await testProxyTarget(
      'http://gamma.example.test/probe',
      () => authCfg,
    );
    assert.equal(probe407.reachable, false);
    assert.equal(probe407.reason, 'proxy_auth_required');

    // Real bidirectional CONNECT tunnel through proxyA to echoServer
    activeConfig = validateProxyConfig({
      mode: 'proxy',
      globalProxy: `http://u1:p1@127.0.0.1:${proxyA.port}`,
      noProxy: [],
      rules: [],
    });
    const tunnelOutput = await new Promise((resolve, reject) => {
      const sock = net.connect(router.port, '127.0.0.1', () => {
        sock.write(
          `CONNECT tunnel.example.test:443 HTTP/1.1\r\nHost: tunnel.example.test:443\r\n\r\n`,
        );
      });
      let buf = Buffer.alloc(0);
      let established = false;
      sock.on('data', (chunk) => {
        buf = Buffer.concat([buf, chunk]);
        if (!established) {
          const sep = buf.indexOf('\r\n\r\n');
          if (sep === -1) return;
          const statusLine = buf.subarray(0, sep).toString('latin1');
          assert.match(statusLine, /^HTTP\/1\.1 200 /);
          established = true;
          buf = buf.subarray(sep + 4);
          sock.write('ping-over-tunnel');
        }
      });
      sock.on('end', () => resolve(buf.toString('utf8')));
      sock.on('error', reject);
    });
    assert.equal(connectSeenA.length, 1);
    assert.equal(connectSeenA[0].authority, 'tunnel.example.test:443');
    assert.equal(
      connectSeenA[0].auth,
      `Basic ${Buffer.from('u1:p1').toString('base64')}`,
    );
    assert.equal(tunnelOutput, 'ECHO:ping-over-tunnel');
  } finally {
    await router.close();
    proxyA.server.close();
    proxyB.server.close();
    authProxy.server.close();
    echoServer.server.close();
    directServer.server.close();
  }
});
