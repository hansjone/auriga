// Explicit no-PG acceptance and same-test, single-production-overlay proof.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  defaultRuntime,
  installNativeRuntime,
  isolatedEnvironment,
  hostPin,
} from './support/native-install.mjs';
import {
  nativeIdentityBypass,
  assertNativeVerdict,
} from './support/native-overlay.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const run = promisify(execFile);
const testPath = 'e2e/network/native-host-check.mjs';
const sourcePath =
  'clients/dsh-plugin/src/network/host/native/source-context.js';
const hash = async (file) =>
  createHash('sha256')
    .update(await readFile(file))
    .digest('hex');
async function recorded(command, args, cwd, env, log) {
  let result;
  try {
    const value = await run(command, args, {
      cwd,
      env,
      windowsHide: true,
      timeout: 300_000,
      maxBuffer: 8 * 1024 * 1024,
    });
    result = { code: 0, stdout: value.stdout, stderr: value.stderr };
  } catch (error) {
    result = {
      code: error.code,
      stdout: error.stdout ?? '',
      stderr: error.stderr ?? '',
    };
  }
  await writeFile(
    log,
    JSON.stringify({ command, args, cwd, exitCode: result.code }) +
      '\n' +
      result.stdout +
      result.stderr,
  );
  if (!Number.isInteger(result.code))
    throw new Error(`native command failed (${result.code}); see ${log}`);
  return result;
}

async function check(directory, runtime, evidence, mode) {
  await mkdir(join(directory, 'e2e/network/target'), { recursive: true });
  const profile = join(evidence, mode + '-profile');
  const nativeEvidence = join(evidence, mode + '-native');
  await mkdir(nativeEvidence);
  const env = {
    ...isolatedEnvironment(profile),
    AURIGA_DSH_RUNTIME: runtime,
    AURIGA_NATIVE_EVIDENCE: nativeEvidence,
  };
  for (const path of ['dsh', 'home', 'config', 'tmp'])
    await mkdir(join(profile, path), { recursive: true });
  const result = await recorded(
    process.execPath,
    ['--test', join(directory, testPath)],
    profile,
    env,
    join(evidence, mode + '-test.log'),
  );
  assertNativeVerdict(result, mode);
  for (const label of ['ordinary', 'lifecycle']) {
    const fact = JSON.parse(
      await readFile(join(nativeEvidence, label + '.json'), 'utf8'),
    );
    assert.equal(fact.closed, true);
    assert.equal(fact.removed, true);
    assert.ok(fact.process.pid > 0 && fact.process.parentPid > 0);
    assert.ok(
      fact.process.creationTime ||
        (fact.process.bootId && fact.process.startTicks),
    );
    const created = fact.events.filter(
      (event) => event.event === 'agent/created',
    );
    const disposed = fact.events.filter(
      (event) => event.event === 'agent/disposed',
    );
    assert.ok(created.length > 0);
    for (const event of created)
      assert.ok(
        disposed.some(
          (value) =>
            value.agentObject === event.agentObject &&
            value.sessionObject === event.sessionObject,
        ),
        'every real native object must be disposed',
      );
  }
  return result.code;
}

export async function verifyNative(mode) {
  assert.ok(['--check', '--verify'].includes(mode));
  const artifactRoot = join(root, 'e2e/network/target');
  await mkdir(artifactRoot, { recursive: true });
  const evidence = await mkdtemp(join(artifactRoot, 'native-verify-'));
  const manifest = {
    ...hostPin,
    variants: [],
    complete: false,
    pg: false,
    model: false,
    send: false,
  };
  try {
    if (mode === '--check') {
      manifest.variants.push({
        name: 'Control',
        runExit: await check(root, defaultRuntime, evidence, 'Control'),
      });
    } else {
      const head = (
        await run('git', ['rev-parse', 'HEAD'], {
          cwd: root,
          windowsHide: true,
        })
      ).stdout.trim();
      assert.equal(
        (
          await run('git', ['status', '--porcelain', '--untracked-files=no'], {
            cwd: root,
            windowsHide: true,
          })
        ).stdout.trim(),
        '',
        'exact HEAD must be clean',
      );
      manifest.head = head;
      const archive = join(evidence, 'source.tar');
      await run(
        'git',
        ['archive', '--format=tar', '--output=' + archive, head],
        { cwd: root, windowsHide: true },
      );
      manifest.archiveSha256 = await hash(archive);
      const runtime = await installNativeRuntime(join(evidence, 'runtime'));
      for (const name of ['Control', 'NativeIdentityBypass']) {
        const directory = join(evidence, name);
        await mkdir(directory);
        await run('tar', ['-xf', archive, '-C', directory], {
          cwd: root,
          windowsHide: true,
        });
        const testSha256 = await hash(join(directory, testPath));
        const source = join(directory, sourcePath);
        const originalSourceSha256 = await hash(source);
        if (name === 'NativeIdentityBypass')
          await writeFile(
            source,
            nativeIdentityBypass(await readFile(source, 'utf8')),
          );
        const buildEnv = { ...process.env, CI: 'true' };
        assert.ok(
          buildEnv.AURIGA_PNPM_STORE,
          'use dispatched locked pnpm store',
        );
        const corepack = join(
          dirname(process.execPath),
          'node_modules/corepack/dist/corepack.js',
        );
        const install = await recorded(
          process.execPath,
          [
            corepack,
            'pnpm',
            'install',
            '--frozen-lockfile',
            '--filter',
            'auriga-dsh...',
            '--store-dir',
            buildEnv.AURIGA_PNPM_STORE,
          ],
          directory,
          buildEnv,
          join(evidence, name + '-install.log'),
        );
        assert.equal(
          install.code,
          0,
          'independent locked plugin dependencies required',
        );
        const compile = await recorded(
          process.execPath,
          [corepack, 'pnpm', '--filter', 'auriga-dsh', 'typecheck'],
          directory,
          buildEnv,
          join(evidence, name + '-typecheck.log'),
        );
        assert.equal(compile.code, 0, 'control and mutant must compile');
        const entry = {
          name,
          testSha256,
          originalSourceSha256,
          sourceSha256: await hash(source),
          compileExit: compile.code,
        };
        manifest.variants.push(entry);
        entry.runExit = await check(directory, runtime, evidence, name);
        assert.equal(await hash(join(directory, testPath)), testSha256);
      }
      assert.equal(
        manifest.variants[0].testSha256,
        manifest.variants[1].testSha256,
      );
      assert.equal(
        manifest.variants[0].originalSourceSha256,
        manifest.variants[1].originalSourceSha256,
      );
      assert.notEqual(
        manifest.variants[1].sourceSha256,
        manifest.variants[1].originalSourceSha256,
      );
    }
    manifest.complete = true;
    return { evidence, complete: true };
  } finally {
    await writeFile(
      join(evidence, 'manifest.json'),
      JSON.stringify(manifest, null, 2),
    );
  }
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  assert.equal(process.argv.length, 3);
  console.log(JSON.stringify(await verifyNative(process.argv[2])));
}
