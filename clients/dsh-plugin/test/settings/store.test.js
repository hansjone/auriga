import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  normalizeOrigin,
  probe,
  createSettingsStore,
} from '../../src/settings/store.js';

test('settings accepts HTTPS and explicit local HTTP origins only', () => {
  for (const value of [
    'https://example.com',
    'http://localhost:3000',
    'http://[::1]',
  ])
    assert.equal(normalizeOrigin(value), new URL(value).origin);
  assert.equal(normalizeOrigin('http://10.0.0.1'), 'http://10.0.0.1'); // gitleaks:allow
  for (const value of [
    'http://example.com',
    'http://192.0.2.10',
    'ftp://example.com',
    'https://user:pass@example.com',
    'https://example.com/path',
    'https://example.com/?x=1',
    'https://example.com/#x',
    'http://10.example.com',
    'http://0x7f000001',
    'http://127.1',
    'http://[::ffff:127.0.0.1]',
  ])
    assert.throws(() => normalizeOrigin(value));
});

test('connection probe refuses redirects and distinguishes HTTP, transport and invalid platform failures', async () => {
  /** @type {RequestInit} */
  let options = {};
  const fetcher = async (
    /** @type {string|URL|Request} */ _url,
    /** @type {RequestInit} */ init = {},
  ) => {
    options = init;
    return Response.json({ keys: [] });
  };
  assert.deepEqual(await probe('https://example.com', fetcher), {
    reachable: true,
  });
  assert.equal(options.redirect, 'error');
  assert.equal(options.credentials, 'omit');
  assert.deepEqual(
    await probe(
      'https://example.com',
      async () => new Response(null, { status: 401 }),
    ),
    { reachable: false, reason: 'http', status: 401 },
  );
  assert.deepEqual(
    await probe('https://example.com', async () => {
      throw new Error('private');
    }),
    { reachable: false, reason: 'network' },
  );
  assert.deepEqual(
    await probe('https://example.com', async () => Response.json({})),
    { reachable: false, reason: 'invalidPlatform' },
  );
  for (const body of ['invalid-json', 'x'.repeat(131073)]) {
    assert.deepEqual(
      await probe('https://example.com', async () => new Response(body)),
      { reachable: false, reason: 'invalidPlatform' },
    );
  }
  assert.deepEqual(
    await probe('https://example.com', async () => {
      throw Object.assign(new Error(), { name: 'TimeoutError' });
    }),
    { reachable: false, reason: 'timeout' },
  );
});

test('settings persist address and deployment across reopen, rejecting invalid writes', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-settings-'));
  try {
    const store = await createSettingsStore(dir);
    assert.equal(store.snapshot().deployment, 'cloud');
    await store.save({
      platformOrigin: 'https://example.com',
      deployment: 'local',
    });
    const reopened = await createSettingsStore(dir);
    assert.deepEqual(reopened.snapshot(), store.snapshot());
    await assert.rejects(
      store.save({ platformOrigin: 'http://example.com', deployment: 'local' }),
    );
    await assert.rejects(
      store.save({
        platformOrigin: 'https://example.com',
        deployment: 'other',
      }),
    );
    assert.equal(store.snapshot().deployment, 'local');
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
