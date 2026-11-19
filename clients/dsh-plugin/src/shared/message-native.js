import { randomUUID } from 'node:crypto';
import { registrationSession } from '../network/host/native/host-port.js';
import { createNativeSourceResolver } from '../network/host/native/source-context.js';
import { u64 } from '../network/source-control/frames.js';
import { canonical, fail, validate } from './protocol.js';

/**
 * An in-process native witness, never a JSON/HTTP session resolver. Platform
 * registration assigns the typed local; this layer only binds that result to
 * the exact live public Host objects which initiated the current K task.
 * @param {import('../network/host/native/types.js').NativeContext} ctx
 * @param {{now?:()=>number}} [options]
 */
export function createSharedMessageNative(ctx, { now = Date.now } = {}) {
  const bootId = randomUUID();
  /** @type {Map<string, {binding:string,token:ReturnType<typeof registrationSession>,bindingId:string,operationId:string,local:string|null,failed:boolean}>} */
  const rows = new Map();
  let closed = false;
  let highClock = -Infinity;
  const clock = () => {
    const value = now();
    if (!Number.isSafeInteger(value) || value < 0 || value < highClock)
      fail('common.unavailable');
    highClock = value;
    return value;
  };
  const api = {
    /** @param {{agent:import('../network/host/native/types.js').Agent,signal?:AbortSignal}} execution @param {any} binding */
    async capture(execution, binding) {
      if (closed || execution?.signal?.aborted) fail();
      validate('SessionBinding', binding);
      if (
        binding.mode !== 'shared' ||
        execution?.agent?.session?.id !== binding.sessionId
      )
        fail('shared.binding_mismatch');
      const identity = canonical(binding);
      let row = rows.get(binding.sessionId);
      if (!row) {
        row = {
          binding: identity,
          token: registrationSession(ctx, execution),
          bindingId: randomUUID(),
          operationId: randomUUID(),
          local: null,
          failed: false,
        };
        rows.set(binding.sessionId, row);
      }
      const current = row;
      const checkNative = () => {
        if (
          closed ||
          current.failed ||
          execution.signal?.aborted ||
          current.binding !== identity ||
          rows.get(binding.sessionId) !== current
        )
          fail();
        try {
          current.token.check();
          if (ctx.agents.get(binding.sessionId) !== execution.agent)
            fail('shared.binding_mismatch');
        } catch (error) {
          current.failed = true;
          throw error;
        }
      };
      /** @param {string} local @param {()=>void} check */
      async function resolveLocal(local, check) {
        check();
        const mapping = {
          local,
          bindingId: current.bindingId,
          hostSessionId: binding.sessionId,
          cwd: current.token.cwd,
          state: 'bound',
          lifecycle: 'open',
          kind: 'session',
        };
        const resolver = await createNativeSourceResolver({
          ctx,
          lookupBinding: async (id) => {
            check();
            return id === binding.sessionId ? mapping : null;
          },
        });
        check();
        const resolved = await resolver(execution);
        check();
        if (
          resolved.local !== local ||
          resolved.bindingId !== current.bindingId
        )
          fail('shared.binding_mismatch');
        return resolved;
      }
      checkNative();
      await current.token.verify();
      checkNative();
      const bootstrap = current.local === null;
      if (current.local !== null)
        await resolveLocal(current.local, checkNative);
      const observed = Math.floor(clock() / 1000) * 1000;
      const iso = (/** @type {number} */ value) =>
        new Date(value).toISOString().replace('.000Z', 'Z');
      const witness = Object.freeze({
        witness_id: randomUUID(),
        binding_id: current.bindingId,
        binding_revision: '1',
        observed_at: iso(observed),
        valid_until: iso(observed + 30_000),
      });
      const check = () => {
        checkNative();
        const value = clock();
        if (value < observed || value >= observed + 30_000)
          fail('shared.expired');
      };
      return Object.freeze({
        bootId,
        operationId: current.operationId,
        bootstrap,
        witness,
        check,
        refresh: () => api.capture(execution, binding),
        /** Only the verified platform registration result may supply local.
         * @param {string} local */
        async bindLocal(local) {
          check();
          if (
            !u64(local) ||
            (current.local !== null && current.local !== local)
          )
            fail('shared.binding_mismatch');
          // Reserve synchronously: concurrent responses cannot both bind an
          // empty row to different locals across the public-host await.
          current.local = local;
          return resolveLocal(local, check);
        },
      });
    },
    close() {
      closed = true;
      rows.clear();
    },
  };
  return api;
}
