import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFileSync } from 'node:child_process';
import { EventEmitter } from 'node:events';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { PassThrough } from 'node:stream';
import { compileHelper, makeExe } from '../scripts/make-exe.mjs';
import { verifyPackage, verifyPackageAsync } from '../runtime/verify.mjs';
import {
  readSetupEntry,
  readSetupManifest,
} from '../runtime/setup-archive.mjs';
import { install } from '../runtime/core.mjs';
import { extractArchive } from '../runtime/update.mjs';
import { createControl } from '../runtime/tray-control.mjs';
import { superviseDsh } from '../runtime/desktop-supervision.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const windows = process.platform === 'win32';

test('verifyPackageAsync matches verifyPackage for valid and tampered payloads', async (t) => {
  const base = tempBase(t);
  const { dir: payload } = buildPayload(base, { pythonContent: 'py-bin' });
  const syncManifest = verifyPackage(payload);
  const asyncManifest = await verifyPackageAsync(payload);
  assert.deepEqual(asyncManifest, syncManifest);

  // Tamper a python runtime file and confirm error code parity.
  writeFileSync(join(payload, 'runtime/python/python.exe'), 'tampered');
  await assert.rejects(
    () => verifyPackageAsync(payload, { trees: false }),
    (err) => err?.code === 'desktop.python_runtime_modified',
  );
});

test(
  'readSetupManifest peeks manifest.json directly from AURZIP01 exe without full extract',
  { skip: !windows, timeout: 60000 },
  (t) => {
    const base = tempBase(t);
    const stageRoot = join(base, 'pkg-stage');
    mkdirSync(stageRoot, { recursive: true });
    const { dir: stagePayload } = buildPayload(stageRoot, {
      name: 'inner',
      pluginVersion: '2.3.4',
    });
    const properStage = join(base, 'proper-stage');
    mkdirSync(properStage, { recursive: true });
    const targetPayload = join(properStage, 'payload');
    execFileSync('cmd.exe', ['/c', 'move', stagePayload, targetPayload], {
      windowsHide: true,
    });
    assert.ok(existsSync(join(targetPayload, 'manifest.json')));

    const exe = join(base, 'setup.exe');
    makeExe({ stage: properStage, out: exe });
    const expected = JSON.parse(
      readFileSync(join(targetPayload, 'manifest.json'), 'utf8'),
    );
    const peeked = readSetupManifest(exe);
    assert.equal(peeked.id, expected.id);
    assert.equal(peeked.bundleVersion, '2.3.4');

    // Also verify extractArchive with hardlink reuse preserves identical inodes.
    const extractDir = join(base, 'extracted');
    const extractedPayload = extractArchive(exe, extractDir, {
      reuse: {
        sourceDir: targetPayload,
        subtrees: ['upstream/app/node_modules', 'upstream/node'],
      },
    });
    const srcStat = statSync(join(targetPayload, 'upstream/node/node.exe'), {
      bigint: true,
    });
    const dstStat = statSync(join(extractedPayload, 'upstream/node/node.exe'), {
      bigint: true,
    });
    assert.equal(dstStat.ino, srcStat.ino);
    assert.equal(verifyPackage(extractedPayload).id, expected.id);
  },
);

test('tray stop signal wakes superviseDsh immediately instead of waiting full poll interval', async () => {
  const input = new PassThrough();
  const control = createControl({ input, write: () => {} });
  let stopSent = false;
  const child = new EventEmitter();
  child.pid = 0;
  child.connected = true;
  child.send = (msg, cb) => {
    if (msg?.aurigaDesktop === 'stop') {
      stopSent = true;
      setTimeout(() => child.emit('exit', 0), 0);
    }
    cb?.();
  };

  const startedAt = Date.now();
  setTimeout(() => {
    input.write('stop\n');
  }, 40);

  const code = await superviseDsh({
    start: () => child,
    stopRequested: () => control.requested,
    stopSignal: control.signal,
    tree: {
      attach() {},
      refresh() {
        return [];
      },
      async stopTree() {},
    },
    supervisor: {
      async reconcile() {
        return {};
      },
    },
    session: { child() {} },
    notify() {},
  });

  const elapsed = Date.now() - startedAt;
  assert.equal(code, 0);
  assert.equal(stopSent, true);
  assert.ok(
    elapsed < 600,
    `expected fast stop wake-up (<600ms), got ${elapsed}ms`,
  );
});

test(
  'native WinHelper compiles and executes ntfs-check, boot-id, proc-info and startup-probe',
  { skip: !windows, timeout: 60000 },
  (t) => {
    const base = tempBase(t);
    const helper = compileHelper(join(base, 'auriga-win-helper.exe'));
    assert.ok(existsSync(helper));

    const bootId = execFileSync(helper, ['boot-id'], {
      encoding: 'utf8',
      windowsHide: true,
    }).trim();
    assert.match(bootId, /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{7}Z$/);

    const ntfs = JSON.parse(
      execFileSync(helper, ['ntfs-check', base], {
        encoding: 'utf8',
        windowsHide: true,
      }),
    );
    assert.equal(ntfs.type, 3);
    assert.equal(ntfs.fs, 'NTFS');
    assert.equal(ntfs.reparse, false);

    const selfInfo = JSON.parse(
      execFileSync(helper, ['proc-info', String(process.pid)], {
        encoding: 'utf8',
        windowsHide: true,
      }),
    );
    assert.equal(selfInfo.pid, process.pid);
    assert.match(
      selfInfo.createdAt,
      /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{6}0Z$/,
    );

    const probe = JSON.parse(
      execFileSync(helper, ['startup-probe', base], {
        encoding: 'utf8',
        windowsHide: true,
      }),
    );
    assert.equal(probe.bootId, bootId);
    assert.equal(probe.ntfs.fs, 'NTFS');
    assert.ok(
      Array.isArray(probe.processes) &&
        probe.processes.some((p) => p.pid === process.pid),
    );

    const list = JSON.parse(
      execFileSync(helper, ['proc-list'], {
        encoding: 'utf8',
        windowsHide: true,
        maxBuffer: 32 * 1024 * 1024,
      }),
    );
    assert.ok(Array.isArray(list) && list.some((p) => p.pid === process.pid));

    const pyDir = join(base, 'py-protected');
    execFileSync(helper, ['protect-python', pyDir, 'create'], {
      windowsHide: true,
    });
    assert.ok(existsSync(pyDir));
    execFileSync(helper, ['protect-python', pyDir, 'seal'], {
      windowsHide: true,
    });

    const dataDir = join(base, 'data-grant');
    mkdirSync(dataDir, { recursive: true });
    execFileSync(helper, ['grant-modify', dataDir], { windowsHide: true });
  },
);

test('movePayload restores staging back to payload on failure and succeeds on clean install', (t) => {
  const base = tempBase(t);
  const root = join(base, 'root');
  const dataHome = join(base, 'data');
  const { dir: badPayload } = buildPayload(base, { name: 'bad' });
  // Corrupt a file so verifyPackage(staging) fails after whole-dir rename.
  writeFileSync(join(badPayload, 'auriga/runtime/core.mjs'), 'corrupted');
  assert.throws(() =>
    install({
      payload: badPayload,
      root,
      dataHome,
      movePayload: true,
      smoke: () => {},
    }),
  );
  // Source payload must be restored so a retry or diagnostic still sees it.
  assert.ok(existsSync(join(badPayload, 'manifest.json')));

  const { dir: goodPayload, manifest } = buildPayload(base, { name: 'good' });
  const res = install({
    payload: goodPayload,
    root,
    dataHome,
    movePayload: true,
    smoke: () => {},
  });
  assert.equal(res.id, manifest.id);
  assert.ok(existsSync(join(root, 'versions', manifest.id, 'manifest.json')));
});

test('readSetupEntry rejects malformed or out-of-bounds ZIP payloads', (t) => {
  const base = tempBase(t);
  const malformed = join(base, 'bad.exe');
  // Fake MZ header + garbage ZIP bytes + valid AURZIP01 trailer
  const zipBody = Buffer.alloc(64, 0);
  // Put an EOCD signature with bogus cdOffset > length
  zipBody.writeUInt32LE(0x06054b50, 42);
  zipBody.writeUInt32LE(100, 42 + 12);
  zipBody.writeUInt32LE(9999, 42 + 16);
  const trailer = Buffer.alloc(16);
  trailer.write('AURZIP01', 0, 'ascii');
  trailer.writeBigInt64LE(BigInt(zipBody.length), 8);
  writeFileSync(
    malformed,
    Buffer.concat([Buffer.from('MZstub'), zipBody, trailer]),
  );
  assert.throws(
    () => readSetupEntry(malformed, 'payload/manifest.json'),
    /invalid setup zip directory bounds/,
  );
});
