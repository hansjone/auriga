// Test-process transport isolation only; never changes system networking.
import { readFileSync } from 'node:fs';
import { Socket } from 'node:net';

export const name = 'auriga-offline-isolation';
export function apply() {}

if (process.env.AURIGA_DESKTOP_OFFLINE_FILE) {
  const targets = JSON.parse(process.env.AURIGA_DESKTOP_OFFLINE_TARGETS);
  const offline = () => {
    try {
      return (
        readFileSync(process.env.AURIGA_DESKTOP_OFFLINE_FILE, 'utf8') ===
        'offline'
      );
    } catch {
      return true;
    }
  };
  const refused = () =>
    Object.assign(new Error('isolated platform unavailable'), {
      code: 'ECONNREFUSED',
    });
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    if (
      targets.some(
        (t) => new URL(t).origin === new URL(url.url ?? url).origin,
      ) &&
      offline()
    )
      throw Object.assign(new TypeError('fetch failed'), { cause: refused() });
    return originalFetch(url, init);
  };
  const matches = (host, port) =>
    targets.some((t) => {
      const url = new URL(t);
      return (
        url.hostname === host &&
        Number(
          url.port || (['https:', 'wss:'].includes(url.protocol) ? 443 : 80),
        ) === Number(port)
      );
    });
  const sockets = new Set(),
    connect = Socket.prototype.connect;
  Socket.prototype.connect = function (...args) {
    const options =
      typeof args[0] === 'object'
        ? args[0]
        : {
            port: args[0],
            host: typeof args[1] === 'string' ? args[1] : 'localhost',
          };
    if (!matches(options.host ?? options.hostname ?? 'localhost', options.port))
      return connect.apply(this, args);
    sockets.add(this);
    this.once('close', () => sockets.delete(this));
    if (offline()) {
      queueMicrotask(() => this.destroy(refused()));
      return this;
    }
    return connect.apply(this, args);
  };
  setInterval(() => {
    if (offline()) for (const socket of sockets) socket.destroy(refused());
  }, 200).unref();
}
