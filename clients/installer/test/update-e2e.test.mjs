import { wrapSetupFixture } from './helpers.mjs';
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
import { spawnSync } from 'node:child_process';
import { signRelease, reviewRelease } from '../scripts/sign-release.mjs';
import { systemTool } from '../runtime/system-tools.mjs';
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
  assert.equal(
    spawnSync(systemTool('tar'), ['-a', '-cf', zip, '-C', stage, '.']).status,
    0,
  );
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
