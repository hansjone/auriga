import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

test(
  'public Cordis assembly writes masked stack to stderr and finite file, then closes on disposal',
  {
    skip: process.env.AURIGA_CORDIS_MODULE
      ? false
      : 'set AURIGA_CORDIS_MODULE to public Cordis 4.0.5',
  },
  async () => {
    const directory = await mkdtemp(join(tmpdir(), 'auriga-obs-cordis-'));
    const source = new URL('../src/observability/index.js', import.meta.url)
      .href;
    const cordis = pathToFileURL(process.env.AURIGA_CORDIS_MODULE).href;
    const code = `
    import { Context } from ${JSON.stringify(cordis)};
    import { installObservability, logEvent, logDirectory } from ${JSON.stringify(source)};
    const ctx = new Context();
    installObservability(ctx);
    process.on('message', async () => {
      await ctx.fiber.dispose();
      process.stdout.write(logDirectory());
      process.disconnect();
    });
    logEvent('ERROR', 'fixture failed', { trace_id:'4bf92f3577b34da6a3ce929d0e0e4736', account_id:'alice' }, new Error('Authorization: Bearer secret-fixture password=secret-password'));
  `;
    const child = spawn(process.execPath, ['--input-type=module', '-e', code], {
      env: {
        SystemRoot: process.env.SystemRoot,
        LOCALAPPDATA: directory,
        XDG_STATE_HOME: directory,
      },
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
      windowsHide: true,
      shell: false,
    });
    try {
      let stderr = '',
        stdout = '',
        signalled = false;
      const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
      child.stderr.on('data', (chunk) => {
        stderr += chunk;
        if (!signalled && stderr.includes('\n')) {
          signalled = true;
          child.send('close');
        }
      });
      child.stdout.on('data', (chunk) => {
        stdout += chunk;
      });
      const result = await new Promise((resolve, reject) => {
        child.once('exit', resolve);
        child.once('error', reject);
      });
      clearTimeout(timer);
      assert.equal(result, 0, stderr);
      const event = JSON.parse(stderr.trim());
      assert.equal(event.trace_id, '4bf92f3577b34da6a3ce929d0e0e4736');
      assert.equal(event.service, 'dsh-plugin');
      assert.equal(event.account_id, 'alice');
      assert.ok(event.exception.includes('Error:'));
      assert.ok(event.exception.includes('at '));
      for (const secret of ['secret-fixture', 'secret-password'])
        assert.ok(!stderr.includes(secret));
      const names = await readdir(stdout);
      assert.deepEqual(names, ['auriga-dsh-0.jsonl']);
      const file = await readFile(join(stdout, names[0]), 'utf8');
      assert.deepEqual(JSON.parse(file), event);
    } finally {
      child.kill('SIGKILL');
      await rm(directory, { recursive: true, force: true });
    }
  },
);
