import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { createHash, createPrivateKey } from 'node:crypto';
import {
  equal,
  signature,
  verifyPublication,
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
for (const vector of vectors.boundary_cases) {
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
    (vector.operation === 'signature_domain'
      ? 'production signature: '
      : 'test-side policy fixture: ') + vector.id,
    () => {
      const i = vector.input;
      let actual = 'accept';
      try {
        switch (vector.operation) {
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
