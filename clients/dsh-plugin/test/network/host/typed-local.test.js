import assert from 'node:assert/strict';
import test from 'node:test';
import { CODES } from '../../../src/network/host/errors.js';
import {
  reserveLocal,
  registerReservedLocal,
  openLedger,
} from '../../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';
import { createWorld, IDENTITY, unicast } from './fixtures.js';
import { createHostPortOperations } from '../../../src/network/host/native/host-port.js';
import { fixture } from './native/fixtures.js';

const intent = (addressType = 'session') => ({
  opId: 'typed',
  purpose: 'test',
  mappingDigest: 'mapping',
  addressType,
});

for (const origin of ['', null, false, 0, 'user', 'unknown', {}]) {
  test(`fake native port rejects invalid origin ${JSON.stringify(origin)} before projection`, async () => {
    const f = fixture();
    f.seed('session-a', { origin });
    const host = await createHostPortOperations(f);
    await assert.rejects(host.getSession('session-a'), {
      code: CODES.hostIncompatible,
    });
  });
}

for (const origin of [undefined, 'subagent']) {
  test(`binding follows verified host origin ${origin} and rejects caller kind conflict`, async () => {
    const w = await createWorld();
    w.host.addSession('native', { cwd: '/work' });
    const native = w.host.sessions.get('native');
    delete native.origin;
    if (origin !== undefined) native.origin = origin;
    const kind = origin === 'subagent' ? 'subagent' : 'session';
    await assert.rejects(
      w.adapter.bindExistingSession({
        opId: 'wrong',
        hostSessionId: 'native',
        cwd: '/work',
        kind: kind === 'session' ? 'subagent' : 'session',
      }),
      { code: CODES.hostIncompatible },
    );
    assert.equal(w.ledger.read().localHighWater, '0');
    const local = await w.adapter.bindExistingSession({
      opId: 'correct',
      hostSessionId: 'native',
      cwd: '/work',
    });
    assert.equal(w.ledger.read().locals[local].addressType, kind);
    assert.equal(w.adapter.localAddress(local).type, kind);
    await w.ledger.close();
  });
}

test('same operation cannot change address type or fingerprint', async () => {
  const w = await createWorld();
  const first = await reserveLocal(w.ledger, intent());
  await assert.rejects(reserveLocal(w.ledger, intent('subagent')), {
    code: CODES.localAllocationConflict,
  });
  assert.deepEqual(w.ledger.read().locals['1'], first);
  await w.ledger.close();
});

for (const field of [
  'addressType',
  'opId',
  'local',
  'fingerprint',
  'registrationRevision',
  'localHighWater',
  'registrationId',
]) {
  test(`registration missing or altered ${field} keeps original typed pending`, async () => {
    const w = await createWorld();
    await reserveLocal(w.ledger, intent());
    const original = w.ledger.read();
    for (const value of [
      undefined,
      field === 'registrationId' ? '' : 'wrong',
    ]) {
      await assert.rejects(
        registerReservedLocal(w.ledger, 'typed', async (entry) => ({
          status: 'registered',
          registrationId: 'reg-1',
          opId: entry.opId,
          local: entry.local,
          addressType: entry.addressType,
          fingerprint: entry.fingerprint,
          registrationRevision: '1',
          localHighWater: '1',
          [field]: value,
        })),
        { code: CODES.protocolError },
      );
      assert.deepEqual(w.ledger.read(), original);
    }
    await w.ledger.close();
  });
}

test('legacy untyped allocation preserves evidence and blocks recovery and allocation', async () => {
  const w = await createWorld();
  await reserveLocal(w.ledger, intent());
  const legacy = w.ledger.read();
  legacy.version = 1;
  delete legacy.locals['1'].addressType;
  // The original v1 fingerprint must stay intact, not be upgraded from binding.
  const { createHash } = await import('node:crypto');
  const e = legacy.locals['1'];
  e.fingerprint = createHash('sha256')
    .update(`${e.opId}\0${e.local}\0${e.nonce}\0${e.mappingDigest}`)
    .digest('hex');
  await w.ledger.close();
  const store = createMemoryLedgerStore();
  const handle = await store.open();
  await handle.commit(legacy);
  await handle.close();
  const ledger = await openLedger({ store, identity: IDENTITY });
  assert.equal(ledger.read().identityConflict, CODES.recoveryConflict);
  await assert.rejects(reserveLocal(ledger, intent()), {
    code: CODES.recoveryConflict,
  });
  await assert.rejects(
    registerReservedLocal(ledger, 'typed', w.platform.network.registerLocal),
    { code: CODES.recoveryConflict },
  );
  assert.deepEqual(ledger.read().locals, legacy.locals);
  assert.deepEqual(ledger.read().localQueue, legacy.localQueue);
  assert.equal(ledger.read().localHighWater, '1');
  await ledger.close();
});

for (const origin of ['', null, false, 0, 'unknown', {}]) {
  test(`fake host invalid origin ${JSON.stringify(origin)} never reserves a local`, async () => {
    const w = await createWorld();
    w.host.addSession('native', { cwd: '/work', origin });
    await assert.rejects(
      w.adapter.bindExistingSession({
        opId: 'bad',
        hostSessionId: 'native',
        cwd: '/work',
      }),
      { code: CODES.hostIncompatible },
    );
    assert.equal(w.ledger.read().localHighWater, '0');
    assert.equal(w.platform.state.calls.register, 0);
    await w.ledger.close();
  });
}

test('fake native stat and inspect origin disagreement fails closed; fork lineage stays ordinary', async () => {
  const f = fixture();
  f.seed('fork', { parentSession: 'parent' });
  const host = await createHostPortOperations(f);
  assert.deepEqual(await host.getSession('fork'), {
    id: 'fork',
    cwd: '/work/project',
  });
  f.ctx.sessionController.inspect = async (id) => ({
    meta: { id, cwd: '/work/project', origin: 'subagent' },
    events: [],
  });
  await assert.rejects(host.getSession('fork'), {
    code: CODES.hostIncompatible,
  });
});

for (const mode of ['missing', 'read_failure', 'bad_id', 'bad_cwd']) {
  test(`fake host ${mode} cannot reserve a session`, async () => {
    const w = await createWorld();
    w.host.getSession = async () => {
      if (mode === 'read_failure') throw new Error('read failure');
      if (mode === 'missing') return null;
      return {
        id: mode === 'bad_id' ? 'other' : 'native',
        cwd: mode === 'bad_cwd' ? '' : '/work',
      };
    };
    await assert.rejects(
      w.adapter.bindExistingSession({
        opId: 'bad',
        hostSessionId: 'native',
        cwd: '/work',
      }),
    );
    assert.equal(w.ledger.read().localHighWater, '0');
    await w.ledger.close();
  });
}

for (const changed of [
  'addressType',
  'fingerprint',
  'nonce',
  'mappingDigest',
]) {
  test(`immutable ${changed} cannot rewrite an existing local`, async () => {
    const w = await createWorld();
    await reserveLocal(w.ledger, intent());
    const original = w.ledger.read();
    await assert.rejects(
      w.ledger.update((d) => {
        d.locals['1'][changed] = 'changed';
      }),
      { code: CODES.localAllocationConflict },
    );
    assert.deepEqual(w.ledger.read(), original);
    await w.ledger.close();
  });
}

for (const corruption of ['addressType', 'fingerprint']) {
  test(`persisted changed ${corruption} fails recovery without rewriting history`, async () => {
    const w = await createWorld();
    await reserveLocal(w.ledger, intent());
    const snapshot = w.store.snapshot();
    snapshot.locals['1'][corruption] =
      corruption === 'addressType' ? 'subagent' : '0'.repeat(64);
    await w.ledger.close();
    w.store.restore(snapshot);
    await assert.rejects(openLedger({ store: w.store, identity: IDENTITY }), {
      code: CODES.ledgerUnavailable,
    });
    assert.deepEqual(w.store.snapshot(), snapshot);
  });
}

for (const changedOrigin of [undefined, 'subagent', 'unknown']) {
  test(`new conversation fixes session intent before create and verifies ${changedOrigin}`, async () => {
    const w = await createWorld();
    await w.platform.pump(w.adapter, [
      unicast({
        recipientId: 'sys',
        local: '0',
        kind: 'first_contact',
        messageId: 'contact',
        inReplyTo: null,
        contactScope: 'conversation',
      }),
    ]);
    const create = w.host.createSession;
    w.host.createSession = async (input) => {
      assert.equal(w.ledger.read().locals['1'].addressType, 'session');
      const result = await create(input);
      if (changedOrigin !== undefined)
        w.host.sessions.get(input.id).origin = changedOrigin;
      return result;
    };
    const result = await w.adapter.openContactSession({
      contactId: 'contact',
      cwd: '/work',
    });
    assert.equal(
      result.state,
      changedOrigin === undefined ? 'session_ready' : 'unknown',
    );
    assert.equal(w.ledger.read().locals['1'].addressType, 'session');
    assert.equal(
      w.ledger.read().bindings['1'].state,
      changedOrigin === undefined ? 'bound' : 'unknown',
    );
    await w.adapter.openContactSession({ contactId: 'contact', cwd: '/work' });
    assert.equal(w.host.counts.create, 1);
    await w.ledger.close();
  });
}

test('settled legacy untyped binding cannot grant a typed address or renewed permission', async () => {
  const w = await createWorld();
  w.host.addSession('native', { cwd: '/work' });
  await w.adapter.bindExistingSession({
    opId: 'old',
    hostSessionId: 'native',
    cwd: '/work',
  });
  const snapshot = w.store.snapshot(),
    entry = snapshot.locals['1'];
  delete entry.addressType;
  delete entry.expectedRegistrationRevision;
  delete entry.registrationRevision;
  delete snapshot.registrationRevision;
  snapshot.version = 1;
  const { createHash } = await import('node:crypto');
  entry.fingerprint = createHash('sha256')
    .update(
      `${entry.opId}\0${entry.local}\0${entry.nonce}\0${entry.mappingDigest}`,
    )
    .digest('hex');
  await w.ledger.close();
  w.store.restore(snapshot);
  await w.boot();
  assert.throws(() => w.adapter.localAddress('1'), {
    code: CODES.recoveryConflict,
  });
  await assert.rejects(w.adapter.recoverLocalRegistrations(), {
    code: CODES.recoveryConflict,
  });
  await assert.rejects(
    w.adapter.openContactSession({ contactId: 'old', cwd: '/work' }),
    { code: CODES.recoveryConflict },
  );
  assert.deepEqual(w.store.snapshot(), snapshot);
  await w.ledger.close();
});
