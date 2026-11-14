// Maintainer tool: materialize the v1 schema from compact, explicit definitions.
import { writeFileSync } from 'node:fs';
import { format } from 'prettier';
const id = { type: 'string', format: 'uuid' };
const n = { type: 'integer', minimum: 1, maximum: Number.MAX_SAFE_INTEGER };
const time = { type: 'integer', minimum: 0, maximum: Number.MAX_SAFE_INTEGER };
const constant = (value) => ({ const: value, type: typeof value });
const enumeration = (...values) => ({ type: 'string', enum: values });
const ref = (name) => ({ $ref: '#/$defs/' + name });
const object = (properties, optional = []) => ({
  type: 'object',
  additionalProperties: false,
  required: Object.keys(properties).filter((k) => !optional.includes(k)),
  properties,
});
const binding = {
  tenantId: id,
  memberId: id,
  placementId: id,
  physicalInstanceId: id,
  mode: enumeration('shared', 'dedicated'),
  epoch: n,
  revision: n,
};
const session = {
  ...binding,
  sessionId: id,
  platformSessionId: id,
  authGeneration: n,
};
const lifetime = { issuedAt: time, expiresAt: time, jti: id };
const scope = enumeration(
  'credentials',
  'outbound',
  'model.invoke',
  'message.send',
  'message.receive',
);
const defs = {
  SignedHeader: object({
    alg: constant('EdDSA'),
    kid: { type: 'string', minLength: 1, maxLength: 128 },
    typ: enumeration(
      'auriga-shared-identity+jwt',
      'auriga-shared-ticket+jwt',
      'auriga-shared-capability+jwt',
    ),
  }),
  Binding: object(binding),
  SessionBinding: object(session),
  OpenTicket: object({
    schemaVersion: constant(1),
    binding: ref('Binding'),
    platformSessionId: id,
    audience: constant('auriga.placement.exchange'),
    ...lifetime,
  }),
  Identity: object({
    schemaVersion: constant(2),
    issuer: constant('auriga.edge'),
    audience: constant('auriga.shared.ingress'),
    binding: ref('Binding'),
    platformSessionId: id,
    method: enumeration(
      'GET',
      'POST',
      'PUT',
      'PATCH',
      'DELETE',
      'HEAD',
      'OPTIONS',
    ),
    canonicalPath: {
      type: 'string',
      pattern: '^/[^?#\\\\\\r\\n]*$',
      maxLength: 4096,
    },
    ...lifetime,
  }),
  Capability: object({
    schemaVersion: constant(1),
    binding: ref('SessionBinding'),
    taskId: id,
    audience: enumeration(
      'auriga.shared.enterprise',
      'auriga.shared.model',
      'auriga.shared.message',
    ),
    scopes: { type: 'array', minItems: 1, uniqueItems: true, items: scope },
    credentialVersion: n,
    ...lifetime,
  }),
  Revocation: object({
    schemaVersion: constant(1),
    tenantId: id,
    memberId: id,
    placementId: id,
    epoch: n,
    authGeneration: n,
    revision: n,
    reason: enumeration(
      'logout',
      'credential-invalid',
      'switch',
      'member-disabled',
    ),
    revokedAt: time,
  }),
  Resource: object({
    resourceId: id,
    digest: { type: 'string', pattern: '^sha256:[a-f0-9]{64}$' },
    leaseId: id,
    expiresAt: time,
  }),
  ResourceView: object({
    schemaVersion: constant(1),
    binding: ref('Binding'),
    viewId: id,
    authorizationSetDigest: {
      type: 'string',
      pattern: '^sha256:[a-f0-9]{64}$',
    },
    source: constant('platform'),
    resources: { type: 'array', items: ref('Resource') },
  }),
  ResourceReport: object({
    schemaVersion: constant(1),
    binding: ref('Binding'),
    viewId: id,
    status: enumeration('ready', 'rejected', 'stopped'),
    observedAt: time,
    reason: enumeration(
      'verified',
      'expired',
      'revoked',
      'authorization-mismatch',
      'cleanup-unconfirmed',
    ),
  }),
  RuntimeIntent: object({
    schemaVersion: constant(1),
    operationId: id,
    binding: ref('Binding'),
    action: enumeration(
      'prepare-empty',
      'cooperative-cancel',
      'stop-pool',
      'archive-reset',
      'discard-sessions',
    ),
    cancelWaitSeconds: constant(30),
    archiveRetentionSeconds: constant(2592000),
    workspacePolicy: constant('empty'),
    sessionPolicy: constant('discard'),
  }),
  RuntimeReceipt: object(
    {
      schemaVersion: constant(1),
      operationId: id,
      binding: ref('Binding'),
      receiptId: id,
      action: enumeration(
        'prepare-empty',
        'cooperative-cancel',
        'stop-pool',
        'archive-reset',
        'discard-sessions',
      ),
      outcome: enumeration('confirmed', 'unconfirmed'),
      observedAt: time,
      executionTreeStopped: { type: 'boolean' },
      sessionsDiscarded: { type: 'boolean' },
      workspaceEmpty: { type: 'boolean' },
      archiveId: id,
      archiveExpiresAt: time,
    },
    ['archiveId', 'archiveExpiresAt'],
  ),
  GuardianImpact: object({
    schemaVersion: constant(1),
    tenantId: id,
    physicalInstanceId: id,
    operationId: id,
    reason: enumeration(
      'resource-expired',
      'resource-revoked',
      'cancel-timeout',
      'credential-stop-unconfirmed',
      'crash',
    ),
    scope: constant('pool'),
    affected: { type: 'array', minItems: 1, items: ref('Binding') },
    state: enumeration('stopping', 'stopped', 'blocked'),
    observedAt: time,
  }),
};
writeFileSync(
  new URL('shared-dsh.schema.json', import.meta.url),
  await format(
    JSON.stringify(
      {
        $schema: 'https://json-schema.org/draft/2020-12/schema',
        $id: 'https://auriga.dev/contracts/shared-dsh/v1',
        title: 'Shared DSH v1 control and data plane',
        $defs: defs,
      },
      null,
      2,
    ) + '\n',
    { parser: 'json' },
  ),
);
const snake = (s) => s.replace(/[A-Z]/g, (c) => '_' + c.toLowerCase());
const protoType = (s) =>
  s.$ref
    ? 'SharedDsh' + s.$ref.split('/').at(-1)
    : s.type === 'integer'
      ? 'int64'
      : s.type === 'boolean'
        ? 'bool'
        : 'string';
const messages = Object.entries(defs)
  .map(([name, schema]) => {
    const fields = Object.entries(schema.properties).map(
      ([key, s], i) =>
        `  ${s.type === 'array' ? 'repeated ' + protoType(s.items) : protoType(s)} ${snake(key)} = ${i + 1};`,
    );
    return `// Wire validation and semantic constraints: contracts/shared-dsh/README.md.\nmessage SharedDsh${name} {\n${fields.join('\n')}\n}`;
  })
  .join('\n\n');
writeFileSync(
  new URL('../proto/auriga/node/v1/shared_dsh_contract.proto', import.meta.url),
  `// Generated by contracts/shared-dsh/build-contract.mjs; do not edit.\nsyntax = "proto3";\n\npackage auriga.node.v1;\n\noption go_package = "github.com/hansjone/auriga/infra/gen/auriga/node/v1;nodev1";\noption java_multiple_files = true;\noption java_package = "auriga.contracts.node.v1";\n\n${messages}\n`,
);
