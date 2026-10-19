import { chmod, mkdir, mkdtemp, open, readFile, readdir, rmdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { networkInterfaces } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';
import { HERE, checkManifest, hashFile, json, options, redact, run, within, writeJson } from './lib.mjs';

const args = options(process.argv.slice(2), ['build-result', 'result', 'network']);
const network = args.network ?? 'online';
if (!['online', 'linux-unshare', 'windows-firewall'].includes(network)) throw new Error('Unknown network mode');
// Host network/firewall changes must never run on a developer workstation.
if (network !== 'online' && (process.env.GITHUB_ACTIONS !== 'true' || process.env.S4_EPHEMERAL_CI !== 'github-hosted')) throw new Error('Offline mode is restricted to GitHub-hosted ephemeral CI');
const resultPath = within(HERE, resolve(args.result ?? join(HERE, 'out', `smoke-${network}.json`)));
const result = { status: 'failed', network, isolationScope: network === 'windows-firewall' ? 'bundled-node-outbound-only' : network === 'linux-unshare' ? 'network-namespace' : 'none', telemetryDisabled: true, defaultProfile: 'web', frontendBrowserExecuted: false };
let child, firewallName, root, entries, temporary;
let log = '', processExit;
try {
  if (!args['build-result']) throw new Error('--build-result is required');
  const buildPath = resolve(args['build-result']);
  const build = await json(buildPath);
  if (build.status !== 'passed') throw new Error('Build did not pass');
  const expectedTarget = `${process.platform === 'win32' ? 'win' : process.platform}-${process.arch}`;
  if (build.target !== expectedTarget) throw new Error(`Cannot execute ${build.target} on ${expectedTarget}`);
  const archive = within(HERE, join(resolve(buildPath, '..'), build.archive));
  if (`sha256:${await hashFile(archive)}` !== build.digest) throw new Error('Archive digest mismatch');
  const work = join(HERE, '.work'); await mkdir(work, { recursive: true });
  temporary = await mkdtemp(join(work, 'smoke-'));
  console.error('Extracting verified runtime archive');
  await run('tar', ['-xzf', archive, '-C', temporary]);
  root = within(temporary, join(temporary, build.artifactId));
  const manifest = await checkManifest(root);
  console.error('Manifest verified; setting extracted artifact read-only');
  result.artifactId = manifest.artifactId;
  result.archiveDigestVerified = true;
  entries = [...manifest.files, { path: 'manifest.json', type: 'file', mode: '0644' }];
  // Only change attributes of this freshly extracted artifact, never shared/global files.
  for (const entry of [...entries].reverse()) {
    if (entry.type !== 'symlink') await chmod(join(root, entry.path), entry.mode === '0755' ? 0o555 : 0o444);
  }
  await chmod(root, 0o555);
  let fileWriteDenied = false;
  try { const fd = await open(join(root, 'node/LICENSE'), 'r+'); await fd.close(); }
  catch (error) { if (!['EACCES', 'EPERM'].includes(error.code)) throw error; fileWriteDenied = true; }
  let directoryCreateDenied = false;
  try { await mkdir(join(root, '.s4-write-probe')); await rmdir(join(root, '.s4-write-probe')); }
  catch (error) { if (!['EACCES', 'EPERM'].includes(error.code)) throw error; directoryCreateDenied = true; }
  result.readOnly = { mechanism: process.platform === 'win32' ? 'file-readonly-attribute' : 'chmod-0555-0444-nonroot', fileWriteDenied, directoryCreateDenied, fullDirectoryEnforcement: fileWriteDenied && directoryCreateDenied };
  if (!fileWriteDenied || (process.platform !== 'win32' && !directoryCreateDenied)) throw new Error('Read-only enforcement probe failed (do not run smoke as root)');
  const home = join(temporary, 'instance', 'dsh-home');
  const cwd = join(temporary, 'instance', 'workspace');
  const temp = join(temporary, 'instance', 'tmp');
  for (const path of [home, cwd, temp]) await mkdir(path, { recursive: true });
  const node = join(root, 'node', process.platform === 'win32' ? 'node.exe' : 'bin/node');
  const cli = join(root, 'app/node_modules/@deepseek-ai/dsh/lib/bin.js');
  const env = { ...process.env, DSH_HOME: home, DSH_TELEMETRY_DISABLED: '1', HOME: join(temporary, 'instance'), USERPROFILE: join(temporary, 'instance'), TMP: temp, TEMP: temp, TMPDIR: temp, XDG_CACHE_HOME: join(temporary, 'instance', 'cache'), XDG_CONFIG_HOME: join(temporary, 'instance', 'config'), XDG_DATA_HOME: join(temporary, 'instance', 'data') };
  // Prevent installed developer profiles and injected runtime hooks from contaminating evidence.
  for (const key of Object.keys(env)) if ((key.startsWith('DSH_') && !['DSH_HOME', 'DSH_TELEMETRY_DISABLED'].includes(key)) || ['NODE_OPTIONS', 'NODE_PATH'].includes(key)) delete env[key];
  if (network === 'linux-unshare') {
    const interfaces = Object.keys(networkInterfaces());
    const routes = await readFile('/proc/net/route', 'utf8');
    if (interfaces.some(name => name !== 'lo') || routes.trim().split('\n').length !== 1) throw new Error('Expected loopback-only namespace with no routes');
    result.networkEvidence = { interfaces, noRoutes: true };
  }
  if (network === 'windows-firewall') {
    if (process.platform !== 'win32') throw new Error('Windows firewall mode requires Windows');
    firewallName = `Auriga-S4-${process.pid}-${Date.now()}`;
    await powershell('New-NetFirewallRule -DisplayName $env:S4_RULE -Direction Outbound -Program $env:S4_NODE -Action Block -Profile Any -Enabled True | Out-Null; $r=Get-NetFirewallRule -DisplayName $env:S4_RULE; if ($r.Enabled -ne "True" -or $r.Action -ne "Block") { throw "Firewall rule not active" }', { S4_RULE: firewallName, S4_NODE: node });
    result.networkEvidence = { programSpecificBlockRuleVerified: true, descendantsUsingOtherExecutablesBlocked: false };
  }
  // Actual native operations: lazy imports alone cannot prove that target binaries work.
  result.nativeProbe = JSON.parse(await run(node, [join(HERE, 'native-probe.mjs'), '--anchor', join(root, 'app/package.json'), '--tmp', temp], { env, cwd }));
  const start = performance.now();
  console.error('Starting DSH with a fresh external DSH_HOME and ephemeral port');
  child = spawn(node, [cli, 'web', '--host', '127.0.0.1', '--port', '0', '--no-open'], { cwd, env, windowsHide: true, detached: process.platform !== 'win32', stdio: ['ignore', 'pipe', 'pipe'] });
  child.on('error', error => { log += String(error); });
  processExit = once(child, 'exit');
  for (const stream of [child.stdout, child.stderr]) stream.on('data', data => { log = (log + data).slice(-200000); });
  const deadline = Date.now() + 90000;
  let origin, html, cookie;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error(`DSH exited before readiness: ${child.exitCode}/${child.signalCode}`);
    // DSH prints the actual ephemeral listen port; no free-port reservation race.
    const match = log.match(/http:\/\/127\.0\.0\.1:(\d+)\/\?token=[A-Za-z0-9_-]+/);
    if (match) {
      origin = `http://127.0.0.1:${match[1]}`;
      try {
        if (!cookie) {
          const exchange = await fetch(match[0], { redirect: 'manual', signal: AbortSignal.timeout(2000) });
          if (exchange.status !== 303) throw new Error('DSH authentication exchange did not return 303');
          cookie = exchange.headers.get('set-cookie')?.split(';', 1)[0];
          if (!cookie) throw new Error('DSH authentication exchange returned no cookie');
          result.authenticationExchangeStatus = exchange.status;
        }
        const response = await fetch(origin, { headers: { cookie }, signal: AbortSignal.timeout(2000) });
        if (response.status === 200 && (response.headers.get('content-type') ?? '').includes('text/html')) {
          html = await response.text();
          if (/<html/i.test(html) && /<script/i.test(html)) break;
        }
      } catch { /* Routes may not be mounted yet. */ }
    }
    await delay(100);
  }
  if (!html) throw new Error('No HTTP 200 HTML interface within 90 seconds');
  result.startupMs = Math.round(performance.now() - start);
  console.error(`HTTP 200 interface ready after ${result.startupMs} ms`);
  result.httpStatus = 200;
  const assets = [...html.matchAll(/(?:src|href)=["']([^"']+\.(?:js|css)(?:\?[^"']*)?)["']/g)].map(match => new URL(match[1], origin));
  if (!assets.some(url => url.pathname.endsWith('.js'))) throw new Error('HTML has no JavaScript entry');
  result.assets = [];
  for (const url of assets) {
    if (url.origin !== origin) throw new Error(`External UI asset: ${url.origin}`);
    const response = await fetch(url, { headers: { cookie }, signal: AbortSignal.timeout(10000) });
    const bytes = (await response.arrayBuffer()).byteLength;
    result.assets.push({ path: url.pathname, status: response.status, bytes });
    if (response.status !== 200 || bytes === 0 || (response.headers.get('content-type') ?? '').includes('text/html')) throw new Error(`Asset unavailable: ${url.pathname}`);
  }
  // Keep the process alive briefly so asynchronous startup side effects can occur.
  await delay(2000);
  result.status = 'passed';
} catch (error) {
  result.error = String(error);
} finally {
  if (child && child.pid) {
    try {
      if (process.platform === 'win32') {
        // TerminateProcess on our direct child works in restricted tokens where taskkill /T is denied.
        child.kill();
      }
      else { try { process.kill(-child.pid, 'SIGTERM'); } catch (e) { if (e.code !== 'ESRCH') throw e; } }
      const exited = await Promise.race([processExit.then(() => true), delay(5000).then(() => false)]);
      if (!exited) {
        if (process.platform !== 'win32') process.kill(-child.pid, 'SIGKILL');
        await Promise.race([processExit, delay(2000).then(() => { throw new Error('DSH process did not exit'); })]);
      }
      result.childStopped = true;
    } catch (error) {
      if (child.exitCode !== null || child.signalCode !== null) result.childStopped = true;
      else { result.status = 'failed'; result.cleanupError = String(error); }
    }
  }
  if (firewallName) {
    try { await powershell('Remove-NetFirewallRule -DisplayName $env:S4_RULE -ErrorAction Stop', { S4_RULE: firewallName }); result.firewallRemoved = true; }
    catch (error) { result.status = 'failed'; result.firewallCleanupError = String(error); }
  }
  if (root && entries) {
    try { await checkManifest(root); result.installTreeUnchanged = true; }
    catch (error) { result.status = 'failed'; result.installTreeUnchanged = false; result.integrityError = String(error); }
    // Restore only the prototype's extracted attributes, including after failure.
    try {
      await chmod(root, 0o755);
      for (const entry of entries) if (entry.type !== 'symlink') await chmod(join(root, entry.path), parseInt(entry.mode, 8));
      result.attributesRestored = true;
    } catch (error) { result.status = 'failed'; result.attributeRestoreError = String(error); }
  }
  // Logs remain local/CI artifacts; avoid persisting developer paths in committed evidence.
  if (temporary) {
    log = redact(log.replaceAll(temporary, '<smoke>').replaceAll(temporary.replaceAll('\\', '/'), '<smoke>'));
    await writeFile(join(temporary, 'dsh.log'), log);
    result.logTail = log.slice(-12000);
  }
  await writeJson(resultPath, result);
  console.log(JSON.stringify(result, null, 2));
  if (result.status !== 'passed') process.exitCode = 1;
}
async function powershell(script, extra) {
  return run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', `$ErrorActionPreference='Stop'; ${script}`], { env: { ...process.env, ...extra } });
}
