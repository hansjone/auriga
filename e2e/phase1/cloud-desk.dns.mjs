import assert from 'node:assert/strict';
import dns from 'node:dns';
import { isIP } from 'node:net';
import { syncBuiltinESMExports } from 'node:module';

export function createTestLookup(mappings, fallback) {
  for (const [name, address] of Object.entries(mappings)) {
    assert.match(name, /^(?:[a-z0-9-]+\.)+[a-z0-9-]+$/);
    assert.equal(isIP(address), 4);
    const [a, b] = address.split('.').map(Number);
    assert.ok(
      a === 127 ||
        a === 10 ||
        (a === 192 && b === 168) ||
        (a === 172 && b >= 16 && b <= 31),
    );
  }
  return (hostname, options, callback) => {
    if (typeof options === 'function') {
      callback = options;
      options = {};
    }
    const address = mappings[hostname];
    if (!address) return fallback(hostname, options, callback);
    globalThis.queueMicrotask(() =>
      options?.all
        ? callback(null, [{ address, family: 4 }])
        : callback(null, address, 4),
    );
  };
}

// Only explicitly launched test children set this variable. URL, Host and TLS
// SNI are untouched; certificate validation remains enabled with the test CA.
if (process.env.AURIGA_CLOUD_DESK_DNS) {
  dns.lookup = createTestLookup(
    JSON.parse(process.env.AURIGA_CLOUD_DESK_DNS),
    dns.lookup.bind(dns),
  );
  syncBuiltinESMExports();
}
