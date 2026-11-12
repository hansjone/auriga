/* global setInterval, clearInterval */
/** Authenticated transport is supplied by the existing platform session adapter. */
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  writeFileSync,
  renameSync,
  rmSync,
  existsSync,
  openSync,
  closeSync,
  writeSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { verifyRelease } from './release.mjs';
import { verifyPackage, sha256File } from './verify.mjs';
import { install, readState, versionDir } from './core.mjs';
import { compare } from './semver-lite.mjs';

export function platformUrl(origin, path) {
  const url = new URL(origin);
  if (
    url.protocol !== 'https:' ||
    url.origin !== origin ||
    url.username ||
    url.password
  )
    throw new Error('updates require configured HTTPS platform origin');
  return new URL('/api/resource/v1/desktop/' + path, origin).href;
}

export function extractArchive(archive, directory) {
  mkdirSync(directory);
  // Only a fully signature/hash-verified publisher archive reaches tar.
  const listed = spawnSync('tar', ['-tf', archive], {
    encoding: 'utf8',
    maxBuffer: 32 * 1024 ** 2,
  });
  if (
    listed.status !== 0 ||
    listed.stdout
      .split(/\r?\n/)
      .filter(Boolean)
      .some(
        (p) =>
          p.startsWith('/') ||
          p.includes('\\') ||
          p.includes(':') ||
          p.split('/').includes('..'),
      )
  )
    throw new Error('unsafe update archive');
  const result = spawnSync('tar', ['-xf', archive, '-C', directory], {
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error('update archive extraction failed');
  return join(directory, 'payload');
}

export function createUpdater({
  root,
  origin,
  request,
  publicKey,
  extract = extractArchive,
  smoke,
  now = () => Date.now(),
  defer = true,
}) {
  const stateFile = join(root, 'desktop-update.json');
  const load = () =>
    existsSync(stateFile)
      ? JSON.parse(readFileSync(stateFile, 'utf8'))
      : { enabled: true, highestSequence: 0 };
  const save = (value) => {
    writeFileSync(stateFile + '.tmp', JSON.stringify(value) + '\n', {
      mode: 0o600,
    });
    renameSync(stateFile + '.tmp', stateFile);
  };
  const current = () => {
    const state = readState(root);
    if (!state) throw new Error('desktop installation missing');
    return {
      ...state,
      version: verifyPackage(versionDir(root, state.current)).bundleVersion,
    };
  };
  let pending = null,
    busy = false;
  const verified = (envelope) => {
    const state = load();
    return verifyRelease(envelope, {
      publicKey,
      now: now(),
      currentVersion: current().version,
      highestSequence: state.highestSequence,
      acceptedDigest: state.acceptedDigest,
    });
  };
  const api = {
    status: () => ({
      enabled: load().enabled !== false,
      available: pending,
      restartRequired: existsSync(join(root, 'desktop-pending')),
    }),
    configure(enabled) {
      if (typeof enabled !== 'boolean')
        throw new Error('invalid update preference');
      save({ ...load(), enabled });
      if (!enabled) pending = null;
      return api.status();
    },
    async check() {
      if (!api.status().enabled || busy) return api.status();
      busy = true;
      try {
        const response = await request(platformUrl(origin, 'latest'), {
          redirect: 'error',
        });
        if (response.status === 404) {
          pending = null;
          return api.status();
        }
        if (!response.ok) throw new Error('release check failed');
        const bytes = await bounded(response, 32768);
        const envelope = JSON.parse(bytes.toString('utf8'));
        const body = verified(envelope);
        save({
          ...load(),
          highestSequence: body.sequence,
          acceptedDigest: body.sha256,
        });
        pending =
          compare(body.version, current().version) > 0 ? envelope : null;
        return api.status();
      } finally {
        busy = false;
      }
    },
    async update() {
      if (busy || !pending) throw new Error('no update ready');
      busy = true;
      const lock = join(root, '.desktop-update-lock');
      let fd, temporary;
      try {
        fd = openSync(lock, 'wx', 0o600);
        const body = verified(pending),
          state = current();
        temporary = mkdtempSync(join(root, '.desktop-download-'));
        if (process.platform === 'win32') {
          const who = spawnSync('whoami', ['/user', '/fo', 'csv', '/nh'], {
            encoding: 'utf8',
          });
          const sid = who.stdout?.match(/S-1-5-[0-9-]+/)?.[0];
          if (
            !sid ||
            spawnSync(
              'icacls',
              [temporary, '/inheritance:r', '/grant:r', `*${sid}:(OI)(CI)F`],
              { windowsHide: true },
            ).status !== 0
          )
            throw new Error('cannot secure update staging');
        }
        const archive = join(temporary, 'desktop.zip');
        const response = await request(
          platformUrl(origin, `packages/${body.sequence}`),
          { redirect: 'error' },
        );
        if (!response.ok) throw new Error('package download failed');
        await download(response, archive, body.sizeBytes);
        if (sha256File(archive) !== body.sha256)
          throw new Error('package SHA mismatch');
        const payload = await extract(archive, join(temporary, 'unpacked'));
        const manifest = verifyPackage(payload);
        if (
          manifest.id !== body.packageId ||
          manifest.bundleVersion !== body.version
        )
          throw new Error('package ID/version mismatch');
        verified(pending); // expiry may have elapsed during the download
        if (defer) {
          const queued = join(root, 'desktop-pending');
          if (existsSync(queued)) throw new Error('update already queued');
          writeFileSync(
            join(temporary, 'release.json'),
            JSON.stringify(pending),
            { mode: 0o600 },
          );
          renameSync(temporary, queued);
          temporary = undefined;
          pending = null;
          return { restartRequired: true, queued: true };
        }
        install({ root, payload, dataHome: state.dataHome, smoke });
        pending = null;
        return {
          restartRequired: true,
          message: '更新完成。请关闭并重新打开 Auriga Desktop。',
        };
      } finally {
        if (temporary) rmSync(temporary, { recursive: true, force: true });
        if (fd !== undefined) {
          closeSync(fd);
          rmSync(lock, { force: true });
        }
        busy = false;
      }
    },
    start() {
      void api.check().catch(() => {});
      const timer = setInterval(
        () => {
          void api.check().catch(() => {});
        },
        6 * 60 * 60 * 1000,
      );
      timer.unref();
      return () => clearInterval(timer);
    },
  };
  return api;
}

async function bounded(response, limit) {
  const parts = [];
  let count = 0;
  for await (const chunk of response.body) {
    count += chunk.length;
    if (count > limit) throw new Error('response too large');
    parts.push(Buffer.from(chunk));
  }
  return Buffer.concat(parts);
}

async function download(response, file, expected) {
  const fd = openSync(file, 'wx', 0o600);
  let count = 0;
  try {
    for await (const chunk of response.body) {
      count += chunk.length;
      if (count > expected) throw new Error('package size mismatch');
      const bytes = Buffer.from(chunk);
      let offset = 0;
      while (offset < bytes.length)
        offset += writeSync(fd, bytes, offset, bytes.length - offset);
    }
    if (count !== expected) throw new Error('interrupted package download');
  } finally {
    closeSync(fd);
  }
}

/** Called only by the launcher after the old DSH has stopped (or before next launch). */
export async function applyPending(
  root,
  { publicKey, smoke, extract = extractArchive, now = Date.now() } = {},
) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued)) return false;
  try {
    const current = readState(root);
    const old = verifyPackage(versionDir(root, current.current));
    const envelope = JSON.parse(
      readFileSync(join(queued, 'release.json'), 'utf8'),
    );
    const saved = JSON.parse(
      readFileSync(join(root, 'desktop-update.json'), 'utf8'),
    );
    const body = verifyRelease(envelope, {
      publicKey,
      now,
      currentVersion: old.bundleVersion,
      highestSequence: saved.highestSequence,
      acceptedDigest: saved.acceptedDigest,
    });
    const archive = join(queued, 'desktop.zip');
    if (sha256File(archive) !== body.sha256)
      throw new Error('queued package SHA mismatch');
    const unpacked = join(queued, 'apply');
    const payload = await extract(archive, unpacked);
    const manifest = verifyPackage(payload);
    if (
      manifest.id !== body.packageId ||
      manifest.bundleVersion !== body.version
    )
      throw new Error('queued package ID mismatch');
    install({ root, payload, dataHome: current.dataHome, smoke });
    return true;
  } finally {
    rmSync(queued, { recursive: true, force: true });
  }
}
