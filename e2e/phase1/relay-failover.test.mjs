import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyPoolFailover } from './relay-failover.mjs';

function fixture({
  fails = false,
  restoreFails = false,
  disabled = false,
} = {}) {
  let off = disabled;
  const calls = [];
  const entries = [
    {
      id: 'a',
      hasCredential: true,
      configuration: {
        name: 'A',
        kind: 'openai-compatible',
        baseUrl: 'https://a.example.com',
        enabled: true,
      },
    },
    {
      id: 'b',
      hasCredential: true,
      configuration: {
        name: 'B',
        kind: 'openai-compatible',
        baseUrl: 'https://b.example.com',
        enabled: true,
      },
    },
  ];
  return {
    calls,
    isOff: () => off,
    api: async (path, options) => {
      calls.push({ path, options });
      if (path.startsWith('pools?'))
        return {
          items: [
            {
              id: 'pool',
              enabled: true,
              routes: [
                { providerId: 'a', model: 'one' },
                { providerId: 'b', model: 'two' },
              ],
            },
          ],
        };
      if (path.startsWith('providers?'))
        return {
          items: entries.map((row) => ({
            ...row,
            configuration: {
              ...row.configuration,
              enabled: row.id === 'a' ? !off : true,
            },
          })),
        };
      assert.equal(path, 'providers/a');
      assert.equal(options.method, 'PUT');
      assert.equal(options.body.baseUrl, entries[0].configuration.baseUrl);
      assert.equal('secret' in options.body, false);
      if (options.body.enabled && restoreFails) throw Error('private-detail');
      off = !options.body.enabled;
      return {};
    },
    converse: async () => {
      if (fails) throw Error('conversation_failed');
      return { requestId: 'fresh', totalTokens: 3 };
    },
  };
}
const config = { pool: 'pool', allowPoolDisruption: true };
const models = [{ upstreamModel: 'one' }, { upstreamModel: 'two' }];
for (const fails of [false, true]) {
  test(`故障演练在会话成功或失败后恢复供应商：${fails}`, async () => {
    const f = fixture({ fails });
    const run = () => verifyPoolFailover(config, models, f.api, f.converse);
    if (fails) await assert.rejects(run());
    else assert.equal((await run()).verified, true);
    assert.equal(f.isOff(), false);
    assert.equal(
      f.calls.filter((row) => row.options?.method === 'PUT').length,
      2,
    );
  });
}
test('单桌面验收保留范围，不冒双端完成', async () => {
  const f = fixture(),
    called = [];
  const result = await verifyPoolFailover(
    config,
    models,
    f.api,
    async (side) => {
      called.push(side);
      return { requestId: 'fresh', totalTokens: 1 };
    },
    {},
    ['desktop'],
  );
  assert.deepEqual(called, ['desktop']);
  assert.deepEqual(result.sides, ['desktop']);
  assert.equal(result.restored, true);
});
test('未授权或原供应商已禁用均不修改配置', async () => {
  const f = fixture({ disabled: true });
  await assert.rejects(verifyPoolFailover({}, models, f.api, f.converse));
  await assert.rejects(verifyPoolFailover(config, models, f.api, f.converse));
  assert.equal(
    f.calls.some((row) => row.options?.method === 'PUT'),
    false,
  );
});
for (const restoreFails of [false, true]) {
  test(`缺用量不可通过，恢复失败保留精确供应商与安全错误：${restoreFails}`, async () => {
    const f = fixture({ restoreFails }),
      evidence = {};
    await assert.rejects(
      verifyPoolFailover(config, models, f.api, async () => ({}), evidence),
      (error) => !error.message.includes('private-detail'),
    );
    assert.equal(evidence.verified, false);
    assert.equal(evidence.restored, !restoreFails);
    assert.equal(evidence.blockedDeploymentId, 'a');
  });
}
