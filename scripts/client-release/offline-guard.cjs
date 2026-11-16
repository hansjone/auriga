/* global require */
/* eslint-disable @typescript-eslint/no-require-imports -- Node 22 preloads this CommonJS file before any build module. */
/* All Node build descendants inherit this guard. Native children are local
 * csc/tar/esbuild only; this is not an OS sandbox for hostile source code. */
const fs = require('node:fs');
const reject = () => {
  if (process.env.AURIGA_CLIENT_NETWORK_LOG)
    fs.appendFileSync(
      process.env.AURIGA_CLIENT_NETWORK_LOG,
      'DENIED network\n',
    );
  throw new Error('AURIGA_OFFLINE_NETWORK_DENIED');
};
for (const name of ['http', 'https']) {
  const mod = require('node:' + name);
  mod.request = reject;
  mod.get = reject;
}
const net = require('node:net');
const original = net.Socket.prototype.connect;
net.Socket.prototype.connect = function (...args) {
  // esbuild may use an anonymous local pipe. TCP, including loopback, is denied.
  const option = args[0];
  if (
    (typeof option === 'string' && option.startsWith('\\\\.\\pipe\\')) ||
    (option &&
      typeof option === 'object' &&
      typeof option.path === 'string' &&
      option.path.startsWith('\\\\.\\pipe\\'))
  )
    return original.apply(this, args);
  return reject();
};
require('node:tls').connect = reject;
const dns = require('node:dns');
for (const name of Object.keys(dns))
  if (/^(lookup|resolve|reverse)/.test(name) && typeof dns[name] === 'function')
    dns[name] = reject;
for (const name of Object.keys(dns.promises))
  if (name !== 'Resolver' && typeof dns.promises[name] === 'function')
    dns.promises[name] = reject;
for (const prototype of [
  dns.Resolver.prototype,
  dns.promises.Resolver.prototype,
])
  for (const name of Object.getOwnPropertyNames(prototype))
    if (
      /^(lookup|resolve|reverse)/.test(name) &&
      typeof prototype[name] === 'function'
    )
      prototype[name] = reject;
require('node:dgram').createSocket = reject;
globalThis.fetch = reject;
require('node:module').syncBuiltinESMExports();
