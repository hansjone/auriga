import { spawn } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { requireValid } from './schema.mjs';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';

/** Windows helper authenticates SID, local pipe, process ancestry and capability. */
export async function startIpc({ helper, broker, business }) {
  if (process.platform !== 'win32')
    throw new Error('OS peer verifier unavailable');
  const capability = randomBytes(32).toString('base64url');
  const pipe = 'auriga-shell-' + randomBytes(16).toString('hex');
  const child = spawn(helper, ['server', pipe], {
    windowsHide: true,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  const waiters = new Map(),
    subscriptions = new Map();
  let readyResolve,
    readyReject,
    closed = false;
  const ready = new Promise((resolve, reject) => {
    readyResolve = resolve;
    readyReject = reject;
  });
  const send = (value) => {
    if (!closed && !child.stdin.destroyed)
      child.stdin.write(JSON.stringify(value) + '\n');
  };
  const cleanup = () => {
    closed = true;
    readyReject(new Error('shell IPC helper unavailable'));
    for (const stop of subscriptions.values()) stop();
    subscriptions.clear();
    for (const { reject } of waiters.values())
      reject(new Error('shell IPC unavailable'));
    waiters.clear();
  };
  child.on('error', cleanup);
  child.on('exit', cleanup);
  child.stderr.resume(); // helper has only a fixed safe error; no raw output forwarded
  const lines = createInterface({ input: child.stdout });
  lines.on('line', async (line) => {
    if (line.length > 4194304) {
      child.kill();
      return;
    }
    let message;
    try {
      message = JSON.parse(line);
      if (message.listening === pipe) {
        readyResolve(undefined);
        return;
      }
      if (message.registered) {
        waiters.get(message.registered)?.resolve();
        waiters.delete(message.registered);
        return;
      }
      if (message.closed) {
        subscriptions.get(message.closed)?.();
        subscriptions.delete(message.closed);
        return;
      }
      const { id, payload, peerPid } = message;
      const owner = `windows:${peerPid}`;
      if (payload.path === '/business/events') {
        const stop = business.streams.subscribe(payload.body, owner, (event) =>
          send({
            id,
            payload: { requestId: payload.requestId, body: event },
            keep: true,
          }),
        );
        subscriptions.set(id, stop);
        return;
      }
      if (payload.path === '/business/streams') {
        const response = await business.streams.command(payload.body, owner);
        send({ id, payload: { requestId: payload.requestId, body: response } });
        return;
      }
      // Fixed IPC routing envelope; its body/result use the frozen enterprise
      // schemas. This endpoint is never registered as browser HTTP.
      if (payload.path === '/dsh-team/agent-credentials') {
        requireValid('ShellAuthAgentCredentialsRequest', payload.body);
        const context = broker.guard();
        const credentials = await broker.enterprise();
        broker.assert(context);
        send({
          id,
          payload: { requestId: payload.requestId, body: credentials },
        });
        return;
      }
      if (payload.action === 'subscribe') {
        const stop = broker.subscribe(payload, owner, (event) =>
          send({ id, payload: event, keep: true }),
        );
        subscriptions.set(id, stop);
        return;
      }
      const response = payload.call
        ? await business.call(payload, owner)
        : await broker.command(payload, owner);
      send({ id, payload: response });
    } catch (error) {
      if (message?.id && message.payload?.requestId)
        send({
          id: message.id,
          payload: {
            protocolVersion: 1,
            requestId: message.payload.requestId,
            type: 'error',
            code: [
              'common.conflict',
              'common.forbidden',
              'common.unauthenticated',
              'common.validation_failed',
            ].includes(error.code)
              ? error.code
              : 'common.unavailable',
          },
        });
    }
  });
  send({ capability });
  try {
    await Promise.race([
      ready,
      delay(5000).then(() => {
        throw new Error('shell IPC timeout');
      }),
    ]);
  } catch (error) {
    child.kill();
    throw error;
  }
  return {
    async attach(dsh) {
      const registered = new Promise((resolve, reject) => {
        waiters.set(dsh.pid, { resolve, reject });
      });
      send({ register: dsh.pid });
      await Promise.race([
        registered,
        delay(5000).then(() => {
          throw new Error('controlled child registration timeout');
        }),
      ]);
      if (!dsh.stdio[3])
        throw new Error('private inherited shell channel required');
      dsh.stdio[3].end(
        JSON.stringify({
          protocolVersion: 1,
          pipe,
          capability,
          helperPath: helper,
        }) + '\n',
      );
    },
    async close() {
      if (!closed) {
        const exited = once(child, 'exit');
        child.stdin.end();
        child.kill();
        await exited;
      }
      lines.close();
    },
  };
}

/** D calls this from the DSH backend, using descriptor read from inherited fd 3.
 * Do not serialize the descriptor into settings, argv, env, logs or HTTP. */
export function connectIpc(descriptor) {
  const child = spawn(descriptor.helperPath, ['client', descriptor.pipe], {
    windowsHide: true,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  child.stderr.resume();
  const lines = createInterface({ input: child.stdout }),
    pending = new Map();
  let readyResolve, readyReject;
  const ready = new Promise((resolve, reject) => {
    readyResolve = resolve;
    readyReject = reject;
  });
  child.on('error', readyReject);
  child.on('exit', () => {
    readyReject(new Error('shell IPC disconnected'));
    for (const entry of pending.values())
      entry.reject(new Error('shell IPC disconnected'));
    pending.clear();
  });
  lines.on('line', (line) => {
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      child.kill();
      return;
    }
    if (event.ready) {
      readyResolve(undefined);
      return;
    }
    const entry = pending.get(event.requestId);
    if (!entry) return;
    if (entry.listener) entry.listener(event);
    else {
      pending.delete(event.requestId);
      entry.resolve(event);
    }
  });
  child.stdin.write(
    JSON.stringify({ capability: descriptor.capability }) + '\n',
  );
  return {
    async call(command) {
      await ready;
      return new Promise((resolve, reject) => {
        pending.set(command.requestId, { resolve, reject });
        child.stdin.write(JSON.stringify(command) + '\n');
      });
    },
    async enterprise() {
      const result = await this.call({
        requestId: randomUUID(),
        path: '/dsh-team/agent-credentials',
        body: {},
      });
      if (result.type === 'error')
        throw new Error('enterprise credentials unavailable');
      return result.body;
    },
    async streamCommand(command) {
      const result = await this.call({
        requestId: randomUUID(),
        path: '/business/streams',
        body: command,
      });
      if (result.type === 'error')
        throw new Error('stream control unavailable');
      return result.body;
    },
    async subscribeStream(command, listener) {
      await this.subscribe(
        { requestId: randomUUID(), path: '/business/events', body: command },
        (result) => listener(result?.body ?? null),
      );
    },
    async subscribe(command, listener) {
      await ready;
      pending.set(command.requestId, {
        listener,
        reject: () => listener(null),
      });
      child.stdin.write(JSON.stringify(command) + '\n');
    },
    close() {
      child.stdin.end();
      child.kill();
      lines.close();
    },
  };
}
