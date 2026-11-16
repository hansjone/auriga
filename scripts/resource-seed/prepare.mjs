import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import {
  readFile,
  writeFile,
  mkdir,
  mkdtemp,
  readdir,
  lstat,
  copyFile,
} from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import {
  packSkill,
  unpackSkill,
} from '../../clients/dsh-plugin/src/resources/skill-package.js';
import { validateArchive } from '../../clients/dsh-plugin/src/resources/archive.js';
import { schemaValid } from '../../clients/dsh-plugin/src/resources/schema.js';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const require = createRequire(new URL('../../package.json', import.meta.url));
const { build, version: esbuildVersion } = createRequire(
  require.resolve('vite/package.json'),
)('esbuild');
const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const json = (value) => JSON.stringify(value, null, 2) + '\n';
const dataFiles = [
  'templates/vault/gitignore',
  'templates/vault/_guide.md',
  'skills/memwiki/SKILL.md',
];

async function copyRegular(source, destination) {
  assert(
    (await lstat(source)).isFile(),
    'Source must be a regular file: ' + source,
  );
  await mkdir(dirname(destination), { recursive: true });
  await copyFile(source, destination);
}

async function snapshotCode(source, destination) {
  for (const entry of await readdir(source, { withFileTypes: true })) {
    assert(!entry.isSymbolicLink(), 'Source links are not admitted');
    if (entry.name.startsWith('.')) continue;
    const input = join(source, entry.name),
      output = join(destination, entry.name);
    if (entry.isDirectory()) await snapshotCode(input, output);
    else if (/\.(js|json)$/.test(entry.name)) await copyRegular(input, output);
  }
}

// The archive is constructed from this explicit map, never from an output tree.
export function packPlugin(files) {
  const chunks = [];
  for (const [path, bytes] of [...files].sort(([a], [b]) =>
    a.localeCompare(b, 'en'),
  )) {
    assert(
      /^package\/[A-Za-z0-9@._/-]+$/.test(path) &&
        !path.split('/').includes('..'),
    );
    const header = Buffer.alloc(512);
    const split = Buffer.byteLength(path) <= 100 ? -1 : path.lastIndexOf('/');
    const leaf = split < 0 ? path : path.slice(split + 1),
      prefix = split < 0 ? '' : path.slice(0, split);
    assert(Buffer.byteLength(leaf) <= 100 && Buffer.byteLength(prefix) <= 155);
    header.write(leaf);
    header.write(prefix, 345);
    header.write('0000644\0', 100);
    header.write('0000000\0', 108);
    header.write('0000000\0', 116);
    header.write(bytes.length.toString(8).padStart(11, '0') + '\0', 124);
    header.write('00000000000\0', 136);
    header.fill(32, 148, 156);
    header[156] = 48;
    header.write('ustar\0' + '00', 257);
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
  return gzipSync(Buffer.concat([...chunks, Buffer.alloc(1024)]));
}

async function dependencyPackage(input, source) {
  let directory = dirname(resolve(input));
  const dependencyRoot = join(source, 'node_modules');
  while (
    directory.startsWith(dependencyRoot + '/') ||
    directory.startsWith(dependencyRoot + '\\')
  ) {
    try {
      const metadata = JSON.parse(
        await readFile(join(directory, 'package.json'), 'utf8'),
      );
      if (
        typeof metadata.name === 'string' &&
        typeof metadata.version === 'string'
      )
        return { directory, metadata };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    directory = dirname(directory);
  }
  throw Error('Cannot resolve bundled dependency metadata: ' + input);
}

export async function prepare(sourceArgument, outputArgument) {
  const source = resolve(sourceArgument);
  const output = resolve(outputArgument ?? join(repository, 'seed-resources'));
  await mkdir(output, { recursive: true });
  const run = await mkdtemp(join(output, 'build-'));
  const snapshot = join(run, 'source'),
    packageDirectory = join(run, 'package');
  await mkdir(snapshot);
  const sourceCommit = execFileSync(
    'git',
    ['-C', source, 'rev-parse', 'HEAD'],
    { encoding: 'utf8' },
  ).trim();
  const sourceStatus = execFileSync(
    'git',
    ['-C', source, 'status', '--porcelain', '--untracked-files=no'],
    { encoding: 'utf8' },
  ).trim();
  for (const path of [
    'package.json',
    'package-lock.json',
    'cordis.patch.yml',
    'LICENSE',
    ...dataFiles,
  ])
    await copyRegular(join(source, path), join(snapshot, path));
  await snapshotCode(join(source, 'lib'), join(snapshot, 'lib'));
  const original = JSON.parse(
    await readFile(join(snapshot, 'package.json'), 'utf8'),
  );
  const lockBytes = await readFile(join(snapshot, 'package-lock.json'));
  const npm =
    process.platform === 'win32'
      ? join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js')
      : 'npm';
  const install =
    process.platform === 'win32'
      ? [npm, 'ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund']
      : ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund'];
  execFileSync(process.platform === 'win32' ? process.execPath : npm, install, {
    cwd: snapshot,
    stdio: 'inherit',
    windowsHide: true,
  });
  assert.deepEqual(
    await readFile(join(snapshot, 'package-lock.json')),
    lockBytes,
  );

  const files = new Map(),
    dependencies = new Map(),
    sourceInputs = new Map();
  for (const name of ['index', 'cli', 'dsh-host']) {
    const result = await build({
      absWorkingDir: snapshot,
      entryPoints: [join(snapshot, 'lib', name + '.js')],
      outfile: join(packageDirectory, 'lib', name + '.js'),
      write: false,
      metafile: true,
      bundle: true,
      platform: 'node',
      format: 'esm',
      target: 'node22',
      sourcemap: false,
      legalComments: 'none',
      banner: {
        js: "import { createRequire as __seedRequire } from 'node:module'; const require = __seedRequire(import.meta.url);",
      },
      plugins: [
        {
          name: 'bundle-template-location',
          setup(builder) {
            builder.onLoad(
              { filter: /[\\/]lib[\\/]core[\\/]vault\.js$/ },
              async ({ path }) => {
                const contents = await readFile(path, 'utf8');
                assert(
                  contents.includes('../../templates/vault/'),
                  'Upstream template lookup changed',
                );
                return {
                  contents: contents.replaceAll(
                    '../../templates/vault/',
                    '../templates/vault/',
                  ),
                  loader: 'js',
                };
              },
            );
          },
        },
      ],
    });
    files.set(`package/lib/${name}.js`, result.outputFiles[0].contents);
    for (const input of Object.keys(result.metafile.inputs)) {
      const absolute = resolve(snapshot, input);
      if (
        !relative(snapshot, absolute)
          .replaceAll('\\', '/')
          .startsWith('node_modules/')
      ) {
        sourceInputs.set(
          relative(snapshot, absolute).replaceAll('\\', '/'),
          digest(await readFile(absolute)),
        );
        continue;
      }
      const { directory, metadata } = await dependencyPackage(
        absolute,
        snapshot,
      );
      dependencies.set(metadata.name + '@' + metadata.version, {
        name: metadata.name,
        version: metadata.version,
        license: metadata.license ?? null,
      });
      for (const filename of [
        'LICENSE',
        'LICENSE.md',
        'LICENSE.txt',
        'LICENCE',
      ]) {
        try {
          files.set(
            'package/licenses/' +
              metadata.name.replaceAll('/', '-') +
              '-' +
              filename,
            await readFile(join(directory, filename)),
          );
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      }
    }
  }
  for (const path of [
    'lib/client.js',
    'cordis.patch.yml',
    'LICENSE',
    ...dataFiles,
  ])
    files.set('package/' + path, await readFile(join(snapshot, path)));
  const toolSource = await readFile(
    join(snapshot, 'lib/mcp/server.js'),
    'utf8',
  );
  const tools = [
    ...new Set(
      [...toolSource.matchAll(/registerTool\(['"](memwiki_[a-z_]+)['"]/g)].map(
        (match) => match[1],
      ),
    ),
  ].sort();
  assert(
    tools.includes('memwiki_secret_get') &&
      tools.includes('memwiki_secret_set') &&
      tools.length >= 18,
  );
  const pkg = {
    name: original.name,
    version: original.version,
    type: 'module',
    license: original.license,
    main: 'lib/index.js',
    exports: {
      '.': './lib/index.js',
      './client': './lib/client.js',
      './dsh-host': './lib/dsh-host.js',
      './package.json': './package.json',
      './cordis.patch.yml': './cordis.patch.yml',
    },
    dsh: original.dsh,
  };
  files.set('package/package.json', Buffer.from(json(pkg)));
  for (const [path, bytes] of files) {
    const destination = join(run, path);
    await mkdir(dirname(destination), { recursive: true });
    await writeFile(destination, bytes);
  }
  // Verify the rewritten lookup through its normal CLI, including template loading.
  execFileSync(
    process.execPath,
    [
      join(packageDirectory, 'lib/cli.js'),
      'init',
      join(run, 'template-check'),
      '--no-git',
    ],
    { stdio: 'pipe', windowsHide: true },
  );
  const pluginBytes = packPlugin(files);
  await validateArchive(pluginBytes, { name: pkg.name, version: pkg.version });
  const meta = {
    name: 'clear-task-summary',
    description: 'Summarize a task using supplied facts only',
  };
  const skill = packSkill(
    {
      'SKILL.md': `---\nname: ${meta.name}\ndescription: "${meta.description}"\n---\n# Clear task summary\n\nUse when the user asks to summarize supplied task notes.\nState the result first. List completed work, evidence and remaining questions.\nUse only facts supplied in the conversation. Label uncertainty explicitly.\nDo not access files, call tools or external services, or include secrets.\n`,
    },
    meta,
  );
  unpackSkill(skill, meta);
  const summary = [];
  for (const [kind, name, version, bytes] of [
    ['plugin', pkg.name, pkg.version, pluginBytes],
    ['skill', meta.name, '1.0.0', skill],
  ]) {
    const manifest = {
      schemaVersion: 1,
      name,
      version,
      description:
        kind === 'plugin'
          ? `Shared memory wiki with ${tools.length} MCP tools, including credential list/get/set/delete. Reads and writes the selected vault and credentials. Executes git, including push to user-configured remotes. Windows secrets use DPAPI; non-Windows secrets are stored as plaintext. Git remote hosts are user-configured and not enumerable.`
          : meta.description,
      author: {
        displayName:
          typeof original.author === 'string'
            ? original.author
            : (original.author?.name ?? `${original.name} maintainers`),
      },
      license: kind === 'plugin' ? original.license : 'MIT',
      permissions: {
        tools: kind === 'plugin' ? tools : [],
        // The catalogue rejects wildcard hosts; git remotes are user-configured (see README).
        outboundHosts: [],
        dataAccess:
          kind === 'plugin'
            ? ['workspace_read', 'workspace_write', 'user_selected_files']
            : [],
      },
      runtimes:
        kind === 'plugin' ? [{ kind: 'node', versionRange: '>=22' }] : [],
      artifacts: [
        {
          id: 'payload',
          role: 'payload',
          digest: digest(bytes),
          sizeBytes: bytes.length,
          mediaType: 'application/gzip',
          os: 'any',
          arch: 'any',
        },
      ],
      signature: { mode: 'detached', algorithm: 'Ed25519' },
      kind,
      ...(kind === 'plugin'
        ? { plugin: { packageName: name, dshVersionRange: '>=0.2.1-rc.3' } }
        : {
            skill: {
              entry: 'SKILL.md',
              contentProfile: 'text-only',
              dshVersionRange: '>=0.2.1-rc.3',
            },
          }),
    };
    assert(
      schemaValid(
        'package',
        kind === 'plugin' ? 'PluginManifest' : 'SkillManifest',
        manifest,
      ),
    );
    const raw = json(manifest),
      filename = `${name}-${version}.tgz`;
    await writeFile(join(output, filename), bytes);
    await writeFile(join(output, name + '.auriga-resource.json'), raw);
    summary.push({
      name,
      version,
      kind,
      filename,
      artifactDigest: digest(bytes),
      manifestDigest: digest(raw),
      sizeBytes: bytes.length,
      signed: false,
    });
  }
  await writeFile(join(output, 'checksums.json'), json(summary));
  await writeFile(
    join(output, 'build-source.json'),
    json({
      name: original.name,
      version: original.version,
      sourceCommit,
      sourceTrackedChanges: sourceStatus ? sourceStatus.split(/\r?\n/) : [],
      sourceLockDigest: digest(lockBytes),
      esbuildVersion,
      dependencyInstall:
        'npm ci --omit=dev --ignore-scripts --no-audit --no-fund',
      bundledDependencies: [...dependencies.values()].sort((a, b) =>
        a.name.localeCompare(b.name),
      ),
      sourceInputs: [...sourceInputs]
        .sort()
        .map(([path, digest]) => ({ path, digest })),
      packageFiles: [...files.keys()].sort(),
      tools,
      capabilities: {
        gitPush: 'user-configured remotes',
        windowsSecrets: 'DPAPI',
        otherPlatformSecrets: 'plaintext',
      },
    }),
  );
  console.log(
    json({
      archiveValidation: true,
      manifestSchema: true,
      skillRoundTrip: true,
      output,
      run,
      resources: summary,
    }),
  );
  return { files: [...files.keys()].sort(), summary, run };
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  assert(
    process.argv[2],
    'usage: node scripts/resource-seed/prepare.mjs <dsh-memwiki source> [output]',
  );
  await prepare(process.argv[2], process.argv[3]);
}
