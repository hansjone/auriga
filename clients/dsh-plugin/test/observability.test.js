/* global Headers */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { setImmediate } from 'node:timers/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createFileSink } from '../src/observability/file.js';
import {
  mask,
  newTraceparent,
  validTraceparent,
  traceFetch,
  natsTraceparent,
} from '../src/observability/index.js';

test('mask bounds work before scanning and handles long untrusted tokens in milliseconds', () => {
  for (const prefix of ['', 'prefix-', 'file.name.'])
    assert.ok(
      !mask(prefix + 'eyJabcde.abcdefgh.signature').includes('eyJabcde'),
    );
  assert.ok(
    !mask('db-password=secret-regression').includes('secret-regression'),
  );
  mask('warmup password=hidden');
  for (const input of [
    '/api/org/v1/invitations/preview/' + 'a'.repeat(8000),
    'x'.repeat(65536),
    'a_'.repeat(32768),
    'a.'.repeat(32768),
    'eyJ-'.repeat(16384),
    'eyJ' + 'x'.repeat(65000) + '.payload.signature',
    '-----BEGIN PRIVATE KEY-----\n'.repeat(2048),
  ]) {
    const start = performance.now();
    mask(input);
    assert.ok(performance.now() - start < 50, 'mask exceeded 50ms');
  }
  assert.ok(mask('x'.repeat(65537)).length <= 65536);
  assert.ok(
    !mask('AURIGA_' + 'A'.repeat(8000) + '_PASSWORD=hidden-value').includes(
      'hidden-value',
    ),
  );
});

test('INFO and complete errors stay in the local file, never stderr; HTTP paths hide credentials', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-log-private-'));
  const source = new URL('../src/observability/index.js', import.meta.url).href;
  const code = `
    import { installObservability, logEvent, logDirectory, traceFetch } from ${JSON.stringify(source)};
    const cleanups = [];
    installObservability({ effect(setup) { cleanups.push(setup()); } });
    logEvent('INFO', 'fixture info', { account_id: 'alice' });
    logEvent('ERROR', 'fixture error', {}, new Error('password=secret-password'));
    for (const path of ['/api/org/v1/invitations/preview/secret-invite', '/api/iam/v1/device/secret-code/approve', '/unmatched/secret-unknown'])
      await traceFetch(async () => new Response('{}'), 'https://example.com' + path);
    logEvent('ERROR', 'x'.repeat(65537));
    await Promise.all(cleanups.map(close => close()));
    process.stdout.write(logDirectory());
  `;
  const child = spawn(process.execPath, ['--input-type=module', '-e', code], {
    env: { ...process.env, LOCALAPPDATA: directory, XDG_STATE_HOME: directory },
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  let stdout = '',
    stderr = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk;
  });
  child.stderr.on('data', (chunk) => {
    stderr += chunk;
  });
  try {
    const result = await new Promise((resolve, reject) => {
      child.once('close', resolve);
      child.once('error', reject);
    });
    assert.equal(result, 0, stderr);
    assert.equal(stderr, '');
    const file = await readFile(join(stdout, 'auriga-dsh-0.jsonl'), 'utf8');
    const events = file.trim().split('\n').map(JSON.parse);
    assert.equal(events.length, 8);
    assert.equal(events[0].level, 'INFO');
    assert.ok(events[1].exception.includes('at '));
    assert.ok(!file.includes('secret-'));
  } finally {
    child.kill('SIGKILL');
    await rm(directory, { recursive: true, force: true });
  }
});

test('trace headers are valid, fresh per operation, and do not replace business request id', async () => {
  const seen = [];
  const fetcher = async (_url, init) => {
    seen.push(new Headers(init.headers));
    return new Response('{}');
  };
  for (let i = 0; i < 2; i++)
    await traceFetch(fetcher, 'https://example.com/api/test', {
      headers: { 'X-Request-Id': 'business-id', traceparent: 'forged' },
    });
  assert.ok(
    seen.every(
      (h) =>
        validTraceparent(h.get('traceparent')) &&
        h.get('X-Request-Id') === 'business-id',
    ),
  );
  assert.notEqual(seen[0].get('traceparent'), seen[1].get('traceparent'));
  for (const v of [
    '',
    'x'.repeat(100000),
    `00-${'0'.repeat(32)}-${'1'.repeat(16)}-01`,
    'ff' + newTraceparent().slice(2),
  ])
    assert.equal(validTraceparent(v), false);
  const failure = new Error('network failure');
  await assert.rejects(
    traceFetch(async () => {
      throw failure;
    }, 'https://example.com/'),
    (error) => error === failure,
  );
});
test('secrets are removed while business IDs and complete stack locations survive', () => {
  const text = mask(
    JSON.stringify({
      account_id: 'alice',
      exception:
        'Error: failure\n at send (file.js:42:1)\nAuthorization: Bearer secret-auth\nCookie: sid=secret-cookie\nAURIGA_PRIVATE_KEY=secret-env\n-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----',
      password: 'secret-password',
      totp: 'secret-totp',
    }),
  );
  for (const secret of [
    'secret-auth',
    'secret-cookie',
    'secret-env',
    'private-material',
    'secret-password',
    'secret-totp',
  ])
    assert.ok(!text.includes(secret), secret);
  const event = JSON.parse(text);
  assert.equal(event.account_id, 'alice');
  assert.ok(event.exception.includes('file.js:42:1'));
  assert.equal(
    mask('input_tokens=42 max_tokens=2048 token_id=business-reference'),
    'input_tokens=42 max_tokens=2048 token_id=business-reference',
  );
});
test('fixed file slots rotate, concurrent owners do not mix, disposal drains and releases', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-log-'));
  try {
    const a = createFileSink(directory, { maxBytes: 128 }),
      b = createFileSink(directory, { maxBytes: 128 });
    for (let i = 0; i < 20; i++) {
      a.write(JSON.stringify({ owner: 'a', i }) + '\n');
      b.write(JSON.stringify({ owner: 'b', i }) + '\n');
    }
    await Promise.all([a.close(), b.close()]);
    const names = await readdir(directory);
    assert.equal(names.filter((n) => n.endsWith('.lock')).length, 0);
    assert.equal(names.length, 4);
    for (const name of names) {
      const text = await readFile(join(directory, name), 'utf8');
      assert.ok(Buffer.byteLength(text) <= 128);
      const events = text.trim().split('\n').map(JSON.parse);
      assert.equal(new Set(events.map((e) => e.owner)).size, 1);
    }
    a.write('closed');
    assert.equal(a.dropped, 1);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
test('NATS logical request traces match the cross-language vector without changing payload', () => {
  const payload = Buffer.from(
    JSON.stringify({
      request_id: 'f20d474e-07d3-48db-9f32-930a43329c02',
      type: 'fetch',
    }),
  );
  const before = payload.toString('hex');
  const first = natsTraceparent(payload),
    second = natsTraceparent(payload);
  assert.equal(first.slice(3, 35), 'fd0a7edb479e9a9982f9912dfa755842');
  assert.equal(first.slice(3, 35), second.slice(3, 35));
  assert.notEqual(first.slice(36, 52), second.slice(36, 52));
  assert.equal(payload.toString('hex'), before);
  assert.notEqual(
    natsTraceparent(Buffer.from('{}')).slice(3, 35),
    natsTraceparent(Buffer.from('{}')).slice(3, 35),
  );
});

test('stale slot reclamation is serialized across concurrent writers; file failure stays best effort', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-log-stale-'));
  try {
    const child = spawn(process.execPath, ['-e', ''], {
      stdio: 'ignore',
      windowsHide: true,
    });
    await new Promise((resolve, reject) => {
      child.once('exit', resolve);
      child.once('error', reject);
    });
    await writeFile(join(directory, 'auriga-dsh-0.lock'), String(child.pid));
    const sinks = Array.from({ length: 4 }, () => createFileSink(directory));
    for (const [index, sink] of sinks.entries())
      sink.write(JSON.stringify({ index }) + '\n');
    // Keep all leases alive until every concurrent writer has written. Closing
    // a fast writer while another is still acquiring legitimately frees a slot
    // for reuse, which does not demonstrate overlapping live leases.
    const deadline = performance.now() + 5000;
    while (performance.now() < deadline) {
      const files = (await readdir(directory)).filter((name) =>
        name.endsWith('.jsonl'),
      );
      const lines = await Promise.all(
        files.map((name) => readFile(join(directory, name), 'utf8')),
      );
      if (lines.join('\n').split('\n').filter(Boolean).length === 4) break;
      await setImmediate();
    }
    await Promise.all(sinks.map((sink) => sink.close()));
    const files = (await readdir(directory)).filter((name) =>
      name.endsWith('.jsonl'),
    );
    assert.equal(files.length, 4);
    const owners = await Promise.all(
      files.map(
        async (name) =>
          JSON.parse((await readFile(join(directory, name), 'utf8')).trim())
            .index,
      ),
    );
    assert.equal(new Set(owners).size, 4);
    const blocked = join(directory, 'regular-file');
    await writeFile(blocked, '');
    const broken = createFileSink(blocked);
    assert.doesNotThrow(() => broken.write('line\n'));
    await broken.close();
    assert.equal(broken.dropped, 1);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
