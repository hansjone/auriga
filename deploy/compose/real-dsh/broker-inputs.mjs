import { createHash } from 'node:crypto';
import {
  cpSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const recipe = 'deploy/compose/real-dsh';
const plugin = 'clients/dsh-plugin';
const sources = [
  'tsconfig.base.json',
  ...[
    'build.mjs',
    'business.mjs',
    'device-provider.mjs',
    'host.mjs',
    'network-core.mjs',
    'network.mjs',
    'platform.mjs',
    'schema.mjs',
    'streams.mjs',
    'team.mjs',
    'vault.mjs',
    'windows/build.mjs',
  ].map((name) => 'clients/shell-auth/' + name),
  ...[
    'scripts/package-broker.mjs',
    'src/local/broker-package.js',
    'src/local/config.js',
    'src/local/platform-error.js',
    'tsconfig.json',
  ].map((name) => plugin + '/' + name),
  ...[
    'auth/standard-device',
    'auth/uac-control',
    'auth/uac-login-info',
    'auth/uac-session-binding',
    'identity/steps',
    'network/address',
    'network/auto-renewal-v2',
    'network/envelope',
    'network/keyset',
    'network/protocol',
    'network/register-result',
    'network/send',
    'network/source-control',
    'plugin/local-login',
    'plugin/managed-device',
    'plugin/managed-model',
    'plugin/shell-auth',
    'plugin/shell-auth-control',
    'plugin/shell-business',
  ].map((name) => 'contracts/' + name + '.schema.json'),
  'contracts/plugin/shell-business-operations.json',
];

// These trees contain the plugin side of the network-core bundle's import graph.
// The compiler gets only these trees and the explicit files above. A new import
// outside the snapshot requires extending this list rather than reusing a stale
// bundle when that file changes.
const sourceTrees = [
  'generated/network',
  'network',
  'observability',
  'settings',
].map((name) => plugin + '/src/' + name);
export const brokerInputs = [
  `${recipe}/Dockerfile`,
  `${recipe}/broker-inputs.mjs`,
  `${recipe}/versions.json`,
  'package.json',
  'pnpm-workspace.yaml',
  'web/pnpm-lock.yaml',
  `${plugin}/package.json`,
  ...sources,
  ...sourceTrees,
];

const sha256 = (bytes) => createHash('sha256').update(bytes).digest('hex');

// Operate only on the checked private context, never the checkout directly.
export function brokerSourceSha256(root) {
  const files = [];
  function walk(path) {
    const absolute = join(root, path);
    const stat = lstatSync(absolute);
    if (stat.isDirectory())
      for (const name of readdirSync(absolute).sort()) walk(path + '/' + name);
    else if (stat.isFile()) files.push([path, sha256(readFileSync(absolute))]);
    else throw new Error('Unexpected broker build input: ' + path);
  }
  for (const path of [...brokerInputs].sort()) walk(path);
  return sha256(JSON.stringify(files));
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  const [context, destination, keyFile] = process.argv.slice(2);
  const key = brokerSourceSha256(context);
  for (const path of [...sources, ...sourceTrees]) {
    const output = join(destination, path);
    mkdirSync(dirname(output), { recursive: true });
    cpSync(join(context, path), output, {
      recursive: sourceTrees.includes(path),
    });
  }
  mkdirSync(dirname(keyFile), { recursive: true });
  writeFileSync(keyFile, key + '\n');
}
