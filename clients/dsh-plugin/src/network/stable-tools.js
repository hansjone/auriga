/** Keep published schemas stable for one authenticated host lifetime. A retired
 * connection only removes its delegate; it can never authorize a new call.
 * @param {{register:(definition:any)=>()=>void}} tools */
export function createStableNetworkTools(tools) {
  /** @type {Map<string,{delegate:any,remove:()=>void}>} */
  const entries = new Map();
  let closed = false;
  /** @param {string} name */
  const unavailable = (name) =>
    name === 'auriga_network_join'
      ? closed
        ? { status: 'unavailable', retryable: false }
        : {
            status: 'unavailable',
            reason: 'network.overloaded',
            retryable: true,
          }
      : {
          status: 'error',
          code: closed ? 'network.unauthorized' : 'network.overloaded',
          retryable: !closed,
        };
  return {
    /** @param {any} definition */
    register(definition) {
      if (closed) throw new Error('network tools closed');
      const name = definition.name;
      if (!['auriga_network_join', 'auriga_send_message'].includes(name))
        throw new Error('unexpected network tool');
      let entry = entries.get(name);
      if (!entry) {
        entry = { delegate: undefined, remove: () => {} };
        const owner = entry;
        entry.remove = tools.register({
          ...definition,
          async execute(
            /** @type {unknown} */ input,
            /** @type {any} */ execution,
          ) {
            const delegate = owner.delegate;
            if (closed || !delegate) return unavailable(name);
            try {
              const result = await delegate.execute(input, execution);
              return closed || owner.delegate !== delegate
                ? unavailable(name)
                : result;
            } catch (error) {
              if (closed || owner.delegate !== delegate)
                return unavailable(name);
              throw error;
            }
          },
        });
        entries.set(name, entry);
      }
      const lease = definition;
      entry.delegate = lease;
      return () => {
        if (entry.delegate === lease) entry.delegate = undefined;
      };
    },
    close() {
      if (closed) return;
      closed = true;
      for (const entry of entries.values()) {
        entry.delegate = undefined;
        entry.remove();
      }
      entries.clear();
    },
  };
}
