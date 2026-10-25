import { findNatsServer } from './nats-server.js';

export const natsBinary = findNatsServer();
/** node:test `skip` option: tests that need a real broker are reported as skipped, not passed. */
export const skipNoServer = natsBinary
  ? false
  : 'nats-server binary not found (set AURIGA_NATS_SERVER or populate ~/.cache/auriga)';
export const text = (s) => new TextEncoder().encode(s);
export const str = (b) => Buffer.from(b).toString('utf8');
