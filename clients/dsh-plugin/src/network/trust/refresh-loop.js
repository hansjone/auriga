/** Signed expiry is a hard ceiling, including after a failed refresh.
 * @param {number} expires @param {number} now @param {number} failures @param {()=>number} [random] */
export function trustRefreshDelay(
  expires,
  now,
  failures = 0,
  random = Math.random,
) {
  const interval = failures
    ? Math.min(3600000, 30000 * 2 ** Math.min(failures - 1, 7))
    : 12 * 3600000;
  return Math.max(
    1,
    Math.min(expires - now, interval * (0.9 + random() * 0.2)),
  );
}
/** Installation-owned refresh port; no URL or timer is selected by a frame.
 * @param {{refresh:()=>Promise<void>,expiresAt:()=>number,now:()=>number,onExpired:()=>void}} options */
export function startTrustRefresh(options) {
  let stopped = false,
    failures = 0;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let timer;
  const schedule = () => {
    timer = setTimeout(
      tick,
      trustRefreshDelay(options.expiresAt(), options.now(), failures),
    );
    timer.unref?.();
  };
  async function tick() {
    if (stopped) return;
    try {
      await options.refresh();
      failures = 0;
    } catch {
      failures++;
    }
    if (stopped) return;
    if (options.now() >= options.expiresAt()) {
      options.onExpired();
      return;
    }
    schedule();
  }
  schedule();
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}
