import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import path from 'node:path';
import { configuration } from './runner.mjs';

function fixture(litellm) {
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
    { ports, roles, database: 'fixture', pgPort: 15432, litellm },
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
    teamKeyFile: '/private/team.key',
  };
  const cfg = fixture(connection);
  assert.equal(cfg.edge.litellmOrigin, connection.origin);
  assert.equal(cfg.ai.origin, connection.origin);
  assert.equal(cfg.ai.keyFile, connection.teamKeyFile);
  assert.equal(cfg.ai.masterKeyFile, undefined);
  assert.equal(cfg.ai.store, undefined);
  assert.equal(
    cfg.ai.iam.ServiceTokenFile,
    cfg.networkOptions.iam.ServiceTokenFile,
  );
  assert.equal(cfg.networkOptions.iam.ServerName, 'platform.beta.internal');
});
