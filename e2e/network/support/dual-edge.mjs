import assert from 'node:assert/strict';
import { dirname, join } from 'node:path';

export function dualEdgeConfig({ accessFile, backend, gateway, directory }) {
  const upstream = new URL(gateway.https);
  assert.equal(upstream.protocol, 'https:');
  assert.equal(upstream.hostname, '127.0.0.1');
  assert.equal(upstream.origin, gateway.https);
  const privateDirectory = dirname(gateway.ca);
  return {
    accessConfigFile: accessFile,
    identityKeysFile: join(directory, 'edge-identity.json'),
    upstreams: {
      iam: backend,
      org: backend,
      workspace: backend,
      'network-submit': gateway.https,
    },
    iamHop: { allowLoopbackHttp: true },
    networkSubmitHop: {
      certFile: join(privateDirectory, 'broker-cert.pem'),
      keyFile: join(privateDirectory, 'broker-key.pem'),
      caFile: gateway.ca,
      serverName: 'localhost',
    },
    tls: {
      mode: 'files',
      certFile: join(privateDirectory, 'broker-cert.pem'),
      keyFile: join(privateDirectory, 'broker-key.pem'),
    },
  };
}
