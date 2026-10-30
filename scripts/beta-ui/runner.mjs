/* global fetch, AbortSignal */
import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import https from 'node:https';
import { randomBytes, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
export const repo = path.resolve(here, '../..');
const tenant = '00000000-0000-0000-0000-000000000001';
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const read = (file) => {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {
    throw new Error('Invalid private Beta JSON file');
  }
};
const json = (file, value) =>
  fs.writeFileSync(file, JSON.stringify(value, null, 2), { mode: 0o600 });
const unix = (value) => value.replaceAll('\\', '/');

export function windowsPowerShellEnvironment(source = process.env) {
  // Windows PowerShell rebuilds its own module paths when this variable is absent.
  return Object.fromEntries(
    Object.entries(source).filter(
      ([key]) => key.toUpperCase() !== 'PSMODULEPATH',
    ),
  );
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repo,
    windowsHide: true,
    encoding: 'utf8',
    ...options,
    ...(command === 'powershell.exe'
      ? { env: windowsPowerShellEnvironment(options.env) }
      : {}),
  });
  if (result.error || result.status !== 0)
    throw new Error('Beta subprocess failed; inspect private logs');
  return result.stdout;
}

export function privatePath(target, restrict = false) {
  const real = fs.realpathSync(target);
  if (
    path.resolve(real).toLowerCase() !== path.resolve(target).toLowerCase() ||
    fs.lstatSync(target).isSymbolicLink()
  ) {
    throw new Error('Beta private path must not contain links');
  }
  run('powershell.exe', [
    '-NoProfile',
    '-NonInteractive',
    '-File',
    path.join(here, 'private.ps1'),
    '-Path',
    target,
    ...(restrict ? [] : ['-VerifyOnly']),
  ]);
}

export function assertExternal(state) {
  const relative = path.relative(repo, state);
  if (
    relative !== '..' &&
    !relative.startsWith('..' + path.sep) &&
    !path.isAbsolute(relative)
  )
    throw new Error('Private StateDir must be outside the repository');
  if (!path.isAbsolute(state) || state.startsWith('\\\\'))
    throw new Error('StateDir must be an absolute local path');
}

export function configuration(state, manifest, secrets) {
  const p = (name) => unix(path.join(state, name));
  const { ports, database, roles } = manifest;
  if (
    ['networkSubmit', 'networkBroker', 'networkWebsocket'].some(
      (name) =>
        !Number.isSafeInteger(ports[name]) ||
        ports[name] < 1 ||
        ports[name] > 65535,
    )
  )
    throw new Error(
      'Network ports are missing; use a fresh private Beta StateDir',
    );
  const origin = (port) => ({ scheme: 'http', host: '127.0.0.1', port });
  const issuer = `http://127.0.0.1:${ports.portal}`;
  const jdbc = (schema) =>
    `jdbc:postgresql://127.0.0.1:${manifest.pgPort}/${database}?currentSchema=${schema}`;
  const hop = {
    certFile: p('tls/edge.pem'),
    keyFile: p('tls/edge.key'),
    caFile: p('tls/ca.pem'),
    serverName: 'platform.beta.internal',
    allowLoopbackHttp: true,
  };
  const access = {
    schemaVersion: 1,
    mode: 'ip',
    tenantId: tenant,
    portalOrigin: origin(ports.portal),
    adminOrigin: origin(ports.admin),
    issuerOrigin: origin(ports.portal),
    insecureHttpAcknowledged: true,
    listeners: Object.fromEntries(
      ['portal', 'admin', 'instanceControl'].map((name) => [
        name,
        { address: '127.0.0.1', port: ports[name], tls: false },
      ]),
    ),
    ingress: { kind: 'direct' },
    instances: { mode: 'path', prefix: '/i/', trustedTeamAcknowledged: true },
    instanceControl: {
      dnsAlias: 'auriga-instance.internal',
      origin: {
        scheme: 'http',
        host: 'auriga-instance.internal',
        port: ports.instanceControl,
      },
      edgeContainer: 'auriga-beta-edge',
    },
  };
  const platform = {
    'auriga.platform.iam.jdbc-url': jdbc('iam'),
    'auriga.platform.iam.username': roles.iam,
    'auriga.platform.iam.password': secrets.iam,
    'auriga.platform.org.jdbc-url': jdbc('org'),
    'auriga.platform.org.username': roles.org,
    'auriga.platform.org.password': secrets.org,
    'auriga.platform.enrollment-file': p('enrollment.json'),
    'auriga.platform.team.rp-id': '127.0.0.1',
    'auriga.platform.team.origin': issuer,
    'auriga.iam.issuer': issuer,
    'auriga.security.jwt.issuer': issuer,
    'auriga.security.jwt.jwk-set-uri': `${issuer}/oauth2/jwks`,
    'auriga.iam.encryption-key': secrets.encryptionKey,
    'auriga.iam.deployment-access-file': p('access.json'),
    'auriga.grpc.server.port': ports.grpc,
    'auriga.grpc.server.host': '127.0.0.1',
    'auriga.grpc.server.tls-cert-chain': p('tls/platform.pem'),
    'auriga.grpc.server.tls-private-key': p('tls/platform.key'),
    'auriga.grpc.server.tls-trust-cert-collection': p('tls/ca.pem'),
    'auriga.grpc.server.tenant-delegates': ['svc:edge', 'svc:ai-gateway'],
    'server.port': ports.platform,
    'server.address': '127.0.0.1',
    'server.ssl.enabled': false,
    'auriga.iam.session-kv-nats': false,
    'auriga.events.nats-url': '',
    'auriga.events.ensure-streams': false,
  };
  for (const [service, directory, group] of [
    ['edge', 'edge', 65532],
    ['ai-gateway', 'gateway', 65533],
  ]) {
    const prefix = `auriga.iam.${service === 'edge' ? 'edge' : 'ai-gateway'}-token-export.`;
    Object.assign(platform, {
      [prefix + 'enabled']: true,
      [prefix + 'installation-secret-file']: p(
        `${directory}-secret/installation.secret`,
      ),
      [prefix + 'token-file']: p(`${directory}-token/service.jwt`),
      [prefix + 'reader-group']: group,
      [`auriga.iam.internal-secret-hashes.${service}`]: createHash('sha256')
        .update(secrets[`${directory}Secret`])
        .digest('hex'),
    });
  }
  const edge = {
    accessConfigFile: p('access.json'),
    listen: `127.0.0.1:${ports.portal}`,
    sessionStore: 'memory',
    natsUrl: '',
    identityKeysFile: p('tls/identity.json'),
    iamHop: hop,
    iamRpc: {
      address: `127.0.0.1:${ports.grpc}`,
      serverName: 'platform.beta.internal',
      serviceTokenFile: p('edge-token/service.jwt'),
    },
    networkSubmitHop: {
      certFile: p('network/edge.pem'),
      keyFile: p('network/edge.key'),
      caFile: p('network/server-ca.pem'),
      serverName: 'submit.network.internal',
    },
    nodeTLS: {
      certFile: p('tls/edge.pem'),
      keyFile: p('tls/edge.key'),
      caFile: p('tls/ca.pem'),
    },
    upstreams: {
      iam: `http://127.0.0.1:${ports.platform}`,
      org: `http://127.0.0.1:${ports.platform}`,
      workspace: `http://127.0.0.1:${ports.platform}`,
      'network-submit': `https://127.0.0.1:${ports.networkSubmit}`,
    },
    static: { portalRoot: p('static/portal'), adminRoot: p('static/admin') },
  };
  const gatewayRPC = {
    Target: `127.0.0.1:${ports.grpc}`,
    ServiceTokenFile: p('gateway-token/service.jwt'),
    CertFile: p('tls/ai-gateway.pem'),
    KeyFile: p('tls/ai-gateway.key'),
    CAFile: p('tls/ca.pem'),
    ServerName: 'platform.beta.internal',
  };
  const ai = manifest.litellm
    ? {
        listen: `127.0.0.1:${ports.ai}`,
        origin: manifest.litellm.origin,
        keyFile: manifest.litellm.teamKeyFile,
        iam: gatewayRPC,
        org: gatewayRPC,
      }
    : undefined;
  if (manifest.litellm) {
    if (
      !Number.isSafeInteger(ports.litellmUI) ||
      ports.litellmUI < 1 ||
      ports.litellmUI > 65535 ||
      Object.entries(ports).some(
        ([name, value]) => name !== 'litellmUI' && value === ports.litellmUI,
      )
    )
      throw new Error(
        'Independent LiteLLM UI port missing; use a fresh Beta StateDir',
      );
    edge.litellmOrigin = manifest.litellm.origin;
    edge.litellmUI = {
      origin: origin(ports.litellmUI),
      listener: { address: '127.0.0.1', port: ports.litellmUI, tls: false },
    };
    edge.upstreams['ai-device'] = `http://127.0.0.1:${ports.ai}`;
  }
  const sessionKV = {
    psql: manifest.psql || 'psql.exe',
    port: manifest.pgPort,
    database,
    username: roles.edgeKv,
    passFile: path.join(state, 'session-reader.pgpass'),
    directory: path.join(state, 'session-projections'),
    tenantId: tenant,
  };
  platform['auriga.iam.network.server-audiences'] = ['auriga-network'];
  platform['auriga.iam.network.client-server-audience'] = 'auriga-network';
  const networkOptions = {
    root: p('network'),
    submitListen: `127.0.0.1:${ports.networkSubmit}`,
    brokerHost: '127.0.0.1',
    brokerURL: `tls://127.0.0.1:${ports.networkBroker}`,
    brokerPort: ports.networkBroker,
    websocketPort: ports.networkWebsocket,
    audience: 'auriga-network',
    iam: gatewayRPC,
    collab: gatewayRPC,
  };
  return { access, platform, edge, ai, sessionKV, networkOptions };
}

export function writeConfiguration(state, manifest, secrets) {
  const cfg = configuration(state, manifest, secrets);
  for (const [name, value] of Object.entries(cfg).filter(
    ([, value]) => value !== undefined,
  ))
    json(
      path.join(state, `${name}.${name === 'platform' ? 'yaml' : 'json'}`),
      value,
    );
  for (const [name, role, password] of [
    ['session-reader.pgpass', manifest.roles.edgeKv, secrets.edgeKv],
    ['session-setup.pgpass', manifest.roles.iam, secrets.iam],
  ]) {
    fs.writeFileSync(
      path.join(state, name),
      `127.0.0.1:${manifest.pgPort}:${manifest.database}:${role}:${password}\n`,
      { mode: 0o600 },
    );
    privatePath(path.join(state, name), true);
  }
}

function initialize(state, pgPort) {
  assertExternal(state);
  if (fs.existsSync(path.join(state, 'installation.json')))
    throw new Error(
      'Installation already exists; reuse it without regenerating credentials',
    );
  if (fs.existsSync(state) && fs.readdirSync(state).length)
    throw new Error('New StateDir must be empty');
  fs.mkdirSync(state, { recursive: true });
  privatePath(state, true); // No credentials are created until inheritance is restricted and verified.
  for (const name of [
    'logs',
    'bin',
    'tls',
    'edge-secret',
    'edge-token',
    'gateway-secret',
    'gateway-token',
    'static',
    'session-projections',
  ]) {
    fs.mkdirSync(path.join(state, name));
    privatePath(path.join(state, name), true);
  }
  const suffix = randomBytes(5).toString('hex');
  const database = `auriga_beta_${suffix}`;
  const roles = Object.fromEntries(
    ['dbOwner', 'iam', 'org', 'edgeKv'].map((name) => [
      name,
      `${database}_${name.toLowerCase()}`,
    ]),
  );
  const manifest = {
    version: 1,
    database,
    roles,
    pgPort,
    dbReady: false,
    ports: {
      portal: 18080,
      admin: 18082,
      instanceControl: 18083,
      platform: 18081,
      grpc: 19090,
      ai: 18084,
      networkBroker: 18085,
      networkWebsocket: 18086,
      networkSubmit: 18087,
      litellmUI: 18088,
    },
  };
  const secrets = Object.fromEntries(
    ['iam', 'org', 'edgeKv', 'edgeSecret', 'gatewaySecret'].map((name) => [
      name,
      randomBytes(32).toString('base64url'),
    ]),
  );
  secrets.encryptionKey = randomBytes(32).toString('base64');
  json(path.join(state, 'installation.json'), manifest);
  json(path.join(state, 'secrets.json'), secrets);
  fs.writeFileSync(
    path.join(state, 'edge-secret/installation.secret'),
    secrets.edgeSecret,
    { mode: 0o600 },
  );
  fs.writeFileSync(
    path.join(state, 'gateway-secret/installation.secret'),
    secrets.gatewaySecret,
    { mode: 0o600 },
  );
  writeConfiguration(state, manifest, secrets);
  run('go', [
    'run',
    path.join(here, 'materialize.go'),
    path.join(state, 'tls'),
  ]);
  run('go', [
    '-C',
    path.join(repo, 'deploy/bootstrap'),
    'run',
    './cmd/network-materialize',
    path.join(state, 'network'),
    path.join(state, 'networkOptions.json'),
  ]);
  privatePath(path.join(state, 'network'), true);
}

export function postgresEnvironment(passfile, inherited = process.env) {
  return {
    ...Object.fromEntries(
      Object.entries(inherited).filter(
        ([key]) => !key.toUpperCase().startsWith('PG'),
      ),
    ),
    PGPASSFILE: passfile,
    PGCONNECT_TIMEOUT: '5',
    PGOPTIONS: '-c statement_timeout=15000 -c lock_timeout=5000',
  };
}

function initializeDatabase(state, manifest, psql, passfile) {
  sessionProjectionDDL({
    ...manifest,
    roles: {
      ...manifest.roles,
      edgeKv: manifest.roles.edgeKv || manifest.database + '_edgekv',
    },
  });
  if (manifest.dbReady && manifest.roles.edgeKv) {
    manifest.psql ||= psql;
    const directory = path.join(state, 'session-projections');
    if (!fs.existsSync(directory)) fs.mkdirSync(directory);
    privatePath(directory, true);
    writeConfiguration(state, manifest, read(path.join(state, 'secrets.json')));
    json(path.join(state, 'installation.json'), manifest);
    return;
  }
  if (!passfile || !fs.statSync(passfile).isFile())
    throw new Error(
      'init-db requires a protected PostgreSQL administrator pgpass file',
    );
  privatePath(passfile);
  const secrets = read(path.join(state, 'secrets.json'));
  if (manifest.dbReady) {
    // Upgrade only our existing isolated Beta installation; preserve data, signing/enrollment keys.
    manifest.roles.edgeKv = `${manifest.database}_edgekv`;
    secrets.edgeKv = randomBytes(32).toString('base64url');
  }
  const lines = ['\\set ON_ERROR_STOP on', 'BEGIN;'];
  for (const [name, role] of Object.entries(manifest.roles).filter(
    ([name]) => !manifest.dbReady || name === 'edgeKv',
  )) {
    lines.push(
      `CREATE ROLE ${role} ${name === 'dbOwner' ? 'NOLOGIN' : `LOGIN PASSWORD '${secrets[name]}'`} NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;`,
    );
  }
  if (manifest.dbReady) {
    lines.push(
      `ALTER ROLE ${manifest.roles.edgeKv} SET default_transaction_read_only=on;`,
      `GRANT CONNECT ON DATABASE ${manifest.database} TO ${manifest.roles.edgeKv};`,
      'COMMIT;',
    );
  } else {
    lines.push(
      'COMMIT;',
      `CREATE DATABASE ${manifest.database} OWNER ${manifest.roles.dbOwner};`,
      `\\connect ${manifest.database}`,
    );
    lines.push(
      'REVOKE ALL ON SCHEMA public FROM PUBLIC;',
      `REVOKE ALL ON DATABASE ${manifest.database} FROM PUBLIC;`,
    );
    for (const name of ['iam', 'org', 'edgeKv'])
      lines.push(
        `GRANT CONNECT ON DATABASE ${manifest.database} TO ${manifest.roles[name]};`,
      );
    for (const name of ['iam', 'org']) {
      lines.push(
        `GRANT CREATE ON DATABASE ${manifest.database} TO ${manifest.roles[name]};`,
      );
    }
    for (const [schema, role] of [
      ['iam', manifest.roles.iam],
      ['org', manifest.roles.org],
    ]) {
      lines.push(
        `CREATE SCHEMA ${schema} AUTHORIZATION ${role};`,
        `ALTER ROLE ${role} IN DATABASE ${manifest.database} SET search_path = ${schema};`,
      );
    }
    lines.push(
      `ALTER ROLE ${manifest.roles.edgeKv} SET default_transaction_read_only=on;`,
    );
  }
  const sql = path.join(state, 'initialize.sql');
  fs.writeFileSync(sql, lines.join('\n'), { mode: 0o600 });
  const log = fs.openSync(path.join(state, 'logs/database.log'), 'a', 0o600);
  try {
    run(
      psql,
      [
        '-X',
        '-q',
        '-h',
        '127.0.0.1',
        '-p',
        String(manifest.pgPort),
        '-U',
        'postgres',
        '-d',
        'postgres',
        '-f',
        sql,
      ],
      {
        env: postgresEnvironment(passfile),
        stdio: ['ignore', log, log],
      },
    );
    manifest.dbReady = true;
    manifest.psql = psql;
    json(path.join(state, 'secrets.json'), secrets);
    json(path.join(state, 'installation.json'), manifest);
    const directory = path.join(state, 'session-projections');
    if (!fs.existsSync(directory)) fs.mkdirSync(directory);
    privatePath(directory, true);
    writeConfiguration(state, manifest, secrets);
  } finally {
    fs.closeSync(log);
    fs.rmSync(sql, { force: true });
  }
}

export function sessionProjectionDDL(manifest) {
  if (
    !/^auriga_beta_[a-f0-9]{10}$/.test(manifest.database) ||
    manifest.roles.iam !== `${manifest.database}_iam` ||
    manifest.roles.edgeKv !== `${manifest.database}_edgekv`
  )
    throw new Error('Invalid Beta session projection roles');
  return `BEGIN;
CREATE SCHEMA IF NOT EXISTS edge_session_cache;
REVOKE ALL ON SCHEMA edge_session_cache FROM PUBLIC;
CREATE OR REPLACE VIEW edge_session_cache.sessions WITH (security_barrier=true) AS
SELECT tenant_id, lookup_key AS cookie_hash, data->'value' AS value, expires_at
FROM iam.session_kv WHERE tenant_id='${tenant}'::uuid;
REVOKE ALL ON edge_session_cache.sessions FROM PUBLIC;
GRANT USAGE ON SCHEMA edge_session_cache TO ${manifest.roles.edgeKv};
GRANT SELECT ON edge_session_cache.sessions TO ${manifest.roles.edgeKv};
COMMIT;
`;
}

function prepareSessionProjection(state, manifest) {
  const sql = path.join(state, 'session-projection.sql');
  fs.writeFileSync(sql, sessionProjectionDDL(manifest), { mode: 0o600 });
  const log = fs.openSync(
    path.join(state, 'logs/session-projection.log'),
    'a',
    0o600,
  );
  try {
    run(
      manifest.psql || 'psql.exe',
      [
        '-X',
        '-q',
        '-w',
        '-v',
        'ON_ERROR_STOP=1',
        '-h',
        '127.0.0.1',
        '-p',
        String(manifest.pgPort),
        '-U',
        manifest.roles.iam,
        '-d',
        manifest.database,
        '-f',
        sql,
      ],
      {
        env: postgresEnvironment(path.join(state, 'session-setup.pgpass')),
        stdio: ['ignore', log, log],
      },
    );
  } finally {
    fs.closeSync(log);
    fs.rmSync(sql, { force: true });
  }
}

export function processIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1) return null;
  const result = spawnSync(
    'powershell.exe',
    [
      '-NoProfile',
      '-NonInteractive',
      '-Command',
      `$p=Get-Process -Id ${pid} -ErrorAction SilentlyContinue; if ($p) { $p.StartTime.ToUniversalTime().Ticks.ToString() }`,
    ],
    {
      encoding: 'utf8',
      windowsHide: true,
      env: windowsPowerShellEnvironment(),
    },
  );
  return result.status === 0 && result.stdout.trim()
    ? result.stdout.trim()
    : null;
}

export async function waitUntil(check, message, timeout = 60000) {
  const end = Date.now() + timeout;
  do {
    if (await check()) return;
    await sleep(150);
  } while (Date.now() < end);
  throw new Error(message);
}

export async function portFree(port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once('error', () => resolve(false));
    server.listen(port, '127.0.0.1', () => server.close(() => resolve(true)));
  });
}

function ownedProcesses(state) {
  const file = path.join(state, 'processes.json');
  return fs.existsSync(file) ? read(file) : [];
}

export async function stopProcesses(state, ports) {
  const records = ownedProcesses(state);
  for (const record of [...records].reverse()) {
    const identity = processIdentity(record.pid);
    if (!identity) continue;
    if (identity !== record.identity)
      throw new Error(
        'Recorded PID was reused; refusing to kill another process',
      );
    process.kill(record.pid);
    await waitUntil(
      () => processIdentity(record.pid) !== identity,
      'Owned process did not exit',
      10000,
    );
  }
  for (const port of ports)
    await waitUntil(
      () => portFree(port),
      'A Beta port is still occupied; no unrelated process was killed',
      10000,
    );
  json(path.join(state, 'processes.json'), []);
  for (const name of ['edge-token/service.jwt', 'gateway-token/service.jwt'])
    fs.rmSync(path.join(state, name), { force: true });
  const projections = path.join(state, 'session-projections');
  if (fs.existsSync(projections)) {
    privatePath(projections);
    // An abrupt Windows edge stop can interrupt its finally block. Only remove our result files;
    // a short-lived psql still closing one gets time to finish its bounded database operation.
    await waitUntil(
      () => {
        try {
          for (const entry of fs.readdirSync(projections, {
            withFileTypes: true,
          })) {
            if (entry.isFile() && /^projection-[0-9]+\.json$/.test(entry.name))
              fs.rmSync(path.join(projections, entry.name), { force: true });
          }
          return true;
        } catch {
          return false;
        }
      },
      'Private session result files are still busy',
      5000,
    );
  }
}

export async function startServices(state, manifest) {
  if (!manifest.dbReady) throw new Error('Run init-db first');
  if (!manifest.roles?.edgeKv)
    throw new Error(
      'Run init-db with the protected administrator pgpass once to upgrade Beta session projections',
    );
  const ports = Object.values(manifest.ports);
  const previous = ownedProcesses(state);
  if (
    previous.length === 5 &&
    ['platform', 'ai', 'network-broker', 'network', 'edge'].every((name) =>
      previous.some((record) => record.name === name),
    ) &&
    previous.every((record) => processIdentity(record.pid) === record.identity)
  )
    return;
  if (
    !manifest.ports.networkSubmit ||
    !fs.existsSync(path.join(state, 'network/runtime.json'))
  )
    throw new Error(
      'Network deployment material is missing; use a fresh private Beta StateDir',
    );
  await stopProcesses(state, previous.length ? ports : []);
  for (const port of ports)
    if (!(await portFree(port)))
      throw new Error('A Beta port is occupied; refusing to reuse it');
  const records = [];
  const launch = (name, executable, args) => {
    let child;
    const out = fs.openSync(path.join(state, `logs/${name}.log`), 'a', 0o600);
    try {
      // Parent environment is filtered so an unrelated AURIGA_* / SPRING_* cannot override files.
      const env = Object.fromEntries(
        Object.entries(process.env).filter(
          ([key]) => !/^(AURIGA_|SPRING_)/i.test(key),
        ),
      );
      child = spawn(executable, args, {
        cwd: state,
        env,
        stdio: ['ignore', out, out],
        windowsHide: true,
        detached: true,
      });
      child.on('error', () => {});
      if (!child.pid) throw new Error('Cannot create Beta process');
      child.unref();
      const identity = processIdentity(child.pid);
      if (!identity) throw new Error('Beta process exited before registration');
      records.push({ name, pid: child.pid, identity });
      json(path.join(state, 'processes.json'), records);
    } catch (error) {
      if (child?.pid) child.kill();
      throw error;
    } finally {
      fs.closeSync(out);
    }
  };
  const healthy = async () => {
    try {
      const response = await fetch(
        `http://127.0.0.1:${manifest.ports.platform}/actuator/health`,
        { signal: AbortSignal.timeout(2000) },
      );
      return response.ok;
    } catch {
      return false;
    }
  };
  try {
    launch('platform', 'java', [
      '--enable-native-access=ALL-UNNAMED',
      '-jar',
      path.join(state, 'bin/platform.jar'),
      '--spring.profiles.active=combined,dev,beta-ui',
      `--spring.config.additional-location=${pathToFileURL(path.join(state, 'platform.yaml')).href}`,
    ]);
    await waitUntil(
      healthy,
      'Platform did not become healthy; inspect private platform.log',
      120000,
    );
    prepareSessionProjection(state, manifest);
    await waitUntil(
      () =>
        ['edge-token/service.jwt', 'gateway-token/service.jwt'].every((name) =>
          fs.existsSync(path.join(state, name)),
        ),
      'Real IAM service tickets were not published',
    );
    // This is the running IAM public endpoint, never a locally generated signing fixture.
    const response = await fetch(
      `http://127.0.0.1:${manifest.ports.platform}/oauth2/jwks`,
      { signal: AbortSignal.timeout(5000) },
    );
    if (!response.ok) throw new Error('Cannot obtain real IAM public JWKS');
    const jwks = await response.json();
    if (
      !Array.isArray(jwks.keys) ||
      jwks.keys.length === 0 ||
      jwks.keys.some((key) => key.d)
    )
      throw new Error('Invalid IAM public JWKS');
    json(path.join(state, 'jwks.json'), jwks);
    if (manifest.litellm)
      launch('ai', path.join(state, 'bin/ai-gateway.exe'), [
        '--litellm-config',
        path.join(state, 'ai.json'),
      ]);
    launch('network-broker', path.join(state, 'bin/network-broker.exe'), [
      '-c',
      path.join(state, 'network/broker.conf'),
    ]);
    await waitUntil(
      async () => !(await portFree(manifest.ports.networkBroker)),
      'Network broker did not become ready; inspect private network-broker.log',
    );
    launch('network', path.join(state, 'bin/ai-gateway.exe'), [
      '--network-config',
      path.join(state, 'network/runtime.json'),
    ]);
    await waitUntil(
      () => networkSubmitReady(state, manifest),
      'Network submit mTLS listener did not become ready; inspect private network.log',
    );
    launch('edge', path.join(state, 'bin/edge.exe'), [
      '--config',
      path.join(state, 'edge.json'),
      '--instance-routes',
      'memory',
      '--session-kv-config',
      path.join(state, 'sessionKV.json'),
    ]);
    await waitUntil(async () => {
      if (
        !records.every(
          (record) => processIdentity(record.pid) === record.identity,
        )
      )
        throw new Error('Beta process exited; inspect private logs');
      try {
        const admin = await fetch(`http://127.0.0.1:${manifest.ports.admin}/`, {
          signal: AbortSignal.timeout(2000),
        });
        if (!manifest.litellm) return admin.ok;
        const ai = await fetch(
          `http://127.0.0.1:${manifest.ports.ai}/ai/v1/models`,
          { signal: AbortSignal.timeout(2000) },
        );
        return admin.ok && ai.status === 401;
      } catch {
        return false;
      }
    }, 'Edge or AI management listener did not become ready');
  } catch (error) {
    await stopProcesses(state, ports);
    throw error;
  }
}

// Reach the actual handler over the same verified mTLS hop as edge. A 401 with
// no device token proves listener readiness without manufacturing authentication.
export async function networkSubmitReady(state, manifest) {
  const hop = {
    serverName: 'submit.network.internal',
    caFile: path.join(state, 'network/server-ca.pem'),
    certFile: path.join(state, 'network/edge.pem'),
    keyFile: path.join(state, 'network/edge.key'),
  };
  try {
    return await new Promise((resolve) => {
      const request = https.request(
        {
          hostname: '127.0.0.1',
          port: manifest.ports.networkSubmit,
          servername: hop.serverName,
          ca: fs.readFileSync(hop.caFile),
          cert: fs.readFileSync(hop.certFile),
          key: fs.readFileSync(hop.keyFile),
          method: 'POST',
          path: '/api/ai/v1/network/submit',
          headers: { 'Content-Type': 'application/json' },
          timeout: 2000,
          agent: false,
        },
        (response) => {
          let body = '';
          response.on('data', (chunk) => {
            if (body.length < 8192) body += chunk;
          });
          response.on('end', () =>
            resolve(
              response.statusCode === 401 &&
                body.includes('network.unauthorized'),
            ),
          );
          response.on('error', () => resolve(false));
        },
      );
      request.on('timeout', () => request.destroy());
      request.on('error', () => resolve(false));
      request.end();
    });
  } catch {
    return false;
  }
}

export async function main(args) {
  if (process.platform !== 'win32')
    throw new Error('This launcher requires native Windows');
  const [action, ...options] = args;
  const values = Object.fromEntries(
    Array.from({ length: options.length / 2 }, (_, i) => [
      options[i * 2],
      options[i * 2 + 1],
    ]),
  );
  if (!values['--state']) throw new Error('StateDir required');
  const state = path.resolve(values['--state']);
  assertExternal(state);
  if (action === 'init')
    return initialize(state, Number(values['--pg-port'] || 5432));
  privatePath(state);
  const lock = path.join(state, 'command.lock');
  // A crash leaves a lock: status and the owner PID/start-time let the operator diagnose it.
  const handle = fs.openSync(lock, 'wx', 0o600);
  try {
    fs.writeFileSync(
      handle,
      JSON.stringify({
        pid: process.pid,
        identity: processIdentity(process.pid),
      }),
    );
    const manifest = read(path.join(state, 'installation.json'));
    switch (action) {
      case 'init-db':
        return initializeDatabase(
          state,
          manifest,
          values['--psql'] || 'psql.exe',
          values['--pg-passfile'],
        );
      case 'build': {
        if (
          ownedProcesses(state).some(
            (record) => processIdentity(record.pid) === record.identity,
          )
        )
          throw new Error('Stop Beta before replacing binaries');
        const log = fs.openSync(path.join(state, 'logs/build.log'), 'a', 0o600);
        try {
          run(
            'powershell.exe',
            [
              '-NoProfile',
              '-NonInteractive',
              '-File',
              path.join(here, 'build.ps1'),
              '-StateDir',
              state,
            ],
            { stdio: ['ignore', log, log] },
          );
        } finally {
          fs.closeSync(log);
        }
        return;
      }
      case 'litellm': {
        const origin = new URL(values['--origin']);
        if (
          !['http:', 'https:'].includes(origin.protocol) ||
          origin.username ||
          origin.password ||
          origin.pathname !== '/' ||
          origin.search ||
          origin.hash
        )
          throw new Error('LiteLLM must be a fixed origin');
        if (
          origin.protocol === 'http:' &&
          !['127.0.0.1', '[::1]'].includes(origin.hostname)
        )
          throw new Error('Plain HTTP LiteLLM requires numeric loopback');
        const teamKeyFile = path.resolve(values['--team-key-file']);
        privatePath(teamKeyFile);
        manifest.litellm = {
          origin: origin.origin,
          teamKeyFile: unix(teamKeyFile),
        };
        json(path.join(state, 'installation.json'), manifest);
        writeConfiguration(
          state,
          manifest,
          read(path.join(state, 'secrets.json')),
        );
        return;
      }
      case 'start':
        return await startServices(state, manifest);
      case 'stop':
        return await stopProcesses(state, Object.values(manifest.ports));
      case 'status':
        console.log(
          JSON.stringify({
            database: manifest.database,
            roles: manifest.roles,
            ports: manifest.ports,
            processes: ownedProcesses(state).map((record) => ({
              name: record.name,
              running: processIdentity(record.pid) === record.identity,
            })),
          }),
        );
        return;
      default:
        throw new Error('Unknown Beta action');
    }
  } finally {
    fs.closeSync(handle);
    fs.rmSync(lock, { force: true });
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
