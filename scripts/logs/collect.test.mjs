import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, readFile, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { options, collect } from './collect.mjs';

const trace = '4bf92f3577b34da6a3ce929d0e0e4736';
test('read-only collector fixture records exact Docker arguments, local trace selection, plugin time filter and masking', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-collect-'));
  try {
    const plugin = join(root, 'plugin');
    await mkdir(plugin);
    const record = {
      '@timestamp': '2026-10-29T12:00:00Z',
      trace_id: trace,
      service: 'dsh-plugin',
      level: 'ERROR',
      message: 'account_id=alice password=secret-password',
    };
    await writeFile(
      join(plugin, 'auriga-dsh-0.jsonl'),
      JSON.stringify(record) +
        '\n' +
        JSON.stringify({ ...record, '@timestamp': '2026-10-28T00:00:00Z' }) +
        '\n',
    );
    const calls = [];
    const config = options([
      '--out',
      join(root, 'result'),
      '--plugin-dir',
      plugin,
      '--since',
      '2026-10-29T11:00:00Z',
      '--until',
      '2026-10-29T13:00:00Z',
      '--trace-id',
      trace,
    ]);
    const manifest = await collect(config, async (args, accept) => {
      calls.push(args);
      if (args[0] === 'ps') await accept('abcdef123456 edge');
      else {
        await accept('2026-10-29T12:00:00Z ' + JSON.stringify(record));
        await accept(JSON.stringify({ ...record, trace_id: '0'.repeat(32) }));
      }
      return { exit_code: 0, reader_errors: [], truncated: false };
    });
    assert.equal(manifest.exit_code, 0);
    assert.deepEqual(calls[1], [
      'logs',
      '--timestamps',
      '--since',
      config.since,
      '--until',
      config.until,
      'abcdef123456',
    ]);
    assert.equal(manifest.sources[1].selected, 1);
    assert.equal(manifest.sources[2].selected, 1);
    for (const name of ['docker-edge-abcdef123456.log', 'auriga-dsh-0.jsonl']) {
      const text = await readFile(join(config.directory, name), 'utf8');
      assert.ok(!text.includes('secret-password'));
      assert.ok(text.includes('account_id=alice'));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
test('missing Docker/plugin sources remain partial with individual exit code', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-collect-missing-'));
  try {
    const config = options([
      '--out',
      join(root, 'result'),
      '--plugin-dir',
      join(root, 'missing'),
    ]);
    const manifest = await collect(config, async () => ({
      exit_code: 127,
      reason: 'ENOENT',
      reader_errors: [],
      truncated: false,
    }));
    assert.equal(manifest.exit_code, 2);
    assert.equal(manifest.sources[0].exit_code, 127);
    assert.ok(manifest.sources.some((s) => s.missing));
    assert.equal(
      JSON.parse(
        await readFile(join(config.directory, 'manifest.json'), 'utf8'),
      ).exit_code,
      2,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
test('bad trace, reverse/invalid windows and injection-like projects are rejected', () => {
  assert.throws(() => options(['--trace-id', 'a'.repeat(10000)]));
  assert.throws(() => options(['--project', '--help']));
  assert.throws(() => options(['--since', 'yesterday']));
  assert.throws(() =>
    options([
      '--since',
      '2027-01-01T00:00:00Z',
      '--until',
      '2026-01-01T00:00:00Z',
    ]),
  );
});
