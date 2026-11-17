import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { writeFileSync } from 'node:fs';
import { pluginState } from '../../src/resources/plugin-state.js';
import { catalogFixture } from './catalog-fixture.js';
import { acquireHostLock } from '../../src/resources/host-lock.js';
import { setTimeout as delay } from 'node:timers/promises';

test('byte proof survives a real Node restart but the pending-operation process fence does not', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const options = { home: f.home, who: f.who };
  const state = pluginState(options),
    row = f.getRow(),
    name = f.manifest.plugin.packageName;
  await state.write(row, name, true, 'install');
  assert.equal(pluginState(options).waiting(state.read(row, name), row), true);
  const reloaded = await import('../../src/resources/plugin-state.js?reload');
  assert.equal(
    reloaded.pluginState(options).waiting(state.read(row, name), row),
    true,
  );
  const child = spawnSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import { pluginState } from ${JSON.stringify(new URL('../../src/resources/plugin-state.js', import.meta.url).href)};
    const store = pluginState(JSON.parse(process.argv[1]));
    const row = JSON.parse(process.argv[2]), name = process.argv[3];
    const value = store.read(row, name);
    console.log(JSON.stringify({ installed: value.installed, waiting: store.waiting(value, row) }));
  `,
      JSON.stringify(options),
      JSON.stringify(row),
      name,
    ],
    { encoding: 'utf8', windowsHide: true },
  );
  assert.equal(child.status, 0, child.stderr);
  assert.deepEqual(JSON.parse(child.stdout), {
    installed: true,
    waiting: false,
  });
  assert.equal(
    state.waiting(state.read(row, name), {
      ...row,
      generation: row.generation + 1,
    }),
    false,
  );
  assert.equal(
    state.read(
      {
        ...row,
        resource: {
          ...row.resource,
          artifacts: [
            {
              ...row.resource.artifacts[0],
              digest: 'sha256:' + 'b'.repeat(64),
            },
          ],
        },
      },
      name,
    ),
    undefined,
  );
  assert.equal(state.read(row, 'different-name'), undefined);
  assert.equal(
    pluginState({
      ...options,
      who: { ...f.who, ownerId: '019a0000-0000-7000-8000-000000000099' },
    }).read(row, name),
    undefined,
  );
  writeFileSync(
    join(f.directory, 'plugin-installations', f.packageId + '.json'),
    '{broken',
  );
  assert.throws(() => state.read(row, name));
});

test('proof writes briefly retry contention but never recover or remove another writer lock', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const store = pluginState({ home: f.home, who: f.who });
  const name = f.manifest.plugin.packageName;
  const lock = acquireHostLock(f.home);
  const pending = store.write(f.getRow(), name, true);
  await delay(15);
  lock.release();
  await pending;
  assert.equal(store.read(f.getRow(), name).installed, true);
  const other = acquireHostLock(f.home);
  try {
    await assert.rejects(store.write(f.getRow(), name, false), {
      code: 'EEXIST',
    });
    other.check();
    assert.equal(store.read(f.getRow(), name).installed, true);
  } finally {
    other.release();
  }
});
