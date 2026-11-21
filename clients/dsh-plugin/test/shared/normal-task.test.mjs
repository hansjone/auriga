import test from 'node:test';
import assert from 'node:assert/strict';
import { fixture } from './normal-task-fixture.mjs';
import {
  openSharedExecution,
  openSharedTask,
} from '../../src/shared/task-context.js';

test('team login without company binding authorizes ordinary task.execute with empty UAC variables', async () => {
  const f = fixture();
  const before = process.env.DSH_AURIGA_TOKEN;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.deepEqual(
      f.issued.map((item) => item.scopes),
      [['task.execute']],
    );
    assert.equal(f.state.reads, 0);
    assert.equal(task.env.DSH_AURIGA_ID, '');
    assert.equal(task.env.DSH_AURIGA_TOKEN, '');
    assert.equal(process.env.DSH_AURIGA_TOKEN, before);
    const id = f.ports.enterprise.taskId(task.token);
    await f.runtime.capability(
      task.token,
      f.binding.sessionId,
      id,
      'task.execute',
    );
    await assert.rejects(
      f.runtime.capability(task.token, f.binding.sessionId, id, 'outbound'),
    );
    await assert.rejects(
      f.runtime.capability(task.token, f.binding.sessionId, id, 'credentials'),
    );
    await assert.rejects(
      openSharedTask({
        ctx: f.ctx,
        runtime: f.runtime,
        ports: f.ports,
        execution: { ...f.task.execution },
        raw: true,
      }),
    );
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
  assert.equal(f.active.size, 0);
});

test('available company material uses a separate task ID and keeps task and UAC versions independent', async () => {
  const f = fixture();
  f.state.company = true;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.equal(f.issued.length, 2);
    assert.notEqual(f.issued[0].id, f.issued[1].id);
    assert.notEqual(f.issued[0].execution, f.issued[1].execution);
    assert.deepEqual(f.issued[0].scopes, ['task.execute']);
    assert.deepEqual(f.issued[1].scopes, ['outbound', 'credentials']);
    assert.equal(task.env.DSH_AURIGA_ID, '00001234');
    assert.equal(task.env.DSH_AURIGA_TOKEN, 'synthetic-company-material');
    assert.equal(f.state.reads, 1);
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
  assert.equal(f.active.size, 0);
});

test('raw-disabled company mode retains ordinary authorization and outbound without reading raw material', async () => {
  const f = fixture();
  f.state.company = true;
  f.state.raw = false;
  const task = await openSharedExecution({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: f.task.execution,
  });
  try {
    assert.deepEqual(f.issued[1].scopes, ['outbound']);
    assert.equal(f.state.reads, 0);
    assert.equal(task.env.DSH_AURIGA_TOKEN, '');
  } finally {
    await task.close();
    f.runtime.close();
    await f.contexts.close();
  }
});

test('unknown typed company state and invalid platform sessions never become ABSENT fallbacks', async (t) => {
  for (const kind of ['missing', 'unspecified', 'invalidPlatform'])
    await t.test(kind, async () => {
      const f = fixture();
      if (kind === 'missing') f.state.status = 0;
      if (kind === 'unspecified')
        f.state.status = 'SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED';
      if (kind === 'invalidPlatform') f.state.invalidPlatform = true;
      await assert.rejects(
        openSharedExecution({
          ctx: f.ctx,
          runtime: f.runtime,
          ports: f.ports,
          execution: f.task.execution,
        }),
      );
      assert.equal(f.active.size, 0);
      assert.equal(f.state.reads, 0);
      f.runtime.close();
      await f.contexts.close();
    });
});
