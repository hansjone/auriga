/* global AbortSignal */
import { open, constants } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { openLedger } from './host/ledger.js';
import { formatAddress } from './host/address.js';
import { registrationSession } from './host/native/host-port.js';

export const DIAGNOSTIC_PATH = '/api/iam/v1/login/providers';

export const BASE_TOOL_NAMES = [
  'auriga_net_status',
  'auriga_net_diagnose',
  'auriga_net_identity',
  'auriga_net_inbox',
];
export const BUILTIN_TOOL_NAMES = new Set([
  ...BASE_TOOL_NAMES,
  'auriga_send_message',
  'auriga_network_join',
]);
/** Only classified evidence is published, never raw transport errors.
 * @param {any} error */
export function connectionReason(error) {
  if (error?.status === 401 || error?.status === 403) return 'unauthorized';
  if (
    [
      'ECONNREFUSED',
      'ENOTFOUND',
      'EHOSTUNREACH',
      'ENETUNREACH',
      'EAI_AGAIN',
      'ETIMEDOUT',
      'UND_ERR_CONNECT_TIMEOUT',
      'UND_ERR_HEADERS_TIMEOUT',
      'CERT_HAS_EXPIRED',
      'DEPTH_ZERO_SELF_SIGNED_CERT',
      'ERR_TLS_CERT_ALTNAME_INVALID',
      'UNABLE_TO_VERIFY_LEAF_SIGNATURE',
      'SELF_SIGNED_CERT_IN_CHAIN',
      'UNABLE_TO_GET_ISSUER_CERT_LOCALLY',
      'ERR_TLS_HANDSHAKE_TIMEOUT',
    ].includes(error?.cause?.code ?? error?.code) ||
    error?.name === 'TimeoutError'
  )
    return 'hq_unreachable';
  return 'unknown';
}
/** Read the atomic main snapshot without acquiring or changing its writer lock.
 * Its device partition comes exclusively from previously verified credentials.
 * @param {string} directory @param {any} credential */
export async function readDeviceLedger(directory, credential) {
  if (!credential?.claims) return null;
  const c = credential.claims;
  const key = createHash('sha256')
    .update(JSON.stringify([c.iss, c.tid, c.did]))
    .digest('hex');
  let file;
  try {
    file = await open(
      join(directory, 'devices', key, 'host', 'ledger.json'),
      constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
    );
    const stat = await file.stat();
    if (!stat.isFile() || stat.size > 33554432)
      throw new Error('local ledger unavailable');
    const saved = JSON.parse(await file.readFile('utf8'));
    if (saved.identity?.tenant !== c.tid)
      throw new Error('local ledger ownership changed');
    const ledger = await openLedger({
      identity: saved.identity,
      store: {
        open: async () => ({
          load: async () => saved,
          commit: async () => {
            throw new Error('read only');
          },
          close: async () => {},
        }),
      },
    });
    try {
      return ledger.peek();
    } finally {
      await ledger.close();
    }
  } catch (error) {
    if (/** @type {any} */ (error).code === 'ENOENT') return null;
    throw new Error('local ledger unavailable');
  } finally {
    await file?.close();
  }
}
/** @param {any} state @param {number} [now] */
export function baseStatus(state, now = Date.now()) {
  const expires = state.expiresAt ?? null;
  const expired = expires !== null && expires <= now;
  const credential = {
    state: expired
      ? 'expired'
      : state.verified
        ? 'verified'
        : state.loggedIn === false
          ? 'signed_out'
          : 'unknown',
    expiresAt: expires === null ? null : new Date(expires).toISOString(),
    remainingSeconds:
      expires === null ? null : Math.max(0, Math.floor((expires - now) / 1000)),
  };
  const messageReason =
    state.reason === 'unconfigured'
      ? 'unconfigured'
      : state.network?.connected
        ? null
        : state.network?.reason === 'keyset_expired'
          ? 'keyset_expired'
          : state.network?.available
            ? 'message_network_degraded'
            : 'unknown';
  return {
    headquarters: {
      origin: state.origin ?? null,
      reachable: state.reachable ?? null,
      lastSuccessAt: state.lastSuccessAt ?? null,
      reason: state.reachable === true ? null : (state.reason ?? 'unknown'),
    },
    credential,
    messageNetwork: {
      ready: state.network?.connected === true,
      reason: messageReason,
    },
    remoteTools: {
      available: state.remoteAvailable === true,
      reason: state.remoteAvailable
        ? null
        : expired
          ? 'credential_expired'
          : (state.reason ?? 'unknown'),
    },
    sending: {
      available:
        !state.verified || expired || !state.network?.connected ? false : null,
      reason:
        state.reason === 'unconfigured'
          ? 'unconfigured'
          : state.reachable === false
            ? state.reason === 'hq_unreachable'
              ? 'hq_unreachable'
              : (state.reason ?? 'unknown')
            : !state.network?.connected
              ? 'message_network_degraded'
              : expired
                ? 'credential_expired'
                : state.verified
                  ? null
                  : 'unauthorized',
      offlineNewMessages: 'unavailable',
    },
    queued: state.queued ?? null,
  };
}
/** Native registration checks reject forged/archived/subagent executions without network I/O.
 * @param {{ctx:any,getState:()=>any,readLocal:()=>Promise<any>,probe:(path:string,signal:AbortSignal)=>Promise<any>}} options */
export function registerBaseTools({ ctx, getState, readLocal, probe }) {
  let closed = false;
  let diagnosing = false;
  /** @type {(()=>void)[]} */ const disposers = [];
  const descriptions = [
    '\u8bfb\u53d6\u672c\u5730\u7f51\u7edc\u72b6\u6001 / Read cached network status.',
    '\u5206\u6bb5\u8bca\u65ad\u7f51\u7edc / Diagnose configured headquarters.',
    '\u8bfb\u53d6\u5df2\u9a8c\u8bc1\u8eab\u4efd / Read verified identity.',
    '\u5206\u9875\u8bfb\u53d6\u672c\u5730\u6536\u4ef6\u7bb1 / Read this session inbox.',
  ];
  try {
    for (const [index, name] of BASE_TOOL_NAMES.entries())
      disposers.push(
        ctx.tools.register({
          name,
          description: descriptions[index],
          parameters: {
            type: 'object',
            properties:
              name === 'auriga_net_inbox'
                ? {
                    limit: { type: 'integer', minimum: 1, maximum: 50 },
                    cursor: { type: 'integer', minimum: 0, maximum: 1000000 },
                  }
                : {},
            additionalProperties: false,
          },
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (/** @type {any} */ _args, /** @type {any} */ value) => [
              { type: 'text', text: JSON.stringify(value) },
            ],
          },
          async execute(/** @type {any} */ args, /** @type {any} */ exec) {
            let assertSource = () => {};
            const check = () => {
              assertSource();
              if (closed) throw new Error('network.unauthorized');
            };
            check();
            const native = registrationSession(ctx, exec);
            await native.verify();
            check();
            if (
              !args ||
              typeof args !== 'object' ||
              Array.isArray(args) ||
              Object.keys(args).some(
                (k) =>
                  name !== 'auriga_net_inbox' ||
                  !['limit', 'cursor'].includes(k),
              )
            )
              throw new Error('network.protocol_error');
            const state = getState();
            assertSource = state.assertCurrent ?? (() => {});
            // An admitted native session may inspect outages, including expired credentials.
            if (name === 'auriga_net_status') {
              let queued = null;
              try {
                const local = await readLocal();
                queued = local
                  ? Object.values(local.durable?.submissions ?? {}).filter(
                      (/** @type {any} */ s) => s.result === null,
                    ).length
                  : 0;
              } catch {
                /* Explicit unknown. */
              }
              native.check();
              check();
              return baseStatus({ ...state, queued });
            }
            if (name === 'auriga_net_identity') {
              const local = state.verified ? await readLocal() : null;
              native.check();
              check();
              return {
                loggedIn: state.verified === true,
                credential: baseStatus(state).credential,
                memberId: state.verified ? (state.memberId ?? null) : null,
                displayName: state.verified
                  ? (state.displayName ?? null)
                  : null,
                nodeAddress: local
                  ? formatAddress({
                      realm: local.identity.realm,
                      node: local.identity.node,
                      local: '0',
                    })
                  : null,
              };
            }
            if (name === 'auriga_net_inbox') {
              const { limit = 20, cursor = 0 } = args;
              if (
                !Number.isSafeInteger(limit) ||
                limit < 1 ||
                limit > 50 ||
                !Number.isSafeInteger(cursor) ||
                cursor < 0 ||
                cursor > 1000000
              )
                throw new Error('network.protocol_error');
              if (!state.verified) throw new Error('network.unauthorized');
              const local = await readLocal();
              native.check();
              check();
              const now = Date.now();
              const rows = Object.values(local?.durable?.inbound ?? {})
                .filter((/** @type {any} */ r) => {
                  const binding = local.bindings[r.local];
                  const receive = local.durable?.receive?.[r.local];
                  return (
                    r.hostSessionId === native.id &&
                    binding?.bindingId === r.bindingId &&
                    binding.hostSessionId === native.id &&
                    binding.kind === 'session' &&
                    binding.state === 'bound' &&
                    binding.lifecycle === 'open' &&
                    ['replies_only', 'thread_messages'].includes(
                      binding.receiveMode,
                    ) &&
                    receive?.enabled === true &&
                    receive.pending?.enabled !== false &&
                    ['accepted', 'committed'].includes(r.stage) &&
                    ['admitted', 'already_admitted'].includes(
                      r.admission?.result,
                    ) &&
                    Date.parse(r.delivery.message?.expires_at) > now
                  );
                })
                .sort((/** @type {any} */ a, /** @type {any} */ b) =>
                  a.recipientId.localeCompare(b.recipientId),
                );
              return {
                items: rows
                  .slice(cursor, cursor + limit)
                  .map((/** @type {any} */ r) => ({
                    messageId: r.delivery.message?.message_id,
                    status: r.stage,
                    text: r.delivery.message?.body?.text,
                  })),
                ...(cursor + limit < rows.length
                  ? { nextCursor: cursor + limit }
                  : {}),
              };
            }
            if (diagnosing) throw new Error('network.overloaded');
            diagnosing = true;
            try {
              /** @type {{segment:string,status:string,reason:string|null,source?:string}[]} */
              const segments = [
                { segment: 'local', status: 'ready', reason: null },
              ];
              if (state.reason === 'unconfigured')
                return {
                  segments: [
                    ...segments,
                    {
                      segment: 'ingress',
                      status: 'unavailable',
                      reason: 'unconfigured',
                    },
                  ],
                  firstFailure: 'ingress',
                };
              for (const [segment, path] of [
                ['ingress', DIAGNOSTIC_PATH],
                ['hq_api', DIAGNOSTIC_PATH],
              ]) {
                native.check();
                check();
                try {
                  const result = await probe(
                    path,
                    AbortSignal.any([
                      ...(exec.signal ? [exec.signal] : []),
                      AbortSignal.timeout(3000),
                    ]),
                  );
                  const ready =
                    segment === 'ingress'
                      ? Number.isInteger(result?.httpStatus)
                      : result?.apiReady === true;
                  segments.push({
                    segment,
                    status: ready ? 'ready' : 'unavailable',
                    reason: ready ? null : (result?.reason ?? 'unknown'),
                  });
                  if (!ready) break;
                } catch (error) {
                  segments.push({
                    segment,
                    status: 'unavailable',
                    reason: connectionReason(error),
                  });
                  break;
                }
              }
              native.check();
              check();
              const status = baseStatus(getState());
              segments.push({
                segment: 'message_network',
                status: status.messageNetwork.ready ? 'ready' : 'unknown',
                reason: status.messageNetwork.reason,
                source: 'cache',
              });
              return {
                segments,
                firstFailure:
                  segments.find((s) => s.status !== 'ready')?.segment ?? null,
              };
            } finally {
              diagnosing = false;
            }
          },
        }),
      );
  } catch (error) {
    for (const dispose of disposers) dispose();
    throw error;
  }
  return () => {
    closed = true;
    for (const dispose of disposers.splice(0)) dispose();
  };
}
