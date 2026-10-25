import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import test from 'node:test';

// Reuse the existing lint toolchain's YAML parser; no dependency installation.
const require = createRequire(import.meta.url);
const { load } = createRequire(require.resolve('eslint'))('js-yaml');
const { services } = load(
  readFileSync(new URL('../docker-compose.yml', import.meta.url), 'utf8'),
);

test('network Broker exposes no host ports and preserves the separate JetStream service', () => {
  const broker = services['network-nats'];
  assert.equal(broker.image, 'nats:2.12.6-alpine');
  assert.equal(broker.ports, undefined);
  assert.equal(broker.network_mode, undefined);
  assert.deepEqual(broker.command, ['-c', '/etc/auriga/network/nats.conf']);
  assert.ok(
    services.nats.volumes.includes('./nats/nats.conf:/etc/nats/nats.conf:ro'),
  );
});

test('gateway and Broker read the same immutable installation; missing directories fail closed', () => {
  const gateway = services['network-gateway'];
  const brokerMount = services['network-nats'].volumes[0];
  assert.deepEqual(gateway.volumes[0], brokerMount);
  assert.equal(brokerMount.read_only, true);
  assert.equal(brokerMount.bind.create_host_path, false);
  assert.equal(gateway.volumes[1].read_only, true);
  assert.equal(gateway.volumes[1].bind.create_host_path, false);
  assert.equal(gateway.volumes[1].target, '/run/auriga-network-tokens');
  assert.equal(gateway.ports, undefined);
  assert.equal(gateway.privileged, undefined);
  assert.deepEqual(gateway.command, [
    '--network-config',
    '/etc/auriga/network/gateway.json',
  ]);
  assert.equal(gateway.depends_on['network-nats'].condition, 'service_healthy');
  assert.equal(gateway.depends_on.platform.condition, 'service_healthy');
});

test('edge forwards the exact network path over verified WSS', () => {
  const env = services.edge.environment;
  assert.equal(env.AURIGA_EDGE_NETWORK_WSS_ENABLED, 'true');
  assert.equal(env.AURIGA_EDGE_NETWORK_WSS_PATH, '/net/v1/ws');
  assert.equal(
    env.AURIGA_EDGE_NETWORK_WSS_UPSTREAM,
    'https://network-nats:8443/',
  );
  assert.equal(env.SSL_CERT_FILE, '/etc/auriga/pki/ca.crt');
  assert.equal(
    services.edge.depends_on['network-nats'].condition,
    'service_healthy',
  );
});

test('IAM delegates the gateway tenant context with an explicit network audience', () => {
  const env = services.platform.environment;
  assert.equal(env.AURIGA_IAM_NETWORK_SERVER_AUDIENCES, 'auriga-network-1');
  assert.deepEqual(env.AURIGA_GRPC_SERVER_TENANT_DELEGATES.split(','), [
    'svc:edge',
    'svc:ai-gateway',
  ]);
  assert.ok(env.AURIGA_GRPC_SERVER_TLS_TRUST_CERT_COLLECTION);
});
