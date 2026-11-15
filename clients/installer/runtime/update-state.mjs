import {
  openSync,
  closeSync,
  writeFileSync,
  readFileSync,
  readdirSync,
  rmSync,
  existsSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { durableJson } from './host-lock.mjs';
import { pathOperationLock } from './operation-lock.mjs';

const transfers = new WeakMap();
function transferHandle(root, release) {
  const handle = () => {
    transfers.delete(handle);
    release();
  };
  transfers.set(handle, resolve(root));
  return handle;
}
export const transferLock = (root) =>
  transferHandle(root, pathOperationLock(root, 'transfer'));
export const holdsTransfer = (root, handle) =>
  transfers.get(handle) === resolve(root);

export function readUpdateState(root) {
  const file = join(root, 'desktop-update.json');
  try {
    const state = JSON.parse(readFileSync(file, 'utf8'));
    const fields = [
      'highestSequence',
      'highestPublishedAt',
      'highestVersion',
      'acceptedDigest',
      'acceptedEnvelope',
    ];
    if (
      Number.isSafeInteger(state.highestSequence) &&
      state.highestSequence >
        (state.channelWatermarks?.stable?.highestSequence ?? 0)
    ) {
      state.channelWatermarks ??= {};
      state.channelWatermarks.stable = Object.fromEntries(
        fields.map((field) => [field, state[field]]),
      );
    }
    for (const field of fields) delete state[field];
    state.channelWatermarks ??= {};
    return state;
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    return { enabled: true, channelWatermarks: {} };
  }
}

export function withUpdateStateLock(root, run) {
  const release = pathOperationLock(root, 'update-state', 5000);
  try {
    return run();
  } finally {
    release();
  }
}

export function changeUpdateState(root, change) {
  return withUpdateStateLock(root, () => {
    if (!existsSync(join(root, 'install.json')))
      throw new Error('desktop installation missing');
    const next = change(readUpdateState(root));
    if (next) durableJson(join(root, 'desktop-update.json'), next);
    return next;
  });
}

/** Called before spawning DSH while the launcher owns the installation session. */
export function cleanupDownloads(root, { stopped = false } = {}) {
  let release;
  try {
    release = transferLock(root);
  } catch (error) {
    if (error.code === 'desktop.in_use') return false;
    throw error;
  }
  try {
    const cleaned = cleanupLegacyDownloads(root, stopped);
    if (cleaned && existsSync(join(root, 'install.json')))
      changeUpdateState(root, (state) =>
        ['downloading', 'verifying'].includes(state.phase)
          ? { ...state, phase: 'cancelled', error: null }
          : undefined,
      );
    return cleaned;
  } finally {
    release();
  }
}

function cleanupLegacyDownloads(root, stopped = false) {
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
  const release = transferLock(root);
  try {
    const releaseLegacy = takeLegacyDownloadLock(root);
    return transferHandle(root, () => {
      try {
        releaseLegacy();
      } finally {
        release();
      }
    });
  } catch (error) {
    release();
    throw error;
  }
}

function takeLegacyDownloadLock(root) {
  const file = join(root, '.desktop-update-lock');
  if (existsSync(file)) cleanupLegacyDownloads(root);
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
