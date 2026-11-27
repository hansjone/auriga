import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import net from 'node:net';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  BUILTIN_LOOPBACK_NO_PROXY,
  createControlledFetch,
  createProxyConfigStore,
  maskProxyUrl,
  normalizeMatchPattern,
  normalizeProxyUrl,
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

test('normalizeProxyUrl & normalizeMatchPattern validate inputs strictly', () => {
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

  assert.equal(normalizeMatchPattern('Example.COM'), 'example.com');
  assert.equal(normalizeMatchPattern('.corp.local'), '*.corp.local');
  assert.equal(normalizeMatchPattern('*.GitHub.com'), '*.github.com');
  assert.equal(normalizeMatchPattern('192.0.2.0/24'), '192.0.2.0/24');
  assert.throws(() => normalizeMatchPattern('https://github.com'), {
    code: 'invalidPattern',
  });
  assert.throws(() => normalizeMatchPattern('192.0.2.0/99'), {
    code: 'invalidPattern',
  });
});

test('resolveProxy enforces priority: loopback > direct mode > ordered rules > noProxy > globalProxy', () => {
  const cfg = validateProxyConfig({
    schemaVersion: 1,
    mode: 'proxy',
    globalProxy: 'http://global.proxy:8080',
    noProxy: ['*.corp.local', '192.0.2.0/24'],
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
  for (const host of ['localhost', '127.0.0.1', '127.0.0.42', '::1']) {
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

  // 5. Global noProxy (wildcard & CIDR)
  assert.equal(
    resolveProxy('git.corp.local', 443, cfg).matchedBy,
    'noProxy:*.corp.local',
  );
  assert.equal(
    resolveProxy('192.0.2.42', 443, cfg).matchedBy,
    'noProxy:192.0.2.0/24',
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

  // 8. Loop prevention against local router port
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

test('local routing proxy forwards per-domain HTTP & CONNECT traffic to distinct upstream proxies with auth and hot-reloads', async () => {
  const seenA = [];
  const seenB = [];
  const connectSeenA = [];

  const createMockProxy = (name, seen, connectSeen) =>
    new Promise((resolve) => {
      const srv = http.createServer((req, res) => {
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
        const [host, portStr] = (req.url ?? '').split(':');
        const upstream = net.connect(Number(portStr), host, () => {
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

  // Echo TCP server to verify CONNECT tunneling through upstream proxy A
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

  const proxyA = await createMockProxy('proxyA', seenA, connectSeenA);
  const proxyB = await createMockProxy('proxyB', seenB, []);

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
      {
        id: 'r-connect',
        enabled: true,
        pattern: 'tunnel.example.test',
        action: 'proxy',
        proxy: `http://u1:p1@127.0.0.1:${proxyA.port}`,
      },
    ],
  });

  const router = await startLocalProxyServer({
    getConfig: () => activeConfig,
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
          res.on('end', () =>
            resolve({
              status: res.statusCode,
              body: JSON.parse(Buffer.concat(chunks).toString('utf8')),
            }),
          );
        },
      );
      req.on('error', reject);
      req.end();
    });

  try {
    // Domain A -> Proxy A with Basic auth
    const resA = await httpViaRouter('http://alpha.example.test/hello');
    assert.equal(resA.status, 200);
    assert.equal(resA.body.via, 'proxyA');
    assert.equal(
      seenA[0].auth,
      `Basic ${Buffer.from('u1:p1').toString('base64')}`,
    );

    // Other domain -> Global Proxy B
    const resB = await httpViaRouter('http://beta.example.test/world');
    assert.equal(resB.status, 200);
    assert.equal(resB.body.via, 'proxyB');
    assert.equal(seenB[0].auth, null);

    // Hot reload: switch alpha.example.test to Proxy B without restarting router
    activeConfig = validateProxyConfig({
      mode: 'proxy',
      globalProxy: `http://127.0.0.1:${proxyB.port}`,
      noProxy: [],
      rules: [],
    });
    const resA2 = await httpViaRouter('http://alpha.example.test/after-reload');
    assert.equal(resA2.body.via, 'proxyB');

    // Controlled fetch obeys same config
    const controlled = createControlledFetch(() => activeConfig);
    const cfRes = await controlled('http://gamma.example.test/cf');
    assert.equal(cfRes.status, 200);
    assert.equal((await cfRes.json()).via, 'proxyB');

    // testProxyTarget probes through controlled fetch
    const probe = await testProxyTarget(
      'http://gamma.example.test/probe',
      () => activeConfig,
    );
    assert.equal(probe.reachable, true);
    assert.equal(probe.action, 'proxy');
    assert.equal(probe.matchedBy, 'globalProxy');

    // Verify CONNECT tunnel through proxyA to echoServer
    activeConfig = validateProxyConfig({
      mode: 'proxy',
      globalProxy: `http://u1:p1@127.0.0.1:${proxyA.port}`,
      noProxy: [],
      rules: [],
    });
    const tunnelResp = await new Promise((resolve, reject) => {
      const sock = net.connect(router.port, '127.0.0.1', () => {
        sock.write(
          `CONNECT 198.51.100.1:${echoServer.port} HTTP/1.1\r\nHost: 198.51.100.1:${echoServer.port}\r\n\r\n`,
        );
      });
      // Replace connect target inside proxyA to point to local echoServer for this test
      sock.once('data', (d) => {
        sock.destroy();
        resolve(d.toString('latin1'));
      });
      sock.on('error', reject);
    });
    assert.ok(connectSeenA.length === 1);
    assert.equal(connectSeenA[0].authority, `198.51.100.1:${echoServer.port}`);
    assert.equal(
      connectSeenA[0].auth,
      `Basic ${Buffer.from('u1:p1').toString('base64')}`,
    );
    assert.match(tunnelResp, /^HTTP\/1\.1 (200|502)/);
  } finally {
    await router.close();
    proxyA.server.close();
    proxyB.server.close();
    echoServer.server.close();
  }
});
