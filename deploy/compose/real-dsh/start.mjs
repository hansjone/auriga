import {
  existsSync,
  mkdirSync,
  readFileSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawn } from 'node:child_process';

for (const key of [
  'AURIGA_INSTANCE_ID',
  'AURIGA_OWNER_ID',
  'AURIGA_PLATFORM_URL',
  'AURIGA_IDENTITY_KEYS_FILE',
  'AURIGA_INSTANCE_TOKEN_FILE',
  'DSH_HOME',
]) {
  if (!process.env[key]) throw new Error(`Missing ${key}`);
}
if (process.env.AURIGA_MODE !== 'managed')
  throw new Error('Managed mode required');
const PATCH = '/run/auriga/cordis.patch.yml';
if (!existsSync(PATCH)) throw new Error(`Missing ${PATCH}`);
const workspace = '/home/dsh/workspace';
const cache = '/home/dsh/.cache';
const temp = '/home/dsh/.tmp';
for (const dir of [workspace, cache, temp, process.env.DSH_HOME]) {
  mkdirSync(dir, { recursive: true });
}
// DSH resolves plugins from manifest dependency closures, not arbitrary package
// links under the installation. Declare only this fixture's prebuilt plugin;
// this materializes metadata and a link, never installs runtime dependencies.
const profile = join(process.env.DSH_HOME, 'profiles', 'web');
const modules = join(profile, 'node_modules');
mkdirSync(modules, { recursive: true });
const plugin = JSON.parse(readFileSync('/opt/auriga-dsh/package.json', 'utf8'));
writeFileSync(
  join(profile, 'package.json'),
  JSON.stringify(
    {
      name: 'auriga-real-dsh-profile',
      private: true,
      dependencies: { 'auriga-dsh': plugin.version },
      dsh: {
        profile: {
          bundles: ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app'],
        },
      },
    },
    null,
    2,
  ) + '\n',
);
const link = join(modules, 'auriga-dsh');
if (!existsSync(link)) symlinkSync('/opt/auriga-dsh', link, 'dir');
// No dependency installation, downloads, host config copying or token arguments.
const child = spawn(
  process.execPath,
  [
    '/opt/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js',
    'web',
    // Rendered by the node agent: plugin row, listener (DSH refuses
    // `--host 0.0.0.0`, the webserver row binds instead), trustedHosts, MCP.
    '--patch',
    PATCH,
    // Launcher options must precede app arguments: --no-open starts pass-through.
    '--no-open',
  ],
  {
    cwd: workspace,
    stdio: 'inherit',
    env: {
      ...process.env,
      HOME: '/home/dsh',
      XDG_CACHE_HOME: cache,
      TMP: temp,
      TEMP: temp,
      TMPDIR: temp,
      DSH_WORKSPACE_ROOT: workspace,
      AURIGA_WORKSPACE_ROOT: workspace,
      XDG_CONFIG_HOME: join(process.env.DSH_HOME, 'config'),
    },
  },
);
for (const signal of ['SIGTERM', 'SIGINT'])
  process.on(signal, () => child.kill(signal));
child.on('error', () => {
  process.exitCode = 1;
});
child.on('exit', (code, signal) => {
  process.exitCode = code ?? (signal ? 1 : 0);
});
