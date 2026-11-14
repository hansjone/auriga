// 跨语言契约测试adapter；实现线必须用自己的生产验证器替换此参考adapter。
import { readFileSync } from 'node:fs';
import { evaluate } from './msgnet-v2-oracle.mjs';
import {
  evaluateIssuer,
  evaluateBoundary,
} from './msgnet-v2-boundary-oracle.mjs';
const fixture = process.argv.includes('--stdin')
  ? JSON.parse(readFileSync(0, 'utf8'))
  : JSON.parse(
      readFileSync(new URL('auto-renewal-v2-vectors.json', import.meta.url)),
    );
const results = [];
for (const [group, fn] of [
  ['cases', evaluate],
  ['issuer_cases', evaluateIssuer],
  ['boundary_cases', evaluateBoundary],
])
  for (const v of fixture[group])
    results.push({ id: v.id, group, result: fn(v) });
process.stdout.write(JSON.stringify(results));
