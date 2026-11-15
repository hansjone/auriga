import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { createHash, createPrivateKey } from 'node:crypto';
import {
  equal,
  digest,
  signature,
  verifyPublication,
  verifyTransport,
  verifyTransportTransition,
  verifyTransportCatchup,
  verifyTransportConfirmation,
} from '../../../src/network/trust/v2.js';
import { openV2TrustStore } from '../../../src/network/trust/v2-store.js';
import {
  checkArchive,
  checkClock,
  checkMigration,
  checkProjection,
  checkReceipt,
  checkRenewalConfig,
  openSealedMaterial,
} from './issuer-policy-fixture.js';

const vectors = JSON.parse(
  await readFile(
    new URL(
      '../../../../../contracts/network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
for (const vector of vectors.cases) {
  test('production v2 consumer: ' + vector.id, () => {
    const i = vector.input;
    const context = {
      ...i,
      roots: new Map([[i.latest_control.root_auth.kid, i.root_spki]]),
    };
    let actual = 'accept';
    try {
      verifyPublication(context);
    } catch (error) {
      actual = error.message;
    }
    assert.equal(actual, vector.expected);
  });
}
for (const vector of vectors.issuer_cases) {
  test('issuer policy fixture: ' + vector.id, () => {
    let actual = 'accept';
    try {
      const i = vector.input;
      checkClock(
        i.clock,
        i.write_first_publication,
        i.clock_error_persisted,
        i.stable_samples,
      );
    } catch (error) {
      actual = error.message;
    }
    assert.equal(actual, vector.expected);
  });
}
// K3: these operations govern server/node deployment, never plugin authority.
const nonPluginOperations = {
  node_control_binding:
    'K3 节点确认身份：I 核对 NodeControlPins 与已认证 node_id；插件使用 InstallationPins，不接节点认证或登记',
  projection_access:
    'K3 UID 与材料权限：N/I/D 核对专属 projector 的 SO_PEERCRED、UID 和私有卷；插件不读取组件私钥卷',
};
for (const vector of vectors.boundary_cases) {
  if (Object.hasOwn(nonPluginOperations, vector.operation)) {
    test(
      '非插件职责: ' + vector.id,
      { skip: nonPluginOperations[vector.operation] },
      () => {},
    );
    continue;
  }
  if (vector.operation === 'announcement') {
    test('production store announcement: ' + vector.id, async () => {
      const i = vector.input,
        c = i.context;
      let persisted = true;
      const store = await openV2TrustStore({
        roots: [
          { kid: c.latest_control.root_auth.kid, public_key_spki: c.root_spki },
        ],
        realm: c.realm,
        tenant_id: c.tenant_id,
        now: () => Date.parse(c.now),
        cache: {
          load: async () => undefined,
          commit: async () => {
            if (!persisted) throw Error('disk unavailable');
          },
        },
      });
      let accepted = false;
      try {
        await store.installControl(c.latest_control);
        await store.update(c.publication);
        persisted = i.persisted;
        await store.announce(i.announcement);
        store.assertUsable();
        accepted = true;
      } catch {
        /* Production store must fail closed for every negative. */
      }
      assert.equal(accepted, vector.expected === 'accept');
    });
    continue;
  }
  test(
    ([
      'signature_domain',
      'transport_transition',
      'transport_catchup',
      'transport_confirmation',
    ].includes(vector.operation)
      ? 'production verification: '
      : 'test-side policy fixture: ') + vector.id,
    () => {
      const i = vector.input;
      let actual = 'accept';
      try {
        switch (vector.operation) {
          case 'transport_transition':
            verifyTransportTransition(i);
            break;
          case 'transport_catchup':
            verifyTransportCatchup(i);
            break;
          case 'transport_confirmation':
            verifyTransportConfirmation(i);
            break;
          case 'signature_domain':
            signature(i.signed, i.verification_domain, i.verification_spki);
            break;
          case 'control_receipt':
            checkReceipt(i);
            break;
          case 'projection':
            checkProjection(i);
            break;
          case 'migration':
            checkMigration(i);
            break;
          case 'archive':
            checkArchive(i);
            break;
          case 'config':
            checkRenewalConfig(i.config);
            break;
          case 'hpke': {
            // Public synthetic vector key is reconstructed only in this adapter.
            const key = createPrivateKey({
              key: Buffer.concat([
                Buffer.from('302e020100300506032b656e04220420', 'hex'),
                createHash('sha256')
                  .update('Auriga synthetic HPKE test only receiver')
                  .digest(),
              ]),
              format: 'der',
              type: 'pkcs8',
            });
            if (!equal(openSealedMaterial(i.sealed, key), i.expected_plaintext))
              throw Error('invalid_chain');
            break;
          }
          default:
            throw Error('unhandled vector operation');
        }
      } catch (error) {
        actual = error.message;
      }
      assert.equal(actual, vector.expected);
    },
  );
}

// Exercise dispatch from the existing production transport entry, including
// rejection when a received proof attempts to supply the installation scope.
for (const operation of ['transport_transition', 'transport_catchup']) {
  const vector = vectors.boundary_cases.find(
    (v) => v.operation === operation && v.expected === 'accept',
  );
  test('production transport dispatch: ' + operation, () => {
    const input = vector.input;
    const transition = input.transition ?? input;
    const bundle = input.current ?? input.transport;
    const context = {
      roots: new Map([
        [transition.latest_control.root_auth.kid, transition.root_spki],
      ]),
      tenant_id: transition.tenant_id,
      realm: transition.realm,
      latest_control: input.control ?? transition.latest_control,
      seen_transport_generation: transition.seen_generation,
      seen_bundle_version: transition.seen_bundle_version,
      seen_bundle_digest: transition.seen_bundle_digest,
      isolation_enforced: transition.isolation_enforced,
      [operation]: input,
    };
    const publication = { authority: transition.authority };
    verifyTransport(bundle, publication, context);
    if (operation === 'transport_catchup') {
      // The local reader may already be beyond the historical root anchor.
      verifyTransport(bundle, publication, {
        ...context,
        seen_bundle_version: '50',
      });
      verifyTransport(bundle, publication, {
        ...context,
        seen_bundle_version: bundle.payload.bundle_version,
        seen_bundle_digest: digest(bundle),
      });
      assert.throws(
        () =>
          verifyTransport(bundle, publication, {
            ...context,
            seen_bundle_version: '999',
          }),
        /version_rollback/,
      );
    }
    assert.throws(
      () =>
        verifyTransport(bundle, publication, {
          ...context,
          tenant_id: 'other-tenant',
        }),
      /scope_denied/,
    );
    assert.throws(
      () =>
        verifyTransport(bundle, publication, { ...context, roots: new Map() }),
      /invalid_chain/,
    );
    assert.throws(
      () =>
        verifyTransport(bundle, publication, {
          ...context,
          seen_transport_generation: '999',
        }),
      /version_rollback/,
    );
  });
}
