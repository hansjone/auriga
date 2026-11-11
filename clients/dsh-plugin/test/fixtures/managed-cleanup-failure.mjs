import fs from 'node:fs';
import net from 'node:net';
import { basename } from 'node:path';
import { syncBuiltinESMExports } from 'node:module';
import { after } from 'node:test';

const listeners = new Map();
const listen = net.Server.prototype.listen;
net.Server.prototype.listen = function (...args) {
  listeners.set(this, new Error('fixture listener created').stack);
  return Reflect.apply(listen, this, args);
};

const remove = fs.promises.rm;
let injected = false;
const active = () =>
  [...listeners]
    .filter(([server]) => server.listening)
    .map(([server, stack]) => ({ address: server.address(), stack }));
fs.promises.rm = async function (path, ...args) {
  if (!injected && basename(String(path)).startsWith('auriga-degraded-')) {
    injected = true;
    console.log('managed_cleanup_before_remove ' + JSON.stringify(active()));
    throw Object.assign(new Error('injected directory cleanup failure'), {
      code: 'ENOTEMPTY',
    });
  }
  return remove(path, ...args);
};
syncBuiltinESMExports();

after(() => {
  if (injected)
    console.log('managed_cleanup_after_hooks ' + JSON.stringify(active()));
});
