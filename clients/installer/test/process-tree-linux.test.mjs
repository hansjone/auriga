import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import {
  assertNTFS,
  processIdentity,
  processList,
  sameProcess,
} from '../runtime/process-tree.mjs';

test(
  'linux local volume check accepts tmpdir and lists this process',
  { skip: process.platform !== 'linux' },
  () => {
    const home = mkdtempSync(join(tmpdir(), 'auriga-linux-home-'));
    try {
      assertNTFS(home);
      const me = processIdentity(process.pid);
      assert.equal(me.pid, process.pid);
      assert.ok(Number.isFinite(Date.parse(me.createdAt)));
      const listed = processList().find((p) => p.pid === process.pid);
      assert.ok(sameProcess(listed, me));
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  },
);

test(
  'linux local volume check rejects symlink parents',
  { skip: process.platform !== 'linux' },
  () => {
    const base = mkdtempSync(join(tmpdir(), 'auriga-linux-link-'));
    try {
      const real = join(base, 'real');
      const link = join(base, 'link');
      mkdirSync(real);
      writeFileSync(join(real, 'marker'), '1');
      symlinkSync('real', link);
      assert.throws(() => assertNTFS(join(link, 'home')), /reparse|link/i);
    } finally {
      rmSync(base, { recursive: true, force: true });
    }
  },
);
