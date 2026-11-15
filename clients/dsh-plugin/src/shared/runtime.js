// @ts-nocheck
import { randomUUID } from 'node:crypto';
import { memberWorkspace } from './member-files.js';
import {
  validate,
  same,
  lifetime,
  verifyToken,
  fail,
  SHARED_RISK,
} from './protocol.js';

const bindingKeys = [
  'tenantId',
  'memberId',
  'placementId',
  'physicalInstanceId',
  'mode',
  'epoch',
  'revision',
];
export const placementBinding = (b) =>
  Object.fromEntries(bindingKeys.map((k) => [k, b[k]]));
const copy = (v) => JSON.parse(JSON.stringify(v));
const immutable = (b) => Object.freeze(copy(b));
export const ALLOWED_ACTIONS = Object.freeze([
  'session/page',
  'session/prompt',
  'session/cancel',
  'event:changed',
  'workspaceFiles/read',
  'workspaceFiles/stat',
  'workspaceFiles/readBytes',
]);

/** All ports are trusted server services, never request body properties.
 * resolve(binding, platformSessionId) MUST query W/IAM on every invocation.
 * register(binding,parentSessionId) MUST durably register before publication.
 * lookup(sessionId) MUST return W's authoritative SessionBinding after restart.
 */
export function createSharedRuntime({
  ports,
  identityKeys,
  capabilityKeys,
  physicalInstanceId,
  now = () => Math.floor(Date.now() / 1000),
}) {
  const ownership = new Map(),
    peers = new WeakMap(),
    replay = new Map();
  let closed = false;
  const floors = new Map();
  const fence = (b) => {
    if (closed) fail();
    const f = floors.get(b.placementId);
    if (
      f &&
      (b.epoch < f.epoch ||
        b.revision < f.revision ||
        b.authGeneration < f.authGeneration)
    )
      fail();
    if (b.mode !== 'shared' || b.physicalInstanceId !== physicalInstanceId)
      fail('shared.binding_mismatch');
  };
  async function current(b) {
    fence(b);
    let state;
    try {
      state = await ports.resolve(placementBinding(b), b.platformSessionId);
    } catch {
      fail('common.unavailable');
    }
    fence(b);
    if (!state?.active) fail();
    validate('Binding', state.binding);
    same(b, state.binding);
    if (
      b.platformSessionId !== state.platformSessionId ||
      (b.authGeneration !== undefined &&
        b.authGeneration !== state.authGeneration)
    )
      fail();
    return state;
  }
  async function session(id) {
    let b;
    try {
      b = await ports.lookup(id);
    } catch {
      fail('common.unavailable');
    }
    validate('SessionBinding', b);
    same(b, { sessionId: id });
    await current(b);
    const cached = ownership.get(id);
    if (cached) same(b, cached);
    b = immutable(b);
    ownership.set(id, b);
    return b;
  }
  async function admit(token, request, makePeer) {
    const c = verifyToken(token, 'Identity', identityKeys);
    lifetime(c, now(), 60);
    if (/\/\/|(^|\/)\.{1,2}(\/|$)|%2f|%5c|%2e/i.test(c.canonicalPath))
      fail('shared.binding_mismatch');
    try {
      decodeURIComponent(c.canonicalPath);
    } catch {
      fail('shared.binding_mismatch');
    }
    same(c, { method: request.method, canonicalPath: request.canonicalPath });
    for (const [j, exp] of replay) if (exp <= now()) replay.delete(j);
    if (replay.has(c.jti)) fail();
    replay.set(c.jti, c.expiresAt);
    const s = await current({
      ...c.binding,
      platformSessionId: c.platformSessionId,
    });
    lifetime(c, now(), 60);
    const principal = Object.freeze({
      kind: 'auriga-shared-member',
      ...c.binding,
      platformSessionId: c.platformSessionId,
      authGeneration: s.authGeneration,
    });
    const peer = makePeer(principal);
    if (peer.principal.kind !== 'auriga-shared-member') fail();
    peers.set(peer, principal);
    return peer;
  }
  async function create(peer, host, input = {}) {
    if (Object.keys(input).some((k) => k !== 'parentSessionId'))
      fail('common.validation_failed');
    const p = peers.get(peer);
    if (!p) fail();
    await current(p);
    if (input.parentSessionId) {
      const parent = await session(input.parentSessionId);
      same(parent, pickSessionOwner(p));
    }
    const b = immutable({ ...pickSessionOwner(p), sessionId: randomUUID() });
    validate('SessionBinding', b);
    const cwd = memberWorkspace(b);
    // prepare is unpublished. No sessions.enter/agent publication before Register ACK.
    const prepared = await host.prepare(b.sessionId, { meta: { cwd } });
    try {
      const ack = await ports.register(copy(b), input.parentSessionId ?? null);
      validate('SessionBinding', ack);
      same(ack, b);
      await current(b);
      ownership.set(b.sessionId, b);
      await host.publish(prepared);
      await current(b);
      return b;
    } catch (e) {
      ownership.delete(b.sessionId);
      await host.discard(prepared);
      throw e;
    }
  }
  async function authorize(peer, action, id) {
    if (!ALLOWED_ACTIONS.includes(action)) fail('common.validation_failed');
    const p = peers.get(peer);
    if (!p) fail();
    await current(p);
    const b = await session(id);
    same(b, pickSessionOwner(p));
    return b;
  }
  async function capability(
    token,
    id,
    taskId,
    scope,
    audience = 'auriga.shared.enterprise',
  ) {
    const trustedKeys =
      capabilityKeys[audience] ??
      (audience === 'auriga.shared.enterprise' ? capabilityKeys : new Map());
    const c = verifyToken(token, 'Capability', trustedKeys);
    lifetime(c, now(), 600);
    const b = await session(id);
    same(c.binding, b);
    const allowed = {
      'auriga.shared.enterprise': ['credentials', 'outbound'],
      'auriga.shared.model': ['model.invoke'],
      'auriga.shared.message': ['message.send', 'message.receive'],
    }[audience];
    if (
      !allowed ||
      c.audience !== audience ||
      !c.scopes.includes(scope) ||
      c.scopes.some((s) => !allowed.includes(s)) ||
      c.taskId !== taskId
    )
      fail();
    let state;
    try {
      state = await ports.authorizeTask(c);
    } catch {
      fail('common.unavailable');
    }
    await current(b);
    lifetime(c, now(), 600);
    if (
      !state?.active ||
      state.credentialVersion !== c.credentialVersion ||
      (scope === 'credentials' && state.allowRawAgentToken === false)
    )
      fail();
    return { binding: b, claims: immutable(c), state };
  }
  function revoke(event) {
    validate('Revocation', event);
    const f = floors.get(event.placementId);
    floors.set(event.placementId, {
      epoch: Math.max(f?.epoch ?? 0, event.epoch),
      revision: Math.max(f?.revision ?? 0, event.revision),
      authGeneration: Math.max(f?.authGeneration ?? 0, event.authGeneration),
    });
    for (const [id, b] of ownership)
      if (b.placementId === event.placementId) ownership.delete(id);
  }
  async function resources(id) {
    const b = await session(id),
      state = await current(b);
    const v = await ports.resourceView(placementBinding(b));
    validate('ResourceView', v);
    same(v.binding, placementBinding(b));
    if (
      v.authorizationSetDigest !== state.authorizationSetDigest ||
      v.resources.some((r) => r.expiresAt <= now())
    )
      fail();
    await current(b);
    if (v.resources.some((resource) => resource.expiresAt <= now())) fail();
    return { ...copy(v), risk: SHARED_RISK };
  }
  // remoteAccess bindings are synchronous projections, never the authorization truth.
  function bindings(p) {
    if (closed || p.kind !== 'auriga-shared-member') return [];
    return [...ownership.values()]
      .filter((b) => {
        try {
          fence(b);
          same(b, pickSessionOwner(p));
          return true;
        } catch {
          return false;
        }
      })
      .map((b) => ({
        resource: { kind: 'session', id: b.sessionId },
        role: {
          name: 'auriga-shared-own',
          resource: 'session',
          actions: [...ALLOWED_ACTIONS],
        },
      }));
  }
  return {
    admit,
    create,
    session,
    authorize,
    capability,
    revoke,
    resources,
    bindings,
    close() {
      closed = true;
      ownership.clear();
      replay.clear();
    },
  };
}
function pickSessionOwner(p) {
  return {
    ...placementBinding(p),
    platformSessionId: p.platformSessionId,
    authGeneration: p.authGeneration,
  };
}
