import { randomUUID } from 'node:crypto';
import { Broker } from '../broker.mjs';
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
  const revoked = [],
    opened = [],
    saved = [],
    logs = [];
  const claims = {
    tid: identity.tenantId,
    sub: identity.memberId,
    did: identity.deviceId,
    exp: Math.floor(Date.now() / 1000) + 600,
  };
  let stored;
  const platform = {
    config: { platformOrigin: 'https://example.com' },
    begin: async () => ({
      device_code: randomUUID(),
      user_code: 'TEST-CODE',
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
  await f.broker.command(command(f.broker, 'open_login'), 'one');
  await Promise.all([...f.broker.inflight]);
}
