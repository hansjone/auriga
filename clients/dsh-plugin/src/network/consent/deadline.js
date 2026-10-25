import { setTimeout as delay } from 'node:timers/promises';

/** Bound trusted callbacks; late completion never receives a new permit.
 * @template T @param {(signal: AbortSignal) => Promise<T>} callback @param {number} ms */
export async function bounded(callback, ms) {
  const controller = new AbortController();
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => {
      controller.abort();
      reject(new Error('authorization callback timeout'));
    }, ms);
  });
  try {
    return await Promise.race([
      Promise.resolve().then(() => callback(controller.signal)),
      /** @type {Promise<never>} */ (timeout),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

/** A fixed response window for source-facing APIs, independent of policy state.
 * A queue/disk/port failure returns a generic error in the same window; it is
 * never presented as a successful durable submission.
 * @param {Promise<unknown>} work @param {number} ms @param {boolean} [privateErrors] */
export async function opaqueResponse(work, ms, privateErrors = false) {
  let settled = false;
  let failed = false;
  /** @type {unknown} */ let failure;
  void work.then(
    () => {
      settled = true;
    },
    (error) => {
      settled = true;
      failed = true;
      failure = error;
    },
  );
  await delay(ms);
  if (!settled || failed) {
    if (privateErrors && failed) throw failure;
    throw new Error('consent intake unavailable');
  }
  return { status: 'request_submitted' };
}
