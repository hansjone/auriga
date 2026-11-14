import test from 'node:test';
import assert from 'node:assert/strict';
import { cp, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { generateKeyPairSync } from 'node:crypto';
import { packageNetworkTrust } from '../../../scripts/package-network-trust.mjs';

test('release trust resource loads outside the source repository and the public store accepts v2 only', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-v2-package-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await cp(new URL('../../../src/', import.meta.url), join(directory, 'src'), {
    recursive: true,
  });
  await packageNetworkTrust(pathToFileURL(join(directory, 'ui') + '/'));
  const { schema } = await import(
    pathToFileURL(join(directory, 'src/network/trust/v2-schema.js')).href
  );
  assert.deepEqual(
    schema,
    JSON.parse(
      await readFile(
        new URL(
          '../../../../../contracts/network/auto-renewal-v2.schema.json',
          import.meta.url,
        ),
        'utf8',
      ),
    ),
  );
  const { openTrustStore } = await import(
    pathToFileURL(join(directory, 'src/network/trust/store.js')).href
  );
  const root = generateKeyPairSync('ed25519');
  const store = await openTrustStore({
    realm: '1',
    tenant_id: 'test-tenant',
    roots: [
      {
        kid: 'root',
        public_key_spki: root.publicKey
          .export({ type: 'spki', format: 'der' })
          .toString('base64url'),
      },
    ],
    cache: { load: async () => undefined, commit: async () => {} },
  });
  await assert.rejects(
    store.update({ keyset: { schema_version: 1 }, root_auth: {} }),
    /invalid_chain/,
  );
});
