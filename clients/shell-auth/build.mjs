import {
  mkdirSync,
  readdirSync,
  copyFileSync,
  readFileSync,
  writeFileSync,
  existsSync,
} from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { safePath } from './vault.mjs';
import { buildHelper } from './windows/build.mjs';
const source = dirname(fileURLToPath(import.meta.url));
export function buildBroker(output) {
  output = resolve(output);
  safePath(output);
  if (output === source || (existsSync(output) && readdirSync(output).length))
    throw new Error('empty broker output directory required');
  mkdirSync(output, { recursive: true });
  const files = readdirSync(source).filter(
    (f) =>
      f.endsWith('.mjs') &&
      ![
        'build.mjs',
        'eslint.config.mjs',
        'network-core.mjs',
        'device-provider.mjs',
      ].includes(f),
  );
  for (const part of ['public', 'windows']) {
    mkdirSync(join(output, part), { recursive: true });
    for (const file of readdirSync(join(source, part)))
      if (part === 'public') files.push(part + '/' + file);
  }
  for (const file of files)
    copyFileSync(join(source, file), join(output, file));
  const helper = 'windows/shell-helper.exe';
  buildHelper(join(output, helper));
  files.push(helper);
  const rootLoader = createRequire(
    new URL('../../package.json', import.meta.url),
  );
  const esbuild = createRequire(rootLoader.resolve('vite/package.json'))(
    'esbuild',
  );
  esbuild.buildSync({
    entryPoints: [join(source, 'network-core.mjs')],
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node22',
    outfile: join(output, 'network-core.bundle.mjs'),
    banner: {
      js: "import {createRequire as networkRequire} from 'node:module'; const require=networkRequire(import.meta.url);",
    },
    external: ['bufferutil', 'utf-8-validate'],
  });
  files.push('network-core.bundle.mjs');
  for (const part of ['auth', 'plugin']) {
    mkdirSync(join(output, 'schemas', part), { recursive: true });
    for (const name of part === 'auth'
      ? [
          'uac-login-info',
          'uac-session-binding',
          'uac-control',
          'standard-device',
        ]
      : ['shell-auth', 'shell-auth-control', 'shell-business']) {
      const file = `schemas/${part}/${name}.schema.json`;
      copyFileSync(
        join(source, '../../contracts', part, name + '.schema.json'),
        join(output, file),
      );
      files.push(file);
    }
  }
  const operations = 'schemas/plugin/shell-business-operations.json';
  copyFileSync(
    join(source, '../../contracts/plugin/shell-business-operations.json'),
    join(output, operations),
  );
  files.push(operations);
  mkdirSync(join(output, 'schemas', 'network'), { recursive: true });
  for (const name of [
    'register-result',
    'address',
    'envelope',
    'source-control',
    'protocol',
    'keyset',
    'auto-renewal-v2',
    'send',
  ]) {
    const file = 'schemas/network/' + name + '.schema.json';
    copyFileSync(
      join(source, '../../contracts/network', name + '.schema.json'),
      join(output, file),
    );
    files.push(file);
  }
  const loader = createRequire(
    new URL('../dsh-plugin/package.json', import.meta.url),
  );
  // npm pack excludes nested node_modules. Bundle the pinned validator so both
  // distributed consumers contain the complete manifest closure.
  esbuild.buildSync({
    entryPoints: [loader.resolve('ajv/dist/2020.js')],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node22',
    outfile: join(output, 'vendor-ajv.cjs'),
  });
  files.push('vendor-ajv.cjs');
  const dependencyLoader = createRequire(loader.resolve('ajv/package.json'));
  for (const name of [
    'ajv',
    'fast-deep-equal',
    'fast-uri',
    'json-schema-traverse',
    'require-from-string',
  ]) {
    const packageDirectory = dirname(
      (name === 'ajv' ? loader : dependencyLoader).resolve(
        name + '/package.json',
      ),
    );
    const relative = 'licenses/' + name + '.txt';
    mkdirSync(join(output, 'licenses'), { recursive: true });
    const license = readdirSync(packageDirectory).find((file) =>
      /^licen[sc]e(?:\.txt|\.md)?$/i.test(file),
    );
    if (!license)
      throw new Error('validator dependency license required: ' + name);
    copyFileSync(join(packageDirectory, license), join(output, relative));
    files.push(relative);
  }

  const manifest = {
    version: 1,
    files: files.sort().map((path) => ({
      path,
      sha256: createHash('sha256')
        .update(readFileSync(join(output, path)))
        .digest('hex'),
    })),
  };
  writeFileSync(
    join(output, 'broker-manifest.json'),
    JSON.stringify(manifest, null, 2),
  );
  return manifest;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  buildBroker(process.argv[2]);
