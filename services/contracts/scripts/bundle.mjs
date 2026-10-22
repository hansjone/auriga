import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../../../', import.meta.url));
const output = path.join(root, 'services/contracts/target/bundled');
mkdirSync(output, { recursive: true });
for (const service of ['iam', 'org', 'workspace', 'ai', 'resource']) {
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
  writeFileSync(destination, JSON.stringify(spec, null, 2) + '\n');
}
