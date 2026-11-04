import { requireThat, StepError } from './run.mjs';
import { allPages } from './relay-backend.mjs';

export async function verifyPoolFailover(
  config,
  models,
  api,
  converse,
  evidence = {},
  sides = ['desktop', 'cloud'],
) {
  requireThat(
    config.allowPoolDisruption === true &&
      sides.length > 0 &&
      new Set(sides).size === sides.length &&
      sides.every((side) => ['desktop', 'cloud'].includes(side)),
    '故障演练范围未授权',
  );
  const pools = await allPages(api, 'pools');
  const providers = await allPages(api, 'providers');
  const pool = pools.find((row) => row.id === config.pool);
  requireThat(
    pool?.enabled &&
      pool.routes?.length === 2 &&
      models.length === 2 &&
      new Set(pool.routes.map((row) => row.providerId)).size === 2 &&
      models.every((model) =>
        pool.routes.some((route) => route.model === model.upstreamModel),
      ),
    '故障演练仅支持已配置的双供应商池',
  );
  const entries = pool.routes.map((route) =>
    providers.find((row) => row.id === route.providerId),
  );
  requireThat(
    entries.every((row) => row?.hasCredential && row.configuration?.enabled),
    '池中供应商未全部在线',
  );
  const [unavailable, healthy] = entries;
  const disabled = { ...unavailable.configuration, enabled: false };
  Object.assign(evidence, {
    verified: false,
    blockedDeploymentId: unavailable.id,
    survivingDeploymentId: healthy.id,
    restored: false,
    sides: [...sides],
  });
  const change = (body) =>
    api('providers/' + encodeURIComponent(unavailable.id), {
      method: 'PUT',
      body,
    });
  let failure;
  try {
    await change(disabled);
    const current = await allPages(api, 'providers');
    requireThat(
      current.some(
        (row) => row.id === unavailable.id && !row.configuration.enabled,
      ) &&
        current.some(
          (row) => row.id === healthy.id && row.configuration.enabled,
        ),
      '未确认故障范围',
    );
    for (const side of sides) {
      const proof = await converse(side, config.pool);
      requireThat(
        proof?.totalTokens > 0 && proof.requestId,
        '故障期间缺少新会话及用量证据',
      );
    }
  } catch (error) {
    failure = error;
  }
  try {
    const current = (await allPages(api, 'providers')).find(
      (row) => row.id === unavailable.id,
    );
    requireThat(
      JSON.stringify(current?.configuration) === JSON.stringify(disabled) ||
        JSON.stringify(current?.configuration) ===
          JSON.stringify(unavailable.configuration),
      '供应商已被其他管理员修改，拒绝覆盖',
    );
    await change(unavailable.configuration);
    const restored = await allPages(api, 'providers');
    requireThat(
      restored.some(
        (row) =>
          row.id === unavailable.id &&
          row.configuration.enabled &&
          row.hasCredential,
      ),
      '供应商未恢复',
    );
    evidence.restored = true;
  } catch {
    throw new StepError('池故障演练恢复失败，须检查报告中的供应商');
  }
  if (failure) throw failure;
  evidence.verified = true;
  return evidence;
}
