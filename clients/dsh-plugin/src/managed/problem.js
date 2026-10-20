// @ts-nocheck
/**
 * RFC 9457 problem+json helpers. Codes must exist in contracts/.
 * @param {number} status
 * @param {string} code
 * @param {string} [detail]
 * @param {Record<string, unknown>} [params]
 */
export function problem(status, code, detail, params) {
  /** @type {{ type: string, title: string, status: number, code: string, detail?: string, params?: Record<string, unknown> }} */
  const body = {
    type: 'about:blank',
    title:
      status === 401 ? 'Unauthorized' : status === 403 ? 'Forbidden' : 'Error',
    status,
    code,
  };
  if (detail) body.detail = detail;
  if (params) body.params = params;
  return body;
}

/**
 * @param {import('node:http').ServerResponse} res
 * @param {number} status
 * @param {string} code
 * @param {string} [detail]
 * @param {Record<string, unknown>} [params]
 */
export function sendProblem(res, status, code, detail, params) {
  const body = problem(status, code, detail, params);
  res.writeHead(status, {
    'content-type': 'application/problem+json; charset=utf-8',
    'cache-control': 'no-store',
  });
  res.end(JSON.stringify(body));
}
