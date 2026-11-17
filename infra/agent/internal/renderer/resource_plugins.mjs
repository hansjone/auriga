import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

const packageName = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;
export async function observePlugins(manager) {
  if (
    typeof manager?.listBundles !== 'function' ||
    typeof manager?.listPlugins !== 'function'
  )
    throw new Error('Public plugin observation API unavailable');
  const entries = await manager.listPlugins();
  return (await manager.listBundles()).map((row) => {
    const actual = (row.rows ?? []).map((entry) =>
      entries.find(
        (live) =>
          live.entryId === entry.entryId &&
          live.moduleName === entry.moduleName,
      ),
    );
    const valid =
      row.enabled &&
      !row.error &&
      actual.length > 0 &&
      actual.every((entry) => entry?.enabled);
    return {
      name: row.name,
      version: row.version,
      enabled: row.enabled,
      installed: row.installed,
      error: row.error,
      loaded: valid && actual.every((entry) => entry.fiberPhase === 'active'),
      pending:
        valid &&
        actual.every((entry) =>
          ['active', 'pending', 'loading'].includes(entry.fiberPhase),
        ) &&
        actual.some((entry) => entry.fiberPhase !== 'active'),
    };
  });
}

export function pluginPlan(policy) {
  const names = policy.managedPluginNames ?? [];
  const plugins = policy.plugins ?? [];
  if (
    !Array.isArray(names) ||
    !Array.isArray(plugins) ||
    names.length > 4096 ||
    plugins.length > 128 ||
    new Set(names).size !== names.length ||
    names.some(
      (name) =>
        !packageName.test(name) ||
        name === 'auriga-dsh' ||
        name.startsWith('@deepseek-ai/'),
    )
  )
    throw new Error('Invalid managed plugin names');
  const seen = new Set();
  for (const plugin of plugins) {
    if (
      !names.includes(plugin.name) ||
      seen.has(plugin.name) ||
      !['ENABLED', 'DISABLED', 'REMOVED'].includes(plugin.desiredState) ||
      !['required', 'default', 'optional'].includes(plugin.installationMode)
    )
      throw new Error('Invalid managed plugin intent');
    seen.add(plugin.name);
    if (plugin.desiredState === 'ENABLED' && !policy.newSessionRequired) {
      if (
        !/^sha256:[a-f0-9]{64}$/.test(plugin.digest) ||
        plugin.path !==
          '/run/auriga/catalog-plugins/' + plugin.digest.slice(7) + '.tgz'
      )
        throw new Error('Invalid managed payload path');
    }
  }
  return { names, plugins };
}

// Runs before DSH loads any user profile. Only the public CLI changes its files.
// Removal is also used for disabled bundles; original signed bytes remain node-owned.
export async function preparePlugins(
  policy,
  run,
  installed,
  read = readFileSync,
) {
  const { names, plugins } = pluginPlan(policy);
  if (policy.allowPersonalPlugins === false) {
    for (const name of installed) {
      if (!packageName.test(name))
        throw new Error('Invalid installed plugin name');
      if (
        name !== 'auriga-dsh' &&
        !name.startsWith('@deepseek-ai/') &&
        !names.includes(name)
      )
        await run(['remove', name, '--config.ignore-scripts=true']);
    }
  }
  for (const name of names)
    if (installed.has(name))
      await run(['remove', name, '--config.ignore-scripts=true']);
  if (policy.newSessionRequired) return;
  for (const plugin of plugins) {
    if (plugin.desiredState !== 'ENABLED') continue;
    const bytes = read(plugin.path);
    if (
      'sha256:' + createHash('sha256').update(bytes).digest('hex') !==
      plugin.digest
    )
      throw new Error('Managed payload changed');
    await run(['add', plugin.path, '--ignore-scripts']);
  }
}

// Public service methods are guarded before the native page/tool can mutate them.
export function governPlugins(ctx, manager, policy) {
  const { names, plugins } = pluginPlan(policy);
  const rules = new Map(plugins.map((plugin) => [plugin.name, plugin]));
  function allowed(name, action) {
    if (action === 'install' && names.includes(name))
      throw new Error(
        '请通过资源目录更新受管插件。 Use the resource catalog to update managed plugins.',
      );
    const rule = rules.get(name);
    if (names.includes(name)) {
      if (
        action === 'install' ||
        (action === 'enable' && rule?.desiredState !== 'ENABLED') ||
        action === 'disable' ||
        action === 'remove'
      )
        throw new Error(
          '受管插件请通过门户或插件市场的资源目录操作。 Use the resource catalog in the portal or plugin marketplace for managed plugins.',
        );
    } else if (
      policy.allowPersonalPlugins === false &&
      (action === 'install' || action === 'enable')
    )
      throw new Error(
        '管理员已禁止自行安装插件。 Personal plugin installation is disabled.',
      );
  }
  for (const method of [
    'installBundle',
    'setBundleEnabled',
    'removeBundle',
    'setPluginEnabled',
  ]) {
    const original = manager?.[method];
    if (typeof original !== 'function')
      throw new Error('Public plugin governance API unavailable');
    const guarded = async function (target, value, ...args) {
      if (method === 'installBundle') {
        // The native manager accepts arbitrary locators, whose package name is unknown until install.
        // All managed updates use the verified node CLI, so native installs never replace managed bytes.
        if (policy.allowPersonalPlugins === false)
          throw new Error(
            '管理员已禁止自行安装插件。 Personal plugin installation is disabled.',
          );
        if (names.length) {
          if (typeof manager.inspect !== 'function')
            throw new Error('Public plugin inspection API unavailable');
          const inspected = await manager.inspect(target);
          if (
            inspected?.status !== 'accepted' ||
            inspected.kind !== 'registry' ||
            typeof inspected.name !== 'string'
          )
            throw new Error(
              '请通过资源目录安装本地或远程归档。 Use the resource catalog for archives.',
            );
          allowed(inspected.name, 'install');
        }
      } else if (method === 'setPluginEnabled') {
        const bundles = await manager.listBundles();
        const bundle = bundles.find((row) =>
          row.rows?.some((entry) => entry.entryId === target),
        );
        if (!bundle) {
          if (policy.allowPersonalPlugins === false && value)
            throw new Error('Unmanaged plugin enablement denied');
        } else allowed(bundle.name, value ? 'enable' : 'disable');
      } else
        allowed(
          target,
          method === 'removeBundle' ? 'remove' : value ? 'enable' : 'disable',
        );
      return original.call(this, target, value, ...args);
    };
    manager[method] = guarded;
    ctx.effect(
      () => () => {
        if (manager[method] === guarded) manager[method] = original;
      },
      'auriga-plugin-policy-' + method,
    );
  }
}
