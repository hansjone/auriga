import { parseStrictJson } from './trust/jcs.js';

/** Secret-free network transport. C owns NATS, ticket refresh and signed frame
 * verification; only the originating native session can open/send/submit.
 * This port does not invent first-install or durable READY authority.
 * @param {{broker:any,contexts:any}} options */
export function createBrokerNetworkPort({ broker, contexts }) {
  /** @type {Map<string,any>} */ const connections = new Map();
  let closed = false;
  const fail = () => {
    throw Object.assign(new Error('network.unauthorized'), {
      code: 'network.unauthorized',
    });
  };
  async function open(
    /** @type {any} */ execution,
    /** @type {(bytes:Buffer)=>void} */ receive,
  ) {
    if (closed) return fail();
    const check = broker.state.fence();
    const tool = await contexts.tool(execution);
    await contexts.finish(tool.executionContextId);
    check();
    const hostSessionId = tool.hostSessionId,
      source = await contexts.session(hostSessionId);
    const old = connections.get(hostSessionId);
    if (old && old.context === source.executionContextId) return old;
    const result = await broker.call('network_open', {
      executionContextId: source.executionContextId,
    });
    check();
    const streamId = result.body.streamId;
    let retired = false,
      sequence = 0;
    const current = () => {
      check();
      if (closed || retired || connections.get(hostSessionId) !== port)
        return fail();
    };
    const port = {
      context: source.executionContextId,
      hostSessionId,
      async send(/** @type {Uint8Array} */ bytes) {
        current();
        if (bytes.byteLength > 65536) return fail();
        parseStrictJson(Buffer.from(bytes));
        const result = await broker.call('network_send', {
          streamId,
          frameWire: Buffer.from(bytes).toString('base64'),
        });
        current();
        return result.body;
      },
      async close() {
        if (retired) return;
        retired = true;
        if (connections.get(hostSessionId) === port)
          connections.delete(hostSessionId);
        await broker
          .streamCommand({ streamId, action: 'cancel' })
          .catch(() => {});
      },
    };
    connections.set(hostSessionId, port);
    try {
      await broker.subscribeStream(streamId, (/** @type {any} */ event) => {
        try {
          current();
          if (!event || event.sequence !== sequence++ || event.type !== 'data')
            throw new Error('network unavailable');
          const bytes = Buffer.from(event.data, 'base64');
          if (bytes.length > 65536 || bytes.toString('base64') !== event.data)
            return fail();
          parseStrictJson(bytes);
          receive(bytes);
          current();
          void broker
            .streamCommand({
              streamId,
              action: 'credit',
              creditBytes: bytes.length,
            })
            .catch(() => port.close());
        } catch {
          void port.close();
        }
      });
      await broker.streamCommand({
        streamId,
        action: 'credit',
        creditBytes: 65536,
      });
      current();
      return port;
    } catch (error) {
      await port.close();
      throw error;
    }
  }
  return {
    open,
    async submit(
      /** @type {any} */ execution,
      /** @type {Uint8Array} */ request,
    ) {
      if (closed || request.byteLength > 65536) return fail();
      parseStrictJson(Buffer.from(request));
      const check = broker.state.fence(),
        context = await contexts.tool(execution);
      try {
        const result = await broker.call('network_submit', {
          executionContextId: context.executionContextId,
          requestWire: Buffer.from(request).toString('base64'),
        });
        check();
        return result.body;
      } finally {
        await contexts.finish(context.executionContextId);
      }
    },
    close() {
      closed = true;
      for (const port of connections.values()) void port.close();
      connections.clear();
    },
  };
}
