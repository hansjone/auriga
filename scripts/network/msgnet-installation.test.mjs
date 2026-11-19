import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, writeFile, chmod, link, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installationInvocation } from './msgnet-installation.mjs';

async function fixture(t) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'msgnet-launcher-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const ref = async (name, value) => {
    const file = path.join(directory, name);
    await writeFile(file, value, { mode: 0o600 });
    return {
      path: file,
      sha256: createHash('sha256').update(value).digest('hex'),
    };
  };
  // These are inert files: no test starts Java or any process.
  const java = await ref(
    process.platform === 'win32' ? 'java.exe' : 'java',
    'inert Java path fixture',
  );
  const jar = await ref('platform-exec.jar', 'inert jar fixture');
  const launcher = (
    await ref('launcher.json', JSON.stringify({ version: 1, java, jar }))
  ).path;
  const plan = (await ref('plan.json', '{}')).path;
  return { directory, java, jar, launcher, plan };
}

test('fixed invocation uses exact main, explicit resource bounds and a minimal environment', async (t) => {
  const f = await fixture(t);
  const result = await installationInvocation('prepare', f.launcher, f.plan, {
    SystemRoot: 'synthetic-system-root',
    UNRELATED_SETTING: 'not-forwarded',
    PATH: 'not-forwarded',
  });
  assert.equal(result.file, f.java.path);
  assert.ok(result.args.includes('-Xmx512m'));
  assert.ok(result.args.includes('-XX:ActiveProcessorCount=2'));
  assert.ok(
    result.args.includes(
      '-Dloader.main=auriga.collab.network.keys.MsgnetInstallationOperator',
    ),
  );
  assert.deepEqual(result.args.slice(-2), ['prepare', f.plan]);
  assert.deepEqual(result.env, {
    SystemRoot: 'synthetic-system-root',
    LANG: 'C.UTF-8',
  });
});

test('loader/JVM/classpath environment overrides and unknown operations reject', async (t) => {
  const f = await fixture(t);
  for (const name of [
    'JAVA_TOOL_OPTIONS',
    'JDK_JAVA_OPTIONS',
    '_JAVA_OPTIONS',
    'CLASSPATH',
    'LOADER_MAIN',
    'loader_path',
  ]) {
    await assert.rejects(
      installationInvocation('sign', f.launcher, f.plan, {
        [name]: 'synthetic',
      }),
    );
  }
  await assert.rejects(
    installationInvocation('some-other-main', f.launcher, f.plan, {}),
  );
});

test('modified pins, duplicate/unknown JSON fields and relative paths reject', async (t) => {
  const f = await fixture(t);
  await writeFile(f.jar.path, 'changed bytes');
  await assert.rejects(
    installationInvocation('finalize', f.launcher, f.plan, {}),
  );
  const json = JSON.stringify({ version: 1, java: f.java, jar: f.jar });
  await writeFile(f.launcher, `{"version":1,${json.slice(1)}`);
  await assert.rejects(
    installationInvocation('prepare', f.launcher, f.plan, {}),
  );
  await writeFile(
    f.launcher,
    JSON.stringify({ version: 1, java: f.java, jar: f.jar, main: 'unknown' }),
  );
  await assert.rejects(
    installationInvocation('prepare', f.launcher, f.plan, {}),
  );
  await assert.rejects(
    installationInvocation('prepare', 'launcher.json', f.plan, {}),
  );
});

test('multiply linked files and public POSIX plan permissions reject', async (t) => {
  const f = await fixture(t);
  await link(f.plan, path.join(f.directory, 'plan-hardlink.json'));
  await assert.rejects(
    installationInvocation('prepare', f.launcher, f.plan, {}),
  );
  const otherPlan = path.join(f.directory, 'other-plan.json');
  await writeFile(otherPlan, '{}', { mode: 0o600 });
  if (process.platform !== 'win32') {
    await chmod(otherPlan, 0o644);
    await assert.rejects(
      installationInvocation('prepare', f.launcher, otherPlan, {}),
    );
  } else {
    // Java performs the Windows owner ACL check before reading the plan itself.
    assert.equal(
      (
        await installationInvocation('prepare', f.launcher, otherPlan, {})
      ).args.at(-1),
      otherPlan,
    );
  }
});
