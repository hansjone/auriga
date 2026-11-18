import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

// Resolve reviewed public package roots from the signed, fixed runtime image.
const require = createRequire('/opt/dsh/package.json');
const { boot } = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-app-boot')).href);
const binding = JSON.parse(readFileSync('/run/auriga/pool-binding.json', 'utf8'));
if (!['shared','dedicated'].includes(binding.mode) || process.env.AURIGA_MODE !== binding.mode || binding.physicalInstanceId !== process.env.AURIGA_INSTANCE_ID)
  throw new Error('Shared pool installation mismatch');
for (const key of Object.keys(process.env)) {
  if (/^(AURIGA_OWNER_ID|AURIGA_.*(?:TOKEN|CREDENTIAL).*|DSH_TEAM_.*|DSH_SESSION_ID)$/.test(key))
    throw new Error('Shared process received personal material');
}
const home = '/home/dsh/.host';
mkdirSync(join(home, '.dsh'), { recursive: true });
process.env.HOME = home;
process.env.DSH_HOME = join(home, '.dsh');
const profile = {
  name: 'auriga-shared', dir: '/run/auriga', home: process.env.DSH_HOME, cwd: home,
  installAnchor: '/opt/dsh/package.json', patchPath: '/run/auriga/unused.patch.yml',
  overlays: [], startedBundles: [], telemetryDisabledEnv: '1',
};
// Boot directly from the expanded profile. The installation anchor and profile
// live on read-only mounts, independently of the shared writable session home.
const callbacks = new Set();
const ctx = await boot('auriga-shared', '/run/auriga/pool-profile.yml', [], (root) => {
  root.provide('profileContext', profile);
  root.provide('appReady', { onReady(fn) { callbacks.add(fn); return () => callbacks.delete(fn); } });
});
if (!ctx.aurigaShared || !ctx.webServer || !ctx.aurigaSharedPorts)
  throw new Error('Shared host authority ports unavailable');
for (const fn of callbacks) await fn();
const pkg = JSON.parse(readFileSync('/opt/auriga-dsh/package.json', 'utf8'));
const detach = ctx.webServer.register({
  kind: 'prefix', path: '/_auriga/health',
  handler(req, res) {
    if (req.method !== 'GET' || req.url?.split('?')[0] !== '/_auriga/health') {
      res.writeHead(404); res.end(); return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    res.end(JSON.stringify({ status: 'ok', ready: true, protocol: 1, pluginVersion: pkg.version }));
  },
});
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  detach();
  await ctx.fiber.dispose();
  process.exit(0);
}
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
