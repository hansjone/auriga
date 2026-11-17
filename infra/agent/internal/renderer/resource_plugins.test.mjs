import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  preparePlugins,
  governPlugins,
  observePlugins,
} from './resource_plugins.mjs';
const bytes = Buffer.from('verified npm pack fixture');
const digest = 'sha256:' + createHash('sha256').update(bytes).digest('hex');
const plugin = {
  name: '@example/test',
  desiredState: 'ENABLED',
  installationMode: 'required',
  digest,
  path: '/run/auriga/catalog-plugins/' + digest.slice(7) + '.tgz',
};
const policy = {
  newSessionRequired: false,
  allowPersonalPlugins: false,
  managedPluginNames: [plugin.name],
  plugins: [plugin],
};
test('saved bundle enablement and existing entry IDs never substitute for a live active fiber', async () => {
  for (const phase of ['active', 'pending', 'loading', 'failed', null]) {
    const manager = {
      async listBundles() {
        return [
          {
            name: plugin.name,
            enabled: true,
            installed: true,
            rows: [{ entryId: 'entry', moduleName: plugin.name }],
          },
        ];
      },
      async listPlugins() {
        return [
          {
            entryId: 'entry',
            moduleName: plugin.name,
            enabled: true,
            fiberPhase: phase,
          },
        ];
      },
    };
    const [row] = await observePlugins(manager);
    assert.equal(row.loaded, phase === 'active');
    assert.equal(row.pending, phase === 'pending' || phase === 'loading');
  }
});
test('personal registry installation stays available without replacing a managed package', async () => {
  const manager = {
    async inspect(target) {
      return { status: 'accepted', kind: 'registry', name: target };
    },
    async listBundles() {
      return [];
    },
  };
  for (const method of [
    'installBundle',
    'setBundleEnabled',
    'removeBundle',
    'setPluginEnabled',
  ])
    manager[method] = async () => 'native';
  governPlugins({ effect() {} }, manager, {
    ...policy,
    allowPersonalPlugins: true,
  });
  assert.equal(await manager.installBundle('@example/personal'), 'native');
  await assert.rejects(manager.installBundle(plugin.name));
});
test('public CLI receives verified tgz after removing the old managed package', async () => {
  const calls = [];
  await preparePlugins(
    policy,
    async (args) => calls.push(args),
    new Set([plugin.name]),
    () => bytes,
  );
  assert.deepEqual(calls, [
    ['remove', plugin.name, '--config.ignore-scripts=true'],
    ['add', plugin.path, '--ignore-scripts'],
  ]);
});
test('private recovery removes managed plugins and never installs revoked bytes', async () => {
  const calls = [];
  await preparePlugins(
    { ...policy, newSessionRequired: true },
    async (args) => calls.push(args),
    new Set([plugin.name]),
    () => {
      throw new Error('must not read');
    },
  );
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], 'remove');
});
test('payload corruption and CLI failure prevent startup', async () => {
  await assert.rejects(
    preparePlugins(
      policy,
      async () => {},
      new Set(),
      () => Buffer.from('changed'),
    ),
    /changed/,
  );
  await assert.rejects(
    preparePlugins(
      policy,
      async () => {
        throw new Error('CLI failed');
      },
      new Set(),
      () => bytes,
    ),
    /CLI failed/,
  );
});
test('required/default/optional and personal policy guard public native mutations', async () => {
  for (const mode of ['required', 'default', 'optional']) {
    const manager = {
      async listBundles() {
        return [{ name: plugin.name, rows: [{ entryId: 'entry' }] }];
      },
    };
    for (const method of [
      'installBundle',
      'setBundleEnabled',
      'removeBundle',
      'setPluginEnabled',
    ])
      manager[method] = async () => 'native';
    governPlugins({ effect() {} }, manager, {
      ...policy,
      plugins: [{ ...plugin, installationMode: mode }],
    });
    await assert.rejects(manager.installBundle('file:/unverified.tgz'));
    await assert.rejects(manager.setBundleEnabled('personal', true));
    await assert.rejects(
      manager.setBundleEnabled(plugin.name, false),
      /资源目录/,
    );
    await assert.rejects(manager.setPluginEnabled('entry', false), /资源目录/);
    await assert.rejects(manager.removeBundle(plugin.name), /资源目录/);
  }
});

test('retired managed names cannot be reinstalled from the native registry', async () => {
  const manager = {
    async inspect(name) {
      return { status: 'accepted', kind: 'registry', name };
    },
    async listBundles() {
      return [];
    },
    async installBundle() {
      throw new Error('native install must not run');
    },
    async removeBundle() {},
    async setBundleEnabled() {},
    async setPluginEnabled() {},
  };
  governPlugins({ effect() {} }, manager, {
    ...policy,
    allowPersonalPlugins: true,
    plugins: [],
  });
  await assert.rejects(manager.installBundle(plugin.name), /资源目录/);
});

test('disallowing personal plugins cleans command-line dependencies and preserves L0', async () => {
  for (const managed of [true, false]) {
    const calls = [];
    const selected = managed
      ? policy
      : { ...policy, managedPluginNames: [], plugins: [] };
    await preparePlugins(
      selected,
      async (args) => calls.push(args),
      new Set([
        'auriga-dsh',
        '@deepseek-ai/dsh-base',
        'personal-cli',
        ...(managed ? [plugin.name] : []),
      ]),
      () => bytes,
    );
    assert(
      calls.some((args) => args[0] === 'remove' && args[1] === 'personal-cli'),
    );
    assert(
      !calls.some((args) =>
        ['auriga-dsh', '@deepseek-ai/dsh-base'].includes(args[1]),
      ),
    );
  }
  const calls = [];
  await preparePlugins(
    { ...policy, allowPersonalPlugins: true },
    async (args) => calls.push(args),
    new Set(['personal-cli']),
    () => bytes,
  );
  assert(
    !calls.some((args) => args[0] === 'remove' && args[1] === 'personal-cli'),
  );
});

test('a bundle without rows is not loaded and does not break the observation endpoint', async () => {
  for (const rows of [undefined, null, []]) {
    const result = await observePlugins({
      async listPlugins() {
        return [];
      },
      async listBundles() {
        return [{ name: plugin.name, enabled: true, installed: true, rows }];
      },
    });
    assert.equal(result[0].loaded, false);
    assert.equal(result[0].pending, false);
  }
});
