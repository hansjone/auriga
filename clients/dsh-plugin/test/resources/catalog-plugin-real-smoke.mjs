/** Isolated rc.3 host evidence. Run from the repository root; no user profile or fixed port. */
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import {
  mkdirSync,
  mkdtempSync,
  writeFileSync,
  readFileSync,
  existsSync,
} from 'node:fs';
import { resolve, join, delimiter } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { Supervisor } from '../../../installer/runtime/supervisor.mjs';
import { ProcessTree } from '../../../installer/runtime/process-tree.mjs';
import { resourcePluginControl } from '../../../installer/runtime/resource-plugins.mjs';

const cli = resolve('.dev/p2b-dsh/node_modules/@deepseek-ai/dsh/lib/bin.js');
const version = JSON.parse(
  readFileSync(
    resolve('.dev/p2b-dsh/node_modules/@deepseek-ai/dsh/package.json'),
  ),
).version;
assert.equal(version, '0.2.1-rc.3');
const evidence = mkdtempSync(resolve('.dev/p2b-host-'));
const home = join(evidence, 'home');
mkdirSync(home);
const tools = resolve('.dev/p2b-tools/node_modules/.bin');
const patch = join(evidence, 'probe.patch.yml');
writeFileSync(
  patch,
  '- insert:\n    - id: auriga-catalog-plugin-real-probe\n      name: ' +
    JSON.stringify(
      pathToFileURL(
        resolve(
          'clients/dsh-plugin/test/resources/catalog-plugin-real-probe.mjs',
        ),
      ).href,
    ) +
    '\n',
);
const clean = Object.fromEntries(
  Object.entries(process.env).filter(
    ([k]) => !k.startsWith('DSH_') && !k.startsWith('AURIGA_'),
  ),
);
const pathKey =
  Object.keys(clean).find((k) => k.toLowerCase() === 'path') ?? 'PATH';
const env = {
  ...clean,
  [pathKey]: tools + delimiter + clean[pathKey],
  DSH_HOME: home,
  P2B_EVIDENCE: evidence,
};
const tree = new ProcessTree(home);
let control;
const supervisor = new Supervisor(home, tree, null, {
  clearPlugins: (names) => control.clearPlugins(names),
  pluginsCleared: () => control?.pluginsCleared() ?? true,
  time: () => ({ bootId: '2026-11-12T00:00:00.000Z', monotonicMillis: 1000 }),
});
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
    cwd: evidence,
    env,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  },
);
let log = '';
child.stdout.on('data', (b) => {
  log += b;
});
child.stderr.on('data', (b) => {
  log += b;
});
const exited = once(child, 'exit');
tree.attach(child.pid);
let release;
async function result(name, timeout = 120000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    if (existsSync(join(evidence, 'error.json')))
      throw new Error(readFileSync(join(evidence, 'error.json'), 'utf8'));
    try {
      return JSON.parse(readFileSync(join(evidence, name + '.json'), 'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT' && !(error instanceof SyntaxError))
        throw error;
    }
    if (child.exitCode !== null || Date.now() > deadline)
      throw new Error(
        'host evidence timeout: ' + name + '\n' + log.slice(-4000),
      );
    await delay(200);
  }
}
try {
  control = resourcePluginControl({
    home,
    profileFile: join(home, 'profiles/web/package.json'),
    remove: (names) => {
      const removed = spawnSync(
        process.execPath,
        [
          cli,
          'plugin',
          '--profile',
          'web',
          'remove',
          ...names,
          '--ignore-scripts',
        ],
        {
          cwd: evidence,
          env,
          windowsHide: true,
          encoding: 'utf8',
          timeout: 120000,
        },
      );
      writeFileSync(
        join(evidence, 'remove.log'),
        (removed.stdout ?? '') + (removed.stderr ?? ''),
      );
      assert.equal(removed.status, 0, removed.stderr);
    },
  });
  const prepared = await result('prepared');
  supervisor.trust = prepared.trust;
  release = tree.singleton();
  const approvalDeadline = Date.now() + 60000;
  while (
    supervisor.guardian.read()?.guardian.state !== 'active' &&
    Date.now() < approvalDeadline
  ) {
    await supervisor.reconcile();
    await delay(250);
  }
  assert.equal(supervisor.guardian.read().guardian.state, 'active');
  const installed = await result('installed');
  assert.equal(installed.observed.name, '@example/catalog-plugin');
  assert.equal(installed.observed.active, true);
  assert.equal(installed.installation.state, 'applied');
  const url = log.match(/http:\/\/127\.0\.0\.1:\d+/)?.[0];
  assert.ok(url);
  assert.equal((await fetch(url)).status, 200);
  writeFileSync(join(evidence, 'revoke-now'), '');
  await result('revoked');
  tree.refresh();
  await supervisor.reconcile();
  assert.equal(await tree.isStopped(), true);
  assert.equal(control.pluginsCleared(), true);
  assert.equal(supervisor.guardian.read().guardian.reason, 'revoked');
  writeFileSync(
    join(evidence, 'summary.json'),
    JSON.stringify(
      {
        ok: true,
        version,
        installed,
        revoked: true,
        stopped: true,
        removed: true,
        normalPageStatus: 200,
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify({ ok: true, evidence }));
} finally {
  try {
    await tree.stopTree();
  } finally {
    if (child.exitCode === null) child.kill();
    await exited;
    release?.();
    writeFileSync(
      join(evidence, 'host.log'),
      log.replace(/token=[^\s&]+/g, 'token=REDACTED'),
    );
  }
}
