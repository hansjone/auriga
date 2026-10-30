import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { childProcess, stopChild } from './support/assembly-process.mjs';
import {
  prepareDevice,
  assemblyDeviceProvider,
} from './support/assembly-device.mjs';
import { createDeviceProvider } from '../../clients/dsh-plugin/src/network/device-provider.js';
import { testRoots } from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';
import { platformConfig } from '../../clients/dsh-plugin/src/local/platform.js';
import {
  signatureBypass,
  assertVerdict,
  failureMarker,
} from './support/assembly-verdict.mjs';

test('split-origin assembly prepares both production device providers without changing the IAM token', async (t) => {
  const iamOrigin = 'https://platform.example.com:43111';
  const submitOrigin = 'https://platform.example.com:43112';
  const signal = new AbortController().signal;
  const network = {
    realm: '2',
    wssUrl: 'wss://broker.example.com',
    roots: testRoots,
  };
  const credential = {
    accessToken: 'fixture-original-iam-token',
    claims: { tid: 'tenant', did: 'device', sub: 'owner', iss: iamOrigin },
  };
  const original = JSON.stringify(credential);
  const getDevice = async (active) => {
    assert.equal(active, signal);
    return credential;
  };
  const requests = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url: url.href, options });
    return new Response('{"fixture":true}');
  });
  const iam = await prepareDevice(
    assemblyDeviceProvider({ origin: iamOrigin, network, getDevice }),
    signal,
  );
  const sending = await prepareDevice(
    assemblyDeviceProvider({ origin: submitOrigin, network, getDevice }),
    signal,
  );
  assert.equal(iam.kind, 'device-connection');
  assert.equal(sending.kind, 'device-connection');
  const request = {
    kind: 'message',
    source: { source_binding: 'source-1' },
    idempotency: { producer_epoch: '42', key: 'original-key' },
  };
  await sending.submit(request, signal);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, submitOrigin + '/api/ai/v1/network/submit');
  assert.equal(
    requests[0].options.headers.authorization,
    'Bearer ' + credential.accessToken,
  );
  assert.equal(requests[0].options.body, JSON.stringify(request));
  assert.equal(requests[0].options.redirect, 'error');
  assert.equal(JSON.stringify(credential), original);
});

test('platform issuer stays restricted to the exact scheme, host and port', () => {
  const config = {
    platformOrigin: 'https://platform.example.com:43111',
    issuer: 'https://platform.example.com:43111/realm',
    clientId: 'fixture',
    providerId: 'fixture',
  };
  assert.equal(platformConfig(config).issuer, config.issuer);
  for (const issuer of [
    'https://platform.example.com:43112',
    'https://other.example.com:43111',
    'http://platform.example.com:43111',
    'https://user@platform.example.com:43111',
    'https://platform.example.com:43111#fragment',
  ])
    assert.throws(
      () => platformConfig({ ...config, issuer }),
      /issuer must belong to platform origin/,
    );
  assert.throws(
    () =>
      createDeviceProvider({
        config: {
          ...config,
          platformOrigin: 'https://platform.example.com:43112',
        },
        getDevice: async () => ({
          accessToken: 'unused',
          claims: { tid: 'tenant', did: 'device', sub: 'owner' },
        }),
      }),
    /issuer must belong to platform origin/,
  );
});

test('driver prepares the real provider using its options API and preserves abort; no network acceptance', async () => {
  const controller = new AbortController();
  const provider = createDeviceProvider({
    config: {
      platformOrigin: 'https://platform.example.com',
      issuer: 'https://platform.example.com',
      clientId: 'fixture',
      providerId: 'fixture',
      network: {
        realm: '2',
        wssUrl: 'wss://broker.example.com',
        roots: testRoots,
      },
    },
    async getDevice(signal) {
      assert.equal(signal, controller.signal);
      return {
        accessToken: 'fixture-not-authenticated',
        claims: { tid: 'tenant', did: 'device', sub: 'owner' },
      };
    },
  });
  const prepared = await prepareDevice(provider, controller.signal);
  assert.equal(prepared.kind, 'device-connection');
  assert.equal(prepared.url, 'wss://broker.example.com');
  controller.abort();
  await assert.rejects(prepareDevice(provider, controller.signal), {
    name: 'AbortError',
  });
});

test('assembly child channel matches replies, stops and waits for its exact child', async () => {
  const child = childProcess(process.execPath, [
    '-e',
    `const r=require('node:readline').createInterface({input:process.stdin});r.on('line',line=>{if(line==='stop'){r.close();return;}const q=JSON.parse(line);console.log(JSON.stringify({stage:'reply',id:q.id,answer:42}));});`,
  ]);
  try {
    assert.equal((await child.request({ op: 'probe' })).answer, 42);
  } finally {
    const stages = [];
    assert.equal((await stopChild('gateway', child, 'stop', stages)).code, 0);
    assert.deepEqual(stages, [
      { stage: 'gateway-closed', exitCode: 0, signal: null },
    ]);
  }
});

test('failed owned child retains its actual closed exit status', async () => {
  const stages = [];
  const child = childProcess(process.execPath, [
    '-e',
    `require('node:readline').createInterface({input:process.stdin}).once('line',()=>process.exit(7));`,
  ]);
  assert.equal(child.exit, undefined);
  await assert.rejects(
    stopChild('gateway', child, 'stop', stages),
    /owned child exited 7\/null/,
  );
  assert.deepEqual(child.exit, { code: 7, signal: null });
  assert.deepEqual(stages, [
    { stage: 'gateway-closed', exitCode: 7, signal: null },
  ]);
});

test('business mutant changes one production Get signature check and keeps other verifications', async () => {
  const source = await readFile(
    new URL(
      '../../clients/dsh-plugin/src/network/source-control/connection.js',
      import.meta.url,
    ),
    'utf8',
  );
  const changed = signatureBypass(source);
  assert.ok(
    changed.includes(
      "if (result.type !== 'source_get_result') await verifyEnvelopeWithRefresh",
    ),
  );
  assert.ok(changed.includes('assertSourceResult(result, request, now())'));
  assert.throws(() => signatureBypass(''));
  assert.throws(() => signatureBypass(source + source));
});

const stages = ['ready', 'gateway-ready', 'gateway-closed', 'java-closed'].map(
  (stage) => ({ stage, exitCode: 0, signal: null }),
);
const base = () => ({
  brokerVersion: '2.15.1',
  head: 'a'.repeat(40),
  stages,
  checks: [],
  failures: [],
  completed: false,
});
const mutant = () => ({
  ...base(),
  businessFailure: {
    name: 'AssertionError',
    code: 'ERR_ASSERTION',
    message: failureMarker,
  },
  failures: [{ message: 'plugin matrix failed: ' + failureMarker }],
});

test('verdict requires every control business case and successful cleanup', () => {
  const valid = {
    ...base(),
    completed: true,
    checks: [
      'P1:',
      'P2/P4/F2:',
      'F6:',
      'MEMBER',
      'P3:',
      'P5/P6:',
      'Broker 2.15.1:',
      'critical: live-C',
      'critical: real deadline',
      'critical: actual JDBC',
      'critical: genuine late',
      'critical: real Org',
    ],
  };
  assertVerdict(valid, 'Control', 0);
  assert.throws(() => assertVerdict(valid, 'Control', 1));
  assert.throws(() => assertVerdict({ ...valid, checks: [] }, 'Control', 0));
  assert.throws(() => assertVerdict({ ...valid, stages: [] }, 'Control', 0));
});

test('verdict refuses initialization/SQL/compile errors and a green mutant', () => {
  assertVerdict(mutant(), 'SignatureBypass', 1);
  for (const name of ['SQLException', 'Error', 'SyntaxError']) {
    const value = mutant();
    value.businessFailure.name = name;
    assert.throws(() => assertVerdict(value, 'SignatureBypass', 1));
  }
  assert.throws(() => assertVerdict(mutant(), 'SignatureBypass', 0));
  assert.throws(() =>
    assertVerdict(
      { ...mutant(), brokerVersion: '2.12.6' },
      'SignatureBypass',
      1,
    ),
  );
  assert.throws(() =>
    assertVerdict(
      { ...mutant(), failures: [{ message: 'own PG survived cleanup' }] },
      'SignatureBypass',
      1,
    ),
  );
  for (const stage of ['gateway-closed', 'java-closed'])
    assert.throws(() =>
      assertVerdict(
        {
          ...mutant(),
          stages: stages.map((v) => ({
            ...v,
            exitCode: v.stage === stage ? 1 : 0,
            signal: null,
          })),
          failures: [
            ...mutant().failures,
            { message: 'owned child exited 1/null' },
          ],
        },
        'SignatureBypass',
        1,
      ),
    );
});
