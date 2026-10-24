import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import {
  createLocalResources,
  safeFields,
} from '../src/local-resources/service.js';

function hostFixture(mode = 'standalone') {
  const row = {
    entryId: 'include:example',
    moduleName: 'undici',
    patchId: 'example',
    enabled: true,
    fiberPhase: 'active',
  };
  const form = {
    ns: 'example',
    revision: 0,
    secrets: [{ path: ['password'], set: true }],
    value: {
      label: 'hello',
      count: 2,
      verbose: false,
      password: 'must-not-leak',
      unknown: 'must-not-leak',
    },
    schema: {
      uid: 1,
      refs: {
        1: {
          type: 'object',
          dict: { label: 2, count: 3, verbose: 4, password: 2, targetUrl: 2 },
        },
        2: { type: 'string' },
        3: { type: 'number' },
        4: { type: 'boolean' },
      },
    },
  };
  let toggles = 0,
    writes = 0;
  const host = {
    profileContext: { dir: fileURLToPath(new URL('../', import.meta.url)) },
    pluginManager: {
      async listPlugins() {
        return [row];
      },
      async setPluginEnabled(_id, enabled) {
        toggles++;
        row.enabled = enabled;
        return { changed: true, application: 'restart-required' };
      },
    },
    settings: {
      writable: true,
      describe() {
        return [form];
      },
      async mutate(_ns, ops, revision) {
        assert.equal(revision, form.revision);
        writes++;
        for (const op of ops) form.value[op.path[0]] = op.value;
        form.revision++;
      },
    },
  };
  return {
    row,
    form,
    host,
    service: createLocalResources({ mode, getHost: () => host }),
    calls: () => ({ toggles, writes }),
  };
}
test('real host-shaped inventory drives detail, toggle and revisioned config readback, never market state', async () => {
  const { service, calls } = hostFixture();
  const list = await service.list(),
    item = list.items[0];
  assert.equal(item.name, 'undici');
  assert.equal(item.enabled, true);
  const detail = await service.detail(item.id);
  assert.deepEqual(
    detail.fields.map((f) => f.key),
    ['label', 'count', 'verbose'],
  );
  assert.doesNotMatch(JSON.stringify(detail), /must-not-leak|password|unknown/);
  const changed = await service.change('enabled', {
    id: item.id,
    revision: item.revision,
    enabled: false,
  });
  assert.equal(changed.application, 'restart-required');
  assert.equal(changed.item.plugin.enabled, false);
  await assert.rejects(
    service.change('enabled', {
      id: item.id,
      revision: item.revision,
      enabled: true,
    }),
    { code: 'common.conflict' },
  );
  const fresh = changed.item;
  const configured = await service.change('config', {
    id: item.id,
    revision: fresh.plugin.revision,
    configRevision: fresh.configRevision,
    changes: [{ key: 'label', value: 'new' }],
  });
  assert.equal(configured.item.fields[0].value, 'new');
  assert.deepEqual(calls(), { toggles: 1, writes: 1 });
});
test('managed, host readonly, protected modules, revoked and stale inputs never mutate', async () => {
  for (const kind of ['managed', 'protected', 'host', 'revoked']) {
    const f = hostFixture(kind === 'managed' ? 'managed' : 'standalone');
    if (kind === 'protected') f.row.moduleName = 'auriga-dsh';
    if (kind === 'host') f.row.readOnlyReason = 'unaddressable';
    const item = (await f.service.list()).items[0];
    await assert.rejects(
      f.service.change(
        'enabled',
        { id: item.id, revision: item.revision, enabled: false },
        () => kind !== 'revoked',
      ),
    );
    assert.deepEqual(f.calls(), { toggles: 0, writes: 0 });
  }
});
test('unknown IDs, arbitrary config keys, nested objects, script fields and stale config are rejected', async () => {
  const f = hostFixture();
  const item = (await f.service.list()).items[0];
  for (const changes of [
    [{ key: 'password', value: 'x' }],
    [{ key: 'label', value: { command: 'x' } }],
    [{ key: '__proto__', value: 'x' }],
    [{ key: 'targetUrl', value: 'https://example.com' }],
  ])
    await assert.rejects(
      f.service.change('config', {
        id: item.id,
        revision: item.revision,
        configRevision: 0,
        changes,
      }),
    );
  await assert.rejects(f.service.detail('https://example.com'));
  assert.deepEqual(safeFields({ ...f.form, secrets: undefined }), []);
  assert.deepEqual(f.calls(), { toggles: 0, writes: 0 });
});
test('queued writes recheck target identity, service lifecycle and authorization at invocation', async () => {
  const f = hostFixture();
  const item = (await f.service.list()).items[0];
  await assert.rejects(
    f.service.change(
      'enabled',
      { id: item.id, revision: item.revision, enabled: false },
      () => {
        f.host.pluginManager = { ...f.host.pluginManager };
        return true;
      },
    ),
    { code: 'common.unavailable' },
  );
  assert.deepEqual(f.calls(), { toggles: 0, writes: 0 });
  let live = true;
  const stopped = createLocalResources({
    mode: 'standalone',
    getHost: () => f.host,
    alive: () => live,
  });
  live = false;
  await assert.rejects(stopped.list(), { code: 'common.unavailable' });
});
test('a profile row without resolvable installed code remains unknown and cannot mutate', async () => {
  const f = hostFixture();
  f.row.moduleName = 'example-package-not-installed';
  const item = (await f.service.list()).items[0];
  assert.equal(item.installation, 'unknown');
  assert.equal(item.readonly, 'unverified');
  assert.equal(item.canToggle, false);
  assert.equal(item.canConfigure, false);
  await assert.rejects(
    f.service.change('enabled', {
      id: item.id,
      revision: item.revision,
      enabled: true,
    }),
    { code: 'common.forbidden' },
  );
  assert.deepEqual(f.calls(), { toggles: 0, writes: 0 });
});
test('Cordis scoped proxy identity is compared through its provider while replacements remain blocked', async () => {
  const f = hostFixture();
  const original = f.host.pluginManager;
  Object.defineProperty(f.host, 'pluginManager', {
    get() {
      return new Proxy(original, {
        get(target, key) {
          return key === Symbol.for('cordis.original')
            ? original
            : Reflect.get(target, key);
        },
      });
    },
  });
  const item = (await f.service.list()).items[0];
  const result = await f.service.change('enabled', {
    id: item.id,
    revision: item.revision,
    enabled: false,
  });
  assert.equal(result.item.plugin.enabled, false);
  assert.equal(f.calls().toggles, 1);
});
