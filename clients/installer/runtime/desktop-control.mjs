/**
 * One-shot commands for the Windows tray (and support): installation facts,
 * update check/download and the update preference. They reuse the same signed
 * updater as the plugin settings page; nothing here widens trust.
 */
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { readState, versionDir } from './core.mjs';
import { approvePending, createUpdater, idleRequest } from './update.mjs';
import {
  bundledDefaultsPath,
  createControlledFetch,
  createProxyConfigStore,
} from './network-proxy.mjs';

const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));
const optionalJson = (file) => (existsSync(file) ? readJson(file) : null);

/** Display-only summary: no signature material, tokens or envelopes. */
export function desktopInfo(root) {
  const state = readState(root);
  if (!state) throw new Error('desktop installation missing');
  const manifest = readJson(
    join(versionDir(root, state.current), 'manifest.json'),
  );
  const binding = optionalJson(join(root, 'desktop-origin.json'));
  const update = optionalJson(join(root, 'desktop-update.json')) ?? {};
  const envelopeVersion = (envelope) =>
    typeof envelope?.version === 'string' ? envelope.version : null;
  return {
    product: manifest.product,
    version: manifest.bundleVersion,
    packageId: manifest.id,
    previous: state.previous ?? null,
    components: {
      dsh: manifest.upstream?.version ?? null,
      node: manifest.upstream?.nodeVersion ?? null,
      plugin: manifest.plugin?.version ?? null,
      pnpm: manifest.pnpm?.version ?? null,
    },
    dataHome: state.dataHome,
    logs: join(state.dataHome, 'auriga', 'logs'),
    platformOrigin: binding?.origin ?? null,
    accessMode: binding?.accessMode ?? (binding ? 'domain' : null),
    updatesConfigured: Boolean(
      binding?.origin &&
      existsSync(
        join(
          versionDir(root, state.current),
          'auriga/runtime/release-public-key.txt',
        ),
      ),
    ),
    update: {
      enabled: update.enabled !== false,
      channel: update.channel ?? 'stable',
      phase: update.phase ?? 'idle',
      available: envelopeVersion(update.available),
      required: Boolean(update.requiredRelease),
      downloadedBytes: update.downloadedBytes ?? 0,
      totalBytes: update.totalBytes ?? 0,
      error: update.error ?? null,
      stage: update.stage ?? null,
      failure: update.failure ?? null,
      manualRetryRequired: update.applyRetry?.manualRequired === true,
      applyFailures: update.applyRetry?.failures ?? 0,
      lastCheckedAt: update.lastCheckedAt ?? null,
      restartRequired: existsSync(join(root, 'desktop-pending')),
    },
  };
}

/** Updater bound to the installation's verified origin and packaged public key. */
export function desktopUpdater(
  root,
  { onState, request, publicKey, requireApproval } = {},
) {
  const binding = optionalJson(join(root, 'desktop-origin.json'));
  if (!binding?.origin)
    throw Object.assign(new Error('desktop platform address not configured'), {
      code: 'desktop.update_unconfigured',
    });
  let controlledFetcher;
  if (!request) {
    const state = readState(root);
    if (state?.dataHome) {
      const store = createProxyConfigStore({
        dataHome: state.dataHome,
        defaultsFile: state.current
          ? bundledDefaultsPath(versionDir(root, state.current))
          : null,
      });
      controlledFetcher = createControlledFetch(() => store.get().config);
    }
  }
  return createUpdater({
    root,
    origin: binding.origin,
    request:
      request ??
      idleRequest(
        binding.origin,
        controlledFetcher ? { fetcher: controlledFetcher } : {},
      ),
    ...(publicKey ? { publicKey } : {}),
    onState,
    requireApproval,
  });
}

const summary = (status) => ({
  enabled: status.enabled,
  channel: status.channel,
  available:
    typeof status.available?.version === 'string'
      ? status.available.version
      : null,
  required: status.required,
  phase: status.phase,
  downloadedBytes: status.downloadedBytes,
  totalBytes: status.totalBytes,
  error: status.error,
  stage: status.stage,
  failure: status.failure,
  manualRetryRequired: status.manualRetryRequired,
  applyFailures: status.applyFailures,
  restartRequired: status.restartRequired,
});

/** Tray commands for checking, preparing and approving signed updates. */
export async function updateCommand(
  root,
  action,
  {
    enabled,
    print = (value) => console.log(JSON.stringify(value)),
    ...options
  } = {},
) {
  if (action === 'approve') {
    approvePending(root);
    print({ approved: true });
    return;
  }
  if (action === 'configure') {
    const updater = desktopUpdater(root, options);
    print(summary(updater.configure(enabled)));
    return;
  }
  let last = 0;
  let lastKey = '';
  const updater = desktopUpdater(root, {
    ...options,
    requireApproval: action === 'download',
    onState: (state) => {
      if (action !== 'download') return;
      const key = `${state.phase ?? ''}\0${state.stage ?? ''}`;
      const now = Date.now();
      // Byte progress stays throttled; phase/stage changes always surface.
      if (key === lastKey && now - last < 200) return;
      last = now;
      lastKey = key;
      print({
        progress: true,
        phase: state.phase ?? 'idle',
        stage: state.stage ?? null,
        downloadedBytes: state.downloadedBytes ?? 0,
        totalBytes: state.totalBytes ?? 0,
      });
    },
  });
  if (action === 'check') {
    print(summary(await updater.check()));
    return;
  }
  if (action !== 'download') throw new Error('unknown update action');
  if (updater.status().restartRequired) {
    print({ ...summary(updater.status()), queued: true });
    return;
  }
  if (!updater.status().available) await updater.check();
  if (!updater.status().available) {
    print(summary(updater.status()));
    return;
  }
  const result = await updater.update();
  print({ ...summary(updater.status()), queued: Boolean(result.queued) });
}
