import {
  isZipSetupArchive,
  readSetupManifest,
  unpackSetup,
} from './setup-archive.mjs';
import { BRAND_NAME } from './brand.mjs';
/** Anonymous transport is bound to the configured platform origin. */
import {
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
  renameSync,
  rmSync,
  existsSync,
  linkSync,
  openSync,
  closeSync,
  writeSync,
  statSync,
  appendFileSync,
} from 'node:fs';
import { setTimeout as delay } from 'node:timers/promises';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { randomUUID, createHash } from 'node:crypto';
import { verifyRelease, releaseDigest } from './release.mjs';
import { verifyPackageAsync, sha256File } from './verify.mjs';
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
import {
  rememberUpdateAttempt,
  finishUpdateAttempt,
  updateWasRolledBack,
} from './desktop-report.mjs';
import { desktopPlatform } from './desktop-platform.mjs';

function desktopHost() {
  return desktopPlatform();
}

function latestPath(channel) {
  const platform = desktopHost().target;
  const query = new URLSearchParams({ platform });
  if (channel && channel !== 'stable') query.set('channel', channel);
  return `latest?${query}`;
}

function stagedPackagePath(rootOrDir) {
  return join(rootOrDir, desktopHost().stagedPackageName);
}

function logUpdate(root, entry) {
  const file = join(root, 'desktop-update.log');
  try {
    if (existsSync(file) && statSync(file).size >= 256 * 1024) {
      rmSync(file + '.1', { force: true });
      renameSync(file, file + '.1');
    }
    appendFileSync(
      file,
      JSON.stringify({ at: new Date().toISOString(), ...entry }) + '\n',
    );
  } catch {
    /* diagnostics must not replace the operation's result */
  }
}

// Caller owns the transfer lock. Missing durable input cannot recover by retry.
function discardIncompletePending(root) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued)) return false;
  try {
    for (const name of ['release.json', desktopHost().stagedPackageName]) {
      const file = statSync(join(queued, name));
      if (!file.isFile() || file.size === 0)
        throw Object.assign(new Error('pending input incomplete'), {
          code: 'ENOENT',
        });
    }
    return false;
  } catch (error) {
    if (!['ENOENT', 'ENOTDIR'].includes(error.code)) throw error;
    if (!existsSync(join(root, '.auriga-desktop-install')) || !readState(root))
      throw new Error('desktop installation missing');
    rmSync(queued, {
      recursive: true,
      force: true,
      maxRetries: 3,
      retryDelay: 200,
    });
    const failure = {
      stage: 'verify-input',
      code: 'desktop.pending_incomplete',
      pathCategory: 'pending',
    };
    changeUpdateState(root, (state) => ({
      ...state,
      phase: 'failed',
      error: 'updateFailed',
      stage: failure.stage,
      failure,
      applyRetry: null,
    }));
    logUpdate(root, failure);
    finishUpdateAttempt(root, 'failed', failure.stage, failure.code);
    return true;
  }
}

export function platformUrl(origin, path, binding) {
  updateOrigin(origin, binding);
  return new URL('/api/resource/v1/desktop/' + path, origin).href;
}

function linkTreeSync(from, to) {
  mkdirSync(to, { recursive: true });
  for (const entry of readdirSync(from, { withFileTypes: true })) {
    const src = join(from, entry.name);
    const dst = join(to, entry.name);
    if (entry.isDirectory()) linkTreeSync(src, dst);
    else if (entry.isFile()) linkSync(src, dst);
    else throw new Error(`unsupported entry for hardlink reuse: ${src}`);
  }
}

/**
 * Determine unchanged immutable subtrees (node_modules, node binary) that can
 * be hard-linked from the current verified version on the same volume instead
 * of extracting tens of thousands of identical files. Never touches
 * `runtime/python` because it carries protected read-only ACLs.
 */
function findReusableSubtrees(oldDir, oldManifest, nextManifest) {
  if (!oldDir || !oldManifest || !nextManifest) return [];
  const subtrees = [];
  const oldTrees = new Map((oldManifest.trees ?? []).map((t) => [t.root, t]));
  for (const nextTree of nextManifest.trees ?? []) {
    const prev = oldTrees.get(nextTree.root);
    if (
      prev &&
      prev.files === nextTree.files &&
      prev.sha256 === nextTree.sha256 &&
      existsSync(join(oldDir, ...nextTree.root.split('/')))
    ) {
      subtrees.push(nextTree.root);
    }
  }
  if (
    oldManifest.upstream?.nodeZipSha256 &&
    oldManifest.upstream.nodeZipSha256 ===
      nextManifest.upstream?.nodeZipSha256 &&
    existsSync(join(oldDir, 'upstream', 'node'))
  ) {
    subtrees.push('upstream/node');
  }
  return subtrees;
}

/** Prefer archive content over host media so Linux CI can still unpack Win setup fixtures. */
function isGzipDesktopPackage(archive) {
  if (isZipSetupArchive(archive)) return false;
  if (/\.tar\.gz$/i.test(archive) || /\.tgz$/i.test(archive)) return true;
  return desktopHost().packageMedia === 'tar.gz';
}

export function extractArchive(archive, directory, { reuse = null } = {}) {
  mkdirSync(directory);
  let zip = archive;
  const gzip = isGzipDesktopPackage(archive);
  if (!gzip) {
    zip = join(directory, 'payload.zip');
    unpackSetup(archive, zip);
  }
  const extractFlag = gzip ? '-xzf' : '-xf';
  // Only a fully signature/hash-verified publisher archive reaches tar.
  const listArgs = gzip ? ['-tzf', zip] : ['-tf', zip];
  const listed = spawnSync(systemTool('tar'), listArgs, {
    encoding: 'utf8',
    maxBuffer: 32 * 1024 ** 2,
  });
  const entries = listed.stdout.split(/\r?\n/).filter(Boolean);
  if (
    listed.status !== 0 ||
    entries.some(
      (p) =>
        p.startsWith('/') ||
        p.includes('\\') ||
        p.includes(':') ||
        p.split('/').includes('..'),
    )
  )
    throw Object.assign(new Error('unsafe update archive'), {
      code: 'desktop.extract_failed',
    });

  const payloadDir = join(directory, 'payload');
  const reusable = reuse?.subtrees?.length ? reuse.subtrees : [];
  let extracted = false;
  if (reusable.length) {
    const excludeArgs = [];
    for (const sub of reusable) {
      excludeArgs.push('--exclude', `payload/${sub}`);
      excludeArgs.push('--exclude', `payload/${sub}/*`);
      excludeArgs.push('--exclude', `./payload/${sub}`);
      excludeArgs.push('--exclude', `./payload/${sub}/*`);
    }
    const partial = spawnSync(
      systemTool('tar'),
      [extractFlag, zip, '-C', directory, ...excludeArgs],
      { encoding: 'utf8' },
    );
    if (!partial.error && partial.status === 0) {
      try {
        for (const sub of reusable) {
          const src = join(reuse.sourceDir, ...sub.split('/'));
          const dst = join(payloadDir, ...sub.split('/'));
          rmSync(dst, { recursive: true, force: true });
          linkTreeSync(src, dst);
        }
        extracted = true;
      } catch {
        // Cross-volume or ACL mismatch: fall back cleanly to full extraction.
        rmSync(payloadDir, { recursive: true, force: true });
      }
    }
  }
  if (!extracted) {
    const result = spawnSync(
      systemTool('tar'),
      [extractFlag, zip, '-C', directory],
      {
        encoding: 'utf8',
      },
    );
    if (result.error) throw result.error;
    if (result.status !== 0)
      throw Object.assign(new Error('update archive extraction failed'), {
        code: 'desktop.extract_failed',
      });
  }
  // Only remove the intermediate ZIP peeled from a Windows setup archive.
  if (zip !== archive) rmSync(zip);
  return payloadDir;
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
    const body = verifyRelease(envelope, {
      publicKey,
      now: now(),
      currentVersion: current().version,
      ...state.channelWatermarks?.[state.channel ?? 'stable'],
      channel: state.channel ?? 'stable',
    });
    if (body.platform !== desktopHost().target)
      throw new Error('release platform mismatch');
    return body;
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
      stage: state.stage ?? null,
      failure: state.failure ?? null,
      manualRetryRequired: state.applyRetry?.manualRequired === true,
      applyFailures: state.applyRetry?.failures ?? 0,
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
      if (busy) return api.status();
      busy = true;
      try {
        let repairUnlock;
        try {
          repairUnlock = transferLock(root);
        } catch (error) {
          if (error.code === 'desktop.in_use') return api.status();
          throw error;
        }
        try {
          discardIncompletePending(root);
        } finally {
          repairUnlock();
        }
        if (api.status().restartRequired && !load().applyRetry?.manualRequired)
          return api.status();
        const observed = load();
        const channel = observed.channel ?? 'stable';
        const response = await request(
          platformUrl(origin, latestPath(channel), binding),
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
            if ((state.channel ?? 'stable') !== channel) return;
            const queued = join(root, 'desktop-pending');
            if (existsSync(queued)) {
              if (!state.applyRetry?.manualRequired) return;
              if (
                observed.checkId !== state.checkId ||
                observed.applyRetry?.digest !== state.applyRetry.digest
              )
                return;
              if (!missing) {
                const active = verified(envelope, state);
                if (releaseDigest(active) === state.applyRetry.digest)
                  return { ...state, lastCheckedAt: now() };
              }
              // A signed replacement or withdrawal retires the paused input.
              rmSync(queued, {
                recursive: true,
                force: true,
                maxRetries: 3,
                retryDelay: 200,
              });
              state = {
                ...state,
                applyRetry: null,
                failure: null,
                error: null,
                phase: 'idle',
              };
            }
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
      let installStage = 'download',
        previousId;
      controller = new AbortController();
      try {
        unlock = downloadLock(root);
        discardIncompletePending(root);
        const pending = load().available;
        if (!pending || existsSync(join(root, 'desktop-pending')))
          throw new Error('no update ready');
        const body = verified(pending),
          state = current();
        previousId = state.current;
        rememberUpdateAttempt(root, body.version, body.channel, true);
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
        const archive = stagedPackagePath(temporary);
        retain = true;
        progress('downloading', {
          error: null,
          totalBytes: body.sizeBytes,
          stage: 'download',
        });
        let lastProgress = 0,
          lastBytes = 0;
        const downloadResult = await downloadPackage(
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
                progress('downloading', {
                  downloadedBytes,
                  stage: 'download',
                });
              }
            },
          },
        );
        retain = false;
        // UI shows these verifying stages separately; Ed25519 on the release
        // envelope was already checked before the transfer started.
        progress('verifying', { stage: 'verify-archive' });
        installStage = 'verify-archive';
        const actualSha = downloadResult?.sha256 ?? sha256File(archive);
        if (actualSha !== body.sha256) throw new Error('package SHA mismatch');
        let payload;
        let manifest;
        // For deferred real-archive updates, validate manifest.json directly
        // from the embedded ZIP without unpacking tens of thousands of files
        // twice (applyPending unpacks once directly into the target volume).
        if (defer && extract === extractArchive && isZipSetupArchive(archive)) {
          progress('verifying', { stage: 'verify-package' });
          installStage = 'verify-package';
          manifest = readSetupManifest(archive);
          if (
            manifest.id !== body.packageId ||
            manifest.bundleVersion !== body.version
          )
            throw new Error('package ID/version mismatch');
        } else {
          progress('verifying', { stage: 'extract' });
          installStage = 'extract';
          payload = await extract(archive, join(temporary, 'unpacked'));
          progress('verifying', { stage: 'verify-package' });
          installStage = 'verify-package';
          manifest = await verifyPackageAsync(payload);
          if (
            manifest.id !== body.packageId ||
            manifest.bundleVersion !== body.version
          )
            throw new Error('package ID/version mismatch');
        }
        progress('verifying', { stage: 'check-release' });
        installStage = 'check-release';
        verified(pending); // expiry may have elapsed during the download
        // A withdrawn or replaced release must not be queued after a long transfer.
        retain = true;
        const latestResponse = await request(
          platformUrl(origin, latestPath(body.channel), binding),
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
          progress('ready', { available: null, applyRetry: null });
          return { restartRequired: true, queued: true };
        }
        progress('installing');
        installStage = 'installation';
        install({
          root,
          payload,
          dataHome: state.dataHome,
          smoke,
          transfer: unlock,
          progress: (stage) => {
            installStage = stage;
          },
        });
        finishUpdateAttempt(root, 'succeeded');
        progress('ready', { available: null });
        return {
          restartRequired: true,
          message: `更新完成。请关闭并重新打开 ${BRAND_NAME}。`,
        };
      } catch (error) {
        if (previousId)
          finishUpdateAttempt(
            root,
            updateWasRolledBack(root, previousId, installStage)
              ? 'rolled_back'
              : 'failed',
            installStage,
            error.code,
          );
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
      const streamHash = offset === 0 ? createHash('sha256') : null;
      let count = offset;
      try {
        for await (const chunk of response.body) {
          signal?.throwIfAborted();
          count += chunk.length;
          if (count > expected) throw new Error('package size mismatch');
          const bytes = Buffer.from(chunk);
          streamHash?.update(bytes);
          let written = 0;
          while (written < bytes.length)
            written += writeSync(fd, bytes, written, bytes.length - written);
          onProgress(count);
        }
        if (count !== expected) throw new Error('interrupted package download');
        return { sha256: streamHash ? streamHash.digest('hex') : null };
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
    changeUpdateState(root, (state) => ({
      ...state,
      applyRetry: null,
      phase: 'ready',
    }));
  } finally {
    release();
  }
}

/** Called only by the launcher after the old DSH has stopped (or before next launch). */
export async function applyPending(root, options = {}) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued)) return false;
  const ownSession = options.session ? null : openVersionSession(root);
  try {
    const transfer = transferLock(root);
    try {
      if (discardIncompletePending(root)) {
        options.onState?.({
          phase: 'failed',
          ...readUpdateState(root).failure,
        });
        return false;
      }
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
    verifiedCurrentManifest = null,
    onState = () => {},
  } = {},
) {
  const queued = join(root, 'desktop-pending');
  if (!existsSync(queued) || existsSync(join(queued, 'approval-required')))
    return false;
  let retain = true;
  let stage = 'verify-current';
  let identity;
  let previousId;
  const note = (next) => {
    if (stage === next) return;
    stage = next;
    changeUpdateState(root, (state) => ({
      ...state,
      stage,
      phase: 'installing',
      failure: null,
      error: null,
    }));
    logUpdate(root, { stage, phase: 'installing' });
    onState({ phase: 'installing', stage });
  };
  const retry = async (operation) => {
    for (let attempt = 0; ; attempt++) {
      try {
        return await operation();
      } catch (error) {
        if (
          !['EBUSY', 'EPERM', 'EACCES', 'ENOTEMPTY'].includes(error.code) ||
          attempt === 3
        )
          throw error;
        const retryState = {
          phase: 'retrying',
          stage,
          code: error.code,
          attempt: attempt + 1,
        };
        logUpdate(root, retryState);
        onState(retryState);
        await delay([150, 350, 750][attempt]);
      }
    }
  };
  const rejectInvalid = (operation, code = 'desktop.release_invalid') => {
    try {
      return operation();
    } catch (error) {
      // Native I/O failures can be retried. Untrusted bytes cannot.
      if (!error.code || !/^(E[A-Z]+)$/.test(error.code)) {
        retain = false;
        error.code ??= code;
      }
      throw error;
    }
  };
  const pendingInput = (operation) => {
    try {
      return operation();
    } catch (error) {
      if (['ENOENT', 'ENOTDIR'].includes(error.code)) {
        retain = false;
        error.code = 'desktop.pending_incomplete';
      }
      throw error;
    }
  };
  try {
    const saved = readUpdateState(root);
    const current = readState(root);
    previousId = current.current;
    const oldDir = versionDir(root, current.current);
    const old =
      verifiedCurrentManifest && verifiedCurrentManifest.id === current.current
        ? verifiedCurrentManifest
        : await verifyPackageAsync(oldDir, { trees: false });
    note('verify-release');
    const envelope = rejectInvalid(() =>
      JSON.parse(
        pendingInput(() => readFileSync(join(queued, 'release.json'), 'utf8')),
      ),
    );
    const body = rejectInvalid(() =>
      verifyRelease(envelope, {
        publicKey,
        now,
        currentVersion: old.bundleVersion,
        ...saved.channelWatermarks?.[saved.channel ?? 'stable'],
        channel: saved.channel ?? 'stable',
      }),
    );
    identity = releaseDigest(body);
    rememberUpdateAttempt(root, body.version, body.channel);
    // Recognize a committed transaction whose input survived the crash.
    if (
      current.current === body.packageId &&
      old.bundleVersion === body.version
    ) {
      retain = false;
      finishUpdateAttempt(root, 'succeeded');
      changeUpdateState(root, (state) => ({
        ...state,
        phase: 'idle',
        error: null,
        failure: null,
        stage: 'done',
        available: null,
        requiredRelease: null,
        applyRetry: null,
      }));
      return true;
    }
    const binding = JSON.parse(
      readFileSync(join(root, 'desktop-origin.json'), 'utf8'),
    );
    retain = true;
    note('check-release');
    const response = await request(
      platformUrl(binding.origin, latestPath(body.channel), binding),
      { redirect: 'error', signal: AbortSignal.timeout(15000) },
    );
    if (response.status === 404) {
      retain = false;
      throw Object.assign(new Error('queued release withdrawn'), {
        code: 'desktop.release_withdrawn',
      });
    }
    if (!response.ok) throw new Error('queued release check unavailable');
    const activeText = (await bounded(response, 32768)).toString('utf8');
    const active = rejectInvalid(() => JSON.parse(activeText));
    const activeBody = rejectInvalid(() =>
      verifyRelease(active, {
        publicKey,
        now,
        channel: body.channel,
      }),
    );
    if (
      releaseDigest(activeBody) !== releaseDigest(body) ||
      JSON.stringify(active.signature) !== JSON.stringify(envelope.signature)
    ) {
      retain = false;
      throw Object.assign(new Error('queued release replaced'), {
        code: 'desktop.release_replaced',
      });
    }
    if (
      saved.applyRetry?.manualRequired &&
      saved.applyRetry.digest === identity
    ) {
      changeUpdateState(root, (state) => ({
        ...state,
        phase: 'awaiting-retry',
        stage: saved.stage,
        failure: saved.failure,
        error: 'updateFailed',
      }));
      onState({ phase: 'awaiting-retry', ...saved.failure });
      return false;
    }
    note('verify-archive');
    const archive = stagedPackagePath(queued);
    const checkArchive = () => {
      if (pendingInput(() => sha256File(archive)) !== body.sha256) {
        retain = false;
        throw Object.assign(new Error('queued package SHA mismatch'), {
          code: 'desktop.archive_invalid',
        });
      }
    };
    const unpacked = join(queued, 'apply');
    const useRealExtract = extract === extractArchive;
    let usedReuse = false;
    let payload = await retry(async () => {
      note('verify-archive');
      checkArchive();
      note('extract');
      // The signed archive is the durable input. Rebuild scratch output after
      // interruption or failed extraction, while holding the transfer lock.
      rmSync(unpacked, { recursive: true, force: true });
      usedReuse = false;
      if (useRealExtract) {
        let reuse = null;
        try {
          const nextManifest = readSetupManifest(archive);
          const subtrees = findReusableSubtrees(oldDir, old, nextManifest);
          if (subtrees.length) reuse = { sourceDir: oldDir, subtrees };
        } catch {
          /* Fall back to standard full extraction if manifest peek fails. */
        }
        usedReuse = Boolean(reuse);
        return await extract(archive, unpacked, { reuse });
      }
      return await extract(archive, unpacked);
    });
    note('verify-package');
    let manifest;
    try {
      manifest = await verifyPackageAsync(payload);
    } catch (error) {
      if (usedReuse) {
        // An unchanged subtree in the old version may have been damaged on disk.
        // Fall back to a clean full extraction from the verified archive before
        // declaring the pending update package invalid.
        note('extract');
        rmSync(unpacked, { recursive: true, force: true });
        payload = await retry(async () => {
          checkArchive();
          rmSync(unpacked, { recursive: true, force: true });
          return await extract(archive, unpacked, { reuse: null });
        });
        note('verify-package');
        try {
          manifest = await verifyPackageAsync(payload);
        } catch (fullError) {
          if (!fullError.code || !/^(E[A-Z]+)$/.test(fullError.code)) {
            retain = false;
            fullError.code ??= 'desktop.package_invalid';
          }
          throw fullError;
        }
      } else {
        if (!error.code || !/^(E[A-Z]+)$/.test(error.code)) {
          retain = false;
          error.code ??= 'desktop.package_invalid';
        }
        throw error;
      }
    }
    if (
      manifest.id !== body.packageId ||
      manifest.bundleVersion !== body.version
    ) {
      retain = false;
      throw Object.assign(new Error('queued package ID mismatch'), {
        code: 'desktop.package_invalid',
      });
    }
    note('install');
    let installAttempt = 0;
    await retry(async () => {
      const retrying = installAttempt++ > 0;
      if (
        useRealExtract &&
        !existsSync(versionDir(root, manifest.id)) &&
        (retrying || !existsSync(join(payload, 'manifest.json')))
      ) {
        checkArchive();
        rmSync(unpacked, { recursive: true, force: true });
        payload = await extract(archive, unpacked, { reuse: null });
        await verifyPackageAsync(payload);
      }
      return install({
        root,
        payload,
        dataHome: current.dataHome,
        smoke,
        session,
        transfer,
        movePayload: useRealExtract,
        preverifiedManifest: useRealExtract ? manifest : null,
        progress: (step) => note(step),
      });
    });
    retain = false;
    finishUpdateAttempt(root, 'succeeded');
    changeUpdateState(root, (state) => ({
      ...state,
      phase: 'idle',
      error: null,
      available: null,
      requiredRelease: null,
      failure: null,
      stage: 'done',
      applyRetry: null,
    }));
    return true;
  } catch (error) {
    finishUpdateAttempt(
      root,
      updateWasRolledBack(root, previousId, stage) ? 'rolled_back' : 'failed',
      stage,
      error.code,
    );
    const failure = {
      stage,
      code: /^(?:E[A-Z_]+|desktop\.[a-z_]+)$/.test(error.code ?? '')
        ? error.code
        : 'updateFailed',
      pathCategory: ['verify-release', 'check-release'].includes(stage)
        ? 'release'
        : ['verify-archive', 'extract', 'verify-package'].includes(stage)
          ? 'pending'
          : ['profile', 'smoke'].includes(stage)
            ? 'data'
            : 'installation',
    };
    error.updateFailure = failure;
    logUpdate(root, failure);
    const updated = changeUpdateState(root, (state) => {
      let applyRetry = retain ? state.applyRetry : null;
      const deterministic =
        stage === 'smoke' ||
        (stage === 'extract' && !/^E[A-Z]+$/.test(error.code ?? ''));
      if (retain && identity && deterministic) {
        const failures =
          (applyRetry?.digest === identity ? applyRetry.failures : 0) + 1;
        applyRetry = {
          digest: identity,
          failures,
          manualRequired: failures >= 3,
        };
      }
      return {
        ...state,
        phase: applyRetry?.manualRequired ? 'awaiting-retry' : 'failed',
        error: 'updateFailed',
        stage,
        failure,
        applyRetry,
      };
    });
    onState({ phase: updated.phase, ...failure });
    throw error;
  } finally {
    if (!retain) {
      try {
        rmSync(queued, {
          recursive: true,
          force: true,
          maxRetries: 3,
          retryDelay: 200,
        });
      } catch (error) {
        // Cleanup must not turn a committed update into an apparent rollback,
        // or hide the original trust/installation error. A leftover queue is
        // checked again on the next launch, including the committed-ID guard.
        const cleanup = {
          phase: 'cleanup',
          stage: 'cleanup',
          code: /^E[A-Z]+$/.test(error.code ?? '')
            ? error.code
            : 'updateFailed',
          pathCategory: 'pending',
        };
        logUpdate(root, cleanup);
        onState(cleanup);
      }
    }
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
