import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';

export function assertReadyReplacement(value) {
  const e = value.endpoint;
  const label = `${e.name}: P1 READY replacement`;
  assert.equal(
    e.transports.length,
    2,
    `${label}: bootstrap and normal transports`,
  );
  assert.equal(e.sockets.length, 2, `${label}: exactly two physical sockets`);
  assert.equal(e.sockets[0].readyState, 3, `${label}: bootstrap C1 is closed`);
  assert.equal(e.sockets[1].readyState, 1, `${label}: normal C2 remains open`);
  assertReadyEvidence(value);
}

export function assertReadyEvidence({
  endpoint: e,
  anchors,
  nodes,
  records,
  identity,
}) {
  const label = `${e.name}: P1 READY evidence`;
  const requests = new Map();
  for (const { direction, frame } of e.frames) {
    if (direction !== 'up' || frame.type !== 'ready') continue;
    assert.equal(
      typeof frame.request_id,
      'string',
      `${label}: request id present`,
    );
    const group = requests.get(frame.request_id) ?? [];
    group.push(frame);
    requests.set(frame.request_id, group);
  }
  assert.equal(
    requests.size,
    2,
    `${label}: two logical handshakes, retries share the original request`,
  );
  const ownAnchors = anchors.filter((v) => {
    const c = v.connection_decoded;
    return (
      c.tenant_id === identity.tenant &&
      c.realm === identity.realm &&
      c.node === identity.node
    );
  });
  assert.equal(
    ownAnchors.length,
    2,
    `${label}: two genuine retained connection anchors`,
  );
  const connections = [],
    epochs = [];
  const originals = [...requests.values()].map((group) => group[0]);
  assert.notEqual(
    originals[0].channel_nonce,
    originals[1].channel_nonce,
    `${label}: new C gets a new nonce`,
  );
  assert.notEqual(
    originals[0].boot_id,
    originals[1].boot_id,
    `${label}: new C gets a new boot id`,
  );
  for (const group of requests.values()) {
    assert.ok(
      group.length <= 5,
      `${label}: retry count stays within the production bound`,
    );
    const original = group[0];
    for (const retry of group)
      assert.deepEqual(
        retry,
        original,
        `${label}: retry preserves the entire READY frame`,
      );
    const calls = records.filter(
      (v) =>
        v.method === 'Ready' && v.request.request_id === original.request_id,
    );
    // A gateway admission refusal may precede an RPC. It cannot prove a C;
    // only an actual successful generated call plus persisted facts can.
    assert.ok(
      calls.length > 0 && calls.length <= group.length,
      `${label}: actual RPCs must correspond to captured sends`,
    );
    const success = calls.find(
      (v) =>
        v.code === 'OK' &&
        v.response?.type === 'ready_result' &&
        v.response.status === 'ready' &&
        e.frames.some(
          (f) =>
            f.direction === 'down' && isDeepStrictEqual(f.frame, v.response),
        ),
    );
    assert.ok(
      success,
      `${label}: a successful real READY_RESULT is required for each C`,
    );
    for (const call of calls) {
      assert.deepEqual(
        call.request,
        original,
        `${label}: relay retains the complete original request`,
      );
      assert.deepEqual(
        call.connection,
        success.connection,
        `${label}: a retry cannot move the original request to another C`,
      );
    }
    assert.ok(
      ownAnchors.some((v) =>
        isDeepStrictEqual(v.connection_decoded, success.connection),
      ),
      `${label}: successful C must match an actual retained anchor`,
    );
    const response = success.response;
    assert.ok(
      e.frames.some(
        (v) => v.direction === 'down' && isDeepStrictEqual(v.frame, response),
      ),
      `${label}: original READY_RESULT must reach the production plugin`,
    );
    assert.deepEqual(
      response.node_identity,
      {
        realm: identity.realm,
        node: identity.node,
        incarnation: identity.nodeIncarnation,
      },
      `${label}: READY identity must match the durable ledger`,
    );
    assert.equal(
      response.server_auth.channel_nonce,
      original.channel_nonce,
      `${label}: response uses this handshake nonce`,
    );
    assert.match(
      response.epoch,
      /^[1-9][0-9]*$/,
      `${label}: successful epoch is positive`,
    );
    connections.push(success.connection);
    epochs.push(BigInt(response.epoch));
  }
  assert.notEqual(
    connections[0].ingress,
    connections[1].ingress,
    `${label}: C2 has a genuinely new ingress`,
  );
  assert.notEqual(
    connections[0].client_id,
    connections[1].client_id,
    `${label}: C2 has a genuinely new broker client`,
  );
  assert.equal(
    epochs[1],
    epochs[0] + 1n,
    `${label}: exactly one connection replacement advances the epoch`,
  );
  const ownNodes = nodes.filter(
    (v) =>
      v.tenant_id === identity.tenant &&
      v.realm === identity.realm &&
      v.node_id === identity.node,
  );
  assert.equal(
    ownNodes.length,
    1,
    `${label}: exactly one actual collab node for this ledger`,
  );
  const node = ownNodes[0];
  assert.equal(
    node.wire_status,
    'ready',
    `${label}: collab business gate is ready`,
  );
  assert.equal(
    node.connection_epoch,
    epochs[1].toString(),
    `${label}: persisted epoch equals C2 READY epoch`,
  );
  assert.equal(
    node.wire_incarnation,
    identity.nodeIncarnation,
    `${label}: persisted incarnation equals this ledger`,
  );
  assert.equal(
    node.ingress_id,
    connections[1].ingress,
    `${label}: persisted ingress is the new physical C2`,
  );
}
