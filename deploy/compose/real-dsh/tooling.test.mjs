import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  findToolingTarball,
  installedTooling,
  toolingInputs,
  verifyToolingTarball,
  runtimePnpmEntry,
} from './tooling.mjs';
import {
  commonInputs,
  withRealDshContext,
} from '../scripts/real-dsh-context.mjs';

const recipe = dirname(fileURLToPath(import.meta.url));
const upstream = JSON.parse(
  readFileSync(join(recipe, '../../../clients/installer/upstream.json')),
);
const pkg = JSON.parse(readFileSync(join(recipe, 'tooling/package.json')));
const lock = JSON.parse(
  readFileSync(join(recipe, 'tooling/package-lock.json')),
);
const hash = (bytes, algorithm = 'sha256') =>
  createHash(algorithm)
    .update(bytes)
    .digest(algorithm === 'sha512' ? 'base64' : 'hex');
function fixture(t) {
  const parent = resolve(tmpdir()),
    root = mkdtempSync(join(parent, 'auriga-tooling-'));
  t.after(() => {
    assert(resolve(root).startsWith(parent + sep));
    rmSync(root, { recursive: true });
  });
  const put = (name, value) => {
    const file = join(root, name);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(
      file,
      typeof value === 'string' ? value : JSON.stringify(value),
    );
    return file;
  };
  return { root, put };
}

test('runtime lock reuses exact installer pnpm distribution, separately from build Corepack', () => {
  const pin = toolingInputs(pkg, lock, upstream);
  assert.equal(pin.version, '11.28.5');
  assert.equal(pin.integrity, upstream.pnpmIntegrity);
  for (const change of [
    (x) => {
      x.packages['node_modules/pnpm'].version = '11.28.6';
    },
    (x) => {
      x.packages['node_modules/pnpm'].resolved =
        'https://example.com/unreviewed.tgz';
    },
    (x) => {
      x.packages['node_modules/other'] = { version: '1.0.0' };
    },
  ]) {
    const bad = globalThis.structuredClone(lock);
    change(bad);
    assert.throws(() => toolingInputs(pkg, bad, upstream), /pin mismatch/);
  }
});

test('original distribution bytes require the exact SHA512; absence, tampering and relative inputs fail', (t) => {
  const f = fixture(t),
    input = f.put('original.tgz', 'fixture original distribution');
  const pin = { integrity: 'sha512-' + hash(readFileSync(input), 'sha512') };
  assert.equal(
    verifyToolingTarball(input, pin).sha256,
    hash(readFileSync(input)),
  );
  f.put('original.tgz', 'tampered');
  assert.throws(() => verifyToolingTarball(input, pin), /integrity/);
  assert.throws(() => findToolingTarball(f.root, 'relative.tgz'), /absolute/);
  assert.throws(
    () => findToolingTarball(f.root, join(f.root, 'missing')),
    /ENOENT/,
  );
});

test('matching standalone and cumulative source+kit bindings locate the pinned original, never a tool command', (t) => {
  const f = fixture(t),
    source = f.put('source/.auriga-devkit.json', {
      root: join(f.root, 'kit'),
      baseline: { version: '0.2.0' },
    });
  const input = f.put(
      'kit/cache/runtime-tooling.tgz',
      'original registry bytes',
    ),
    sha = hash(readFileSync(input));
  f.put('kit/devkit.json', {
    release: {
      build: { runtimeTooling: { url: 'runtime-tooling.tgz', sha256: sha } },
    },
  });
  assert.equal(findToolingTarball(dirname(source)), input);
  f.put('kit/blobs/' + sha, 'original registry bytes');
  f.put('kit/devkit-catalog.json', {
    versions: {
      '0.2.0': {
        files: { 'runtime-tooling.tgz': sha },
        kit: { release: { build: { runtimeTooling: { sha256: sha } } } },
      },
    },
  });
  assert.equal(
    findToolingTarball(dirname(source)),
    join(f.root, 'kit/blobs', sha),
  );
  f.put('source/.auriga-devkit.json', {
    root: join(f.root, 'kit'),
    baseline: { version: '0.2.0-site.1', upstreamVersion: '0.2.0' },
  });
  assert.equal(
    findToolingTarball(dirname(source)),
    join(f.root, 'kit/blobs', sha),
  );
  f.put('kit/blobs/' + sha, 'tampered');
  assert.throws(() => findToolingTarball(dirname(source)), /bytes differ/);
  f.put('source/.auriga-devkit.json', {
    root: join(f.root, 'kit'),
    baseline: { version: 'uncovered' },
  });
  assert.throws(() => findToolingTarball(dirname(source)), /matching devkit/);
});

test('installed check binds exact current Node and fixed pnpm entry with no ambient credential environment', (t) => {
  const f = fixture(t);
  f.put('package.json', pkg);
  f.put('package-lock.json', lock);
  f.put('node_modules/pnpm/package.json', {
    name: 'pnpm',
    version: upstream.pnpm,
  });
  const entry = f.put(runtimePnpmEntry, 'fixture code never executed');
  const calls = [];
  const result = installedTooling(f.root, upstream, {
    run: (command, args, options) => {
      calls.push({ command, args, options });
      return upstream.pnpm + '\n';
    },
  });
  assert.equal(result.entry, runtimePnpmEntry);
  assert.equal(calls[0].command, process.execPath);
  assert.deepEqual(calls[0].args, [entry, '--version']);
  assert.equal(calls[0].options.env.COREPACK_ENABLE_NETWORK, '0');
  assert.deepEqual(
    Object.keys(calls[0].options.env).sort(),
    [
      'COREPACK_ENABLE_NETWORK',
      'HOME',
      'PATH',
      'PNPM_DISABLE_SELF_UPDATE_CHECK',
      'npm_config_update_notifier',
    ].sort(),
  );
  assert.throws(
    () => installedTooling(f.root, upstream, { run: () => '11.28.6' }),
    /version differs/,
  );
  rmSync(entry);
  assert.throws(
    () => installedTooling(f.root, upstream, { run: () => upstream.pnpm }),
    /ENOENT/,
  );
});

test('production recipe copies the verified offline tool into manifest scope, while build inputs retain pins', () => {
  const docker = readFileSync(join(recipe, 'Dockerfile'), 'utf8');
  assert.match(docker, /FROM \$\{NODE_IMAGE\} AS runtime-tooling/);
  assert.match(
    docker,
    /RUN --network=none node \/opt\/fixture\/tooling.mjs input/,
  );
  assert.match(docker, /npm ci --offline --ignore-scripts --bin-links=false/);
  assert.match(
    docker,
    /COPY --from=runtime-tooling \/opt\/auriga-tools \/opt\/auriga-tools/,
  );
  const stage = docker
    .split('FROM ${NODE_IMAGE} AS runtime-tooling')[1]
    .split('FROM toolchain AS broker')[0];
  assert.doesNotMatch(stage, /corepack|npm install|https?:\/\//);
  assert(
    commonInputs.includes('deploy/compose/real-dsh/tooling/package-lock.json'),
  );
  assert(commonInputs.includes('clients/installer/upstream.json'));
  assert.match(
    readFileSync(join(recipe, 'build.mjs'), 'utf8'),
    /walk\('\/opt\/auriga-tools'\)/,
  );
});

test('the actual checked full-build context rejects wrong original bytes before any Docker callback', async (t) => {
  const f = fixture(t),
    key = 'AURIGA_REAL_DSH_TOOLING_TARBALL',
    prior = process.env[key];
  process.env[key] = f.put('wrong.tgz', 'wrong original distribution');
  t.after(() => {
    if (prior === undefined) delete process.env[key];
    else process.env[key] = prior;
  });
  let builds = 0;
  await assert.rejects(
    withRealDshContext(
      resolve(recipe, '../../..'),
      ['deploy/compose/real-dsh/Dockerfile', ...commonInputs],
      () => {
        builds++;
      },
    ),
    (error) =>
      /offline runtime pnpm/.test(error.message) &&
      /integrity/.test(error.cause?.message),
  );
  assert.equal(builds, 0);
});
