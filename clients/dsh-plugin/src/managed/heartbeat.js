// @ts-nocheck
import { PROTOCOL_VERSION, protocolInfo, userAgent } from './protocol.js';
import { checkProtocol } from './protocol.js';

/**
 * @param {{
 *   platformUrl: string,
 *   getInstanceToken: () => string | Promise<string>,
 *   pluginVersion: string,
 *   dshVersion: string,
 *   activity: ReturnType<import('./activity.js').createActivityProbe>,
 *   fetchImpl?: typeof fetch,
 *   intervalMs?: number,
 *   debounceMs?: number,
 *   healthOk?: () => boolean,
 *   onProtocol?: (info: { current: number, minSupported: number }) => void,
 *   refreshActivity?: () => void | Promise<void>,
 * }} opts
 */
export function createHeartbeatClient(opts) {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const debounceMs = opts.debounceMs ?? 2_000;
  let timer;
  let debounce;
  let stopped = false;
  let nextHeartbeatSeconds = Math.round((opts.intervalMs ?? 30_000) / 1000);
  let backoffSeconds = 0;

  async function send() {
    if (stopped) return { ok: false, status: 0, skipped: true };
    try {
      if (opts.refreshActivity) await opts.refreshActivity();
      const token = await opts.getInstanceToken();
      const activity = opts.activity.snapshot();
      const problems = activity.unknownFields.map(
        (f) => `activity.${f}.unknown`,
      );
      const healthOk =
        (opts.healthOk ? opts.healthOk() : true) && activity.allKnown;
      const body = {
        pluginVersion: opts.pluginVersion,
        dshVersion: opts.dshVersion,
        activity: {
          runningTurns: activity.runningTurns,
          openTerminals: activity.openTerminals,
          activeImBots: activity.activeImBots,
          connectedClients: activity.connectedClients,
          ...(activity.lastInteractionAt
            ? { lastInteractionAt: activity.lastInteractionAt }
            : {}),
        },
        health: {
          ok: healthOk,
          ...(problems.length ? { problems } : {}),
        },
      };
      const url = new URL(
        '/api/workspace/v1/instance/heartbeat',
        opts.platformUrl,
      );
      const res = await fetchImpl(url, {
        method: 'POST',
        redirect: 'error',
        headers: {
          authorization: `Bearer ${token}`,
          'content-type': 'application/json',
          'auriga-protocol': String(PROTOCOL_VERSION),
          'user-agent': userAgent(opts.pluginVersion, opts.dshVersion),
        },
        body: JSON.stringify(body),
      });
      if (res.status === 426) {
        const problem = await res.json().catch(() => ({}));
        const checked = checkProtocol(0);
        opts.onProtocol?.(
          problem?.params ?? {
            current: PROTOCOL_VERSION,
            minSupported: checked.ok ? 1 : (checked.params?.minSupported ?? 1),
          },
        );
        backoffSeconds = Math.min(120, Math.max(5, (backoffSeconds || 5) * 2));
        return { ok: false, status: 426, problem };
      }
      if (!res.ok) {
        await res.body?.cancel?.().catch?.(() => {});
        backoffSeconds = Math.min(120, Math.max(5, (backoffSeconds || 5) * 2));
        return { ok: false, status: res.status };
      }
      const data = await res.json().catch(() => ({}));
      if (data?.protocol) opts.onProtocol?.(data.protocol);
      if (typeof data?.nextHeartbeatSeconds === 'number')
        nextHeartbeatSeconds = data.nextHeartbeatSeconds;
      backoffSeconds = 0;
      return { ok: true, data };
    } catch {
      backoffSeconds = Math.min(120, Math.max(5, (backoffSeconds || 5) * 2));
      return { ok: false, status: 0, error: true };
    }
  }

  const schedule = () => {
    clearTimeout(timer);
    const delaySec =
      backoffSeconds > 0 ? backoffSeconds : nextHeartbeatSeconds || 30;
    timer = setTimeout(() => {
      void send().finally(() => {
        if (!stopped) schedule();
      });
    }, delaySec * 1000);
    if (typeof timer.unref === 'function') timer.unref();
  };

  const onActivityChange = () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      void send();
    }, debounceMs);
    if (typeof debounce.unref === 'function') debounce.unref();
  };

  return {
    send,
    start() {
      stopped = false;
      opts.activity.onChange(onActivityChange);
      schedule();
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
      clearTimeout(debounce);
    },
    protocolInfo,
  };
}
