/* global fetch */
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
import { createUpdater, applyPending } from '../runtime/update.mjs';
import { install, readState, profileDir } from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

test('沙箱：发布、检测、点击、退出安装；坏包拒绝，旧数据保留', async (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const old = buildPayload(base, { name: 'old' }),
    next = buildPayload(base, { name: 'next', pluginVersion: '2.0.0' });
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
  const signingOptions = {
    payload: next.dir,
    archive: zip,
    keyFile,
    sequence: 2,
    publishedAt: new Date().toISOString(),
    minimumVersion: '1.0.0',
    notAfter: new Date(Date.now() + 86400000).toISOString(),
  };
  let manifest = signRelease({
    ...signingOptions,
    confirmSha256: reviewRelease(signingOptions).summary.reviewSha256,
  });
  let corrupt = false;
  let interrupted = false,
    resumed = false;
  const server = createServer((req, res) => {
    if (req.url.endsWith('/latest')) {
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
  await applyPending(root, { publicKey: keys.publicKey, smoke: () => {} });
  assert.equal(readState(root).current, next.id);
  assert.equal(
    readFileSync(join(profileDir(dataHome), 'conversations.json'), 'utf8'),
    'keep',
  );
  manifest = { ...manifest, version: '1.0.0' };
  await assert.rejects(updater.check(), /signature/);
});
