import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tempBase } from './helpers.mjs';

const windows = process.platform === 'win32';
const source = (file) =>
  fileURLToPath(new URL('../windows/launcher/' + file, import.meta.url));
const ps = (file, args = []) =>
  spawnSync(
    'powershell.exe',
    [
      '-NoProfile',
      '-NonInteractive',
      '-ExecutionPolicy',
      'Bypass',
      '-File',
      file,
      ...args,
    ],
    { encoding: 'utf8', windowsHide: true, timeout: 30000 },
  );

test(
  'deferred cleanup rejects arbitrary paths and touches only its program quarantine',
  { skip: !windows },
  (t) => {
    const base = tempBase(t);
    const root = join(base, 'install');
    const data = join(base, 'reused-data');
    const id = randomUUID();
    const job = join(base, 'AurigaDesktopCleanup', id);
    const quarantine = join(root, '.uninstall-' + id);
    const script = join(job, 'uninstall-cleanup.ps1');
    const planFile = join(job, 'cleanup.json');
    const guard = join(root, '.auriga-desktop-uninstall-pending');
    for (const path of [data, job, quarantine])
      mkdirSync(path, { recursive: true });
    copyFileSync(source('uninstall-cleanup.ps1'), script);
    writeFileSync(guard, id);
    writeFileSync(join(data, 'new-session'), 'present');
    writeFileSync(join(quarantine, 'program.exe'), 'old program');
    writeFileSync(
      planFile,
      JSON.stringify({ schema: 1, root, id, paths: [data] }),
    );
    assert.notEqual(ps(script).status, 0);
    assert.ok(existsSync(quarantine));
    assert.ok(existsSync(guard));
    assert.equal(readFileSync(join(data, 'new-session'), 'utf8'), 'present');
    writeFileSync(planFile, JSON.stringify({ schema: 1, root, id }));
    const cleaned = ps(script);
    assert.equal(cleaned.status, 0, cleaned.stderr);
    assert.equal(existsSync(quarantine), false);
    assert.equal(existsSync(guard), false);
    assert.ok(
      existsSync(root),
      'the deferred job never removes the install root',
    );
    assert.equal(readFileSync(join(data, 'new-session'), 'utf8'), 'present');
  },
);

test(
  'shell bindings belong to their actual install and autostart resolves the current version',
  { skip: !windows },
  (t) => {
    const base = tempBase(t);
    const root = join(base, 'Install A');
    const other = join(base, 'Install B');
    const bin = join(root, 'bin');
    const trayDir = join(root, 'versions', 'with-tray', 'auriga', 'tray');
    mkdirSync(bin, { recursive: true });
    mkdirSync(trayDir, { recursive: true });
    writeFileSync(join(root, '.auriga-desktop-install'), '{}');
    writeFileSync(join(trayDir, 'AurigaDesktop.exe'), '');
    copyFileSync(
      source('auriga-autostart.ps1'),
      join(bin, 'auriga-autostart.ps1'),
    );
    const script = fileURLToPath(
      new URL('./windows-shell-fixture.ps1', import.meta.url),
    );
    const resolved = ps(script, [root, other, source('shell-integration.ps1')]);
    assert.equal(resolved.status, 0, resolved.stderr);
    const result = JSON.parse(resolved.stdout);
    assert.deepEqual(result.ownership, [false, false, true, true]);
    assert.equal(result.chosen, join(root, 'fixture-shortcuts', 'App (A).lnk'));
    assert.equal(result.new.file, join(trayDir, 'AurigaDesktop.exe'));
    assert.deepEqual(result.new.args, ['/autostart']);
    assert.match(result.old.file, /rolled-back\\upstream\\node\\node\.exe$/);
    assert.ok(result.old.args.includes('--no-open'));
    assert.ok(!result.old.args.includes('/autostart'));
    assert.equal(result.new.hidden && result.old.hidden, true);
  },
);
