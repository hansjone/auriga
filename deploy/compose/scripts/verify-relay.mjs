// Run against a disposable deployment prepared by scripts/prepare.sh.
// Credentials are read in-process from that deployment and never printed.
import { readFileSync } from 'node:fs';
import { createHash, createHmac, randomBytes } from 'node:crypto';
import http from 'node:http';
import https from 'node:https';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const options = Object.fromEntries(
  process.argv
    .slice(2)
    .reduce(
      (pairs, value, i, args) =>
        i % 2 ? pairs : [...pairs, [value, args[i + 1]]],
      [],
    ),
);
const materials = path.resolve(options['--materials']);
const access = JSON.parse(readFileSync(path.join(materials, 'access.json')));
const enrollment = JSON.parse(
  readFileSync(path.join(materials, 'enrollment/emergency-enrollment.json')),
);
const master = readFileSync(
  path.join(materials, 'litellm/master'),
  'utf8',
).trim();
const ca = options['--ca'] ? readFileSync(options['--ca']) : undefined;
const origins = Object.fromEntries(
  ['portal', 'admin'].map((role) => [
    role,
    new URL(
      `${access[`${role}Origin`].scheme}://${access[`${role}Origin`].host}:${access[`${role}Origin`].port}`,
    ),
  ]),
);
const consumed = new Map();
function check(ok, label) {
  if (!ok) throw new Error(label);
  console.log(`PASS ${label}`);
}
function client(role) {
  const cookies = new Map();
  return { role, cookies, csrf: undefined };
}
function request(
  client,
  route,
  { method = 'GET', body, headers = {}, browser = true, form = false } = {},
) {
  const origin = origins[client.role];
  const bytes =
    body === undefined
      ? undefined
      : Buffer.from(
          form ? new URLSearchParams(body).toString() : JSON.stringify(body),
        );
  const wire = { ...headers };
  if (browser) {
    wire.Origin = origin.origin;
    if (client.cookies.size)
      wire.Cookie = [...client.cookies].map(([k, v]) => `${k}=${v}`).join('; ');
  }
  if (bytes) {
    wire['Content-Type'] = form
      ? 'application/x-www-form-urlencoded'
      : 'application/json';
    wire['Content-Length'] = bytes.length;
  }
  return new Promise((resolve, reject) => {
    const req = (origin.protocol === 'https:' ? https : http).request(
      new URL(route, origin),
      { method, headers: wire, ca, timeout: 20000 },
      (response) => {
        for (const cookie of response.headers['set-cookie'] || []) {
          const pair = cookie.split(';', 1)[0];
          const i = pair.indexOf('=');
          client.cookies.set(pair.slice(0, i), pair.slice(i + 1));
        }
        const chunks = [];
        response.on('data', (chunk) => chunks.push(chunk));
        response.on('end', () => {
          const text = Buffer.concat(chunks).toString();
          let json;
          try {
            json = JSON.parse(text);
          } catch {
            /* HTML/SSE is validated by the caller. */
          }
          resolve({
            status: response.statusCode,
            headers: response.headers,
            text,
            json,
          });
        });
      },
    );
    req.on('timeout', () => req.destroy(new Error('request timeout')));
    req.on('error', () => reject(new Error('deployment request failed')));
    req.end(bytes);
  });
}
async function expectRequest(c, route, options, expected) {
  const result = await request(c, route, options);
  if (!expected.includes(result.status)) {
    const code = result.json?.code;
    const detail =
      typeof code === 'string' &&
      /^(common|org|iam|ai|edge)\.[a-z_]+$/.test(code)
        ? ` ${code}`
        : '';
    throw new Error(`${route}: HTTP ${result.status}${detail}`);
  }
  return result;
}
async function otp(secret) {
  let counter = Math.floor(Date.now() / 30000);
  if (counter <= (consumed.get(secret) ?? -1)) {
    await delay(30000 - (Date.now() % 30000) + 100);
    counter = Math.floor(Date.now() / 30000);
  }
  consumed.set(secret, counter);
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const char of secret.replace(/=+$/, '').toUpperCase())
    bits += alphabet.indexOf(char).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map((byte) => parseInt(byte, 2)));
  const count = Buffer.alloc(8);
  count.writeBigUInt64BE(BigInt(counter));
  const digest = createHmac('sha1', key).update(count).digest();
  const offset = digest.at(-1) & 15;
  return String((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(
    6,
    '0',
  );
}
async function login(c, provider, credentials, extra = {}) {
  const begin = await expectRequest(
    c,
    '/api/iam/v1/login/transactions',
    { method: 'POST', body: { providerId: provider, returnTo: '/', ...extra } },
    [200, 201],
  );
  let transaction = begin.json;
  for (let n = 0; transaction.status !== 'completed' && n < 5; n++) {
    const id = transaction.step?.formId;
    let values;
    if (id === 'emergency')
      values = {
        username: credentials.username,
        password: credentials.password,
        otp: await otp(credentials.totp),
      };
    else if (id === 'register' || id === 'password')
      values = {
        username: credentials.username,
        password: credentials.password,
        displayName: 'Relay test member',
      };
    else if (id === 'first_totp') {
      const label = transaction.step.fields.find((f) => f.name === 'enrollment')
        ?.label?.en;
      credentials.totp = label?.match(/[A-Z2-7]{16,}/)?.[0];
      if (!credentials.totp) throw new Error('first TOTP enrollment missing');
      values = { otp: await otp(credentials.totp) };
    } else if (id === 'totp') values = { code: await otp(credentials.totp) };
    else throw new Error('unsupported login step');
    transaction = (
      await expectRequest(
        c,
        `/api/iam/v1/login/transactions/${transaction.id}/advance`,
        { method: 'POST', body: { type: 'form', formId: id, values } },
        [200],
      )
    ).json;
  }
  if (transaction.status !== 'completed') throw new Error('login incomplete');
  return begin.json;
}
async function write(c, route, body) {
  if (!c.csrf) {
    if (access.mode === 'ip') {
      c.csrf = randomBytes(32).toString('base64url');
      await expectRequest(
        c,
        '/api/iam/v1/admin/session/csrf-tokens',
        {
          method: 'POST',
          body: { token: c.csrf },
          headers: { 'X-Auriga-CSRF-Bootstrap': 'admin-session-v1' },
        },
        [200, 201],
      );
    } else
      c.csrf = [...c.cookies].find(([name]) =>
        name.endsWith('auriga_csrf'),
      )?.[1];
  }
  return expectRequest(
    c,
    route,
    { method: 'POST', body, headers: { 'X-Auriga-CSRF': c.csrf } },
    [200, 201],
  );
}
const admin = client('admin');
const native = client('portal');
const llm = (route, body) =>
  expectRequest(
    admin,
    '/ai' + route,
    {
      method: body === undefined ? 'GET' : 'POST',
      body,
      headers: { Authorization: `Bearer ${master}` },
    },
    [200, 201],
  );
try {
  check(
    [401, 403].includes((await request(client('admin'), '/ai/ui/')).status),
    'anonymous UI rejected',
  );
  await login(admin, enrollment.providerId, {
    username: enrollment.username,
    password: enrollment.password,
    totp: enrollment.totpSecret,
  });
  check(
    (await llm('/health/liveliness')).status === 200,
    'LiteLLM liveliness through admin edge',
  );
  let page = await expectRequest(admin, '/ai/ui/', {}, [200, 307, 308]);
  for (let n = 0; page.status !== 200 && n < 5; n++) {
    const target = new URL(page.headers.location, origins.admin);
    if (target.origin !== origins.admin.origin)
      throw new Error('UI redirect left admin origin');
    page = await expectRequest(
      admin,
      target.pathname + target.search,
      {},
      [200, 307, 308],
    );
  }
  check(
    page.status === 200 && page.text.toLowerCase().includes('html'),
    'administrator UI admitted',
  );
  const assets = [
    ...page.text.matchAll(/(?:src|href)="([^"<>]+\.(?:js|css))"/g),
  ].map((match) => match[1]);
  check(
    assets.length > 0 && assets.every((asset) => asset.startsWith('/ai/')),
    'UI assets retain admin prefix',
  );
  for (const asset of assets.slice(0, 3)) {
    const result = await expectRequest(admin, asset, {}, [200]);
    check(
      !result.headers['content-type']?.includes('text/html'),
      'administrator UI asset served',
    );
    check(
      [401, 403].includes((await request(client('admin'), asset)).status),
      'anonymous UI asset rejected',
    );
  }
  const providers = (
    await expectRequest(native, '/api/iam/v1/login/providers', {}, [200])
  ).json.items;
  const provider = providers.find(
    (p) => p.connectorType === 'team-account',
  )?.id;
  if (!provider) throw new Error('team provider missing');
  const role = (
    await write(admin, '/api/org/v1/admin/roles', {
      key: 'relay_' + randomBytes(5).toString('hex'),
      name: { en: 'Relay test' },
      permissions: ['auriga.connect.models.invoke'],
    })
  ).json;
  const invitation = (
    await write(admin, '/api/org/v1/admin/invitations', {
      memberType: 'member',
      roleIds: [role.id],
      providerIds: [provider],
      maxUses: 1,
    })
  ).json;
  const member = {
    username: 'relay-' + randomBytes(6).toString('hex'),
    password: randomBytes(32).toString('base64url'),
  };
  await login(client('portal'), provider, member, {
    invitationCode: invitation.code,
  });
  const ordinaryAdmin = client('admin');
  await login(ordinaryAdmin, provider, member);
  check(
    [401, 403].includes((await request(ordinaryAdmin, '/ai/ui/')).status),
    'authenticated nonadministrator UI rejected',
  );
  const verifier = randomBytes(48).toString('base64url');
  const nativeBegin = await login(native, provider, member, {
    nativeConnect: {
      clientId: 'auriga-dsh',
      codeChallenge: createHash('sha256').update(verifier).digest('base64url'),
      codeChallengeMethod: 'S256',
      deviceName: 'Relay acceptance',
    },
  });
  const device = (
    await expectRequest(
      native,
      '/oauth2/token',
      {
        method: 'POST',
        form: true,
        body: {
          grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
          client_id: 'auriga-dsh',
          device_code: nativeBegin.nativeDevice.deviceCode,
          code_verifier: verifier,
        },
      },
      [200],
    )
  ).json;
  const ai = (
    await expectRequest(
      native,
      '/oauth2/token',
      {
        method: 'POST',
        form: true,
        body: {
          grant_type: 'refresh_token',
          client_id: 'auriga-dsh',
          refresh_token: device.refresh_token,
          resource: 'urn:auriga:ai-gateway',
        },
      },
      [200],
    )
  ).json;
  check(Boolean(ai.access_token), 'IAM issued member AI short ticket');
  await llm('/model/new', {
    model_name: 'relay-local',
    litellm_params: {
      model: 'openai/gpt-4o-mini',
      api_base: 'http://fake-upstream:4001/v1',
      api_key: 'unused-local-fixture',
    },
    model_info: { input_cost_per_token: 0.001, output_cost_per_token: 0.001 },
  });
  const key = (await llm('/key/generate', { models: ['relay-local'] })).json
    .key;
  const outside = client('portal');
  const call = (token, stream = false) =>
    request(outside, '/ai/v1/chat/completions', {
      browser: false,
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      body: {
        model: 'relay-local',
        messages: [{ role: 'user', content: 'local fixture only' }],
        stream,
        user: 'forged',
        metadata: { tenant_id: 'forged' },
      },
    });
  check(
    (
      await request(outside, '/ai/v1/models', {
        browser: false,
        headers: { Authorization: `Bearer ${key}` },
      })
    ).status === 200,
    'external models through edge',
  );
  check((await call(key)).status === 200, 'external JSON through edge');
  const stream = await call(key, true);
  check(
    stream.status === 200 &&
      stream.headers['content-type']?.includes('text/event-stream') &&
      stream.text.includes('local fixture') &&
      stream.text.includes('data: [DONE]'),
    'external SSE through edge',
  );
  for (const token of [undefined, 'sk-' + randomBytes(32).toString('hex')]) {
    check(
      [401, 403].includes(
        (
          await request(outside, '/ai/v1/models', {
            browser: false,
            headers: token ? { Authorization: `Bearer ${token}` } : {},
          })
        ).status,
      ),
      'missing or wrong external key rejected',
    );
    check(
      [401, 403].includes((await call(token)).status),
      'missing or wrong chat key rejected',
    );
  }
  for (const route of [
    '/ai/key/generate',
    '/ai/v1/key/generate',
    '/key/generate',
  ])
    check(
      [401, 403, 404, 405].includes(
        (
          await request(outside, route, {
            browser: false,
            method: 'POST',
            body: {},
            headers: { Authorization: `Bearer ${key}` },
          })
        ).status,
      ),
      'external key cannot manage LiteLLM',
    );
  for (const route of ['/ai/key/generate', '/v1/key/generate'])
    check(
      [401, 403, 404, 405].includes(
        (
          await request(client('admin'), route, {
            browser: false,
            method: 'POST',
            body: {},
            headers: { Authorization: `Bearer ${key}` },
          })
        ).status,
      ),
      'external key cannot manage on admin origin',
    );
  await llm('/key/delete', { keys: [key] });
  check(
    [401, 403].includes((await call(key)).status),
    'revoked key rejected through edge',
  );
  const budget = (
    await llm('/key/generate', {
      models: ['relay-local'],
      max_budget: 0.000001,
    })
  ).json.key;
  check(
    (await call(budget)).status === 200,
    'budget initial call through edge',
  );
  let denied = false;
  const budgetDeadline = Date.now() + 30000;
  while (Date.now() < budgetDeadline) {
    const result = await call(budget);
    if (
      result.status === 422 &&
      result.json?.error?.type === 'budget_exceeded'
    ) {
      denied = true;
      break;
    }
    await delay(250);
  }
  check(denied, 'budget exceeded through edge');
  check(
    (await call(ai.access_token)).status === 200,
    'member ticket gateway LiteLLM upstream',
  );
  const claims = JSON.parse(
    Buffer.from(ai.access_token.split('.')[1], 'base64url'),
  );
  let attributed = false;
  const spendDeadline = Date.now() + 30000;
  while (Date.now() < spendDeadline) {
    const response = (
      await llm(
        '/spend/logs?user=' + encodeURIComponent(claims.tid + ':' + claims.sub),
      )
    ).json;
    const rows = Array.isArray(response) ? response : response.data || [];
    if (rows.some((row) => row.end_user === claims.tid + ':' + claims.sub)) {
      attributed = true;
      break;
    }
    await delay(250);
  }
  check(attributed, 'member usage persisted with trusted identity');
} catch (error) {
  console.error(
    `FAIL ${error instanceof Error ? error.message : 'relay acceptance'}`,
  );
  process.exitCode = 1;
}
