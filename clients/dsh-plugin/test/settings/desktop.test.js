import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readFile, writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { desktopUpdater, idleFetch } from '../../src/local/desktop.js';
import { setTimeout as delay } from 'node:timers/promises';
import { TextDecoder } from 'node:util';
import { createSettingsStore } from '../../src/settings/store.js';
import { createSettingsHandler } from '../../src/settings/host.js';

test('下载空闲超时随数据续期，慢速持续传输不受总时长限制', async (t) => {
  const server = createServer(async (req, res) => {
    res.writeHead(200);
    res.write('a');
    if (req.url === '/stalled') return;
    for (let i = 0; i < 5; i++) {
      await delay(40);
      if (res.destroyed) return;
      res.write('b');
    }
    res.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => {
    server.closeAllConnections();
    return new Promise((resolve) => server.close(resolve));
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const controller = new AbortController();
  const good = await idleFetch(origin + '/slow', {}, controller.signal, 150);
  let value = '';
  for await (const bytes of good.body) value += new TextDecoder().decode(bytes);
  assert.equal(value, 'abbbbb');
  const bad = await idleFetch(origin + '/stalled', {}, controller.signal, 150);
  await assert.rejects(async () => {
    for await (const bytes of bad.body) void bytes;
  });
});

test('Desktop 仅绑定首次 HTTPS 平台，普通 DSH 不挂更新器', async () => {
  const savedRoot = process.env.AURIGA_DESKTOP_ROOT,
    savedRuntime = process.env.AURIGA_DESKTOP_RUNTIME;
  const root = await mkdtemp(join(tmpdir(), 'auriga-desktop-adapter-'));
  try {
    delete process.env.AURIGA_DESKTOP_ROOT;
    assert.equal(
      await desktopUpdater({
        origin: 'https://example.com',
      }),
      undefined,
    );
    const runtime = join(
      root,
      'versions',
      'v1',
      'auriga',
      'runtime',
      'update.mjs',
    );
    await mkdir(join(root, 'versions', 'v1', 'auriga', 'runtime'), {
      recursive: true,
    });
    await writeFile(
      join(root, 'install.json'),
      JSON.stringify({ current: 'v1' }),
    );
    await writeFile(
      runtime,
      `export { platformUrl } from ${JSON.stringify(new URL('../../../installer/runtime/update.mjs', import.meta.url).href)}; export function createUpdater(){return {status:()=>({enabled:true}), start:()=>()=>{}}}`,
    );
    process.env.AURIGA_DESKTOP_ROOT = root;
    process.env.AURIGA_DESKTOP_RUNTIME = runtime;
    assert.equal(
      await desktopUpdater({
        origin: 'http://example.com',
      }),
      undefined,
    );
    const updater = await desktopUpdater({
      origin: 'https://example.com',
    });
    assert.equal(updater.status().enabled, true);
    updater.close();
    assert.equal(
      JSON.parse(await readFile(join(root, 'desktop-origin.json'), 'utf8'))
        .origin,
      'https://example.com',
    );
    assert.equal(
      await desktopUpdater({
        origin: 'https://other.example.com',
      }),
      undefined,
    );
  } finally {
    if (savedRoot === undefined) delete process.env.AURIGA_DESKTOP_ROOT;
    else process.env.AURIGA_DESKTOP_ROOT = savedRoot;
    if (savedRuntime === undefined) delete process.env.AURIGA_DESKTOP_RUNTIME;
    else process.env.AURIGA_DESKTOP_RUNTIME = savedRuntime;
    await rm(root, { recursive: true, force: true });
  }
});

test('设置更新操作保留主人认证和跨源门禁', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-desktop-settings-'));
  let server,
    calls = 0;
  try {
    const store = await createSettingsStore(root);
    const handler = createSettingsHandler({
      store,
      activeOrigin: 'https://example.com',
      auth: {
        requestRejection: (req) =>
          req.headers['x-owner'] === 'yes' ? undefined : 401,
      },
      desktop: () => ({
        status: () => ({ enabled: true }),
        update: async () => {
          calls++;
          return { queued: true };
        },
      }),
    });
    server = createServer((req, res) => void handler(req, res));
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const url = `http://127.0.0.1:${server.address().port}/auriga/settings/desktop`;
    const send = (extra = {}) =>
      fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Auriga-Settings': '1',
          ...extra,
        },
        body: JSON.stringify({ action: 'update' }),
      });
    assert.equal((await send()).status, 401);
    assert.equal(calls, 0);
    assert.equal(
      (await send({ 'x-owner': 'yes', origin: 'https://evil.example.com' }))
        .status,
      403,
    );
    assert.equal(calls, 0);
    assert.equal((await send({ 'x-owner': 'yes' })).status, 200);
    assert.equal(calls, 1);
  } finally {
    if (server) await new Promise((resolve) => server.close(resolve));
    await rm(root, { recursive: true, force: true });
  }
});
