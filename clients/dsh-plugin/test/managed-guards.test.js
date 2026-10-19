import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  NATIVE_PLUGIN_MUTATIONS,
  guardPluginManager,
  guardWorkspaceRegistry,
  isNativePluginManageRequest,
  isNativePluginManageRpc,
  assertWorkspaceCreatePath,
  DEFAULT_WORKSPACE_ROOT,
} from '../src/managed/guards.js';

function fakePluginManager() {
  const calls = [];
  const pm = {};
  for (const name of [
    ...NATIVE_PLUGIN_MUTATIONS,
    'listPlugins',
    'listBundles',
    'inspect',
  ]) {
    pm[name] = async (...args) => {
      calls.push(name);
      return { ok: true, args };
    };
  }
  return { pm, calls };
}

test('guard blocks installBundle with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.installBundle({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard blocks removeBundle with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.removeBundle({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard blocks setBundleEnabled with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.setBundleEnabled({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard blocks setPluginEnabled with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.setPluginEnabled({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard blocks setVersionExemption with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.setVersionExemption({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard blocks cancelInstall with common.forbidden', async () => {
  const { pm } = fakePluginManager();
  guardPluginManager(pm);
  await assert.rejects(
    () => pm.cancelInstall({}),
    (err) => err.code === 'common.forbidden',
  );
});

test('guard keeps read-only pluginManager methods', async () => {
  const { pm, calls } = fakePluginManager();
  guardPluginManager(pm);
  assert.equal((await pm.listPlugins()).ok, true);
  assert.equal((await pm.listBundles()).ok, true);
  assert.equal((await pm.inspect()).ok, true);
  assert.deepEqual(calls, ['listPlugins', 'listBundles', 'inspect']);
});

test('HTTP and RPC blocklists match DSH pluginManager mutations', () => {
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/installBundle'),
    true,
  );
  assert.equal(
    isNativePluginManageRequest('POST', '/api/pluginManager/listPlugins'),
    false,
  );
  assert.equal(isNativePluginManageRpc('pluginManager/removeBundle'), true);
  assert.equal(isNativePluginManageRpc('pluginManager/listPlugins'), false);
});

test('workspace create outside root is forbidden', async () => {
  const created = [];
  const registry = {
    create: async (path, title) => {
      created.push({ path, title });
      return { path, title };
    },
  };
  guardWorkspaceRegistry(registry, DEFAULT_WORKSPACE_ROOT);
  await assert.rejects(
    () => registry.create('/tmp/evil', 'x'), // gitleaks:allow
    (err) => err.code === 'common.forbidden',
  );
  assert.equal(created.length, 0);

  const ok = await registry.create('/home/dsh/workspace/notes', 'notes');
  assert.equal(ok.path, '/home/dsh/workspace/notes');
  assert.equal(created.length, 1);
});

test('assertWorkspaceCreatePath rejects empty and outside paths', () => {
  assert.equal(assertWorkspaceCreatePath('').ok, false);
  assert.equal(
    assertWorkspaceCreatePath('/etc/passwd').code, // gitleaks:allow
    'common.forbidden',
  );
  assert.equal(assertWorkspaceCreatePath('/home/dsh/workspace/a').ok, true);
});
