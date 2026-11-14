/* global fetch */
/** Explicit isolated Windows evidence run. No user DSH, profile, or fixed port is touched. */
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, createWriteStream } from 'node:fs';
import { join, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { setTimeout as delay } from 'node:timers/promises';
import { resourceFixture, id } from '../test/resource-fixture.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
import { ProcessTree, assertNTFS } from '../runtime/process-tree.mjs';
import { durableJson } from '../runtime/host-lock.mjs';
import { mergeSkillPatch, yamlParser } from '../runtime/skill-config.mjs';

if (process.platform !== 'win32')
  throw new Error('Windows evidence run required');
const base = resolve('clients/installer/target/real-dsh-' + Date.now());
const installation = resolve('clients/installer/target/dsh');
const cli = join(installation, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
const require = createRequire(cli);
assert.equal(require('@deepseek-ai/dsh/package.json').version, '0.2.1-rc.3');
const results = [];
for (const mode of [
  'lease_expired',
  'clock_rollback',
  'identity_changed',
  'windows_occupied',
]) {
  const home = join(base, mode);
  mkdirSync(home, { recursive: true });
  assertNTFS(home);
  const f = resourceFixture(home);
  let now = Date.now(),
    mono = 1000;
  const tree = new ProcessTree(home);
  const supervisor = new Supervisor(home, tree, f.trust, {
    now: () => now,
    time: () => ({ bootId: id(9), monotonicMillis: mono }),
  });
  const release = tree.singleton();
  let child, holder;
  try {
    await supervisor.prepare();
    const patch = join(home, 'shared.patch.yml');
    writeFileSync(
      patch,
      mergeSkillPatch('[]\n', yamlParser(require('yaml')), home),
    );
    const output = createWriteStream(join(home, 'dsh.log'));
    let text = '';
    child = spawn(
      process.execPath,
      [
        cli,
        '--profile',
        'web',
        '--patch',
        patch,
        '--no-open',
        '--host',
        '127.0.0.1',
        '--port',
        '0',
      ],
      {
        env: { ...process.env, DSH_HOME: home },
        stdio: ['ignore', 'pipe', 'pipe'],
        windowsHide: true,
      },
    );
    child.stdout.on('data', (b) => {
      text += b;
      output.write(b.toString().replace(/token=[^\s]+/g, 'token=[redacted]'));
    });
    child.stderr.on('data', (b) => {
      text += b;
      output.write(b.toString().replace(/token=[^\s]+/g, 'token=[redacted]'));
    });
    tree.attach(child.pid);
    let url;
    for (let i = 0; i < 90; i++) {
      url = text.match(/https?:\/\/127\.0\.0\.1:\d+/)?.[0];
      if (url) {
        try {
          const response = await fetch(url);
          if (response.status < 500) break;
        } catch {
          url = null;
        }
      }
      if (child.exitCode !== null)
        throw new Error('DSH exited: ' + text.slice(-2000));
      await delay(500);
    }
    assert(url, 'isolated real DSH did not become ready');
    tree.refresh();
    if (mode === 'windows_occupied') {
      const file = join(
        home,
        'auriga/skills-active/current/example-resource/SKILL.md',
      ).replaceAll("'", "''");
      holder = spawn(
        'powershell.exe',
        [
          '-NoProfile',
          '-NonInteractive',
          '-Command',
          `$f=[IO.File]::Open('${file}',[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read); Write-Output 'HELD'; Start-Sleep 60; $f.Dispose()`,
        ],
        { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true },
      );
      await new Promise((ok, bad) => {
        holder.once('error', bad);
        holder.stdout.once('data', ok);
      });
      mono += 3600001;
      await assert.rejects(supervisor.reconcile());
      assert.equal(supervisor.guardian.read().guardian.state, 'blocked');
      holder.kill();
      await new Promise((ok) => holder.once('exit', ok));
      holder = null;
      await supervisor.guardian.disable('verification_failed');
    } else {
      if (mode === 'clock_rollback') now -= 120001;
      if (mode === 'lease_expired') mono += 3600001;
      if (mode === 'identity_changed') {
        f.ledger.generation++;
        durableJson(join(f.directory, 'ledger.json'), f.ledger);
      }
      await supervisor.reconcile();
    }
    assert(await tree.isStopped());
    assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
    assert(supervisor.guardian.cleared());
    await supervisor.reconcile(true);
    let privateOutput = '';
    child = spawn(process.execPath, child.spawnargs.slice(1), {
      env: { ...process.env, DSH_HOME: home },
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
    child.stdout.on('data', (b) => {
      privateOutput += b;
    });
    child.stderr.on('data', (b) => {
      privateOutput += b;
    });
    tree.attach(child.pid);
    let privateReady = false;
    for (let attempt = 0; attempt < 90; attempt++) {
      const endpoint = privateOutput.match(/https?:\/\/127\.0\.0\.1:\d+/)?.[0];
      if (endpoint) {
        const response = await fetch(endpoint);
        if (response.status < 500) {
          privateReady = true;
          break;
        }
      }
      if (child.exitCode !== null) throw new Error('private DSH exited');
      await delay(500);
    }
    assert(privateReady, 'private DSH restart failed');
    await supervisor.reconcile();
    assert.equal(supervisor.guardian.read().guardian.state, 'private_only');
    assert(supervisor.guardian.cleared());
    results.push({
      mode,
      version: '0.2.1-rc.3',
      url,
      aurigaPluginInstalled: false,
      stopped: true,
      isolated: true,
      privateRestartPermitted: true,
      privateRestartStarted: true,
    });
    output.end();
  } finally {
    holder?.kill();
    await tree.stopTree();
    release();
  }
}
writeFileSync(join(base, 'evidence.json'), JSON.stringify(results, null, 2));
console.log(JSON.stringify({ base, results }, null, 2));
