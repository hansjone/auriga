// @ts-nocheck
import { fail } from '../shared/protocol.js';
import { setTimeout as delay } from 'node:timers/promises';
import { memberWorkspace } from '../shared/member-files.js';

// A shared member has exactly one fixed Workspace: their own member directory.
// It is synthesized here; native Workspace create/rename/delete stay denied.
function fixedWorkspaceId(principal) {
  return `auriga-member-${principal.memberId}`;
}
const fixedCreatedAt = new Date().toISOString();
function fixedWorkspaceState(principal, sessionIds) {
  return {
    items: [
      {
        workspaceId: fixedWorkspaceId(principal),
        path: memberWorkspace(principal),
        title: '我的目录',
        sessionIds: [...sessionIds],
        createdAt: fixedCreatedAt,
        updatedAt: fixedCreatedAt,
      },
    ],
    archivedSessionIds: [],
    pinnedSessionIds: [],
  };
}

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
export function createNativeBrowser({
  host,
  gateway,
  remoteAccess,
  workspaceRecheckMs = 5000,
}) {
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
    if (!stream && endpoint === 'workspace/initializeDefault') {
      exact(args, []);
      return {
        endpoint,
        workspaceDefault: true,
        check: () => host.refresh(peer),
      };
    }
    if (stream && endpoint === 'workspace/follow') {
      exact(args, []);
      return {
        endpoint,
        workspaceFollow: true,
        check: () => host.refresh(peer),
      };
    }
    if (!stream && endpoint === 'session/create') {
      exact(args, ['request']);
      // Native blank-session reuse is optional. It must never select an
      // arbitrary id or cwd; the trusted Host mints and registers the new id.
      // workspaceId is optional: omitted means the member's fixed Workspace.
      // agentPreset is optional and must be a published preset id only.
      exact(args.request, ['sessionId', 'workspaceId', 'agentPreset']);
      if (
        args.request.workspaceId !== undefined &&
        args.request.workspaceId !== fixedWorkspaceId(peer.principal)
      )
        fail('shared.binding_mismatch');
      if (
        args.request.agentPreset !== undefined &&
        (typeof args.request.agentPreset !== 'string' ||
          !args.request.agentPreset)
      )
        fail('common.validation_failed');
      if (args.request.sessionId !== undefined)
        await host.authorize(peer, 'session/page', args.request.sessionId);
      await host.refresh(peer);
      const createInput =
        args.request.agentPreset === undefined
          ? {}
          : { agentPreset: args.request.agentPreset };
      return {
        endpoint,
        create: true,
        createInput,
        check: () => host.refresh(peer),
      };
    }
    if (!stream && endpoint === 'agentPresets/list') {
      exact(args, []);
      // Gateway requires the {args} envelope even for an empty list request.
      return {
        endpoint,
        payload: envelope.payload,
        check: () => host.refresh(peer),
      };
    }
    if (!stream && endpoint === 'agentPresets/select') {
      // Wire: Agent parameter projects to agentId; preset id is agentPreset.
      exact(args, ['agentId', 'agentPreset']);
      if (
        typeof args.agentId !== 'string' ||
        !args.agentId ||
        typeof args.agentPreset !== 'string' ||
        !args.agentPreset
      )
        fail('common.validation_failed');
      const check = () => host.authorize(peer, 'session/page', args.agentId);
      await check();
      return { endpoint, payload: envelope.payload, check };
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
        // Keep the empty-object create path literal for AUTH-K shape review;
        // only a validated agentPreset may ride createInput.
        if (Object.keys(selected.createInput).length === 0) {
          const binding = await host.create(peer, {});
          remoteAccess.invalidate(peer.principal);
          result = { ok: true, value: { sessionId: binding.sessionId } };
        } else {
          const binding = await host.create(peer, selected.createInput);
          remoteAccess.invalidate(peer.principal);
          result = { ok: true, value: { sessionId: binding.sessionId } };
        }
      } else if (selected.messages) {
        result = { ok: true, value: await host.messagingSessions(peer) };
      } else if (selected.settings) {
        result = {
          ok: true,
          value: await host.messagingSettings(peer, selected.settings),
        };
      } else if (selected.workspaceDefault) {
        const ids = await host.refresh(peer);
        result = {
          ok: true,
          value: fixedWorkspaceState(peer.principal, ids).items[0],
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
      if (selected.workspaceFollow) {
        async function* fixed() {
          let last = await host.refresh(peer);
          yield {
            type: 'baseline',
            value: fixedWorkspaceState(peer.principal, last),
          };
          while (!signal?.aborted) {
            await delay(workspaceRecheckMs, undefined, { signal });
            const ids = await host.refresh(peer);
            if (ids.join(',') !== last.join(',')) {
              last = ids;
              yield {
                type: 'replace',
                value: fixedWorkspaceState(peer.principal, ids),
              };
            }
          }
        }
        return { source: fixed(), check: selected.check };
      }
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
