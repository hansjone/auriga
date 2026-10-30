import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const here = path.dirname(fileURLToPath(import.meta.url));
const inspect = spawnSync(
  'docker',
  ['context', 'inspect', '--format', '{{json .Endpoints.docker.Host}}'],
  { encoding: 'utf8', windowsHide: true },
);
if (inspect.error || inspect.status !== 0)
  throw new Error('无法检查本机 Docker context');
const endpoint = JSON.parse(inspect.stdout.trim());
if (
  typeof endpoint !== 'string' ||
  !(endpoint.startsWith('unix:///') || endpoint.startsWith('npipe:////./pipe/'))
)
  throw new Error('验收仅支持本机 Docker socket；拒绝远程 context');
mkdirSync(path.join(here, '.verify'), { recursive: true });
const temporary = mkdtempSync(path.join(here, '.verify/runtime-'));
const project = `auriga-relay-test-${randomBytes(8).toString('hex')}`;
const args = [
  '--host',
  endpoint,
  'compose',
  '--project-name',
  project,
  '--file',
  path.join(here, 'test-compose.yml'),
];
const env = {
  ...Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) => !['DOCKER_HOST', 'DOCKER_CONTEXT'].includes(name),
    ),
  ),
  AURIGA_RELAY_TEST_ROOT: temporary,
};
for (const name of ['bootstrap', 'master', 'salt', 'team', 'pg_password']) {
  writeFileSync(
    path.join(temporary, name),
    `${['master', 'salt', 'team'].includes(name) ? 'sk-' : ''}${randomBytes(32).toString('hex')}`,
    // The host directory is private. Only the disposable PostgreSQL fixture
    // needs a cross-uid readable bind file; production uses its uid999 copy.
    { mode: name === 'pg_password' ? 0o644 : 0o600 },
  );
}
let failed = false;
function run(command) {
  const result = spawnSync('docker', [...args, ...command], {
    env,
    cwd: here,
    stdio: 'inherit',
    windowsHide: true,
  });
  if (result.error || result.status !== 0)
    throw new Error('isolated LiteLLM verification command failed');
}
try {
  run([
    'up',
    '-d',
    '--wait',
    '--wait-timeout',
    '240',
    'postgres',
    'fake-upstream',
    'litellm',
  ]);
  run(['run', '--rm', 'litellm-init']);
  run(['run', '--rm', 'checker']);
} catch {
  failed = true;
} finally {
  // The name is generated here, never taken from user/environment input.
  const cleanup = spawnSync(
    'docker',
    [...args, 'down', '--volumes', '--remove-orphans'],
    { env, cwd: here, stdio: 'inherit', windowsHide: true },
  );
  if (cleanup.error || cleanup.status !== 0) failed = true;
  else if (path.dirname(temporary) === path.join(here, '.verify'))
    rmSync(temporary, { recursive: true });
}
process.exitCode = failed ? 1 : 0;
