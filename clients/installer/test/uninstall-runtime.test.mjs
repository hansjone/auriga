import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { install } from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

// Native wrapper coverage is meaningful on its supported Windows platform;
// the normal resource/coordination tests continue to run on every platform.
if (process.platform === 'win32')
  test('Windows uninstall temp copy includes frozen protocol assets', (t) => {
    const base = tempBase(t),
      root = join(base, 'app'),
      home = join(base, 'home');
    const payload = buildPayload(base);
    install({ payload: payload.dir, root, dataHome: home, smoke: () => {} });
    const version = join(root, 'versions', payload.id);
    const runtime = join(version, 'auriga/runtime');
    cpSync(fileURLToPath(new URL('../runtime', import.meta.url)), runtime, {
      recursive: true,
    });
    const schemas = join(runtime, 'schemas');
    mkdirSync(schemas);
    cpSync(
      fileURLToPath(
        new URL(
          '../../../contracts/resource/guardian-transitions.json',
          import.meta.url,
        ),
      ),
      join(schemas, 'guardian-transitions.json'),
    );
    cpSync(process.execPath, join(version, 'upstream/node/node.exe'));
    const wrapper = join(root, 'bin/uninstall.ps1');
    cpSync(
      fileURLToPath(
        new URL('../windows/launcher/uninstall.ps1', import.meta.url),
      ),
      wrapper,
    );
    const partial = join(base, 'old-temp-copy');
    mkdirSync(partial);
    for (const name of readdirSync(runtime).filter((n) => n.endsWith('.mjs')))
      cpSync(join(runtime, name), join(partial, name));
    const broken = spawnSync(
      process.execPath,
      [join(partial, 'cli.mjs'), 'uninstall', '--root', root],
      { encoding: 'utf8' },
    );
    assert.notEqual(broken.status, 0);
    assert.match(broken.stderr, /guardian-transitions/);
    assert(existsSync(root));
    const result = spawnSync(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-ExecutionPolicy',
        'Bypass',
        '-File',
        wrapper,
      ],
      { encoding: 'utf8', timeout: 60000, windowsHide: true },
    );
    assert.equal(result.status, 0, result.stderr);
    assert.equal(existsSync(root), false);
    assert.equal(existsSync(home), true);
  });
