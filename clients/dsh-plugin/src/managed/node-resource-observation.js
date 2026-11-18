import { timingSafeEqual } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { sendProblem } from './problem.js';

export const NODE_RESOURCE_PATH = '/auriga-node-resource-catalog';
// The renderer is loaded separately from this package. The binding is an
// in-process handler on this instance's server, never an HTTP route or header.
const OBSERVER = Symbol.for('auriga.node-resource-observer.v1');
const BROWSER_HEADERS = ['auriga-identity', 'cookie', 'origin', 'upgrade'];

/**
 * @param {import('node:http').IncomingMessage} req
 * @param {string | undefined} tokenFile
 */
function authorized(req, tokenFile) {
  const headers = req.headers;
  const supplied = headers.authorization;
  if (
    req.method !== 'GET' ||
    req.url !== NODE_RESOURCE_PATH ||
    !tokenFile ||
    typeof supplied !== 'string' ||
    req.headersDistinct?.authorization?.length !== 1 ||
    BROWSER_HEADERS.some((name) => name in headers) ||
    Object.keys(headers).some((name) => name.startsWith('sec-fetch-')) ||
    /(?:^|,)\s*upgrade\s*(?:,|$)/i.test(headers.connection ?? '')
  )
    return false;
  try {
    const token = readFileSync(tokenFile, 'utf8').trim();
    if (!token) return false;
    const expected = Buffer.from('Bearer ' + token);
    const actual = Buffer.from(supplied);
    return (
      actual.length === expected.length && timingSafeEqual(actual, expected)
    );
  } catch {
    return false;
  }
}

/**
 * @typedef {(req: import('node:http').IncomingMessage, res: import('node:http').ServerResponse, canPublish: () => boolean) => Promise<void>} Observer
 * @param {import('node:http').Server & {[OBSERVER]?: Observer}} server
 * @param {{tokenFile?: string, revoked: () => boolean}} options
 */
export function createNodeResourceObservation(server, { tokenFile, revoked }) {
  /** @type {(req: import('node:http').IncomingMessage, res: import('node:http').ServerResponse) => Promise<void>} */
  const dispatch = async (req, res) => {
    if (!authorized(req, tokenFile) || revoked()) {
      sendProblem(res, 401, 'common.unauthenticated');
      return;
    }
    const observer = Object.hasOwn(server, OBSERVER) && server[OBSERVER];
    if (typeof observer !== 'function') {
      sendProblem(res, 503, 'common.unavailable');
      return;
    }
    try {
      await observer(
        req,
        res,
        () =>
          !revoked() &&
          server[OBSERVER] === observer &&
          authorized(req, tokenFile),
      );
    } catch {
      if (res.headersSent) res.destroy();
      else sendProblem(res, 503, 'common.unavailable');
    }
  };
  return dispatch;
}
