import http from 'node:http';

const counters = [
  'runningTurns',
  'openTerminals',
  'activeImBots',
  'connectedClients',
];

// This allowlist is deliberately smaller than the request. Never persist raw
// headers, credentials, health.problems or unknown body properties.
function observation(req, raw) {
  const body = JSON.parse(raw);
  const token = /^Bearer ait_([a-f0-9-]{36})\.[A-Za-z0-9_-]+$/.exec(
    req.headers.authorization || '',
  );
  if (!token) throw new Error('invalid heartbeat credential shape');
  const activity = {};
  for (const field of counters) {
    const value = body.activity?.[field];
    if (!(value === null || (Number.isSafeInteger(value) && value >= 0)))
      throw new Error('invalid heartbeat counter');
    activity[field] = value;
  }
  const last = body.activity?.lastInteractionAt;
  if (last !== undefined) {
    if (
      typeof last !== 'string' ||
      !/^\d{4}-\d\d-\d\dT[0-9:.+-]+Z?$/.test(last) ||
      !Number.isFinite(Date.parse(last))
    )
      throw new Error('invalid heartbeat time');
    activity.lastInteractionAt = new Date(last).toISOString();
  }
  if (typeof body.health?.ok !== 'boolean')
    throw new Error('invalid heartbeat health');
  const versions = {};
  for (const field of ['pluginVersion', 'dshVersion']) {
    if (
      typeof body[field] !== 'string' ||
      !/^[a-zA-Z0-9.+_-]{1,64}$/.test(body[field])
    )
      throw new Error('invalid heartbeat version');
    versions[field] = body[field];
  }
  // The upstream 200 proves the opaque token was authenticated. Persist only
  // its public instance routing UUID, never the opaque credential suffix.
  return {
    receivedAt: new Date().toISOString(),
    instanceId: token[1],
    activity,
    health: { ok: body.health.ok },
    ...versions,
    upstreamStatus: 200,
  };
}

/** Test-only transparent observer between edge and the actual workspace. */
export function createHeartbeatObserver({
  upstream,
  onHeartbeat,
  onCaptureError = () => {},
}) {
  const target = new URL(upstream);
  if (
    target.protocol !== 'http:' ||
    target.hostname !== '127.0.0.1' ||
    target.username ||
    target.password ||
    target.pathname !== '/' ||
    target.search ||
    target.hash
  )
    throw new Error('observer upstream must be a local HTTP base');
  const server = http.createServer(async (req, res) => {
    const heartbeat =
      req.method === 'POST' &&
      req.url === '/api/workspace/v1/instance/heartbeat';
    let raw;
    if (heartbeat) {
      const chunks = [];
      let size = 0;
      try {
        for await (const chunk of req) {
          size += chunk.length;
          if (size > 65536) {
            res.writeHead(413).end();
            return;
          }
          chunks.push(chunk);
        }
        raw = Buffer.concat(chunks);
      } catch {
        if (!res.destroyed) res.writeHead(400).end();
        return;
      }
    }
    const proxy = http.request(
      {
        hostname: target.hostname,
        port: target.port,
        path: req.url,
        method: req.method,
        headers: { ...req.headers, host: 'portal.example.com' },
        timeout: 15000,
      },
      (response) => {
        if (heartbeat && response.statusCode === 200) {
          try {
            onHeartbeat(observation(req, raw));
          } catch {
            onCaptureError();
          }
        }
        res.writeHead(response.statusCode, response.headers);
        response.pipe(res);
      },
    );
    proxy.on('timeout', () => proxy.destroy());
    proxy.on('error', () => {
      if (!res.headersSent) res.writeHead(502);
      res.end();
    });
    req.on('aborted', () => proxy.destroy());
    res.on('close', () => {
      if (!res.writableFinished) proxy.destroy();
    });
    if (heartbeat) proxy.end(raw);
    else req.pipe(proxy);
  });
  server.headersTimeout = 10000;
  server.requestTimeout = 15000;
  return server;
}
