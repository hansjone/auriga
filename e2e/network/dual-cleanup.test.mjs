import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtemp,
  mkdir,
  writeFile,
  readFile,
  symlink,
  realpath,
  rm,
} from 'node:fs/promises';
import { join, resolve, dirname } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { dualHostState } from './support/dual-state.mjs';

test('DSH profile只能位于Node自有根，不能进入Java递归清理根', () => {
  const owner = resolve('owned'),
    java = join(owner, 'auriga-java-bridge-example');
  assert.equal(dualHostState(owner, java, 'A'), join(owner, 'dsh-A'));
  assert.equal(dualHostState(owner, java, 'B'), join(owner, 'dsh-B'));
  assert.throws(() => dualHostState(owner, resolve('outside'), 'A'));
  assert.throws(() => dualHostState(owner, java, '../outside'));
});

test('真实JVM清理与Node清理后，profile源码junction的目标仍完整保留', async (t) => {
  const target = resolve('e2e/network/target');
  await mkdir(target, { recursive: true });
  const artifacts = await mkdtemp(join(target, 'cleanup-check-'));
  const fixture = join(artifacts, 'private');
  const owner = join(fixture, 'node-owner'),
    java = join(owner, 'auriga-java-bridge-example');
  const source = join(fixture, 'source');
  await mkdir(source, { recursive: true });
  await mkdir(java, { recursive: true });
  const sentinel = join(source, 'nats-binary.js'),
    bytes = 'source sentinel must survive both cleanups\n';
  await writeFile(sentinel, bytes);
  t.after(async () => {
    assert.equal(dirname(resolve(fixture)), resolve(artifacts));
    await rm(fixture, { recursive: true, force: true });
  });
  for (const name of ['A', 'B']) {
    const state = dualHostState(owner, java, name);
    assert.equal(dirname(state), owner);
    const modules = join(state, 'dsh/profiles/web/node_modules');
    await mkdir(modules, { recursive: true });
    const link = join(modules, 'source');
    await symlink(
      source,
      link,
      process.platform === 'win32' ? 'junction' : 'dir',
    );
    assert.equal(await realpath(link), await realpath(source));
  }
  // Every link target is an exclusive fixture sibling, never a real repo/cache.
  assert.equal(dirname(await realpath(source)), await realpath(fixture));
  const run = promisify(execFile);
  const binary = (name) =>
    process.env.JAVA_HOME
      ? join(
          process.env.JAVA_HOME,
          'bin',
          name + (process.platform === 'win32' ? '.exe' : ''),
        )
      : name;
  await run(
    binary('javac'),
    [
      '-d',
      artifacts,
      resolve('e2e/network/support/NodeOwnedCleanupProbe.java'),
    ],
    { windowsHide: true, timeout: 30000 },
  );
  const result = await run(
    binary('java'),
    ['-cp', artifacts, 'NodeOwnedCleanupProbe', java, owner],
    { windowsHide: true, timeout: 30000 },
  );
  assert.equal(await readFile(sentinel, 'utf8'), bytes);
  // The Node owner holds the links. Node removes link entries, not their targets.
  assert.equal(dirname(resolve(owner)), resolve(fixture));
  await rm(owner, { recursive: true, force: true });
  assert.equal(await readFile(sentinel, 'utf8'), bytes);
  await writeFile(
    join(artifacts, 'manifest.json'),
    JSON.stringify(
      {
        javaCleanup: result.stdout.trim(),
        nodeCleanup: true,
        sentinelUnchanged: true,
        scope: 'owned fixture only; no real repository link target',
      },
      null,
      2,
    ),
  );
});
