import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { openTrustStore } from '../../../clients/dsh-plugin/src/network/trust/store.js';
import { verifyEnvelope } from '../../../clients/dsh-plugin/src/network/trust/verifier.js';

const input = JSON.parse(await readFile(process.argv[2], 'utf8'));
const frames = JSON.parse(await readFile(input.frames, 'utf8'));
const signed = JSON.parse(await readFile(input.signed, 'utf8'));
const root = await readFile(input.root);
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
  config: signed,
  realm: '1',
  roots: [{ kid: 'fixture-root', public_key_spki: root.toString('base64url') }],
});
const context = {
  realm: '1',
  node: '2',
  nodeIncarnation: 'inc',
  epoch: '1',
  channelNonce: 'nonce',
  requestId: 'req',
};
assert.equal(verifyEnvelope(frames.good, { trust, context }), true);
assert.throws(
  () => verifyEnvelope(frames.mixed, { trust, context }),
  (error) => error.code === 'network.signature_invalid',
);
assert.notEqual(frames.good.server_auth.kid, frames.mixed.server_auth.kid);
assert.equal(
  frames.good.server_auth.keyset_version,
  signed.keyset.keyset_version,
);
console.log(
  'PASS production gateway ERROR accepted after next-to-active promotion; old metadata/new private key rejected',
);
