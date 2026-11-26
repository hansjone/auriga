/** In-process Unix domain socket IPC for Desktop shell-auth (linux-x64). */
import { createServer, createConnection } from 'node:net';
import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  unlinkSync,
} from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { randomBytes, randomUUID } from 'node:crypto';
import { createInterface } from 'node:readline';
import { HostChannel } from '../host.mjs';
import { requireValid } from '../schema.mjs';

const PEERCRED_HELPER = join(
  dirname(fileURLToPath(import.meta.url)),
  'peercred',
);

function runtimeDir() {
  const base = process.env.XDG_RUNTIME_DIR || tmpdir();
  const dir = join(base, 'auriga-shell-auth');
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  if (lstatSync(dir).uid !== process.getuid())
    throw new Error('runtime directory owner mismatch');
  chmodSync(dir, 0o700);
  return dir;
}

function peerUid(pid) {
  const status = readFileSync(`/proc/${pid}/status`, 'utf8');
  const line = status.split('\n').find((row) => row.startsWith('Uid:'));
  if (!line) return null;
  const uid = Number(line.split(/\s+/)[1]);
  return Number.isSafeInteger(uid) ? uid : null;
}

function startTicks(pid) {
  const start = readFileSync(`/proc/${pid}/stat`, 'utf8');
  const close = start.lastIndexOf(')');
  return start.slice(close + 2).split(' ')[19];
}

function controlled(children, pid) {
  if (!children.has(pid)) return false;
  try {
    return (
      children.get(pid) === startTicks(pid) && peerUid(pid) === process.getuid()
    );
  } catch {
    return false;
  }
}

/** Kernel SO_PEERCRED identity for an accepted Unix-domain connection. */
function peerCredentials(socket) {
  if (typeof socket?.getPeerCredentials === 'function') {
    const creds = socket.getPeerCredentials();
    if (
      creds &&
      Number.isSafeInteger(creds.pid) &&
      creds.pid >= 1 &&
      Number.isSafeInteger(creds.uid)
    )
      return creds;
  }
  const handle = socket?._handle;
  if (handle && typeof handle.getPeerCredentials === 'function') {
    const creds = handle.getPeerCredentials();
    if (
      creds &&
      Number.isSafeInteger(creds.pid) &&
      creds.pid >= 1 &&
      Number.isSafeInteger(creds.uid)
    )
      return creds;
  }
  const fd = handle?.fd;
  if (!Number.isInteger(fd) || fd < 0)
    throw new Error('peer credentials unavailable');
  const stdio = ['ignore', 'pipe', 'pipe', fd];
  let result;
  if (existsSync(PEERCRED_HELPER))
    result = spawnSync(PEERCRED_HELPER, [], {
      encoding: 'utf8',
      timeout: 3000,
      stdio,
    });
  else {
    // Source-tree / test fallback when the packaged helper is not built yet.
    result = spawnSync(
      'python3',
      [
        '-c',
        'import os,socket,struct\n' +
          's=socket.fromfd(3,socket.AF_UNIX,socket.SOCK_STREAM)\n' +
          'c=s.getsockopt(1,17,struct.calcsize("3i"))\n' +
          'pid,uid,gid=struct.unpack("3i",c)\n' +
          'print(pid,uid,gid)\n' +
          'os.close(s.fileno())\n',
      ],
      { encoding: 'utf8', timeout: 3000, stdio },
    );
  }
  if (result.status !== 0) throw new Error('peer credentials unavailable');
  const [pidText, uidText, gidText] = (result.stdout || '').trim().split(/\s+/);
  const pid = Number(pidText),
    uid = Number(uidText),
    gid = Number(gidText);
  if (
    !Number.isSafeInteger(pid) ||
    pid < 1 ||
    !Number.isSafeInteger(uid) ||
    !Number.isSafeInteger(gid)
  )
    throw new Error('peer credentials unavailable');
  return { pid, uid, gid };
}

export async function startUnixIpc({ broker, business, directory }) {
  const capability = randomBytes(32).toString('base64url');
  const dir = directory ?? runtimeDir();
  const pipe = join(
    dir,
    'auriga-shell-' + randomBytes(16).toString('hex') + '.sock',
  );
  if (existsSync(pipe)) unlinkSync(pipe);
  const host = business.host ?? new HostChannel(broker, business.streams);
  const peers = new Map();
  const subscriptions = new Map();
  const children = new Map();
  const sockets = new Set();
  let closed = false;
  let active = 0;

  const cleanup = () => {
    if (closed) return;
    closed = true;
    for (const peer of peers.values()) host.disconnect(peer);
    peers.clear();
    if (!business.host) host.close();
    for (const stop of subscriptions.values()) stop();
    subscriptions.clear();
    for (const socket of sockets) {
      try {
        socket.destroy();
      } catch {
        /* best effort */
      }
    }
    sockets.clear();
    try {
      server.close();
    } catch {
      /* already closed */
    }
    try {
      if (existsSync(pipe)) unlinkSync(pipe);
    } catch {
      /* best effort */
    }
  };

  const sendPayload = (socket, value) => {
    if (closed || socket.destroyed || !socket.writable) return;
    try {
      socket.write(JSON.stringify(value) + '\n');
    } catch {
      /* drop */
    }
  };

  const handleRequest = async (socket, connectionId, payload) => {
    const peer = peers.get(connectionId);
    host.checkPeer(peer);
    const owner = peer.id;
    const id = randomUUID();
    if (payload.path === '/host/local-uac') {
      sendPayload(socket, {
        requestId: payload.requestId,
        body: await host.localUac(payload.body, peer),
      });
      return;
    }
    if (payload.path === '/host/contexts') {
      sendPayload(socket, {
        requestId: payload.requestId,
        body: await host.command(payload.body, peer),
      });
      return;
    }
    if (payload.path === '/host/credentials') {
      sendPayload(socket, {
        requestId: payload.requestId,
        body: await host.credentials(payload.body, peer),
      });
      return;
    }
    if (payload.path === '/business/events') {
      const stop = business.streams.subscribe(payload.body, owner, (event) =>
        sendPayload(socket, {
          requestId: payload.requestId,
          body: event,
          keep: true,
        }),
      );
      if (closed) stop();
      else subscriptions.set(id, stop);
      return;
    }
    if (payload.path === '/business/streams') {
      sendPayload(socket, {
        requestId: payload.requestId,
        body: await business.streams.command(payload.body, owner),
      });
      return;
    }
    if (payload.action === 'subscribe') {
      const stop = broker.subscribe(payload, owner, (event) =>
        sendPayload(socket, event),
      );
      if (closed) stop();
      else subscriptions.set(id, stop);
      return;
    }
    sendPayload(
      socket,
      payload.call
        ? await business.call(payload, owner)
        : await broker.command(payload, owner),
    );
  };

  const server = createServer((socket) => {
    if (closed || active >= 8) {
      socket.destroy();
      return;
    }
    active += 1;
    sockets.add(socket);
    let connectionId = null;
    let peerPid = null;
    const lines = createInterface({ input: socket });
    const lease = setInterval(() => {
      if (peerPid == null || controlled(children, peerPid)) return;
      socket.destroy();
    }, 500);
    lease.unref?.();
    const finish = () => {
      clearInterval(lease);
      active -= 1;
      sockets.delete(socket);
      lines.close();
      if (connectionId) {
        host.disconnect(peers.get(connectionId));
        peers.delete(connectionId);
      }
    };
    socket.on('close', finish);
    socket.on('error', () => {});
    lines.on('line', async (line) => {
      if (line.length > 4194304) {
        socket.destroy();
        return;
      }
      let message;
      try {
        message = JSON.parse(line);
      } catch {
        socket.destroy();
        return;
      }
      try {
        if (!connectionId) {
          let creds;
          try {
            creds = peerCredentials(socket);
          } catch {
            socket.destroy();
            return;
          }
          if (
            typeof message.capability !== 'string' ||
            message.capability !== capability ||
            creds.uid !== process.getuid() ||
            !controlled(children, creds.pid) ||
            (Number.isSafeInteger(message.pid) && message.pid !== creds.pid)
          ) {
            socket.destroy();
            return;
          }
          peerPid = creds.pid;
          connectionId = randomUUID();
          peers.set(
            connectionId,
            host.connect({
              verified: true,
              connectionId,
              sid: String(creds.uid),
              processStart: children.get(peerPid),
              role: 'plugin-backend',
            }),
          );
          sendPayload(socket, { ready: true });
          return;
        }
        if (!controlled(children, peerPid)) {
          socket.destroy();
          return;
        }
        await handleRequest(socket, connectionId, message);
      } catch (error) {
        if (message?.requestId)
          sendPayload(socket, {
            protocolVersion: 1,
            requestId: message.requestId,
            type: 'error',
            code: [
              'common.conflict',
              'common.forbidden',
              'common.unauthenticated',
              'common.validation_failed',
            ].includes(error.code)
              ? error.code
              : 'common.unavailable',
          });
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(pipe, () => {
      try {
        chmodSync(pipe, 0o600);
        resolve();
      } catch (error) {
        reject(error);
      }
    });
  });

  return {
    async attach(dsh) {
      if (closed) throw new Error('shell IPC unavailable');
      try {
        children.set(dsh.pid, startTicks(dsh.pid));
      } catch {
        throw new Error('controlled child registration failed');
      }
      if (!dsh.stdio[3])
        throw new Error('private inherited shell channel required');
      dsh.stdio[3].end(
        JSON.stringify({
          protocolVersion: 1,
          pipe,
          capability,
          helperPath: null,
          transport: 'unix',
        }) + '\n',
      );
    },
    async close() {
      cleanup();
    },
  };
}

export function connectUnixIpc(descriptor) {
  const pending = new Map();
  let readyResolve,
    readyReject,
    closed = false,
    disposed = false;
  const ready = new Promise((resolve, reject) => {
    readyResolve = resolve;
    readyReject = reject;
  });
  void ready.catch(() => {});
  const socket = createConnection({ path: descriptor.pipe });
  const lines = createInterface({ input: socket });
  const disconnect = (error) => {
    if (closed) return;
    closed = true;
    readyReject(error);
    const entries = [...pending.values()];
    pending.clear();
    for (const entry of entries) entry.reject?.(error);
  };
  socket.on('error', disconnect);
  socket.on('close', () => disconnect(new Error('shell IPC disconnected')));
  lines.on('line', (line) => {
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      socket.destroy();
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
  socket.write(
    JSON.stringify({
      capability: descriptor.capability,
      pid: process.pid,
    }) + '\n',
  );
  return {
    async call(command) {
      await ready;
      if (closed) throw new Error('shell IPC disconnected');
      return new Promise((resolve, reject) => {
        pending.set(command.requestId, { resolve, reject });
        socket.write(JSON.stringify(command) + '\n');
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
      socket.write(JSON.stringify(command) + '\n');
    },
    close() {
      if (disposed) return;
      disposed = true;
      disconnect(new Error('shell IPC disconnected'));
      socket.end();
      socket.destroy();
      lines.close();
    },
  };
}
