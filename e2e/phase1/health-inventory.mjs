import { commandJson } from './command-json.mjs';
import { requireThat, StepError } from './run.mjs';

export function verifyInventory(config, data) {
  const expected = config.expectedServices;
  const oneShot = config.oneShotServices ?? [];
  requireThat(
    Array.isArray(expected) &&
      Array.isArray(oneShot) &&
      expected.length > 0 &&
      new Set(expected).size === expected.length &&
      expected.every((name) => /^[a-z][a-z0-9-]{0,63}$/.test(name)) &&
      oneShot.every((name) => expected.includes(name)),
    '健康清单须声明完整且无重复的服务名称',
  );
  requireThat(
    data?.schemaVersion === 1 &&
      data.project === config.project &&
      /^[a-z0-9][a-z0-9_-]{0,63}$/.test(config.project ?? '') &&
      Array.isArray(data.services) &&
      data.services.length > 0,
    '健康快照项目或格式不匹配',
  );
  const stamp = Date.parse(data.observedAt);
  requireThat(
    Number.isFinite(stamp) && Math.abs(Date.now() - stamp) <= 120000,
    '健康快照过期或时间无效',
  );
  const names = new Set();
  for (const row of data.services) {
    requireThat(
      typeof row.name === 'string' &&
        /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(row.name) &&
        !names.has(row.name) &&
        expected.includes(row.service),
      '健康快照存在重复容器或未声明服务',
    );
    names.add(row.name);
    requireThat(
      row.oom === false &&
        Number.isSafeInteger(row.restarts) &&
        row.restarts >= 0,
      `服务${row.service}发生OOM或restart计数无效`,
    );
    if (oneShot.includes(row.service))
      requireThat(
        row.status === 'exited' && row.exitCode === 0,
        `初始化服务${row.service}未成功完成`,
      );
    else {
      requireThat(row.status === 'running', `服务${row.service}未运行`);
      if (row.health !== undefined && row.health !== null)
        requireThat(
          row.health === 'healthy',
          `服务${row.service}健康探针未通过`,
        );
      else
        requireThat(
          row.probe?.status === 'PASS' &&
            ['http', 'tcp', 'agent-online', 'signed-ready'].includes(
              row.probe.kind,
            ),
          `服务${row.service}缺少明确就绪探针`,
        );
    }
  }
  requireThat(
    expected.every((service) =>
      data.services.some((row) => row.service === service),
    ),
    '健康快照缺少已声明服务',
  );
  return {
    complete: true,
    project: data.project,
    observedAt: data.observedAt,
    services: data.services.map((row) => ({
      service: row.service,
      name: row.name,
      status: row.status,
      health: row.health ?? null,
      restarts: row.restarts,
      ...(row.probe
        ? { probe: { kind: row.probe.kind, status: row.probe.status } }
        : {}),
    })),
  };
}

export async function healthInventory(config) {
  let data;
  try {
    data = await commandJson(
      config.command,
      '{"operation":"health-inventory","schemaVersion":1}\n',
      config.timeoutMs ?? 60000,
    );
  } catch {
    throw new StepError('健康清单采集命令失败、超时或返回无效JSON');
  }
  return verifyInventory(config, data);
}
