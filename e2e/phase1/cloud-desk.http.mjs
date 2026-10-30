import assert from 'node:assert/strict';
import https from 'node:https';
import dns from 'node:dns';
import { randomBytes } from 'node:crypto';
import { readFile, writeFile, realpath, lstat } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';
import { CookieJar } from '../lib/cookie-jar.mjs';
import { createTestLookup } from './cloud-desk.dns.mjs';
import {
  accessOrigin,
  assessManagedPlugin,
  loginOtpField,
} from './cloud-desk.browser.mjs';
import { authenticatorCode } from '../network/support/dual-cli.mjs';
import { poll } from './cloud-desk.desktop.mjs';

export async function httpsTransport(
  origin,
  { caFile, dnsMappings, report, jar = new CookieJar() },
) {
  const base = new URL(origin);
  assert.equal(base.protocol, 'https:');
  assert.equal(base.origin, origin);
  const agent = new https.Agent({
    ca: await readFile(caFile),
    rejectUnauthorized: true,
    lookup: createTestLookup(dnsMappings, dns.lookup.bind(dns)),
  });
  const inspect = (
    path,
    body,
    csrf,
    method = body === undefined ? 'GET' : 'POST',
  ) =>
    new Promise((resolve, reject) => {
      const url = new URL(path, base);
      assert.equal(
        url.origin,
        base.origin,
        'public request stays on its authenticated origin',
      );
      const cookie = jar.cookieHeader({
        host: url.host,
        path: url.pathname,
        secure: true,
      });
      const headers = {
        origin: base.origin,
        cookie,
        accept: 'application/json',
        traceparent: `00-${randomBytes(16).toString('hex')}-${randomBytes(8).toString('hex')}-01`,
      };
      const cookieCsrf = cookie.match(
        /(?:^|; )(?:__Host-)?auriga_csrf=([^;]+)/,
      )?.[1];
      if (csrf ?? cookieCsrf) headers['x-auriga-csrf'] = csrf ?? cookieCsrf;
      const payload = body === undefined ? undefined : JSON.stringify(body);
      if (payload) headers['content-type'] = 'application/json';
      const req = https.request(url, { agent, method, headers }, (res) => {
        if (!res.socket.authorized)
          return req.destroy(new Error('public_https_unverified'));
        let raw = '';
        res.on('data', (chunk) => {
          raw += chunk;
          if (raw.length > 16 * 1024 * 1024)
            req.destroy(new Error('public_response_too_large'));
        });
        res.once('end', () => {
          jar.storeFromResponse(res.headers['set-cookie'], {
            host: url.host,
            path: url.pathname,
            secure: true,
          });
          let value;
          try {
            value = JSON.parse(raw);
          } catch {
            /* HTML is never identity or RPC evidence. */
          }
          const traceId = res.headers['x-trace-id'];
          const result = {
            status: res.statusCode,
            body: value,
            location: res.headers.location,
            ...(/^[a-f0-9]{32}$/.test(traceId ?? '') ? { traceId } : {}),
          };
          if (result.status >= 400 && report?.httpFailures?.length < 30)
            report.httpFailures.push({
              stage: report.stage,
              status: result.status,
              ...(result.traceId ? { traceId: result.traceId } : {}),
            });
          resolve(result);
        });
      });
      req.setTimeout(20000, () =>
        req.destroy(
          Object.assign(new Error('public_https_timeout'), {
            code: 'public_https_timeout',
          }),
        ),
      );
      req.once('error', () =>
        reject(
          Object.assign(new Error('public_https_transport_failed'), {
            code: 'public_https_transport_failed',
          }),
        ),
      );
      req.end(payload);
    });
  const request = async (...args) => {
    const response = await inspect(...args);
    if (![200, 201, 204].includes(response.status))
      throw Object.assign(new Error('public_https_request_failed'), {
        code: 'public_https_request_failed',
        status: response.status,
        ...(response.traceId ? { traceId: response.traceId } : {}),
        ...(/^[a-z_.]+$/.test(response.body?.code ?? '')
          ? { remoteCode: response.body.code }
          : {}),
      });
    return response.body;
  };
  return { jar, inspect, request, close: () => agent.destroy() };
}

async function otp(credentials) {
  assert.ok(credentials.totpSecret);
  const previous =
    credentials.lastTotpCounter ?? Math.floor(Date.now() / 30000);
  await poll(
    () =>
      Math.floor(Date.now() / 30000) > previous &&
      30000 - (Date.now() % 30000) > 5000,
    'fresh_authenticator_window',
  );
  credentials.lastTotpCounter = Math.floor(Date.now() / 30000);
  return authenticatorCode(credentials.totpSecret);
}

export async function publicLogin(
  transport,
  credentials,
  { invitationCode, providerId } = {},
) {
  const id = providerId ?? credentials.providerId;
  assert.ok(
    id,
    'provider comes from real discovery or installation enrollment',
  );
  let tx = await transport.request('/api/iam/v1/login/transactions', {
    providerId: id,
    returnTo: '/',
    ...(invitationCode ? { invitationCode } : {}),
  });
  assert.equal(tx.step?.type, 'form');
  const advance = async (values) =>
    transport.request('/api/iam/v1/login/transactions/' + tx.id + '/advance', {
      type: 'form',
      formId: tx.step.formId,
      values,
    });
  const values = {
    username: credentials.username,
    password: credentials.password,
  };
  const fields = tx.step.fields ?? [];
  if (fields.some((field) => field.name === 'displayName'))
    values.displayName = credentials.displayName;
  if (fields.some((field) => field.kind === 'otp'))
    values[loginOtpField(tx.step)] = await otp(credentials);
  tx = await advance(values);
  if (tx.status === 'pending' && tx.step?.formId === 'first_totp') {
    const enrollment = tx.step.fields.find(
      (field) => field.name === 'enrollment',
    )?.label.en;
    credentials.totpSecret = enrollment?.match(/([A-Z2-7]{16,})\s*$/)?.[1];
    assert.ok(
      credentials.totpSecret,
      'enrollment secret only from production login form',
    );
    tx = await advance({ [loginOtpField(tx.step)]: await otp(credentials) });
  } else if (
    tx.status === 'pending' &&
    ['totp', 'platform_totp'].includes(tx.step?.formId)
  ) {
    tx = await advance({ [loginOtpField(tx.step)]: await otp(credentials) });
  }
  assert.equal(tx.status, 'completed', 'real IAM transaction must complete');
  const me = await transport.request('/api/iam/v1/me');
  assert.match(me.account.id, /^[a-f0-9-]{36}$/);
  return me.account.id;
}

export function desktopMemberRole(roles) {
  const choices = roles.items.filter(
    (role) => role.key === 'member' && role.builtin === true,
  );
  assert.equal(
    choices.length,
    1,
    'discover one built-in member role, never an admin role',
  );
  assert.match(choices[0].id, /^[a-f0-9-]{36}$/);
  assert.ok(
    choices[0].permissions.includes('auriga.connect.collab.use'),
    'normal member role must authorize collaboration',
  );
  return choices[0].id;
}

export async function ensureDesktopMemberRole(admin, accountId, roleId) {
  assert.match(accountId, /^[a-f0-9-]{36}$/);
  assert.match(roleId, /^[a-f0-9-]{36}$/);
  const bindings = await admin.request(
    '/api/org/v1/admin/members/' + accountId + '/role-bindings',
  );
  if (!bindings.items.some((binding) => binding.roleId === roleId)) {
    await admin.request('/api/org/v1/admin/role-bindings', {
      roleId,
      subject: { type: 'member', id: accountId },
    });
  }
}

export async function validateMemberCredentialsPath(file) {
  const target = resolve(file);
  const parent = dirname(target);
  assert.equal(
    await realpath(parent),
    parent,
    'member credentials parent must not contain links',
  );
  const check = async (path, directory) => {
    const info = await lstat(path);
    assert.ok(
      directory ? info.isDirectory() : info.isFile(),
      'member credentials require a regular private path',
    );
    if (process.platform === 'win32') privatePath(path);
    else {
      assert.equal(
        info.uid,
        process.getuid(),
        'member credentials must be owned by the current user',
      );
      assert.equal(
        info.mode & 0o077,
        0,
        'member credentials must not be accessible by group or others',
      );
    }
  };
  // Validate before login/invitation, never chmod an arbitrary caller directory.
  await check(parent, true);
  try {
    await check(target, false);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  return target;
}

export async function writeMemberCredentials(file, member) {
  const target = await validateMemberCredentialsPath(file);
  await writeFile(target, JSON.stringify(member, null, 2), {
    flag: 'wx',
    mode: 0o600,
  });
  // On Windows the verified private parent supplies the ACL from creation.
  if (process.platform === 'win32') privatePath(target, true);
  await validateMemberCredentialsPath(target);
}

export async function inviteDesktopMember(cfg, access, report) {
  await validateMemberCredentialsPath(cfg.desktopMemberFile);
  const credentials = JSON.parse(
    await readFile(cfg.adminCredentialsFile, 'utf8'),
  );
  const options = { caFile: cfg.caFile, dnsMappings: cfg.dnsMappings, report };
  const admin = await httpsTransport(accessOrigin(access.adminOrigin), options);
  const portal = await httpsTransport(
    accessOrigin(access.portalOrigin),
    options,
  );
  try {
    await publicLogin(admin, credentials);
    const providers = (
      await portal.request('/api/iam/v1/login/providers')
    ).items.filter(
      (p) => p.connectorType === 'team-account' && p.primaryStep === 'form',
    );
    assert.equal(providers.length, 1);
    const roleId = desktopMemberRole(
      await admin.request('/api/org/v1/admin/roles'),
    );
    let existing;
    try {
      existing = JSON.parse(await readFile(cfg.desktopMemberFile, 'utf8'));
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    if (existing) {
      assert.equal(existing.portalUrl, accessOrigin(access.portalOrigin));
      assert.equal(existing.providerId, providers[0].id);
      await ensureDesktopMemberRole(admin, existing.accountId, roleId);
      return {
        accountId: existing.accountId,
        registered: false,
        memberRoleBound: true,
      };
    }
    const member = {
      username: 'cloud-desk-' + randomBytes(6).toString('hex'),
      displayName: 'Cloud desktop test',
      password: randomBytes(24).toString('base64url'),
      providerId: providers[0].id,
      portalUrl: accessOrigin(access.portalOrigin),
    };
    const invitation = await admin.request('/api/org/v1/admin/invitations', {
      memberType: 'member',
      maxUses: 1,
      roleIds: [roleId],
      groupIds: [],
      providerIds: [member.providerId],
      displayName: member.displayName,
    });
    assert.equal(
      (
        await portal.request(
          '/api/org/v1/invitations/preview/' + invitation.code,
        )
      ).valid,
      true,
    );
    member.accountId = await publicLogin(portal, member, {
      invitationCode: invitation.code,
    });
    // Explicit private handoff enables resuming without creating extra accounts.
    await writeMemberCredentials(cfg.desktopMemberFile, member);
    return { accountId: member.accountId, registered: true };
  } finally {
    admin.close();
    portal.close();
  }
}

export async function openHttpsCloud({ cfg, access, report, stage }) {
  const credentials = JSON.parse(
    await readFile(cfg.httpsCloud.credentialsFile, 'utf8'),
  );
  const options = { caFile: cfg.caFile, dnsMappings: cfg.dnsMappings, report };
  const portal = await httpsTransport(
    accessOrigin(access.portalOrigin),
    options,
  );
  let instance;
  try {
    stage('cloud-public-iam-login');
    const accountId = await publicLogin(portal, credentials);
    stage('cloud-existing-instance-open');
    const opened = await portal.request(
      '/api/workspace/v1/me/instance/open',
      {},
    );
    const id = opened.instance?.id ?? opened.id;
    assert.equal(id, cfg.httpsCloud.instanceId);
    const value = await poll(
      async () => {
        const row = await portal.request('/api/workspace/v1/instances/' + id);
        return row.ready && ['running', 'pending_restart'].includes(row.phase)
          ? row
          : null;
      },
      'cloud_not_ready',
      180000,
    );
    assert.equal(value.ownerId, accountId);
    const entry = new URL(
      `${access.instances.scheme}://${id}.${access.instances.parentDomain}:${access.instances.port}/`,
    );
    instance = await httpsTransport(entry.origin, options);
    stage('cloud-instance-entry');
    const door = await portal.inspect('/open/' + id);
    assert.equal(door.status, 302);
    instance.jar.absorbMatchingDomainCookies(portal.jar, entry.host);
    let location = door.location;
    let authenticated = false;
    for (let i = 0; i < 8; i++) {
      const url = new URL(location, entry);
      assert.equal(url.origin, entry.origin);
      assert.equal(url.username + url.password + url.hash, '');
      const response = await poll(
        async () => {
          const current = await instance.inspect(url.pathname + url.search);
          return current.status === 503 ? null : current;
        },
        'cloud_instance_entry_unavailable',
        45000,
      );
      if ([302, 303].includes(response.status)) {
        location = response.location;
        continue;
      }
      assert.equal(
        response.status,
        200,
        'native instance entry must authenticate',
      );
      authenticated = true;
      break;
    }
    assert.equal(
      authenticated,
      true,
      'redirect limit must not establish authentication',
    );
    stage('cloud-managed-node');
    const plugin = assessManagedPlugin(
      await instance.inspect('/_auriga/health'),
      await instance.inspect('/auriga/bootstrap'),
    );
    report.cloud = {
      instanceId: id,
      ownerId: accountId,
      ready: true,
      memberAuthenticated: true,
      browserAuthenticated: false,
      authentication: 'public-iam-cookie-redirect',
      httpsVerified: true,
      ...plugin,
    };
    return {
      instance: value,
      request: instance.request,
      inspect: instance.inspect,
      member: credentials,
      browser: {
        close: async () => {
          instance.close();
          portal.close();
        },
      },
    };
  } catch (error) {
    instance?.close();
    portal.close();
    throw error;
  }
}
