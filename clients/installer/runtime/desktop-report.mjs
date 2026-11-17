/* global AbortController, setInterval, clearInterval, clearTimeout */
import { randomUUID } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { readState, versionDir } from './core.mjs';
import { changeUpdateState, readUpdateState } from './update-state.mjs';
import { updateOrigin } from './platform-origin.mjs';

const stages = new Set([
  'verify-current',
  'verify-release',
  'check-release',
  'verify-input',
  'verify-archive',
  'extract',
  'verify-package',
  'install',
  'profile',
  'smoke',
  'download',
  'verify',
  'installation',
  'copy',
  'verify-copy',
  'commit',
]);
const reasons = new Set([
  'updateFailed',
  'ENOENT',
  'ENOTDIR',
  'EBUSY',
  'EPERM',
  'EACCES',
  'ENOTEMPTY',
  'desktop.pending_incomplete',
  'desktop.release_invalid',
  'desktop.release_withdrawn',
  'desktop.release_replaced',
  'desktop.archive_invalid',
  'desktop.package_invalid',
  'desktop.smoke_failed',
  'desktop.extract_failed',
  'desktop.install_cancelled',
  'desktop.in_use',
]);
const listeners = new Map();

/** Telemetry persistence must never replace an installation or update outcome. */
export function rememberUpdateAttempt(root, version, channel, fresh = false) {
  try {
    changeUpdateState(root, (state) => ({
      ...state,
      lastAttempt:
        !fresh && state.lastAttempt?.targetVersion === version
          ? state.lastAttempt
          : { attemptId: randomUUID(), targetVersion: version, channel },
    }));
  } catch {
    /* retry from the next durable updater state */
  }
}
export function finishUpdateAttempt(root, result, stage = null, code = null) {
  try {
    changeUpdateState(root, (state) =>
      state.lastAttempt
        ? {
            ...state,
            lastAttempt: {
              ...state.lastAttempt,
              result,
              stage:
                result === 'succeeded'
                  ? null
                  : stages.has(stage)
                    ? stage
                    : 'installation',
              reasonCode:
                result === 'succeeded'
                  ? null
                  : reasons.has(code)
                    ? code
                    : 'updateFailed',
            },
          }
        : undefined,
    );
  } catch {
    /* diagnostic failure cannot fail an update */
  }
  for (const notify of listeners.get(root) ?? []) notify();
}

export function createDesktopReporter({
  root,
  getAuth,
  fetcher = globalThis.fetch,
  schedule = setInterval,
  unschedule = clearInterval,
}) {
  let busy = false,
    closed = false,
    controller,
    timer,
    lastSent = '';
  const auth = getAuth();
  function payload() {
    const installed = readState(root);
    if (!installed) return null;
    const version = JSON.parse(
      readFileSync(
        join(versionDir(root, installed.current), 'manifest.json'),
        'utf8',
      ),
    ).bundleVersion;
    const state = readUpdateState(root);
    const facts = {
      clientVersion: version,
      channel: state.channel ?? 'stable',
      ...(state.lastAttempt?.result
        ? {
            lastUpdate: Object.fromEntries(
              [
                'attemptId',
                'targetVersion',
                'result',
                'stage',
                'reasonCode',
              ].map((key) => [key, state.lastAttempt[key]]),
            ),
          }
        : {}),
    };
    let report;
    changeUpdateState(root, (current) => {
      const previous = current.desktopReport;
      if (JSON.stringify(previous?.facts) === JSON.stringify(facts)) {
        report = {
          installationId: previous.installationId,
          sequence: previous.sequence,
          ...facts,
        };
        return;
      }
      const installationId = previous?.installationId ?? randomUUID();
      const sequence = (previous?.sequence ?? 0) + 1;
      report = { installationId, sequence, ...facts };
      return { ...current, desktopReport: { installationId, sequence, facts } };
    });
    return report;
  }
  async function tick() {
    if (busy || closed) return;
    busy = true;
    try {
      const currentAuth = getAuth();
      if (
        currentAuth?.snapshot().state !== 'authenticated' ||
        !currentAuth.broker
      )
        return;
      const broker = currentAuth.broker;
      const context = broker.guard();
      const identity = context.identity;
      const binding = JSON.parse(
        readFileSync(join(root, 'desktop-origin.json'), 'utf8'),
      );
      updateOrigin(binding.origin, binding);
      if (broker.platform.config.platformOrigin !== binding.origin) return;
      const body = payload();
      if (!body) return;
      const stamp = JSON.stringify([
        identity.tenantId,
        identity.memberId,
        identity.deviceId,
        body,
      ]);
      if (stamp === lastSent) return;
      const token = await broker.tokenFor('auriga', context);
      broker.assert(context);
      controller = new AbortController();
      const timeout = setTimeout(() => controller?.abort(), 5000);
      timeout.unref?.();
      try {
        const response = await fetcher(
          new URL('/api/iam/v1/me/desktop-report', binding.origin).href,
          {
            method: 'POST',
            credentials: 'omit',
            redirect: 'error',
            signal: controller.signal,
            headers: {
              Authorization: `Bearer ${token}`,
              'Content-Type': 'application/json',
            },
            body: JSON.stringify(body),
          },
        );
        await response.body?.cancel();
        broker.assert(context);
        if (response.ok && !closed) lastSent = stamp;
      } finally {
        clearTimeout(timeout);
        controller = undefined;
      }
    } catch {
      /* offline/auth changes retry silently on the next tick */
    } finally {
      busy = false;
    }
  }
  const changed = () => {
    controller?.abort();
    void tick();
  };
  return {
    tick,
    start() {
      const active = listeners.get(root) ?? new Set();
      active.add(changed);
      listeners.set(root, active);
      auth?.broker?.on('changed', changed);
      void tick();
      timer = schedule(() => void tick(), 15000);
      timer.unref?.();
      return this;
    },
    close() {
      closed = true;
      unschedule(timer);
      controller?.abort();
      listeners.get(root)?.delete(changed);
      if (!listeners.get(root)?.size) listeners.delete(root);
      auth?.broker?.off('changed', changed);
      // Durable facts are retried after the next login if shutdown interrupts delivery.
    },
  };
}

export function updateWasRolledBack(root, previousId, stage) {
  try {
    return (
      ['profile', 'smoke'].includes(stage) &&
      readState(root)?.current === previousId &&
      !existsSync(join(root, 'txn.json'))
    );
  } catch {
    return false;
  }
}
