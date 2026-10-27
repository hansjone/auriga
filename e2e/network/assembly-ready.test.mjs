import test from 'node:test';
import assert from 'node:assert/strict';
import { assertReadyReplacement } from './support/assembly-ready.mjs';

// Evidence-verifier fixtures only: no authentication or real chain acceptance.
function evidence(retry = true) {
  const identity = {
    tenant: 'tenant',
    realm: '2',
    node: '1',
    nodeIncarnation: 'incarnation',
  };
  const connections = [1, 2].map((i) => ({
    tenant_id: identity.tenant,
    realm: identity.realm,
    node: identity.node,
    ingress: `ingress-${i}`,
    server_id: 'server',
    client_id: String(i),
    client_account: 'CLIENTS',
  }));
  const requests = [1, 2].map((i) => ({
    type: 'ready',
    request_id: `request-${i}`,
    channel_nonce: `nonce-${i}`,
    boot_id: `boot-${i}`,
  }));
  const responses = requests.map((r, i) => ({
    type: 'ready_result',
    status: 'ready',
    request_id: r.request_id,
    epoch: String(i + 1),
    node_identity: {
      realm: identity.realm,
      node: identity.node,
      incarnation: identity.nodeIncarnation,
    },
    server_auth: { channel_nonce: r.channel_nonce },
  }));
  const frames = requests.flatMap((frame, i) => [
    { direction: 'up', frame },
    ...(retry && i === 0
      ? [{ direction: 'up', frame: structuredClone(frame) }]
      : []),
    { direction: 'down', frame: responses[i] },
  ]);
  const records = requests.map((r, i) => ({
    method: 'Ready',
    code: 'OK',
    connection: connections[i],
    request: r,
    response: responses[i],
  }));
  if (retry)
    records.unshift({ ...records[0], code: 'Canceled', response: undefined });
  return {
    endpoint: {
      name: 'member',
      frames,
      sockets: [{ readyState: 3 }, { readyState: 1 }],
      transports: [{}, {}],
    },
    anchors: connections.map((c) => ({ connection_decoded: c })),
    nodes: [
      {
        tenant_id: identity.tenant,
        realm: identity.realm,
        node_id: identity.node,
        wire_status: 'ready',
        connection_epoch: '2',
        wire_incarnation: identity.nodeIncarnation,
        ingress_id: connections[1].ingress,
      },
    ],
    records,
    identity,
  };
}

test('P1 accepts two real logical connections when bootstrap READY is identically retried', () => {
  assertReadyReplacement(evidence());
  assertReadyReplacement(evidence(false));
  const admissionRefusal = evidence();
  admissionRefusal.records.shift();
  assertReadyReplacement(admissionRefusal);
});

test('P1 refuses changed retry, replay on another C, missing commit, missing success and wrong ledger', () => {
  for (const change of [
    (v) => {
      v.endpoint.frames[1].frame.boot_id = 'changed';
    },
    (v) => {
      v.records[0].connection = v.records[2].connection;
    },
    (v) => {
      v.anchors.pop();
    },
    (v) => {
      v.records[2].code = 'Canceled';
    },
    (v) => {
      v.identity.nodeIncarnation = 'other';
    },
    (v) => {
      v.endpoint.sockets[0].readyState = 1;
    },
    (v) => {
      v.nodes[0].connection_epoch = '1';
    },
    (v) => {
      v.nodes[0].ingress_id = 'old-ingress';
    },
    (v) => {
      v.endpoint.frames.unshift(
        ...Array.from({ length: 5 }, () =>
          structuredClone(v.endpoint.frames[0]),
        ),
      );
    },
  ]) {
    const value = evidence();
    change(value);
    assert.throws(() => assertReadyReplacement(value));
  }
});
