// @ts-nocheck
import { fail } from './protocol.js';

/** Private enterprise header names are deployment inputs. Credentials stay in this call. */
export function createEnterpriseOutbound({ config, fetchImpl = fetch }) {
  return async (request, material) => {
    const { empNoHeader, authValueHeader } = config();
    const headerName = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
    if (
      typeof empNoHeader !== 'string' ||
      typeof authValueHeader !== 'string' ||
      !headerName.test(empNoHeader) ||
      !headerName.test(authValueHeader) ||
      empNoHeader.toLowerCase() === authValueHeader.toLowerCase()
    )
      fail('common.unavailable');
    const headers = {
      ...request.headers,
      [empNoHeader]: material.empNo,
      [authValueHeader]: material.token,
    };
    let body = request.body;
    if (body !== undefined && body !== null && typeof body !== 'string') {
      body = JSON.stringify(body);
      if (
        !Object.keys(headers).some(
          (key) => key.toLowerCase() === 'content-type',
        )
      )
        headers['content-type'] = 'application/json';
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), request.timeoutMs);
    try {
      const response = await fetchImpl(request.url, {
        method: request.method,
        headers,
        body: ['GET', 'HEAD'].includes(request.method) ? undefined : body,
        redirect: 'error',
        signal: controller.signal,
      });
      const text = await response.text();
      let json = null;
      try {
        json = JSON.parse(text);
      } catch {
        /* A business response may be plain text. */
      }
      return {
        statusCode: response.status,
        headers: Object.fromEntries(response.headers),
        body: text,
        json,
      };
    } finally {
      clearTimeout(timer);
      controller.abort();
    }
  };
}
