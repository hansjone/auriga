import { normalizePlatformBinding } from '../runtime/platform-origin.mjs';
/**
 * Build the Desktop installer package.
 *
 *   node scripts/build.mjs [--target win-x64|linux-x64] [--upstream <dsh version>]
 *        [--plugin-tgz <file>] [--out <dir>] [--cache <dir>] [--no-zip]
 *
 * Upstream DSH is a replaceable, version-pinned component: changing
 * `--upstream` (or upstream.json) rebuilds the package against another DSH
 * release. Nothing of upstream is modified; it is installed from npm as-is.
 * Default target follows the host OS; Windows still produces the SFX exe.
 */
import { Buffer } from 'node:buffer';
import { createPublicKey } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  cpSync,
  existsSync,
  lstatSync,
  readdirSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { compileHelper, compileTray, makeExe } from './make-exe.mjs';
import {
  copyBrokerArtifact,
  verifyBrokerArtifact,
} from '../../shell-auth/artifact.mjs';
import { parse as parseVersion } from '../runtime/semver-lite.mjs';
import { satisfiesMin } from '../runtime/semver-lite.mjs';
import {
  PINNED_TREES,
  computeId,
  isPinnedPath,
  treeDigest,
} from '../runtime/verify.mjs';
import { sha256File, assertNoSecrets, listFiles } from './lib/files.mjs';
import { verifyInputs } from '../../../scripts/client-release/inputs.mjs';
import { toolsBaselineOption } from '../../../scripts/client-release/baseline.mjs';
import {
  clientPlatform,
  hostClientTarget,
} from '../../../scripts/client-release/platform.mjs';
import { assemblePython } from './python-runtime.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const installerDir = resolve(here, '..');
const pluginDir = resolve(installerDir, '..', 'dsh-plugin');
export function validateResourceRoots(trust) {
  if (
    !trust ||
    typeof trust !== 'object' ||
    Array.isArray(trust) ||
    Object.keys(trust).sort().join() !== 'downgrade,release,revocation'
  )
    throw new Error('invalid resource trust roles');
  const fixtureKeys = Object.values(
    JSON.parse(
      readFileSync(
        join(installerDir, '../../contracts/resource/unified-vectors.json'),
        'utf8',
      ),
    ).trust,
  ).map((k) => k.publicKeySpkiBase64);
  const canonicalKeys = [];
  const normalized = { ...trust };
  for (const role of ['release', 'revocation', 'downgrade']) {
    const root = trust[role];
    if (
      !root ||
      typeof root !== 'object' ||
      Array.isArray(root) ||
      Object.keys(root).sort().join() !== 'kid,publicKeySpkiBase64' ||
      typeof root.kid !== 'string' ||
      !/^[A-Za-z0-9._-]{1,128}$/.test(root.kid) ||
      typeof root.publicKeySpkiBase64 !== 'string'
    )
      throw new Error('invalid resource trust root');
    const encoded = root.publicKeySpkiBase64.trim();
    if (/\s/.test(encoded))
      throw new Error('invalid resource trust public key encoding');
    const der = Buffer.from(encoded, 'base64');
    if (der.toString('base64') !== encoded)
      throw new Error('invalid resource trust public key encoding');
    const key = createPublicKey({
      key: der,
      type: 'spki',
      format: 'der',
    });
    if (key.asymmetricKeyType !== 'ed25519')
      throw new Error('resource trust must be Ed25519');
    const canonical = key
      .export({ type: 'spki', format: 'der' })
      .toString('base64');
    if (fixtureKeys.includes(canonical))
      throw new Error('public test resource key cannot be deployed');
    if (canonical !== encoded) throw new Error('invalid resource trust SPKI');
    canonicalKeys.push(canonical);
    normalized[role] = { ...root, publicKeySpkiBase64: canonical };
  }
  if (new Set(canonicalKeys).size !== 3)
    throw new Error('resource trust roles require independent keys');
  if (new Set(Object.values(trust).map((root) => root.kid)).size !== 3)
    throw new Error('resource trust roles require independent kids');
  return normalized;
}
const MAX_UAC_CONFIG_BYTES = 65536;
/** Validate an optional site UAC config directory containing adapter.json + provider.json. */
export function validateUacConfigDir(dir) {
  if (typeof dir !== 'string' || !dir.trim())
    throw new Error('invalid --uac-config-dir');
  const rootStat = lstatSync(dir);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
    throw new Error('--uac-config-dir must be a regular directory');
  const readRegularJson = (name) => {
    const file = join(dir, name);
    const st = lstatSync(file);
    if (!st.isFile() || st.isSymbolicLink() || st.size > MAX_UAC_CONFIG_BYTES)
      throw new Error(`invalid UAC config file: ${name}`);
    const bytes = readFileSync(file);
    const parsed = JSON.parse(bytes.toString('utf8'));
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      throw new Error(`invalid UAC JSON object: ${name}`);
    return { bytes, parsed };
  };
  const adapter = readRegularJson('adapter.json');
  const provider = readRegularJson('provider.json');
  const providerKeys = Object.keys(provider.parsed);
  if (
    providerKeys.some(
      (k) => !['baseUrl', 'systemCode', 'revalidation'].includes(k),
    ) ||
    typeof provider.parsed.baseUrl !== 'string' ||
    !provider.parsed.baseUrl.trim() ||
    typeof provider.parsed.systemCode !== 'string' ||
    !provider.parsed.systemCode.trim()
  )
    throw new Error('invalid UAC provider.json');
  return { adapterBytes: adapter.bytes, providerBytes: provider.bytes };
}
const isWin = process.platform === 'win32';
// Git Bash's GNU tar treats `D:` as a remote host; use the Windows bsdtar.
const TAR = isWin
  ? join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe')
  : 'tar';

function npmFlags(target) {
  return [
    '--omit=dev',
    '--ignore-scripts',
    '--no-audit',
    '--no-fund',
    `--os=${target.npmOs}`,
    `--cpu=${target.npmCpu}`,
  ];
}

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
  'Auriga DSH',
  '',
  'Auriga DSH is built on the open-source DSH (@deepseek-ai/dsh, MIT License).',
  'The upstream package is installed unmodified; its licence text is kept in',
  'LICENSE.dsh.txt. Auriga DSH is not an official DeepSeek product and is not',
  'endorsed by DeepSeek. Node.js is bundled under its own licence (LICENSE.node.txt).',
  '',
].join('\r\n');

/**
 * Secret scan over everything we ship, including the expanded plugin tarball.
 * Skipped: registry-pinned third-party trees and the official Node binary
 * (pinned by checksum). Our own files are never skipped, whatever their size.
 */
export function scanForSecrets(stage, tgz, work) {
  const thirdParty = (p) =>
    p.startsWith('/payload/upstream/app/node_modules/') ||
    p.startsWith('/payload/tools/pnpm/node_modules/') ||
    p.startsWith('/payload/upstream/node/') ||
    (p.startsWith('/payload/runtime/python/') &&
      p !== '/payload/runtime/python/runtime.json') ||
    (p.startsWith('/payload/auriga/plugin/node_modules/') &&
      !p.startsWith('/payload/auriga/plugin/node_modules/auriga-dsh/'));
  assertNoSecrets(stage, thirdParty);
  const expanded = join(work, 'plugin-tgz-scan');
  rmSync(expanded, { recursive: true, force: true });
  mkdirSync(expanded, { recursive: true });
  run(TAR, ['-xf', tgz, '-C', expanded]);
  assertNoSecrets(expanded, () => false);
}

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

export async function nodeRuntime(
  version,
  expected,
  cache,
  offline = false,
  target = clientPlatform('win-x64'),
) {
  if (!offline) mkdirSync(cache, { recursive: true });
  const name = target.nodeArchive(version);
  const archive = join(cache, name);
  const base = `https://nodejs.org/dist/v${version}`;
  let sha = expected;
  if (offline && (!sha || !existsSync(archive)))
    throw new Error(`缺少离线输入：${name} 或固定摘要`);
  if (!sha) {
    const sums = join(cache, `SHASUMS256-${version}.txt`);
    if (!existsSync(sums)) await download(`${base}/SHASUMS256.txt`, sums);
    const line = readFileSync(sums, 'utf8')
      .split('\n')
      .find((l) => l.trim().endsWith(` ${name}`));
    if (!line) throw new Error(`no checksum for ${name}`);
    sha = line.trim().split(/\s+/)[0];
  }
  if (!existsSync(archive)) await download(`${base}/${name}`, archive);
  const actual = sha256File(archive);
  if (actual !== sha) {
    if (!offline) rmSync(archive, { force: true });
    throw new Error(`node runtime checksum mismatch: ${actual} != ${sha}`);
  }
  return { zip: archive, archive, sha256: actual };
}

function readTgzPackageJson(tgz) {
  return JSON.parse(run(TAR, ['-xOf', tgz, 'package/package.json']));
}

export async function build(args) {
  if (typeof args['broker-artifact'] !== 'string')
    throw new Error('--broker-artifact is required');
  const target = clientPlatform(args.target ?? hostClientTarget());
  const flags = npmFlags(target);
  const resourceTrust = args['resource-trust-json']
    ? validateResourceRoots(
        JSON.parse(readFileSync(args['resource-trust-json'], 'utf8')),
      )
    : null;
  const uacConfig = args['uac-config-dir']
    ? validateUacConfigDir(args['uac-config-dir'])
    : null;
  const config = JSON.parse(
    readFileSync(join(installerDir, 'upstream.json'), 'utf8'),
  );
  const offline = args.offline === true;
  const inputs = offline
    ? verifyInputs(
        args['offline-inputs'],
        args['offline-inputs-sha256'],
        undefined,
        {
          toolsBaseline: toolsBaselineOption(),
        },
      )
    : null;
  if (offline && args.upstream && args.upstream !== config.dsh)
    throw new Error('离线构建不能更换未批准的 DSH 版本');
  if (offline && !args['plugin-tgz'])
    throw new Error('离线构建必须提供本地重建的插件包');
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
  if (
    (process.env.GITHUB_REF_NAME?.startsWith('desktop-v') ||
      process.env.RELEASE_BUILD === 'true') &&
    pluginPkg.version === '0.0.0-ci'
  )
    throw new Error(
      'CI placeholder plugin version forbidden in Desktop release',
    );
  if (!satisfiesMin(dshVersion, pluginPkg.engines?.dsh))
    throw new Error(
      `plugin ${pluginPkg.name}@${pluginPkg.version} requires dsh ${pluginPkg.engines?.dsh}, upstream is ${dshVersion}`,
    );
  const desktopVersion =
    args['desktop-version'] ??
    JSON.parse(readFileSync(join(installerDir, 'desktop.json'), 'utf8'))
      .version;
  parseVersion(desktopVersion);
  const bundleName = `${desktopVersion}-dsh${dshVersion}`;

  // 2. Stage the payload.
  const stage = join(out, `auriga-desktop-${bundleName}-${target.target}`);
  rmSync(stage, { recursive: true, force: true });
  const payload = join(stage, 'payload');
  mkdirSync(payload, { recursive: true });

  const nodeSha = config[target.nodeShaField];
  if (!nodeSha) throw new Error(`upstream.json missing ${target.nodeShaField}`);
  const node = await nodeRuntime(
    config.node,
    nodeSha,
    inputs ? join(inputs.directory, 'runtime') : cache,
    offline,
    target,
  );
  const nodeTmp = join(work, 'node');
  mkdirSync(nodeTmp, { recursive: true });
  run(TAR, ['-xf', node.archive, '-C', nodeTmp]);
  const nodeSrc = join(nodeTmp, target.nodeExtractDir(config.node));
  mkdirSync(join(payload, 'upstream', 'node'), { recursive: true });
  if (target.target === 'win-x64') {
    for (const f of ['node.exe', 'LICENSE'])
      cpSync(join(nodeSrc, f), join(payload, 'upstream', 'node', f));
  } else {
    // Keep the official linux layout (bin/node, lib, LICENSE, …).
    for (const entry of readdirSync(nodeSrc)) {
      if (entry === 'CHANGELOG.md' || entry === 'README.md') continue;
      cpSync(join(nodeSrc, entry), join(payload, 'upstream', 'node', entry), {
        recursive: true,
      });
    }
    chmodSync(join(payload, 'upstream', 'node', 'bin', 'node'), 0o755);
  }

  if (target.target === 'win-x64') {
    await assemblePython({
      payload,
      inputs: inputs ? inputs.directory : cache,
      offline,
    });
  }

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
  if (offline) {
    if (!locked) throw new Error('离线构建缺少固定 DSH 依赖锁');
    cpSync(join(inputs.directory, 'upstream-app'), app, { recursive: true });
    if (
      sha256File(join(app, 'package-lock.json')) !== sha256File(committedLock)
    )
      throw new Error('离线 DSH 闭包与当前锁不符');
  } else if (locked) {
    cpSync(committedLock, join(app, 'package-lock.json'));
    run('npm', ['ci', ...flags], { cwd: app });
  } else {
    run('npm', ['install', ...flags], { cwd: app });
  }
  const lockFile = join(app, 'package-lock.json');
  const lock = JSON.parse(readFileSync(lockFile, 'utf8'));
  const dshEntry = lock.packages['node_modules/@deepseek-ai/dsh'];
  if (dshEntry?.version !== dshVersion)
    throw new Error(
      `lock resolves dsh ${dshEntry?.version}, expected ${dshVersion}`,
    );
  const registryIntegrity = offline
    ? config.dshIntegrity
    : JSON.parse(
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
  if (offline) {
    const deps = join(inputs.directory, 'plugin-deps');
    const dependencyPackage = JSON.parse(
      readFileSync(join(deps, 'package.json'), 'utf8'),
    );
    if (
      JSON.stringify(dependencyPackage.dependencies) !==
      JSON.stringify(pluginPkg.dependencies)
    )
      throw new Error('离线插件依赖变化，请重新准备 devkit');
    cpSync(join(deps, 'node_modules'), join(plugin, 'node_modules'), {
      recursive: true,
    });
    const expanded = join(work, 'offline-plugin');
    mkdirSync(expanded);
    run(TAR, ['-xf', tgz, '-C', expanded]);
    cpSync(join(expanded, 'package'), join(plugin, 'node_modules/auriga-dsh'), {
      recursive: true,
    });
  } else {
    run('npm', ['install', ...flags, '--no-package-lock', tgz], {
      cwd: plugin,
    });
  }
  cpSync(tgz, join(payload, 'auriga', 'plugin', 'auriga-dsh.tgz'));
  rmSync(join(plugin, 'package.json'));

  // 3b. Size trimming (no runtime code removed).
  const sizeBefore = dirSize(app) + dirSize(join(plugin, 'node_modules'));
  const removed = { files: 0 };
  slim(join(app, 'node_modules'), removed);
  slim(join(plugin, 'node_modules'), removed);
  const withoutLibreoffice = args['without-libreoffice'] === true;
  if (withoutLibreoffice)
    rmSync(
      join(app, 'node_modules', '@deepseek-ai', target.libreofficeKit),
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
  if (offline)
    cpSync(join(inputs.directory, 'payload-pnpm'), pnpmDir, {
      recursive: true,
    });
  else run('npm', ['install', ...flags], { cwd: pnpmDir });
  const pnpmLock = JSON.parse(
    readFileSync(join(pnpmDir, 'package-lock.json'), 'utf8'),
  );
  const pnpmIntegrity = pnpmLock.packages['node_modules/pnpm']?.integrity;
  const pnpmRegistry = offline
    ? config.pnpmIntegrity
    : JSON.parse(
        run('npm', ['view', `pnpm@${config.pnpm}`, 'dist.integrity', '--json']),
      );
  if (!pnpmIntegrity || pnpmIntegrity !== pnpmRegistry)
    throw new Error('pnpm integrity mismatch');
  if (config.pnpmIntegrity && config.pnpmIntegrity !== pnpmIntegrity)
    throw new Error('pnpm integrity differs from upstream.json pin');
  mkdirSync(join(payload, 'tools', 'bin'), { recursive: true });
  if (target.target === 'win-x64') {
    writeFileSync(
      join(payload, 'tools', 'bin', 'pnpm.cmd'),
      '@echo off\r\n"%~dp0..\\..\\upstream\\node\\node.exe" "%~dp0..\\pnpm\\node_modules\\pnpm\\bin\\pnpm.cjs" %*\r\n',
    );
  } else {
    const pnpmSh = join(payload, 'tools', 'bin', 'pnpm');
    writeFileSync(
      pnpmSh,
      '#!/usr/bin/env bash\n' +
        'ROOT=$(cd -- "$(dirname -- "$0")/../.." && pwd)\n' +
        'exec "$ROOT/upstream/node/bin/node" "$ROOT/tools/pnpm/node_modules/pnpm/bin/pnpm.cjs" "$@"\n',
    );
    chmodSync(pnpmSh, 0o755);
  }

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
    const selected = normalizePlatformBinding(args['platform-origin'], {
      accessMode: args['platform-access-mode'],
      confirmedOrigin: args['confirm-platform-origin'],
    });
    const { origin, ...platformBinding } = selected;
    mkdirSync(join(payload, 'auriga'), { recursive: true });
    writeFileSync(
      join(payload, 'auriga', 'defaults.json'),
      JSON.stringify({ platformOrigin: origin, platformBinding }, null, 2) +
        '\n',
    );
  }

  // 4. Our own runtime and platform wrappers.
  const shellAuthManifest = copyBrokerArtifact(
    args['broker-artifact'],
    join(payload, 'auriga', 'shell-auth'),
  );
  const pluginBroker = join(plugin, 'node_modules', 'auriga-dsh', 'shell-auth');
  verifyBrokerArtifact(pluginBroker);
  for (const path of [
    'broker-manifest.json',
    ...shellAuthManifest.files.map((file) => file.path),
  ]) {
    if (
      sha256File(join(pluginBroker, path)) !==
      sha256File(join(payload, 'auriga', 'shell-auth', path))
    )
      throw new Error('Desktop and plugin broker artifacts differ');
  }
  cpSync(join(installerDir, 'runtime'), join(payload, 'auriga', 'runtime'), {
    recursive: true,
  });
  mkdirSync(join(payload, 'auriga', 'runtime', 'schemas'), { recursive: true });
  for (const name of ['package', 'signatures', 'unified'])
    cpSync(
      join(installerDir, '../../contracts/resource', name + '.schema.json'),
      join(payload, 'auriga', 'runtime', 'schemas', name + '.schema.json'),
    );
  cpSync(
    join(installerDir, '../../contracts/resource/guardian-transitions.json'),
    join(payload, 'auriga/runtime/schemas/guardian-transitions.json'),
  );
  if (resourceTrust) {
    writeFileSync(
      join(payload, 'auriga', 'runtime', 'resource-roots.json'),
      JSON.stringify(resourceTrust),
    );
  }
  if (uacConfig) {
    const uacOut = join(payload, 'auriga', 'uac');
    mkdirSync(uacOut, { recursive: true });
    writeFileSync(join(uacOut, 'adapter.json'), uacConfig.adapterBytes);
    writeFileSync(join(uacOut, 'provider.json'), uacConfig.providerBytes);
  }
  if (args['release-public-key']) {
    const key = createPublicKey(readFileSync(args['release-public-key']));
    if (key.asymmetricKeyType !== 'ed25519')
      throw new Error('Ed25519 release public key required');
    writeFileSync(
      join(payload, 'auriga', 'runtime', 'release-public-key.txt'),
      key.export({ type: 'spki', format: 'pem' }),
    );
  }
  if (target.target === 'win-x64') {
    cpSync(
      join(installerDir, 'windows', 'launcher'),
      join(payload, 'auriga', 'launcher'),
      { recursive: true },
    );
    // Older launchers are resumed by byte offset after an update (see the
    // launcher header): the shipped bytes must be CRLF on any build machine.
    for (const name of readdirSync(join(payload, 'auriga', 'launcher')))
      if (name.endsWith('.cmd')) {
        const file = join(payload, 'auriga', 'launcher', name);
        writeFileSync(
          file,
          readFileSync(file, 'latin1').replace(/\r?\n/g, '\r\n'),
          'latin1',
        );
      }
    cpSync(
      resolve(installerDir, '../../assets/brand/auriga-dsh.ico'),
      join(payload, 'auriga', 'launcher', 'auriga.ico'),
    );
    // Tray app (Start menu entry, update/exit owner), built with the Windows C# compiler.
    compileTray(
      join(payload, 'auriga', 'tray', 'AurigaDesktop.exe'),
      resolve(installerDir, '../../assets/brand/auriga-dsh.ico'),
    );
    compileHelper(join(payload, 'auriga', 'runtime', 'auriga-win-helper.exe'));
    for (const f of ['install.ps1', 'install.cmd', 'README.txt'])
      cpSync(join(installerDir, 'windows', f), join(stage, f));
  } else {
    cpSync(
      join(installerDir, 'linux', 'launcher'),
      join(payload, 'auriga', 'launcher'),
      { recursive: true },
    );
    for (const name of [
      'auriga-desktop',
      'rollback.sh',
      'uninstall.sh',
      'shell-integration.sh',
    ]) {
      const file = join(payload, 'auriga', 'launcher', name);
      if (existsSync(file)) chmodSync(file, 0o755);
    }
    const png = resolve(installerDir, '../../assets/brand/auriga-dsh.png');
    if (existsSync(png))
      cpSync(png, join(payload, 'auriga', 'launcher', 'auriga.png'));
    cpSync(join(installerDir, 'linux', 'install.sh'), join(stage, 'install.sh'));
    cpSync(join(installerDir, 'linux', 'README.txt'), join(stage, 'README.txt'));
    chmodSync(join(stage, 'install.sh'), 0o755);
  }

  // 5. Manifest: component versions and checksums (upstream tree is pinned by the lock).
  const ours = listFiles(payload).filter((f) => !isPinnedPath(f));
  const manifest = {
    schema: 1,
    id: '',
    bundleVersion: desktopVersion,
    product: 'Auriga Desktop',
    shellAuth: {
      version: shellAuthManifest.version,
      manifestSha256: sha256File(
        join(payload, 'auriga/shell-auth/broker-manifest.json'),
      ),
    },
    target: target.target,
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
    // Digests of the distributed upstream DSH and pnpm trees (not only their locks).
    trees: PINNED_TREES.map((p) => treeDigest(payload, p.slice(0, -1))),
    files: ours.map((f) => ({
      path: f,
      size: statSync(join(payload, f)).size,
      sha256: sha256File(join(payload, f)),
    })),
  };
  const id = computeId(manifest);
  manifest.id = id;
  writeFileSync(
    join(payload, 'manifest.json'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  scanForSecrets(stage, tgz, work);

  // 6. Package archive.
  let zip = null;
  let archive = null;
  if (!args['no-zip']) {
    if (target.target === 'win-x64') {
      zip = `${stage}.zip`;
      rmSync(zip, { force: true });
      run(TAR, ['-a', '-cf', zip, '-C', stage, '.']);
      archive = zip;
    } else {
      archive = join(out, target.setupName(desktopVersion));
      rmSync(archive, { force: true });
      run(TAR, ['-czf', archive, '-C', stage, '.']);
    }
  }
  let exe = null;
  if (args.exe) {
    if (target.target !== 'win-x64')
      throw new Error('--exe is only supported for win-x64');
    exe = makeExe({
      stage,
      icon:
        args['installer-icon'] ??
        resolve(installerDir, '../../assets/brand/auriga-dsh.ico'),
      archive: zip,
      out: join(out, target.setupName(desktopVersion)),
    });
  }
  return { id, stage, zip, archive, exe, manifest, target: target.target };
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
