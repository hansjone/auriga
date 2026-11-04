import { test } from 'node:test';
import assert from 'node:assert/strict';
import { installManagedSettingsOwnership } from '../src/managed/settings.js';

test('R7 managed owner composition uses the public structured transport bootstrap, without changing HTTP transport', () => {
  let inject,
    disposed = false;
  const cleanup = installManagedSettingsOwnership({
    on(event, callback) {
      assert.equal(event, 'webserver/index-inject');
      inject = callback;
      return () => {
        disposed = true;
      };
    },
  });
  const rows = [];
  inject(rows);
  assert.deepEqual(rows, [
    { kind: 'global', name: '__DSH_TRANSPORT__', value: { ownsHost: true } },
  ]);
  assert.throws(() => inject(rows), /conflicting transport/);
  cleanup();
  assert.equal(disposed, true);
});
