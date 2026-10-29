/* global Headers */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
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
