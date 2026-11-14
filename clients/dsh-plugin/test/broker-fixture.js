import { readFileSync, readdirSync } from 'node:fs';
import Ajv from '../node_modules/ajv/dist/2020.js';
import addFormats from '../../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { randomUUID, randomBytes } from 'node:crypto';
const root = new URL('../../../contracts/', import.meta.url);
const read = (path) => JSON.parse(readFileSync(new URL(path, root), 'utf8'));
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv, { keywords: true });
for (const path of [
  ...readdirSync(new URL('network/', root))
    .filter((p) => p.endsWith('.schema.json'))
    .map((p) => 'network/' + p),
  'plugin/team-mcp.schema.json',
  'plugin/team-transport.schema.json',
  'plugin/shell-auth.schema.json',
  'plugin/shell-auth-control.schema.json',
  'plugin/shell-business.schema.json',
  'auth/uac-login-info.schema.json',
  'auth/uac-session-binding.schema.json',
  'auth/uac-control.schema.json',
])
  ajv.addSchema(read(path), new URL(path, root).href);
export function validate(name, value) {
  for (const path of [
    'plugin/shell-auth.schema.json',
    'plugin/shell-auth-control.schema.json',
    'plugin/shell-business.schema.json',
  ]) {
    const check = ajv.getSchema(new URL(path, root).href + '#/$defs/' + name);
    if (check) {
      if (!check(value))
        throw Object.assign(new Error('contract rejected ' + name), {
          errors: check.errors,
        });
      return;
    }
  }
  throw new Error('unknown schema');
}
export const operations = read(
  'plugin/shell-business-operations.json',
).operations;
export function brokerFixture() {
  const epoch = randomUUID(),
    identity = {
      tenantId: randomUUID(),
      memberId: randomUUID(),
      deviceId: randomUUID(),
      providerId: randomUUID(),
    };
  const sourceSessionId = randomUUID(),
    ownerId = randomUUID();
  identity.uacSessionBinding = {
    sourceSessionId,
    authGeneration: 1,
    providerId: identity.providerId,
  };
  let revision = 1,
    state = 'authenticated',
    sub,
    changed,
    token = randomBytes(32).toString('base64url'),
    version = 1;
  const contexts = new Map(),
    calls = [];
  const snapshot = () => ({
    protocolVersion: 1,
    shellEpoch: epoch,
    revision,
    state,
    expiresAt: new Date(Date.now() + 29000).toISOString(),
    ...(state === 'authenticated' ? { identity } : {}),
  });
  const event = (requestId) => ({
    protocolVersion: 1,
    requestId,
    type: 'snapshot',
    snapshot: snapshot(),
  });
  const ipc = {
    async call(message) {
      calls.push(message);
      if (!message.path && !message.call) {
        if (message.action === 'logout') {
          revision++;
          state = 'signed_out';
        }
        return event(message.requestId);
      }
      const body = message.body;
      const result = body
        ? {
            protocolVersion: 1,
            requestId: body.requestId,
            shellEpoch: epoch,
            revision,
          }
        : {};
      if (message.path === '/host/contexts') {
        validate('ShellAuthHostContextCommand', body);
        if (body.action === 'register') {
          const context = {
            executionContextId: randomUUID(),
            ownerId,
            kind: body.context.kind,
            hostSessionId: body.context.hostSessionId,
            expiresAt: body.context.expiresAt,
          };
          contexts.set(context.executionContextId, context);
          return { body: { ...result, action: 'register', context } };
        }
        contexts.delete(body.executionContextId);
        return {
          body: {
            ...result,
            action: 'revoke',
            revokedExecutionContextId: body.executionContextId,
          },
        };
      }
      if (message.path === '/host/credentials') {
        validate('ShellAuthHostCredentialsRequest', body);
        if (!contexts.has(body.executionContextId))
          throw new Error('missing context');
        return {
          body: {
            ...result,
            executionContextId: body.executionContextId,
            purpose: body.purpose,
            credentials: {
              binding: {
                tenantId: identity.tenantId,
                memberId: identity.memberId,
                deviceId: identity.deviceId,
                providerId: identity.providerId,
                sourceSessionId,
                authGeneration: 1,
                expiresAt: new Date(Date.now() + 60000).toISOString(),
              },
              credentialVersion: version,
              empNo: '00001',
              token,
              updatedAt: new Date().toISOString(),
            },
          },
        };
      }
      if (message.call) {
        validate('ShellAuthBusinessCall', message);
        return {
          requestId: message.requestId,
          operation: message.call.operation,
          output: {
            statusCode: 200,
            headers: { 'content-type': 'application/json' },
            body: { items: [] },
          },
        };
      }
      throw new Error('unknown transport message');
    },
    async subscribe(command, callback) {
      sub = command.requestId;
      changed = callback;
      callback(event(sub));
    },
    close() {},
  };
  return {
    ipc,
    validate,
    operations,
    calls,
    identity,
    contexts,
    snapshot,
    token: () => token,
    rotate() {
      token = randomBytes(32).toString('base64url');
      version++;
    },
    refresh() {
      changed(event(sub));
    },
    logout() {
      state = 'signed_out';
      revision++;
      changed(event(sub));
    },
  };
}
