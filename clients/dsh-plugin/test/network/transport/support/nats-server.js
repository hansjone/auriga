import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync } from 'node:fs';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';
import { nkeys } from '@nats-io/nats-core';
import { createTestPki } from './pki.js';

const exeName =
  process.platform === 'win32' ? 'nats-server.exe' : 'nats-server';

/** Same lookup order as services/test-support NatsServer.java. Null when unavailable. */
export function findNatsServer() {
  const candidates = [];
  if (process.env.AURIGA_NATS_SERVER)
    candidates.push(process.env.AURIGA_NATS_SERVER);
  const cache =
    process.env.AURIGA_TEST_CACHE ?? join(homedir(), '.cache', 'auriga');
  const platform = `${process.platform === 'win32' ? 'windows' : process.platform}-${
    process.arch === 'x64' ? 'amd64' : process.arch
  }`;
  for (const dir of [platform, `${platform}.zip`, `${platform}.tar.gz`]) {
    candidates.push(join(cache, 'nats-server', '2.15.1', dir, exeName));
  }
  return candidates.find((candidate) => existsSync(candidate)) ?? null;
}

const b64url = (buf) => Buffer.from(buf).toString('base64url');

function encodeJwt(issuerKp, claims) {
  const body = { ...claims, iat: Math.floor(Date.now() / 1000) };
  body.jti = createHash('sha256')
    .update(JSON.stringify(body))
    .digest('base64url');
  const head = b64url(JSON.stringify({ typ: 'JWT', alg: 'ed25519-nkey' }));
  const payload = b64url(JSON.stringify(body));
  const sig = b64url(
    issuerKp.sign(new TextEncoder().encode(`${head}.${payload}`)),
  );
  return `${head}.${payload}.${sig}`;
}

/** Operator + account trust chain; `mintUser` issues real, expiring NATS user JWTs. */
export function createJwtAuth({ allowPub = '>', allowSub = '>' } = {}) {
  const operator = nkeys.createOperator();
  const account = nkeys.createAccount();
  const operatorPub = operator.getPublicKey();
  const accountPub = account.getPublicKey();
  const unlimited = { subs: -1, data: -1, payload: -1 };
  const operatorJwt = encodeJwt(operator, {
    iss: operatorPub,
    sub: operatorPub,
    name: 'auriga-test',
    nats: { type: 'operator', version: 2 },
  });
  const accountJwt = encodeJwt(operator, {
    iss: operatorPub,
    sub: accountPub,
    name: 'auriga-test-account',
    nats: {
      type: 'account',
      version: 2,
      limits: {
        ...unlimited,
        imports: -1,
        exports: -1,
        wildcards: true,
        conn: -1,
        leaf: -1,
      },
    },
  });
  return {
    config: `operator: ${operatorJwt}
resolver: MEMORY
resolver_preload: { ${accountPub}: ${accountJwt} }
`,
    /** @param {{ ttlMs: number }} opts */
    mintUser({ ttlMs }) {
      const user = nkeys.createUser();
      const jwt = encodeJwt(account, {
        iss: accountPub,
        sub: user.getPublicKey(),
        name: 'dsh',
        exp: Math.ceil((Date.now() + ttlMs) / 1000),
        nats: {
          type: 'user',
          version: 2,
          ...unlimited,
          pub: { allow: [allowPub] },
          sub: { allow: [allowSub] },
        },
      });
      return { jwt, nkeySeed: Buffer.from(user.getSeed()).toString('utf8') };
    },
  };
}

/**
 * Local nats-server child on random loopback ports with WSS from a fresh test CA
 * chain. `close()` kills the process and removes its temp directory.
 *
 * @param {{ binary: string, authConfig?: string, maxPayload?: number, maxPending?: number }} opts
 */
export async function startNatsServer({
  binary,
  authConfig = '',
  maxPayload = 64 * 1024,
  maxPending,
}) {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-nats-'));
  const pki = createTestPki();
  await writeFile(join(dir, 'cert.pem'), pki.certPem);
  await writeFile(join(dir, 'key.pem'), pki.keyPem);
  const slash = (p) => p.replaceAll('\\', '/');
  await writeFile(
    join(dir, 'server.conf'),
    `host: 127.0.0.1
port: -1
max_payload: ${maxPayload}
${maxPending === undefined ? '' : `max_pending: ${maxPending}`}
websocket {
  host: 127.0.0.1
  port: -1
  compression: false
  tls { cert_file: "${slash(join(dir, 'cert.pem'))}", key_file: "${slash(join(dir, 'key.pem'))}" }
}
${authConfig}
`,
  );
  const child = spawn(binary, ['-c', join(dir, 'server.conf')], {
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  let log = '';
  const port = await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () =>
        reject(
          new Error(`nats-server did not report a websocket port:\n${log}`),
        ),
      15_000,
    );
    const onData = (chunk) => {
      log += chunk;
      const match = /websocket clients on wss?:\/\/[^:\s]+:(\d+)/.exec(log);
      if (match) {
        clearTimeout(timer);
        resolve(Number(match[1]));
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', onData);
    child.once('exit', (code) => {
      clearTimeout(timer);
      reject(new Error(`nats-server exited early (${code}):\n${log}`));
    });
  }).catch(async (error) => {
    child.kill();
    await rm(dir, { recursive: true, force: true });
    throw error;
  });
  const exited = new Promise((resolve) => child.once('exit', resolve));
  return {
    port,
    url: `wss://localhost:${port}`,
    rootPem: pki.rootPem,
    get log() {
      return log;
    },
    async close() {
      if (child.exitCode === null) {
        child.kill();
        await exited;
      }
      await rm(dir, { recursive: true, force: true });
    },
  };
}
