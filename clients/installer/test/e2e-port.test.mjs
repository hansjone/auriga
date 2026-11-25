import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { desktopLaunchArgs } from '../runtime/core.mjs';

// Exercise the script's real helper without running its installer main, opening
// sockets, starting a process, or sending a credential to an actual endpoint.
const source = readFileSync(
  new URL('../scripts/e2e.mjs', import.meta.url),
  'utf8',
);
const start = source.indexOf('async function withDsh(fn) {');
const end = source.indexOf('\nasync function pluginChecks', start);
assert.ok(start >= 0 && end > start);
const load = new Function(
  'spawn',
  'spawnSync',
  'root',
  'env',
  'sandbox',
  'started',
  'freePort',
  'join',
  'globalThis',
  `${source.slice(start, end)}\nreturn withDsh;`,
);

for (const current of [false, true]) {
  test(`e2e probes an explicit isolated port for ${current ? 'current 12721' : 'legacy 3080'} runtime`, async () => {
    const isolatedPort = 43129;
    const launched = [];
    const killed = [];
    const requests = [];
    let actualPort;
    const withDsh = load(
      (_command, argv) => {
        launched.push(argv);
        const input = argv.slice(2);
        const { values } = parseArgs({
          args: current ? desktopLaunchArgs(input) : input,
          options: { port: { type: 'string' }, 'no-open': { type: 'boolean' } },
        });
        actualPort = Number(values.port ?? 3080);
        return {
          pid: 12345,
          stdout: {
            on(_event, listener) {
              listener(
                `http://127.0.0.1:${actualPort}/?token=synthetic-e2e-token`,
              );
            },
          },
          stderr: { on() {} },
        };
      },
      (command, args) => killed.push({ command, args }),
      '/synthetic/root',
      {},
      '/synthetic/sandbox',
      [],
      async () => isolatedPort,
      join,
      {
        async fetch(url, init) {
          requests.push({ url, init });
          assert.equal(new URL(url).port, String(actualPort));
          return {
            headers: {
              getSetCookie: () => ['session=synthetic-session; Path=/'],
            },
          };
        },
      },
    );
    await withDsh(async (dsh) => {
      assert.equal(dsh.base, `http://127.0.0.1:${isolatedPort}`);
      assert.equal(dsh.out().includes('synthetic-e2e-token'), false);
      await dsh.call('/auriga/settings/state');
    });
    assert.deepEqual(launched[0].slice(2), [
      '--port',
      String(isolatedPort),
      '--no-open',
    ]);
    assert.equal(actualPort, isolatedPort);
    assert.equal(requests.length, 2);
    assert.equal(
      requests[1].init.headers.origin,
      `http://127.0.0.1:${isolatedPort}`,
    );
    assert.deepEqual(killed, [
      { command: 'taskkill', args: ['/PID', '12345', '/T', '/F'] },
    ]);
  });
}
