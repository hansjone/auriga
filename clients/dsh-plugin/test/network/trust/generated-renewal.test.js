import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { generatedRenewalFiles } from '../../../scripts/gen-network-keyset.mjs';

test('v2 runtime schema generation preserves the complete frozen contract', async () => {
  const source = JSON.parse(
    await readFile(
      new URL(
        '../../../../../contracts/network/auto-renewal-v2.schema.json',
        import.meta.url,
      ),
      'utf8',
    ),
  );
  assert.deepEqual(Object.keys(generatedRenewalFiles), [
    'auto-renewal-v2.d.ts',
    'auto-renewal-v2-schema.js',
  ]);
  for (const [name, content] of Object.entries(generatedRenewalFiles))
    assert.equal(
      await readFile(
        new URL('../../../src/generated/network/' + name, import.meta.url),
        'utf8',
      ),
      content,
    );
  const output = generatedRenewalFiles['auto-renewal-v2-schema.js'];
  // Execute the exact generated module in memory; do not modify K-owned files.
  const { schema } = await import(
    'data:text/javascript;base64,' + Buffer.from(output).toString('base64')
  );
  assert.deepEqual(schema, source);
  assert.equal(schema.$ref, '#/$defs/Publication');
  const visit = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.$ref) {
      assert.ok(node.$ref.startsWith('#/$defs/'));
      assert.ok(
        Object.hasOwn(schema.$defs, node.$ref.slice('#/$defs/'.length)),
      );
    }
    for (const value of Object.values(node)) visit(value);
  };
  visit(schema);
  // Signature-relevant policy and schema keywords must survive generation.
  assert.equal(schema.$defs.RuntimeAuthority.additionalProperties, false);
  assert.equal(
    schema.$defs.RuntimeAuthority.properties.expiry_policy.const,
    'no_hard_expiry',
  );
  assert.equal(
    schema.$defs.Limits.properties.restart_key_seconds.maximum,
    604800,
  );
  assert.equal(
    schema.$defs.RootControl.properties.revoked_authority_ids.maxItems,
    65536,
  );
});
