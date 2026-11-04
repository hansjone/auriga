import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { installManagedSessionGuard } from '../src/managed/session.js';

test('R7 expired managed sessions block business and upgrades, keeping health and identity diagnostics', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });
  const server = new EventEmitter();
  let available = true,
    reached = 0;
  server.on('request', () => reached++);
  const dispose = installManagedSessionGuard(server, () => available);
  t.after(dispose);
  const request = (url) => {
    const res = {
      writeHead(status, headers) {
        this.status = status;
        this.headers = headers;
      },
      end(body) {
        this.body = body;
      },
    };
    server.emit(
      'request',
      { method: 'GET', url, headers: { 'accept-language': 'zh-CN' } },
      res,
    );
    return res;
  };
  request('/api/settings/describe');
  assert.equal(reached, 1);
  const socket = new EventEmitter();
  socket.destroy = () => {
    socket.destroyed = true;
    socket.emit('close');
  };
  server.emit('upgrade', { url: '/api/_remote-stream' }, socket);
  available = false;
  t.mock.timers.tick(1000);
  assert.equal(socket.destroyed, true);
  const rejected = request('/api/session/prompt');
  assert.equal(rejected.status, 401);
  assert.equal(JSON.parse(rejected.body).code, 'common.unauthenticated');
  const index = request('/');
  assert.equal(index.status, 401);
  assert.match(index.body, /实例会话已失效，请从门户重新进入/);
  assert.doesNotMatch(index.body, /未登录/);
  assert.equal(reached, 1);
  request('/_auriga/health');
  request('/auriga/local-login/status');
  assert.equal(reached, 3);
  request('/auriga/settings/state');
  request('/auriga/settings/view.js');
  assert.equal(reached, 5);
  dispose();
  request('/api/session/prompt');
  assert.equal(reached, 6);
});
