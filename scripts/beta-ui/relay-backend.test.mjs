import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import path from 'node:path';
import { configuration } from './runner.mjs';

function fixture(relay) {
  const ports = Object.fromEntries(
    [
      'portal',
      'admin',
      'instanceControl',
      'platform',
      'grpc',
      'ai',
      'networkSubmit',
      'networkBroker',
      'networkWebsocket',
    ].map((name, index) => [name, 18000 + index]),
  );
  const roles = {
    iam: 'fixture_iam',
    org: 'fixture_org',
    edgeKv: 'fixture_edge',
  };
  const secrets = Object.fromEntries(
    ['iam', 'org', 'edgeSecret', 'gatewaySecret', 'encryptionKey'].map(
      (name) => [name, randomBytes(32).toString('hex')],
    ),
  );
  return configuration(
    path.resolve('.private-fixture'),
    { ports, roles, database: 'fixture', pgPort: 15432, relay },
    secrets,
  );
}

test('Beta without LiteLLM has no old relay runtime or admin upstream', () => {
  const cfg = fixture();
  assert.equal(cfg.ai, undefined);
  assert.equal(cfg.edge.upstreams['ai-admin'], undefined);
  assert.equal(cfg.edge.upstreams['ai-device'], undefined);
});

test('Beta uses the service key file and retains independent network control RPCs', () => {
  const connection = {
    origin: 'http://127.0.0.1:14000',
    memberPasswordFile: '/private/member.key',
    adminPasswordFile: '/private/admin.key',
  };
  const cfg = fixture(connection);
  assert.deepEqual(cfg.edge.relay, {
    origin: connection.origin,
    adminPasswordFile: connection.adminPasswordFile,
  });
  assert.equal(cfg.ai.memberPasswordFile, connection.memberPasswordFile);
  assert.equal(cfg.ai.adminPasswordFile, undefined);
  assert.equal(cfg.ai.origin, connection.origin);
  assert.equal(
    cfg.ai.iam.ServiceTokenFile,
    cfg.networkOptions.iam.ServiceTokenFile,
  );
  assert.equal(cfg.networkOptions.iam.ServerName, 'platform.beta.internal');
});
