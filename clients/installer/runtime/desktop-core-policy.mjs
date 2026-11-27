/** Keep the Desktop's required bundle read-only in DSH's public management API. */
export const inject = ['pluginManager'];

export function apply(ctx, { packageName, moduleUrl }) {
  const manager = ctx.pluginManager;
  const protect = (item) => ({
    ...item,
    readOnlyReason: 'management-required',
  });
  const refuse = (target, stage, enabled) => ({
    application: 'failed',
    changed: false,
    error: { code: 'management-required' },
    target,
    stage,
    ...(enabled === undefined ? {} : { enabled }),
  });
  const methods = {
    listBundles:
      (original) =>
      async (...args) =>
        (await original(...args)).map((item) =>
          item.name === packageName
            ? protect({ ...item, enabled: true, removable: false })
            : item,
        ),
    listPlugins:
      (original) =>
      async (...args) =>
        (await original(...args)).map((item) =>
          item.moduleName === moduleUrl ? protect(item) : item,
        ),
    setBundleEnabled: (original) => async (name, enabled) =>
      name === packageName
        ? refuse(name, 'enable', enabled)
        : original(name, enabled),
    setPluginEnabled: (original) => async (id, enabled) => {
      const row = (await manager.listPlugins()).find(
        (item) => item.entryId === id,
      );
      return row?.moduleName === moduleUrl
        ? refuse(id, 'enable', enabled)
        : original(id, enabled);
    },
    removeBundle: (original) => async (name) =>
      name === packageName ? refuse(name, 'remove') : original(name),
  };
  ctx.effect(() => {
    const previous = Object.entries(methods).map(([key, wrap]) => {
      const own = Object.getOwnPropertyDescriptor(manager, key);
      const replacement = wrap(manager[key].bind(manager));
      Object.defineProperty(manager, key, {
        configurable: true,
        writable: true,
        value: replacement,
      });
      return { key, own, replacement };
    });
    return () => {
      for (const { key, own, replacement } of previous) {
        if (manager[key] !== replacement) continue;
        if (own) Object.defineProperty(manager, key, own);
        else delete manager[key];
      }
    };
  }, 'Auriga desktop core protection');
}
