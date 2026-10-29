import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

// Explicit integration input: the public Cordis library, never a DSH profile.
// CI without this public host dependency reports an opt-in skip, not a fake pass.
const modulePath = process.env.AURIGA_CORDIS_MODULE;
const diagnosticUrl = new URL(
  '../../src/network/diagnostics.js',
  import.meta.url,
).href;
const lifecycleUrl = new URL('../../src/network/lifecycle.js', import.meta.url)
  .href;
const options = {
  skip: modulePath
    ? false
    : 'set AURIGA_CORDIS_MODULE to public Cordis 4.0.5 lib/index.js',
};
const event = {
  event: 'failure',
  phase: 'ledger_restore',
  code: 'ledger_missing',
  http_status: 0,
  errno: 'none',
  elapsed_ms: 2,
  budget_ms: 0,
  attempt: 0,
  cancelled: false,
  created: false,
};
function child(body) {
  const result = spawnSync(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `
    import assert from 'node:assert/strict';
    import fs from 'node:fs';
    import { syncBuiltinESMExports } from 'node:module';
    const { Context } = await import(${JSON.stringify(modulePath ? pathToFileURL(modulePath).href : '')});
    const d = await import(${JSON.stringify(diagnosticUrl)});
    const { createNetworkLifecycle } = await import(${JSON.stringify(lifecycleUrl)});
    const event = ${JSON.stringify(event)};
    async function denied(ctx) {
      const lifecycle = createNetworkLifecycle({
        directory: 'unused', ctx, getAccess: async () => 'unused', authorizeLocal: async () => false,
        defaultProvider: { kind: 'device-connection', async prepare() { throw { status: 403, code: 'SECRET-code', message: 'SECRET-message' }; } },
      });
      lifecycle.login();
      for (let i=0; i<100 && lifecycle.status.state !== 'unavailable'; i++) await new Promise(r=>setImmediate(r));
      assert.equal(lifecycle.status.reason, 'forbidden');
      await lifecycle.close();
      assert.equal(lifecycle.status.state, 'stopped');
    }
    ${body}
  `,
    ],
    { encoding: 'utf8', timeout: 10000 },
  );
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  return result;
}
function records(stderr) {
  return stderr
    .trim()
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      assert.ok(line.startsWith('auriga_network_diagnostic '));
      return JSON.parse(line.slice('auriga_network_diagnostic '.length));
    });
}

test('真实Cordis默认无stdio输出且默认exporter过滤warn', options, () => {
  const result = child(`
    const ctx = new Context();
    let info = 0, warn = 0;
    ctx.logger.exporter({ export(message) { if(message.type === 'info') info++; if(message.type === 'warn') warn++; } });
    ctx.logger.info('test-info');
    ctx.logger.warn('test-warn');
    assert.equal(info, 1);
    assert.equal(warn, 0);
    d.createDiagnosticLog(ctx, 1)(event);
    await ctx.fiber.dispose();
  `);
  assert.equal(result.stderr, '');
  assert.equal(result.stdout, '');
});

test(
  '真实Cordis经插件专用exporter把自产12键事件写入子进程stderr',
  options,
  () => {
    const result = child(`
    const ctx = new Context();
    d.installNetworkDiagnosticSink?.(ctx);
    d.createDiagnosticLog(ctx, 1)(event);
    await ctx.fiber.dispose();
  `);
    const output = records(result.stderr);
    assert.equal(output.length, 1);
    assert.deepEqual(output[0], { v: 1, ...event, generation: 1 });
    assert.equal(Object.keys(output[0]).length, 12);
    assert.equal(result.stdout, '');
  },
);

test(
  '真实Cordis插件子上下文继承sink，子fiber注销后迟到事件不输出',
  options,
  () => {
    const result = child(`
    const root = new Context();
    let nested, emit;
    await root.plugin(async function parent(ctx) {
      d.installNetworkDiagnosticSink(ctx);
      nested = await ctx.plugin(function descendant(child) {
        emit = d.createDiagnosticLog(child, 4);
        emit(event);
      });
    });
    await nested.dispose();
    emit({ ...event, phase: 'normal_ready' });
    await root.fiber.dispose();
  `);
    assert.equal(records(result.stderr).length, 1);
  },
);

test(
  '真实Cordis重复安装不会重复输出，所有者卸载后旧emitter不借用新sink',
  options,
  () => {
    const result = child(`
    const root = new Context();
    let old;
    const first = await root.plugin(function first(ctx) {
      d.installNetworkDiagnosticSink(ctx);
      d.installNetworkDiagnosticSink(ctx);
      old = d.createDiagnosticLog(ctx, 1);
      old(event);
    });
    await first.dispose();
    await root.plugin(function second(ctx) {
      d.installNetworkDiagnosticSink(ctx);
      old({ ...event, phase: 'normal_ready' });
      d.createDiagnosticLog(ctx, 2)(event);
    });
    await root.fiber.dispose();
    old({ ...event, phase: 'bootstrap_ready' });
  `);
    assert.deepEqual(
      records(result.stderr).map((e) => e.generation),
      [1, 2],
    );
  },
);

test(
  '真实Cordis伪造相同marker和logger名字不授权输出，formatter与metadata不透传',
  options,
  () => {
    const result = child(`
    const ctx = new Context();
    d.installNetworkDiagnosticSink(ctx);
    const forged = 'auriga_network_diagnostic ' + JSON.stringify({ v:1, ...event, generation:1 });
    ctx.logger('auriga-network-diagnostic').warn(forged);
    ctx.logger('other').warn(forged);
    ctx.logger('auriga-network-diagnostic').warn('SECRET-raw', { tenant:'SECRET-tenant' });
    ctx.logger('auriga-network-diagnostic').warn({ toString() { throw Error('SECRET-format'); } });
    ctx.logger.exporter({ levels:{ default:2 }, formatters:{ s() { throw Error('SECRET-formatter'); } }, export(message) {
      // These fields never get serialized or formatted by the diagnostic sink.
      message.metadata = { tenant:'SECRET-meta' };
    } });
    d.createDiagnosticLog(ctx, 1)(event);
    ctx.logger('auriga-network-diagnostic').warn(forged);
    await ctx.fiber.dispose();
  `);
    assert.equal(records(result.stderr).length, 1);
    assert.equal(result.stderr.includes('SECRET'), false);
  },
);

test('真实Cordis sink一次性permit拒绝同步重放及异步迟到原文', options, () => {
  const result = child(`
    const ctx = new Context();
    let replayed = false, saved;
    ctx.logger.exporter({ levels:{ default:2 }, export(message) {
      if (replayed) return;
      replayed = true;
      saved = message.args[0];
      ctx.logger('other').warn(saved);
      ctx.logger('auriga-network-diagnostic').warn(saved, 'SECRET-extra');
      ctx.logger('auriga-network-diagnostic').warn(saved);
    } });
    d.installNetworkDiagnosticSink(ctx);
    d.createDiagnosticLog(ctx, 1)(event);
    await new Promise(r=>setImmediate(r));
    ctx.logger('auriga-network-diagnostic').warn(saved);
    await ctx.fiber.dispose();
  `);
  assert.equal(records(result.stderr).length, 1);
  assert.equal(result.stderr.includes('SECRET'), false);
});

test(
  '真实Cordis严格拒绝自产接口中的额外键、未知枚举和非法数字',
  options,
  () => {
    const result = child(`
    const ctx = new Context();
    d.installNetworkDiagnosticSink(ctx);
    const emit = d.createDiagnosticLog(ctx, 1);
    for (const bad of [
      { ...event, extra:'SECRET-extra' }, { ...event, phase:'SECRET-phase' },
      { ...event, code:'SECRET-code' }, { ...event, errno:'SECRET-errno' },
      { ...event, elapsed_ms:NaN }, { ...event, attempt:1.5 },
      { ...event, cancelled:'SECRET-cancelled' }, { ...event, created:'SECRET-created' },
      { ...event, http_status:700 }, { ...event, budget_ms:2147483648 },
    ]) emit(bad);
    emit(event);
    await ctx.fiber.dispose();
  `);
    assert.equal(records(result.stderr).length, 1);
    assert.equal(result.stderr.includes('SECRET'), false);
  },
);

for (const failure of ['exporter-install', 'logger-export', 'stderr'])
  test(
    '真实Cordis ' + failure + '失败保持原业务拒绝且没有raw fallback',
    options,
    () => {
      const result = child(`
    const ctx = new Context();
    const failure = ${JSON.stringify(failure)};
    if (failure === 'exporter-install') ctx.logger.exporter = () => { throw Error('SECRET-install'); };
    if (failure === 'logger-export') ctx.logger.exporter({ levels:{ default:2 }, export() { throw Error('SECRET-export'); } });
    d.installNetworkDiagnosticSink(ctx);
    let writes = 0;
    if (failure === 'stderr') {
      fs.writeSync = (fd) => { assert.equal(fd, 2); writes++; throw Error('SECRET-stderr'); };
      syncBuiltinESMExports();
    }
    await denied(ctx);
    if (failure === 'stderr') assert.equal(writes, 1);
    await ctx.fiber.dispose();
  `);
      assert.equal(result.stderr, '');
      assert.equal(result.stdout, '');
    },
  );

test('真实Cordis写入失败清除permit且不消耗成功输出去重额度', options, () => {
  const result = child(`
    const ctx = new Context();
    d.installNetworkDiagnosticSink(ctx);
    const emit = d.createDiagnosticLog(ctx, 1);
    const original = fs.writeSync;
    let attempts = 0;
    fs.writeSync = () => { attempts++; throw Error('SECRET-write'); };
    syncBuiltinESMExports();
    emit(event);
    assert.equal(attempts, 1);
    fs.writeSync = original;
    syncBuiltinESMExports();
    emit(event);
    emit(event);
    await ctx.fiber.dispose();
  `);
  assert.equal(records(result.stderr).length, 1);
  assert.equal(result.stderr.includes('SECRET'), false);
});

test('真实Cordis生产lifecycle拒绝经真实sink输出首失并过滤秘密', options, () => {
  const result = child(`
    const ctx = new Context();
    d.installNetworkDiagnosticSink(ctx);
    await denied(ctx);
    await ctx.fiber.dispose();
  `);
  const output = records(result.stderr);
  assert.equal(output.length, 1);
  assert.equal(output[0].phase, 'identity_prepare');
  assert.equal(output[0].http_status, 403);
  assert.equal(output[0].code, 'http_error');
  assert.equal(result.stderr.includes('SECRET'), false);
});
