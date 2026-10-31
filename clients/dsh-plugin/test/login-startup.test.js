import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
for (const protocol of ['dsh-app:', 'http:'])
  for (const platform of ['unconfigured', 'signed-out', 'offline', 'expired'])
    test(
      'R2 bootstrap never blocks local DSH: ' + protocol + ' ' + platform,
      async () => {
        const source = await readFile(
          new URL('../src/login/startup.js', import.meta.url),
          'utf8',
        );
        let calls = 0;
        await runInNewContext(source, {
          location: {
            protocol,
            replace() {
              calls++;
            },
          },
          fetch() {
            calls++;
            throw Error('unexpected');
          },
        });
        assert.equal(calls, 0);
      },
    );
