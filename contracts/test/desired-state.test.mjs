import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const fixtures = JSON.parse(
  readFileSync(
    new URL('../resource/desired-state-fixtures.json', import.meta.url),
    'utf8',
  ),
);
const states = ['ENABLED', 'DISABLED', 'REMOVED'];
function desiredState(resource) {
  if (Object.hasOwn(resource, 'desiredState'))
    return states.includes(resource.desiredState)
      ? resource.desiredState
      : null;
  return resource.enabled ? 'ENABLED' : 'DISABLED';
}
function sameTuple(expected, value) {
  return (
    expected.resource.installationRevision > 0 &&
    JSON.stringify(expected.resource.identity) ===
      JSON.stringify(value.identity) &&
    expected.resource.installationRevision === value.installationRevision &&
    expected.selectionRevision === value.selectionRevision &&
    expected.generation === value.generation
  );
}
function acceptsRemoval(input) {
  return (
    desiredState(input.expected.resource) === 'REMOVED' &&
    sameTuple(input.expected, input.report) &&
    input.report.observedGeneration === input.expected.generation &&
    input.report.state === 'removed' &&
    input.report.reportSource === input.channel &&
    input.contentCleared
  );
}
for (const fixture of fixtures.cases) {
  test('desired state ' + fixture.id, () => {
    const input = fixture.input;
    switch (fixture.operation) {
      case 'desired-state': {
        const state = desiredState(input);
        assert.deepEqual(
          { state, errorCode: state ? null : 'common.validation_failed' },
          fixture.expect,
        );
        break;
      }
      case 'dispatch': {
        assert(['remove', 'disable'].includes(input.action));
        assert.deepEqual(
          {
            ...input.before,
            installationRevision: input.before.installationRevision + 1,
            enabled: false,
            desiredState: input.action === 'remove' ? 'REMOVED' : 'DISABLED',
          },
          fixture.expect,
        );
        break;
      }
      case 'acknowledgement': {
        const accepted = acceptsRemoval(input);
        assert.deepEqual(
          { accepted, errorCode: accepted ? null : 'resource.state_conflict' },
          fixture.expect,
        );
        break;
      }
      case 'collect': {
        const collected =
          acceptsRemoval(input) &&
          input.nextGeneration === input.expected.generation + 1;
        const resources = collected
          ? input.resources.filter(
              (resource) =>
                JSON.stringify(resource) !==
                JSON.stringify(input.expected.resource),
            )
          : input.resources;
        assert.deepEqual(
          {
            collected,
            resources,
            installationRevision: input.expected.resource.installationRevision,
          },
          fixture.expect,
        );
        break;
      }
      case 'execute': {
        const execute =
          desiredState(input.incoming.resource) === 'REMOVED' &&
          desiredState(input.current.resource) === 'REMOVED' &&
          sameTuple(input.current, {
            ...input.incoming.resource,
            selectionRevision: input.incoming.selectionRevision,
            generation: input.incoming.generation,
          });
        assert.deepEqual({ execute }, fixture.expect);
        break;
      }
      default:
        assert.fail('Unknown fixture operation');
    }
  });
}
