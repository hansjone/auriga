import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { childProcess } from './support/assembly-process.mjs';
import { prepareDevice } from './support/assembly-device.mjs';
import { createDeviceProvider } from '../../clients/dsh-plugin/src/network/device-provider.js';
import { testRoots } from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';
import {
  signatureBypass,
  assertVerdict,
  failureMarker,
} from './support/assembly-verdict.mjs';

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
    assert.equal((await child.stop()).code, 0);
  }
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
  (stage) => ({ stage }),
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
});
