import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyPoolFailover } from './relay-failover.mjs';

for (const fails of [false, true]) {
  test(`pool outage drill uses only configured deployments and restores its block: ${fails}`, async () => {
    let blocked = false;
    const calls = [];
    const entries = [
      {
        model_name: 'pool',
        model_info: { id: 'deployment-a' },
        litellm_params: { model: 'openai/a' },
      },
      {
        model_name: 'pool',
        model_info: { id: 'deployment-b' },
        litellm_params: { model: 'openai/b' },
      },
    ];
    const api = async (path, options) => {
      calls.push({ path, options });
      if (path === 'model/info')
        return { data: blocked ? [entries[1]] : entries };
      assert.equal(options.discardBody, true);
      assert.deepEqual(options.body, { model_id: 'deployment-a' });
      if (path === 'model/block') blocked = true;
      else if (path === 'model/unblock') blocked = false;
      else assert.fail('unexpected API');
      return null;
    };
    const run = () =>
      verifyPoolFailover(
        { pool: 'pool', allowPoolDisruption: true },
        [{ upstreamModel: 'openai/a' }, { upstreamModel: 'openai/b' }],
        api,
        async (side, model) => {
          assert.ok(['cloud', 'desktop'].includes(side));
          assert.equal(model, 'pool');
          if (fails) throw Error('conversation_failed');
          return { deploymentId: 'deployment-b' };
        },
      );
    if (fails) await assert.rejects(run());
    else assert.equal((await run()).verified, true);
    assert.equal(blocked, false);
    assert.equal(
      calls.filter((call) => call.path !== 'model/info').at(-1).path,
      'model/unblock',
    );
  });
}

test('pool outage drill refuses missing approval, an already blocked deployment, or an unverified fallback', async () => {
  const entries = [
    {
      model_name: 'pool',
      model_info: { id: 'a', blocked: true },
      litellm_params: { model: 'openai/a' },
    },
    {
      model_name: 'pool',
      model_info: { id: 'b' },
      litellm_params: { model: 'openai/b' },
    },
  ];
  let mutations = 0;
  const api = async (path) => {
    if (path !== 'model/info') mutations++;
    return { data: entries };
  };
  await assert.rejects(
    verifyPoolFailover({ pool: 'pool' }, [], api, async () => {}),
  );
  await assert.rejects(
    verifyPoolFailover(
      { pool: 'pool', allowPoolDisruption: true },
      [{ upstreamModel: 'openai/a' }, { upstreamModel: 'openai/b' }],
      api,
      async () => {},
    ),
  );
  assert.equal(mutations, 0);
});

for (const restoreFails of [false, true]) {
  test(`unverified fallback cannot pass; recovery evidence retains the exact deployment: ${restoreFails}`, async () => {
    let unavailable = false;
    const evidence = {};
    const entries = [
      {
        model_name: 'pool',
        model_info: { id: 'a' },
        litellm_params: { model: 'openai/a' },
      },
      {
        model_name: 'pool',
        model_info: { id: 'b' },
        litellm_params: { model: 'openai/b' },
      },
    ];
    const api = async (path) => {
      if (path === 'model/info')
        return { data: unavailable ? [entries[1]] : entries };
      if (path === 'model/block') unavailable = true;
      if (path === 'model/unblock') {
        if (restoreFails) throw Error('private-manager-detail');
        unavailable = false;
      }
    };
    await assert.rejects(
      verifyPoolFailover(
        { pool: 'pool', allowPoolDisruption: true },
        [{ upstreamModel: 'openai/a' }, { upstreamModel: 'openai/b' }],
        api,
        async () => ({ deploymentId: 'unrelated' }),
        evidence,
      ),
      (error) => !error.message.includes('private-manager-detail'),
    );
    assert.equal(evidence.verified, false);
    assert.equal(evidence.restored, !restoreFails);
    assert.equal(evidence.blockedDeploymentId, 'a');
  });
}
