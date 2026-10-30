import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { generateKeyPairSync } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  validateConnect,
  readConnect,
  planPair,
  summarizeLedger,
} from './demo-network-send.mjs';

const key = generateKeyPairSync('ed25519')
  .publicKey.export({ type: 'spki', format: 'der' })
  .toString('base64url');
const config = () => ({
  platformOrigin: 'https://portal.example.com',
  issuer: 'https://portal.example.com',
  clientId: 'auriga-dsh',
  providerId: 'team',
  network: {
    realm: '1',
    wssUrl: 'wss://portal.example.com/net/v1/ws',
    roots: [{ kid: 'root-1', public_key_spki: key }],
  },
});
function temporary(t) {
  const base = path.resolve('.dev/plugin-dsh');
  fs.mkdirSync(base, { recursive: true });
  const dir = fs.mkdtempSync(path.join(base, 'demo-guard-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('production validators accept explicit pins and independent WSS endpoint without inferring realm', () => {
  const value = config();
  value.network.wssUrl = 'wss://network.example.com:9443/custom';
  assert.equal(validateConnect(value).network.realm, '1');
  assert.equal(validateConnect(value).network.wssUrl, value.network.wssUrl);
  assert.ok(Object.isFrozen(validateConnect(value).network.roots));
});

test('reject HTTP, mixed issuer, missing pins, unknown or credential-shaped fields', () => {
  const variants = [
    {
      platformOrigin: 'http://127.0.0.1:18080',
      issuer: 'http://127.0.0.1:18080',
      allowLocalHttp: true,
    },
    { issuer: 'https://other.example.com' },
    { network: undefined },
    { token: 'must-never-echo' },
    { platformOrigin: 'https://user:secret@portal.example.com' },
    { platformOrigin: 'https://portal.example.com/path' },
  ];
  for (const changes of variants)
    assert.throws(() => validateConnect({ ...config(), ...changes }));
  for (const changes of [
    { realm: '01' },
    { realm: '0' },
    { realm: '18446744073709551616' },
    { wssUrl: 'ws://portal.example.com' },
    { wssUrl: 'wss://portal.example.com/?token=secret' },
    { roots: [{ kid: 'root-1', public_key_spki: key + '=' }] },
    {
      roots: [
        {
          kid: 'root-1',
          public_key_spki: generateKeyPairSync('rsa', { modulusLength: 1024 })
            .publicKey.export({ type: 'spki', format: 'der' })
            .toString('base64url'),
        },
      ],
    },
    { roots: [...config().network.roots, ...config().network.roots] },
    { skipVerify: true },
  ])
    assert.throws(() =>
      validateConnect({
        ...config(),
        network: { ...config().network, ...changes },
      }),
    );
});

test('public connect file has bounded strict JSON, no symlink or secret echo', (t) => {
  const dir = temporary(t),
    file = path.join(dir, 'connect.json');
  fs.writeFileSync(file, JSON.stringify(config()));
  assert.equal(readConnect(file).clientId, 'auriga-dsh');
  for (const contents of [
    '{"clientId":"a","clientId":"b"}',
    'x'.repeat(65537),
    '{"token":"must-never-echo"}',
  ]) {
    fs.writeFileSync(file, contents);
    assert.throws(
      () => readConnect(file),
      (e) => !e.message.includes('must-never-echo'),
    );
  }
  assert.throws(() => readConnect('relative.json'));
  assert.throws(() => readConnect(dir));
});

test('public configuration rejects junction ancestors and preserves the original target', (t) => {
  const dir = temporary(t),
    target = path.join(dir, 'target'),
    link = path.join(dir, 'link');
  fs.mkdirSync(target);
  const file = path.join(target, 'connect.json'),
    bytes = JSON.stringify(config());
  fs.writeFileSync(file, bytes);
  fs.symlinkSync(
    target,
    link,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.throws(() => readConnect(path.join(link, 'connect.json')));
  fs.unlinkSync(link);
  assert.equal(fs.readFileSync(file, 'utf8'), bytes);
});

test('pair planning is read-only and rejects overlap or any existing state', (t) => {
  const dir = temporary(t),
    a = path.join(dir, 'A'),
    b = path.join(dir, 'B');
  const result = planPair({
    connectFile: path.join(dir, 'connect.json'),
    connect: validateConnect(config()),
    a,
    b,
  });
  assert.equal(result.status, 'prepared-plan');
  assert.equal(result.readiness, 'unverified');
  assert.equal(fs.existsSync(a), false);
  assert.equal(fs.existsSync(b), false);
  assert.throws(() =>
    planPair({
      connectFile: '',
      connect: config(),
      a,
      b: path.join(a, 'nested'),
    }),
  );
  fs.mkdirSync(a);
  assert.throws(() => planPair({ connectFile: '', connect: config(), a, b }));
});

test('ledger evidence keeps immutable request/result correlation and cannot claim native input count', () => {
  const raw = {
    version: 3,
    revision: 9,
    identity: { tenant: 'private' },
    bindings: {},
    durable: {
      producerEpoch: '1',
      resumeCursor: '2',
      receive: {},
      submissions: {
        k: {
          request: {
            body: { text: 'private-body' },
            idempotency: { key: 'key-1', producer_epoch: '1' },
          },
          result: '{"status":"platform_stored","message_id":"message-1"}',
        },
      },
      inbound: {
        r: {
          recipientId: 'r',
          stage: 'committed',
          hostSessionId: 'session-B',
          delivery: { message: { message_id: 'message-1' } },
          receipt: {
            results: [
              {
                recipient_id: 'r',
                host_request_id: 'r',
                status: 'host_accepted',
              },
            ],
          },
        },
      },
    },
    vault: { token: 'private-token' },
  };
  const result = summarizeLedger(raw);
  assert.equal(result.submissions[0].messageId, 'message-1');
  assert.equal(result.inbound[0].hostRequestId, 'r');
  assert.equal(result.nativeInputCount, null);
  for (const secret of ['private-body', 'private-token', '"tenant"'])
    assert.ok(!JSON.stringify(result).includes(secret));
  assert.deepEqual(
    summarizeLedger({ version: 3, revision: 0, bindings: {} }).submissions,
    [],
  );
});

test('unconfigured CLI fails closed without contacting or starting services', () => {
  const result = spawnSync(
    process.execPath,
    ['scripts/beta-ui/demo-network-send.mjs', 'preflight'],
    { encoding: 'utf8' },
  );
  assert.equal(result.status, 2);
  assert.equal(JSON.parse(result.stdout).status, 'unconfigured');
  const unknown = spawnSync(
    process.execPath,
    [
      'scripts/beta-ui/demo-network-send.mjs',
      'preflight',
      '--skip-tls',
      'true',
    ],
    { encoding: 'utf8' },
  );
  assert.equal(unknown.status, 1);
});

test('CLI preflight and pair plan produce only static unverified results and create no profiles', (t) => {
  const dir = temporary(t),
    file = path.join(dir, 'connect.json');
  fs.writeFileSync(file, JSON.stringify(config()));
  const cli = (...args) =>
    spawnSync(
      process.execPath,
      ['scripts/beta-ui/demo-network-send.mjs', ...args],
      { encoding: 'utf8' },
    );
  const preflight = cli('preflight', '--connect-config', file);
  assert.equal(preflight.status, 0, preflight.stderr);
  assert.equal(JSON.parse(preflight.stdout).readiness, 'unverified');
  const a = path.join(dir, 'A'),
    b = path.join(dir, 'B');
  const plan = cli(
    'plan',
    '--connect-config',
    file,
    '--state-a',
    a,
    '--state-b',
    b,
  );
  assert.equal(plan.status, 0, plan.stderr);
  assert.equal(JSON.parse(plan.stdout).commands.length, 2);
  assert.deepEqual(fs.readdirSync(dir), ['connect.json']);
  const missing = cli('evidence', '--state', a);
  assert.equal(missing.status, 1);
  assert.equal(missing.stdout, '');
  // Synthetic disk snapshot only: no registration, login, host or backend.
  const host = path.join(a, 'dsh/profiles/web/auriga-connect/network/host');
  fs.mkdirSync(host, { recursive: true });
  fs.writeFileSync(
    path.join(a, 'plugin-dsh.json'),
    JSON.stringify({ kind: 'auriga-beta-plugin-dsh', schema: 1, state: a }),
  );
  const ledger = path.join(host, 'ledger.json');
  const bytes = JSON.stringify({
    version: 3,
    revision: 0,
    token: 'private-test-sentinel',
  });
  fs.writeFileSync(ledger, bytes);
  const evidence = cli('evidence', '--state', a);
  assert.equal(evidence.status, 0, evidence.stderr);
  assert.equal(JSON.parse(evidence.stdout).nativeInputCount, null);
  assert.ok(!evidence.stdout.includes('private-test-sentinel'));
  assert.equal(fs.readFileSync(ledger, 'utf8'), bytes);
  fs.writeFileSync(path.join(a, 'plugin-dsh.json'), '{}');
  assert.equal(cli('evidence', '--state', a).status, 1);
});
