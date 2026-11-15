import { unpackSetup } from './setup-archive.mjs';
import { BRAND_NAME } from './brand.mjs';
/* global clearTimeout, AbortController, AbortSignal, fetch */
/** Anonymous transport is bound to the configured platform origin. */
import {
  mkdirSync,
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
import { randomUUID, createHash } from 'node:crypto';
import { verifyRelease, releaseDigest } from './release.mjs';
import { verifyPackage, sha256File } from './verify.mjs';
import { install, openVersionSession, readState, versionDir } from './core.mjs';
import {
  changeUpdateState,
  cleanupDownloads,
  downloadLock,
  readUpdateState,
  transferLock,
} from './update-state.mjs';
import { systemTool } from './system-tools.mjs';
import { compare } from './semver-lite.mjs';
import { updateOrigin } from './platform-origin.mjs';

export function platformUrl(origin, path, binding) {
  updateOrigin(origin, binding);
  return new URL('/api/resource/v1/desktop/' + path, origin).href;
}

export function extractArchive(archive, directory) {
  mkdirSync(directory);
  const zip = join(directory, 'payload.zip');
  unpackSetup(archive, zip);
  // Only a fully signature/hash-verified publisher archive reaches tar.
  const listed = spawnSync(systemTool('tar'), ['-tf', zip], {
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
  const result = spawnSync(systemTool('tar'), ['-xf', zip, '-C', directory], {
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error('update archive extraction failed');
  rmSync(zip);
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
  requireApproval = false,
  random = Math.random,
  schedule = setTimeout,
  unschedule = clearTimeout,
  onState = () => {},
}) {
  const bindingFile = join(root, 'desktop-origin.json');
  const binding = existsSync(bindingFile)
    ? JSON.parse(readFileSync(bindingFile, 'utf8'))
    : {};
  if (binding.origin && binding.origin !== origin)
    throw new Error('platform binding differs');
  updateOrigin(origin, binding);
  const load = () => readUpdateState(root);
  const change = (mutate) => {
    const saved = changeUpdateState(root, mutate);
    if (saved) onState(saved);
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
  let busy = false,
    controller;
  if (!load().clientId)
    change((state) =>
      state.clientId ? undefined : { ...state, clientId: randomUUID() },
    );
  if (['downloading', 'verifying'].includes(load().phase))
    cleanupDownloads(root);
  const resume = join(root, '.desktop-resume');
  const progress = (phase, extra = {}) =>
    change((state) => ({ ...state, phase, ...extra }));
  const verified = (envelope, state = load()) => {
    return verifyRelease(envelope, {
      publicKey,
      now: now(),
      currentVersion: current().version,
      ...state.channelWatermarks?.[state.channel ?? 'stable'],
      channel: state.channel ?? 'stable',
    });
  };
  const status = (state) => {
    let available = state.available ?? null;
    let body;
    try {
      const policy = available ?? state.requiredRelease;
      if (policy) body = verified(policy, state);
    } catch {
      available = null;
    }
    return {
      enabled: state.enabled !== false,
      channel: state.channel ?? 'stable',
      available,
      required: !!body && compare(current().version, body.minimumVersion) < 0,
      phase: state.phase ?? 'idle',
      busy,
      downloading: busy && state.phase === 'downloading',
      downloadedBytes: state.downloadedBytes ?? 0,
      totalBytes: state.totalBytes ?? 0,
      error: state.error ?? null,
      restartRequired: existsSync(join(root, 'desktop-pending')),
    };
  };
  const api = {
    status() {
      return status(load());
    },
    configure(enabled) {
      if (typeof enabled !== 'boolean')
        throw new Error('invalid update preference');
      change((state) => {
        if (!enabled && status(state).required)
          throw new Error('mandatory update required');
        return { ...state, enabled };
      });
      return api.status();
    },
    setChannel(channel) {
      if (!['stable', 'beta'].includes(channel) || busy)
        throw new Error('invalid update channel');
      const unlock = transferLock(root);
      try {
        change((state) => {
          const current = status(state);
          if (current.required || current.restartRequired)
            throw new Error('invalid update channel');
          return { ...state, channel, available: null, requiredRelease: null };
        });
      } finally {
        unlock();
      }
      return api.status();
    },
    cancel() {
      if (busy && load().phase === 'downloading') controller?.abort();
      return api.status();
    },
    beginUpdate() {
      if (busy || !api.status().available) throw new Error('no update ready');
      void api.update().catch(() => {});
      return api.status();
    },
    async check() {
      if (busy || api.status().restartRequired) return api.status();
      busy = true;
      try {
        const observed = load();
        const channel = observed.channel ?? 'stable';
        const response = await request(
          platformUrl(
            origin,
            channel === 'stable' ? 'latest' : 'latest?channel=beta',
            binding,
          ),
          {
            redirect: 'error',
          },
        );
        const missing = response.status === 404;
        if (!response.ok && !missing) throw new Error('release check failed');
        const envelope = missing
          ? null
          : JSON.parse((await bounded(response, 32768)).toString('utf8'));
        let unlock;
        try {
          unlock = transferLock(root);
        } catch (error) {
          if (error.code !== 'desktop.in_use') throw error;
          return api.status();
        }
        try {
          change((state) => {
            if (
              (state.channel ?? 'stable') !== channel ||
              existsSync(join(root, 'desktop-pending'))
            )
              return;
            if (missing) {
              if (observed.checkId !== state.checkId) return;
              return { ...state, available: null, checkId: randomUUID() };
            }
            const body = verified(envelope, state);
            const version = current().version;
            const installed = compare(body.version, version) === 0;
            const required = compare(version, body.minimumVersion) < 0;
            const bucket =
              createHash('sha256')
                .update(state.clientId + ':' + body.sequence)
                .digest()
                .readUInt32BE(0) % 100;
            const eligible =
              required || bucket < (body.rolloutPercentage ?? 100);
            const available =
              eligible && !installed && (state.enabled !== false || required)
                ? envelope
                : null;
            const channelWatermarks = { ...state.channelWatermarks };
            if (available || installed) {
              channelWatermarks[channel] = {
                highestSequence: body.sequence,
                highestPublishedAt: body.publishedAt,
                highestVersion: body.version,
                acceptedDigest: body.sha256,
                acceptedEnvelope: releaseDigest(body),
              };
            }
            return {
              ...state,
              channelWatermarks,
              available,
              requiredRelease: required ? envelope : null,
              checkId: randomUUID(),
              lastCheckedAt: now(),
            };
          });
        } finally {
          unlock();
        }
        return api.status();
      } finally {
        busy = false;
      }
    },
    async update() {
      if (busy) throw new Error('no update ready');
      busy = true;
      let unlock,
        temporary,
        retain = false;
      controller = new AbortController();
      try {
        unlock = downloadLock(root);
        const pending = load().available;
        if (!pending || existsSync(join(root, 'desktop-pending')))
          throw new Error('no update ready');
        const body = verified(pending),
          state = current();
        temporary = resume;
        const identity = join(resume, 'identity.json');
        if (
          existsSync(identity) &&
          readFileSync(identity, 'utf8') !== body.sha256
        )
          rmSync(resume, { recursive: true, force: true });
        mkdirSync(resume, { recursive: true });
        writeFileSync(identity, body.sha256, { mode: 0o600 });
        rmSync(join(resume, 'unpacked'), { recursive: true, force: true });
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
        const archive = join(temporary, 'desktop.exe');
        retain = true;
        progress('downloading', { error: null, totalBytes: body.sizeBytes });
        let lastProgress = 0,
          lastBytes = 0;
        await downloadPackage(
          request,
          platformUrl(origin, `packages/${body.sequence}`, binding),
          archive,
          body.sizeBytes,
          {
            signal: controller.signal,
            onProgress: (downloadedBytes) => {
              if (
                now() - lastProgress >= 250 ||
                (lastBytes === 0 && downloadedBytes > 0) ||
                downloadedBytes === body.sizeBytes
              ) {
                lastProgress = now();
                lastBytes = downloadedBytes;
                progress('downloading', { downloadedBytes });
              }
            },
          },
        );
        retain = false;
        progress('verifying');
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
        // A withdrawn or replaced release must not be queued after a long transfer.
        retain = true;
        const latestResponse = await request(
          platformUrl(
            origin,
            body.channel === 'stable' ? 'latest' : 'latest?channel=beta',
            binding,
          ),
          { redirect: 'error', signal: controller.signal },
        );
        if (latestResponse.status === 404) {
          retain = false;
          throw new Error('release withdrawn');
        }
        if (!latestResponse.ok) throw new Error('release check unavailable');
        const latest = verifyRelease(
          JSON.parse((await bounded(latestResponse, 32768)).toString('utf8')),
          { publicKey, now: now(), channel: body.channel },
        );
        if (releaseDigest(latest) !== releaseDigest(body)) {
          retain = false;
          throw new Error('release replaced');
        }
        retain = false;
        if (defer) {
          const queued = join(root, 'desktop-pending');
          if (existsSync(queued)) throw new Error('update already queued');
          writeFileSync(
            join(temporary, 'release.json'),
            JSON.stringify(pending),
            { mode: 0o600 },
          );
          if (requireApproval)
            writeFileSync(join(temporary, 'approval-required'), '', {
              mode: 0o600,
            });
          renameSync(temporary, queued);
          temporary = undefined;
          progress('ready', { available: null });
          return { restartRequired: true, queued: true };
        }
        progress('installing');
        install({
          root,
          payload,
          dataHome: state.dataHome,
          smoke,
          transfer: unlock,
        });
        progress('ready', { available: null });
        return {
          restartRequired: true,
          message: `更新完成。请关闭并重新打开 ${BRAND_NAME}。`,
        };
      } catch (error) {
        if (temporary)
          progress(controller.signal.aborted ? 'cancelled' : 'failed', {
            error: controller.signal.aborted ? null : 'updateFailed',
          });
        throw error;
      } finally {
        if (temporary && !retain)
          rmSync(temporary, { recursive: true, force: true });
        unlock?.();
        busy = false;
      }
    },
    start() {
      let timer,
        stopped = false;
      const tick = async () => {
        try {
          await api.check();
        } catch {
          /* retry next interval */
        }
        if (!stopped) {
          timer = schedule(tick, (4 + random() * 4) * 60 * 60 * 1000);
          timer.unref?.();
        }
      };
      void tick();
      return () => {
        stopped = true;
        unschedule(timer);
        controller?.abort();
      };
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
export async function downloadPackage(
  request,
  url,
  file,
  expected,
  { signal, onProgress = () => {} } = {},
) {
  for (let attempt = 0; attempt < 3; attempt++) {
    signal?.throwIfAborted();
    let offset = existsSync(file) ? statSync(file).size : 0;
    if (offset > expected) {
      rmSync(file);
      offset = 0;
    }
    onProgress(offset);
    if (offset === expected) return; // full hash is still checked by the caller
    let response;
    try {
      response = await request(url, {
        redirect: 'error',
        signal,
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
          signal?.throwIfAborted();
          count += chunk.length;
          if (count > expected) throw new Error('package size mismatch');
          const bytes = Buffer.from(chunk);
          let written = 0;
          while (written < bytes.length)
            written += writeSync(fd, bytes, written, bytes.length - written);
          onProgress(count);
        }
        if (count !== expected) throw new Error('interrupted package download');
        return;
      } finally {
        closeSync(fd);
      }
    } catch (error) {
      await response?.cancel?.();
      signal?.throwIfAborted();
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

export function approvePending(root) {
  const release = transferLock(root);
  try {
    const queued = join(root, 'desktop-pending');
    if (!existsSync(join(queued, 'release.json')))
      throw new Error('no pending update');
    rmSync(join(queued, 'approval-required'), { force: true });
  } finally {
    release();
  }
}

/** Called only by the launcher after the old DSH has stopped (or before next launch). */
export async function applyPending(root, options = {}) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued) || existsSync(join(queued, 'approval-required')))
    return false;
  const ownSession = options.session ? null : openVersionSession(root);
  try {
    const transfer = transferLock(root);
    try {
      return await applyPendingLocked(root, {
        ...options,
        transfer,
        session: options.session ?? ownSession,
      });
    } finally {
      transfer();
    }
  } finally {
    ownSession?.close();
  }
}

async function applyPendingLocked(
  root,
  {
    publicKey,
    smoke,
    extract = extractArchive,
    now = Date.now(),
    request = fetch,
    session,
    transfer,
  } = {},
) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued) || existsSync(join(queued, 'approval-required')))
    return false;
  let retain = false;
  try {
    const current = readState(root);
    const old = verifyPackage(versionDir(root, current.current));
    const envelope = JSON.parse(
      readFileSync(join(queued, 'release.json'), 'utf8'),
    );
    const saved = readUpdateState(root);
    const body = verifyRelease(envelope, {
      publicKey,
      now,
      currentVersion: old.bundleVersion,
      ...saved.channelWatermarks?.[saved.channel ?? 'stable'],
      channel: saved.channel ?? 'stable',
    });
    const binding = JSON.parse(
      readFileSync(join(root, 'desktop-origin.json'), 'utf8'),
    );
    retain = true;
    const response = await request(
      platformUrl(
        binding.origin,
        body.channel === 'stable' ? 'latest' : 'latest?channel=beta',
        binding,
      ),
      { redirect: 'error', signal: AbortSignal.timeout(15000) },
    );
    if (response.status === 404) {
      retain = false;
      throw new Error('queued release withdrawn');
    }
    if (!response.ok) throw new Error('queued release check unavailable');
    const active = JSON.parse(
      (await bounded(response, 32768)).toString('utf8'),
    );
    const activeBody = verifyRelease(active, {
      publicKey,
      now,
      channel: body.channel,
    });
    if (
      releaseDigest(activeBody) !== releaseDigest(body) ||
      JSON.stringify(active.signature) !== JSON.stringify(envelope.signature)
    ) {
      retain = false;
      throw new Error('queued release replaced');
    }
    retain = false;
    const archive = join(queued, 'desktop.exe');
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
    install({
      root,
      payload,
      dataHome: current.dataHome,
      smoke,
      session,
      transfer,
    });
    changeUpdateState(root, (state) => ({
      ...state,
      phase: 'idle',
      error: null,
      available: null,
      requiredRelease: null,
    }));
    return true;
  } catch (error) {
    changeUpdateState(root, (state) => ({
      ...state,
      phase: 'failed',
      error: 'updateFailed',
    }));
    throw error;
  } finally {
    if (!retain) rmSync(queued, { recursive: true, force: true });
  }
}

/**
 * Origin-bound transport for launcher/tray commands: no redirects, no
 * credentials, and an idle (not total) timeout that each received chunk renews.
 */
export function idleRequest(origin, { idleMs = 60_000, fetcher = fetch } = {}) {
  return async (url, init = {}) => {
    if (new URL(url).origin !== origin)
      throw new Error('untrusted desktop download');
    const controller = new AbortController();
    let timer;
    const reset = () => {
      clearTimeout(timer);
      timer = setTimeout(
        () => controller.abort(new Error('desktop transfer idle timeout')),
        idleMs,
      );
    };
    reset();
    let response;
    try {
      response = await fetcher(url, {
        ...init,
        credentials: 'omit',
        redirect: 'error',
        headers: { Accept: 'application/json', ...init.headers },
        signal: init.signal
          ? AbortSignal.any([init.signal, controller.signal])
          : controller.signal,
      });
    } catch (error) {
      clearTimeout(timer);
      throw error;
    }
    if (!response.ok || !response.body) {
      clearTimeout(timer);
      await response.body?.cancel().catch(() => {});
      return response;
    }
    const reader = response.body.getReader();
    return {
      ok: response.ok,
      status: response.status,
      headers: response.headers,
      async cancel() {
        clearTimeout(timer);
        controller.abort();
        await reader.cancel().catch(() => {});
      },
      body: (async function* () {
        try {
          for (;;) {
            reset();
            const { value, done } = await reader.read();
            if (done) break;
            yield value;
          }
        } finally {
          clearTimeout(timer);
          await reader.cancel().catch(() => {});
        }
      })(),
    };
  };
}
