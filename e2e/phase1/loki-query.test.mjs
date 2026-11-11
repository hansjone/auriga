import test from 'node:test';
import assert from 'node:assert/strict';
import { queryLoki } from './loki-query.mjs';

test('remote Loki transport sends a URL on stdin and parses actual JSON stdout', async () => {
  const url = new URL('http://127.0.0.1:3100/loki/api/v1/query_range');
  url.searchParams.set('query', '{service="edge"} | json | trace_id="abc"');
  const data = await queryLoki(
    [
      process.execPath,
      '-e',
      `
      let input = '';
      process.stdin.on('data', chunk => input += chunk);
      process.stdin.on('end', () => {
        const url = new URL(input.trim());
        if (!url.searchParams.get('query').includes('trace_id="abc"')) process.exit(2);
        process.stdout.write(JSON.stringify({status:'success',data:{result:[]}}));
      });
    `,
    ],
    url,
  );
  assert.equal(data.status, 'success');
});

test('remote Loki transport rejects failures, invalid JSON, excess output and shell strings', async () => {
  const url = new URL('http://127.0.0.1:3100/loki/api/v1/query_range');
  for (const program of [
    'process.exit(3)',
    'process.stdout.write("private-not-json")',
    'process.stdout.write("x".repeat(1048577))',
  ]) {
    await assert.rejects(
      queryLoki([process.execPath, '-e', program], url),
      (error) => !error.message.includes('private-not-json'),
    );
  }
  await assert.rejects(queryLoki('sh -c unsafe', url));
  for (const timeout of [0, -1, 60001, 0.5, '30000'])
    await assert.rejects(queryLoki([process.execPath], url, timeout));
});

test('startup trace URL validates ID and selects the entire chain in time order', async () => {
  const { startupTraceUrl } = await import('./loki-query.mjs');
  const id = '4bf92f3577b34da6a3ce929d0e0e4736';
  const url = startupTraceUrl(id, {
    start: '2026-11-01T00:00:00Z',
    end: '2026-11-01T01:00:00Z',
  });
  assert.equal(
    url.searchParams.get('query'),
    '{job=~"auriga-nas|auriga-plugin|auriga-compose"} | json | trace_id="' + id + '"',
  );
  assert.equal(url.searchParams.get('direction'), 'forward');
  assert.equal(url.searchParams.get('start'), '2026-11-01T00:00:00Z');
  for (const bad of ['', '0'.repeat(32), id + '"', 'x'.repeat(32)])
    assert.throws(() => startupTraceUrl(bad));
});
