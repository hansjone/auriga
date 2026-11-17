import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const { governPlugins, preparePlugins, observePlugins } = await import(
  pathToFileURL(process.argv[2]).href
);
const policy = JSON.parse(process.argv[3]);
const plugin = policy.plugins[0];
const bundles = new Map([
  [plugin.name, true],
  ['auriga-dsh', true],
  ['@deepseek-ai/dsh-base', true],
]);
if (process.argv[4] === 'cleanup') {
  bundles.set('personal-cli', true);
  await preparePlugins(
    policy,
    async ([operation, target]) => {
      if (operation === 'remove') bundles.delete(target);
      else bundles.set(plugin.name, true);
    },
    new Set(bundles.keys()),
    () => Buffer.from(process.argv[5], 'base64'),
  );
  assert(!bundles.has('personal-cli'));
  assert(bundles.has('auriga-dsh') && bundles.has('@deepseek-ai/dsh-base'));
}
const manager = {
  async listBundles() {
    return [...bundles].map(([name, enabled]) => ({
      name,
      enabled,
      installed: true,
      version: '1.0.0',
      rows: [{ entryId: name, moduleName: name }],
    }));
  },
  async listPlugins() {
    return [...bundles].map(([name, enabled]) => ({
      entryId: name,
      moduleName: name,
      enabled,
      fiberPhase: enabled ? 'active' : 'disposed',
    }));
  },
  async installBundle() {},
  async setBundleEnabled(name, enabled) {
    bundles.set(name, enabled);
  },
  async setPluginEnabled(name, enabled) {
    bundles.set(name, enabled);
  },
  async removeBundle(name) {
    bundles.delete(name);
  },
};
governPlugins({ effect() {} }, manager, policy);
await assert.rejects(manager.setBundleEnabled(plugin.name, false), /资源目录/);
await assert.rejects(manager.setPluginEnabled(plugin.name, false), /资源目录/);
await assert.rejects(manager.removeBundle(plugin.name), /资源目录/);
console.log(
  JSON.stringify({
    privateOnly: false,
    skills: [],
    plugins: await observePlugins(manager),
  }),
);
