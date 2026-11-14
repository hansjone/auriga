import { BRAND_NAME } from './brand.mjs';
/* global setInterval, clearInterval */
/** Anonymous transport is bound to the configured platform origin. */
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
  statSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { verifyRelease } from './release.mjs';
import { verifyPackage, sha256File } from './verify.mjs';
import { install, readState, versionDir } from './core.mjs';
import { downloadLock } from './update-state.mjs';
import { systemTool } from './system-tools.mjs';
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
  const listed = spawnSync(systemTool('tar'), ['-tf', archive], {
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
  const result = spawnSync(
    systemTool('tar'),
    ['-xf', archive, '-C', directory],
    {
      encoding: 'utf8',
    },
  );
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
      version: JSON.parse(
        readFileSync(
          join(versionDir(root, state.current), 'manifest.json'),
          'utf8',
        ),
      ).bundleVersion,
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
      let unlock, temporary;
      try {
        unlock = downloadLock(root);
        const body = verified(pending),
          state = current();
        temporary = mkdtempSync(join(root, '.desktop-download-'));
        if (process.platform === 'win32') {
          const who = spawnSync(
            systemTool('whoami'),
            ['/user', '/fo', 'csv', '/nh'],
            {
              encoding: 'utf8',
            },
          );
          const sid = who.stdout?.match(/S-1-5-[0-9-]+/)?.[0];
          if (
            !sid ||
            spawnSync(
              systemTool('icacls'),
              [temporary, '/inheritance:r', '/grant:r', `*${sid}:(OI)(CI)F`],
              { windowsHide: true },
            ).status !== 0
          )
            throw new Error('cannot secure update staging');
        }
        const archive = join(temporary, 'desktop.zip');
        await downloadPackage(
          request,
          platformUrl(origin, `packages/${body.sequence}`),
          archive,
          body.sizeBytes,
        );
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
          message: `更新完成。请关闭并重新打开 ${BRAND_NAME}。`,
        };
      } finally {
        if (temporary) rmSync(temporary, { recursive: true, force: true });
        unlock?.();
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

/** Retry interrupted transfers with a validated single byte range; verify full SHA afterwards. */
export async function downloadPackage(request, url, file, expected) {
  for (let attempt = 0; attempt < 3; attempt++) {
    let offset = existsSync(file) ? statSync(file).size : 0;
    if (offset === expected) return; // full hash is still checked by the caller
    let response;
    try {
      response = await request(url, {
        redirect: 'error',
        headers: offset ? { Range: `bytes=${offset}-` } : {},
      });
      if (!response.ok) throw new Error('package download failed');
      if (response.status === 206) {
        const range = response.headers.get('content-range');
        if (range !== `bytes ${offset}-${expected - 1}/${expected}`)
          throw new Error('invalid package content range');
      } else if (response.status === 200)
        offset = 0; // server ignored Range: restart safely
      else throw new Error('invalid package response');
      const fd = openSync(file, offset ? 'a' : 'w', 0o600);
      let count = offset;
      try {
        for await (const chunk of response.body) {
          count += chunk.length;
          if (count > expected) throw new Error('package size mismatch');
          const bytes = Buffer.from(chunk);
          let written = 0;
          while (written < bytes.length)
            written += writeSync(fd, bytes, written, bytes.length - written);
        }
        if (count !== expected) throw new Error('interrupted package download');
        return;
      } finally {
        closeSync(fd);
      }
    } catch (error) {
      await response?.cancel?.();
      await response?.body?.cancel?.().catch(() => {});
      // Never extract or trust partial bytes. Oversize/range violations fail closed.
      if (
        /size mismatch|content range|invalid package response/.test(
          error.message,
        ) ||
        attempt === 2
      )
        throw error;
    }
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
