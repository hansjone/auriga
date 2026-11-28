import { wrapSetupFixture, writeStageZip } from './helpers.mjs';
/** Isolated publisher/transport substitute, real archive checks and install transactions. */
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { generateKeyPairSync } from 'node:crypto';
import {
  cpSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  existsSync,
} from 'node:fs';
import { join } from 'node:path';
import { signRelease, reviewRelease } from '../scripts/sign-release.mjs';
import {
  approvePending,
  createUpdater,
  applyPending,
} from '../runtime/update.mjs';
import {
  install,
  readState,
  profileDir,
  versionDir,
  rollback,
  launchEnv,
} from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';
import {
  desktopPlatform,
  hostDesktopTarget,
} from '../runtime/desktop-platform.mjs';

test('沙箱：发布、检测、点击、退出安装；坏包拒绝，旧数据保留', async (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const old = buildPayload(base, { name: 'old', pythonContent: 'old-python' }),
    next = buildPayload(base, {
      name: 'next',
      pluginVersion: '2.0.0',
      pythonContent: 'next-python',
    });
  const keys = generateKeyPairSync('ed25519'),
    keyFile = join(base, 'offline-key.pem');
  writeFileSync(
    keyFile,
    keys.privateKey.export({ type: 'pkcs8', format: 'pem' }),
    { mode: 0o600 },
  );
  const stage = join(base, 'stage');
  mkdirSync(stage);
  cpSync(next.dir, join(stage, 'payload'), { recursive: true });
  const zip = join(base, 'desktop.zip');
  writeStageZip(stage, zip);
  wrapSetupFixture(zip);
  const signingOptions = {
    payload: next.dir,
    archive: zip,
    keyFile,
    sequence: 2,
    publishedAt: new Date().toISOString(),
    minimumVersion: '1.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
    platform: hostDesktopTarget(),
  };
  let manifest = signRelease({
    ...signingOptions,
    confirmSha256: reviewRelease(signingOptions).summary.reviewSha256,
  });
  let corrupt = false,
    withdrawn = false;
  let interrupted = false,
    resumed = false;
  const server = createServer((req, res) => {
    // The isolated publisher closes each reply; avoid stale keep-alive reuse
    // while Windows copies/scans the new runtime between requests.
    res.setHeader('Connection', 'close');
    if (/\/latest(?:\?|$)/.test(req.url ?? '')) {
      if (withdrawn) {
        res.writeHead(404);
        res.end();
        return;
      }
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(manifest));
    } else {
      const bytes = readFileSync(zip);
      if (corrupt) bytes[0] ^= 1;
      if (req.headers.range) {
        const offset = Number(req.headers.range.match(/^bytes=(\d+)-$/)[1]);
        resumed = true;
        res.writeHead(206, {
          'Content-Range': `bytes ${offset}-${bytes.length - 1}/${bytes.length}`,
        });
        res.end(bytes.subarray(offset));
      } else if (interrupted) {
        interrupted = false;
        res.end(bytes.subarray(0, 100));
      } else res.end(bytes);
    }
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const port = server.address().port;
  // The production URL remains HTTPS. Only this test transport maps it to the
  // random-port publisher substitute; there is no production insecure option.
  const request = (url, init) =>
    fetch(`http://127.0.0.1:${port}${new URL(url).pathname}`, init);
  install({
    root,
    dataHome,
    payload: old.dir,
    platformOrigin: 'https://example.com',
    smoke: () => {},
  });
  writeFileSync(join(profileDir(dataHome), 'conversations.json'), 'keep');
  const updater = createUpdater({
    root,
    origin: 'https://example.com',
    request,
    publicKey: keys.publicKey,
    requireApproval: true,
  });
  await updater.check();
  assert.equal(updater.status().available.version, '2.0.0');
  corrupt = true;
  await assert.rejects(updater.update(), /SHA/);
  assert.equal(readState(root).current, old.id);
  assert.equal(existsSync(join(root, 'versions', next.id)), false);
  corrupt = false;
  interrupted = true;
  const queued = await updater.update();
  assert.equal(resumed, true);
  assert.equal(queued.queued, true);
  assert.equal(readState(root).current, old.id); // no live-session mutation
  const approval = join(root, 'desktop-pending', 'approval-required');
  assert.equal(
    await applyPending(root, { publicKey: keys.publicKey, request }),
    false,
  );
  assert.equal(existsSync(approval), true);
  approvePending(root);
  assert.equal(existsSync(approval), false);
  withdrawn = true;
  await assert.rejects(
    applyPending(root, { publicKey: keys.publicKey, request, smoke: () => {} }),
    /withdrawn/,
  );
  assert.equal(readState(root).current, old.id);
  withdrawn = false;
  await updater.check();
  await updater.update();
  approvePending(root);
  await assert.rejects(
    applyPending(root, {
      publicKey: keys.publicKey,
      request,
      smoke: () => {
        throw new Error('smoke failed');
      },
    }),
    /smoke/,
  );
  assert.equal(readState(root).current, old.id);
  assert.equal(
    readFileSync(join(profileDir(dataHome), 'conversations.json'), 'utf8'),
    'keep',
  );
  assert.equal(
    existsSync(
      join(root, 'desktop-pending', desktopPlatform().stagedPackageName),
    ),
    true,
  );
  assert.equal(updater.status().restartRequired, true);
  await applyPending(root, {
    publicKey: keys.publicKey,
    smoke: () => {},
    request,
  });
  assert.equal(readState(root).current, next.id);
  assert.equal(
    readFileSync(
      join(versionDir(root, next.id), 'runtime/python/python.exe'),
      'utf8',
    ),
    'next-python',
  );
  assert.equal(
    launchEnv(versionDir(root, next.id), dataHome).PYTHONHOME,
    undefined,
  );
  assert.equal(
    readFileSync(join(profileDir(dataHome), 'conversations.json'), 'utf8'),
    'keep',
  );
  manifest = { ...manifest, version: '1.0.0' };
  await assert.rejects(updater.check(), /signature/);
  rollback({ root, dataHome, smoke: () => {} });
  assert.equal(readState(root).current, old.id);
  assert.equal(
    readFileSync(
      join(versionDir(root, old.id), 'runtime/python/python.exe'),
      'utf8',
    ),
    'old-python',
  );
});

test('后台预备 versions/<id>、多次发版单槽位覆盖清理、重启瞬间新版放行与损坏回退', async (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const v1 = buildPayload(base, {
    name: 'v1',
    pythonContent: 'py-v1',
    isolated: true,
  });
  const v2 = buildPayload(base, {
    name: 'v2',
    pluginVersion: '2.0.0',
    pythonContent: 'py-v2',
    isolated: true,
  });
  const v3 = buildPayload(base, {
    name: 'v3',
    pluginVersion: '3.0.0',
    pythonContent: 'py-v3',
    isolated: true,
  });
  const keys = generateKeyPairSync('ed25519'),
    keyFile = join(base, 'offline-key.pem');
  writeFileSync(
    keyFile,
    keys.privateKey.export({ type: 'pkcs8', format: 'pem' }),
    { mode: 0o600 },
  );
  const pack = (payload, name, sequence) => {
    const stage = join(base, `stage-${name}`);
    mkdirSync(stage);
    cpSync(payload.dir, join(stage, 'payload'), { recursive: true });
    const zip = join(base, `desktop-${name}.zip`);
    writeStageZip(stage, zip);
    wrapSetupFixture(zip);
    const signingOptions = {
      payload: payload.dir,
      archive: zip,
      keyFile,
      sequence,
      publishedAt: new Date(Date.now() - 1000).toISOString(),
      minimumVersion: '1.0.0',
      notAfter: new Date(Date.now() + 86400000).toISOString(),
      platform: hostDesktopTarget(),
    };
    const manifest = signRelease({
      ...signingOptions,
      confirmSha256: reviewRelease(signingOptions).summary.reviewSha256,
    });
    return { zip, manifest };
  };
  const pkg2 = pack(v2, 'v2', 2);
  const pkg3 = pack(v3, 'v3', 3);
  let activePkg = pkg2;
  let failDownload = false;
  const server = createServer((req, res) => {
    res.setHeader('Connection', 'close');
    if (/\/latest(?:\?|$)/.test(req.url ?? '')) {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(activePkg.manifest));
      return;
    }
    if (failDownload) {
      res.writeHead(503);
      res.end();
      return;
    }
    res.end(readFileSync(activePkg.zip));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const port = server.address().port;
  const request = (url, init) =>
    fetch(`http://127.0.0.1:${port}${new URL(url).pathname}`, init);

  install({
    root,
    dataHome,
    payload: v1.dir,
    platformOrigin: 'https://example.com',
    smoke: () => {},
  });
  const updater = createUpdater({
    root,
    origin: 'https://example.com',
    request,
    publicKey: keys.publicKey,
    smoke: () => {},
  });
  assert.equal(updater.status().autoDownload, true);
  updater.setAutoDownload(false);
  assert.equal(updater.status().autoDownload, false);
  updater.setAutoDownload(true);
  assert.equal(updater.status().autoDownload, true);

  // 1. 后台下载并预备 v2：versions/<v2.id> 提前落盘
  await updater.check();
  assert.equal(updater.status().available.version, '2.0.0');
  const r2 = await updater.update({ background: true });
  assert.equal(r2.queued, true);
  assert.equal(r2.prepared, true);
  assert.equal(existsSync(versionDir(root, v2.id)), true);
  assert.equal(
    existsSync(join(root, 'desktop-pending', 'prepared.json')),
    true,
  );
  assert.equal(updater.status().pendingVersion, '2.0.0');
  assert.equal(readState(root).current, v1.id);

  // 2. 用户未重启期间服务端发布 v3：check() 照常发现 v3
  activePkg = pkg3;
  await updater.check();
  assert.equal(updater.status().available.version, '3.0.0');
  assert.equal(updater.status().restartRequired, true);
  assert.equal(updater.status().pendingVersion, '2.0.0');

  // 3. v3 下载失败时，旧的 v2 预备完好保留
  failDownload = true;
  await assert.rejects(updater.update({ background: true }));
  assert.equal(existsSync(versionDir(root, v2.id)), true);
  assert.equal(updater.status().pendingVersion, '2.0.0');

  // 4. v3 恢复下载并预备成功：原子覆盖 desktop-pending，旧的未激活 v2 目录被清理
  failDownload = false;
  const r3 = await updater.update({ background: true });
  assert.equal(r3.queued, true);
  assert.equal(r3.prepared, true);
  assert.equal(updater.status().pendingVersion, '3.0.0');
  assert.equal(existsSync(versionDir(root, v3.id)), true);
  assert.equal(existsSync(versionDir(root, v2.id)), false);

  // 5. 重启时快速路径直接切换，无需重新解压
  const events = [];
  assert.equal(
    await applyPending(root, {
      publicKey: keys.publicKey,
      request,
      smoke: () => {},
      onState: (e) => events.push(e),
    }),
    true,
  );
  assert.equal(readState(root).current, v3.id);
  assert.equal(
    events.some((e) => e.stage === 'extract'),
    false,
    'fast path should skip re-extracting prestaged version',
  );
  assert.equal(
    readFileSync(
      join(versionDir(root, v3.id), 'runtime/python/python.exe'),
      'utf8',
    ),
    'py-v3',
  );
});

test('预备阶段 smoke 失败清理 versions/<id>；预备目录损坏时快速路径自动回退压缩包恢复', async (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const v1 = buildPayload(base, {
    name: 'v1',
    pythonContent: 'py-v1',
    isolated: true,
  });
  const v2 = buildPayload(base, {
    name: 'v2',
    pluginVersion: '2.0.0',
    pythonContent: 'py-v2',
    isolated: true,
  });
  const keys = generateKeyPairSync('ed25519'),
    keyFile = join(base, 'offline-key.pem');
  writeFileSync(
    keyFile,
    keys.privateKey.export({ type: 'pkcs8', format: 'pem' }),
    { mode: 0o600 },
  );
  const stage = join(base, 'stage-v2');
  mkdirSync(stage);
  cpSync(v2.dir, join(stage, 'payload'), { recursive: true });
  const zip = join(base, 'desktop-v2.zip');
  writeStageZip(stage, zip);
  wrapSetupFixture(zip);
  const signingOptions = {
    payload: v2.dir,
    archive: zip,
    keyFile,
    sequence: 2,
    publishedAt: new Date(Date.now() - 1000).toISOString(),
    minimumVersion: '1.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
    platform: hostDesktopTarget(),
  };
  const manifest = signRelease({
    ...signingOptions,
    confirmSha256: reviewRelease(signingOptions).summary.reviewSha256,
  });
  const server = createServer((req, res) => {
    res.setHeader('Connection', 'close');
    if (/\/latest(?:\?|$)/.test(req.url ?? '')) {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(manifest));
      return;
    }
    res.end(readFileSync(zip));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const port = server.address().port;
  const request = (url, init) =>
    fetch(`http://127.0.0.1:${port}${new URL(url).pathname}`, init);

  install({
    root,
    dataHome,
    payload: v1.dir,
    platformOrigin: 'https://example.com',
    smoke: () => {},
  });

  // 1. 预备阶段 pre-smoke 失败，不能残留未激活的 versions/<v2.id>
  const badSmokeUpdater = createUpdater({
    root,
    origin: 'https://example.com',
    request,
    publicKey: keys.publicKey,
    smoke: () => {
      throw Object.assign(new Error('prestage smoke failed'), {
        code: 'desktop.smoke_failed',
      });
    },
  });
  await badSmokeUpdater.check();
  await assert.rejects(badSmokeUpdater.update({ background: true }), /smoke/);
  assert.equal(existsSync(versionDir(root, v2.id)), false);
  assert.equal(existsSync(join(root, 'desktop-pending')), false);

  // 2. 预备成功后模拟等待重启期间 versions/<v2.id> 被损坏，applyPending 应自动回退到压缩包恢复安装
  const goodUpdater = createUpdater({
    root,
    origin: 'https://example.com',
    request,
    publicKey: keys.publicKey,
    smoke: () => {},
  });
  await goodUpdater.check();
  const res = await goodUpdater.update({ background: true });
  assert.equal(res.prepared, true);
  assert.equal(existsSync(versionDir(root, v2.id)), true);
  writeFileSync(
    join(versionDir(root, v2.id), 'auriga/runtime/core.mjs'),
    'corrupted',
  );
  const events = [];
  assert.equal(
    await applyPending(root, {
      publicKey: keys.publicKey,
      request,
      smoke: () => {},
      onState: (e) => events.push(e),
    }),
    true,
  );
  assert.equal(readState(root).current, v2.id);
  assert.equal(
    events.some((e) => e.stage === 'extract'),
    true,
    'damaged prestaged version should fall back to signed archive extraction',
  );
});
