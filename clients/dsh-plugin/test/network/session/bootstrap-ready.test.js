/* global setImmediate */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { getEventListeners } from 'node:events';
import { bootstrapReady } from '../../../src/network/session/bootstrap-ready.js';
import { decodeJson, encodeJson } from '../../../src/network/session/frames.js';
import { signingInput } from '../../../src/network/trust/verifier.js';
import { openTrustStore } from '../../../src/network/trust/store.js';
import { signedKeyset, testRoots } from '../trust/keyset-fixtures.js';

const registration = Object.freeze({
  tenant: 'tenant-1',
  realm: '2',
  node: '64',
  device: 'device-1',
  created: true,
});
const clock = () => Date.parse('2026-10-25T00:01:00Z');
const tick = () => new Promise((resolve) => setImmediate(resolve));
function deferred() {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function fixture({ refresh, now = clock } = {}) {
  const pair = generateKeyPairSync('ed25519');
  const config = {
    schema_version: 1,
    realm: '2',
    keyset_version: '5',
    issued_at: '2026-10-25T00:00:00Z',
    valid_until: '2026-10-26T00:00:00Z',
    keys: [
      {
        kid: 'response',
        purpose: 'protocol_response',
        alg: 'Ed25519',
        public_key_spki: pair.publicKey
          .export({ format: 'der', type: 'spki' })
          .toString('base64url'),
        state: 'active',
        not_before: '2026-10-24T00:00:00Z',
        not_after: '2026-10-27T00:00:00Z',
      },
    ],
  };
  const trust = await openTrustStore({
    roots: testRoots,
    config: signedKeyset(config),
    now,
    refresh,
    cache: { async load() {}, async commit() {} },
  });
  const controller = new AbortController();
  const listeners = new Set();
  const sent = [];
  const transport = {
    epoch: 41,
    state: 'ready',
    async send(bytes) {
      sent.push(decodeJson(bytes));
    },
    onMessage(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    async stop() {
      assert.fail('helper must not stop caller transport');
    },
  };
  const options = {
    registration,
    transport,
    trust,
    signal: controller.signal,
    now,
    timeoutMs: 1000,
  };
  function result(request = sent.at(-1)) {
    return {
      app_version: '1.0',
      type: 'ready_result',
      request_id: request.request_id,
      status: 'ready',
      epoch: '7',
      node_identity: { realm: '2', node: '64', incarnation: 'inc-new' },
      head_seq: '0',
      committed_cursor: '0',
      retention_low_seq: '0',
      local_high_water: '0',
      registration_revision: '0',
      seen_checkpoint: null,
      required_features: [],
      server_auth: {
        version: 1,
        alg: 'Ed25519',
        purpose: 'protocol_response',
        kid: 'response',
        keyset_version: '5',
        issuer_realm: '2',
        issued_at: '2026-10-25T00:00:00Z',
        not_after: '2026-10-25T00:05:00Z',
        request_id: request.request_id,
        channel_nonce: request.channel_nonce,
        audience: {
          realm: '2',
          node: '64',
          incarnation: 'inc-new',
          epoch: '7',
        },
      },
    };
  }
  function seal(value, key = pair.privateKey) {
    value.server_auth.signature = sign(null, signingInput(value), key).toString(
      'base64url',
    );
    return value;
  }
  function emit(value, epoch = transport.epoch) {
    for (const listener of [...listeners]) listener(encodeJson(value), epoch);
  }
  function clean() {
    assert.equal(listeners.size, 0);
    assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
    assert.ok(sent.every((r) => r.type === 'ready'));
    assert.equal(transport.ready, undefined);
  }
  return {
    ...options,
    options,
    config,
    controller,
    listeners,
    sent,
    result,
    seal,
    emit,
    clean,
  };
}

test('root-approved READY returns deeply immutable identity without business effects', async () => {
  const f = await fixture();
  const pending = bootstrapReady(f.options);
  assert.equal(f.sent.length, 1);
  const request = f.sent[0];
  assert.equal(request.keyset_version, '5');
  for (const field of [
    'resume_cursor',
    'local_high_water',
    'registration_revision',
  ])
    assert.equal(request[field], '0');
  assert.equal(request.pending_registration, null);
  assert.deepEqual(
    { ...request.receipt_checkpoint },
    {
      checkpoint_seq: '0',
      checkpoint_revision: '0',
      summary_digest: 'sha256:' + '0'.repeat(64),
    },
  );
  const raw = f.result();
  raw.app_version = '1.12';
  raw.future = { values: ['signed optional extension'] };
  f.emit(f.seal(raw));
  const output = await pending;
  assert.deepEqual(output.identity, {
    tenant: 'tenant-1',
    realm: '2',
    node: '64',
    nodeIncarnation: 'inc-new',
  });
  assert.equal(output.readyResult.epoch, '7');
  assert.notEqual(output.readyResult.epoch, String(f.transport.epoch));
  assert.ok(Object.isFrozen(output) && Object.isFrozen(output.identity));
  assert.ok(Object.isFrozen(output.readyResult.future.values));
  assert.throws(() => {
    output.identity.node = '65';
  }, TypeError);
  f.emit(f.seal(f.result()));
  f.clean();
});

const signature = 'network.signature_invalid';
const protocol = 'network.protocol_error';
const unavailable = 'network.key_unavailable';
const unsupported = 'network.unsupported_app_version';
const cases = [];
function add(name, path, value, code = signature, afterSeal = false) {
  cases.push({ name, path, value, code, afterSeal });
}
for (const [path, value] of [
  ['head_seq', '1'],
  ['future', 'injected'],
  ['node_identity.incarnation', 'evil'],
  ['epoch', '8'],
])
  add('tamper ' + path, path, value, signature, true);
for (const [path, value] of [
  ['server_auth.audience.realm', '3'],
  ['server_auth.audience.node', '65'],
  ['server_auth.audience.incarnation', 'other'],
  ['server_auth.audience.epoch', '8'],
  ['server_auth.request_id', 'other'],
  ['server_auth.channel_nonce', 'other'],
  ['server_auth.issuer_realm', '3'],
  ['server_auth.purpose', 'business_delivery'],
  ['server_auth.not_after', '2026-10-25T00:00:30Z'],
])
  add(path, path, value);
add('signed identity realm', 'node_identity.realm', '3', protocol);
add('signed identity node', 'node_identity.node', '65', protocol);
add(
  'missing signature',
  'server_auth',
  undefined,
  'network.signature_required',
  true,
);
add('unknown key', 'server_auth.kid', 'unknown', unavailable);
add('new keyset unavailable', 'server_auth.keyset_version', '6', unavailable);
add('unsupported major', 'app_version', '2.0', unsupported);
add(
  'required unknown feature',
  'required_features',
  ['future_v1'],
  unsupported,
);
add(
  'duplicate feature',
  'required_features',
  ['server_auth_v1', 'server_auth_v1'],
  protocol,
);
add('invalid checkpoint', 'seen_checkpoint', { checkpoint_seq: '0' }, protocol);
add('invalid feature', 'required_features', [1], protocol);
for (const field of [
  'head_seq',
  'committed_cursor',
  'retention_low_seq',
  'local_high_water',
  'registration_revision',
  'seen_checkpoint',
  'required_features',
  'node_identity',
  'epoch',
  'status',
  'app_version',
  'request_id',
])
  add(
    'missing ' + field,
    field,
    undefined,
    field === 'app_version' ? unsupported : protocol,
  );
for (const value of ['0', '01', '-1', '18446744073709551616', 7, '', '1e2'])
  add('bad epoch ' + JSON.stringify(value), 'epoch', value, protocol);
for (const value of ['', 'x'.repeat(129), 'bad incarnation'])
  add(
    'bad incarnation ' + value.length,
    'node_identity.incarnation',
    value,
    protocol,
  );
for (const field of [
  'head_seq',
  'committed_cursor',
  'retention_low_seq',
  'local_high_water',
  'registration_revision',
])
  add('bad uint64 ' + field, field, '00', protocol);
for (const status of [
  'reconciling',
  'recovery_conflict',
  'identity_rollback',
  'unknown',
])
  add(status, 'status', status, 'ready_failed');
for (const type of ['error', 'head', 'register_result'])
  add('non READY ' + type, 'type', type, 'ready_failed');
for (const { name, path, value, code, afterSeal } of cases) {
  test('rejects ' + name, async () => {
    const f = await fixture();
    const pending = bootstrapReady(f.options);
    const r = f.result();
    if (afterSeal) f.seal(r);
    const keys = path.split('.');
    const parent = keys.slice(0, -1).reduce((object, key) => object[key], r);
    if (value === undefined) delete parent[keys.at(-1)];
    else parent[keys.at(-1)] = value;
    if (!afterSeal) f.seal(r);
    f.emit(r);
    await assert.rejects(pending, { code });
    f.clean();
  });
}

test('known nonce but another legitimate node cannot bootstrap identity', async () => {
  const f = await fixture();
  const pending = bootstrapReady(f.options);
  const r = f.result();
  r.node_identity.node = r.server_auth.audience.node = '65';
  f.emit(f.seal(r));
  await assert.rejects(pending, { code: signature });
  f.clean();
});

test('another private key cannot authenticate READY', async () => {
  const f = await fixture();
  const pending = bootstrapReady(f.options);
  f.emit(f.seal(f.result(), generateKeyPairSync('ed25519').privateKey));
  await assert.rejects(pending, { code: 'network.signature_invalid' });
  f.clean();
});

for (const change of ['revoked', 'expired', 'wrong root']) {
  test(`rejects ${change} trust`, async () => {
    const f = await fixture();
    if (change === 'wrong root') {
      const other = generateKeyPairSync('ed25519');
      await assert.rejects(
        openTrustStore({
          roots: [
            {
              kid: testRoots[0].kid,
              public_key_spki: other.publicKey
                .export({ format: 'der', type: 'spki' })
                .toString('base64url'),
            },
          ],
          config: signedKeyset(f.config),
          cache: { async load() {}, async commit() {} },
          now: clock,
        }),
        { code: 'network.key_unavailable' },
      );
    } else {
      f.config.keyset_version = '6';
      if (change === 'revoked') f.config.keys[0].state = 'revoked';
      else f.config.valid_until = '2026-10-25T00:00:30Z';
      await f.trust.update(signedKeyset(f.config));
      const pending = bootstrapReady(f.options);
      f.emit(f.seal(f.result()));
      await assert.rejects(pending, { code: 'network.key_unavailable' });
    }
    f.clean();
  });
}

test('unknown key refresh failure is closed', async () => {
  const f = await fixture({
    refresh: async () => {
      throw new Error('offline');
    },
  });
  const pending = bootstrapReady(f.options);
  const r = f.result();
  r.server_auth.kid = 'new-key';
  f.emit(f.seal(r));
  await assert.rejects(pending, { code: 'network.key_unavailable' });
  assert.equal(f.trust.diagnostics.refreshFailures, 1);
  f.clean();
});

test('root-approved refresh succeeds and preserves captured request keyset version', async () => {
  const f = await fixture({ refresh: async () => signedKeyset(f.config) });
  const pending = bootstrapReady(f.options);
  f.config.keyset_version = '6';
  const r = f.result();
  r.server_auth.keyset_version = '6';
  r.epoch = r.server_auth.audience.epoch = '18446744073709551615';
  r.seen_checkpoint = {
    checkpoint_seq: '0',
    checkpoint_revision: '0',
    summary_digest: 'sha256:' + '0'.repeat(64),
  };
  f.emit(f.seal(r));
  assert.equal((await pending).readyResult.epoch, '18446744073709551615');
  assert.equal(f.sent[0].keyset_version, '5');
  assert.equal(f.trust.version, '6');
  assert.equal(f.trust.diagnostics.refreshSuccesses, 1);
  f.clean();
});

for (const interruption of ['abort', 'epoch']) {
  test(`rechecks ${interruption} after signature verification`, async () => {
    const f = await fixture();
    const pending = bootstrapReady({
      ...f.options,
      now: () => {
        if (interruption === 'abort') f.controller.abort();
        else f.transport.epoch++;
        return clock();
      },
    });
    f.emit(f.seal(f.result()));
    await assert.rejects(pending, { code: 'stopped' });
    f.clean();
  });
}

for (const registration of [
  undefined,
  {},
  { tenant: 't', realm: '2', node: '64', device: 'd', created: false },
  { tenant: 't', realm: '02', node: '64', device: 'd', created: true },
]) {
  test(`rejects untrusted initialization input ${JSON.stringify(registration)}`, async () => {
    const f = await fixture();
    await assert.rejects(bootstrapReady({ ...f.options, registration }), {
      code: 'network.protocol_error',
    });
    assert.equal(f.sent.length, 0);
    f.clean();
  });
}

test('double call preserves original listener; duplicate response settles once', async () => {
  const f = await fixture();
  const pending = bootstrapReady(f.options);
  await assert.rejects(bootstrapReady(f.options), { code: 'invalid_state' });
  assert.equal(f.listeners.size, 1);
  assert.equal(f.sent.length, 1);
  const response = f.seal(f.result());
  f.emit(response);
  f.emit(response);
  await pending;
  f.clean();
});

for (const reason of [
  'abort',
  'timeout',
  'epoch',
  'callback epoch',
  'stopped',
]) {
  test(`cleans ${reason} while key refresh is in flight; late result cannot finish next call`, async () => {
    const gate = deferred(),
      entered = deferred();
    const f = await fixture({
      refresh: async () => {
        entered.resolve();
        return gate.promise;
      },
    });
    const pending = bootstrapReady({
      ...f.options,
      timeoutMs: reason === 'timeout' ? 25 : 1000,
    });
    const oldRequest = f.sent[0];
    const response = f.result();
    response.server_auth.keyset_version = '6';
    f.emit(f.seal(response));
    await entered.promise;
    if (reason === 'abort') f.controller.abort();
    if (reason === 'epoch') f.transport.epoch++;
    if (reason === 'stopped') f.transport.state = 'stopped';
    if (reason === 'callback epoch') f.emit(f.seal(f.result()), 40);
    const rejected = assert.rejects(pending, {
      code: reason === 'timeout' ? 'timeout' : 'stopped',
    });
    if (reason === 'timeout' || reason === 'abort') {
      await rejected;
      f.clean();
    }
    f.config.keyset_version = '6';
    gate.resolve(signedKeyset(f.config));
    await rejected;
    await tick();
    f.clean();
    f.transport.state = 'ready';
    const nextController = new AbortController();
    const next = bootstrapReady({
      ...f.options,
      signal: nextController.signal,
    });
    assert.notEqual(f.sent[1].request_id, oldRequest.request_id);
    assert.notEqual(f.sent[1].channel_nonce, oldRequest.channel_nonce);
    assert.notEqual(f.sent[1].boot_id, oldRequest.boot_id);
    f.emit(f.seal(f.result(oldRequest)));
    await tick();
    assert.equal(f.listeners.size, 1);
    f.emit(f.seal(f.result()));
    await next;
    assert.equal(getEventListeners(nextController.signal, 'abort').length, 0);
    f.clean();
  });
}

test('pre-aborted call, send/setup failure and malformed/budget JSON release listeners', async () => {
  for (const mode of [
    'aborted',
    'throw',
    'reject',
    'subscribe',
    'json',
    'oversize',
    'duplicate key',
  ]) {
    const f = await fixture();
    if (mode === 'aborted') f.controller.abort();
    if (mode === 'throw')
      f.transport.send = () => {
        throw new Error('send throw');
      };
    if (mode === 'reject')
      f.transport.send = async () => {
        throw new Error('send reject');
      };
    if (mode === 'subscribe')
      f.transport.onMessage = () => {
        throw new Error('subscribe');
      };
    const pending = bootstrapReady(f.options);
    if (['json', 'oversize', 'duplicate key'].includes(mode)) {
      const bytes =
        mode === 'oversize'
          ? new Uint8Array(65537)
          : new TextEncoder().encode(
              mode === 'json' ? '{' : '{"type":"ready_result","type":"error"}',
            );
      for (const listener of f.listeners) listener(bytes, 41);
    }
    const code =
      mode === 'aborted'
        ? 'stopped'
        : mode === 'oversize'
          ? 'payload_size'
          : 'not_json';
    await assert.rejects(
      pending,
      ['throw', 'reject', 'subscribe'].includes(mode)
        ? { message: mode === 'subscribe' ? 'subscribe' : `send ${mode}` }
        : { code },
    );
    f.clean();
  }
});

test('cancelled refresh cannot settle or remove a newer attempt listener', async () => {
  const gate = deferred(),
    entered = deferred();
  const f = await fixture({
    refresh: async () => {
      entered.resolve();
      return gate.promise;
    },
  });
  const first = bootstrapReady(f.options);
  const old = f.result();
  old.server_auth.keyset_version = '6';
  f.emit(f.seal(old));
  await entered.promise;
  f.controller.abort();
  await assert.rejects(first, { code: 'stopped' });
  f.clean();
  const signal = new AbortController().signal;
  const next = bootstrapReady({ ...f.options, signal });
  f.config.keyset_version = '6';
  gate.resolve(signedKeyset(f.config));
  await tick();
  assert.equal(f.listeners.size, 1);
  f.emit(f.seal(f.result()));
  await next;
  assert.equal(getEventListeners(signal, 'abort').length, 0);
  f.clean();
});
