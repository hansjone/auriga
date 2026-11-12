import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { dirname } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { mergeCordisFixture } from './unified-cordis-fixtures.mjs';
const evidence = resolve('services/contracts/target/p1k-r3-real-dsh');
const installation = resolve('services/contracts/target/p1k-r3-dsh');
const cli = join(installation, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
const require = createRequire(cli);
const bundledSkills = join(
  dirname(require.resolve('@deepseek-ai/dsh-agent-preset/package.json')),
  'skills',
);
assert.equal(
  JSON.parse(
    await readFile(
      join(installation, 'node_modules/@deepseek-ai/dsh/package.json'),
      'utf8',
    ),
  ).version,
  '0.2.1-rc.3',
);
await mkdir(evidence, { recursive: true });
const index = JSON.parse(
  await readFile('contracts/resource/cordis-upgrade-fixture.json', 'utf8'),
);
const host = mergeCordisFixture('[]\n', null, index.fixedRootExpression);
assert(host.shared);
const home = join(evidence, 'home'),
  workspace = join(home, 'workspace');
await mkdir(workspace, { recursive: true });
for (const [folder, name] of [
  ['auriga/skills-active/current/p1k-shared-probe', 'p1k-shared-probe'],
  ['skills/p1k-default-root-probe', 'p1k-default-root-probe'],
]) {
  const dir = join(home, folder);
  await mkdir(dir, { recursive: true });
  await writeFile(
    join(dir, 'SKILL.md'),
    `---\nname: ${name}\ndescription: Public isolated contract verification fixture.\n---\n\nFixture body ${name}.\n`,
  );
}
const results = {};
for (const phase of ['baseline', 'shared']) {
  const patch = join(evidence, phase + '.patch.yml'),
    output = join(evidence, phase + '.json');
  const probe =
    '- id: p1k-skill-probe\n  name: ' +
    JSON.stringify(
      pathToFileURL(
        resolve('services/contracts/scripts/unified-real-dsh-probe.mjs'),
      ).href,
    ) +
    '\n';
  await writeFile(output, '');
  await writeFile(
    patch,
    (phase === 'shared' ? host.text : '') +
      '- insert:\n' +
      probe
        .split('\n')
        .filter(Boolean)
        .map((line) => '    ' + line)
        .join('\n') +
      '\n',
  );
  let log = '';
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
        DSH_AGENTS_HOME: join(home, 'isolated-agents'),
        DSH_BUNDLED_SKILL_DIR: bundledSkills,
        P1K_DSH_CLI: cli,
        P1K_WORKSPACE: workspace,
        P1K_RESULT: output,
      },
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  child.stdout.on('data', (b) => (log += b));
  child.stderr.on('data', (b) => (log += b));
  let exited = false;
  child.on('exit', () => (exited = true));
  try {
    const deadline = Date.now() + 45000;
    for (;;) {
      try {
        results[phase] = JSON.parse(await readFile(output, 'utf8'));
        break;
      } catch (e) {
        if (e.code !== 'ENOENT' && !(e instanceof SyntaxError)) throw e;
      }
      if (exited) throw new Error('DSH exited before ' + phase + ' probe');
      if (Date.now() > deadline) throw new Error('DSH probe timeout ' + phase);
      await delay(100);
    }
    assert(results[phase].ok, JSON.stringify(results[phase]));
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
for (const baseline of results.baseline.results) {
  const after = results.shared.results.find(
    (x) => x.preset === baseline.preset,
  );
  assert(!baseline.skills.some((x) => x.name === 'p1k-shared-probe'));
  const shared = after.skills.find((x) => x.name === 'p1k-shared-probe');
  assert.equal(shared?.provider, 'auriga-shared');
  assert(
    shared.path.startsWith(join(home, 'auriga', 'skills-active', 'current')),
  );
  assert(baseline.skills.some((x) => x.name === 'p1k-default-root-probe'));
  assert(baseline.skills.some((x) => x.name === 'editing-cordis-compositions'));
  for (const skill of baseline.skills)
    assert.deepEqual(
      after.skills.find((x) => x.name === skill.name),
      skill,
      'preserved ' + baseline.preset + '/' + skill.name,
    );
}
assert(
  results.shared.results
    .find((x) => x.preset === 'cordis')
    .skills.some((x) => x.name === 'editing-cordis-compositions'),
);
await writeFile(
  join(evidence, 'summary.json'),
  JSON.stringify({ ok: true, ...results }, null, 2),
);
console.log(
  'Real DSH 0.2.1-rc.3: cordis/standard/ptc shared catalog and all baseline skills preserved. All child processes stopped.',
);
