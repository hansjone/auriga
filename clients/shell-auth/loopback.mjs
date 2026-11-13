import { createServer } from 'node:http';
import { randomBytes, createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';

const secret = () => randomBytes(32).toString('base64url');
const digest = (s) => createHash('sha256').update(s).digest('hex');
const validSecret = (s) => typeof s === 'string' && /^[\w-]{43}$/.test(s);
const denied = () =>
  Object.assign(new Error('common.forbidden'), { status: 403 });

/** Browser control only. Native clients must use an OS-authenticated transport. */
export async function createLoopback({
  broker,
  validate,
  now = () => performance.now(),
}) {
  if (typeof validate !== 'function')
    throw new Error('frozen contract validator required');
  const tickets = new Map(),
    sessions = new Map();
  let origin, port;
  const server = createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader(
      'Content-Security-Policy',
      "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
    );
    const send = (status, body) => {
      res.writeHead(status, {
        'Content-Type':
          status >= 400 ? 'application/problem+json' : 'application/json',
      });
      res.end(JSON.stringify(body));
    };
    try {
      if (
        req.headers.host !== new URL(origin).host ||
        req.socket.remoteAddress !== '127.0.0.1'
      )
        throw denied();
      if (req.method === 'GET' && ['/', '/page.js'].includes(req.url)) {
        const file = req.url === '/' ? 'index.html' : 'page.js';
        res.writeHead(200, {
          'Content-Type': file.endsWith('html')
            ? 'text/html; charset=utf-8'
            : 'text/javascript; charset=utf-8',
        });
        res.end(readFileSync(new URL(`./public/${file}`, import.meta.url)));
        return;
      }
      if (
        req.method !== 'POST' ||
        req.headers.origin !== origin ||
        req.headers['content-type'] !== 'application/json' ||
        req.headers['sec-fetch-site'] !== 'same-origin' ||
        req.headers['sec-fetch-mode'] !== 'cors' ||
        req.headers['sec-fetch-dest'] !== 'empty'
      )
        throw denied();
      if (!['/loopback/exchange', '/loopback/messages'].includes(req.url))
        throw denied();
      for (const [k, v] of tickets) if (v.until <= now()) tickets.delete(k);
      for (const [k, v] of sessions) if (v.until <= now()) sessions.delete(k);
      const parts = [];
      let size = 0;
      for await (const part of req) {
        size += part.length;
        if (size > 8192) throw denied();
        parts.push(part);
      }
      let body;
      try {
        body = JSON.parse(Buffer.concat(parts).toString('utf8'));
      } catch {
        throw denied();
      }
      if (!body || Array.isArray(body) || typeof body !== 'object')
        throw denied();
      if (req.url === '/loopback/exchange') {
        if (!validate('ShellAuthLoopbackExchange', body)) throw denied();
        const key = digest(body.ticket),
          ticket = tickets.get(key);
        tickets.delete(key); // atomic before any await
        if (!ticket || ticket.until <= now() || ticket.epoch !== broker.epoch)
          throw denied();
        if (sessions.size >= 32) throw denied();
        const pageToken = secret(),
          csrf = secret();
        sessions.set(digest(pageToken), {
          csrf: digest(csrf),
          until: now() + 900000,
          epoch: broker.epoch,
          clientNonce: body.clientNonce,
          owner: key,
        });
        send(200, {
          pageToken,
          csrf,
          shellEpoch: broker.epoch,
          origin,
          port,
          clientNonce: body.clientNonce,
          expiresAt: new Date(Date.now() + 900000).toISOString(),
        });
        return;
      }
      const token = req.headers['x-auriga-shell-token'],
        csrf = req.headers['x-auriga-shell-csrf'];
      if (!validSecret(token) || !validSecret(csrf)) throw denied();
      const session = sessions.get(digest(token));
      if (
        !session ||
        session.csrf !== digest(csrf) ||
        session.until <= now() ||
        session.epoch !== broker.epoch
      )
        throw denied();
      if (!validate('ShellAuthPageCommand', body)) throw denied();
      const result = await broker.pageCommand(body, session.owner);
      if (session.until <= now() || session.epoch !== broker.epoch)
        throw denied();
      if (!validate('ShellAuthEvent', result))
        throw new Error('invalid broker projection');
      send(200, result);
    } catch (error) {
      const status =
        error.status === 409 ? 409 : error.status === 403 ? 403 : 503;
      send(status, {
        title:
          status === 409
            ? 'Conflict'
            : status === 403
              ? 'Forbidden'
              : 'Unavailable',
        status,
        code:
          status === 409
            ? 'common.conflict'
            : status === 403
              ? 'common.forbidden'
              : 'common.unavailable',
      });
    }
  });
  server.requestTimeout = 5000;
  server.headersTimeout = 5000;
  server.maxConnections = 32;
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(undefined));
  });
  const address = server.address();
  if (!address || typeof address === 'string')
    throw new Error('loopback address unavailable');
  port = address.port;
  origin = `http://127.0.0.1:${port}`;
  return {
    origin,
    issue() {
      for (const [k, v] of tickets) if (v.until <= now()) tickets.delete(k);
      if (tickets.size >= 32) throw denied();
      const ticket = secret();
      tickets.set(digest(ticket), {
        until: now() + 30000,
        epoch: broker.epoch,
        purpose: 'control',
      });
      return `${origin}/#${ticket}`;
    },
    async close() {
      tickets.clear();
      sessions.clear();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}
