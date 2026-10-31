// Test-process transport isolation only; never changes system networking.
import { readFileSync, writeFileSync } from 'node:fs';
import { Socket } from 'node:net';

export const name = 'auriga-offline-isolation';
export function apply(_ctx, options) {
  if (!options?.file) return;
  writeFileSync(options.file + '.active', String(process.pid));
  const targets = options.targets;
  const diagnosis = {
    node: process.version,
    proxyVariables: Object.keys(process.env).filter((name) =>
      /^(https?_proxy|no_proxy|node_use_env_proxy)$/i.test(name),
    ),
    dispatcher:
      globalThis[Symbol.for('undici.globalDispatcher.1')]?.constructor?.name ??
      'default',
    state: 'unknown',
    passed: 0,
    blocked: 0,
    failures: 0,
    proxyConnections: 0,
    targetConnections: 0,
    responses: [],
  };
  const record = () =>
    writeFileSync(options.file + '.diagnosis.json', JSON.stringify(diagnosis));
  const offline = () => {
    try {
      const state = readFileSync(options.file, 'utf8');
      if (state !== diagnosis.state) {
        diagnosis.state = state;
        record();
      }
      return state === 'offline';
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
    const target = targets.some(
      (t) => new URL(t).origin === new URL(url.url ?? url).origin,
    );
    if (target && offline()) {
      diagnosis.blocked++;
      record();
      throw Object.assign(new TypeError('fetch failed'), { cause: refused() });
    }
    if (target) {
      diagnosis.passed++;
      record();
    }
    try {
      const response = await originalFetch(url, init);
      if (target) {
        const path = new URL(url.url ?? url).pathname;
        const operation =
          path === '/oauth2/token'
            ? init?.body instanceof URLSearchParams && init.body.has('resource')
              ? 'ai_token'
              : 'device_token'
            : path === '/ai/team/node/register'
              ? 'team_register'
              : path.startsWith('/api/iam/v1/network/')
                ? 'device_network'
                : path.startsWith('/api/iam/v1/login/')
                  ? 'login'
                  : 'other';
        diagnosis.responses.push({ operation, status: response.status });
        diagnosis.responses = diagnosis.responses.slice(-40);
        record();
      }
      return response;
    } catch (error) {
      if (target) {
        diagnosis.failures++;
        record();
      }
      throw error;
    }
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
    const options = Array.isArray(args[0])
      ? args[0][0]
      : typeof args[0] === 'object'
        ? args[0]
        : {
            port: args[0],
            host: typeof args[1] === 'string' ? args[1] : 'localhost',
          };
    const host = options.host ?? options.hostname ?? 'localhost';
    const proxy = Object.entries(process.env).some(([name, value]) => {
      if (!/^https?_proxy$/i.test(name) || !value) return false;
      try {
        const url = new URL(value);
        return (
          url.hostname === host &&
          Number(url.port || 80) === Number(options.port)
        );
      } catch {
        return false;
      }
    });
    if (proxy) {
      diagnosis.proxyConnections++;
      record();
    }
    if (!matches(host, options.port)) return connect.apply(this, args);
    diagnosis.targetConnections++;
    record();
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
