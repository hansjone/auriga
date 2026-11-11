/**
 * Build the Windows installer package.
 *
 *   node scripts/build.mjs [--upstream <dsh version>] [--plugin-tgz <file>]
 *        [--out <dir>] [--cache <dir>] [--no-zip]
 *
 * Upstream DSH is a replaceable, version-pinned component: changing
 * `--upstream` (or upstream.json) rebuilds the package against another DSH
 * release. Nothing of upstream is modified; it is installed from npm as-is.
 */
import { Buffer } from 'node:buffer';
import { spawnSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  readdirSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { makeExe } from './make-exe.mjs';
import { satisfiesMin } from './lib/semver-lite.mjs';
import { sha256File, assertNoSecrets, listFiles } from './lib/files.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const installerDir = resolve(here, '..');
const pluginDir = resolve(installerDir, '..', 'dsh-plugin');
const isWin = process.platform === 'win32';
const NPM_FLAGS = [
  '--omit=dev',
  '--ignore-scripts',
  '--no-audit',
  '--no-fund',
  '--os=win32',
  '--cpu=x64',
];
// Git Bash's GNU tar treats `D:` as a remote host; use the Windows bsdtar.
const TAR = isWin
  ? join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe')
  : 'tar';

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    if (!argv[i].startsWith('--'))
      throw new Error(`unexpected argument ${argv[i]}`);
    const key = argv[i].slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith('--')) out[key] = true;
    else {
      out[key] = next;
      i += 1;
    }
  }
  return out;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    shell: isWin && /^(npm|npx)$/.test(command),
    maxBuffer: 256 * 1024 * 1024,
    ...options,
  });
  if (result.status !== 0)
    throw new Error(
      `${command} ${args.join(' ')} failed (${result.status}): ${(result.stderr || result.stdout || '').slice(-800)}`,
    );
  return result.stdout;
}

const NOTICE = [
  'Auriga Desktop',
  '',
  'Auriga Desktop is built on the open-source DSH (@deepseek-ai/dsh, MIT License).',
  'The upstream package is installed unmodified; its licence text is kept in',
  'LICENSE.dsh.txt. Auriga Desktop is not an official DeepSeek product and is not',
  'endorsed by DeepSeek. Node.js is bundled under its own licence (LICENSE.node.txt).',
  '',
].join('\r\n');

function dirSize(dir) {
  let total = 0;
  for (const f of listFiles(dir)) total += statSync(join(dir, f)).size;
  return total;
}

const PLATFORM_DIRS =
  /^(linux|darwin|android|freebsd|openbsd|win32-arm64|win32-ia32)/;

/** Remove source maps, type declarations, readmes and other-platform prebuilds. */
function slim(modules, counter) {
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (basename(dir) === 'prebuilds' && PLATFORM_DIRS.test(entry.name)) {
          counter.files += listFiles(full).length;
          rmSync(full, { recursive: true, force: true });
        } else walk(full);
      } else if (
        /\.(map|d\.ts|d\.mts|d\.cts)$/.test(entry.name) ||
        /^(README|CHANGELOG|HISTORY)(\..*)?$/i.test(entry.name)
      ) {
        rmSync(full);
        counter.files += 1;
      }
    }
  };
  walk(modules);
}

async function download(url, file) {
  const response = await globalThis.fetch(url);
  if (!response.ok) throw new Error(`GET ${url}: ${response.status}`);
  writeFileSync(file, Buffer.from(await response.arrayBuffer()));
}

async function nodeRuntime(version, expected, cache) {
  mkdirSync(cache, { recursive: true });
  const name = `node-v${version}-win-x64.zip`;
  const zip = join(cache, name);
  const base = `https://nodejs.org/dist/v${version}`;
  let sha = expected;
  if (!sha) {
    const sums = join(cache, `SHASUMS256-${version}.txt`);
    if (!existsSync(sums)) await download(`${base}/SHASUMS256.txt`, sums);
    const line = readFileSync(sums, 'utf8')
      .split('\n')
      .find((l) => l.trim().endsWith(` ${name}`));
    if (!line) throw new Error(`no checksum for ${name}`);
    sha = line.trim().split(/\s+/)[0];
  }
  if (!existsSync(zip)) await download(`${base}/${name}`, zip);
  const actual = sha256File(zip);
  if (actual !== sha) {
    rmSync(zip, { force: true });
    throw new Error(`node runtime checksum mismatch: ${actual} != ${sha}`);
  }
  return { zip, sha256: actual };
}

function readTgzPackageJson(tgz) {
  return JSON.parse(run(TAR, ['-xOf', tgz, 'package/package.json']));
}

export async function build(args) {
  const config = JSON.parse(
    readFileSync(join(installerDir, 'upstream.json'), 'utf8'),
  );
  const dshVersion = args.upstream ?? config.dsh;
  const pinned = !args.upstream || args.upstream === config.dsh;
  const out = resolve(args.out ?? join(installerDir, 'target'));
  const cache = resolve(args.cache ?? join(out, 'cache'));
  const work = join(out, 'work');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(work, { recursive: true });

  // 1. Our plugin tarball and the version lock against the chosen upstream.
  let tgz = args['plugin-tgz'] ? resolve(args['plugin-tgz']) : null;
  if (!tgz) {
    const name = run('npm', ['pack', '--pack-destination', work], {
      cwd: pluginDir,
    })
      .trim()
      .split('\n')
      .pop();
    tgz = join(work, name);
  }
  const pluginPkg = readTgzPackageJson(tgz);
  if (!satisfiesMin(dshVersion, pluginPkg.engines?.dsh))
    throw new Error(
      `plugin ${pluginPkg.name}@${pluginPkg.version} requires dsh ${pluginPkg.engines?.dsh}, upstream is ${dshVersion}`,
    );
  const id = `${pluginPkg.version}-dsh${dshVersion}`;

  // 2. Stage the payload.
  const stage = join(out, `auriga-desktop-${id}-win-x64`);
  rmSync(stage, { recursive: true, force: true });
  const payload = join(stage, 'payload');
  mkdirSync(payload, { recursive: true });

  const node = await nodeRuntime(config.node, config.nodeWinX64Sha256, cache);
  const nodeTmp = join(work, 'node');
  mkdirSync(nodeTmp, { recursive: true });
  run(TAR, ['-xf', node.zip, '-C', nodeTmp]);
  const nodeSrc = join(nodeTmp, `node-v${config.node}-win-x64`);
  mkdirSync(join(payload, 'upstream', 'node'), { recursive: true });
  for (const f of ['node.exe', 'LICENSE'])
    cpSync(join(nodeSrc, f), join(payload, 'upstream', 'node', f));

  const app = join(payload, 'upstream', 'app');
  mkdirSync(app, { recursive: true });
  writeFileSync(
    join(app, 'package.json'),
    JSON.stringify(
      {
        name: 'auriga-upstream-dsh',
        private: true,
        dependencies: { '@deepseek-ai/dsh': dshVersion },
      },
      null,
      2,
    ) + '\n',
  );
  const committedLock = join(
    installerDir,
    'locks',
    `dsh-${dshVersion}.package-lock.json`,
  );
  const locked = existsSync(committedLock);
  if (locked) {
    cpSync(committedLock, join(app, 'package-lock.json'));
    run('npm', ['ci', ...NPM_FLAGS], { cwd: app });
  } else {
    run('npm', ['install', ...NPM_FLAGS], { cwd: app });
  }
  const lockFile = join(app, 'package-lock.json');
  const lock = JSON.parse(readFileSync(lockFile, 'utf8'));
  const dshEntry = lock.packages['node_modules/@deepseek-ai/dsh'];
  if (dshEntry?.version !== dshVersion)
    throw new Error(
      `lock resolves dsh ${dshEntry?.version}, expected ${dshVersion}`,
    );
  const registryIntegrity = JSON.parse(
    run('npm', [
      'view',
      `@deepseek-ai/dsh@${dshVersion}`,
      'dist.integrity',
      '--json',
    ]),
  );
  if (dshEntry.integrity !== registryIntegrity)
    throw new Error('dsh integrity differs between lock and registry');
  if (
    pinned &&
    config.dshIntegrity &&
    dshEntry.integrity !== config.dshIntegrity
  )
    throw new Error('dsh integrity differs from upstream.json pin');
  if (!locked) {
    mkdirSync(join(out, 'locks'), { recursive: true });
    cpSync(lockFile, join(out, 'locks', `dsh-${dshVersion}.package-lock.json`));
  }

  // 3. Plugin closure (production dependencies only, no install scripts).
  const plugin = join(payload, 'auriga', 'plugin');
  mkdirSync(plugin, { recursive: true });
  writeFileSync(
    join(plugin, 'package.json'),
    '{"name":"auriga-plugin-closure","private":true}\n',
  );
  run('npm', ['install', ...NPM_FLAGS, '--no-package-lock', tgz], {
    cwd: plugin,
  });
  cpSync(tgz, join(payload, 'auriga', 'plugin', 'auriga-dsh.tgz'));
  rmSync(join(plugin, 'package.json'));

  // 3b. Size trimming (win-x64 only; no runtime code removed).
  const sizeBefore = dirSize(app) + dirSize(join(plugin, 'node_modules'));
  const removed = { files: 0 };
  slim(join(app, 'node_modules'), removed);
  slim(join(plugin, 'node_modules'), removed);
  const withoutLibreoffice = args['without-libreoffice'] === true;
  if (withoutLibreoffice)
    rmSync(
      join(app, 'node_modules', '@deepseek-ai', 'libreoffice-kit-win32-x64'),
      { recursive: true, force: true },
    );
  const sizeAfter = dirSize(app) + dirSize(join(plugin, 'node_modules'));

  // 3c. pnpm (the plugin manager shells out to it); pinned and verified against the registry.
  const pnpmDir = join(payload, 'tools', 'pnpm');
  mkdirSync(pnpmDir, { recursive: true });
  writeFileSync(
    join(pnpmDir, 'package.json'),
    JSON.stringify({
      name: 'auriga-tools-pnpm',
      private: true,
      dependencies: { pnpm: config.pnpm },
    }) + '\n',
  );
  run('npm', ['install', ...NPM_FLAGS], { cwd: pnpmDir });
  const pnpmLock = JSON.parse(
    readFileSync(join(pnpmDir, 'package-lock.json'), 'utf8'),
  );
  const pnpmIntegrity = pnpmLock.packages['node_modules/pnpm']?.integrity;
  const pnpmRegistry = JSON.parse(
    run('npm', ['view', `pnpm@${config.pnpm}`, 'dist.integrity', '--json']),
  );
  if (!pnpmIntegrity || pnpmIntegrity !== pnpmRegistry)
    throw new Error('pnpm integrity mismatch');
  if (config.pnpmIntegrity && config.pnpmIntegrity !== pnpmIntegrity)
    throw new Error('pnpm integrity differs from upstream.json pin');
  mkdirSync(join(payload, 'tools', 'bin'), { recursive: true });
  writeFileSync(
    join(payload, 'tools', 'bin', 'pnpm.cmd'),
    '@echo off\r\n"%~dp0..\\..\\upstream\\node\\node.exe" "%~dp0..\\pnpm\\node_modules\\pnpm\\bin\\pnpm.cjs" %*\r\n',
  );

  // 3d. Licences: upstream MIT text is kept, product notice added.
  mkdirSync(join(payload, 'licenses'), { recursive: true });
  cpSync(
    join(app, 'node_modules', '@deepseek-ai', 'dsh', 'LICENSE'),
    join(payload, 'licenses', 'LICENSE.dsh.txt'),
  );
  cpSync(
    join(nodeSrc, 'LICENSE'),
    join(payload, 'licenses', 'LICENSE.node.txt'),
  );
  cpSync(
    join(pluginDir, 'LICENSE'),
    join(payload, 'licenses', 'LICENSE.auriga-dsh.txt'),
  );
  writeFileSync(join(payload, 'licenses', 'NOTICE.txt'), NOTICE);
  if (args['platform-origin']) {
    const origin = new URL(args['platform-origin']).origin;
    mkdirSync(join(payload, 'auriga'), { recursive: true });
    writeFileSync(
      join(payload, 'auriga', 'defaults.json'),
      JSON.stringify({ platformOrigin: origin }, null, 2) + '\n',
    );
  }

  // 4. Our own runtime and Windows wrappers.
  cpSync(join(installerDir, 'runtime'), join(payload, 'auriga', 'runtime'), {
    recursive: true,
  });
  cpSync(
    join(installerDir, 'windows', 'launcher'),
    join(payload, 'auriga', 'launcher'),
    { recursive: true },
  );
  for (const f of ['install.ps1', 'install.cmd', 'README.txt'])
    cpSync(join(installerDir, 'windows', f), join(stage, f));

  // 5. Manifest: component versions and checksums (upstream tree is pinned by the lock).
  const ours = listFiles(payload).filter(
    (f) =>
      !f.startsWith('upstream/app/node_modules/') &&
      !f.startsWith('auriga/plugin/node_modules/') &&
      !f.startsWith('tools/pnpm/node_modules/'),
  );
  const manifest = {
    schema: 1,
    id,
    bundleVersion: pluginPkg.version,
    product: 'Auriga Desktop',
    target: 'win-x64',
    size: {
      upstreamAndPluginBeforeTrim: sizeBefore,
      afterTrim: sizeAfter,
      trimmedFiles: removed.files,
      withoutLibreoffice,
    },
    pnpm: { version: config.pnpm, integrity: pnpmIntegrity },
    upstream: {
      name: '@deepseek-ai/dsh',
      version: dshVersion,
      integrity: dshEntry.integrity,
      pinnedInRepo: pinned && Boolean(config.dshIntegrity),
      lockSha256: sha256File(lockFile),
      lockCommitted: locked,
      nodeVersion: config.node,
      nodeZipSha256: node.sha256,
    },
    plugin: {
      name: pluginPkg.name,
      version: pluginPkg.version,
      enginesDsh: pluginPkg.engines.dsh,
      tgzSha256: sha256File(tgz),
    },
    files: ours.map((f) => ({
      path: f,
      size: statSync(join(payload, f)).size,
      sha256: sha256File(join(payload, f)),
    })),
  };
  writeFileSync(
    join(payload, 'manifest.json'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  assertNoSecrets(stage, (p) => p.includes('/node_modules/'));

  // 6. Zip.
  let zip = null;
  if (!args['no-zip']) {
    zip = `${stage}.zip`;
    rmSync(zip, { force: true });
    run(TAR, ['-a', '-cf', zip, '-C', stage, '.']);
  }
  let exe = null;
  if (args.exe) {
    exe = makeExe({ stage, out: join(out, `Auriga-Desktop-Setup-${id}.exe`) });
  }
  return { id, stage, zip, exe, manifest };
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  const result = await build(parseArgs(process.argv.slice(2)));
  console.log(
    JSON.stringify(
      { id: result.id, stage: result.stage, zip: result.zip, exe: result.exe },
      null,
      2,
    ),
  );
}
