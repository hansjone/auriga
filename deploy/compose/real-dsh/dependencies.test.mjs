import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawnSync } from 'node:child_process';
import { test } from 'node:test';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  assertRuntimeBase,
  assertRuntimeImageArchive,
  dependencyFiles,
  dependencySources,
  hashDependencyFile,
  prepareRuntimeBase,
  runtimeBaseInput,
  sealDependencies,
  stageDependencies,
  verifyDependencies,
} from './dependencies.mjs';
import { productionInputs } from './inputs.mjs';

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const configBytes = Buffer.from(
  JSON.stringify({ os: 'linux', architecture: 'amd64', fixture: true }),
);
const config =
  'sha256:' + createHash('sha256').update(configBytes).digest('hex');
const hash = (x) => createHash('sha256').update(x).digest('hex');
function fixture(t) {
  const parent = resolve(tmpdir()),
    base = mkdtempSync(join(parent, 'auriga-runtime-deps-'));
  t.after(() => {
    assert(resolve(base).startsWith(parent + sep));
    rmSync(base, { recursive: true });
  });
  const put = (relative, value) => {
    const path = join(base, relative);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(
      path,
      typeof value === 'string' || value instanceof Uint8Array
        ? value
        : JSON.stringify(value),
    );
    return path;
  };
  for (const name of dependencySources) {
    const path = join(base, 'source', name);
    mkdirSync(dirname(path), { recursive: true });
    cpSync(join(repository, name), path);
  }
  for (const name of Object.values(dependencyFiles).filter(
    (x) => x !== dependencyFiles.manifest,
  ))
    put('inputs/' + name, 'fixture raw ' + name);
  const root = join(base, 'source'),
    inputs = join(base, 'inputs');
  const metadata = sealDependencies(root, inputs, config, 'a'.repeat(64)),
    sha = hashDependencyFile(join(inputs, dependencyFiles.manifest));
  return { base, root, inputs, metadata, sha, put };
}
function signedRoot(directory, privateKey) {
  const rows = [];
  function walk(path, prefix = '') {
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      const relative = prefix + entry.name,
        full = join(path, entry.name);
      if (entry.isDirectory()) walk(full, relative + '/');
      else if (!['SHA256SUMS', 'SHA256SUMS.sig'].includes(relative))
        rows.push([relative, hashDependencyFile(full)]);
    }
  }
  walk(directory);
  rows.sort(([a], [b]) => a.localeCompare(b));
  const sums = rows.map(([name, sha]) => sha + '  ' + name + '\n').join('');
  writeFileSync(join(directory, 'SHA256SUMS'), sums);
  writeFileSync(
    join(directory, 'SHA256SUMS.sig'),
    sign(null, Buffer.from(sums), privateKey).toString('base64') + '\n',
  );
}
function imageTar(tag, { manifestText, configValue = configBytes } = {}) {
  const chunks = [];
  for (const [name, bytes] of [
    [
      'manifest.json',
      Buffer.from(
        manifestText ??
          JSON.stringify([
            { Config: 'config.json', RepoTags: [tag], Layers: [] },
          ]),
      ),
    ],
    ['config.json', configValue],
  ]) {
    const header = Buffer.alloc(512);
    header.write(name);
    header.write('0000644\0', 100);
    header.write('0000000\0', 108);
    header.write('0000000\0', 116);
    header.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124);
    header.write('00000000000\0', 136);
    header.fill(32, 148, 156);
    header.write('0', 156);
    header.write('ustar\0', 257);
    header.write(
      header
        .reduce((sum, byte) => sum + byte, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    chunks.push(
      header,
      bytes,
      Buffer.alloc((512 - (bytes.length % 512)) % 512),
    );
  }
  return Buffer.concat([...chunks, Buffer.alloc(1024)]);
}
function attachKit(f, { cumulative = false, site = false } = {}) {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519'),
    version = '0.2.0',
    commit = 'b'.repeat(40),
    lockDigest = 'c'.repeat(64);
  f.put(
    'source/release-public.pem',
    publicKey.export({ type: 'spki', format: 'pem' }),
  );
  f.put('source/auriga-source.json', {
    schemaVersion: 1,
    source: { upstreamVersion: version, commit },
    locks: { digest: lockDigest },
  });
  f.put('source/.auriga-devkit.json', {
    root: join(f.base, 'kit'),
    baseline: {
      version: site ? '0.2.0-site.1' : version,
      upstreamVersion: version,
      commit,
    },
    lockDigest,
  });
  const spec = {},
    files = {};
  for (const [key, name] of Object.entries(dependencyFiles)) {
    const raw = readFileSync(join(f.inputs, name)),
      sha = hash(raw),
      path = 'runtime-dependencies/' + name;
    spec[key] = { url: path, sha256: sha };
    files[path] = sha;
    f.put(cumulative ? 'kit/blobs/' + sha : 'kit/cache/' + path, raw);
  }
  const digest = f.metadata.nodeImage.ref.split('@sha256:')[1],
    tag = 'auriga/devkit-tool-runtime:' + digest.slice(0, 24),
    archive = 'images/tool-runtime.tar',
    imageSha = hash(imageTar(tag));
  files[archive] = imageSha;
  f.put(
    cumulative ? 'kit/blobs/' + imageSha : 'kit/cache/' + archive,
    imageTar(tag),
  );
  const kit = {
    schemaVersion: 1,
    locks: { digest: lockDigest },
    release: {
      build: {
        runtimeDependencies: spec,
        tools: { runtime: f.metadata.nodeImage.ref },
      },
    },
    images: [
      {
        role: 'tool-runtime',
        ref: f.metadata.nodeImage.ref,
        tag,
        file: archive,
      },
    ],
  };
  f.put(
    cumulative ? 'kit/devkit-catalog.json' : 'kit/devkit.json',
    cumulative
      ? { schemaVersion: 1, versions: { [version]: { commit, kit, files } } }
      : kit,
  );
  signedRoot(f.root, privateKey);
  signedRoot(join(f.base, 'kit'), privateKey);
  return { privateKey, kit, tag, spec };
}

test('closure envelope pins all current locks and fixed archives, rejecting changed bytes, extra input and no independent SHA', (t) => {
  const f = fixture(t);
  assert.equal(
    verifyDependencies(f.root, f.inputs, f.sha).nodeImage.config,
    config,
  );
  assert.throws(() => verifyDependencies(f.root, f.inputs), /SHA256 required/);
  f.put('inputs/npm-cache.tar.gz', 'tampered');
  assert.throws(
    () => verifyDependencies(f.root, f.inputs, f.sha),
    /archive bytes differ/,
  );
  f.put('inputs/extra', 'unlisted');
  assert.throws(
    () => verifyDependencies(f.root, f.inputs, f.sha),
    /extra files/,
  );
});
test('manifest-only exports changes require a new current source envelope, while original cache bytes can be reused', (t) => {
  const f = fixture(t),
    manifest = join(f.root, 'clients/dsh-plugin/package.json'),
    pkg = JSON.parse(readFileSync(manifest));
  pkg.exports['./test-entry'] = './src/test.js';
  writeFileSync(manifest, JSON.stringify(pkg));
  assert.throws(
    () => verifyDependencies(f.root, f.inputs, f.sha),
    /source locks/,
  );
  rmSync(join(f.inputs, dependencyFiles.manifest));
  sealDependencies(f.root, f.inputs, config, 'a'.repeat(64));
  const next = hashDependencyFile(join(f.inputs, dependencyFiles.manifest));
  assert.notEqual(next, f.sha);
  assert.equal(
    verifyDependencies(f.root, f.inputs, next).files.corepack.sha256,
    f.metadata.files.corepack.sha256,
  );
});
test('explicit preparation inputs require an approved manifest digest before staging any bytes', (t) => {
  const f = fixture(t),
    destination = join(f.base, 'context');
  assert.throws(
    () => stageDependencies(f.root, destination, { directory: f.inputs }),
    /SHA256 required/,
  );
  assert(!existsSync(destination));
  stageDependencies(f.root, destination, {
    directory: f.inputs,
    manifestSha256: f.sha,
  });
  assert.equal(
    hashDependencyFile(join(destination, dependencyFiles.manifest)),
    f.sha,
  );
});
for (const cumulative of [false, true])
  test(
    'signed matching ' +
      (cumulative ? 'cumulative' : 'standalone') +
      ' kit stages exact inputs and binds the original image mapping',
    (t) => {
      const f = fixture(t),
        k = attachKit(f, { cumulative });
      const metadata = stageDependencies(f.root, join(f.base, 'context'), {
        directory: undefined,
        manifestSha256: undefined,
      });
      const selected = runtimeBaseInput(f.root, metadata, {
        nodeImage: undefined,
      });
      assert.equal(selected.from, k.tag);
      assert.equal(selected.config, config);
      assert.equal(selected.manifest, f.metadata.nodeImage.ref.split('@')[1]);
      assert(existsSync(selected.archive));
      const cache = cumulative
        ? join(f.base, 'kit/blobs', k.spec.npmCache.sha256)
        : join(f.base, 'kit/cache', k.spec.npmCache.url);
      writeFileSync(cache, 'tampered');
      assert.throws(
        () =>
          stageDependencies(f.root, join(f.base, 'bad-context'), {
            directory: undefined,
          }),
        /SHA256|differs/,
      );
    },
  );
test('unsigned or wrong-version kit and unreviewed local tag are rejected, with no self-issued cache key fallback', (t) => {
  const f = fixture(t),
    k = attachKit(f);
  f.put('kit/SHA256SUMS.sig', 'A'.repeat(86) + '==');
  assert.throws(
    () =>
      stageDependencies(f.root, join(f.base, 'no-signature'), {
        directory: undefined,
      }),
    /signature/,
  );
  signedRoot(join(f.base, 'kit'), k.privateKey);
  k.kit.images[0].tag = 'node:latest';
  f.put('kit/devkit.json', k.kit);
  signedRoot(join(f.base, 'kit'), k.privateKey);
  assert.throws(
    () => runtimeBaseInput(f.root, f.metadata, { nodeImage: undefined }),
    /base image/,
  );
  assert.throws(
    () => runtimeBaseInput(f.root, f.metadata, { nodeImage: 'node:22-latest' }),
    /pinned runtime source/,
  );
});
test('site source selects its pinned upstream kit exactly as the Go loader does', (t) => {
  const f = fixture(t);
  attachKit(f, { cumulative: true, site: true });
  const metadata = stageDependencies(f.root, join(f.base, 'site-context'), {
    directory: null,
    verifyKey: null,
  });
  assert.equal(metadata.nodeImage.config, config);
  const bindingFile = join(f.root, '.auriga-devkit.json'),
    binding = JSON.parse(readFileSync(bindingFile));
  binding.baseline.upstreamVersion = 'uncovered';
  writeFileSync(bindingFile, JSON.stringify(binding));
  assert.throws(
    () =>
      stageDependencies(f.root, join(f.base, 'wrong-site'), {
        directory: null,
        verifyKey: null,
      }),
    /source binding differs/,
  );
});
test('classic config and containerd manifest identities remain distinct and only their reviewed mapping is accepted', () => {
  const expected = { config, manifest: 'sha256:' + 'd'.repeat(64) },
    base = { Os: 'linux', Architecture: 'amd64' };
  assert.equal(assertRuntimeBase({ ...base, Id: config }, expected), 'config');
  assert.equal(
    assertRuntimeBase({ ...base, Id: expected.manifest }, expected),
    'manifest',
  );
  for (const actual of [
    { ...base, Id: 'sha256:' + 'e'.repeat(64) },
    { ...base, Id: config, Architecture: 'arm64' },
  ])
    assert.throws(
      () => assertRuntimeBase(actual, expected),
      /approved manifest\/config/,
    );
});
function maintenanceInput(f, data) {
  const pin = f.metadata.nodeImage.ref.split('@sha256:')[1],
    version = f.metadata.nodeVersion;
  const tag = `auriga-input/node-runtime:${version}-${pin.slice(0, 8)}`;
  const archive = f.put(
    `node-runtime-${version}-${pin.slice(0, 8)}.tar`,
    data ?? imageTar(tag),
  );
  return {
    tag,
    options: {
      nodeImage: null,
      nodeArchive: archive,
      nodeArchiveSha256: hashDependencyFile(archive),
      dependenciesDirectory: f.inputs,
      dependenciesSha256: f.sha,
    },
  };
}
test('explicit maintenance archive binds approvals, source basename, exact tag and config without altering canonical selection', (t) => {
  const f = fixture(t),
    m = maintenanceInput(f);
  const base = runtimeBaseInput(f.root, f.metadata, m.options);
  assert.equal(base.from, m.tag);
  assert.equal(base.archive, m.options.nodeArchive);
  assert.equal(
    runtimeBaseInput(f.root, f.metadata, {
      nodeImage: f.metadata.nodeImage.ref,
      nodeArchive: null,
      nodeArchiveSha256: null,
    }).from,
    f.metadata.nodeImage.ref,
  );
  for (const patch of [
    { nodeArchiveSha256: 'f'.repeat(64) },
    { dependenciesSha256: null },
    { dependenciesDirectory: null },
    { nodeArchive: 'relative.tar' },
    { nodeImage: f.metadata.nodeImage.ref },
  ])
    assert.throws(
      () => runtimeBaseInput(f.root, f.metadata, { ...m.options, ...patch }),
      /approval|SHA256|without NODE_IMAGE/,
    );
  const alias = f.put('wrong-name.tar', readFileSync(m.options.nodeArchive));
  assert.throws(
    () =>
      runtimeBaseInput(f.root, f.metadata, {
        ...m.options,
        nodeArchive: alias,
      }),
    /pin or path/,
  );
  const changed = globalThis.structuredClone(f.metadata);
  changed.nodeImage.config = 'sha256:' + 'f'.repeat(64);
  assert.throws(
    () => runtimeBaseInput(f.root, changed, m.options),
    /envelope differs/,
  );
});
test('maintenance archive rejects wrong role, config, duplicate keys, duplicate tags and duplicate tar metadata even with its supplied SHA', (t) => {
  const f = fixture(t),
    m = maintenanceInput(f),
    good = imageTar(m.tag);
  const duplicate = JSON.stringify([
    { Config: 'config.json', RepoTags: [m.tag], Layers: [] },
  ]).replace(
    '"Config":"config.json"',
    '"Config":"config.json","Config":"config.json"',
  );
  const cases = [
    imageTar('unexpected:role'),
    imageTar(m.tag, {
      configValue: Buffer.from('{"os":"linux","architecture":"arm64"}'),
    }),
    imageTar(m.tag, { manifestText: duplicate }),
    imageTar(m.tag, {
      manifestText: JSON.stringify([
        { Config: 'config.json', RepoTags: [m.tag, m.tag], Layers: [] },
      ]),
    }),
    Buffer.concat([
      good.subarray(0, -1024),
      good.subarray(0, 1024),
      Buffer.alloc(1024),
    ]),
  ];
  for (const data of cases) {
    writeFileSync(m.options.nodeArchive, data);
    assert.throws(
      () =>
        runtimeBaseInput(f.root, f.metadata, {
          ...m.options,
          nodeArchiveSha256: hash(data),
        }),
      /tag\/config|config differs|duplicate|tar member/,
    );
  }
});
test('public caller base assembly loads only the verified maintenance archive and verifies the resulting fixed tag', (t) => {
  const f = fixture(t),
    m = maintenanceInput(f),
    calls = [];
  let loaded = false;
  const run = (args, capture) => {
    calls.push({ args, capture });
    if (args[0] === 'load') {
      loaded = true;
      return 'loaded';
    }
    if (args[0] === 'image' && args[1] === 'inspect') {
      if (!loaded) throw new Error('absent');
      return JSON.stringify([
        { Id: config, Os: 'linux', Architecture: 'amd64' },
      ]);
    }
    throw new Error('unexpected Docker operation');
  };
  const result = prepareRuntimeBase(f.root, f.metadata, run, m.options);
  assert.equal(result.from, m.tag);
  assert.deepEqual(
    calls.map((x) => x.args),
    [
      ['image', 'inspect', m.tag],
      ['load', '-i', m.options.nodeArchive],
      ['image', 'inspect', m.tag],
    ],
  );
  assert.throws(
    () =>
      prepareRuntimeBase(
        f.root,
        f.metadata,
        () =>
          JSON.stringify([
            {
              Id: 'sha256:' + 'f'.repeat(64),
              Os: 'linux',
              Architecture: 'amd64',
            },
          ]),
        m.options,
      ),
    /approved manifest\/config/,
  );
});
test('approved original 79MB Node archive is accepted by maintenance assembly with its independent SHA and actual config', (t) => {
  const real = process.env.AURIGA_RUNTIME_REAL_NODE_ARCHIVE;
  if (!real)
    return t.skip(
      'set the approved original Node archive for the file-only integration check',
    );
  const f = fixture(t),
    realConfig =
      'sha256:daa74c183f7d8c1ba55ed79c76e912f50be8782ca9d2da640645f906dce474b8';
  rmSync(join(f.inputs, dependencyFiles.manifest));
  f.metadata = sealDependencies(f.root, f.inputs, realConfig, 'a'.repeat(64));
  f.sha = hashDependencyFile(join(f.inputs, dependencyFiles.manifest));
  const options = {
    nodeImage: null,
    nodeArchive: real,
    nodeArchiveSha256:
      '4dcd214a743ef522699ebbb0c7a41051fb08d4fd9158e86ed21b48dc48a4aee0',
    dependenciesDirectory: f.inputs,
    dependenciesSha256: f.sha,
  };
  const calls = [];
  const base = prepareRuntimeBase(
    f.root,
    f.metadata,
    (args) => {
      calls.push(args);
      return JSON.stringify([
        { Id: realConfig, Os: 'linux', Architecture: 'amd64' },
      ]);
    },
    options,
  );
  assert.equal(base.config, realConfig);
  assert.equal(base.from, 'auriga-input/node-runtime:22.19.0-cff78eb5');
  assert.equal(calls.length, 1);
  assert.equal(base.archive, resolve(real));
});
test('the actual image archive metadata reader rejects changed config, tag and tar checksums without extracting', (t) => {
  const f = fixture(t),
    file = f.put('image.tar', imageTar('fixture:reviewed'));
  assert.equal(
    assertRuntimeImageArchive(file, config, 'fixture:reviewed'),
    true,
  );
  assert.throws(
    () =>
      assertRuntimeImageArchive(
        file,
        'sha256:' + 'f'.repeat(64),
        'fixture:reviewed',
      ),
    /config differs/,
  );
  assert.throws(
    () => assertRuntimeImageArchive(file, config, 'fixture:wrong'),
    /tag\/config/,
  );
  const bytes = readFileSync(file);
  bytes[10] ^= 1;
  writeFileSync(file, bytes);
  assert.throws(
    () => assertRuntimeImageArchive(file, config, 'fixture:reviewed'),
    /checksum/,
  );
});
test('the preparation script emits the actual production-only npm input with valid newlines and never embeds the project broker', (t) => {
  const f = fixture(t),
    output = join(f.base, 'production-lock');
  mkdirSync(output);
  const script = readFileSync(
    join(repository, 'deploy/nas/offline-build/dsh-inputs.sh'),
    'utf8',
  ).replaceAll('\r\n', '\n');
  const program = script.split("<<'NODE'\n")[1]?.split('\nNODE\n')[0];
  assert(program);
  const result = spawnSync(
    process.execPath,
    [
      '--max-old-space-size=512',
      '--input-type=module',
      '-',
      repository,
      output,
    ],
    {
      input: program,
      encoding: 'utf8',
      env: { PATH: dirname(process.execPath) },
      windowsHide: true,
    },
  );
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  const json = (name) =>
    JSON.parse(readFileSync(join(repository, 'clients/dsh-plugin', name)));
  const expected = productionInputs(
    json('package.json'),
    json('package-lock.json'),
  );
  assert.deepEqual(
    JSON.parse(readFileSync(join(output, 'package.json'))),
    expected.pkg,
  );
  assert.deepEqual(
    JSON.parse(readFileSync(join(output, 'package-lock.json'))),
    expected.lock,
  );
  assert(
    readFileSync(join(output, 'package-lock.json'), 'utf8').endsWith('\n'),
  );
  assert.match(
    script,
    /tar -czf "\$out\/npm-cache.tar.gz" -C "\$cache" _cacache/,
  );
  assert.doesNotMatch(script, /package-broker\.mjs|shell-auth\/build\.mjs/);
});
test('full recipe and public caller have no network install fallback and always rebuild the current broker source', () => {
  const docker = readFileSync(
    join(repository, 'deploy/compose/real-dsh/Dockerfile'),
    'utf8',
  );
  for (const line of docker.split('\n').filter((x) => x.startsWith('RUN ')))
    assert.match(line, /^RUN --network=none /);
  assert.doesNotMatch(docker, /corepack prepare|--registry=https:/);
  assert.match(docker, /corepack install -g --cache-only/);
  assert.match(docker, /install --offline --frozen-lockfile --ignore-scripts/);
  assert.equal((docker.match(/npm ci --offline/g) ?? []).length, 3);
  assert.match(docker, /node clients\/shell-auth\/build.mjs/);
  const caller = readFileSync(
    join(repository, 'deploy/compose/scripts/build-real-dsh.mjs'),
    'utf8',
  );
  assert.match(caller, /'--network=none'/);
  assert.match(caller, /'--pull=false'/);
  assert.match(caller, /prepareRuntimeBase\(root, dependencies, docker\)/);
});
