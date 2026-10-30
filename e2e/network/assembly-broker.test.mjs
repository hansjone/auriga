import test from 'node:test';
import assert from 'node:assert/strict';
import {
  payloadCloseEvidence,
  probePayload,
} from './support/assembly-broker.mjs';

const peer = () => ({
  initialInfo: {
    version: '2.15.1',
    server_id: 'N' + 'A'.repeat(55),
    client_id: 21,
  },
  info: {
    version: '2.15.1',
    server_id: 'N' + 'A'.repeat(55),
    client_id: 21,
    connect_info: true,
    remote_account: 'CLIENTS',
    max_payload: 65536,
  },
  localPort: 40001,
  closed: { code: 1006, reason: '' },
  lines: [],
});

test('payload probe sends the full exact boundary and oversize bodies and needs server-correlated closure', async () => {
  const session = peer(),
    writes = [];
  session.closed = null;
  session.lines.push('PONG');
  session.send = (value) => {
    writes.push(value);
    if (writes.length === 1) session.lines.push('PONG');
    else session.closed = { code: 1006, reason: '' };
  };
  const result = await probePayload(session, 'probe.up', async (state) =>
    payloadCloseEvidence(state, log(), 1234),
  );
  assert.deepEqual(writes, [
    `PUB probe.up 65536\r\n${'x'.repeat(65536)}\r\nPING\r\n`,
    `PUB probe.up 65537\r\n${'x'.repeat(65537)}\r\n`,
  ]);
  assert.equal(result.boundaryBytes, 65536);
  assert.equal(result.actualBytes, 65537);
  assert.equal(result.errorFrameObserved, false);
});
const log = () =>
  `[1234] 2026/10/28 00:00:00.000001 [INF]   ID:       N${'A'.repeat(55)}\n[1234] 2026/10/28 00:00:01.000001 [ERR] 127.0.0.1:40001 - wid:21 - "v1:test" - "CLIENTS/user:probe" - maximum payload exceeded: 65537 vs 65536\n`;

test('full payload WSS reset needs exact own Broker, CID, local port, account and size evidence', () => {
  const result = payloadCloseEvidence(peer(), log(), 1234);
  assert.equal(result.clientId, 21);
  assert.equal(result.actualBytes, 65537);
  assert.equal(result.limitBytes, 65536);
  assert.equal(result.account, 'CLIENTS');
  assert.equal(result.errorFrameObserved, false);
  assert.ok(!JSON.stringify(result).includes('user:probe'));
  const framed = peer();
  framed.lines.push("-ERR 'Maximum Payload Violation'");
  framed.closed = { code: 1009, reason: 'Maximum Message Payload Exceeded' };
  assert.equal(
    payloadCloseEvidence(framed, log(), 1234).errorFrameObserved,
    true,
  );
});

test('disconnect, timeout, protocol error, wrong limits or unrelated Broker records never prove the limit', () => {
  assert.equal(payloadCloseEvidence(peer(), '', 1234), null);
  for (const changed of [
    log().replace('wid:21', 'wid:22'),
    log().replace(':40001 ', ':40002 '),
    log().replace('CLIENTS/user:probe', 'SVC/user:probe'),
    log().replace('65537 vs 65536', '65537 vs 32768'),
    log().replace('65537 vs 65536', '65538 vs 65536'),
    log().replace(
      'maximum payload exceeded: 65537 vs 65536',
      'read error: EOF',
    ),
    log().replace('[1234]', '[1235]'),
    log().replace(/ID:\s+N/, 'ID: X'),
  ]) {
    assert.notEqual(changed, log(), 'negative log must actually change');
    assert.equal(payloadCloseEvidence(peer(), changed, 1234), null);
  }
  for (const change of [
    (p) => {
      p.closed = null;
    },
    (p) => {
      p.closed = { code: 1000, reason: '' };
    },
    (p) => {
      p.closed = { code: 1002, reason: 'Protocol Error' };
    },
    (p) => {
      p.closed = { code: 1009, reason: 'other limit' };
    },
    (p) => {
      p.lines.push("-ERR 'Parser Error'");
    },
    (p) => {
      p.info.remote_account = 'SVC';
    },
    (p) => {
      p.info.max_payload = 1048576;
    },
    (p) => {
      p.info.client_id++;
    },
    (p) => {
      p.info.server_id = 'N' + 'B'.repeat(55);
    },
    (p) => {
      p.info.connect_info = false;
    },
    (p) => {
      p.initialInfo.version = '2.12.6';
    },
    (p) => {
      p.localPort = undefined;
    },
  ]) {
    const value = peer();
    change(value);
    assert.equal(payloadCloseEvidence(value, log(), 1234), null);
  }
});
