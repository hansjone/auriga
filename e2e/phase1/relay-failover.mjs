import { requireThat, StepError } from './run.mjs';

const idOf = (entry) => entry.model_info?.id;
const blocked = (entry) =>
  entry.blocked === true || entry.model_info?.blocked === true;

// Explicitly disrupt one deployment in the named test pool. Provider credentials
// are never read, changed or copied back from the redacted model catalog.
export async function verifyPoolFailover(
  config,
  models,
  api,
  converse,
  evidence = {},
) {
  requireThat(
    config.allowPoolDisruption === true,
    '模型池故障演练须显式allowPoolDisruption',
  );
  const catalog = await api('model/info');
  const entries =
    catalog.data?.filter((entry) => entry.model_name === config.pool) ?? [];
  requireThat(
    entries.length === 2 &&
      models.length === 2 &&
      new Set(models.map((model) => model.upstreamModel)).size === 2 &&
      models.every((model) =>
        entries.some(
          (entry) => entry.litellm_params?.model === model.upstreamModel,
        ),
      ) &&
      entries.every(
        (entry) =>
          /^[A-Za-z0-9_-]{1,128}$/.test(idOf(entry) ?? '') && !blocked(entry),
      ) &&
      idOf(entries[0]) !== idOf(entries[1]),
    '故障演练只允许两个已配置且在线的池部署',
  );
  const [unavailable, healthy] = entries;
  Object.assign(evidence, {
    verified: false,
    blockedDeploymentId: idOf(unavailable),
    survivingDeploymentId: idOf(healthy),
    restored: false,
  });
  const change = (path) =>
    api(path, {
      method: 'POST',
      body: { model_id: idOf(unavailable) },
      discardBody: true,
    });
  let failure;
  try {
    // Register restoration even if the blocking response is lost after commit.
    await change('model/block');
    const current = await api('model/info');
    const row = current.data?.find(
      (entry) => idOf(entry) === idOf(unavailable),
    );
    requireThat(
      (!row || blocked(row)) &&
        current.data?.some(
          (entry) => idOf(entry) === idOf(healthy) && !blocked(entry),
        ),
      '未确认一个池部署不可用且另一个仍在线',
    );
    for (const side of ['desktop', 'cloud']) {
      const evidence = await converse(side, config.pool);
      requireThat(
        evidence.deploymentId === idOf(healthy),
        '故障期间的原生会话用量未关联到存活部署',
      );
    }
  } catch (error) {
    failure = error;
  }
  try {
    await change('model/unblock');
    const restored = await api('model/info');
    requireThat(
      restored.data?.some(
        (entry) => idOf(entry) === idOf(unavailable) && !blocked(entry),
      ),
      '原池部署尚未恢复在线',
    );
    evidence.restored = true;
  } catch {
    throw new StepError('池故障演练恢复失败，须恢复报告中原部署');
  }
  if (failure) throw failure;
  evidence.verified = true;
  return evidence;
}
