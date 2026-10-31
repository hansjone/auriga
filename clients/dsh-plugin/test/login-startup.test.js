import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installLocalAdmission } from '../src/local/host.js';
for (const protocol of ['https:', 'http:'])
  for (const platform of ['unconfigured', 'signed-out', 'offline', 'expired'])
    test(
      'R4 no login bootstrap changes native index access: ' +
        protocol +
        ' ' +
        platform,
      () => {
        const peer = { principal: { kind: 'owner' } };
        const connection = {
          admit: () => ({ peer }),
          authorizeIndex: () => true,
          isTrusted: () => true,
        };
        const index = connection.authorizeIndex;
        const admission = installLocalAdmission(connection, {}, { protocol });
        assert.equal(connection.authorizeIndex, index);
        assert.equal(connection.admit({ headers: {}, url: '/' }).peer, peer);
        admission.dispose();
      },
    );
