import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { sources, mutate, verdict } from './support/plugin-send-verdict.mjs';
import { persistenceMarker, receiptMarker } from './support/plugin-send.mjs';

for (const mode of Object.keys(sources))
  test(`${mode} 仅改变一个指定生产语义并拒绝不精确叠加`, async () => {
    const source = await readFile(
      new URL('../../' + sources[mode], import.meta.url),
      'utf8',
    );
    const changed = mutate(source, mode);
    assert.notEqual(changed, source);
    if (mode === 'SkipSubmissionCommit') {
      assert.ok(!changed.includes('draft.durable.submissions[key] = saved;'));
      assert.ok(changed.includes('const bytes = await submit('));
    } else
      assert.ok(
        changed.includes(
          'expected_state_version: String(ledger.peek().revision),',
        ),
      );
    assert.throws(() => mutate('', mode));
    assert.throws(() => mutate(source + source, mode));
  });
const failure = (mode) => ({
  completed: false,
  stages: ['ready', 'gateway-ready', 'gateway-closed', 'java-closed'].map(
    (stage) => ({ stage, exitCode: 0, signal: null }),
  ),
  businessFailure: {
    name: 'AssertionError',
    code: 'ERR_ASSERTION',
    message:
      mode === 'SkipSubmissionCommit' ? persistenceMarker : receiptMarker,
    completedChecks: ['MEMBER and EXTERNAL real source issuance'],
  },
  failures: [
    {
      message:
        mode === 'SkipSubmissionCommit' ? persistenceMarker : receiptMarker,
    },
  ],
});
for (const mode of Object.keys(sources))
  test(`${mode} 判决仅接受同正控中的指定业务断言和完整清理`, () => {
    verdict(failure(mode), mode, 1);
    assert.throws(() => verdict(failure(mode), mode, 0));
    for (const change of [
      (m) => {
        m.businessFailure.message = 'timeout';
      },
      (m) => {
        m.businessFailure.name = 'SQLException';
      },
      (m) => {
        m.businessFailure.completedChecks = [];
      },
      (m) => {
        m.stages[2].exitCode = 1;
      },
      (m) => {
        m.failures.push({ message: 'PG remains alive' });
      },
    ]) {
      const m = failure(mode);
      change(m);
      assert.throws(() => verdict(m, mode, 1));
    }
  });
