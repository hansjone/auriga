import test from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { createConnectRoutes } from '../../src/local/routes.js';

async function invoke({
  auth = true,
  sameOrigin = true,
  loggedIn = true,
  csrf = true,
  body = { local: '1', enabled: true },
  method = 'POST',
  action = 'receive',
} = {}) {
  const request = Object.assign(
    Readable.from([Buffer.from(JSON.stringify(body))]),
    {
      method,
      url: '/auriga/local-login/messaging/' + action,
      headers: { 'content-type': 'application/json' },
    },
  );
  let status, output;
  const calls = [];
  const handler = createConnectRoutes({
    admission: {
      nativeAuth: { requestRejection: () => (auth ? undefined : 401) },
      sameOrigin: () => sameOrigin,
    },
    runtime: {
      resolve: () => loggedIn,
      checkCsrf: () => csrf,
      messagingUI(context) {
        assert.equal(context, request);
        return {
          async snapshot() {
            calls.push('snapshot');
            return { sessions: [] };
          },
          async setReceive(input) {
            calls.push(input);
            return input;
          },
        };
      },
    },
  });
  await handler(request, {
    writeHead(code) {
      status = code;
    },
    end(value) {
      output = value && JSON.parse(value);
    },
  });
  return { status, output, calls };
}
test('接收设置沿 native auth、同源、已登录和 CSRF 准入，拒绝时不调用控制面', async () => {
  for (const [options, status] of [
    [{ auth: false }, 401],
    [{ sameOrigin: false }, 403],
    [{ loggedIn: false }, 401],
    [{ csrf: false }, 403],
  ]) {
    const result = await invoke(options);
    assert.equal(result.status, status);
    assert.equal(result.calls.length, 0);
  }
  const result = await invoke();
  assert.equal(result.status, 200);
  assert.deepEqual(result.calls, [{ local: '1', enabled: true }]);
});
test('接收设置拒绝自报身份和无效 local/布尔值；snapshot 为只读入口', async () => {
  for (const body of [
    { local: '1', enabled: true, tenant: 'forged' },
    { local: '0', enabled: true },
    { local: '1', enabled: 'true' },
    [],
  ]) {
    const result = await invoke({ body });
    assert.equal(result.status, 400);
    assert.equal(result.calls.length, 0);
  }
  const result = await invoke({
    method: 'GET',
    action: 'snapshot',
    csrf: false,
  });
  assert.equal(result.status, 200);
  assert.deepEqual(result.calls, ['snapshot']);
});
