/** Responses belong to a local browser session, independently of the shared
 * native Peer and keep-alive socket. Closing one must not cancel another.
 * @param {(request:import('node:http').IncomingMessage)=>{key:string}|null} resolve */
export function createHttpResponseTracker(resolve) {
  /** @type {Map<string,Set<import('node:http').ServerResponse>>} */
  const responses = new Map();
  return {
    /** @param {import('node:http').IncomingMessage} request @param {import('node:http').ServerResponse} response */
    bind(request, response) {
      const session = resolve(request);
      if (!session || response.destroyed || response.writableEnded)
        return false;
      const owned = responses.get(session.key) ?? new Set();
      responses.set(session.key, owned);
      if (owned.has(response)) return true;
      owned.add(response);
      const release = () => {
        response.off('close', release);
        response.off('finish', release);
        owned.delete(response);
        if (owned.size === 0 && responses.get(session.key) === owned)
          responses.delete(session.key);
      };
      response.once('close', release);
      response.once('finish', release);
      return true;
    },
    /** @param {string} key */
    close(key) {
      const owned = responses.get(key);
      if (!owned) return;
      responses.delete(key);
      for (const response of owned) response.destroy();
    },
  };
}
