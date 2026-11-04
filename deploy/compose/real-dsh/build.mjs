import { createHash } from 'node:crypto';
import {
  readFileSync,
  writeFileSync,
  readdirSync,
  lstatSync,
  chmodSync,
} from 'node:fs';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { productionInputs } from './inputs.mjs';
import {
  runtimeInputs,
  runtimeChecks,
  pluginPackageFiles,
} from './runtime.mjs';

const hash = (value) => createHash('sha256').update(value).digest('hex');
const json = (path) => JSON.parse(readFileSync(path, 'utf8'));
const versions = json('/opt/fixture/versions.json');
const action = process.argv[2];
if (action === 'check') {
  if (
    process.platform !== 'linux' ||
    process.arch !== 'x64' ||
    process.versions.node !== versions.nodeVersion ||
    execFileSync('npm', ['--version'], { encoding: 'utf8' }).trim() !==
      versions.npmVersion
  ) {
    throw new Error('Use the pinned Linux x64 Node/npm toolchain');
  }
  if (
    hash(readFileSync('/opt/dsh/package-lock.json')) !== versions.lockSha256
  ) {
    throw new Error('DSH lock SHA-256 mismatch');
  }
  runtimeInputs(
    json('/opt/dsh/package.json'),
    json('/opt/dsh/package-lock.json'),
    versions,
    json('/opt/fixture/plugin-package.json'),
  );
} else if (action === 'plugin-lock') {
  const { pkg, lock } = productionInputs(
    json('package.json'),
    json('package-lock.json'),
  );
  // The repository lock has stale devDependency ranges. Derive a production-only
  // lock without resolving or changing any production version/integrity.
  writeFileSync(
    '/opt/fixture/plugin-source-lock.sha256',
    hash(readFileSync('package-lock.json')),
  );
  writeFileSync('package.json', JSON.stringify(pkg, null, 2) + '\n');
  writeFileSync('package-lock.json', JSON.stringify(lock, null, 2) + '\n');
} else if (action === 'manifest') {
  const pluginFiles = pluginPackageFiles('/opt/auriga-dsh');
  const runtime = await runtimeChecks(
    '/opt/dsh',
    versions,
    json('/opt/auriga-dsh/package.json'),
  );
  const files = [];
  function walk(dir) {
    for (const name of readdirSync(dir).sort()) {
      const path = join(dir, name);
      if (path === '/opt/fixture/manifest.json') continue;
      const st = lstatSync(path);
      if (st.isSymbolicLink()) continue;
      if (st.isDirectory()) {
        walk(path);
        continue;
      }
      // Same executable normalization as S4 (npm install scripts stay disabled).
      if (
        /(?:^|\/)(?:bin|prebuilds?|native)(?:\/|$)/.test(path) ||
        /spawn-helper$/.test(path)
      )
        chmodSync(path, 0o755);
      files.push({
        path: path.slice('/opt/'.length),
        sha256: hash(readFileSync(path)),
      });
    }
  }
  walk('/opt/dsh');
  walk('/opt/auriga-dsh');
  walk('/opt/fixture');
  for (const path of pluginFiles) {
    if (!files.some((file) => file.path === `auriga-dsh/${path}`))
      throw new Error(
        `Declared plugin entry is missing from manifest: ${path}`,
      );
  }
  writeFileSync(
    '/opt/fixture/manifest.json',
    JSON.stringify(
      {
        ...versions,
        dshVersion: runtime.dshVersion,
        runtimePackages: runtime.packages,
        publicCLI: runtime.publicCLI,
        admissionMethods: runtime.admissionMethods,
        nativeModules: runtime.nativeModules,
        pluginVersion: json('/opt/auriga-dsh/package.json').version,
        pluginSourceLockSha256: readFileSync(
          '/opt/fixture/plugin-source-lock.sha256',
          'utf8',
        ),
        pluginProductionLockSha256: hash(
          readFileSync('/opt/auriga-dsh/package-lock.json'),
        ),
        files,
      },
      null,
      2,
    ) + '\n',
  );
} else throw new Error('Unknown build action');
