/** 现场系统工具基线：Windows 更新后可重新登记，构建时与清单或基线之一相符即可。 */
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { release as osRelease } from 'node:os';
import { join, resolve } from 'node:path';
import { CLIENT_TARGET } from './platform.mjs';
import { canonical } from '../../clients/installer/runtime/release.mjs';

export const BASELINE_FILENAME = 'client-tools-baseline.json';
export const TOOLS_BASELINE_MISMATCH =
  'Windows 更新导致系统工具变化，请重新 deps baseline --client';

export function toolsBaselineOption() {
  return process.env.AURIGA_CLIENT_TOOLS_BASELINE || null;
}

export function baselinePath(cacheDir) {
  return join(resolve(cacheDir), BASELINE_FILENAME);
}

/** 把系统工具摘要登记到可写缓存根目录（tools 由调用方传入，通常为 systemTools()）。 */
export function registerBaseline(cacheDir, tools) {
  const dir = resolve(cacheDir);
  mkdirSync(dir, { recursive: true });
  if (!Array.isArray(tools) || !tools.length)
    throw new Error('系统工具清单为空，无法登记基线');
  for (const row of tools) {
    if (
      typeof row?.path !== 'string' ||
      typeof row?.sha256 !== 'string' ||
      !/^[a-f0-9]{64}$/.test(row.sha256)
    )
      throw new Error('系统工具基线条目无效');
  }
  const baseline = {
    schemaVersion: 1,
    platform: CLIENT_TARGET,
    windowsVersion: osRelease(),
    registeredAt: new Date().toISOString(),
    tools,
  };
  writeFileSync(baselinePath(dir), JSON.stringify(baseline, null, 2) + '\n', {
    encoding: 'utf8',
  });
  return baseline;
}

export function readBaseline(cacheDir) {
  const file = baselinePath(cacheDir);
  if (!existsSync(file)) return null;
  const baseline = JSON.parse(readFileSync(file, 'utf8'));
  if (
    baseline?.schemaVersion !== 1 ||
    baseline.platform !== CLIENT_TARGET ||
    !Array.isArray(baseline.tools) ||
    !baseline.tools.length
  )
    throw new Error(`系统工具基线无效：${file}`);
  return baseline;
}

/** 比对两组工具摘要（数组或基线对象的 tools 字段）。 */
export function matchTools(actual, expected) {
  const left = Array.isArray(actual) ? actual : actual?.tools;
  const right = Array.isArray(expected) ? expected : expected?.tools;
  if (!Array.isArray(left) || !Array.isArray(right)) return false;
  return canonical(left) === canonical(right);
}

/**
 * 当前工具须与发行清单或现场基线之一相符；都不符则抛出固定文案。
 * 基线只覆盖系统工具，调用方不得用它跳过非工具输入摘要。
 */
export function assertToolsAllowed(current, manifestTools, baseline) {
  if (matchTools(current, manifestTools)) return { source: 'manifest' };
  if (baseline && matchTools(current, baseline))
    return { source: 'baseline', baseline };
  throw new Error(TOOLS_BASELINE_MISMATCH);
}
