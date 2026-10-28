// SEND-2 transport acceptance only. No host prompt, receipt proof or model call.
/* global AbortSignal */
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { postSubmit } from './gateway-submit.mjs';
import { createFileLedgerStore } from '../../../clients/dsh-plugin/src/network/host/store.js';
import { requestAssemblyMailbox } from './assembly-mailbox.mjs';

const rows = (db, name) => db[`collab.${name}`];
const count = (db) =>
  Object.fromEntries(
    [
      'network_submit',
      'network_message',
      'network_mail_item',
      'network_recipient',
    ].map((name) => [name, rows(db, name).length]),
  );

export async function gatewaySend({
  member,
  external,
  config,
  gateway,
  control,
  snapshot,
  checks,
  artifacts,
}) {
  const state = async (e) => {
    const ledger = await e.ledger();
    const ready = e.frames.findLast(
      (v) => v.direction === 'down' && v.frame.type === 'ready_result',
    ).frame;
    const boot = e.frames.findLast(
      (v) => v.direction === 'up' && v.frame.type === 'ready',
    ).frame.boot_id;
    return { ledger, ready, boot, identity: ledger.identity };
  };
  const frame = async (e, type, fields, expected) => {
    const s = await state(e),
      request_id = randomUUID();
    const cache = await createFileLedgerStore({
      dir: join(e.directory, 'send-probe-trust'),
    }).open();
    let result;
    try {
      result = {
        frame: await requestAssemblyMailbox({
          prepared: e.prepared,
          cache,
          transport: e.mailboxTransports.at(-1),
          identity: s.identity,
          ready: {
            epoch: s.ready.epoch,
            bootId: s.boot,
            channelNonce: s.ready.server_auth.channel_nonce,
          },
          frame: { app_version: '1.0', type, request_id, ...fields },
          expected,
          signal: AbortSignal.timeout(30_000),
        }),
      };
    } finally {
      await cache.close();
    }
    assert.equal(
      result.frame.type,
      expected,
      `SEND-2 ${type}: ${JSON.stringify(result.frame)}`,
    );
    const signed = result.frame.page ?? result.frame;
    assert.equal(signed.server_auth.audience.node, s.identity.node);
    assert.equal(signed.server_auth.audience.epoch, s.ready.epoch);
    assert.equal(
      signed.server_auth.channel_nonce,
      s.ready.server_auth.channel_nonce,
    );
    return result.frame;
  };
  const source = await state(member),
    target = await state(external);
  const registration =
    target.ledger.sourceControl.entries['1'].observation.source.registration;
  const receive = await frame(
    external,
    'receive_set',
    {
      epoch: target.ready.epoch,
      boot_id: target.boot,
      registration,
      enabled: true,
      expected_version: '1',
    },
    'receive_set_result',
  );
  assert.equal(receive.enabled, true);
  assert.equal(receive.state_version, '2');

  const device = JSON.parse(await readFile(config.device, 'utf8'));
  const otherDevice = JSON.parse(await readFile(config.externalDevice, 'utf8'));
  const post = (body, token = device.access_token, headers = {}) =>
    postSubmit(gateway, body, token, headers);
  const request = {
    kind: 'message',
    intent: 'information',
    durability: 'durable',
    dispatch_mode: 'atomic',
    ttl: 60,
    to: {
      version: 1,
      realm: target.identity.realm,
      node: target.identity.node,
      local: registration.local,
      type: 'session',
    },
    body: {
      subtype: 'text',
      text: 'SEND-2 HTTPS durable gateway acceptance',
      optional: { preserved: true },
    },
    source: { source_binding: source.ledger.sourceControl.entries['1'].head },
    idempotency: {
      producer_epoch: source.ready.producer_epoch,
      key: randomUUID(),
    },
  };
  assert.match(request.idempotency.producer_epoch, /^[1-9][0-9]*$/);
  const before = await snapshot('send-before');
  const accepted = await post(request);
  assert.equal(
    accepted.status,
    200,
    `SEND-2 real submit failed: ${accepted.body}`,
  );
  const result = JSON.parse(accepted.body);
  assert.equal(result.status, 'platform_stored');
  assert.equal(result.from.node, source.identity.node);
  assert.equal(result.to.node, target.identity.node);
  const replay = await post(request);
  assert.deepEqual(
    replay,
    accepted,
    'SEND-2 same key must recover exact original result bytes',
  );
  const saved = await snapshot('send-stored');
  for (const name of Object.keys(count(before)))
    assert.equal(
      count(saved)[name],
      count(before)[name] + 1,
      `SEND-2 single durable ${name}`,
    );
  const fetched = await frame(
    external,
    'fetch',
    {
      epoch: target.ready.epoch,
      resume_cursor: '0',
      continuation: null,
      max_items: 1,
      max_bytes: 65536,
      held_ids: [],
      paused: [],
      state_barrier: [],
    },
    'fetch_result',
  );
  const item = fetched.items.find(
    (v) => v.type === 'delivery' && v.message.message_id === result.message_id,
  );
  assert.ok(item, 'SEND-2 HTTPS message must reach target FETCH');
  assert.deepEqual(JSON.parse(JSON.stringify(item.message.body)), request.body);
  const recipient = item.recipients[0];
  assert.match(recipient.state_version, /^[1-9][0-9]*$/);
  const attempt_id = randomUUID();
  const attempt = await frame(
    external,
    'attempt_begin',
    {
      epoch: target.ready.epoch,
      boot_id: target.boot,
      attempt_id,
      delivery_id: item.delivery_id,
      recipient_id: recipient.recipient_id,
      purpose: 'process',
    },
    'attempt_result',
  );
  assert.equal(attempt.result, 'entered');
  const admitted = await frame(
    external,
    'execution_admit',
    {
      connection_epoch: target.ready.epoch,
      boot_id: target.boot,
      attempt_id,
      recipient_id: recipient.recipient_id,
      host_request_id: recipient.recipient_id,
      target: request.to,
    },
    'admit_result',
  );
  assert.equal(admitted.recipient_id, recipient.recipient_id);
  assert.equal(admitted.result, 'admitted');
  assert.match(admitted.state_version, /^[1-9][0-9]*$/);
  // No RECEIPT: a transport driver has no genuine host durability evidence.
  checks.push(
    'SEND-2: real device HTTPS -> gateway -> collab -> PG -> target WSS FETCH, same-key original result, receive/attempt/admit server versions',
  );

  const records = (await control({ op: 'trace' })).records;
  const connection = records.findLast(
    (v) =>
      v.method === 'Ready' &&
      v.code === 'OK' &&
      v.connection.node === source.identity.node,
  ).connection;
  // An authenticated OTHER device asks to impersonate the source's full tuple.
  // Correct gateway rejects the claim before RPC; collab cannot fix a gateway
  // that delegates a real victim tuple as if it had authenticated that victim.
  const attack = {
    ...request,
    thread_id: randomUUID(),
    idempotency: { ...request.idempotency, key: randomUUID() },
    tenant: connection.tenant_id,
    node: connection.node,
    connection,
  };
  const crossBefore = await snapshot('send-cross-before');
  const cross = await post(attack, otherDevice.access_token);
  const crossAfter = await snapshot('send-cross-after');
  const evidence = {
    accepted: result,
    replayIdentical: accepted.body === replay.body,
    storedCounts: count(saved),
    message: item.message.message_id,
    recipient: recipient.recipient_id,
    cross: {
      status: cross.status,
      before: count(crossBefore),
      after: count(crossAfter),
    },
  };
  await writeFile(
    join(artifacts, 'gateway-send.json'),
    JSON.stringify(evidence, null, 2),
  );
  assert.deepEqual(
    count(crossAfter),
    count(crossBefore),
    'SEND-2 cross-identity zero writes',
  );
  assert.equal(
    cross.status,
    400,
    'SEND-2 client identity claims must be rejected',
  );
  const wrongSource = await post(
    { ...request, idempotency: { ...request.idempotency, key: randomUUID() } },
    otherDevice.access_token,
  );
  assert.notEqual(
    wrongSource.status,
    200,
    'SEND-2 foreign source_binding is only a reference',
  );
  assert.deepEqual(
    count(await snapshot('send-foreign-binding')),
    count(crossAfter),
    'SEND-2 cross-identity zero writes',
  );
  checks.push(
    'SEND-2: cross-identity zero writes for forged tenant/node/connection and foreign source binding',
  );
}
