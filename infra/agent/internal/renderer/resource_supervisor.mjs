import { readFileSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { performance } from 'node:perf_hooks';

// This is the container entrypoint, outside Cordis and all uninstallable plugins.
// PID 1 exiting terminates the entire container; Engine automatic restart is denied.
const [policyFile, command, ...args] = process.argv.slice(2);
if (!command) throw new Error('Trusted DSH command missing');
let child, stopping = false, maxObserved = 0, lastMono = performance.now(), deadline = 0, lease = '';
function policy() {
  const value = JSON.parse(readFileSync(policyFile, 'utf8'));
  if (value.schemaVersion !== 1 || typeof value.newSessionRequired !== 'boolean') throw new Error('Invalid guardian policy');
  const wall = Date.now(), mono = performance.now();
  if (wall < maxObserved - 120000 || mono < lastMono) throw new Error('Guardian clock rollback');
  maxObserved = Math.max(wall, maxObserved + mono - lastMono); lastMono = mono;
  if (!value.leaseExpiresAt) { if (lease && !value.newSessionRequired) throw new Error('Shared lease disappeared'); return; }
  const trusted = Date.parse(value.trustedTime), expires = Date.parse(value.leaseExpiresAt);
  if (!Number.isFinite(trusted) || !Number.isFinite(expires) || expires <= trusted || expires - trusted > 86400000) throw new Error('Invalid shared lease');
  maxObserved = Math.max(maxObserved, trusted);
  if (lease !== value.leaseExpiresAt) { deadline = mono + expires - maxObserved; lease = value.leaseExpiresAt; }
  if (maxObserved >= expires || mono >= deadline) throw new Error('Shared lease expired');
}
function stop() {
  if (stopping) return; stopping = true;
  if (!child) process.exit(78);
  try { if (process.platform !== 'win32') process.kill(-child.pid, 'SIGTERM'); else child.kill('SIGTERM'); } catch {}
  setTimeout(() => { try { if (process.platform !== 'win32') process.kill(-child.pid, 'SIGKILL'); else child.kill('SIGKILL'); } catch {} process.exit(78); }, 5000).unref();
}
policy();
child = spawn(command, args, { stdio: 'inherit', detached: process.platform !== 'win32', windowsHide: true });
child.on('error', stop); child.on('exit', code => process.exit(stopping ? 78 : (code ?? 1)));
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, stop);
setInterval(() => { try { policy(); } catch { stop(); } }, 250).unref();
