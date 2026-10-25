/**
 * In-process FIFO mutex per key. It serializes work inside one plugin
 * process; cross-process exclusion comes from the ledger single-writer lock.
 */
export function createKeyedMutex() {
  /** @type {Map<string, Promise<void>>} */
  const tails = new Map();

  return {
    /**
     * @template T
     * @param {string} key
     * @param {() => Promise<T>} fn
     * @returns {Promise<T>}
     */
    async run(key, fn) {
      const previous = tails.get(key) ?? Promise.resolve();
      /** @type {() => void} */
      let release = () => {};
      const current = new Promise((resolve) => {
        release = () => resolve(undefined);
      });
      const tail = previous.then(() => current);
      tails.set(key, tail);
      await previous;
      try {
        return await fn();
      } finally {
        release();
        if (tails.get(key) === tail) tails.delete(key);
      }
    },
    /** @param {string} key */
    busy(key) {
      return tails.has(key);
    },
  };
}
