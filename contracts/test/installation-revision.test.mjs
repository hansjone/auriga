import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const read = (p) =>
  JSON.parse(
    readFileSync(new URL('../resource/' + p, import.meta.url), 'utf8'),
  );
const fixtures = read('installation-revision-fixtures.json');
for (const fixture of fixtures.cases) {
  test('installation revision ' + fixture.id, () => {
    const input = fixture.input;
    if (fixture.operation === 'renewal') {
      assert.deepEqual(
        {
          sameInstallationRevision:
            input.before.installationRevision ===
            input.after.installationRevision,
        },
        fixture.expect,
      );
      assert.notDeepEqual(input.before.lease, input.after.lease);
      return;
    }
    if (fixture.operation === 'mutation') {
      assert(
        ['install', 'update', 'enable', 'disable', 'remove'].includes(
          input.action,
        ),
      );
      const accepted = input.persistedRevision === input.ifMatch;
      assert.deepEqual(
        {
          accepted,
          installationRevision: input.persistedRevision + (accepted ? 1 : 0),
        },
        fixture.expect,
      );
      return;
    }
    assert.equal(fixture.operation, 'report');
    const { expected, report, channel } = input;
    const revision = expected.resource.installationRevision;
    const applied =
      Number.isSafeInteger(revision) &&
      revision > 0 &&
      report.state === 'applied' &&
      report.reportSource === channel &&
      JSON.stringify(report.identity) ===
        JSON.stringify(expected.resource.identity) &&
      report.installationRevision === revision &&
      report.selectionRevision === expected.selectionRevision &&
      report.generation === expected.generation &&
      report.observedGeneration === expected.generation;
    assert.deepEqual(
      { applied, errorCode: applied ? null : 'resource.state_conflict' },
      fixture.expect,
    );
  });
}
