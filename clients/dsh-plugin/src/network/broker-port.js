import { parseStrictJson } from './trust/jcs.js';

/** Secret-free network transport. C owns NATS, ticket refresh and signed frame
 * verification; only the originating native session can open/send/submit.
 * This port does not invent first-install or durable READY authority.
 * @param {{broker:any,contexts:any}} options */
export function createBrokerNetworkPort({ broker, contexts }) {
  /** @type {Map<string,any>} */ const connections = new Map();
  /** @type {Map<string,Promise<any>>} */ const opening = new Map();
  const unsubscribe = broker.state.subscribeInvalidation(() => {
    for (const port of connections.values()) void port.close();
  });
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
    return openSession(tool.hostSessionId, receive);
  }
  async function openSession(
    /** @type {string} */ hostSessionId,
    /** @type {(bytes:Buffer)=>void} */ receive,
  ) {
    if (closed) return fail();
    const check = broker.state.fence();
    const source = await contexts.session(hostSessionId);
    check();
    if (
      closed ||
      !contexts.validSession(hostSessionId, source.executionContextId)
    )
      return fail();
    const old = connections.get(hostSessionId);
    if (old && old.context === source.executionContextId) {
      old.listen(receive);
      return old;
    }
    await old?.close();
    let pending = opening.get(hostSessionId);
    if (!pending) {
      pending = establish(hostSessionId, source, check, receive);
      opening.set(hostSessionId, pending);
    }
    try {
      const port = await pending;
      check();
      if (port.context !== source.executionContextId) return fail();
      port.listen(receive);
      return port;
    } finally {
      if (opening.get(hostSessionId) === pending) opening.delete(hostSessionId);
    }
  }
  async function establish(
    /** @type {string} */ hostSessionId,
    /** @type {any} */ source,
    /** @type {()=>void} */ check,
    /** @type {(bytes:Buffer)=>void} */ firstReceiver,
  ) {
    const snapshot = broker.state.current();
    const result = await broker.call('network_open', {
      executionContextId: source.executionContextId,
    });
    const streamId = result.body.streamId;
    let registration = result.body.registration;
    try {
      check();
      const binding = result.body.binding;
      if (
        binding?.executionContextId !== source.executionContextId ||
        binding?.shellEpoch !== snapshot?.shellEpoch ||
        binding?.revision !== snapshot?.revision ||
        registration?.tenant !== snapshot?.identity?.tenantId ||
        registration?.device !== snapshot?.identity?.deviceId ||
        !/^[1-9][0-9]*$/.test(registration?.realm ?? '') ||
        !/^[1-9][0-9]*$/.test(registration?.node ?? '') ||
        BigInt(registration.realm) > 18446744073709551615n ||
        BigInt(registration.node) > 18446744073709551615n ||
        registration.systemAddress !==
          `aur1:${BigInt(registration.realm).toString(16)}.${BigInt(registration.node).toString(16)}.0`
      )
        return fail();
      if (
        closed ||
        !contexts.validSession(hostSessionId, source.executionContextId)
      )
        return fail();
    } catch (error) {
      await broker
        .streamCommand({ streamId, action: 'cancel' })
        .catch(() => {});
      throw error;
    }
    /** @type {Set<(bytes:Buffer)=>void>} */ const receivers = new Set([
      firstReceiver,
    ]);
    /** @type {Set<()=>void>} */ const closedListeners = new Set();
    let retired = false,
      sequence = 0;
    const current = () => {
      check();
      if (
        closed ||
        retired ||
        !contexts.validSession(hostSessionId, source.executionContextId) ||
        connections.get(hostSessionId) !== port
      )
        return fail();
    };
    const port = {
      context: source.executionContextId,
      hostSessionId,
      takeRegistration() {
        current();
        const result = { ...registration };
        registration = { ...registration, created: false };
        return result;
      },
      onClosed(/** @type {()=>void} */ listener) {
        current();
        closedListeners.add(listener);
        return () => closedListeners.delete(listener);
      },
      listen(/** @type {(bytes:Buffer)=>void} */ receive) {
        current();
        receivers.add(receive);
      },
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
        registration = undefined;
        receivers.clear();
        for (const listener of closedListeners) {
          try {
            listener();
          } catch {
            // One consumer cannot retain the retired shell stream.
          }
        }
        closedListeners.clear();
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
          for (const receive of receivers) receive(bytes);
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
      // Credit replies carry the next downstream event. Waiting here would
      // prevent the caller from sending the READY that produces that event.
      void broker
        .streamCommand({
          streamId,
          action: 'credit',
          creditBytes: 65536,
        })
        .catch(() => port.close());
      current();
      return port;
    } catch (error) {
      await port.close();
      throw error;
    }
  }
  return {
    open,
    openSession,
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
        if (closed || !contexts.valid(context.executionContextId))
          return fail();
        return result.body;
      } finally {
        await contexts.finish(context.executionContextId);
      }
    },
    close() {
      closed = true;
      unsubscribe();
      for (const port of connections.values()) void port.close();
      connections.clear();
    },
  };
}
