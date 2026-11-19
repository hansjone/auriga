import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { createSharedMessageTrust } from '../../src/shared/message-trust.js';
import { signingInput } from '../../src/network/trust/verifier.js';
import { setup, iso } from './message-fixture.mjs';
test('native context publishes only trusted K identity and original five-field witness', async () => {
  const f = setup();
  const proof = await f.contexts.prepare(
    { ...f.request, owner: 'forged', local: '99' },
    f.task,
  );
  proof.check();
  const first = f.state.calls[0];
  assert.equal(first.operation, 'messageContext');
  assert.deepEqual(
    Object.keys(first.body).sort(),
    [
      'bootId',
      'capability',
      'nativeWitness',
      'operationId',
      'sessionId',
      'taskId',
      'tenantId',
    ].sort(),
  );
  assert.deepEqual(
    Object.keys(first.body.nativeWitness).sort(),
    [
      'binding_id',
      'binding_revision',
      'observed_at',
      'valid_until',
      'witness_id',
    ].sort(),
  );
  await f.contexts.prepare(f.request, f.task);
  const again = f.state.calls[1];
  assert.equal(first.body.operationId, again.body.operationId);
  assert.equal(first.body.bootId, again.body.bootId);
  assert.notEqual(
    first.body.nativeWitness.witness_id,
    again.body.nativeWitness.witness_id,
  );
  await f.contexts.close();
  assert.throws(() => proof.check());
});

test('platform context rejects member, task, registration and native proof substitutions', async (t) => {
  const changes = {
    member: (v) => {
      v.binding.memberId = randomUUID();
    },
    task: (v) => {
      v.taskId = randomUUID();
    },
    transport: (v) => {
      v.connection.transport = 'nats';
    },
    node: (v) => {
      v.registration.node = '65';
    },
    zeroLocal: (v) => {
      v.registration.local = '0';
    },
    swappedLocal: (v) => {
      v.sourceGet.source.registration.local = '8';
    },
    binding: (v) => {
      v.sourceGet.source.native_binding.binding_id = randomUUID();
    },
    revision: (v) => {
      v.sourceGet.source.native_binding.binding_revision = '2';
    },
    witness: (v) => {
      v.sourceGet.witness_id = randomUUID();
    },
    revoked: (v) => {
      v.sourceGet.source.state = 'revoked';
    },
    expired: (v) => {
      v.sourceGet.valid_until = iso(Date.parse(v.sourceGet.observed_at));
    },
    audience: (v) => {
      v.sourceGet.server_auth.audience.node = '65';
    },
    signatureEncoding: (v) => {
      v.sourceGet.server_auth.signature = 'invalid';
    },
    missingAuth: (v) => {
      delete v.sourceGet.server_auth;
    },
  };
  for (const [name, change] of Object.entries(changes)) {
    await t.test(name, async () => {
      const f = setup();
      f.state.change = change;
      f.state.resign = !['signatureEncoding', 'missingAuth'].includes(name);
      await assert.rejects(f.contexts.prepare(f.request, f.task));
      await f.contexts.close();
    });
  }
});

test('a changed host or canceled call discards a late server registration', async (t) => {
  for (const change of ['cwd', 'provider', 'agent', 'cancel', 'expire']) {
    await t.test(change, async () => {
      const f = setup();
      f.state.change = () => {
        if (change === 'cwd') f.agent.session.header.cwd = '/another';
        if (change === 'provider')
          f.ctx.sessionPersistence.identity = Symbol('other');
        if (change === 'agent') f.agents.delete(f.agent.id);
        if (change === 'cancel') f.controller.abort();
        if (change === 'expire') f.state.now += 30_000;
      };
      await assert.rejects(
        f.contexts.prepare(f.request, f.task, f.controller.signal),
      );
      await f.contexts.close();
    });
  }
});

test('connection, producer and registration high-water marks never roll back', async (t) => {
  for (const kind of [
    'connection',
    'producer',
    'registration',
    'policy',
    'context',
    'boot',
    'local',
  ]) {
    await t.test(kind, async () => {
      const f = setup();
      await f.contexts.prepare(f.request, f.task);
      f.state.resign = true;
      f.state.change = (value) => {
        if (kind === 'connection') {
          value.connection.epoch = '2';
          value.sourceGet.server_auth.audience.epoch = '2';
        }
        if (kind === 'producer') value.producerEpoch = '1';
        if (kind === 'registration') {
          value.registration.registration_revision = '0';
          value.sourceGet.source.registration.registration_revision = '0';
        }
        if (kind === 'policy') value.sourceGet.policy_revision = '0';
        if (kind === 'context') value.contextId = randomUUID();
        if (kind === 'boot') value.connection.bootId = randomUUID();
        if (kind === 'local') {
          value.registration.local = '8';
          value.sourceGet.source.registration.local = '8';
        }
      };
      await assert.rejects(f.contexts.prepare(f.request, f.task));
      await f.contexts.close();
    });
  }
});

test('a retained context expires at its native observation deadline', async () => {
  const f = setup();
  const context = await f.contexts.prepare(f.request, f.task);
  f.state.now += 30_000;
  assert.throws(() => context.check());
  await f.contexts.close();
});

test('Host verifies the installed v2 root and real source signature, not a response public key', async (t) => {
  for (const kind of [
    'signature',
    'root',
    'keyset',
    'announcement',
    'missingConfig',
    'replaceConfig',
  ]) {
    await t.test(kind, async () => {
      const f = setup();
      if (kind === 'missingConfig') delete f.settings.messageTrust;
      if (kind === 'replaceConfig') {
        await f.contexts.prepare(f.request, f.task);
        f.settings.messageTrust.roots[0].public_key_spki = generateKeyPairSync(
          'ed25519',
        )
          .publicKey.export({ type: 'spki', format: 'der' })
          .toString('base64url');
      }
      f.state.change = (value) => {
        if (kind === 'signature')
          value.sourceGet.server_auth.signature = sign(
            null,
            signingInput(value.sourceGet),
            generateKeyPairSync('ed25519').privateKey,
          ).toString('base64url');
        if (kind === 'root')
          value.messageTrust.control.payload.allowed_authorities = [];
        if (kind === 'keyset')
          value.messageTrust.publication.keyset.payload.keys[0].public_key_spki =
            generateKeyPairSync('ed25519')
              .publicKey.export({ type: 'spki', format: 'der' })
              .toString('base64url');
        if (kind === 'announcement')
          value.messageTrust.announcement.payload.keyset_version = '2';
      };
      await assert.rejects(f.contexts.prepare(f.request, f.task));
      if (kind === 'missingConfig') assert.equal(f.state.calls.length, 0);
      await f.contexts.close();
    });
  }
});

test('message trust persists v2 high-water marks and verifies after reopening the same cache', async () => {
  const f = setup();
  await f.contexts.prepare(f.request, f.task);
  const saved = f.store.snapshot();
  assert.equal(saved.schema_version, 2);
  assert.equal(saved.publication.keyset.payload.keyset_version, '1');
  assert.equal(saved.announcement.payload.keyset_version, '1');
  assert.ok(saved.first_publication_ledger.length >= 3);
  await f.contexts.close();
  const trust = createSharedMessageTrust({
    settings: () => f.settings,
    tenantId: f.binding.tenantId,
    physicalInstanceId: f.binding.physicalInstanceId,
    now: () => f.state.now,
    store: f.store,
  });
  const prior = f.state.calls[0].body;
  const value = f.response(prior);
  await trust.verify(
    value.sourceGet,
    value.connection,
    prior.nativeWitness.witness_id,
  );
  await trust.close();
});
