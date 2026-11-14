import { randomUUID, randomBytes } from 'node:crypto';
import { Broker } from '../broker.mjs';
export const OWNER = randomUUID(),
  OTHER = randomUUID();
export function fixture(overrides = {}) {
  const identity = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    deviceId: randomUUID(),
    providerId: randomUUID(),
  };
  const tokens = {
    access_token: randomUUID(),
    refresh_token: randomUUID(),
    token_type: 'Bearer',
    scope: 'auriga.connect',
  };
  identity.uacSessionBinding = {
    sourceSessionId: randomUUID(),
    providerId: identity.providerId,
    authGeneration: 1,
  };
  const revoked = [],
    opened = [],
    saved = [],
    logs = [];
  const claims = {
    tid: identity.tenantId,
    sub: identity.memberId,
    did: identity.deviceId,
    exp: Math.floor(Date.now() / 1000) + 600,
    uacSessionBinding: identity.uacSessionBinding,
  };
  let stored;
  const platform = {
    config: { platformOrigin: 'https://example.com' },
    begin: async () => ({
      device_code: randomUUID(),
      user_code: [...randomBytes(8)]
        .map((n) => 'BCDFGHJKLMNPQRSTVWXZ'[n % 20])
        .join('')
        .replace(/^(.{4})/, '$1-'),
      verification_uri: 'https://example.com/device',
      expires_in: 300,
      interval: 5,
    }),
    poll: async () => tokens,
    verifyDevice: async () => claims,
    profile: async () => ({ identity }),
    refresh: async () => tokens,
    revoke: async (token) => {
      revoked.push(token);
    },
    enterprise: async () => ({
      binding: {
        tenantId: identity.tenantId,
        memberId: identity.memberId,
        deviceId: identity.deviceId,
        ...identity.uacSessionBinding,
        expiresAt: new Date(Date.now() + 600000).toISOString(),
      },
      credentialVersion: 1,
      empNo: '001',
      token: randomUUID(),
      updatedAt: new Date().toISOString(),
    }),
    audience: async (token) => token,
    ...overrides,
  };
  const vault = {
    load: async () => stored,
    save: async (record) => {
      stored = structuredClone(record);
      saved.push(stored);
    },
  };
  const broker = new Broker({
    platform,
    vault,
    openBrowser: async (url) => {
      opened.push(url);
    },
    log: (event) => logs.push(event),
    wait: async () => {},
  });
  return {
    broker,
    platform,
    identity,
    tokens,
    claims,
    revoked,
    opened,
    saved,
    logs,
    vault,
  };
}
export const command = (broker, action) => ({
  protocolVersion: 1,
  requestId: randomUUID(),
  shellEpoch: broker.epoch,
  expectedRevision: broker.revision,
  action,
});
export async function login(f) {
  await f.broker.command(command(f.broker, 'open_login'), OWNER);
  await Promise.all([...f.broker.inflight]);
}
