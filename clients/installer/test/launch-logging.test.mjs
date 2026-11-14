import test from 'node:test';
import assert from 'node:assert/strict';
import { cpSync, existsSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';
import { BRAND_NAME } from '../runtime/brand.mjs';
import { install, readState } from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

for (const code of ['desktop.smoke_failed', 'unexpected_failure'])
  test(`launch logs only the safe update failure notice: ${code}`, async (t) => {
    const base = tempBase(t),
      root = join(base, 'app'),
      dataHome = join(base, 'home'),
      runtime = join(base, 'runtime');
    const payload = buildPayload(base);
    install({ payload: payload.dir, root, dataHome, smoke: () => {} });
    const before = readState(root);

    // Exercise the unmodified launch implementation. Only its unrelated OS,
    // supervisor and DSH boundaries are isolated; no processes are started.
    cpSync(new URL('../runtime/', import.meta.url), runtime, {
      recursive: true,
    });
    writeFileSync(
      join(runtime, 'process-tree.mjs'),
      `export class ProcessTree {
        record = { children: [] };
        singleton() { return () => {}; }
        async stopTree() {}
      }
      export function assertNTFS() {}
      export function notifyPrivateOnly() {}
      export function notifySharedRestart() {}`,
    );
    writeFileSync(
      join(runtime, 'supervisor.mjs'),
      `export class Supervisor {
        guardian = { read: () => null };
        async prepare() {}
        async reconcile() {}
      }
      export function packagedTrust() { return {}; }`,
    );
    writeFileSync(
      join(runtime, 'desktop-supervision.mjs'),
      'export async function superviseDsh() { return 0; }',
    );
    const sensitive = 'PRIVATE_UPSTREAM_CONFIG_' + randomUUID();
    writeFileSync(
      join(runtime, 'update.mjs'),
      `export async function applyPending() {
        throw Object.assign(new Error(${JSON.stringify(sensitive)}, {
          cause: new Error('PRIVATE_UPSTREAM_CAUSE')
        }), { code: ${JSON.stringify(code)} });
      }`,
    );
    const { launch } = await import(pathToFileURL(join(runtime, 'core.mjs')));
    const errors = [];
    t.mock.method(console, 'error', (...args) => errors.push(args));

    assert.equal(await launch({ root }), 0);
    const expected =
      `${BRAND_NAME} 更新失败，已保留旧版。` +
      (code === 'desktop.smoke_failed' ? '[desktop.smoke_failed]' : '');
    // Both startup and post-exit application must retain the same safe output.
    assert.deepEqual(errors, [[expected], [expected]]);
    assert.ok(!JSON.stringify(errors).includes(sensitive));
    assert.ok(!JSON.stringify(errors).includes('PRIVATE_UPSTREAM_CAUSE'));
    assert.deepEqual(readState(root), before);
    assert.equal(existsSync(join(root, 'desktop-session.json')), false);
  });
