import {
  openSync,
  closeSync,
  writeFileSync,
  readFileSync,
  readdirSync,
  rmSync,
  existsSync,
} from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

/** Called before spawning DSH while the launcher owns the installation session. */
export function cleanupDownloads(root, { stopped = false } = {}) {
  const file = join(root, '.desktop-update-lock');
  if (existsSync(file) && !stopped) {
    let lock;
    try {
      lock = JSON.parse(readFileSync(file, 'utf8'));
    } catch {
      return false;
    }
    if (
      !Number.isSafeInteger(lock.pid) ||
      lock.pid < 1 ||
      !Number.isFinite(lock.createdAt)
    )
      return false;
    try {
      process.kill(lock.pid, 0);
      return false;
    } catch (error) {
      if (error.code !== 'ESRCH') return false;
    }
  }
  rmSync(file, { force: true });
  for (const name of readdirSync(root))
    if (/^\.desktop-download-[A-Za-z0-9-]+$/.test(name))
      rmSync(join(root, name), { recursive: true, force: true });
  return true;
}

export function downloadLock(root) {
  const file = join(root, '.desktop-update-lock');
  if (existsSync(file)) cleanupDownloads(root);
  const fd = openSync(file, 'wx', 0o600),
    token = randomUUID();
  try {
    writeFileSync(
      fd,
      JSON.stringify({ pid: process.pid, createdAt: Date.now(), token }),
    );
  } finally {
    closeSync(fd);
  }
  return () => {
    if (
      existsSync(file) &&
      JSON.parse(readFileSync(file, 'utf8')).token === token
    )
      rmSync(file, { force: true });
  };
}
