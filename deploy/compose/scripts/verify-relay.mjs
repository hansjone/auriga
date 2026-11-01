// Local maintained-core acceptance; requires an isolated PostgreSQL fixture.
// This is not a deployed DSH/public-domain or NAS acceptance test.
import { spawnSync } from 'node:child_process';
if (!process.env.AURIGA_AI_TEST_PG_DSN) {
  throw new Error(
    'Set AURIGA_AI_TEST_PG_DSN to an isolated test database; see docs/architecture/relay-bifrost.zh.md',
  );
}
const result = spawnSync(
  'go',
  [
    'test',
    '-count=1',
    './internal/bifrostbackend',
    './internal/bifrost',
    './internal/modelgate',
    './internal/budget',
    './internal/store',
  ],
  {
    cwd: new URL('../../../infra/ai-gateway/', import.meta.url),
    stdio: 'inherit',
    env: { ...process.env, CI: 'true' },
  },
);
if (result.error) throw result.error;
process.exit(result.status ?? 1);
