#!/usr/bin/env node
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { BrowserSteps } from './browser.mjs';
import { nativeModels } from './native-models.mjs';
import { parseArgs, traceState, requireThat, StepError } from './run.mjs';
import { validateAccess } from './checks.mjs';

// This narrowly scoped drill never claims full phase ⑥ or starts a cloud DSH.
export async function runDesktopDrill(options, config) {
  const report = {
    schemaVersion: 1,
    scope: 'desktop-pool-outage',
    completePhase6: false,
    status: 'failed',
    startedAt: new Date().toISOString(),
  };
  requireThat(
    options.desktopDsh &&
      config.desktop?.installedApp === true &&
      config.relay?.allowModelCalls === true &&
      config.relay.upstreamMode === 'live' &&
      config.modelNative?.allowPoolDisruption === true,
    '桌面池演练须安装版隔离profile、真实模型调用和显式停用授权',
  );
  const adapter = new BrowserSteps(options, config);
  adapter.access = validateAccess(config.access);
  try {
    await adapter.desktop(traceState());
    await nativeModels(adapter, traceState(), report, {
      sides: ['desktop'],
      faultOnly: true,
    });
    requireThat(
      report.nativeModels.failover.verified &&
        report.nativeModels.failover.restored,
      '桌面池演练缺少恢复证据',
    );
    report.status = 'passed';
  } catch (error) {
    report.failure =
      error instanceof StepError
        ? error.message
        : '原生桌面演练失败，私密原始异常未写入报告';
  } finally {
    try {
      await adapter.close();
    } catch {
      report.status = 'failed';
      report.cleanup = 'failed';
    }
    report.finishedAt = new Date().toISOString();
  }
  return report;
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log(
      'node e2e/phase1/desktop-models.mjs --base-url <门户URL> --admin-credentials <JSON文件> --desktop-dsh <安装版隔离profile JSON> --output <报告JSON>',
    );
    return;
  }
  const config = JSON.parse(
    await readFile(resolve(options.credentialsFile), 'utf8'),
  );
  const report = await runDesktopDrill(options, config);
  await mkdir(dirname(resolve(options.output)), { recursive: true });
  await writeFile(
    resolve(options.output),
    JSON.stringify(report, null, 2) + '\n',
  );
  console.log(`桌面池停用演练：${report.status}；完整第⑥项：否`);
  process.exitCode = report.status === 'passed' ? 0 : 1;
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
)
  main().catch(() => {
    console.error('桌面池演练配置或执行失败');
    process.exitCode = 1;
  });
