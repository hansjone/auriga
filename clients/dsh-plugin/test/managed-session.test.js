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
  const dispose = installManagedSessionGuard(
    server,
    () => available,
    () => !available,
  );
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
  request('/auriga/shell/status');
  assert.equal(reached, 3);
  request('/auriga/settings/state');
  request('/auriga/settings/view.js');
  assert.equal(reached, 5);
  dispose();
  request('/api/session/prompt');
  assert.equal(reached, 6);
});

for (const language of ['zh-CN', 'en'])
  test(
    'managed guard distinguishes temporary failure and revocation: ' + language,
    (t) => {
      const server = new EventEmitter();
      let available = false,
        revoked = false,
        reached = 0;
      server.on('request', () => reached++);
      t.after(
        installManagedSessionGuard(
          server,
          () => available,
          () => revoked,
        ),
      );
      function request(url) {
        const response = {
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
          { method: 'GET', url, headers: { 'accept-language': language } },
          response,
        );
        return response;
      }
      const temporary =
        language === 'zh-CN'
          ? '连接中断，正在重试'
          : 'Connection interrupted. Retrying.';
      const permanent =
        language === 'zh-CN'
          ? '实例会话已失效，请从门户重新进入'
          : 'Instance session has expired. Re-enter from the portal.';
      for (const url of ['/', '/api/session/prompt']) {
        const res = request(url);
        assert.equal(res.status, 401);
        assert.ok(res.body.includes(temporary));
        assert.ok(!res.body.includes(permanent));
      }
      assert.equal(reached, 0);
      const socket = {
        destroyed: false,
        destroy() {
          this.destroyed = true;
        },
      };
      server.emit('upgrade', { url: '/api/_remote-stream' }, socket);
      assert.equal(socket.destroyed, true);
      revoked = true;
      for (const url of ['/', '/api/session/prompt']) {
        const res = request(url);
        assert.equal(res.status, 401);
        assert.ok(res.body.includes(permanent));
        assert.ok(!res.body.includes(temporary));
      }
      revoked = false;
      available = true;
      request('/api/session/prompt');
      assert.equal(reached, 1);
    },
  );
