/** @typedef {import('node:http').Server['emit']} Emit */
/** @type {WeakMap<Emit, Emit>} */
const removed = new WeakMap();

/** @param {Emit} emit */
function current(emit) {
  for (let previous; (previous = removed.get(emit));) emit = previous;
  return emit;
}

/**
 * Each guard owns one layer. A replaced entry can be disposed even if a
 * newer guard still sits above it on the same server.
 * @param {import('node:http').Server} server
 * @param {(next: (event: string|symbol, ...args: any[]) => boolean, event: string|symbol, ...args: any[]) => boolean} guard
 */
export function installServerEmitGuard(server, guard) {
  const previous = server.emit;
  /** @param {string|symbol} event @param {any[]} args */
  const next = (event, ...args) =>
    Reflect.apply(current(previous), server, [event, ...args]);
  /** @param {string|symbol} event @param {any[]} args */
  const guarded = (event, ...args) =>
    removed.has(guarded) ? next(event, ...args) : guard(next, event, ...args);
  server.emit = guarded;
  return () => {
    removed.set(guarded, previous);
    if (server.emit === guarded) server.emit = current(previous);
  };
}
