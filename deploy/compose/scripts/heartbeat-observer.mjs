#!/usr/bin/env node
import { appendFileSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHeartbeatObserver } from '../../../e2e/lib/heartbeat-observer.mjs';

// Run only on the local test host. Explicit bootstrap observer mode sends all
// three APIs to this authenticated HTTPS origin. No request is synthesized.
const configFile =
  process.env.HEARTBEAT_OBSERVER_CONFIG ||
  fileURLToPath(new URL('../var/heartbeat-observer.json', import.meta.url));
const config = JSON.parse(readFileSync(configFile, 'utf8'));
if (!config.enabled)
  throw new Error('bootstrap heartbeat observer mode required');
const read = (name) => readFileSync(join(dirname(configFile), config[name]));
const file = process.env.RAW_HEARTBEAT_FILE;
if (!file) throw new Error('RAW_HEARTBEAT_FILE required');
writeFileSync(file, '', { mode: 0o600, flag: 'wx' });
let records = 0;
const server = createHeartbeatObserver({
  upstream: config.upstream,
  tls: {
    ca: read('caFile'),
    cert: read('certFile'),
    key: read('keyFile'),
    clientCert: read('clientCertFile'),
    clientKey: read('clientKeyFile'),
    serverName: config.serverName,
    clientDNSName: config.clientDNSName,
  },
  onHeartbeat(entry) {
    if (++records > 10000) throw new Error('capture limit');
    appendFileSync(file, JSON.stringify(entry) + '\n', { mode: 0o600 });
  },
  onCaptureError() {
    process.stderr.write('heartbeat capture incomplete\n');
  },
});
server.listen(config.listenPort, '0.0.0.0', () =>
  console.log('local heartbeat observer ready'),
);
for (const signal of ['SIGINT', 'SIGTERM'])
  process.on(signal, () => server.close());
