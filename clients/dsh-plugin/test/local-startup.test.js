import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installLocalAdmission } from '../src/local/host.js';
for (const configured of [false, true])
  test(
    'R2 platform configuration does not change DSH admission: ' + configured,
    () => {
      const peer = { principal: { kind: 'owner' } },
        connection = {
          admit: (req) =>
            req.headers.cookie === 'dsh=valid' ? { peer } : { rejection: 401 },
          authorizeIndex: () => true,
          isTrusted: () => true,
        };
      const original = connection.admit;
      const gate = installLocalAdmission(
        connection,
        { isConfigured: () => configured },
        { protocol: 'http:' },
      );
      try {
        for (const upgrade of [undefined, 'websocket']) {
          const req = { headers: { cookie: 'dsh=valid', upgrade } };
          assert.equal(connection.admit(req).peer, peer);
          assert.equal(connection.authorizeIndex(req, {}), true);
          assert.equal(
            connection.admit({ headers: { 'x-auriga-native': 'true' } })
              .rejection,
            401,
          );
        }
      } finally {
        gate.dispose();
      }
      assert.equal(connection.admit, original);
    },
  );
