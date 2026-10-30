#!/usr/bin/env node
// Uses public browser, settings and Session APIs. Only the local model is fixed.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { readFile, writeFile, mkdir, realpath } from 'node:fs/promises';
import { resolve, dirname, join, relative, isAbsolute } from 'node:path';
import { createRequire } from 'node:module';
import { isIP } from 'node:net';
import { pathToFileURL } from 'node:url';
import { clearTimeout } from 'node:timers';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';
import { prepareDesktop, launchDesktop, poll } from './cloud-desk.desktop.mjs';
import {
  createCloudMember,
  openExistingCloud,
  accessOrigin,
} from './cloud-desk.browser.mjs';
import { startFixture } from './cloud-desk.fixture.mjs';
import { openHttpsCloud, inviteDesktopMember } from './cloud-desk.http.mjs';
import {
  nativeClient,
  configureFixtureModel,
  loginDesktop,
  assertConversation,
  diagnoseNativeSession,
} from './cloud-desk.native.mjs';

const root = resolve(import.meta.dirname, '../..');
const json = async (path) => JSON.parse(await readFile(resolve(path), 'utf8'));
const fail = (code) => {
  throw Object.assign(new Error(code), { code });
};
const marker = (runId, label) => `CLOUD_DESK_${runId}_${label}`;

function privateIPv4(host) {
  if (isIP(host) !== 4) return false;
  const [a, b] = host.split('.').map(Number);
  return (
    a === 127 ||
    a === 10 ||
    (a === 192 && b === 168) ||
    (a === 172 && b >= 16 && b <= 31)
  );
}

export function validateRunConfig(cfg, access) {
  accessOrigin(access.portalOrigin);
  accessOrigin(access.adminOrigin);
  assert.ok(['domain', 'ip'].includes(access.mode));
  if (cfg.httpsCloud) {
    assert.equal(
      access.mode,
      'domain',
      'public HTTPS cookie transport currently verifies domain mode',
    );
    assert.equal(access.portalOrigin.scheme, 'https');
    assert.ok(cfg.caFile && cfg.dnsMappings && cfg.httpsCloud.credentialsFile);
  }
  const connect = cfg.connect;
  if (
    !connect?.network?.roots?.length ||
    !connect.network.realm ||
    !connect.network.wssUrl
  )
    fail('installation_network_config_missing');
  const platform = new URL(connect.platformOrigin);
  // DNS HTTP cannot be used by the native desktop connect runtime.
  if (
    platform.protocol === 'http:' &&
    !privateIPv4(platform.hostname) &&
    platform.hostname !== '[::1]'
  )
    fail('desktop_platform_origin_not_supported');
  assert.equal(new URL(connect.network.wssUrl).protocol, 'wss:');
  assert.ok(connect.providerId && connect.clientId && connect.issuer);
  assert.equal(new URL(connect.issuer).origin, platform.origin);
  assert.equal(
    platform.origin,
    accessOrigin(access.portalOrigin),
    'desktop and cloud use the same installation',
  );
  assert.ok(
    cfg.cloudCwd &&
      cfg.desktopDescriptor &&
      (cfg.httpsCloud || cfg.existingCloud || cfg.enrollmentFile),
  );
  if (cfg.fixture?.cloudInstanceId) {
    assert.equal(
      cfg.fixture.cloudInstanceId,
      cfg.httpsCloud?.instanceId ?? cfg.existingCloud?.instanceId,
      'model fixture must run in the authenticated cloud instance',
    );
    assert.match(cfg.fixture.cloudInstanceId, /^[a-f0-9-]{36}$/);
    return;
  }
  assert.ok(cfg.fixture?.cloudHost && cfg.fixture?.desktopHost);
  for (const host of [cfg.fixture.cloudHost, cfg.fixture.desktopHost]) {
    assert.ok(
      privateIPv4(host),
      'fixed model endpoint must stay on the explicitly configured local/private test network',
    );
  }
}

export async function startModelHost(cfg, runId) {
  if (cfg.cloudInstanceId)
    return startIsolatedModelHost(cfg.cloudInstanceId, runId);
  let port, keys, stop;
  if (!cfg.wsl) {
    const fixture = await startFixture({
      host: cfg.bind ?? '127.0.0.1',
      runId,
    });
    ({ port, keys } = fixture);
    stop = () => fixture.close();
  } else {
    assert.equal(process.platform, 'win32');
    assert.ok(cfg.wsl.node && cfg.wsl.distribution);
    const file = execFileSync(
      'wsl.exe',
      [
        '-d',
        cfg.wsl.distribution,
        '--exec',
        'wslpath',
        '-a',
        resolve(import.meta.dirname, 'cloud-desk.fixture.mjs'),
      ],
      { windowsHide: true, encoding: 'utf8', timeout: 10000 },
    ).trim();
    const child = spawn(
      'wsl.exe',
      [
        '-d',
        cfg.wsl.distribution,
        '--exec',
        'env',
        `AURIGA_CLOUD_DESK_RUN=${runId}`,
        `AURIGA_CLOUD_DESK_FIXTURE_BIND=${cfg.bind ?? '127.0.0.1'}`,
        cfg.wsl.node,
        file,
        '--serve',
      ],
      { windowsHide: true, stdio: ['ignore', 'pipe', 'ignore'] },
    );
    let announcement,
      exited = false,
      buffer = '';
    const done = new Promise((resolve) =>
      child.once('close', (code) => {
        exited = true;
        resolve({ code });
      }),
    );
    child.once('error', () => {
      exited = true;
    });
    child.stdout.on('data', (chunk) => {
      buffer += chunk;
      if (buffer.length > 8192) return child.kill();
      const line = buffer.split('\n')[0];
      try {
        const value = JSON.parse(line);
        if (value.port && value.keys) announcement = value;
      } catch {
        /* Wait for the complete private startup line. */
      }
    });
    try {
      ({ port, keys } = await poll(
        () => {
          if (exited) fail('fixture_process_exited');
          return announcement;
        },
        'fixture_startup_missing',
        15000,
      ));
    } catch (error) {
      child.kill();
      await done;
      throw error;
    }
    stop = async () => {
      try {
        await control('stop');
      } finally {
        const result = await Promise.race([
          done,
          new Promise((resolve) => {
            const timer = setTimeout(() => resolve(null), 10000);
            timer.unref();
          }),
        ]);
        if (!result) {
          child.kill();
          await done;
          fail('fixture_shutdown_failed');
        }
      }
    };
  }
  assert.ok(Number.isInteger(port) && port > 0 && port < 65536);
  for (const value of Object.values(keys))
    assert.match(value, /^[a-f0-9-]{36}$/);
  const control = async (op, value) => {
    const response = await globalThis.fetch(
      `http://${cfg.desktopHost}:${port}/control/${keys.control}`,
      {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ op, value }),
        signal: globalThis.AbortSignal.timeout(10000),
      },
    );
    assert.equal(response.status, 200, 'owned fixture control');
    return response.json();
  };
  return {
    control,
    stop,
    urls: Object.fromEntries(
      ['cloud', 'desktop'].map((side) => [
        side,
        `http://${cfg[side + 'Host']}:${port}/${side}/${keys[side]}/v1`,
      ]),
    ),
  };
}

function containerNode(instanceId, source) {
  assert.match(instanceId, /^[a-f0-9-]{36}$/);
  const child = spawn(
    'wsl.exe',
    [
      '-d',
      'Ubuntu',
      '--exec',
      'docker',
      'exec',
      '-i',
      'auriga-inst-' + instanceId,
      'node',
      '--input-type=module',
    ],
    { windowsHide: true, stdio: ['pipe', 'pipe', 'ignore'] },
  );
  let output = '',
    closed = false;
  const done = new Promise((resolve) =>
    child.once('close', (code) => {
      closed = true;
      resolve({ code });
    }),
  );
  child.once('error', () => {});
  child.stdout.on('data', (chunk) => {
    if (output.length < 8 * 1024 * 1024) output += chunk;
  });
  child.stdin.on('error', () => {});
  child.stdin.end(source);
  return {
    child,
    done,
    get output() {
      return output;
    },
    get closed() {
      return closed;
    },
  };
}

async function startIsolatedModelHost(instanceId, runId) {
  const source = await readFile(
    resolve(import.meta.dirname, 'cloud-desk.fixture.mjs'),
    'utf8',
  );
  const cloud = containerNode(
    instanceId,
    source +
      `\nconst owned=await startFixture({host:'127.0.0.1',runId:${JSON.stringify(runId)}});process.stdout.write(JSON.stringify({port:owned.port,keys:owned.keys})+'\\n');`,
  );
  let announcement, desktop;
  try {
    announcement = await poll(
      () => {
        if (cloud.closed) fail('fixture_process_exited');
        try {
          return JSON.parse(cloud.output.trim());
        } catch {
          return null;
        }
      },
      'container_fixture_startup_missing',
      15000,
    );
    assert.ok(Number.isInteger(announcement.port));
    for (const key of Object.values(announcement.keys))
      assert.match(key, /^[a-f0-9-]{36}$/);
    desktop = await startFixture({ runId });
  } catch (error) {
    cloud.child.kill();
    await cloud.done;
    if (desktop) await desktop.close();
    throw error;
  }
  const cloudControl = async (op, value) => {
    const call = containerNode(
      instanceId,
      `try{const r=await fetch(${JSON.stringify(`http://127.0.0.1:${announcement.port}/control/${announcement.keys.control}`)},{method:'POST',headers:{'content-type':'application/json'},body:${JSON.stringify(JSON.stringify({ op, value }))},signal:AbortSignal.timeout(5000)});console.log(JSON.stringify({status:r.status,body:await r.json()}));}catch{console.log(JSON.stringify({status:0}));}`,
    );
    const timer = setTimeout(() => call.child.kill(), 10000);
    try {
      await call.done;
      const response = JSON.parse(call.output);
      assert.equal(response.status, 200, 'owned in-container fixture control');
      return response.body;
    } finally {
      clearTimeout(timer);
    }
  };
  const desktopControl = (op, value) => {
    if (op === 'state') return desktop.state.snapshot();
    if (op === 'plan') return { marker: desktop.state.plan(value) };
    if (op === 'expect')
      return { marker: desktop.state.expect(value.side, value.label) };
    fail('invalid_control_operation');
  };
  const control = async (op, value) => {
    if (op !== 'state')
      return value?.side === 'cloud'
        ? cloudControl(op, value)
        : desktopControl(op, value);
    const a = await cloudControl('state'),
      b = desktopControl('state');
    return {
      runId,
      requests: a.requests + b.requests,
      tools: [...a.tools, ...b.tools],
      receipts: [...a.receipts, ...b.receipts],
      failures: [...a.failures, ...b.failures],
      modelRequests: [...(a.modelRequests ?? []), ...(b.modelRequests ?? [])],
    };
  };
  return {
    control,
    urls: {
      cloud: `http://127.0.0.1:${announcement.port}/cloud/${announcement.keys.cloud}/v1`,
      desktop: desktop.urls.desktop,
    },
    async stop() {
      try {
        await cloudControl('stop');
        await cloud.done;
      } finally {
        await desktop.close();
      }
    },
  };
}

export async function run(cfg, output) {
  output = resolve(output);
  const rel = relative(root, output);
  assert.ok(rel && !rel.startsWith('..') && !isAbsolute(rel));
  assert.equal(await realpath(dirname(output)), dirname(output));
  await mkdir(output, { mode: 0o700 }); // A run never overwrites existing evidence.
  if (process.platform === 'win32') privatePath(output, true);
  const report = {
    runId: randomUUID(),
    status: 'failed',
    stage: 'preflight',
    httpFailures: [],
    cleanup: [],
  };
  const stage = (value) => {
    report.stage = value;
    process.stdout.write(`STEP ${value}\n`);
  };
  const cleanup = [];
  let cloud, desktop, model;
  try {
    const access = await json(cfg.accessFile);
    validateRunConfig(cfg, access);
    const descriptorPath = resolve(cfg.desktopDescriptor);
    const descriptorRelative = relative(root, descriptorPath);
    assert.ok(
      descriptorRelative &&
        !descriptorRelative.startsWith('..') &&
        !isAbsolute(descriptorRelative),
      'desktop descriptor belongs to this worktree',
    );
    assert.equal(await realpath(descriptorPath), descriptorPath);
    const descriptor = await json(descriptorPath);
    const member = cfg.desktopMemberFile
      ? await json(cfg.desktopMemberFile)
      : null;
    if (descriptor.usedForLiveRun) {
      assert.ok(member?.accountId && cfg.resumeDesktop === true);
      assert.deepEqual(
        descriptor.liveIdentity,
        {
          accountId: member.accountId,
          platformOrigin: cfg.connect.platformOrigin,
        },
        'resume cannot change identity or installation',
      );
    }
    // The credentials belong only to this new disposable stack, never to a user vault.
    if (cfg.httpsCloud || cfg.existingCloud) await requireTestLock();
    if (cfg.httpsCloud) {
      cloud = await openHttpsCloud({ cfg, access, report, stage });
    } else {
      const { chromium } = createRequire(
        join(
          resolve(cfg.playwrightModuleRoot ?? import.meta.dirname),
          'package.json',
        ),
      )('playwright');
      cloud = await (cfg.existingCloud ? openExistingCloud : createCloudMember)(
        {
          chromium,
          access,
          ...(cfg.existingCloud
            ? {
                member: await json(cfg.existingCloud.credentialsFile),
                instanceId: cfg.existingCloud.instanceId,
              }
            : { enrollment: await json(cfg.enrollmentFile) }),
          resolveLoopback: cfg.resolveLoopback === true,
          report,
          stage,
        },
      );
    }
    stage('desktop-login');
    descriptor.usedForLiveRun = true;
    if (member?.accountId)
      descriptor.liveIdentity = {
        accountId: member.accountId,
        platformOrigin: cfg.connect.platformOrigin,
      };
    await writeFile(descriptorPath, JSON.stringify(descriptor, null, 2));
    desktop = await launchDesktop(descriptor, {
      connect: cfg.connect,
      caFile: cfg.caFile,
      dnsMappings: cfg.dnsMappings,
    });
    report.desktop = {
      installation: descriptor.installation,
      packageVersion: descriptor.packageVersion,
      packageSHA256: descriptor.packageSHA256,
      dshVersion: descriptor.dshVersion,
      ...(await loginDesktop(desktop.request, member ?? cloud.member, {
        resume: cfg.resumeDesktop === true,
        reauthenticate: cfg.reauthenticateDesktop === true,
      })),
    };
    assert.notEqual(
      report.desktop.accountId,
      report.cloud.ownerId,
      'cloud and desktop use different members',
    );
    const clients = {
      cloud: nativeClient(cloud.request),
      desktop: nativeClient(desktop.request),
    };
    for (const side of ['cloud', 'desktop']) {
      stage(side + '-network-ready');
      await poll(
        async () => {
          const status = await (
            side === 'cloud' ? cloud.request : desktop.request
          )('/auriga/local-login/status');
          report[side].network = {
            available: status.network?.available === true,
            connected: status.network?.connected === true,
            state: /^[a-z_]+$/.test(status.network?.state ?? '')
              ? status.network.state
              : 'unknown',
          };
          return status.network?.connected === true;
        },
        side + '_network_not_ready',
        90000,
      );
      report[side].nativePlugin = await clients[side].activePlugin();
    }
    stage('local-model-config');
    model = await startModelHost(cfg.fixture, report.runId);
    const provider = 'cloud-desk-' + randomBytes(6).toString('hex');
    const sessions = {};
    for (const side of ['cloud', 'desktop']) {
      const client = clients[side];
      await configureFixtureModel(client, model.urls[side], provider, cleanup);
      sessions[side] = await client.createSession(
        side === 'cloud' ? cfg.cloudCwd : join(descriptor.state, 'session-cwd'),
        provider,
      );
      cleanup.push(() => client.cancel());
    }
    report.sessions = sessions;
    const state = () => model.control('state');
    const waitTool = (label, status) =>
      poll(
        async () => {
          const current = await state();
          if (current.failures.length) fail(current.failures[0].code);
          const tool = current.tools.find((t) => t.label === label);
          if (!tool?.result) return null;
          if (tool.result.status !== status)
            fail(tool.result.code ?? 'native_tool_result_rejected');
          return tool.result;
        },
        'native_tool_result_missing',
        90000,
      );
    const addresses = {};
    for (const side of ['cloud', 'desktop']) {
      stage(side + '-native-network-join');
      const label = side + '_join';
      await model.control('plan', {
        side,
        label,
        tool: 'auriga_network_join',
        args: {},
      });
      await clients[side].prompt(sessions[side], marker(report.runId, label));
      addresses[side] = (await waitTool(label, 'registered')).address;
      assert.match(addresses[side], /^aur1:[0-9a-f]+\.[0-9a-f]+\.[0-9a-f]+$/);
    }
    assert.notEqual(addresses.cloud, addresses.desktop);
    for (const side of ['cloud', 'desktop']) {
      stage(side + '-receive-enable');
      const request = side === 'cloud' ? cloud.request : desktop.request;
      const local = BigInt('0x' + addresses[side].split('.').at(-1)).toString();
      const setReceive = async (enabled) => {
        const status = await request('/auriga/local-login/status');
        return request(
          '/auriga/local-login/messaging/receive',
          { local, enabled },
          status.csrf,
        );
      };
      await setReceive(true);
      cleanup.push(() => setReceive(false));
    }
    for (const [sender, receiver, direction] of [
      ['desktop', 'cloud', 'd2c'],
      ['cloud', 'desktop', 'c2d'],
    ]) {
      stage(direction + '-request-and-reply');
      const requestLabel = direction + '_request',
        replyLabel = direction + '_reply';
      await model.control('expect', { side: receiver, label: requestLabel });
      await model.control('expect', { side: sender, label: replyLabel });
      const sendArgs = (target, label) => ({
        target,
        body: { subtype: 'text', text: marker(report.runId, label) },
      });
      await model.control('plan', {
        side: receiver,
        label: replyLabel,
        tool: 'auriga_send_message',
        args: sendArgs(addresses[sender], replyLabel),
        trigger: marker(report.runId, requestLabel),
        external: true,
      });
      await model.control('plan', {
        side: sender,
        label: requestLabel,
        tool: 'auriga_send_message',
        args: sendArgs(addresses[receiver], requestLabel),
      });
      await clients[sender].prompt(
        sessions[sender],
        marker(report.runId, requestLabel),
      );
      await waitTool(requestLabel, 'request_submitted');
      await waitTool(replyLabel, 'request_submitted');
      await poll(
        async () =>
          (await state()).receipts.some(
            (r) =>
              r.side === sender &&
              r.marker === marker(report.runId, replyLabel),
          ),
        'native_reply_not_received',
        90000,
      );
    }
    report.fixture = await state();
    assertConversation(report, report.fixture);
    stage('conversation-verified');
    report.status = 'passed';
  } catch (error) {
    report.failure = {
      stage: report.stage,
      type: /^[A-Za-z]+$/.test(error.name ?? '') ? error.name : 'Error',
      code: /^[a-z_.]+$/.test(error.code ?? '')
        ? error.code
        : 'assertion_or_transport_failed',
    };
    if (/^[a-z/]+$/.test(error.rpcMethod ?? ''))
      report.failure.rpcMethod = error.rpcMethod;
    if (Number.isInteger(error.status))
      report.failure.httpStatus = error.status;
    if (/^[a-f0-9]{32}$/.test(error.traceId ?? ''))
      report.failure.traceId = error.traceId;
    if (/^[a-z_.]+$/.test(error.remoteCode ?? ''))
      report.failure.remoteCode = error.remoteCode;
  } finally {
    if (model) {
      try {
        report.fixture = await model.control('state');
      } catch {
        report.cleanup.push({ action: 'fixture-state', status: 'failed' });
      }
    }
    for (const action of cleanup.reverse()) {
      try {
        await action();
      } catch {
        report.cleanup.push({ action: 'native-test-state', status: 'failed' });
      }
    }
    for (const [name, action] of [
      ['fixture', model && (() => model.stop())],
      ['desktop', desktop && (() => desktop.stop())],
      ['browser', cloud && (() => cloud.browser.close())],
    ]) {
      if (!action) continue;
      try {
        const exit = await action();
        report.cleanup.push({
          action: name,
          status: 'passed',
          ...(name === 'desktop' ? { exit } : {}),
        });
      } catch {
        report.cleanup.push({ action: name, status: 'failed' });
      }
    }
    if (report.cleanup.some((v) => v.status === 'failed'))
      report.status = 'failed';
    await writeFile(
      join(output, 'report.json'),
      JSON.stringify(report, null, 2),
    );
  }
  return report;
}

async function requireTestLock() {
  const owner = await readFile(
    resolve(root, '../../agent-cache/locks/compose-stack/owner.txt'),
    'utf8',
  );
  assert.match(
    owner,
    /auriga-SEND-FIX R(?:[456] 只测不重建|7 仅重建 platform 应用来源权威配置)/,
    'shared-stack requests require this task own test lock',
  );
}

async function probeModelTransport(instanceId, url) {
  assert.match(instanceId, /^[a-f0-9-]{36}$/);
  return new Promise((resolve) => {
    const child = spawn(
      'wsl.exe',
      [
        '-d',
        'Ubuntu',
        '--exec',
        'docker',
        'exec',
        '-i',
        'auriga-inst-' + instanceId,
        'node',
        '--input-type=module',
      ],
      { windowsHide: true, stdio: ['pipe', 'pipe', 'ignore'] },
    );
    let output = '';
    const timer = setTimeout(() => child.kill(), 15000);
    child.stdout.on('data', (chunk) => {
      if (output.length < 8192) output += chunk;
    });
    child.once('error', () => {});
    child.once('close', () => {
      clearTimeout(timer);
      try {
        const value = JSON.parse(output);
        resolve({
          status: Number.isInteger(value.status) ? value.status : null,
          reachable: value.reachable === true,
          ...(value.code && /^[A-Z_]+$/.test(value.code)
            ? { code: value.code }
            : {}),
        });
      } catch {
        resolve({ reachable: false, code: 'TRANSPORT_PROBE_FAILED' });
      }
    });
    // Capability URL stays on the owned process's stdin, never in argv or logs.
    child.stdin.end(
      `try { const r=await fetch(${JSON.stringify(url + '/models')},{signal:AbortSignal.timeout(5000)});await r.arrayBuffer();console.log(JSON.stringify({reachable:true,status:r.status})); } catch(e){console.log(JSON.stringify({reachable:false,code:e.cause?.code??'FETCH_FAILED'}));}`,
    );
  });
}

// Isolates the real managed-host capability check when a desktop cannot yet join.
// A successful probe is never a successful conversation acceptance run.
export async function probeCloud(cfg, output) {
  output = resolve(output);
  const rel = relative(root, output);
  assert.ok(rel && !rel.startsWith('..') && !isAbsolute(rel));
  assert.equal(await realpath(dirname(output)), dirname(output));
  await mkdir(output, { mode: 0o700 });
  if (process.platform === 'win32') privatePath(output, true);
  const report = {
    runId: randomUUID(),
    status: 'failed',
    conversationPassed: false,
    stage: 'preflight',
    httpFailures: [],
    cleanup: [],
  };
  const cleanup = [];
  let cloud, model, client, session;
  const stage = (value) => {
    report.stage = value;
    process.stdout.write(`STEP ${value}\n`);
  };
  try {
    await requireTestLock();
    const access = await json(cfg.accessFile);
    assert.ok(
      cfg.existingCloud?.credentialsFile &&
        cfg.existingCloud.instanceId &&
        cfg.cloudCwd,
    );
    if (cfg.fixture?.cloudInstanceId)
      assert.equal(cfg.fixture.cloudInstanceId, cfg.existingCloud.instanceId);
    const { chromium } = createRequire(
      join(
        resolve(cfg.playwrightModuleRoot ?? import.meta.dirname),
        'package.json',
      ),
    )('playwright');
    cloud = await openExistingCloud({
      chromium,
      access,
      member: await json(cfg.existingCloud.credentialsFile),
      instanceId: cfg.existingCloud.instanceId,
      resolveLoopback: cfg.resolveLoopback === true,
      report,
      stage,
    });
    stage('cloud-native-inventory');
    client = nativeClient(cloud.request);
    report.cloud.nativePlugin = await client.activePlugin();
    stage('cloud-public-model-settings');
    const described = await client.rpc('settings/describe');
    report.cloud.settings = {
      writable: described.writable,
      namespaces: described.namespaces.map(({ ns }) => ns),
    };
    stage('cloud-fixed-model-config');
    model = await startModelHost(cfg.fixture, report.runId);
    report.cloud.modelTransport = await probeModelTransport(
      cfg.existingCloud.instanceId,
      model.urls.cloud,
    );
    const provider = 'cloud-desk-' + randomBytes(6).toString('hex');
    await configureFixtureModel(client, model.urls.cloud, provider, cleanup);
    stage('cloud-native-session');
    cleanup.push(() => client.cancel());
    session = await client.createSession(cfg.cloudCwd, provider);
    report.cloud.sessionId = session;
    stage('cloud-native-network-join');
    await model.control('plan', {
      side: 'cloud',
      label: 'cloud_join',
      tool: 'auriga_network_join',
      args: {},
    });
    await client.prompt(session, marker(report.runId, 'cloud_join'));
    await poll(
      async () => {
        const state = await model.control('state');
        if (state.failures.length) fail(state.failures[0].code);
        const result = state.tools.find(
          (p) => p.label === 'cloud_join',
        )?.result;
        if (!result) return false;
        if (result.status !== 'registered')
          fail(result.code ?? 'native_tool_result_rejected');
        return true;
      },
      'cloud_native_tool_not_executed',
      45000,
    );
    report.status = 'probe-passed';
  } catch (error) {
    report.failure = {
      stage: report.stage,
      type: error.name,
      code: /^[a-z_.]+$/.test(error.code ?? '')
        ? error.code
        : 'assertion_or_transport_failed',
      ...(error.rpcMethod ? { rpcMethod: error.rpcMethod } : {}),
      ...(error.remoteCode ? { remoteCode: error.remoteCode } : {}),
      ...(Number.isInteger(error.status) ? { httpStatus: error.status } : {}),
    };
    if (client && session) {
      try {
        report.cloud.sessionDiagnostic = await diagnoseNativeSession(
          client,
          session,
        );
      } catch {
        report.cloud.sessionDiagnostic = { available: false };
      }
    }
  } finally {
    if (model) {
      try {
        report.fixture = await model.control('state');
      } catch {
        report.cleanup.push({ action: 'fixture-state', status: 'failed' });
      }
    }
    for (const action of cleanup.reverse()) {
      try {
        await action();
      } catch {
        report.cleanup.push({ action: 'native-test-state', status: 'failed' });
      }
    }
    for (const [name, action] of [
      ['fixture', model && (() => model.stop())],
      ['browser', cloud && (() => cloud.browser.close())],
    ]) {
      if (!action) continue;
      try {
        await action();
        report.cleanup.push({ action: name, status: 'passed' });
      } catch {
        report.cleanup.push({ action: name, status: 'failed' });
      }
    }
    if (report.cleanup.some((row) => row.status === 'failed'))
      report.status = 'failed';
    await writeFile(
      join(output, 'report.json'),
      JSON.stringify(report, null, 2),
    );
  }
  return report;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const [command, configFile, output] = process.argv.slice(2);
  assert.ok(
    ['prepare', 'run', 'probe-cloud', 'invite-member'].includes(command) &&
      configFile &&
      (['prepare', 'invite-member'].includes(command) || output),
    'Usage: node e2e/phase1/cloud-desk.mjs prepare CONFIG | invite-member CONFIG | run CONFIG NEW_OUTPUT_DIR | probe-cloud CONFIG NEW_OUTPUT_DIR',
  );
  const cfg = await json(configFile);
  if (command === 'invite-member') {
    await requireTestLock();
    const result = await inviteDesktopMember(cfg, await json(cfg.accessFile), {
      httpFailures: [],
      stage: 'invite-desktop-member',
    });
    process.stdout.write(JSON.stringify(result) + '\n');
  } else if (command === 'prepare') {
    const prepared = await prepareDesktop({ ...cfg, root });
    process.stdout.write(
      JSON.stringify({
        installation: prepared.installation,
        dshVersion: prepared.dshVersion,
        packageSHA256: prepared.packageSHA256,
      }) + '\n',
    );
  } else {
    const report = await (command === 'probe-cloud' ? probeCloud : run)(
      cfg,
      output,
    );
    process.stdout.write(
      JSON.stringify({
        status: report.status,
        stage: report.stage,
        failure: report.failure,
      }) + '\n',
    );
    if (!['passed', 'probe-passed'].includes(report.status))
      process.exitCode = 1;
  }
}
