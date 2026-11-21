import { releaseVersion } from './release-version.mjs';
import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  mkdtemp,
  mkdir,
  readFile,
  readdir,
  writeFile,
  rm,
  utimes,
} from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { packagePlugin } from './package.mjs';
import { installLocalAdmission } from '../../clients/dsh-plugin/src/local/host.js';

async function releaseWorkflow(name) {
  const { parseDocument } = createRequire(
    new URL('../../clients/dsh-plugin/package.json', import.meta.url),
  )('yaml');
  const document = parseDocument(
    await readFile(
      new URL('../../.github/workflows/' + name, import.meta.url),
      'utf8',
    ),
  );
  assert.deepEqual(document.errors, [], 'workflow must be valid YAML');
  return document.toJS();
}

test('release workflow PR runs only the real read-only package check', async () => {
  const workflow = await releaseWorkflow('plugin-release.yml');
  assert.deepEqual(Object.keys(workflow.on), ['pull_request']);
  assert.deepEqual(Object.keys(workflow.jobs), ['package']);
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.deepEqual(workflow.jobs.package, {
    uses: './.github/workflows/auth-release-artifacts.yml',
  });
  for (const path of [
    'clients/shell-auth/**',
    'contracts/plugin/**',
    '.github/workflows/auth-release-artifacts.yml',
    'scripts/plugin/**',
    'clients/dsh-plugin/**',
    'web/panel/**',
    'web/pnpm-lock.yaml',
    '.github/workflows/plugin-release.yml',
    '.github/workflows/plugin-tag-release.yml',
  ]) {
    assert.ok(
      workflow.on.pull_request.paths.includes(path),
      'missing PR path ' + path,
    );
  }
});

test('release workflow plugin tags have one publisher after shared artifact validation', async () => {
  const workflow = await releaseWorkflow('plugin-tag-release.yml');
  assert.deepEqual(workflow.on, { push: { tags: ['plugin-v*'] } });
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.deepEqual(Object.keys(workflow.jobs), ['package', 'release']);
  assert.deepEqual(workflow.jobs.package, {
    uses: './.github/workflows/auth-release-artifacts.yml',
  });
  const release = workflow.jobs.release;
  assert.equal(release.needs, 'package');
  assert.deepEqual(release.permissions, { contents: 'write' });
  assert.equal(
    release.if,
    "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/plugin-v')",
  );
  const artifact = release.steps.find((step) =>
    step.uses?.startsWith('actions/download-artifact@'),
  );
  assert.deepEqual(artifact.with, { name: 'auriga-dsh', path: 'dist/plugin' });
  const shared = await releaseWorkflow('auth-release-artifacts.yml');
  const commands = shared.jobs.produce.steps
    .map((step) => step.run ?? '')
    .join('\n');
  assert.match(
    commands,
    /node --test scripts\/plugin\/package\.test\.mjs clients\/shell-auth\/test\/\*\.test\.mjs/,
  );
  assert.match(commands, /node clients\/shell-auth\/build\.mjs dist\/broker/);
  assert.equal(
    (commands.match(/node scripts\/plugin\/package\.mjs/g) ?? []).length,
    2,
  );
  assert.match(commands, /cmp "dist\/plugin\/auriga-dsh-\$version\.tgz"/);
  assert.match(commands, /sha256sum -c \*\.sha256/);

  const publishers = [];
  const directory = new URL('../../.github/workflows/', import.meta.url);
  for (const name of await readdir(directory)) {
    if (!/\.ya?ml$/.test(name)) continue;
    const candidate = await releaseWorkflow(name);
    const tags = candidate.on?.push?.tags ?? [];
    const matches = tags.some((pattern) => {
      const escaped = pattern
        .replace(/[.+^\x24{}()|[\]\\]/g, '\\$&')
        .replaceAll('*', '.*');
      return new RegExp('^' + escaped + '$').test('plugin-v1.2.3');
    });
    if (
      matches &&
      Object.values(candidate.jobs).some((job) =>
        job.steps?.some((step) => step.run?.includes('gh release create')),
      )
    )
      publishers.push(name);
  }
  assert.deepEqual(publishers, ['plugin-tag-release.yml']);
});

test('release workflow stable and prerelease tags retain verified assets and notes', async () => {
  const workflow = await releaseWorkflow('plugin-tag-release.yml');
  const publish = workflow.jobs.release.steps.find(
    (step) => step.name === 'Publish tag assets',
  );
  assert.equal(publish.env.GH_TOKEN, '$' + '{{ github.token }}');
  assert.equal(publish.env.GH_REPO, '$' + '{{ github.repository }}');
  assert.equal(publish.env.RELEASE_TAG, '$' + '{{ github.ref_name }}');
  const bash =
    process.platform === 'win32'
      ? join(process.env.ProgramFiles, 'Git/bin/bash.exe')
      : '/bin/bash';
  for (const version of ['1.2.3', '1.2.3-rc.1']) {
    const root = await mkdtemp(join(tmpdir(), 'auriga-release-workflow-'));
    try {
      const assets = [
        'auriga-dsh-' + version + '.tgz',
        'auriga-dsh-' + version + '.tgz.sha256',
        'auriga-dsh-' + version + '.json',
      ];
      await mkdir(join(root, 'dist/plugin'), { recursive: true });
      for (const name of assets)
        await writeFile(join(root, 'dist/plugin', name), 'fixture');
      // Capture the real workflow command with a shell function. No GitHub
      // executable or token is supplied to this synthetic invocation.
      const result = spawnSync(
        bash,
        [
          '--noprofile',
          '--norc',
          '-c',
          'gh() { printf "%s\\0" "$@"; }\n' + publish.run,
        ],
        {
          cwd: root,
          env: {
            SystemRoot: process.env.SystemRoot,
            PATH: '',
            RELEASE_TAG: 'plugin-v' + version,
          },
          encoding: 'utf8',
          windowsHide: true,
          timeout: 10000,
        },
      );
      assert.equal(result.status, 0, result.stderr);
      const args = result.stdout.split('\0').filter(Boolean);
      assert.deepEqual(args.slice(0, 3), [
        'release',
        'create',
        'plugin-v' + version,
      ]);
      assert.deepEqual(
        args.filter((arg) => arg.startsWith('dist/plugin/')).sort(),
        assets.map((name) => 'dist/plugin/' + name).sort(),
      );
      assert.ok(args.includes('--verify-tag'));
      assert.equal(args.includes('--prerelease'), version.includes('-'));
      assert.equal(args[args.indexOf('--title') + 1], 'plugin-v' + version);
      assert.equal(
        args[args.indexOf('--notes') + 1],
        'Auriga DSH plugin. Installation: docs/user/install-plugin.zh.md at this tag.',
      );
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }
});

test('missing host admission tells users the exact minimum DSH version', () => {
  assert.throws(
    () => installLocalAdmission({}, {}, { protocol: 'http:' }),
    /需要 DSH 0\.2\.1-rc\.3.*升级 DSH.*upgrade DSH/,
  );
});

test('package contains standalone runtime, version and matching digest; rebuild is identical', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    const plugin = join(root, 'clients/dsh-plugin');
    for (const dir of ['src', 'ui', 'test', 'scripts', 'skills'])
      await mkdir(join(plugin, dir), { recursive: true });
    const manifest = JSON.parse(
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await writeFile(join(plugin, 'package.json'), JSON.stringify(manifest));
    for (const file of [
      'src/plugin.js',
      'src/client.js',
      'LICENSE',
      'README.md',
      'cordis.patch.yml',
      'ui/auriga-team-panel.js',
      'ui/metadata.json',
      'skills/agent-auth.js',
      'test/private.js',
      'scripts/private.js',
    ])
      await writeFile(join(plugin, file), file);
    const brokerArtifact = join(root, 'broker');
    await mkdir(join(brokerArtifact, 'windows'), { recursive: true });
    const files = [];
    for (const path of [
      'windows/shell-helper.exe',
      'desktop.mjs',
      'broker.mjs',
    ]) {
      await writeFile(join(brokerArtifact, path), path);
      files.push({
        path,
        sha256: createHash('sha256').update(path).digest('hex'),
      });
    }
    await writeFile(
      join(brokerArtifact, 'broker-manifest.json'),
      JSON.stringify({ version: 1, files }),
    );
    const original = await readFile(join(plugin, 'package.json'), 'utf8');
    const first = await packagePlugin({
      root,
      version: '1.2.3-rc.1',
      outDir: join(root, 'one'),
      build: false,
      brokerArtifact,
    });
    const bytes = await readFile(first.tarball);
    assert.equal(
      await readFile(first.checksum, 'utf8'),
      `${createHash('sha256').update(bytes).digest('hex')}  auriga-dsh-1.2.3-rc.1.tgz\n`,
    );
    const receipt = JSON.parse(await readFile(first.manifest));
    assert.equal(receipt.version, '1.2.3-rc.1');
    assert.equal(
      receipt.sha256,
      createHash('sha256').update(bytes).digest('hex'),
    );
    const extraction = join(root, 'extracted');
    await mkdir(extraction);
    const unpack = spawnSync(
      process.platform === 'win32'
        ? join(process.env.SystemRoot, 'System32', 'tar.exe')
        : 'tar',
      ['-xzf', first.tarball, '-C', extraction],
      {
        encoding: 'utf8',
        windowsHide: true,
      },
    );
    assert.equal(unpack.status, 0, unpack.stderr);
    const packed = JSON.parse(
      await readFile(join(extraction, 'package/package.json')),
    );
    assert.deepEqual(
      await readFile(
        join(extraction, 'package/shell-auth/broker-manifest.json'),
      ),
      await readFile(join(brokerArtifact, 'broker-manifest.json')),
    );
    assert.deepEqual(
      await readFile(
        join(extraction, 'package/shell-auth/windows/shell-helper.exe'),
      ),
      await readFile(join(brokerArtifact, 'windows/shell-helper.exe')),
    );
    assert.equal(packed.version, '1.2.3-rc.1');
    assert.equal(packed.engines.dsh, '>=0.2.1-rc.3');
    assert.equal(packed.dependencies.ws, manifest.dependencies.ws);
    assert.equal(packed.scripts, undefined);
    assert.equal(packed.devDependencies, undefined);
    for (const file of [
      'src/plugin.js',
      'src/client.js',
      'cordis.patch.yml',
      'ui/auriga-team-panel.js',
      'ui/metadata.json',
      'skills/agent-auth.js',
    ])
      assert.ok(receipt.files.includes(file), file);
    assert.ok(
      !receipt.files.some((file) =>
        /^(test|scripts|node_modules)\//.test(file),
      ),
    );
    await utimes(join(plugin, 'src/plugin.js'), new Date(), new Date());
    const second = await packagePlugin({
      root,
      version: '1.2.3-rc.1',
      outDir: join(root, 'two'),
      build: false,
      brokerArtifact,
    });
    assert.deepEqual(await readFile(second.tarball), bytes);
    assert.equal(
      await readFile(join(plugin, 'package.json'), 'utf8'),
      original,
    );
    await assert.rejects(
      packagePlugin({
        root,
        version: '1.2.3-rc.1',
        outDir: join(root, 'one'),
        build: false,
        brokerArtifact,
      }),
      /already exists/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects invalid versions before building or writing', async () => {
  for (const version of ['', '../evil', 'v1.2.3', '01.2.3', '1.2', '1.2.3-01'])
    await assert.rejects(
      packagePlugin({
        version,
        root: resolve('absent'),
        outDir: resolve('absent'),
      }),
      /SemVer/,
    );
});

test('missing prepared UI fails without publishing partial assets', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    await mkdir(join(root, 'clients/dsh-plugin'), { recursive: true });
    await writeFile(
      join(root, 'clients/dsh-plugin/package.json'),
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await assert.rejects(
      packagePlugin({
        root,
        version: '1.0.0',
        outDir: join(root, 'out'),
        build: false,
      }),
      /ENOENT/,
    );
    await assert.rejects(
      readFile(join(root, 'out/auriga-dsh-1.0.0.tgz')),
      /ENOENT/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('failed UI build propagates its exit code and publishes nothing', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    const plugin = join(root, 'clients/dsh-plugin');
    await mkdir(join(plugin, 'scripts'), { recursive: true });
    await writeFile(
      join(plugin, 'package.json'),
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await writeFile(
      join(plugin, 'scripts/package-ui.mjs'),
      'process.exit(23);',
    );
    await assert.rejects(
      packagePlugin({ root, version: '1.0.0', outDir: join(root, 'out') }),
      /exit 23/,
    );
    await assert.rejects(
      readFile(join(root, 'out/auriga-dsh-1.0.0.tgz')),
      /ENOENT/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Desktop release selects real plugin version and rejects CI placeholder', () => {
  assert.equal(releaseVersion('desktop-v1.2.3', '0.4.0'), '0.4.0');
  assert.throws(
    () => releaseVersion('desktop-v1.2.3', '0.0.0-ci'),
    /placeholder/,
  );
  assert.equal(releaseVersion('plugin-v0.5.0', '0.4.0'), '0.5.0');
  assert.equal(releaseVersion('feature', '0.4.0'), '0.0.0-ci');
});

test('manual official release also rejects placeholder plugin version', () => {
  assert.equal(releaseVersion('main', '0.4.0', 'workflow_dispatch'), '0.4.0');
  assert.throws(
    () => releaseVersion('main', '0.0.0-ci', 'workflow_dispatch'),
    /placeholder/,
  );
  assert.throws(
    () => releaseVersion('plugin-v0.0.0-ci', '0.4.0'),
    /placeholder/,
  );
});
