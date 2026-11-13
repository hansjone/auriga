import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile, readFile, mkdtemp } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { createHash } from 'node:crypto';

// Run from the repository root after npm ci with the installer lock in .dev/p1c-dsh.
const cli = resolve('.dev/p1c-dsh/node_modules/@deepseek-ai/dsh/lib/bin.js');
assert.equal(
  JSON.parse(
    await readFile(
      resolve('.dev/p1c-dsh/node_modules/@deepseek-ai/dsh/package.json'),
      'utf8',
    ),
  ).version,
  '0.2.1-rc.3',
);
const evidence = await mkdtemp(resolve('.dev/p1c-host-'));
const home = join(evidence, 'home'),
  workspace = join(home, 'workspace');
await mkdir(workspace, { recursive: true });
// The public provider uses the nearest .git ancestor as projectRoot.
await mkdir(join(workspace, '.git'));
const content = (body) =>
  `---\nname: p1c-collision\ndescription: "Isolated public test fixture"\n---\n${body}\n`;
const shared = join(home, 'auriga/skills-active/current/p1c-collision');
await mkdir(shared, { recursive: true });
await writeFile(join(shared, 'SKILL.md'), content('SHARED'));
const digest = 'sha256:' + createHash('sha256').update('SHARED').digest('hex');
const patch = join(evidence, 'probe.patch.yml');
await writeFile(
  patch,
  `- insert:\n    - id: auriga-shared-skills\n      name: '@deepseek-ai/dsh-skill-filesystem'\n      config:\n        providerName: auriga-shared\n        includeDefaultRoots: false\n        customSkillDirs:\n          - !!js process.getBuiltinModule('node:path').join(process.env.DSH_HOME, 'auriga', 'skills-active', 'current')\n        watch: true\n    - id: auriga-private-real-probe\n      name: ${JSON.stringify(pathToFileURL(resolve('clients/dsh-plugin/test/resources/catalog-host-real-probe.mjs')).href)}\n`,
);
const results = {};
for (const phase of ['shared-adapter']) {
  const output = join(evidence, phase + '.json');
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([k]) => !k.startsWith('AURIGA_') && !k.startsWith('DSH_'),
    ),
  );
  const child = spawn(
    process.execPath,
    [
      cli,
      'web',
      '--patch',
      patch,
      '--no-open',
      '--host',
      '127.0.0.1',
      '--port',
      '0',
    ],
    {
      cwd: workspace,
      env: {
        ...env,
        DSH_HOME: home,
        DSH_AGENTS_HOME: join(home, 'agents'),
        P1C_DSH_CLI: cli,
        P1C_WORKSPACE: workspace,
        P1C_RESULT: output,
      },
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let log = '',
    exited = false;
  child.stdout.on('data', (b) => {
    log += b;
  });
  child.stderr.on('data', (b) => {
    log += b;
  });
  child.on('exit', () => {
    exited = true;
  });
  try {
    const deadline = Date.now() + 45000;
    while (!results[phase]) {
      try {
        results[phase] = JSON.parse(await readFile(output, 'utf8'));
      } catch (error) {
        if (error.code !== 'ENOENT' && !(error instanceof SyntaxError))
          throw error;
      }
      if (results[phase]) break;
      if (exited || Date.now() > deadline)
        throw new Error('isolated DSH probe failed: ' + phase);
      await delay(100);
    }
    assert.equal(results[phase].ok, true, JSON.stringify(results[phase]));
    assert.equal(results[phase].results.length, 3);
    for (const row of results[phase].results) {
      assert.equal(row.state, 'observed');
      assert.equal(row.provider, 'auriga-shared');
      assert.equal(resolve(row.path), resolve(shared, 'SKILL.md'));
      assert.equal(row.contentDigest, digest);
      assert.ok(row.sessionId);
    }
  } finally {
    if (!exited) {
      const done = once(child, 'exit');
      child.kill();
      await done;
    }
    await writeFile(
      join(evidence, phase + '.log'),
      log.replace(/token=[^&\s]+/g, 'token=REDACTED'),
    );
  }
}
await writeFile(
  join(evidence, 'summary.json'),
  JSON.stringify({ ok: true, version: '0.2.1-rc.3', results }, null, 2),
);
console.log(JSON.stringify({ ok: true, evidence, processesStopped: true }));
