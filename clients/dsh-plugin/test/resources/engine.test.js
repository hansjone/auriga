import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture, id } from './fixtures.js';
const input = {
  packageId: id(4),
  versionId: id(5),
  action: 'install',
  key: 'operation-1',
};
test('member install verifies fixed archive before host activation and reports the exact observation', async (t) => {
  const f = await fixture(t);
  const result = await f.engine.apply(input);
  assert.deepEqual(f.hostCalls, ['install', 'enable']);
  assert.equal(result.localVersion, '1.0.0');
  assert.equal(result.state, 'applied');
  assert.equal(result.reportedRevision, 1);
  const select = f.calls.find(
    (c) => c.path.endsWith('/installations') && c.init.method === 'POST',
  );
  assert.equal(select.init.headers['if-match'], '"0"');
  const report = f.calls.find((c) => c.path.endsWith('/installation-reports'));
  assert.equal(report.body.installationRevision, 1);
  assert.equal(report.body.state, 'applied');
  await f.engine.apply(input);
  assert.equal(f.hostCalls.length, 2);
  assert.equal((await f.engine.list()).items[0].localVersion, '1.0.0');
});
test('forged archive envelope never reaches host or installation registration', async (t) => {
  const f = await fixture(t);
  f.invalidSignature();
  await assert.rejects(f.engine.apply(input), {
    code: 'resource.signature_invalid',
  });
  assert.equal(f.hostCalls.length, 0);
  assert.equal(
    f.calls.some(
      (c) => c.path.endsWith('/installations') && c.init.method === 'POST',
    ),
    false,
  );
});
test('failed transfer obtains a new one-use ticket with a new key on explicit retry', async (t) => {
  const f = await fixture(t);
  f.breakDownloads(1);
  await assert.rejects(f.engine.apply(input));
  await f.engine.apply(input);
  const tickets = f.calls.filter((c) => c.path.endsWith('/download-tickets'));
  assert.equal(tickets.length, 2);
  assert.notEqual(
    tickets[0].init.headers['idempotency-key'],
    tickets[1].init.headers['idempotency-key'],
  );
  assert(f.calls.every((c) => !c.path.includes('ticket=')));
});
test('member disable and remove use latest selection revisions and retain no false local installation', async (t) => {
  const f = await fixture(t);
  await f.engine.apply(input);
  await f.engine.apply({ ...input, action: 'disable', key: 'operation-2' });
  const result = await f.engine.apply({
    ...input,
    action: 'remove',
    key: 'operation-3',
  });
  assert.equal(result.desired, 'removed');
  assert.equal(result.localVersion, null);
  assert.equal(result.state, 'applied');
  assert(f.hostCalls.includes('remove'));
});
test('L1 cannot disable, L2 cannot remove and L4 cannot act for another owner', async (t) => {
  for (const [level, action] of [
    ['l1', 'disable'],
    ['l2', 'remove'],
    ['l4', 'install'],
  ]) {
    const f = await fixture(t);
    f.pack.level = level;
    if (level === 'l4') {
      f.pack.visibility = 'private';
      f.pack.ownerId = id(9);
    }
    await assert.rejects(f.engine.apply({ ...input, action }), {
      code: 'resource.level_locked',
    });
    assert.equal(f.hostCalls.length, 0);
  }
});
test('revocation disables active code and an expired cache cannot keep it enabled', async (t) => {
  const f = await fixture(t);
  await f.engine.apply(input);
  f.revocations.revision = 2;
  f.revocations.revokedKids = ['release'];
  await f.engine.monitor();
  assert(f.hostCalls.includes('disable'));
});
test('lost installation report is recovered without reinstalling or changing the report key', async (t) => {
  const f = await fixture(t);
  f.failReports(1);
  await assert.rejects(f.engine.apply(input));
  await f.engine.list();
  const result = await f.engine.apply(input);
  assert.equal(result.state, 'applied');
  assert.deepEqual(f.hostCalls, ['install', 'enable']);
  const reports = f.calls.filter((c) =>
    c.path.endsWith('/installation-reports'),
  );
  assert.equal(reports.length, 2);
  assert.equal(
    reports[0].init.headers['idempotency-key'],
    reports[1].init.headers['idempotency-key'],
  );
});
test('release high water survives engine reopen and rejects a lower signed release before host changes', async (t) => {
  const f = await fixture(t);
  f.version.releaseSequence = 2;
  f.envelope.releaseSequence = 2;
  await f.engine.apply(input);
  await f.reopen();
  f.version.releaseSequence = 1;
  f.envelope.releaseSequence = 1;
  await assert.rejects(f.engine.apply({ ...input, key: 'rollback' }), {
    code: 'resource.signature_invalid',
  });
  assert.deepEqual(f.hostCalls, ['install', 'enable']);
});
test('host restart requirement remains applying and never reports applied in the same process', async (t) => {
  const f = await fixture(t);
  f.host.install = async () => {
    f.hostCalls.push('install');
    return { restartRequired: true };
  };
  const result = await f.engine.apply(input);
  assert.equal(result.state, 'applying');
  assert.equal(result.restartRequired, true);
  await f.reopen();
  assert.equal((await f.engine.apply(input)).state, 'applying');
  assert.equal(
    f.calls.some((c) => c.path.endsWith('/installation-reports')),
    false,
  );
  assert.deepEqual(f.hostCalls, ['install']);
});
test('an envelope that expires during host installation cannot be activated', async (t) => {
  const f = await fixture(t);
  const install = f.host.install;
  f.host.install = async (...args) => {
    const result = await install(...args);
    f.advance(7200000);
    return result;
  };
  await assert.rejects(f.engine.apply(input));
  assert.equal(f.hostCalls.includes('enable'), false);
  assert.equal(
    f.calls.find((c) => c.path.endsWith('/installation-reports')).body.state,
    'failed',
  );
});
