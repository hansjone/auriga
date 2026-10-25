import { Buffer } from 'node:buffer';
import { randomUUID } from 'node:crypto';
import {
  addressKey,
  isUint64String,
  parseAddress,
  parseAddressText,
} from '../host/address.js';
import { HostSeamError } from '../host/errors.js';
import {
  encoding,
  inputSchema,
  opaqueIdPattern,
} from '../../generated/network/contract.js';
import validateInput from '../../generated/network/validate-input.js';
import { snapshotInput } from './json-input.js';

/** @typedef {import('../host/address.js').Address} Address */
/** @typedef {import('../host/ledger.js').Ledger} Ledger */
/** @typedef {import('../../generated/network/AgentSendToolInput.js').AgentSendToolInput} AgentSendToolInput */
/** @typedef {import('../../generated/network/SubmitRequest.js').SubmitRequest} SubmitRequest */
/** @typedef {import('../../generated/network/SendResult.js').SendResult} SendResult */
/** @typedef {{ agent?: { session?: { id?: string } } }} ExecutionContext */
/** @typedef {{ messages: number, windowMs: number, localMessages?: number }} RateLimit */
/**
 * Trusted plugin control-path authorization. It must be reloaded on every
 * sourceFor call and never obtained from model parameters or human UI intents.
 * @typedef {{ sourceBinding: string, producerEpoch: string, address: Address,
 * senderKind: 'agent', sendAllowed: boolean, firstContactAllowed?: boolean, expiresAt: number }} SourceAuthorization
 */
/** @typedef {{ status: 'request_submitted' } | { status: 'error', code: string }} ModelResult */

const opaqueId = new RegExp(opaqueIdPattern);
/** One bounded node-wide budget per durable ledger, shared by all tool instances. */
/** @type {WeakMap<Ledger, { policy: Required<RateLimit>, times: Array<{at: number, local: string}>, lastNow: number }>} */
const budgets = new WeakMap();
const hiddenTargetCodes = new Set([
  'network.target_unavailable',
  'network.target_mismatch',
  'network.host_incompatible',
  'network.contact_pending',
  'network.contact_denied',
  'network.contact_expired',
  'network.quota_exceeded',
  'network.revoked',
  'network.expired',
  'network.group_not_available',
  'network.group_publish_forbidden',
]);

/**
 * Model output intentionally carries no target-dependent identifiers, counters,
 * retry hints or receipt reasons. This is not a delivery/execution receipt.
 * @param {unknown} receipt
 * @returns {ModelResult}
 */
export function mapSendReceipt(receipt) {
  const value = /** @type {(Partial<SendResult> & { code?: unknown })|null} */ (
    receipt
  );
  if (
    value &&
    (['platform_stored', 'request_submitted', 'contact_pending'].includes(
      String(value.status),
    ) ||
      hiddenTargetCodes.has(String(value.code)))
  )
    return { status: 'request_submitted' };
  return { status: 'error', code: 'network.outcome_unknown' };
}

/**
 * No socket, HTTP client, MCP registration or background work. The returned
 * prepare() produces the private SubmitRequest, never a self-stamped envelope.
 * sourceFor must check current send scope/expiry and bind the exact local;
 * resolveTarget reads authoritative address registration, not a session list.
 *
 * @param {{
 * ledger: Ledger,
 * localAddress: (local: string) => Address,
 * sourceFor: (source: { hostSessionId: string, address: Address }) => SourceAuthorization|Promise<SourceAuthorization>,
 * resolveTarget: (target: { realm: string, node: string, local: string }) => Address|null|Promise<Address|null>,
 * releaseResult: (startedAt: number) => Promise<void>,
 * rateLimit: RateLimit,
 * now?: () => number,
 * }} options
 */
export function createAgentSendTool({
  ledger,
  localAddress,
  sourceFor,
  resolveTarget,
  releaseResult,
  rateLimit,
  now = Date.now,
}) {
  if (
    !rateLimit ||
    ![rateLimit.messages, rateLimit.windowMs].every(
      (n) => Number.isSafeInteger(n) && n > 0,
    )
  )
    throw new TypeError('an explicit positive send rate policy is required');
  const policy = {
    ...rateLimit,
    localMessages: rateLimit.localMessages ?? rateLimit.messages,
  };
  if (
    !Number.isSafeInteger(policy.localMessages) ||
    policy.localMessages <= 0 ||
    policy.localMessages > policy.messages
  )
    throw new TypeError(
      'localMessages must be a positive integer within the node budget',
    );
  if (
    ![localAddress, sourceFor, resolveTarget, releaseResult, now].every(
      (fn) => typeof fn === 'function',
    )
  )
    throw new TypeError('trusted host ports are required');
  let existing = budgets.get(ledger);
  if (
    existing &&
    (existing.policy.messages !== rateLimit.messages ||
      existing.policy.windowMs !== rateLimit.windowMs ||
      existing.policy.localMessages !== policy.localMessages)
  )
    throw new TypeError('a ledger must share one send rate policy');
  if (!existing) {
    existing = { policy, times: [], lastNow: -Infinity };
    budgets.set(ledger, existing);
  }
  const budget = existing;

  function clock() {
    const time = now();
    if (!Number.isFinite(time)) throw new HostSeamError('network.unauthorized');
    // A wall-clock rollback must not mint tokens or revive expired grants.
    budget.lastNow = Math.max(budget.lastNow, time);
    return budget.lastNow;
  }

  /**
   * The host's shared bounded queue must release responses under the same
   * timing policy. No target, receipt or rejection reason is given to it.
   * @param {ModelResult} result @param {number} startedAt
   * @returns {Promise<ModelResult>}
   */
  async function release(result, startedAt) {
    try {
      await releaseResult(startedAt);
      return result;
    } catch {
      return { status: 'error', code: 'network.outcome_unknown' };
    }
  }

  /** @param {string} local */
  function reserveRate(local) {
    const time = clock();
    budget.times = budget.times.filter(
      (t) => time - t.at < budget.policy.windowMs,
    );
    if (
      budget.times.length >= budget.policy.messages ||
      budget.times.filter((t) => t.local === local).length >=
        budget.policy.localMessages
    )
      throw new HostSeamError('network.quota_exceeded');
    budget.times.push({ at: time, local });
  }

  /** @param {ExecutionContext|undefined} exec */
  function sourceAddress(exec) {
    try {
      ledger.assertAvailable();
      const id = exec?.agent?.session?.id;
      if (typeof id !== 'string' || !id) throw new Error();
      const bindings = Object.values(ledger.peek().bindings).filter(
        (b) => b.hostSessionId === id,
      );
      if (bindings.length !== 1) throw new Error();
      const binding = bindings[0];
      const registration = ledger.peek().locals[binding.local];
      if (
        ledger.peek().identityConflict ||
        registration?.state !== 'registered' ||
        !registration.registrationId
      )
        throw new Error();
      const address = parseAddress(localAddress(binding.local), {
        role: 'source',
        realm: ledger.identity.realm,
      });
      if (
        address.node !== ledger.identity.node ||
        address.local !== binding.local ||
        address.type !== binding.kind ||
        !['session', 'subagent'].includes(address.type) ||
        binding.state !== 'bound' ||
        binding.lifecycle !== 'open'
      )
        throw new Error();
      return { hostSessionId: id, address };
    } catch {
      throw new HostSeamError('network.local_registration_required');
    }
  }

  /** @param {{ hostSessionId: string, address: Address }} source @param {boolean} firstContact */
  async function authorize(source, firstContact) {
    try {
      const auth = await sourceFor({
        ...source,
        address: { ...source.address },
      });
      const address = parseAddress(auth.address, {
        role: 'source',
        realm: ledger.identity.realm,
      });
      if (
        auth.senderKind !== 'agent' ||
        auth.sendAllowed !== true ||
        addressKey(address) !== addressKey(source.address) ||
        address.type !== source.address.type ||
        typeof auth.sourceBinding !== 'string' ||
        !opaqueId.test(auth.sourceBinding) ||
        !isUint64String(auth.producerEpoch) ||
        auth.producerEpoch === '0' ||
        !Number.isFinite(auth.expiresAt) ||
        auth.expiresAt <= clock()
      )
        throw new Error();
      if (firstContact && auth.firstContactAllowed !== true)
        throw new HostSeamError('network.first_contact_forbidden');
      // Copy scalars: the port must not mutate an authorization while awaiting target lookup.
      return {
        sourceBinding: auth.sourceBinding,
        producerEpoch: auth.producerEpoch,
      };
    } catch (error) {
      if (
        error instanceof HostSeamError &&
        error.code === 'network.first_contact_forbidden'
      )
        throw error;
      throw new HostSeamError('network.unauthorized');
    }
  }

  /** @param {ExecutionContext|undefined} exec @param {{hostSessionId: string, address: Address}} source */
  function assertSameSource(exec, source) {
    const current = sourceAddress(exec);
    if (
      current.hostSessionId !== source.hostSessionId ||
      addressKey(current.address) !== addressKey(source.address) ||
      current.address.type !== source.address.type
    )
      throw new HostSeamError('network.unauthorized');
    return current;
  }

  /** @param {unknown} input @param {ExecutionContext} [exec] @returns {Promise<SubmitRequest>} */
  async function prepare(input, exec) {
    const snapshot = snapshotInput(input);
    if (!validateInput(snapshot))
      throw new HostSeamError('network.protocol_error');
    const data = /** @type {AgentSendToolInput} */ (snapshot);
    if (Buffer.byteLength(JSON.stringify(data.body)) > encoding.maxBodyBytes)
      throw new HostSeamError('network.quota_exceeded');
    const source = sourceAddress(exec);
    const target = parseAddressText(data.target, ledger.identity.realm);
    if (target.node === '0')
      throw new HostSeamError(
        target.local === '1'
          ? 'network.broadcast_forbidden'
          : 'network.group_not_available',
      );
    const firstContact = target.local === '0';
    const auth = await authorize(source, firstContact);
    assertSameSource(exec, source);
    // Failed authorization cannot spend the shared budget. Authorized target
    // probes reserve synchronously before lookup and are never refunded.
    reserveRate(source.address.local);
    let to;
    try {
      to = parseAddress(
        await resolveTarget({
          realm: target.realm,
          node: target.node,
          local: target.local,
        }),
        { realm: source.address.realm },
      );
      if (
        addressKey(to) !== `${target.realm}.${target.node}.${target.local}` ||
        to.type === 'group'
      )
        throw new Error();
    } catch {
      throw new HostSeamError('network.target_unavailable');
    }
    const current = assertSameSource(exec, source);
    const fresh = await authorize(current, firstContact);
    assertSameSource(exec, source);
    if (
      fresh.sourceBinding !== auth.sourceBinding ||
      fresh.producerEpoch !== auth.producerEpoch
    )
      throw new HostSeamError('network.unauthorized');
    return {
      kind: firstContact ? 'first_contact' : 'message',
      ...(firstContact ? { contact_scope: 'conversation' } : {}),
      to,
      durability: 'durable',
      intent: firstContact ? 'request' : 'information',
      body: data.body,
      ...(data.thread === undefined ? {} : { thread_id: data.thread }),
      source: { source_binding: auth.sourceBinding },
      idempotency: { producer_epoch: auth.producerEpoch, key: randomUUID() },
    };
  }

  return {
    // Detached schema: registration cannot mutate the compiled validator.
    inputSchema: /** @type {typeof inputSchema} */ (
      JSON.parse(JSON.stringify(inputSchema))
    ),
    prepare,
    /**
     * submit must durably retain this request/idempotency key before I/O and
     * reconcile response loss with the same key. This tool never retries.
     * @param {unknown} input @param {ExecutionContext} exec
     * @param {(request: SubmitRequest) => unknown|Promise<unknown>} submit
     * @returns {Promise<ModelResult>}
     */
    async execute(input, exec, submit) {
      let startedAt;
      try {
        startedAt = clock();
      } catch {
        return { status: 'error', code: 'network.unauthorized' };
      }
      let request;
      try {
        request = await prepare(input, exec);
      } catch (error) {
        if (error instanceof HostSeamError) {
          if (error.code === 'network.target_unavailable')
            return release({ status: 'request_submitted' }, startedAt);
          return release({ status: 'error', code: error.code }, startedAt);
        }
        return release(
          { status: 'error', code: 'network.protocol_error' },
          startedAt,
        );
      }
      try {
        return release(mapSendReceipt(await submit(request)), startedAt);
      } catch {
        return release(
          { status: 'error', code: 'network.outcome_unknown' },
          startedAt,
        );
      }
    },
  };
}
