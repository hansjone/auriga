import { copyFile, cp, mkdir, mkdtemp, readFile, writeFile, stat } from 'node:fs/promises';
import { createWriteStream } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';
import { HERE, hashFile, inventory, json, options, run, within, writeJson } from './lib.mjs';
import { pack } from './archive.mjs';
import { testSignature } from './verify.mjs';

const args = options(process.argv.slice(2), ['dsh', 'node', 'target', 'lock', 'out', 'npm-cli']);
let resultFile;
try {
  if (process.versions.node.split('.')[0] !== '22') throw new Error('Build with Node 22');
  if (!/^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/.test(args.dsh ?? '')) throw new Error('--dsh requires an exact version');
  if (!['win-x64', 'linux-x64', 'linux-arm64'].includes(args.target)) throw new Error('Unsupported --target');
  const requestedNode = (args.node ?? '').replace(/^v/, '');
  if (!/^22\.(?:x|\d+\.\d+)$/.test(requestedNode)) throw new Error('--node must be 22.x or exact 22.minor.patch');
  const out = within(HERE, resolve(args.out ?? join(HERE, 'out')));
  await mkdir(out, { recursive: true });
  resultFile = join(out, `build-${args.target}.json`);
  const start = performance.now();
  const work = join(HERE, '.work');
  await mkdir(work, { recursive: true });
  const stage = await mkdtemp(join(work, 'build-'));
  const nodeVersion = requestedNode === '22.x' ? (await getJson('https://nodejs.org/dist/index.json')).find(x => /^v22\./.test(x.version)).version.slice(1) : requestedNode;
  const id = `dsh-${args.dsh}+node${nodeVersion}-${args.target}`;
  const root = join(stage, id);
  await mkdir(root);
  const windows = args.target.startsWith('win-');
  const os = windows ? 'win32' : 'linux', cpu = args.target.split('-')[1];
  const nodeName = `node-v${nodeVersion}-${args.target}`;
  const nodeArchive = `${nodeName}.${windows ? 'zip' : 'tar.gz'}`;
  const base = `https://nodejs.org/dist/v${nodeVersion}/`;
  const sums = await getText(base + 'SHASUMS256.txt');
  const sumLine = sums.split('\n').find(line => line.trim().endsWith('  ' + nodeArchive));
  if (!sumLine) throw new Error(`Node checksum missing: ${nodeArchive}`);
  const expected = sumLine.split(/\s+/)[0];
  const download = join(stage, nodeArchive);
  console.error(`Downloading ${nodeArchive}`);
  const response = await fetch(base + nodeArchive, { signal: AbortSignal.timeout(300000) });
  if (!response.ok) throw new Error(`Node download HTTP ${response.status}`);
  await pipeline(Readable.fromWeb(response.body), createWriteStream(download));
  const actual = await hashFile(download);
  if (actual !== expected) throw new Error('Node official SHA-256 mismatch');
  const unpack = join(stage, 'node-download'); await mkdir(unpack);
  // Extract only the executable and license; never extract foreign npm links on Windows.
  await run('tar', ['-xf', download, '-C', unpack, `${nodeName}/${windows ? 'node.exe' : 'bin/node'}`, `${nodeName}/LICENSE`]);
  await mkdir(join(root, 'node', windows ? '' : 'bin'), { recursive: true });
  await copyFile(join(unpack, nodeName, windows ? 'node.exe' : 'bin/node'), join(root, 'node', windows ? 'node.exe' : 'bin/node'));
  await copyFile(join(unpack, nodeName, 'LICENSE'), join(root, 'node/LICENSE'));
  const app = join(root, 'app'); await mkdir(app);
  await writeJson(join(app, 'package.json'), { name: 'auriga-s4-runtime', version: '0.0.0', private: true, dependencies: { '@deepseek-ai/dsh': args.dsh } });
  const npmCli = args['npm-cli'] ? resolve(args['npm-cli']) : join(dirname(process.execPath), process.platform === 'win32' ? 'node_modules/npm/bin/npm-cli.js' : '../lib/node_modules/npm/bin/npm-cli.js');
  const npmVersion = await run(process.execPath, [npmCli, '--version']);
  const env = { ...process.env, npm_config_cache: join(work, 'npm-cache'), TMP: stage, TEMP: stage, TMPDIR: stage };
  const flags = ['--omit=dev', '--include=optional', '--ignore-scripts', '--bin-links=false', '--no-audit', '--no-fund', '--registry=https://registry.npmjs.org', `--os=${os}`, `--cpu=${cpu}`, ...(os === 'linux' ? ['--libc=glibc'] : [])];
  if (args.lock) await copyFile(resolve(args.lock), join(app, 'package-lock.json'));
  else {
    console.error('Resolving lockfile (capture it for all rebuilds)');
    await run(process.execPath, [npmCli, 'install', '--package-lock-only', ...flags], { cwd: app, env, verbose: true });
  }
  const lock = await json(join(app, 'package-lock.json'));
  if (lock.packages[''].dependencies['@deepseek-ai/dsh'] !== args.dsh || lock.packages['node_modules/@deepseek-ai/dsh'].version !== args.dsh) throw new Error('Lock does not match DSH version');
  for (const [path, entry] of Object.entries(lock.packages)) {
    if (path && (!entry.integrity || !entry.resolved?.startsWith('https://registry.npmjs.org/'))) throw new Error(`Non-registry or unlocked dependency: ${path}`);
  }
  await copyFile(join(app, 'package-lock.json'), join(out, `${id}.package-lock.json`));
  console.error(`Installing production closure for ${os}/${cpu} with npm ${npmVersion}`);
  await run(process.execPath, [npmCli, 'ci', ...flags], { cwd: app, env, verbose: true });
  await mkdir(join(root, 'bin'));
  const launcher = windows
    ? '@echo off\r\n"%~dp0..\\node\\node.exe" "%~dp0..\\app\\node_modules\\@deepseek-ai\\dsh\\lib\\bin.js" %*\r\n'
    : '#!/bin/sh\nset -eu\nROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)\nexec "$ROOT/node/bin/node" "$ROOT/app/node_modules/@deepseek-ai/dsh/lib/bin.js" "$@"\n';
  await writeFile(join(root, 'bin', windows ? 'dsh.cmd' : 'dsh'), launcher);
  const files = await inventory(root);
  const installScriptPackages = Object.entries(lock.packages).filter(([, v]) => v.hasInstallScript).map(([path, v]) => ({ path, version: v.version, optional: !!v.optional }));
  const manifest = { schemaVersion: 1, artifactId: id, dshVersion: args.dsh, nodeVersion, target: args.target, libc: os === 'linux' ? 'glibc' : null, npmVersion, lockSha256: await hashFile(join(app, 'package-lock.json')), nodeDistribution: { url: base + nodeArchive, sha256: actual, checksumUrl: base + 'SHASUMS256.txt' }, installPolicy: 'npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false', installScriptPackages, files };
  await writeJson(join(root, 'manifest.json'), manifest);
  const archive = join(out, id + '.tar.gz');
  console.error(`Packing ${files.length} entries`);
  await pack(root, id, archive);
  const digest = await hashFile(archive);
  await writeFile(archive + '.sha256', `${digest}  ${id}.tar.gz\n`);
  const signatureProof = await testSignature(archive);
  const all = await inventory(root, '', false);
  const result = { status: 'passed', artifactId: id, target: args.target, builder: { platform: process.platform, arch: process.arch, node: process.version, npm: npmVersion }, archive: `${id}.tar.gz`, digest: `sha256:${digest}`, nodeVersion, dshVersion: args.dsh, lockSha256: manifest.lockSha256, unpackedBytes: all.reduce((n, f) => n + f.size, 0), compressedBytes: (await stat(archive)).size, fileCount: all.filter(x => x.type === 'file').length, buildMs: Math.round(performance.now() - start), nativeFiles: files.filter(x => /\.(?:node|dll|exe)$/.test(x.path) || /\/node-addon-system[^/]*\/.*(?:bin|prebuild)/.test(x.path)).map(x => x.path), installScriptPackages, signatureProof };
  await writeJson(resultFile, result); console.log(JSON.stringify(result, null, 2));
} catch (error) {
  if (resultFile) await writeJson(resultFile, { status: 'failed', target: args.target, error: String(error) });
  console.error(error); process.exitCode = 1;
}
async function getText(url) {
  const response = await fetch(url, { signal: AbortSignal.timeout(60000) });
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  return response.text();
}
async function getJson(url) { return JSON.parse(await getText(url)); }
