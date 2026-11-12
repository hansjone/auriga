import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import {
  desiredState,
  requireInstallation,
  assertSelectionProgress,
} from '../../src/resources/selection-state.js';
import { schemaValid } from '../../src/resources/schema.js';
import { catalogService } from '../../src/resources/catalog-service.js';
import { catalogFixture } from './catalog-fixture.js';
const root = new URL('../../../../contracts/resource/', import.meta.url);
const read = (file) => JSON.parse(readFileSync(new URL(file, root), 'utf8'));
for (const fixture of read('desired-state-fixtures.json').cases.filter(
  (c) => c.operation === 'execute',
)) {
  test('C K2 tombstone execution: ' + fixture.id, () => {
    const { incoming, current } = fixture.input;
    const previous = {
      ...current,
      installations: [
        {
          resource: current.resource,
          installationRevision: current.resource.installationRevision,
        },
      ],
    };
    const rows = [
      {
        ...incoming,
        installationRevision: incoming.resource.installationRevision,
      },
    ];
    if (fixture.expect.execute)
      assert.doesNotThrow(() => assertSelectionProgress(previous, rows));
    else
      assert.throws(() => assertSelectionProgress(previous, rows), {
        code: 'common.conflict',
      });
  });
}
test('Desktop rejects a lower-revision tombstone even in a newer generation and preserves active content', (t) => {
  const f = catalogFixture(t),
    row = {
      ...f.getRow(),
      installationRevision: 3,
      resource: { ...f.resource, installationRevision: 3 },
    };
  f.desktop.prepare(
    f.who,
    [row],
    [
      {
        manifest: f.raw,
        artifacts: [f.archive],
        revocations: Buffer.from(JSON.stringify(f.revoked)),
      },
    ],
  );
  f.allow();
  f.desktop.finish(f.who, () => true);
  const old = {
    ...row,
    installationRevision: 2,
    selectionRevision: 8,
    generation: 10,
    resource: {
      ...row.resource,
      installationRevision: 2,
      desiredState: 'REMOVED',
      enabled: false,
    },
  };
  assert.throws(() => f.desktop.prepare(f.who, [old], []), {
    code: 'common.conflict',
  });
  assert.equal(f.desktop.read(f.who).installations[0].installationRevision, 3);
  assert.match(
    readFileSync(f.active + '/example/SKILL.md', 'utf8'),
    /Verified body/,
  );
});
for (const fixture of read('desired-state-fixtures.json').cases.filter(
  (c) => c.operation === 'desired-state',
)) {
  test('C K2 desired state: ' + fixture.id, () => {
    if (fixture.expect.errorCode)
      assert.throws(() => desiredState(fixture.input), {
        code: fixture.expect.errorCode,
      });
    else assert.equal(desiredState(fixture.input), fixture.expect.state);
  });
}
for (const fixture of read('installation-revision-fixtures.json').cases.filter(
  (c) => c.operation === 'dispatch-validation',
)) {
  test('C K2 dispatch schema: ' + fixture.id, () =>
    assert.equal(
      schemaValid('unified', 'UnifiedResourceDispatchRef', fixture.input),
      fixture.expect,
    ),
  );
}
test('C binds a K2 nested resource revision to the authoritative member installation CAS', (t) => {
  const f = catalogFixture(t),
    row = f.getRow();
  assert.equal(requireInstallation(row), row);
  assert.throws(
    () =>
      requireInstallation({
        ...row,
        installationRevision: row.installationRevision + 1,
      }),
    { code: 'resource.state_conflict' },
  );
  const legacy = globalThis.structuredClone(row);
  assert.throws(
    () => f.desktop.prepare(f.who, [{ ...row, installationRevision: 2 }], []),
    { code: 'resource.state_conflict' },
  );
  delete legacy.resource.installationRevision;
  delete legacy.resource.desiredState;
  assert.equal(requireInstallation(legacy), legacy);
});
for (const [state, observed] of [
  ['DISABLED', 'disabled'],
  ['REMOVED', 'removed'],
]) {
  test(
    'C observes ' +
      state +
      ' after reconnect and never installs from a legacy enabled=true boolean',
    async (t) => {
      const f = catalogFixture(t),
        resource = {
          ...f.resource,
          desiredState: state,
          enabled: true,
          installationRevision: 2,
        };
      const incoming = {
        ...f.getRow(),
        resource,
        installationRevision: 2,
        selectionRevision: 8,
        generation: 10,
      };
      f.setRow(incoming);
      const service = catalogService({
        ...f,
        privateSkills: {},
        host: {},
        roots: () =>
          assert.fail('non-enabled selection must not load trust roots'),
      });
      const result = await service.list();
      assert.equal(result.items[0].state, observed);
      assert.deepEqual(readdirSync(f.active), []);
      assert.equal(
        f.requests.some((r) =>
          /downloads|download-tickets|\/manifest$/.test(r.path),
        ),
        false,
      );
      assert.equal(f.desktop.confirmDisabled(f.who, incoming), true);
      assert.equal(
        f.desktop.confirmDisabled(f.who, {
          ...incoming,
          resource: { ...resource, desiredState: 'ENABLED' },
        }),
        false,
      );
      assert.equal(
        f.desktop.confirmDisabled(f.who, {
          ...incoming,
          installationRevision: 3,
        }),
        false,
      );
      assert.equal(f.getRow().report.state, observed);
    },
  );
}
