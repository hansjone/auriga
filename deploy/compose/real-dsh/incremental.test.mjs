import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { productionInputs } from './inputs.mjs';
import { prepareIncremental } from './incremental.mjs';
import { brokerInputs } from './broker-inputs.mjs';
import { runtimePnpmEntry } from './tooling.mjs';

const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const repository = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const scratch = join(repository, '.dev/incremental-tests');
mkdirSync(scratch, { recursive: true });
const baseImage =
  'registry.example.com/auriga/runtime@sha256:' + 'a'.repeat(64);
const toolchain = {
  platform: 'linux',
  arch: 'x64',
  nodeVersion: '22.19.0',
  npmVersion: '10.9.3',
};

function fixture(t) {
  const directory = mkdtempSync(join(scratch, 'case-'));
  t.after(() => {
    assert.ok(resolve(directory).startsWith(resolve(scratch) + sep));
    rmSync(directory, { recursive: true });
  });
  const opt = join(directory, 'opt');
  const expected = join(directory, 'expected');
  const put = (path, data) => {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(
      path,
      typeof data === 'string' ? data : JSON.stringify(data, null, 2) + '\n',
    );
  };
  const pkg = {
    name: 'auriga-dsh',
    version: '0.1.0',
    type: 'module',
    engines: { dsh: '>=0.2.1-rc.3' },
    dependencies: { fixture: '1.0.0' },
    devDependencies: { test: '1.0.0' },
    scripts: { test: 'unused' },
  };
  const lock = {
    name: 'auriga-dsh',
    version: pkg.version,
    lockfileVersion: 3,
    packages: {
      '': {
        name: pkg.name,
        version: pkg.version,
        dependencies: pkg.dependencies,
      },
      'node_modules/fixture': {
        version: '1.0.0',
        integrity: 'sha512-YQ==',
        resolved: 'https://registry.npmjs.org/fixture/-/fixture-1.0.0.tgz',
      },
      'node_modules/test': { dev: true, version: '1.0.0' },
    },
  };
  const runtimePkg = { dependencies: { '@deepseek-ai/dsh': '0.2.1-rc.3' } };
  const runtimeLock = {
    packages: {
      '': runtimePkg,
      'node_modules/@deepseek-ai/dsh': {
        version: '0.2.1-rc.3',
        integrity: 'sha512-YQ==',
        resolved:
          'https://registry.npmjs.org/@deepseek-ai/dsh/-/dsh-0.2.1-rc.3.tgz',
      },
    },
  };
  put(join(expected, 'plugin/package.json'), pkg);
  put(join(expected, 'plugin/package-lock.json'), lock);
  put(join(expected, 'recipe/package.json'), runtimePkg);
  put(join(expected, 'recipe/package-lock.json'), runtimeLock);
  const versions = {
    dshVersion: '0.2.1-rc.3',
    ...{ nodeVersion: toolchain.nodeVersion, npmVersion: toolchain.npmVersion },
    lockSha256: hash(readFileSync(join(expected, 'recipe/package-lock.json'))),
  };
  put(join(expected, 'recipe/versions.json'), versions);
  for (const name of [
    'build.mjs',
    'inputs.mjs',
    'runtime.mjs',
    'start.mjs',
    'tooling.mjs',
  ])
    put(join(expected, 'recipe', name), '// unchanged foundation ' + name);
  for (const name of ['package.json', 'package-lock.json'])
    put(
      join(opt, 'dsh', name),
      readFileSync(join(expected, 'recipe', name)).toString(),
    );
  for (const name of [
    'versions.json',
    'build.mjs',
    'inputs.mjs',
    'runtime.mjs',
    'start.mjs',
    'tooling.mjs',
  ])
    put(
      join(opt, 'fixture', name),
      readFileSync(join(expected, 'recipe', name)).toString(),
    );
  put(
    join(opt, 'fixture/plugin-package.json'),
    readFileSync(join(expected, 'plugin/package.json')).toString(),
  );
  put(
    join(opt, 'fixture/plugin-source-lock.sha256'),
    hash(readFileSync(join(expected, 'plugin/package-lock.json'))),
  );
  const toolingRoot = join(opt, 'auriga-tools/pnpm');
  const runtimePins = JSON.parse(
    readFileSync(join(repository, 'clients/installer/upstream.json')),
  );
  put(join(expected, 'runtime-pins.json'), runtimePins);
  put(join(opt, 'fixture/runtime-pins.json'), runtimePins);
  for (const name of ['package.json', 'package-lock.json']) {
    const content = readFileSync(
      join(repository, 'deploy/compose/real-dsh/tooling', name),
      'utf8',
    );
    put(join(expected, 'recipe/tooling', name), content);
    put(join(toolingRoot, name), content);
  }
  put(join(toolingRoot, 'node_modules/pnpm/package.json'), {
    name: 'pnpm',
    version: runtimePins.pnpm,
  });
  put(
    join(toolingRoot, runtimePnpmEntry),
    `process.stdout.write(${JSON.stringify(runtimePins.pnpm)});`,
  );
  const production = productionInputs(pkg, lock);
  put(join(opt, 'auriga-dsh/package.json'), production.pkg);
  put(join(opt, 'auriga-dsh/package-lock.json'), production.lock);
  put(join(opt, 'auriga-dsh/node_modules/fixture/package.json'), {
    name: 'fixture',
    version: '1.0.0',
  });
  put(join(opt, 'auriga-dsh/src/removed.js'), 'old removed source');
  put(join(opt, 'auriga-dsh/src/entry.js'), 'old source');
  put(
    join(expected, 'recipe/broker-source.sha256'),
    hash('broker sources') + '\n',
  );
  put(join(opt, 'fixture/broker-source.sha256'), hash('broker sources') + '\n');
  put(
    join(opt, 'auriga-dsh/src/local/broker-package.js'),
    readFileSync(
      join(repository, 'clients/dsh-plugin/src/local/broker-package.js'),
      'utf8',
    ),
  );
  const broker = join(opt, 'auriga-dsh/shell-auth');
  const brokerFiles = [
    ...[
      'business',
      'host',
      'network',
      'platform',
      'schema',
      'streams',
      'team',
      'vault',
      'network-core.bundle',
    ].map((name) => name + '.mjs'),
    'vendor-ajv.cjs',
    ...brokerInputs
      .filter((name) => name.startsWith('contracts/'))
      .map((name) => name.replace('contracts/', 'schemas/')),
    ...[
      'ajv',
      'fast-deep-equal',
      'fast-uri',
      'json-schema-traverse',
      'require-from-string',
    ].map((name) => 'licenses/' + name + '.txt'),
  ];
  put(join(broker, 'broker-manifest.json'), {
    version: 1,
    target: 'managed-linux',
    files: brokerFiles.map((path) => {
      put(join(broker, path), 'fixture bytes');
      return { path, sha256: hash('fixture bytes') };
    }),
  });
  put(join(opt, 'dsh/node_modules/@deepseek-ai/dsh/package.json'), {
    name: '@deepseek-ai/dsh',
    version: '0.2.1-rc.3',
    bin: './cli.mjs',
  });
  put(join(opt, 'dsh/node_modules/@deepseek-ai/dsh/cli.mjs'), '// public CLI');
  function seal() {
    const files = [];
    function walk(path) {
      for (const name of readdirSync(path, { withFileTypes: true })) {
        const full = join(path, name.name);
        if (name.isDirectory()) walk(full);
        else if (full !== join(opt, 'fixture/manifest.json'))
          files.push({
            path: full.slice(opt.length + 1).replaceAll('\\', '/'),
            sha256: hash(readFileSync(full)),
          });
      }
    }
    walk(opt);
    put(join(opt, 'fixture/manifest.json'), { files });
  }
  seal();
  return {
    opt,
    expected,
    put,
    seal,
    run: (image = baseImage, chain = toolchain) =>
      prepareIncremental({ opt, expected, baseImage: image, toolchain: chain }),
  };
}

test('pinned unchanged base is accepted and all old plugin source is removed', async (t) => {
  const f = fixture(t);
  await f.run();
  assert.ok(!existsSync(join(f.opt, 'auriga-dsh/src')));
  assert.ok(
    existsSync(join(f.opt, 'auriga-dsh/node_modules/fixture/package.json')),
  );
  const newSource = join(f.opt, 'auriga-dsh/src');
  mkdirSync(newSource);
  writeFileSync(join(newSource, 'entry.js'), 'new snapshot');
  assert.equal(
    readFileSync(join(newSource, 'entry.js'), 'utf8'),
    'new snapshot',
  );
  assert.ok(!existsSync(join(newSource, 'removed.js')));
});

for (const image of [
  'runtime:latest',
  'runtime@sha256:' + 'a'.repeat(63),
  'https://registry.example.com/runtime@sha256:' + 'a'.repeat(64),
])
  test(
    'unpinned or invalid base is refused: ' + image.slice(0, 20),
    async (t) => {
      const f = fixture(t);
      await assert.rejects(f.run(image), /digest/);
      assert.ok(existsSync(join(f.opt, 'auriga-dsh/src/entry.js')));
    },
  );

for (const path of [
  'recipe/package.json',
  'recipe/package-lock.json',
  'recipe/versions.json',
  'recipe/build.mjs',
  'recipe/inputs.mjs',
  'recipe/runtime.mjs',
  'recipe/start.mjs',
  'recipe/tooling.mjs',
  'recipe/tooling/package.json',
  'recipe/tooling/package-lock.json',
  'runtime-pins.json',
  'plugin/package.json',
  'plugin/package-lock.json',
])
  test('changed foundation input refuses reuse: ' + path, async (t) => {
    const f = fixture(t);
    writeFileSync(
      join(f.expected, path),
      readFileSync(join(f.expected, path), 'utf8') + ' ',
    );
    await assert.rejects(f.run(), /differs|mismatch/);
    assert.ok(existsSync(join(f.opt, 'auriga-dsh/src/entry.js')));
  });

test('base manifest rejects tampered bytes and unlisted files before deleting source', async (t) => {
  const f = fixture(t);
  f.put(join(f.opt, 'auriga-dsh/node_modules/fixture/package.json'), {
    name: 'fixture',
    version: '9.0.0',
  });
  await assert.rejects(f.run(), /manifest/);
  f.seal();
  f.put(join(f.opt, 'fixture/unlisted.js'), 'unexpected');
  await assert.rejects(f.run(), /manifest/);
  assert.ok(existsSync(join(f.opt, 'auriga-dsh/src/entry.js')));
});

test('resealed production lock drift and installed host drift refuse reuse', async (t) => {
  for (const path of [
    'auriga-dsh/package-lock.json',
    'dsh/node_modules/@deepseek-ai/dsh/package.json',
  ]) {
    const f = fixture(t);
    f.put(join(f.opt, path), { name: 'unexpected', version: '9.0.0' });
    f.seal();
    await assert.rejects(f.run(), /differs|Installed DSH/);
    assert.ok(existsSync(join(f.opt, 'auriga-dsh/src/entry.js')));
  }
});

test('actual toolchain mismatch refuses reuse', async (t) => {
  const f = fixture(t);
  await assert.rejects(
    f.run(baseImage, { ...toolchain, nodeVersion: '24.0.0' }),
    /toolchain/,
  );
});

test('pre-broker base, changed source lock and resealed bad broker all require a full build', async (t) => {
  const old = fixture(t);
  rmSync(join(old.opt, 'fixture/broker-source.sha256'));
  rmSync(join(old.opt, 'auriga-dsh/shell-auth/broker-manifest.json'));
  old.seal();
  await assert.rejects(old.run(), /lacks the managed broker; use full build/);

  const drift = fixture(t);
  drift.put(
    join(drift.expected, 'recipe/broker-source.sha256'),
    hash('new source') + '\n',
  );
  await assert.rejects(drift.run(), /broker source\/lock; use full build/);

  const tampered = fixture(t);
  tampered.put(
    join(tampered.opt, 'auriga-dsh/shell-auth/network-core.bundle.mjs'),
    'new bytes',
  );
  tampered.seal();
  await assert.rejects(
    tampered.run(),
    (error) =>
      /managed broker is invalid; use full build/.test(error.message) &&
      error.cause?.message === 'broker digest mismatch',
  );
  for (const f of [old, drift, tampered])
    assert.ok(existsSync(join(f.opt, 'auriga-dsh/src/entry.js')));
});

test('Docker recipe checks before mutation and regenerates original manifest', () => {
  const recipe = readFileSync(
    new URL('./Dockerfile.incremental', import.meta.url),
    'utf8',
  );
  assert.match(recipe, /ARG BASE_RUNTIME_IMAGE/);
  assert.match(recipe, /FROM \$\{BASE_RUNTIME_IMAGE\}/);
  assert.ok(
    recipe.indexOf('incremental.mjs') <
      recipe.indexOf('COPY real-dsh-inputs/clients/dsh-plugin/src'),
  );
  assert.match(
    recipe,
    /node --import \S+incremental-fs.mjs \/opt\/fixture\/build.mjs manifest/,
  );
  assert.match(recipe, /USER 200000:200000/);
  assert.match(
    recipe,
    /USER 200000:200000\s+RUN --network=none node \/opt\/fixture\/runtime.mjs/,
  );
  assert.match(recipe, /ENTRYPOINT \["node", "\/opt\/fixture\/start.mjs"\]/);
});

test('incremental Docker guard uses the fixed Node/tool before mutation, never global Corepack', () => {
  const recipe = readFileSync(
    new URL('./Dockerfile.incremental', import.meta.url),
    'utf8',
  );
  assert.match(recipe, /RUN if ! node \/opt\/fixture\/tooling.mjs installed/);
  assert.match(recipe, /rebuild the full real-dsh image/);
  assert.doesNotMatch(recipe, /command -v|corepack|pnpm --version/);
  assert(
    recipe.indexOf('tooling.mjs installed') <
      recipe.indexOf('RUN node /tmp/auriga-incremental/recipe/incremental.mjs'),
  );
});

test('missing, resealed wrong, or broken runtime tooling rejects reuse before source deletion', async (t) => {
  for (const state of ['absent', 'wrong-version', 'broken-entry']) {
    const f = fixture(t);
    if (state === 'absent')
      rmSync(join(f.opt, 'auriga-tools/pnpm'), { recursive: true });
    if (state === 'wrong-version')
      f.put(join(f.opt, 'auriga-tools/pnpm/node_modules/pnpm/package.json'), {
        name: 'pnpm',
        version: '11.28.6',
      });
    if (state === 'broken-entry')
      f.put(
        join(f.opt, 'auriga-tools/pnpm', runtimePnpmEntry),
        'process.exit(7)',
      );
    f.seal();
    await assert.rejects(f.run(), /runtime tooling|ENOENT|foundation/);
    assert(existsSync(join(f.opt, 'auriga-dsh/src')));
  }
});

test('incremental preload only skips no-op 0755 on regular files', () => {
  const preload = new URL('./incremental-fs.mjs', import.meta.url).href;
  const output = execFileSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import fs from 'node:fs';
    const calls=[];
    fs.lstatSync=path=>({isFile:()=>path!=='directory',mode:path==='suid'?0o104755:0o100755});
    fs.chmodSync=(...args)=>{calls.push(args);if(args[0]==='missing')throw new Error('chmod_failed');};
    await import(${JSON.stringify(preload)});
    fs.chmodSync('same',0o755);
    fs.chmodSync('suid',0o755);
    fs.chmodSync('directory',0o755);
    fs.chmodSync('other-mode',0o644);
    fs.lstatSync=()=>{throw new Error('stat_failed');};
    let failed=false;try{fs.chmodSync('missing',0o755);}catch(e){failed=e.message==='chmod_failed';}
    console.log(JSON.stringify({calls,failed}));
  `,
    ],
    { encoding: 'utf8' },
  );
  assert.deepEqual(JSON.parse(output), {
    calls: [
      ['suid', 0o755],
      ['directory', 0o755],
      ['other-mode', 0o644],
      ['missing', 0o755],
    ],
    failed: true,
  });
});

test('preload preserves actual child-process filesystem permission and failure results', (t) => {
  const f = fixture(t);
  const preload = new URL('./incremental-fs.mjs', import.meta.url).href;
  const output = execFileSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import fs from 'node:fs';import {join} from 'node:path';
    const directory=${JSON.stringify(f.opt)};
    const chmod=fs.chmodSync, rows=[];
    for(const prefix of ['control','adapted']){
      for(const mode of [0o755,0o444,0o4755]){
        const file=join(directory,prefix+'-'+mode);
        fs.writeFileSync(file,'retained bytes');chmod(file,mode);
      }
    }
    for(const mode of [0o755,0o444,0o4755])chmod(join(directory,'control-'+mode),0o755);
    let controlError;try{chmod(join(directory,'absent'),0o755);}catch(e){controlError=e.code;}
    await import(${JSON.stringify(preload)});
    for(const mode of [0o755,0o444,0o4755]){
      const file=join(directory,'adapted-'+mode);fs.chmodSync(file,0o755);
      rows.push({sameMode:(fs.statSync(file).mode&0o7777)===(fs.statSync(join(directory,'control-'+mode)).mode&0o7777),bytes:fs.readFileSync(file,'utf8')});
    }
    let adaptedError;try{fs.chmodSync(join(directory,'absent'),0o755);}catch(e){adaptedError=e.code;}
    console.log(JSON.stringify({rows,sameError:controlError==='ENOENT'&&adaptedError===controlError}));
  `,
    ],
    { encoding: 'utf8' },
  );
  assert.deepEqual(JSON.parse(output), {
    rows: [0, 1, 2].map(() => ({ sameMode: true, bytes: 'retained bytes' })),
    sameError: true,
  });
});
