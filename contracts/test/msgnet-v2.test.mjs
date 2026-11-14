import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createPublicKey, verify, X509Certificate } from 'node:crypto';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import formats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { evaluate, digest } from '../network/fixtures/msgnet-v2-oracle.mjs';
import {
  evaluateIssuer,
  evaluateBoundary,
} from '../network/fixtures/msgnet-v2-boundary-oracle.mjs';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';
import {
  checkMutations,
  discoverPredicates,
} from '../network/fixtures/check-msgnet-v2-mutations.mjs';
const fixture = JSON.parse(
  readFileSync(
    new URL(
      '../network/fixtures/auto-renewal-v2-vectors.json',
      import.meta.url,
    ),
  ),
);
const schema = JSON.parse(
  readFileSync(
    new URL('../network/auto-renewal-v2.schema.json', import.meta.url),
  ),
);
const ajv = new Ajv({ strict: true });
formats(ajv);
ajv.addSchema(schema);
const vectorSchema = JSON.parse(
  readFileSync(
    new URL('../network/msgnet-v2-vectors.schema.json', import.meta.url),
  ),
);
ajv.addSchema(vectorSchema);
test('every oracle guard deletion is detected by a shared vector', async () => {
  const result = await checkMutations();
  assert.ok(result.count > 0);
  const equivalents = JSON.parse(
    readFileSync(
      new URL(
        '../network/fixtures/msgnet-v2-equivalent-mutations.json',
        import.meta.url,
      ),
    ),
  );
  assert.equal(result.survivors.length, equivalents.length);
  assert.equal(result.killed + result.equivalent, result.count);
});
test('all shared input groups conform to vector schema', () => {
  const validate = ajv.getSchema(vectorSchema.$id);
  assert.ok(validate(fixture), JSON.stringify(validate.errors));
});
test('OpenAPI permissions and status errors bind to catalog', () => {
  const catalog = JSON.parse(
    readFileSync(new URL('../catalog/msgnet-v2.json', import.meta.url)),
  );
  const api = YAML.parse(
    readFileSync(
      new URL('../openapi/network-keys-v2.yaml', import.meta.url),
      'utf8',
    ),
  );
  for (const path of Object.values(api.paths))
    for (const operation of Object.values(path))
      if (operation['x-permission'])
        assert.ok(catalog.permissions.includes(operation['x-permission']));
  assert.deepEqual(
    new Set(
      schema.$defs.RenewalStatus.properties.failure_code.enum.filter(
        (x) => x !== 'none',
      ),
    ),
    new Set(catalog.errors.map((x) => x.code)),
  );
  for (const v of [
    ...fixture.cases,
    ...fixture.issuer_cases,
    ...fixture.boundary_cases,
  ])
    if (v.expected !== 'accept')
      assert.ok(
        catalog.errors.some((x) => x.code === 'collab.msgnet_' + v.expected),
        v.id,
      );
});
for (const v of fixture.cases)
  test(v.id, () => assert.equal(evaluate(v), v.expected));
for (const v of fixture.issuer_cases)
  test(v.id, () => assert.equal(evaluateIssuer(v), v.expected));
for (const v of fixture.boundary_cases)
  test(v.id, () => assert.equal(evaluateBoundary(v), v.expected));
for (const v of fixture.signatures)
  test('signature ' + v.id, () =>
    assert.equal(
      verify(
        null,
        Buffer.from(v.domain + '\0' + v.canonical),
        createPublicKey({
          key: Buffer.from(v.public_key_spki, 'base64url'),
          format: 'der',
          type: 'spki',
        }),
        Buffer.from(v.signature, 'base64url'),
      ),
      v.valid,
    ),
  );
test('valid publication conforms to strict schema', () => {
  const validate = ajv.getSchema(schema.$id);
  assert.ok(
    validate(fixture.cases[0].input.publication),
    JSON.stringify(validate.errors),
  );
});
for (const field of ['not_after', 'delegate'])
  test('authority rejects ' + field, () => {
    const p = structuredClone(
      fixture.cases[0].input.publication.authority.payload,
    );
    p[field] = 'unexpected';
    assert.equal(
      ajv.getSchema(schema.$id + '#/$defs/RuntimeAuthority')(p),
      false,
    );
  });
test('old/new internal CA leaf chains and foreign CA refusal', () => {
  const chains = fixture.certificates.map((x) => ({
    ca: new X509Certificate(Buffer.from(x.ca_der, 'base64url')),
    leaf: new X509Certificate(Buffer.from(x.leaf_der, 'base64url')),
  }));
  for (const chain of chains) {
    assert.ok(chain.leaf.verify(chain.ca.publicKey));
    assert.equal(
      chain.leaf.checkHost('submit.example.com'),
      'submit.example.com',
    );
    assert.equal(chain.leaf.checkHost('foreign.example.com'), undefined);
  }
  assert.equal(chains[0].leaf.verify(chains[1].ca.publicKey), false);
});

// N/I/D 同读本地投影向量，实现线须换成其生产 reader adapter。
const localVectors = JSON.parse(
  readFileSync(
    new URL(
      '../network/fixtures/msgnet-local-projection-vectors.json',
      import.meta.url,
    ),
  ),
);
const { evaluateLocalProjection } =
  await import('../network/fixtures/msgnet-local-projection-oracle.mjs');
for (const v of localVectors)
  test(v.id, () => {
    for (const [name, value] of [
      ['LocalProjectionRequest', v.input.request],
      ['LocalProjectionManifest', v.input.manifest],
    ]) {
      const validate = ajv.getSchema(schema.$id + '#/$defs/' + name);
      assert.ok(validate(value), JSON.stringify(validate.errors));
    }
    assert.equal(evaluateLocalProjection(v), v.expected);
  });
test('local IPC rejects path, exec and unknown operations', () => {
  const valid = localVectors[0].input.request;
  const validate = ajv.getSchema(schema.$id + '#/$defs/LocalProjectionRequest');
  for (const extra of [
    { path: '../arbitrary' },
    { exec: 'arbitrary' },
    { operation: 'restart' },
  ])
    assert.equal(validate({ ...valid, ...extra }), false);
});
test('plugin runtime schema is fresh and preserves every source constraint', async () => {
  const { schema: runtimeSchema } =
    await import('../../clients/dsh-plugin/src/generated/network/auto-renewal-v2-schema.js');
  assert.deepEqual(runtimeSchema, schema);
});

test('comparison discovery includes callbacks and skips literals and comments', () => {
  const source =
    "const selected = items.some(x => x.purpose === purpose); const end = now <= expiry; // a > b\nconst text = 'x !== y';";
  const targets = discoverPredicates(source);
  assert.deepEqual(
    targets.map((t) => [source.slice(t.start, t.end), t.replacement]),
    [
      ['===', '!=='],
      ['<=', '<'],
    ],
  );
});

test('local response phases and nonce bounds follow schema', () => {
  const selector = structuredClone(localVectors[0].input.request.selector);
  const validate = ajv.getSchema(
    schema.$id + '#/$defs/LocalProjectionResponse',
  );
  for (const phase of ['loaded', 'handshake_verified', 'failed'])
    for (const failure_code of schema.$defs.LocalProjectionResponse.properties
      .failure_code.enum) {
      assert.equal(
        validate({ selector, phase, failure_code }),
        (phase === 'failed') === (failure_code !== 'none'),
      );
    }
  const validateSelector = ajv.getSchema(
    schema.$id + '#/$defs/LocalProjectionSelector',
  );
  for (const nonce of ['A'.repeat(42), 'A'.repeat(44), 'A'.repeat(42) + 'B'])
    assert.equal(validateSelector({ ...selector, nonce }), false);
  assert.equal(validateSelector({ ...selector, nonce: 'A'.repeat(43) }), true);
});
test('installation pins share a versioned closed model', () => {
  const validate = ajv.getSchema(schema.$id + '#/$defs/InstallationPins');
  const pin = {
    target_id: 'synthetic-target',
    identity_id: 'synthetic-identity',
    signing_spki: fixture.cases[0].input.root_spki,
    component: 'network',
    gateway_id: 'synthetic-gateway',
    sealing_spki: 'synthetic-public-key',
    subject: 'CN=synthetic-projector',
  };
  const source = {
    schema_version: 2,
    tenant_id: 'synthetic-tenant',
    realm: '1',
    pins: [pin],
  };
  assert.ok(validate(source), JSON.stringify(validate.errors));
  for (const field of Object.keys(pin)) {
    const bad = structuredClone(source);
    delete bad.pins[0][field];
    assert.equal(validate(bad), false);
  }
  assert.equal(validate({ ...source, schema_version: 1 }), false);
  assert.equal(
    validate({ ...source, pins: [{ ...pin, exec: 'arbitrary' }] }),
    false,
  );
});

test('unpublished status carries no fabricated publication fields', () => {
  const validate = ajv.getSchema(schema.$id + '#/$defs/RenewalStatusOutput');
  const source = {
    tenant_id: 'synthetic-tenant',
    realm: '1',
    state: 'clock_error',
    authority_id: 'synthetic-authority',
    expiry_policy: 'no_hard_expiry',
    control_revision: '1',
    review_due_at: '2027-11-14T00:00:00Z',
    review_required: false,
    announcement_confirmed: false,
    failure_code: 'collab.msgnet_clock_error',
    has_publication: false,
  };
  assert.ok(validate(source), JSON.stringify(validate.errors));
  for (const field of [
    'generation',
    'keyset_version',
    'last_success_at',
    'business_valid_until',
    'next_renewal_at',
  ])
    assert.equal(validate({ ...source, [field]: 'fabricated' }), false);
  assert.equal(validate({ ...source, state: 'ready' }), false);
});
test('sealed bootstrap authenticates by installation signature independently', () => {
  const api = YAML.parse(
    readFileSync(
      new URL('../openapi/network-keys-v2.yaml', import.meta.url),
      'utf8',
    ),
  );
  const operation =
    api.paths['/api/collab/v2/network/bootstrap/materials'].post;
  assert.deepEqual(operation.security, []);
  assert.ok(
    operation['x-auriga-installation-auth'].includes(
      'ProjectionRequest.identity_auth',
    ),
  );
  assert.equal(operation['x-permission'], 'collab.msgnet.projection.read');
});
test('migration material commitment has a closed public-key model', () => {
  const validate = ajv.getSchema(
    schema.$id + '#/$defs/MigrationMaterialCommitment',
  );
  const keys = fixture.cases[0].input.publication.keyset.payload.keys.map(
    ({ kid, purpose, public_key_spki, not_before, not_after }) => ({
      kid,
      purpose,
      public_key_spki,
      not_before,
      not_after,
    }),
  );
  assert.ok(validate({ schema_version: 2, keys }));
  assert.equal(
    validate({
      schema_version: 2,
      keys: keys.map((x) => ({ ...x, private_pkcs8: 'arbitrary' })),
    }),
    false,
  );
});

test('migration member binds the actual signed v1 pair and public material', () => {
  const v1 = JSON.parse(
    readFileSync(
      new URL(
        '../network/fixtures/keyset-signature-vector.json',
        import.meta.url,
      ),
    ),
  );
  const keys = v1.signed.keyset.keys
    .map(({ kid, purpose, public_key_spki, not_before, not_after }) => ({
      kid,
      purpose,
      public_key_spki,
      not_before,
      not_after,
    }))
    .sort((a, b) => (a.kid < b.kid ? -1 : a.kid > b.kid ? 1 : 0));
  const vector = fixture.boundary_cases.find(
    (x) => x.id === 'migration-real-v1-pair-and-material',
  );
  assert.deepEqual(vector.input.members, [
    {
      version: v1.signed.keyset.keyset_version,
      pair_digest: digest(v1.signed),
      material_digest: digest({ schema_version: 2, keys }),
    },
  ]);
  assert.equal(
    vector.input.permit.payload.queue_commitment_digest,
    digest(vector.input.members),
  );
});
