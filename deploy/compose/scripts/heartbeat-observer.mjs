#!/usr/bin/env node
import { appendFileSync, writeFileSync } from 'node:fs';
import { createHeartbeatObserver } from '../../../e2e/lib/heartbeat-observer.mjs';

// Run only on the local test host. Configure the generated edge workspace
// upstream to this port. No request is synthesized and IAM/org are unchanged.
const file = process.env.RAW_HEARTBEAT_FILE;
if (!file) throw new Error('RAW_HEARTBEAT_FILE required');
writeFileSync(file, '', { mode: 0o600, flag: 'wx' });
let records = 0;
const server = createHeartbeatObserver({
  upstream: 'http://127.0.0.1:8081',
  onHeartbeat(entry) {
    if (++records > 10000) throw new Error('capture limit');
    appendFileSync(file, JSON.stringify(entry) + '\n', { mode: 0o600 });
  },
  onCaptureError() {
    process.stderr.write('heartbeat capture incomplete\n');
  },
});
server.listen(8082, '0.0.0.0', () =>
  console.log('local heartbeat observer ready'),
);
for (const signal of ['SIGINT', 'SIGTERM'])
  process.on(signal, () => server.close());
