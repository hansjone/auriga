import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  cpSync,
  writeFileSync,
  rmSync,
  existsSync,
  readFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { buildBroker } from '../../shell-auth/build.mjs';
import { startShellAuth } from '../runtime/shell-auth.mjs';
for (const pathPrefix of ['auriga-package-auth-', 'auriga-runner~1-'])
  test(
    `packaged Windows launcher exports diagnostics using its bundled Node and self-contained broker (${pathPrefix})`,
    { skip: process.platform !== 'win32', timeout: 30000 },
    (t) => {
      const directory = mkdtempSync(join(tmpdir(), pathPrefix));
      t.after(() =>
        rmSync(directory, {
          recursive: true,
          force: true,
          maxRetries: 5,
          retryDelay: 100,
        }),
      );
      const root = join(directory, 'installed root'),
        version = join(root, 'versions', 'test-build'),
        dataHome = join(directory, 'data'),
        output = join(directory, 'exports');
      mkdirSync(join(version, 'auriga'), { recursive: true });
      mkdirSync(join(version, 'upstream', 'node'), { recursive: true });
      mkdirSync(join(root, 'bin'), { recursive: true });
      const manifest = buildBroker(join(version, 'auriga', 'shell-auth'));
      for (const file of manifest.files)
        assert.equal(
          createHash('sha256')
            .update(
              readFileSync(join(version, 'auriga', 'shell-auth', file.path)),
            )
            .digest('hex'),
          file.sha256,
        );
      cpSync(
        new URL('../runtime/', import.meta.url),
        join(version, 'auriga', 'runtime'),
        { recursive: true },
      );
      mkdirSync(join(version, 'auriga', 'runtime', 'schemas'), {
        recursive: true,
      });
      for (const name of [
        'guardian-transitions.json',
        'package.schema.json',
        'signatures.schema.json',
        'unified.schema.json',
      ])
        cpSync(
          new URL('../../../contracts/resource/' + name, import.meta.url),
          join(version, 'auriga', 'runtime', 'schemas', name),
        );
      cpSync(
        new URL('../windows/launcher/auriga-desktop.cmd', import.meta.url),
        join(root, 'bin', 'auriga-desktop.cmd'),
      );
      cpSync(process.execPath, join(version, 'upstream', 'node', 'node.exe'));
      writeFileSync(join(root, '.auriga-desktop-install'), 'test');
      writeFileSync(join(root, 'current.txt'), 'test-build');
      writeFileSync(
        join(root, 'install.json'),
        JSON.stringify({ current: 'test-build', dataHome }),
      );
      const tenant = randomUUID(),
        provider = randomUUID();
      mkdirSync(join(dataHome, 'auriga', 'logs'), { recursive: true });
      writeFileSync(
        join(dataHome, 'auriga', 'logs', 'auriga-shell-auth.jsonl'),
        JSON.stringify({
          timestamp: new Date().toISOString(),
          tenantId: tenant,
          providerId: provider,
          stage: 'shell_identity',
          result: 'success',
        }) + '\n',
      );
      const command = `""${join(root, 'bin', 'auriga-desktop.cmd')}" diagnostics export --output "${output}" --tenant ${tenant} --provider ${provider}"`;
      const result = spawnSync(
        join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'cmd.exe'),
        ['/d', '/s', '/c', command],
        {
          cwd: directory,
          encoding: 'utf8',
          windowsHide: true,
          windowsVerbatimArguments: true,
          timeout: 20000,
        },
      );
      assert.equal(result.status, 3, result.stderr || result.stdout);
      const exported = JSON.parse(result.stdout);
      assert.equal(exported.partial, true);
      assert.ok(existsSync(exported.file));
    },
  );

test(
  'shell startup failure records a safe local diagnostic while allowing local DSH to continue',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-startup-'));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const version = join(directory, 'version'),
      root = join(directory, 'root'),
      dataHome = join(directory, 'home');
    buildBroker(join(version, 'auriga', 'shell-auth'));
    mkdirSync(root);
    const sensitive = randomUUID();
    writeFileSync(join(root, 'desktop-origin.json'), '{invalid:' + sensitive);
    const errors = [];
    t.mock.method(console, 'error', (value) => errors.push(value));
    assert.equal(
      await startShellAuth({ root, dataHome, dir: version }),
      undefined,
    );
    const text = readFileSync(
      join(dataHome, 'auriga', 'logs', 'auriga-shell-auth.jsonl'),
      'utf8',
    );
    const event = JSON.parse(text.trim());
    assert.equal(event.stage, 'shell_identity');
    assert.equal(event.state, 'unreachable');
    assert.equal(event.reason, 'shell_restarted');
    assert.equal(event.level, 'WARN');
    assert.ok(!text.includes(sensitive));
    assert.ok(!JSON.stringify(errors).includes(sensitive));
  },
);

test('missing shell diagnostic sink emits safe structured stderr without exposing paths or raw errors', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'auriga-missing-shell-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const errors = [];
  t.mock.method(console, 'error', (value) => errors.push(value));
  assert.equal(
    await startShellAuth({
      root: directory,
      dataHome: directory,
      dir: directory,
    }),
    undefined,
  );
  const event = JSON.parse(errors[0]);
  assert.equal(event.stage, 'shell_identity');
  assert.equal(event.result, 'failure');
  assert.ok(!JSON.stringify(errors).includes(directory));
});
