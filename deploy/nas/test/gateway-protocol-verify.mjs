import assert from 'node:assert/strict';
import { createHash, createPrivateKey } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { buffer } from 'node:stream/consumers';
import { openTrustStore } from '../../../clients/dsh-plugin/src/network/trust/store.js';
import { equal } from '../../../clients/dsh-plugin/src/network/trust/v2.js';
import { verifyEnvelope } from '../../../clients/dsh-plugin/src/network/trust/verifier.js';
import { createV2Signer } from '../../../clients/dsh-plugin/test/network/trust/v2-keyset-fixtures.js';

const input = JSON.parse(await readFile(process.argv[2], 'utf8'));
const frames = JSON.parse(await readFile(input.frames, 'utf8'));
const { keyset } = JSON.parse(await readFile(input.signed, 'utf8'));
const { tenantId, realm, rootKid, rootFingerprint } = JSON.parse(
  await readFile(input.trust, 'utf8'),
);
const root = await readFile(input.root);
assert.equal(
  rootFingerprint,
  'sha256:' + createHash('sha256').update(root).digest('hex'),
);
assert.equal(keyset.realm, realm);

// The NAS queue fixture supplies the installed public keyset. Sign that same
// payload as a v2 bootstrap for a fresh plugin trust store using test keys.
const rootKey = await buffer(process.stdin);
const privateKey = createPrivateKey({
  key: rootKey,
  format: 'der',
  type: 'pkcs8',
});
rootKey.fill(0);
const signer = createV2Signer({ privateKey }, rootKid);
const publication = signer.publication(keyset, tenantId);
assert(equal(publication.keyset.payload.keys, keyset.keys));
const announcement = signer.announcement(keyset, {}, tenantId);

let value;
const trust = await openTrustStore({
  cache: {
    async load() {
      return value;
    },
    async commit(next) {
      value = next;
    },
  },
  realm,
  tenant_id: tenantId,
  roots: [{ kid: rootKid, public_key_spki: root.toString('base64url') }],
});
await trust.update(publication);
await trust.announce(announcement);

const context = {
  realm,
  node: '2',
  nodeIncarnation: 'inc',
  epoch: '1',
  channelNonce: 'nonce',
  requestId: 'req',
};
const active = keyset.keys.find(
  (key) => key.purpose === 'protocol_response' && key.state === 'active',
);
assert.equal(frames.good.type, 'error');
assert.equal(frames.good.server_auth.kid, active?.kid);
assert.equal(frames.good.server_auth.keyset_version, keyset.keyset_version);
assert.equal(verifyEnvelope(frames.good, { trust, context }), true);

const retired = keyset.keys.find(
  (key) => key.kid === frames.mixed.server_auth.kid,
);
assert.equal(retired?.state, 'retired');
assert(
  BigInt(frames.mixed.server_auth.keyset_version) < BigInt(keyset.keyset_version),
);
assert.throws(
  () => verifyEnvelope(frames.mixed, { trust, context }),
  { code: 'network.signature_invalid', message: 'bad signature' },
);
console.log(
  'PASS production gateway ERROR accepted after next-to-active promotion; old metadata/new private key rejected',
);
