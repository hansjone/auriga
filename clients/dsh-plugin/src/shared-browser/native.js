// @ts-nocheck
import { fail } from '../shared/protocol.js';
import { memberWorkspace } from '../shared/member-files.js';

const unary = new Map([
  ['session/page', 'session/page'],
  ['session/prompt', 'session/prompt'],
  ['session/cancel', 'session/cancel'],
  ['session/projections', 'session/projections'],
]);
const streams = new Map([['session/follow', 'session/follow']]);
const forbidden = [
  'cwd',
  'ownerId',
  'tenantId',
  'memberId',
  'placementId',
  'physicalInstanceId',
  'epoch',
  'authGeneration',
  'sender',
];
function object(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    fail('common.validation_failed');
  return value;
}
function exact(value, keys) {
  object(value);
  if (Object.keys(value).some((key) => !keys.includes(key)))
    fail('common.validation_failed');
}

/** The transport is finite. The native Gateway remains responsible for codecs,
 * resource decisions, parameter validation, streaming and actual execution. */
export function createNativeBrowser({ host, gateway, remoteAccess }) {
  async function prepare(peer, envelope, stream) {
    exact(envelope, ['channel', 'endpoint', 'payload']);
    if (envelope.channel !== '/api' || typeof envelope.endpoint !== 'string')
      fail('common.validation_failed');
    exact(envelope.payload, ['args']);
    const args = object(envelope.payload.args);
    const { endpoint } = envelope;
    if (endpoint === '$events/result') fail('shared.revoked');
    const action = (stream ? streams : unary).get(endpoint);
    if (action) {
      exact(args, ['request']);
      const request = object(args.request);
      if (forbidden.some((key) => Object.hasOwn(request, key)))
        fail('common.validation_failed');
      let id = request.sessionId;
      if (endpoint === 'session/page' || endpoint === 'session/follow') {
        exact(request.address, ['kind', 'sessionId']);
        if (request.address.kind !== 'session') fail('shared.revoked');
        id = request.address.sessionId;
        if (request.sessionId !== undefined && request.sessionId !== id)
          fail('shared.binding_mismatch');
      }
      if (typeof id !== 'string' || !id) fail('common.validation_failed');
      const check = () => host.authorize(peer, action, id);
      await check();
      return { endpoint, payload: envelope.payload, check };
    }
    const global = stream
      ? ['$events', 'remoteAccess/follow', 'session/control']
      : [
          'session/list',
          'session/modelCatalog',
          'session/canOpenWorkspacePath',
        ];
    if (global.includes(endpoint)) {
      exact(args, endpoint === 'session/list' ? ['_request', 'request'] : []);
      if (endpoint === 'session/list') {
        for (const value of Object.values(args)) exact(value, []);
      }
      const check = () => host.refresh(peer);
      await check();
      return { endpoint, payload: envelope.payload, check };
    }
    if (!stream && endpoint === 'session/create') {
      exact(args, ['request']);
      // Native blank-session reuse is optional. It must never select an
      // arbitrary id or cwd; the trusted Host mints and registers the new id.
      exact(args.request, ['sessionId']);
      if (args.request.sessionId !== undefined)
        await host.authorize(peer, 'session/page', args.request.sessionId);
      await host.refresh(peer);
      return { endpoint, create: true, check: () => host.refresh(peer) };
    }
    if (!stream && endpoint === 'aurigaShared/messagingSessions') {
      exact(args, []);
      return { endpoint, messages: true, check: () => host.refresh(peer) };
    }
    if (!stream && endpoint === 'aurigaShared/messagingSettings') {
      exact(args, ['request']);
      exact(args.request, ['sessionId', 'enabled']);
      return {
        endpoint,
        settings: args.request,
        check: () =>
          host.authorize(peer, 'session/page', args.request.sessionId),
      };
    }
    fail('shared.revoked');
  }
  return {
    async snapshot(peer) {
      await host.refresh(peer);
      return remoteAccess.snapshotFor(peer.principal);
    },
    async call(peer, envelope, signal) {
      const selected = await prepare(peer, envelope, false);
      signal.throwIfAborted();
      let result;
      if (selected.create) {
        const binding = await host.create(peer, {});
        remoteAccess.invalidate(peer.principal);
        result = { ok: true, value: { sessionId: binding.sessionId } };
      } else if (selected.messages) {
        result = { ok: true, value: await host.messagingSessions(peer) };
      } else if (selected.settings) {
        result = {
          ok: true,
          value: await host.messagingSettings(peer, selected.settings),
        };
      } else if (selected.endpoint === 'session/canOpenWorkspacePath') {
        // A shared remote process has no user's desktop to open a path in.
        result = { ok: true, value: false };
      } else {
        result = await gateway.dispatchRpc(
          selected.endpoint,
          selected.payload,
          signal,
          peer,
        );
      }
      await selected.check();
      signal.throwIfAborted();
      return result;
    },
    async open(peer, envelope, signal) {
      const selected = await prepare(peer, envelope, true);
      const source = await gateway.wireStream.open(
        selected.endpoint,
        selected.payload,
        undefined,
        peer,
        signal,
      );
      async function* scoped() {
        for await (const frame of source) {
          if (selected.endpoint === '$events' && frame.type === 'ready') {
            yield { ...frame, host: { home: memberWorkspace(peer.principal) } };
          } else {
            yield frame;
          }
        }
      }
      return { source: scoped(), check: selected.check };
    },
    failure(error) {
      return gateway.wireStream.failure(error);
    },
  };
}
