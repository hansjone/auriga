import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
const read = (p) =>
  JSON.parse(
    readFileSync(new URL('../resource/' + p, import.meta.url), 'utf8'),
  );
const fixtures = read('installation-revision-fixtures.json');
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
for (const name of ['signatures', 'package', 'unified']) {
  const file = new URL('../resource/' + name + '.schema.json', import.meta.url);
  ajv.addSchema(JSON.parse(readFileSync(file, 'utf8')), file.href);
}
const dispatchSchema = ajv.getSchema(
  new URL('../resource/unified.schema.json', import.meta.url).href +
    '#/$defs/UnifiedResourceDispatchRef',
);
for (const fixture of fixtures.cases) {
  test('installation revision ' + fixture.id, () => {
    const input = fixture.input;
    if (fixture.operation === 'dispatch-validation') {
      assert.equal(dispatchSchema(input), fixture.expect);
      return;
    }
    if (fixture.operation === 'lifecycle') {
      let persistedRevision = input.initialRevision;
      const responses = new Map();
      const responseRevisions = input.steps.map((step) => {
        const prior = responses.get(step.idempotencyKey);
        if (prior) {
          assert.deepEqual(step, prior.request);
          return prior.revision;
        }
        assert(['install', 'remove'].includes(step.action));
        assert.equal(step.ifMatch, persistedRevision);
        persistedRevision++;
        responses.set(step.idempotencyKey, {
          request: step,
          revision: persistedRevision,
        });
        return persistedRevision;
      });
      assert.deepEqual(
        { responseRevisions, persistedRevision },
        fixture.expect,
      );
      return;
    }
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
