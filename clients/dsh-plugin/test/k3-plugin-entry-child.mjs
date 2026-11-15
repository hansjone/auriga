import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const [pluginRoot, profile, configJson] = process.argv.slice(2);
const config = JSON.parse(configJson);
const routes = [],
  effects = [],
  jobs = [],
  tools = new Set(),
  sessions = new Map();
let hostCalls = 0,
  controlSessions = 0,
  lastState;
const server = createServer(async (req, res) => {
  for (const route of routes) {
    if (typeof route.path === 'string' && !req.url.startsWith(route.path))
      continue;
    const result = await route.handler(req, res);
    if (res.writableEnded || result) return;
  }
  res.writeHead(404);
  res.end();
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const owner = { principal: { kind: 'owner' } };
const ctx = {
  logger: { warn: (line) => process.stderr.write(line + '\n') },
  profileContext: { dir: profile },
  webServer: {
    protocol: 'http:',
    register(route) {
      routes.push(route);
      return () => {};
    },
  },
  connection: {
    admit() {
      return { peer: owner };
    },
    requestRejection() {},
    isTrusted() {
      return true;
    },
    authorizeIndex() {
      return true;
    },
    register() {
      return () => {};
    },
  },
  tools: {
    register(tool) {
      tools.add(tool.name);
      return () => tools.delete(tool.name);
    },
  },
  sessions: { get: (id) => sessions.get(id), flush() {} },
  sessionController: {
    create({ sessionId }) {
      controlSessions++;
      sessions.set(sessionId, { header: { id: sessionId } });
      return { sessionId };
    },
    prompt() {
      hostCalls++;
    },
  },
  on() {
    return () => {};
  },
  effect(fn) {
    effects.push(fn());
  },
  inject(names, fn) {
    if (names.every((name) => ctx[name]))
      jobs.push(Promise.resolve().then(() => fn(ctx)));
  },
};
const until = async (condition) => {
  const deadline = Date.now() + 25000;
  for (;;) {
    const result = await condition();
    if (result) return result;
    assert.ok(
      Date.now() < deadline,
      'plugin native shell/network entry timeout ' + JSON.stringify(lastState),
    );
    await delay(10);
  }
};
try {
  const plugin = await import(
    pathToFileURL(join(pluginRoot, 'src/plugin.js')).href
  );
  await plugin.apply(ctx, { connect: config });
  await Promise.all(jobs);
  const status = await until(async () => {
    const response = await fetch(origin + '/auriga/shell/status');
    if (response.status !== 200) return;
    const result = await response.json();
    lastState = {
      brokerAvailable: result.brokerAvailable,
      platform: result.platform,
      network: result.network,
    };
    if (result.network?.connected) return result;
  });
  assert.equal(status.loggedIn, true);
  assert.equal(status.network.connected, true);
  assert.equal(status.network.state, 'ready');
  assert.equal(tools.has('auriga_send_message'), false);
  assert.equal(hostCalls, 0);
  const logout = await fetch(origin + '/auriga/shell/command', {
    method: 'POST',
    headers: {
      origin,
      'content-type': 'application/json',
      'x-auriga-csrf': status.csrf,
    },
    body: JSON.stringify({ action: 'logout' }),
  });
  assert.equal(logout.status, 200);
  const devices = await readFile(join(profile, 'auriga-connect/connect.json'));
  assert.ok(devices.length);
  await writeFile(
    join(profile, 'entry-result.json'),
    JSON.stringify({
      status,
      logoutStatus: logout.status,
      hostCalls,
      controlSessions,
      hasSendTool: tools.has('auriga_send_message'),
    }),
  );
} finally {
  for (const dispose of effects.reverse()) await dispose?.();
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
}
