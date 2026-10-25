import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, response } from './shell-host.mjs';
const localBoot = (connected) => ({
  mode: 'connect',
  protocol: 1,
  pluginVersion: '0.0.0',
  status: connected ? 'connected' : 'disconnected',
  connection: {
    connected,
    endpointId: '00000000-0000-4000-8000-000000000001',
    received: 3,
  },
  localPanel: {
    source: 'package',
    version: '0.0.0',
    entry: 'auriga/local-panel.js',
    integrity: 'sha384-YQ==',
  },
  fetchBase: null,
});
test('local connection state drives the real shell entry and endpoint/acceptance settings', async () => {
  for (const connected of [true, false]) {
    const host = surfaceHost(async () => response(200, localBoot(connected)));
    try {
      const main = host.mount('main'),
        settings = host.mount('settings.section');
      await flush();
      const start = [...main.root.querySelectorAll('button')].find(
        (b) => b.textContent === '在会话中使用 Team 工具',
      );
      assert.ok(start);
      assert.equal(start.disabled, !connected);
      assert.match(
        main.root.textContent,
        connected ? /平台已连接/ : /平台连接暂不可用/,
      );
      assert.match(
        settings.root.textContent,
        /本机通信端点00000000-0000-4000-8000-000000000001/,
      );
      assert.match(settings.root.textContent, /已接受的消息3/);
      assert.doesNotMatch(settings.root.textContent, /尚未配置总部连接/);
      main.close();
      settings.close();
      host.dispose();
    } finally {
      host.close();
    }
  }
});
test('connection bootstrap without an explicit validated status cannot claim connected', async () => {
  const value = localBoot(true);
  delete value.connection;
  const host = surfaceHost(async () => response(200, value));
  try {
    const main = host.mount('main');
    await flush();
    assert.equal(main.root.querySelector('auriga-team-panel'), null);
    assert.match(main.root.textContent, /验证未通过/);
    main.close();
    host.dispose();
  } finally {
    host.close();
  }
});
