import { connectionReason, DIAGNOSTIC_PATH } from './base-tools.js';
export { DIAGNOSTIC_PATH } from './base-tools.js';

/** A received HTTP response proves ingress reachability even if the API is unhealthy.
 * The body and headers never leave the ordinary bounded JSON client.
 * @param {{request:(path:string,options:any)=>Promise<any>}} platform
 * @param {string} path @param {AbortSignal} signal */
export async function probeEndpoint(platform, path, signal) {
  if (path !== DIAGNOSTIC_PATH) throw new Error('untrusted diagnostic path');
  /** @type {number|null} */ let httpStatus = null;
  try {
    const data = await platform.request(DIAGNOSTIC_PATH, {
      signal,
      redirect: 'manual',
      onResponse: (/** @type {number} */ status) => {
        httpStatus = status;
      },
    });
    const apiReady = Array.isArray(data?.items);
    return { httpStatus, apiReady, reason: apiReady ? null : 'unknown' };
  } catch (error) {
    if (httpStatus === null) throw error;
    const reason = connectionReason(error);
    return {
      httpStatus,
      apiReady: false,
      reason: reason === 'hq_unreachable' ? 'unknown' : reason,
    };
  }
}
