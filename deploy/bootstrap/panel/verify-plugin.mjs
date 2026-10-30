import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  verifyManifest,
  signingInput,
  sriDigest,
} from '../../../clients/dsh-plugin/src/managed/panel.js';
import { canonicalize } from '../../../clients/dsh-plugin/src/managed/jcs.js';

const [root, output, epoch] = process.argv.slice(2);
const keys = JSON.parse(await readFile(`${root}/panel-keys.json`, 'utf8'));
const manifest = JSON.parse(await readFile(`${output}/manifest.json`, 'utf8'));
const now = Number(epoch);
const { signature, ...body } = manifest;
assert.equal(signingInput(manifest).toString(), canonicalize(body));
assert.equal(signature.alg, 'EdDSA');
assert.equal(
  verifyManifest(manifest, { keys, pluginVersion: '0.0.0', now }),
  manifest,
);
const file = await readFile(`${output}/${manifest.entry}`);
assert.equal(sriDigest('sha384', file), manifest.integrity[manifest.entry]);
for (const [candidate, trusted, clock, reason] of [
  [{ ...manifest, version: '9.0.0' }, keys, now, 'tampered manifest'],
  [manifest, keys, now + 31 * 86400, 'expired manifest'],
  [manifest, { keys: [] }, now, 'missing public key'],
  [
    manifest,
    { keys: keys.keys.map((key) => ({ ...key, exp: now - 10 })) },
    now,
    'expired key',
  ],
]) {
  assert.throws(
    () =>
      verifyManifest(candidate, {
        keys: trusted,
        pluginVersion: '0.0.0',
        now: clock,
      }),
    (error) => error.code === 'common.unauthenticated',
    reason,
  );
}
assert.notEqual(
  sriDigest('sha384', Buffer.concat([file, Buffer.from('tampered')])),
  manifest.integrity[manifest.entry],
);
