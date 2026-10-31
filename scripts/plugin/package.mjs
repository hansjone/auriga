/** Build a self-contained DSH bundle. Only release preparation runs UI builds. */
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  access,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

const repository = fileURLToPath(new URL('../../', import.meta.url));
const semver =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$/;

async function npmCLI() {
  const candidates = [
    process.env.npm_execpath,
    join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'),
    resolve(
      dirname(process.execPath),
      '../lib/node_modules/npm/bin/npm-cli.js',
    ),
  ];
  try {
    const require = createRequire(import.meta.url);
    candidates.push(
      join(dirname(require.resolve('npm/package.json')), 'bin/npm-cli.js'),
    );
  } catch {
    /* npm is commonly bundled beside Node instead. */
  }
  for (const candidate of candidates) {
    if (!candidate?.endsWith('npm-cli.js')) continue;
    try {
      await access(candidate);
      return candidate;
    } catch {
      /* Try the next installation layout. */
    }
  }
  throw new Error('npm CLI not found; install Node.js with npm');
}

function run(args, cwd, { capture = false, env = process.env } = {}) {
  const result = spawnSync(process.execPath, args, {
    cwd,
    env,
    encoding: 'utf8',
    windowsHide: true,
    stdio: capture ? 'pipe' : 'inherit',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(
      `Command failed (exit ${result.status}): ${args[0]}\n${result.stderr ?? ''}`,
    );
  return result.stdout;
}

export async function packagePlugin({
  root = repository,
  version,
  outDir = join(root, 'dist/plugin'),
  build = true,
} = {}) {
  if (typeof version !== 'string' || !semver.test(version))
    throw new Error('--version must be an exact SemVer, e.g. 0.1.0-rc.1');
  root = resolve(root);
  outDir = resolve(outDir);
  const source = join(root, 'clients/dsh-plugin');
  const pkg = JSON.parse(await readFile(join(source, 'package.json'), 'utf8'));
  if (pkg.name !== 'auriga-dsh') throw new Error('Expected auriga-dsh package');
  const filename = `auriga-dsh-${version}.tgz`;
  const paths = {
    tarball: join(outDir, filename),
    checksum: join(outDir, `${filename}.sha256`),
    manifest: join(outDir, `auriga-dsh-${version}.json`),
  };
  for (const path of Object.values(paths)) {
    const exists = await access(path).then(
      () => true,
      () => false,
    );
    if (exists) throw new Error(`Release asset already exists: ${path}`);
  }
  const npm = await npmCLI();
  if (build)
    run([join(source, 'scripts/package-ui.mjs')], root, {
      env: { ...process.env, VITE_AURIGA_MOCK: '0' },
    });
  // Never reuse or publish a tarball if required browser assets are absent.
  for (const file of ['ui/auriga-team-panel.js', 'ui/metadata.json'])
    await access(join(source, file));
  await mkdir(outDir, { recursive: true });
  const temporary = await mkdtemp(join(outDir, '.package-'));
  try {
    const stage = join(temporary, 'bundle');
    await mkdir(stage);
    for (const entry of pkg.files) {
      if (
        !['src', 'ui', 'LICENSE', 'README.md', 'cordis.patch.yml'].includes(
          entry,
        )
      )
        throw new Error(
          `Review new package file entry before releasing: ${entry}`,
        );
      await cp(join(source, entry), join(stage, entry), {
        recursive: true,
        dereference: false,
      });
    }
    // Distributable installation has no lifecycle scripts or build dependencies.
    const runtime = { ...pkg };
    delete runtime.scripts;
    delete runtime.devDependencies;
    await writeFile(
      join(stage, 'package.json'),
      JSON.stringify({ ...runtime, version }, null, 2) + '\n',
    );
    const packed = JSON.parse(
      run(
        [
          npm,
          'pack',
          '--ignore-scripts',
          '--json',
          '--pack-destination',
          temporary,
          '--cache',
          join(temporary, 'npm-cache'),
        ],
        stage,
        { capture: true },
      ),
    )[0];
    if (packed.filename !== filename)
      throw new Error('Unexpected npm pack filename');
    const bytes = await readFile(join(temporary, filename));
    const sha256 = createHash('sha256').update(bytes).digest('hex');
    const metadata = {
      name: runtime.name,
      version,
      filename,
      sha256,
      bytes: bytes.length,
      engines: runtime.engines,
      files: packed.files.map((file) => file.path).sort(),
    };
    await writeFile(paths.tarball, bytes, { flag: 'wx' });
    await writeFile(paths.checksum, `${sha256}  ${filename}\n`, { flag: 'wx' });
    await writeFile(paths.manifest, JSON.stringify(metadata, null, 2) + '\n', {
      flag: 'wx',
    });
    return paths;
  } finally {
    // The only recursive cleanup target is the directory mkdtemp just created.
    await rm(temporary, { recursive: true, force: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  try {
    const { values } = parseArgs({
      options: {
        version: { type: 'string' },
        out: { type: 'string' },
        help: { type: 'boolean' },
      },
    });
    if (values.help) {
      console.log(
        'node scripts/plugin/package.mjs --version <SemVer> [--out <directory>]',
      );
    } else {
      console.log(
        JSON.stringify(
          await packagePlugin({ version: values.version, outDir: values.out }),
          null,
          2,
        ),
      );
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
