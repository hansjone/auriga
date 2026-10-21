import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { once } from 'node:events';
import { openWebSocket } from './ws-client.mjs';

async function fake(t) {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-fake-test-'));
  const pair = generateKeyPairSync('ed25519');
  const kid = randomUUID();
  const jwk = pair.publicKey.export({ format: 'jwk' });
  const keys = join(dir, 'keys.json');
  writeFileSync(keys, JSON.stringify({ keys: [{ ...jwk, kid }] }));
  const child = spawn(process.execPath, ['deploy/compose/fake-dsh/server.js'], {
    env: {
      ...process.env,
      PORT: '0',
      DSH_HOME: dir,
      AURIGA_INSTANCE_ID: 'fixture-instance',
      AURIGA_OWNER_ID: 'fixture-owner',
      AURIGA_IDENTITY_KEYS_FILE: keys,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stderr.resume();
  t.after(async () => {
    if (child.exitCode === null) {
      child.kill();
      await once(child, 'exit');
    }
    rmSync(dir, { recursive: true, force: true });
  });
  const port = await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error('fake server startup timeout')),
      5000,
    );
    child.once('error', reject);
    child.once('exit', () => {
      clearTimeout(timer);
      reject(new Error('fake server exited'));
    });
    child.stdout.on('data', (data) => {
      const match = /listening on (\d+)/.exec(String(data));
      if (match) {
        clearTimeout(timer);
        resolve(Number(match[1]));
      }
    });
  });
  function identity(htu = '/ws', sub = 'fixture-owner') {
    const header = Buffer.from(
      JSON.stringify({ alg: 'EdDSA', typ: 'auriga-id+jwt', kid }),
    ).toString('base64url');
    const claims = Buffer.from(
      JSON.stringify({
        iss: 'auriga-edge',
        aud: 'fixture-instance',
        sub,
        htm: 'GET',
        htu,
        exp: Math.floor(Date.now() / 1000) + 60,
      }),
    ).toString('base64url');
    const content = `${header}.${claims}`;
    return (
      content +
      '.' +
      sign(null, Buffer.from(content), pair.privateKey).toString('base64url')
    );
  }
  return {
    opts: {
      hostname: '127.0.0.1',
      port,
      hostHeader: 'fixture.i.example.com',
      timeoutMs: 2000,
    },
    identity,
  };
}

test('fake WS requires a valid owner/method/path identity and valid upgrade', async (t) => {
  const { opts, identity } = await fake(t);
  for (const [label, headers] of [
    ['missing', {}],
    ['malformed', { 'Auriga-Identity': 'malformed' }],
    ['wrong-path', { 'Auriga-Identity': identity('/wrong') }],
    ['wrong-owner', { 'Auriga-Identity': identity('/ws', 'other-owner') }],
  ]) {
    await assert.rejects(
      openWebSocket({ ...opts, headers }),
      { status: 401 },
      label,
    );
  }
  await assert.rejects(
    openWebSocket({
      ...opts,
      headers: { Upgrade: 'h2c', 'Auriga-Identity': identity() },
    }),
    { status: 400 },
  );
  const response = await fetch(`http://127.0.0.1:${opts.port}/_auriga/health`);
  assert.equal(response.status, 200, 'health bypass remains unchanged');
  assert.equal((await response.json()).ready, true);
});

test('fake WS echoes bounded extended payloads and answers liveness Ping', async (t) => {
  const { opts, identity } = await fake(t);
  const ws = await openWebSocket({
    ...opts,
    headers: { 'Auriga-Identity': identity() },
  });
  t.after(() => ws.close());
  for (const length of [13, 126, 65536]) {
    const text = 'p'.repeat(length);
    ws.sendText(text);
    assert.equal(await ws.readTextFrame(), text);
  }
  assert.equal((await ws.probe()).kind, 'ping-pong');
});
