import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import {
  assertExternal,
  configuration,
  portFree,
  processIdentity,
  privatePath,
  repo,
  postgresEnvironment,
  sessionProjectionDDL,
  startServices,
  stopProcesses,
  waitUntil,
  windowsPowerShellEnvironment,
} from '../../scripts/beta-ui/runner.mjs';

test('private state rejects repository paths including dot-prefixed child names', () => {
  for (const relative of [
    '',
    'beta-state',
    '..beta-state',
    '..beta-state/child',
  ]) {
    assert.throws(
      () => assertExternal(path.resolve(repo, relative)),
      /Private StateDir must be outside the repository/,
    );
  }
});

test('private state accepts actual parent and sibling paths', () => {
  for (const state of [
    path.dirname(repo),
    path.resolve(repo, '../beta-state'),
  ]) {
    assert.doesNotThrow(() => assertExternal(state));
  }
});

test(
  'private restriction replaces an Administrators default owner and verification rejects it',
  { skip: process.platform !== 'win32' },
  () => {
    const result = spawnSync(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-File',
        path.resolve('e2e/beta-ui/private-owner.test.ps1'),
      ],
      {
        encoding: 'utf8',
        windowsHide: true,
        env: windowsPowerShellEnvironment(),
      },
    );
    assert.equal(result.status, 0, result.stdout + result.stderr);
  },
);

test('Windows PowerShell children drop foreign module paths without changing build settings', () => {
  const source = {
    PSModulePath: 'core/modules',
    psmodulepath: 'foreign/modules',
    PATH: 'tools',
    JAVA_TOOL_OPTIONS: 'injected-options',
    AURIGA_PNPM_STORE: 'cache',
  };
  assert.deepEqual(windowsPowerShellEnvironment(source), {
    PATH: 'tools',
    JAVA_TOOL_OPTIONS: 'injected-options',
    AURIGA_PNPM_STORE: 'cache',
  });
  assert.equal(source.PSModulePath, 'core/modules');
});

test(
  'private ACL installation survives foreign module paths and rebuilding an existing directory',
  { skip: process.platform !== 'win32' },
  () => {
    const state = fs.mkdtempSync(path.join(os.tmpdir(), 'auriga-beta-acl-'));
    const target = path.join(state, 'static', 'admin');
    fs.mkdirSync(target, { recursive: true });
    const module = path.join(state, 'Microsoft.PowerShell.Security');
    fs.mkdirSync(module);
    fs.writeFileSync(
      path.join(module, 'Microsoft.PowerShell.Security.psd1'),
      "@{ RootModule='Microsoft.PowerShell.Security.psm1'; ModuleVersion='99.0'; GUID='66d12774-4058-4c85-a69b-4d2b59dfb8f6'; FunctionsToExport=@('Get-Acl','Set-Acl') }",
    );
    fs.writeFileSync(
      path.join(module, 'Microsoft.PowerShell.Security.psm1'),
      "throw 'Foreign PowerShell module must not load in Windows PowerShell'",
    );
    try {
      const clean = spawnSync(
        'powershell.exe',
        [
          '-NoProfile',
          '-NonInteractive',
          '-Command',
          'Import-Module Microsoft.PowerShell.Security -ErrorAction Stop; if ((Get-Command Set-Acl).Module.Path -notlike "$PSHOME\\Modules\\Microsoft.PowerShell.Security\\*") { exit 2 }',
        ],
        {
          encoding: 'utf8',
          windowsHide: true,
          env: windowsPowerShellEnvironment({
            ...process.env,
            PSModulePath: state,
          }),
        },
      );
      assert.equal(clean.status, 0, clean.stdout + clean.stderr);
      for (let build = 0; build < 3; build++) {
        const result = spawnSync(
          'powershell.exe',
          [
            '-NoProfile',
            '-NonInteractive',
            '-File',
            path.resolve('scripts/beta-ui/private.ps1'),
            '-Path',
            target,
          ],
          {
            encoding: 'utf8',
            windowsHide: true,
            env: { ...process.env, PSModulePath: state },
          },
        );
        assert.equal(result.status, 0, result.stdout + result.stderr);
        fs.mkdirSync(path.join(target, 'assets'), { recursive: true });
        const asset = path.join(target, 'assets', 'main.js');
        fs.writeFileSync(asset, `build ${build}`);
        privatePath(target);
        privatePath(asset, true);
        privatePath(asset);
        assert.equal(fs.readFileSync(asset, 'utf8'), `build ${build}`);
      }
    } finally {
      fs.rmSync(state, { recursive: true, force: true });
    }
  },
);

test(
  'stop removes only owned private projection result files',
  { skip: process.platform !== 'win32' },
  async () => {
    const state = fs.mkdtempSync(
      path.join(os.tmpdir(), 'auriga-beta-results-'),
    );
    const projections = path.join(state, 'session-projections');
    try {
      fs.mkdirSync(projections);
      privatePath(projections, true);
      fs.writeFileSync(
        path.join(projections, 'projection-12345.json'),
        'unit private result',
      );
      fs.writeFileSync(path.join(projections, 'operator-notes.txt'), 'keep');
      await stopProcesses(state, []);
      await stopProcesses(state, []);
      assert.deepEqual(fs.readdirSync(projections), ['operator-notes.txt']);
    } finally {
      fs.rmSync(state, { recursive: true, force: true });
    }
  },
);

test(
  'repeated start retains all five owned processes and stop releases their ports',
  { skip: process.platform !== 'win32' },
  async () => {
    const dir = fs.mkdtempSync(
      path.join(os.tmpdir(), 'auriga-beta-idempotent-'),
    );
    const children = [];
    const ports = [];
    try {
      for (let i = 0; i < 5; i++) {
        const socket = net.createServer();
        await new Promise((resolve) => socket.listen(0, '127.0.0.1', resolve));
        const port = socket.address().port;
        await new Promise((resolve) => socket.close(resolve));
        ports.push(port);
        children.push(
          spawn(
            process.execPath,
            ['-e', `require('net').createServer().listen(${port},'127.0.0.1')`],
            { windowsHide: true, stdio: 'ignore' },
          ),
        );
      }
      await waitUntil(
        async () =>
          (await Promise.all(ports.map(portFree))).every((free) => !free),
        'test processes did not start',
        10000,
      );
      const records = children.map((child, i) => ({
        name: ['platform', 'ai', 'network-broker', 'network', 'edge'][i],
        pid: child.pid,
        identity: processIdentity(child.pid),
      }));
      fs.writeFileSync(
        path.join(dir, 'processes.json'),
        JSON.stringify(records),
      );
      await startServices(dir, {
        dbReady: true,
        ports,
        roles: { edgeKv: 'owned-test-role' },
      });
      await startServices(dir, {
        dbReady: true,
        ports,
        roles: { edgeKv: 'owned-test-role' },
      });
      assert.deepEqual(
        JSON.parse(fs.readFileSync(path.join(dir, 'processes.json'))),
        records,
      );
      await stopProcesses(dir, ports);
      await stopProcesses(dir, ports);
      assert.ok((await Promise.all(ports.map(portFree))).every(Boolean));
    } finally {
      for (const child of children)
        if (processIdentity(child.pid)) child.kill();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  },
);

test('configuration uses real IAM, isolated roles, fixed loopback origins and disjoint ticket paths', () => {
  const manifest = {
    pgPort: 5432,
    database: 'auriga_beta_test',
    roles: { iam: 'iam', org: 'org', aiOwner: 'ai_owner', aiApp: 'ai_app' },
    ports: {
      portal: 18080,
      admin: 18082,
      instanceControl: 18083,
      platform: 18081,
      grpc: 19090,
      ai: 18084,
      networkSubmit: 18087,
      networkBroker: 18085,
      networkWebsocket: 18086,
    },
  };
  const secrets = Object.fromEntries(
    [
      'iam',
      'org',
      'aiOwner',
      'aiApp',
      'edgeSecret',
      'gatewaySecret',
      'encryptionKey',
    ].map((name) => [name, randomBytes(32).toString('base64url')]),
  );
  const cfg = configuration(
    path.resolve(os.tmpdir(), 'auriga-beta-test'),
    manifest,
    secrets,
  );
  assert.equal(cfg.sessionKV.username, manifest.roles.edgeKv);
  assert.equal(cfg.sessionKV.tenantId, cfg.access.tenantId);
  assert.equal(
    cfg.sessionKV.passFile,
    path.join(
      path.resolve(os.tmpdir(), 'auriga-beta-test'),
      'session-reader.pgpass',
    ),
  );
  assert.equal(cfg.sessionKV.password, undefined);
  assert.equal(cfg.platform['auriga.iam.session-kv-nats'], false);
  assert.equal(cfg.platform['auriga.events.nats-url'], '');
  assert.equal(cfg.access.portalOrigin.host, '127.0.0.1');
  assert.notEqual(cfg.access.portalOrigin.port, cfg.access.adminOrigin.port);
  assert.ok(
    Object.values(cfg.access.listeners).every(
      (listener) => listener.address === '127.0.0.1',
    ),
  );
  assert.notEqual(
    cfg.platform['auriga.iam.edge-token-export.token-file'],
    cfg.platform['auriga.iam.ai-gateway-token-export.token-file'],
  );
  assert.equal(
    cfg.ai.org.ServiceTokenFile,
    cfg.platform['auriga.iam.ai-gateway-token-export.token-file'],
  );
  assert.equal(
    cfg.edge.iamRpc.serviceTokenFile,
    cfg.platform['auriga.iam.edge-token-export.token-file'],
  );
  assert.equal(
    cfg.platform['auriga.iam.issuer'],
    `http://127.0.0.1:${manifest.ports.portal}`,
  );
  assert.equal(cfg.ai.issuer, cfg.platform['auriga.iam.issuer']);
  assert.notEqual(cfg.ai.store.dsn, cfg.ai.store.migrationDsn);
  assert.equal(cfg.edge.upstreams['network-submit'], 'https://127.0.0.1:18087');
  assert.equal(cfg.edge.networkSubmitHop.serverName, 'submit.network.internal');
  assert.notEqual(cfg.edge.networkSubmitHop.caFile, cfg.edge.iamHop.caFile);
  assert.notEqual(cfg.edge.networkSubmitHop.certFile, cfg.edge.iamHop.certFile);
  assert.equal(cfg.networkOptions.submitListen, '127.0.0.1:18087');
  assert.equal(
    cfg.networkOptions.iam.ServiceTokenFile,
    cfg.ai.org.ServiceTokenFile,
  );
  const profile = fs.readFileSync(
    new URL(
      '../../services/platform-app/src/main/resources/application-beta-ui.yaml',
      import.meta.url,
    ),
    'utf8',
  );
  assert.match(profile, /native-connect: true/);
  assert.match(
    profile,
    /urn:ietf:params:oauth:grant-type:device_code, refresh_token/,
  );
  assert.match(profile, /scopes: \[auriga.connect\]/);
});

test(
  'stop is idempotent, cleans owned random-port process and refuses a reused PID',
  { skip: process.platform !== 'win32' },
  async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'auriga-beta-process-'));
    const listener = net.createServer();
    await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
    const port = listener.address().port;
    await new Promise((resolve) => listener.close(resolve));
    const child = spawn(
      process.execPath,
      ['-e', `require('net').createServer().listen(${port},'127.0.0.1')`],
      { windowsHide: true, stdio: 'ignore' },
    );
    try {
      await waitUntil(
        async () => !(await portFree(port)),
        'test listener did not start',
        10000,
      );
      const identity = processIdentity(child.pid);
      assert.ok(identity);
      const file = path.join(dir, 'processes.json');
      fs.writeFileSync(
        file,
        JSON.stringify([
          { name: 'test', pid: child.pid, identity: identity + 'wrong' },
        ]),
      );
      await assert.rejects(stopProcesses(dir, [port]), /PID was reused/);
      assert.equal(processIdentity(child.pid), identity);
      fs.writeFileSync(
        file,
        JSON.stringify([{ name: 'test', pid: child.pid, identity }]),
      );
      await stopProcesses(dir, [port]);
      await stopProcesses(dir, [port]);
      assert.equal(await portFree(port), true);
      assert.deepEqual(JSON.parse(fs.readFileSync(file)), []);
    } finally {
      if (processIdentity(child.pid)) child.kill();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  },
);

test('session projection view grants only the installed tenant cache and rejects foreign role names', () => {
  const database = 'auriga_beta_0123456789';
  const manifest = {
    database,
    roles: { iam: database + '_iam', edgeKv: database + '_edgekv' },
  };
  const sql = sessionProjectionDDL(manifest);
  assert.match(sql, /security_barrier=true/);
  assert.match(
    sql,
    /FROM iam.session_kv WHERE tenant_id='00000000-0000-0000-0000-000000000001'::uuid/,
  );
  assert.match(
    sql,
    /GRANT SELECT ON edge_session_cache.sessions TO auriga_beta_0123456789_edgekv/,
  );
  assert.doesNotMatch(
    sql,
    /GRANT.*(?:INSERT|UPDATE|DELETE|ALL|BYPASSRLS|iam\.)/,
  );
  assert.throws(() =>
    sessionProjectionDDL({
      ...manifest,
      roles: { ...manifest.roles, edgeKv: 'other_owner' },
    }),
  );
});

test('PostgreSQL subprocesses use only the intended credential file', () => {
  const env = postgresEnvironment('private.pgpass', {
    PATH: 'tools',
    PGPASSWORD: 'unit-private-password',
    PGSERVICE: 'unrelated',
    PGHOST: 'remote.invalid',
    pgoptions: 'unrelated',
  });
  assert.equal(env.PGPASSFILE, 'private.pgpass');
  assert.equal(env.PATH, 'tools');
  assert.equal(env.PGPASSWORD, undefined);
  assert.equal(env.PGSERVICE, undefined);
  assert.equal(env.PGHOST, undefined);
  assert.equal(env.pgoptions, undefined);
  assert.equal(env.PGCONNECT_TIMEOUT, '5');
});
