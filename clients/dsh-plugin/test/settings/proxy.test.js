import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createSettingsStore } from '../../src/settings/store.js';
import { createSettingsHandler } from '../../src/settings/host.js';
import { createProxyRuntime } from '../../../installer/runtime/network-proxy.mjs';

async function withSettingsServer({ mode = 'connect', proxy }) {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-settings-proxy-'));
  const store = await createSettingsStore(join(dir, 'settings'));
  const handler = createSettingsHandler({
    store,
    mode,
    proxy: proxy ? () => proxy : undefined,
    activeOrigin: () => '',
    auth: { requestRejection: () => undefined },
  });
  const server = http.createServer(handler);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = /** @type {import('node:net').AddressInfo} */ (
    server.address()
  );
  const baseUrl = `http://127.0.0.1:${port}`;
  return {
    baseUrl,
    dir,
    close: async () => {
      await new Promise((resolve) => server.close(resolve));
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

test('settings proxy endpoints save, redact, reset, and reject invalid inputs', async () => {
  const dataDir = mkdtempSync(join(tmpdir(), 'auriga-proxy-ctrl-'));
  const defaultsFile = join(dataDir, 'defaults.json');
  writeFileSync(
    defaultsFile,
    JSON.stringify({
      networkProxy: {
        schemaVersion: 1,
        mode: 'proxy',
        globalProxy: 'http://bundled.corp.local:8080',
        noProxy: ['*.corp.local'],
        rules: [],
      },
    }),
  );
  const proxyCtrl = createProxyRuntime({
    dataHome: dataDir,
    defaultsFile,
  });
  const srv = await withSettingsServer({ proxy: proxyCtrl });
  const headers = {
    'content-type': 'application/json',
    'x-auriga-settings': '1',
    origin: srv.baseUrl,
  };

  try {
    // 1. State exposes bundled default proxy config
    const stRes = await fetch(`${srv.baseUrl}/auriga/settings/state`);
    assert.equal(stRes.status, 200);
    const st = await stRes.json();
    assert.equal(st.proxy.source, 'bundled');
    assert.equal(st.proxy.globalProxy, 'http://bundled.corp.local:8080');

    // 2. Missing X-Auriga-Settings header rejected
    const forbidden = await fetch(`${srv.baseUrl}/auriga/settings/proxy`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', origin: srv.baseUrl },
      body: JSON.stringify({ mode: 'direct' }),
    });
    assert.equal(forbidden.status, 403);

    // 3. Invalid proxy protocol rejected with 400 + fixed code
    const bad = await fetch(`${srv.baseUrl}/auriga/settings/proxy`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        schemaVersion: 1,
        mode: 'proxy',
        globalProxy: 'socks5://127.0.0.1:1080',
        noProxy: [],
        rules: [],
      }),
    });
    assert.equal(bad.status, 400);
    assert.equal((await bad.json()).reason, 'invalidProxyUrl');

    // 4. Save valid user override with per-domain rule and masked credentials
    const savedRes = await fetch(`${srv.baseUrl}/auriga/settings/proxy`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        schemaVersion: 1,
        mode: 'proxy',
        globalProxy: 'http://u:secret@proxy.local:8080',
        noProxy: ['192.0.2.0/24'],
        rules: [
          {
            id: 'r1',
            enabled: true,
            pattern: '*.github.com',
            action: 'proxy',
            proxy: 'http://ext.local:3128',
          },
        ],
      }),
    });
    assert.equal(savedRes.status, 200);
    const saved = await savedRes.json();
    assert.equal(saved.proxy.source, 'user');
    assert.equal(saved.proxy.globalProxy, 'http://u:***@proxy.local:8080');
    assert.equal(saved.proxy.rules.length, 1);

    // 5. Reset restores bundled default
    const resetRes = await fetch(`${srv.baseUrl}/auriga/settings/proxy/reset`, {
      method: 'POST',
      headers,
      body: '{}',
    });
    assert.equal(resetRes.status, 200);
    const resetState = await resetRes.json();
    assert.equal(resetState.proxy.source, 'bundled');
    assert.equal(
      resetState.proxy.globalProxy,
      'http://bundled.corp.local:8080',
    );
  } finally {
    await srv.close();
    rmSync(dataDir, { recursive: true, force: true });
  }
});

test('managed mode hides proxy state and blocks proxy mutations', async () => {
  const dataDir = mkdtempSync(join(tmpdir(), 'auriga-proxy-managed-'));
  const proxyCtrl = createProxyRuntime({ dataHome: dataDir });
  const srv = await withSettingsServer({ mode: 'managed', proxy: proxyCtrl });
  try {
    const stRes = await fetch(`${srv.baseUrl}/auriga/settings/state`);
    assert.equal(stRes.status, 200);
    const st = await stRes.json();
    assert.equal(st.proxy, undefined);

    const postRes = await fetch(`${srv.baseUrl}/auriga/settings/proxy`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-auriga-settings': '1',
        origin: srv.baseUrl,
      },
      body: JSON.stringify({ mode: 'direct' }),
    });
    assert.equal(postRes.status, 403);
    assert.equal((await postRes.json()).reason, 'managed');
  } finally {
    await srv.close();
    rmSync(dataDir, { recursive: true, force: true });
  }
});
