// @ts-nocheck
/** Let DSH's native UI/RPC use the request already admitted by the managed door. */
export function installHostAuthentication(connection, door) {
  if (!connection) return () => {};
  const restores = [];
  for (const [name, accepted] of [
    ['requestRejection', undefined],
    ['authorizeIndex', true],
  ]) {
    if (typeof connection[name] !== 'function') continue;
    const original = connection[name];
    const wrapped = function (req, ...args) {
      if (door.isAuthenticated(req)) return accepted;
      return original.call(this, req, ...args);
    };
    connection[name] = wrapped;
    restores.push(() => {
      if (connection[name] === wrapped) connection[name] = original;
    });
  }
  return () => restores.reverse().forEach((restore) => restore());
}
