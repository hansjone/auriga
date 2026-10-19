import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../../../', import.meta.url));
const output = path.join(root, 'services/contracts/target/bundled');
mkdirSync(output, { recursive: true });
for (const service of ['iam', 'org', 'workspace']) {
  const destination = path.join(output, service + '.json');
  const result = spawnSync(process.execPath, [
    path.join(root, 'services/contracts/node_modules/@redocly/cli/bin/cli.js'),
    'bundle', path.join(root, 'contracts/openapi', service + '.yaml'),
    '--config', path.join(root, 'contracts/redocly.yaml'), '--output', destination,
  ], { stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
  const spec = JSON.parse(readFileSync(destination, 'utf8'));
  for (const url of Object.keys(spec.paths)) {
    // These callbacks are implemented by hand, never generated.
    if (url.startsWith('/login/callback/')) delete spec.paths[url];
  }
  writeFileSync(destination, JSON.stringify(spec, null, 2) + '\n');
}
