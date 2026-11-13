/** Installation-owned refresh port; no URL or timer is selected by a frame.
 * Errors retry without extending the signed deadline. @param {{refresh:()=>Promise<void>,expiresAt:()=>number,now:()=>number,onExpired:()=>void}} options */
export function startTrustRefresh(options) {
  let stopped = false;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let timer;
  async function tick() {
    if (stopped) return;
    try {
      await options.refresh();
    } catch {
      /* Keep the original expiry. */
    }
    if (stopped) return;
    if (options.now() >= options.expiresAt()) {
      options.onExpired();
      return;
    }
    timer = setTimeout(
      tick,
      Math.max(1, Math.min(60000, options.expiresAt() - options.now())),
    );
    timer.unref?.();
  }
  timer = setTimeout(
    tick,
    Math.max(1, Math.min(60000, options.expiresAt() - options.now())),
  );
  timer.unref?.();
  return () => {
    stopped = true;
    clearTimeout(timer);
  };
}
