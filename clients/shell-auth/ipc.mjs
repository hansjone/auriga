import { spawn } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { requireValid } from './schema.mjs';
import { HostChannel } from './host.mjs';
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
  const host = business.host ?? new HostChannel(broker, business.streams),
    peers = new Map();
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
    for (const peer of peers.values()) host.disconnect(peer);
    peers.clear();
    if (!business.host) host.close();
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
      if (message.opened) {
        peers.set(
          message.opened,
          host.connect({
            verified: true,
            connectionId: message.opened,
            sid: message.sid,
            processStart: message.processStart,
            role: message.role,
          }),
        );
        return;
      }
      if (message.disconnected) {
        host.disconnect(peers.get(message.disconnected));
        peers.delete(message.disconnected);
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
      const { id, payload, connectionId } = message;
      const peer = peers.get(connectionId);
      host.checkPeer(peer);
      const owner = peer.id;
      if (payload.path === '/host/contexts') {
        const response = await host.command(payload.body, peer);
        send({ id, payload: { requestId: payload.requestId, body: response } });
        return;
      }
      if (payload.path === '/host/credentials') {
        const response = await host.credentials(payload.body, peer);
        send({ id, payload: { requestId: payload.requestId, body: response } });
        return;
      }
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
  let readyResolve,
    readyReject,
    closed = false;
  const ready = new Promise((resolve, reject) => {
    readyResolve = resolve;
    readyReject = reject;
  });
  // A caller may close before the handshake or while callbacks are retiring.
  // Settle every waiter and handle stdin errors before ending the helper pipe.
  void ready.catch(() => {});
  const disconnect = (error) => {
    closed = true;
    readyReject(error);
    const entries = [...pending.values()];
    pending.clear();
    for (const entry of entries) entry.reject(error);
  };
  child.on('error', disconnect);
  child.stdin.on('error', disconnect);
  child.on('exit', () => disconnect(new Error('shell IPC disconnected')));
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
      if (closed) throw new Error('shell IPC disconnected');
      return new Promise((resolve, reject) => {
        pending.set(command.requestId, { resolve, reject });
        child.stdin.write(JSON.stringify(command) + '\n');
      });
    },
    async hostContext(message) {
      const result = await this.call({
        requestId: randomUUID(),
        path: '/host/contexts',
        body: message,
      });
      return requireValid('ShellAuthHostContextResult', result.body);
    },
    async enterprise(message) {
      const result = await this.call({
        requestId: randomUUID(),
        path: '/host/credentials',
        body: message,
      });
      return requireValid('ShellAuthHostCredentialsResult', result.body);
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
      if (closed) throw new Error('shell IPC disconnected');
      pending.set(command.requestId, {
        listener,
        reject: () => listener(null),
      });
      child.stdin.write(JSON.stringify(command) + '\n');
    },
    close() {
      if (closed) return;
      disconnect(new Error('shell IPC disconnected'));
      child.stdin.end();
      child.kill();
      lines.close();
    },
  };
}
