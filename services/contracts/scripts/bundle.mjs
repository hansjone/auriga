import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../../../', import.meta.url));
const output = path.join(root, 'services/contracts/target/bundled');
mkdirSync(output, { recursive: true });
for (const service of [
  'network-keys-v2',
  'shared-dsh-edge',
  'iam',
  'shell-auth',
  'org',
  'workspace',
  'ai',
  'resource',
  'audit',
  'ops',
]) {
  const destination = path.join(output, service + '.json');
  const result = spawnSync(
    process.execPath,
    [
      path.join(
        root,
        'services/contracts/node_modules/@redocly/cli/bin/cli.js',
      ),
      'bundle',
      path.join(root, 'contracts/openapi', service + '.yaml'),
      '--config',
      path.join(root, 'contracts/redocly.yaml'),
      '--output',
      destination,
    ],
    { stdio: 'inherit' },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
  const spec = JSON.parse(readFileSync(destination, 'utf8'));
  for (const url of Object.keys(spec.paths)) {
    // These callbacks are implemented by hand, never generated.
    if (url.startsWith('/login/callback/')) delete spec.paths[url];
  }
  // Signed UTC strings are part of the exact JCS bytes. Mapping them to a runtime
  // date object could normalize fractional precision on serialization. Keep the
  // source schema's date-time validation, but generate string fields for this marker.
  function preserveSignatureText(value) {
    if (!value || typeof value !== 'object') return;
    if (value.$comment === 'auriga-preserve-signature-text')
      delete value.format;
    for (const child of Object.values(value)) preserveSignatureText(child);
  }
  preserveSignatureText(spec);
  if (service === 'workspace') {
    // Generator 7.26.0 renders a boolean const as an enum with String values,
    // which does not compile. Preserve the original wire/schema contract and
    // project the equivalent bean validation constraint for Java generation.
    function projectBooleanConstants(value) {
      if (!value || typeof value !== 'object') return;
      if (value.type === 'boolean' && typeof value.const === 'boolean') {
        value['x-field-extra-annotation'] = value.const
          ? '@jakarta.validation.constraints.AssertTrue'
          : '@jakarta.validation.constraints.AssertFalse';
        delete value.const;
      }
      for (const child of Object.values(value)) projectBooleanConstants(child);
    }
    projectBooleanConstants(spec);
  }
  if (service === 'network-keys-v2') {
    const javaSpec = structuredClone(spec);
    const models = javaSpec.components.schemas;
    // OpenAPI Generator merges oneOf enums by last-branch-wins. Generate a
    // structural superset; validate the original wire oneOf at the boundary.
    const published = models.RenewalStatus;
    const unpublished = models.UnpublishedRenewalStatus;
    models.RenewalStatusOutput = {
      type: 'object',
      properties: { ...unpublished.properties, ...published.properties },
      required: published.required.filter((name) =>
        unpublished.required.includes(name),
      ),
      additionalProperties: false,
    };
    writeFileSync(
      path.join(output, 'network-keys-v2-java.json'),
      JSON.stringify(javaSpec, null, 2) + '\n',
    );
  }
  writeFileSync(destination, JSON.stringify(spec, null, 2) + '\n');
}
