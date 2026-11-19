import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  checkPlatformMigration,
  readPluginSources,
} from '../../../../contracts/test/auth-k-source-scan.mjs';

test('AUTH-K permits only the new fixed messageContext operation and rejects variants or bearer/TCP expansion', () => {
  const base = new URL('../../../../contracts/plugin/', import.meta.url);
  const inventory = JSON.parse(
    readFileSync(new URL('shell-auth-call-sites.json', base), 'utf8'),
  );
  const operations = JSON.parse(
    readFileSync(new URL('shell-business-operations.json', base), 'utf8'),
  ).operations;
  const sources = readPluginSources();
  const broker = { ...inventory, migrationState: 'broker', calls: [] };
  const file = 'shared/node-transport.js';
  const original = sources[file];
  assert.ok(original.includes("'messageContext',"));
  assert.doesNotThrow(() =>
    checkPlatformMigration(broker, operations, sources),
  );
  for (const [before, after] of [
    ["'messageContext',", "'messageContext', 'messageContextUnrestricted',"],
    ["'messageContext',", "'messageContext/' + body.operation,"],
    [
      "'content-type': 'application/json',",
      "authorization: body.capability, 'content-type': 'application/json',",
    ],
    ['socketPath,', "hostname: 'untrusted.invalid', port: 443,"],
  ]) {
    assert.ok(original.includes(before));
    assert.throws(() =>
      checkPlatformMigration(broker, operations, {
        ...sources,
        [file]: original.replace(before, after),
      }),
    );
  }
});
