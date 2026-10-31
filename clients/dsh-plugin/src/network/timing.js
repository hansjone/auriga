/** One WAN operation budget; nested work and retries keep the original parent
 * cancellation. The environment belongs to the host, never to model input.
 * @param {number} [operationMs] */
export function resolveNetworkTiming(operationMs = 30000) {
  if (
    !Number.isSafeInteger(operationMs) ||
    operationMs < 10000 ||
    operationMs > 120000
  )
    throw new Error('invalid network operation budget');
  const authMs = Math.floor(operationMs / 3);
  return Object.freeze({
    operationMs,
    rpcMs: Math.floor((operationMs * 2) / 3),
    authorityMs: Math.floor(operationMs / 6),
    authMs,
    redeemMs: authMs - 2000,
    graceMs: Math.floor(operationMs / 2),
  });
}
export const networkTiming = resolveNetworkTiming(
  Number(process.env.AURIGA_NETWORK_OPERATION_TIMEOUT_MS ?? 30000),
);
