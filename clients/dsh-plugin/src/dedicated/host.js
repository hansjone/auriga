// @ts-nocheck
import { canonical, fail } from '../shared/protocol.js';
import { placementBinding } from '../shared/runtime.js';
import { sameBinding } from './intent.js';

const sessionId = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/;
const mutations = new Set([
  'pluginManager/installBundle',
  'pluginManager/setBundleEnabled',
  'pluginManager/removeBundle',
  'pluginManager/setPluginEnabled',
  'pluginManager/setVersionExemption',
]);
const forbidden = new Set([
  'tenantId',
  'memberId',
  'placementId',
  'physicalInstanceId',
  'platformSessionId',
  'authGeneration',
  'credentialVersion',
]);

/** The ONLY owner mapping is a fresh-E/W-admitted unique dedicated binding.
 * It mints no owner cookie or reusable owner ticket. The retained native Peer
 * exists solely so rc.3 matches interaction answers to their real event stream. */
export function createDedicatedHost(
  ctx,
  { runtime, ports, binding, resources },
) {
  sameBinding(binding, binding);
  const records = new Map();
  let closed = false;
  async function check(member) {
    if (closed || member.signal?.aborted) fail('shared.revoked');
    sameBinding(placementBinding(member.principal), binding);
    await runtime.refresh(member);
    await resources.verify();
    await runtime.refresh(member);
  }
  async function owns(member, id) {
    if (typeof id !== 'string' || !sessionId.test(id))
      fail('common.validation_failed');
    await runtime.authorize(member, 'session/page', id);
    return id;
  }
  async function visible(member, id) {
    try {
      await owns(member, id);
      return true;
    } catch (error) {
      if (['common.not_found', 'shared.binding_mismatch'].includes(error.code))
        return false;
      throw error;
    }
  }
  async function retire(key, row) {
    if (records.get(key) === row) records.delete(key);
    row.abort.abort();
    await Promise.allSettled([row.native.dispose(), row.member.dispose()]);
  }
  const timer = setInterval(() => {
    for (const [key, row] of records) {
      if (row.checking) continue;
      row.checking = true;
      void check(row.member)
        .catch(() => retire(key, row))
        .finally(() => {
          row.checking = false;
        })
        .catch(() => {});
    }
  }, 1000);
  timer.unref?.();
  async function admit(token, request) {
    const member = await runtime.admit(token, request, (principal) =>
      ports.createPeer({ principal, interactions: 'none' }),
    );
    let retained = false;
    try {
      await check(member);
      const key = canonical(member.principal);
      let row = records.get(key);
      if (!row) {
        if (records.size >= 128) fail('common.unavailable');
        row = {
          member,
          native: ports.createPeer({
            principal: Object.freeze({ kind: 'owner' }),
            interactions: 'answer',
          }),
          abort: new AbortController(),
          checking: false,
          events: new Map(),
        };
        records.set(key, row);
        retained = true;
      }
      await check(row.member);
      return row;
    } finally {
      if (!retained) await member.dispose();
    }
  }
  async function checkArguments(member, value, depth = 0) {
    if (depth > 32) fail('common.validation_failed');
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      for (const entry of value) await checkArguments(member, entry, depth + 1);
      return;
    }
    for (const [key, entry] of Object.entries(value)) {
      if (forbidden.has(key)) fail('common.validation_failed');
      if (
        (/SessionId$/.test(key) ||
          ['sessionId', 'workspaceFileScopeId', 'agentId'].includes(key)) &&
        entry !== undefined &&
        entry !== null
      )
        await owns(member, entry);
      if (/SessionIds$/.test(key) || key === 'sessionIds') {
        if (!Array.isArray(entry)) fail('common.validation_failed');
        for (const id of entry) await owns(member, id);
      }
      if ((key === 'id' || key === 'sessionId') && value.kind === 'session')
        await owns(member, entry);
      await checkArguments(member, entry, depth + 1);
    }
  }
  const envelope = (input) => {
    if (
      !input ||
      typeof input !== 'object' ||
      Array.isArray(input) ||
      Object.keys(input).sort().join(',') !== 'channel,endpoint,payload' ||
      input.channel !== '/api' ||
      typeof input.endpoint !== 'string' ||
      !/^(?:\$events(?:\/result)?|[A-Za-z][A-Za-z0-9_-]*\/[A-Za-z][A-Za-z0-9_-]*)$/.test(
        input.endpoint,
      ) ||
      !input.payload ||
      typeof input.payload !== 'object'
    )
      fail('common.validation_failed');
    return input;
  };
  async function prepare(row, input, stream) {
    envelope(input);
    await check(row.member);
    const endpoint = input.endpoint;
    if (mutations.has(endpoint)) fail('common.forbidden');
    if (endpoint === '$events') {
      if (!stream) fail('common.validation_failed');
      return;
    }
    if (endpoint === '$events/result') {
      if (stream) fail('common.validation_failed');
      const delivered = row.events.get(input.payload.args?.eventId);
      if (!delivered || delivered.clientId !== input.payload.args?.clientId)
        fail('common.forbidden');
      await owns(row.member, delivered.agentId);
      await check(row.member);
      return;
    }
    if (endpoint === 'session/create') {
      if (
        stream ||
        !input.payload.args?.request ||
        Object.keys(input.payload.args.request).some(
          (key) => key !== 'sessionId',
        )
      )
        fail('common.validation_failed');
      if (input.payload.args.request.sessionId !== undefined)
        await owns(row.member, input.payload.args.request.sessionId);
      return;
    }
    if (
      [
        'session/fork',
        'session/clone',
        'session/import',
        'session/restore',
      ].includes(endpoint)
    )
      fail('common.forbidden');
    // Host controllers register local descriptors; `remotes` describes Client
    // receivers. The public Gateway also recognizes SRC-declared controllers.
    if (
      typeof ctx.typertGateway?.claimsEndpoint !== 'function' ||
      !ctx.typertGateway.claimsEndpoint(endpoint)
    )
      fail('common.forbidden');
    await checkArguments(row.member, input.payload.args);
    const serialized = canonical(input.payload.args);
    if (
      endpoint.startsWith('session/') &&
      ![
        'session/list',
        'session/search',
        'session/control',
        'session/modelCatalog',
        'session/canOpenWorkspacePath',
      ].includes(endpoint) &&
      !/"(?:sessionId|agentId|[A-Za-z]+SessionId)":/.test(serialized)
    )
      fail('common.forbidden');
    await check(row.member);
  }
  async function call(row, input, signal) {
    await prepare(row, input, false);
    signal.throwIfAborted();
    let result;
    if (input.endpoint === 'session/create') {
      const created = await runtime.create(row.member, ports.sessions, {});
      result = { ok: true, value: { sessionId: created.sessionId } };
    } else
      result = await ctx.typertGateway.dispatchRpc(
        input.endpoint,
        input.payload,
        signal,
        row.native,
      );
    if (input.endpoint === '$events/result' && result.ok)
      row.events.delete(input.payload.args.eventId);
    if (
      result.ok &&
      ['session/list', 'session/search'].includes(input.endpoint)
    ) {
      if (!Array.isArray(result.value?.items)) fail('common.unavailable');
      const items = [];
      for (const item of result.value.items)
        if (await visible(row.member, item.sessionId)) items.push(item);
      result = { ...result, value: { ...result.value, items } };
    }
    await check(row.member);
    signal.throwIfAborted();
    return result;
  }
  async function open(row, input, signal) {
    await prepare(row, input, true);
    signal.throwIfAborted();
    const source = await ctx.typertGateway.wireStream.open(
      input.endpoint,
      input.payload,
      undefined,
      row.native,
      signal,
    );
    async function* filtered() {
      let clientId;
      const delivered = new Set();
      try {
        for await (let frame of source) {
          await check(row.member);
          signal.throwIfAborted();
          if (input.endpoint === 'session/control') {
            if (frame.type === 'baseline') {
              const projections = {};
              for (const [id, value] of Object.entries(frame.value.projections))
                if (await visible(row.member, id)) projections[id] = value;
              frame = { ...frame, value: { ...frame.value, projections } };
            } else if (frame.type === 'projection') {
              if (!(await visible(row.member, frame.sessionId))) continue;
            } else fail('common.unavailable');
          } else if (input.endpoint === '$events') {
            if (frame.type === 'ready') clientId = frame.clientId;
            if (frame.type === 'waterfall') {
              if (!(await visible(row.member, frame.agentId))) continue;
              if (typeof clientId !== 'string' || row.events.size >= 1024)
                fail('common.unavailable');
              row.events.set(frame.eventId, {
                clientId,
                agentId: frame.agentId,
              });
              delivered.add(frame.eventId);
            }
            if (frame.type === 'cancel') {
              row.events.delete(frame.eventId);
              delivered.delete(frame.eventId);
            }
            if (frame.type === 'emit') {
              try {
                await checkArguments(row.member, frame.args);
              } catch (error) {
                if (
                  ['common.not_found', 'shared.binding_mismatch'].includes(
                    error.code,
                  )
                )
                  continue;
                throw error;
              }
            }
          }
          await check(row.member);
          signal.throwIfAborted();
          yield frame;
        }
      } finally {
        for (const id of delivered)
          if (row.events.get(id)?.clientId === clientId) row.events.delete(id);
      }
    }
    return filtered();
  }
  return {
    admit,
    call,
    open,
    check: (row) => check(row.member),
    async close() {
      if (closed) return;
      closed = true;
      clearInterval(timer);
      await Promise.allSettled(
        [...records.entries()].map(([key, row]) => retire(key, row)),
      );
    },
  };
}
