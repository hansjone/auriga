import { randomUUID, createHash } from 'node:crypto';
import { createBrokerNetworkPort } from './broker-port.js';
import { networkInstallation } from './installation.js';
import { parseStrictJson } from './trust/jcs.js';

/** Native composition only. The control session is created by the public host
 * controller and registered through the same session checks as native tools.
 * @param {{broker:any,contexts:any,ctx:any,issuer:string,origin:string,installation:any,loadInstallation?:(scope:any)=>Promise<any>}} options */
export function createBrokerNetworkProvider({
  broker,
  contexts,
  ctx,
  issuer,
  origin,
  installation: input,
  loadInstallation,
}) {
  const installation = networkInstallation(input);
  const bridge = createBrokerNetworkPort({ broker, contexts });
  /** @type {string|undefined} */ let controlSessionId;
  /** @type {string|undefined} */ let controlOwner;
  return {
    kind: /** @type {const} */ ('device-connection'),
    async prepare(/** @type {{signal:AbortSignal}} */ { signal }) {
      const check = broker.state.fence();
      const snapshot = broker.state.current();
      const identity = snapshot.identity;
      const installed = await loadInstallation?.({
        tenant_id: identity.tenantId,
        target_id: identity.deviceId,
        realm: installation.realm,
      });
      check();
      signal.throwIfAborted();
      const owner = JSON.stringify([
        identity.tenantId,
        identity.memberId,
        identity.deviceId,
      ]);
      if (controlOwner !== owner || !ctx.sessions?.get(controlSessionId)) {
        if (typeof ctx.sessionController?.create !== 'function')
          throw new Error('native network control session unavailable');
        const sessionId = randomUUID();
        const created = await ctx.sessionController.create({ sessionId });
        if (created?.error)
          throw new Error('native network control session unavailable');
        check();
        signal.throwIfAborted();
        controlSessionId = sessionId;
        controlOwner = owner;
      }
      const sessionId = controlSessionId;
      if (!sessionId)
        throw new Error('native network control session unavailable');
      /** @type {any} */ let port;
      const receivers = new Set();
      const open = async () => {
        check();
        signal.throwIfAborted();
        if (!port)
          port = await bridge.openSession(sessionId, (bytes) => {
            for (const receive of receivers) receive(bytes);
          });
        check();
        signal.throwIfAborted();
        return port;
      };
      signal.addEventListener(
        'abort',
        () => {
          void port?.close();
        },
        { once: true },
      );
      const read = async (/** @type {string} */ operation) => {
        let result;
        switch (operation) {
          case 'getPublicNetworkControl':
            result = await broker.call('getPublicNetworkControl', {});
            break;
          case 'getNetworkKeys':
            result = await broker.call('getNetworkKeys', {});
            break;
          case 'getNetworkKeyAnnouncement':
            result = await broker.call('getNetworkKeyAnnouncement', {});
            break;
          default:
            throw new Error('unknown fixed network control operation');
        }
        check();
        signal.throwIfAborted();
        const wire = result.body.wire;
        const bytes =
          typeof wire === 'string'
            ? Buffer.from(wire, 'base64')
            : Buffer.alloc(0);
        if (
          !bytes.length ||
          bytes.length > 33554432 ||
          bytes.toString('base64') !== wire
        )
          throw new Error('invalid signed network wire');
        return parseStrictJson(bytes);
      };
      return {
        kind: /** @type {const} */ ('device-connection'),
        storageKey: createHash('sha256')
          .update(
            JSON.stringify([issuer, identity.tenantId, identity.deviceId]),
          )
          .digest('hex'),
        url: installation.wssUrl,
        origin,
        roots: installation.roots,
        ...installed,
        async register() {
          const registration = (await open()).takeRegistration();
          if (registration.realm !== installation.realm)
            throw new Error('network registration installation mismatch');
          return registration;
        },
        async refresh(/** @type {any} */ trust) {
          await trust.installControl(await read('getPublicNetworkControl'));
          await trust.update(await read('getNetworkKeys'));
          await trust.announce(await read('getNetworkKeyAnnouncement'));
          check();
          trust.assertUsable();
        },
        async confirmControlReceipt(/** @type {any} */ receipt) {
          await broker.call('confirmNetworkControl', {
            receiptWire: Buffer.from(JSON.stringify(receipt)).toString(
              'base64',
            ),
          });
          check();
        },
        async getTicket() {
          throw new Error('network tickets are owned by the shell');
        },
        async submit(/** @type {any} */ request) {
          const context = await contexts.session(sessionId);
          const result = await broker.call('network_submit', {
            executionContextId: context.executionContextId,
            requestWire: Buffer.from(JSON.stringify(request)).toString(
              'base64',
            ),
          });
          check();
          return Buffer.from(result.body.resultWire, 'base64');
        },
        transportFactory(/** @type {any} */ options) {
          let state = 'idle';
          const epoch = 1;
          let off = () => {};
          const receive = (/** @type {Buffer} */ bytes) =>
            options.onMessage(bytes, epoch);
          return {
            get state() {
              return state;
            },
            get epoch() {
              return epoch;
            },
            async start() {
              const current = await open();
              receivers.add(receive);
              off = current.onClosed(() => {
                state = 'stopped';
                options.onState({ state, epoch });
              });
              state = 'ready';
              options.onState({ state, epoch });
              return current;
            },
            async send(/** @type {Uint8Array} */ bytes) {
              if (state !== 'ready')
                throw new Error('network transport stopped');
              check();
              await (await open()).send(bytes);
            },
            async stop() {
              state = 'stopped';
              off();
              receivers.delete(receive);
              const closing = port;
              port = undefined;
              await closing?.close();
            },
          };
        },
      };
    },
    close() {
      bridge.close();
    },
  };
}
