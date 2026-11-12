import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv from '../../services/contracts/node_modules/ajv/dist/2020.js';
import addFormats from '../../services/contracts/node_modules/ajv-formats/dist/index.js';
import { parse as parseYaml } from '../../services/contracts/node_modules/yaml/dist/index.js';
const root = new URL('../', import.meta.url);
const read = (file) => JSON.parse(readFileSync(new URL(file, root), 'utf8'));
const ajv = new Ajv({ strict: true, allErrors: true });
addFormats(ajv);
for (const name of ['signatures', 'package', 'unified']) {
  const file = new URL(`resource/${name}.schema.json`, root);
  ajv.addSchema(JSON.parse(readFileSync(file, 'utf8')), file.href);
}
const check = (file, name, value) =>
  ajv.getSchema(
    new URL(`resource/${file}.schema.json`, root).href + '#/$defs/' + name,
  )(value);
const vectors = read('resource/unified-vectors.json');
const canon = (value) => JSON.stringify(value);
import { evaluateVector } from '../../services/contracts/scripts/unified-vector-runner.mjs';
for (const vector of vectors.cases) {
  test('shared trust vector ' + vector.id, () => {
    assert.deepEqual(
      evaluateVector(vector, vectors.trust),
      vector.expect,
      vector.id,
    );
  });
}
const sample = vectors.cases.find((c) => c.id === 'migration-proof-valid').input
  .proof;
test('public schema registry names and every definition compile', () => {
  const registry = read('resource/unified-registry.json');
  for (const name of Object.keys(registry.schemas))
    assert(name.startsWith('Unified'));
  for (const name of Object.keys(read('resource/unified.schema.json').$defs))
    assert(
      ajv.getSchema(
        new URL('resource/unified.schema.json', root).href + '#/$defs/' + name,
      ),
    );
});

test('risk floor, quorum and review role have machine-checked rejection cases', () => {
  const spec = read('openapi/resource.yaml');
  const uri = 'https://example.com/unified-openapi';
  function refs(v) {
    if (Array.isArray(v)) return v.map(refs);
    if (!v || typeof v !== 'object') return v;
    return Object.fromEntries(
      Object.entries(v).map(([k, x]) => [
        k,
        k === '$ref'
          ? x
              .replace('#/components/schemas/', '#/$defs/')
              .replace('../resource/', new URL('resource/', root).href)
          : refs(x),
      ]),
    );
  }
  ajv.addSchema({ $id: uri, $defs: refs(spec.components.schemas) });
  const risk = ajv.getSchema(uri + '#/$defs/UnifiedRiskAssessment');
  assert(
    risk({
      minimumLevel: 'r2',
      level: 'r2',
      requiredReviewers: 2,
      requiredSecurityReviewers: 1,
    }),
  );
  for (const bad of [
    {
      minimumLevel: 'r2',
      level: 'r1',
      requiredReviewers: 1,
      requiredSecurityReviewers: 0,
    },
    {
      minimumLevel: 'r3',
      level: 'r2',
      requiredReviewers: 2,
      requiredSecurityReviewers: 1,
    },
    {
      minimumLevel: 'r2',
      level: 'r2',
      requiredReviewers: 1,
      requiredSecurityReviewers: 0,
    },
  ])
    assert(!risk(bad));
  const decision = ajv.getSchema(uri + '#/$defs/UnifiedReviewDecision');
  const body = {
    decision: 'approve',
    reason: 'Reviewed',
    versionRevision: 1,
    digest: sample.unifiedEnvelope.statement.manifestDigest,
    role: 'security',
  };
  assert(decision(body));
  assert(!decision({ ...body, role: 'administrator' }));
  const quorum = (author, decisions) => {
    const reviewers = new Map();
    for (const d of decisions)
      if (d.id !== author && (d.role !== 'security' || d.qualified))
        reviewers.set(d.id, d);
    return (
      reviewers.size >= 2 &&
      [...reviewers.values()].some((d) => d.role === 'security' && d.qualified)
    );
  };
  const general = { id: 'reviewer-1', role: 'general' },
    security = { id: 'reviewer-2', role: 'security', qualified: true };
  assert(quorum('author', [general, security]));
  assert(!quorum('author', [general, { ...security, id: 'author' }]));
  assert(!quorum('author', [general, { ...security, id: general.id }]));
  assert(!quorum('author', [general, { ...security, qualified: false }]));
});
test('manifest ACL is rechecked on every read and old/new event digest fields cannot mix', () => {
  const spec = read('openapi/resource.yaml');
  const op =
    spec.paths[
      '/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/manifest'
    ].get;
  assert.deepEqual(op['x-authorization'], {
    tenant: 'authenticated',
    visibility: 'package-version',
    resolveKind: true,
    allowedKinds: ['skill', 'tool'],
    pluginStatus: 404,
    locatorGrantsAccess: false,
    checkEveryRead: true,
  });
  const allowed = (c) =>
    c.sameTenant &&
    c.visibleVersion &&
    c.permission &&
    ['skill', 'tool'].includes(c.kind);
  const good = {
    sameTenant: true,
    visibleVersion: true,
    permission: true,
    kind: 'skill',
  };
  assert(allowed(good));
  for (const field of ['sameTenant', 'visibleVersion', 'permission'])
    assert(
      !allowed({
        ...good,
        [field]: false,
        knowsDigest: true,
        hasLocator: true,
      }),
    );
  assert(!allowed({ ...good, kind: 'plugin' }));
  const eventFile = new URL('events/resource.schema.json', root);
  ajv.addSchema(read('events/resource.schema.json'), eventFile.href);
  const old = ajv.getSchema(
      eventFile.href + '#/$defs/ResourceVersionPublished',
    ),
    unified = ajv.getSchema(
      eventFile.href + '#/$defs/UnifiedResourceVersionPublished',
    );
  const e = {
    packageId: sample.legacyEnvelope.packageId,
    versionId: sample.legacyEnvelope.versionId,
    version: '1.2.3',
    digest: sample.legacyEnvelope.statement.digest,
    revision: 1,
  };
  assert(old(e));
  assert(!unified(e));
  const { digest, ...common } = e;
  const next = {
    ...common,
    kind: 'skill',
    signatureVersion: 2,
    manifestDigest: sample.unifiedEnvelope.statement.manifestDigest,
    artifactDigests: [digest],
  };
  assert(unified(next));
  assert(!old(next));
  assert(!unified({ ...next, digest }));
});
test('guardian private-only state requires stopped process, cleared view and new session', () => {
  const p = sample.legacyEnvelope;
  const value = {
    tenantId: p.tenantId,
    ownerId: p.packageId,
    hostId: p.versionId,
    selectionRevision: 1,
    generation: 1,
    state: 'private_only',
    reason: 'lease_expired',
    lease: {
      envelopeNotAfter: p.notAfter,
      revocationsExpiresAt: p.notAfter,
      expiresAt: p.notAfter,
      revocationRevision: 1,
    },
    processStopped: true,
    viewCleared: true,
    newSessionRequired: true,
  };
  assert(check('unified', 'UnifiedGuardianState', value));
  for (const field of ['processStopped', 'viewCleared', 'newSessionRequired'])
    assert(
      !check('unified', 'UnifiedGuardianState', { ...value, [field]: false }),
    );
  assert(
    !check('unified', 'UnifiedGuardianState', { ...value, state: 'unknown' }),
  );
});

test('kind/delivery matrix fails closed for tool, plugin and unknown kinds at unified P1 entry', () => {
  const matrix = read('resource/unified-authorization.json');
  const allowed = (kind, delivery, permission) =>
    matrix.rules.some(
      (r) =>
        r.kind === kind &&
        r.delivery === delivery &&
        r.entry === 'unified' &&
        r.p1Enabled === true &&
        permission === r.basePermission,
    );
  assert(allowed('skill', 'text-only', 'resource.packages.upload'));
  for (const rule of matrix.rules.filter((r) => r.kind !== 'skill'))
    assert(!allowed(rule.kind, rule.delivery, 'resource.packages.upload'));
  assert(!allowed('unknown', 'text-only', 'resource.packages.upload'));
  assert(!allowed('skill', 'text-only', 'resource.packages.read'));
  assert.equal(matrix.default, 'deny');
  assert.equal(matrix.securityReviewRoleRequiresQualification, true);
});

test('applied observations require current immutable identity and all three revision counters', () => {
  const identity = {
    tenantId: 'tenant',
    packageId: 'package',
    versionId: 'version',
    kind: 'skill',
    version: '1.0.0',
    manifestDigest: 'digest',
  };
  const current = {
    identity,
    installationRevision: 3,
    selectionRevision: 4,
    generation: 5,
  };
  const valid = (report, channel) =>
    report.state === 'applied' &&
    report.reportSource === channel &&
    ['installationRevision', 'selectionRevision', 'generation'].every(
      (k) => report[k] === current[k],
    ) &&
    report.observedGeneration === current.generation &&
    canon(report.identity) === canon(current.identity);
  const good = {
    ...current,
    observedGeneration: 5,
    state: 'applied',
    reportSource: 'node',
  };
  assert(valid(good, 'node'));
  assert(!valid(good, 'member'));
  for (const field of [
    'installationRevision',
    'selectionRevision',
    'generation',
    'observedGeneration',
  ])
    assert(!valid({ ...good, [field]: 1 }, 'node'));
  for (const state of [
    'applying',
    'pending_restart',
    'superseded',
    'overridden',
    'expired',
    'revoked',
    'failed',
  ])
    assert(!valid({ ...good, state }, 'node'));
  assert(
    !valid({ ...good, identity: { ...identity, versionId: 'other' } }, 'node'),
  );
});

import {
  mergeCordisFixture,
  cordisValue,
  cordisExpressions,
} from '../../services/contracts/scripts/unified-cordis-fixtures.mjs';
const fixtureIndex = read('resource/cordis-upgrade-fixture.json');
for (const fixture of fixtureIndex.cases) {
  test('Cordis YAML preservation ' + fixture.id, () => {
    const text = (file) =>
      readFileSync(new URL('resource/' + file, root), 'utf8');
    const input = text(fixture.inputPath),
      expected = text(fixture.expectedPath);
    const result = mergeCordisFixture(
      input,
      fixture.upstreamPath ? text(fixture.upstreamPath) : null,
      fixtureIndex.fixedRootExpression,
    );
    assert.equal(result.shared, fixture.expectShared);
    if (fixture.expectShared && cordisValue(input).length)
      assert(result.text.startsWith(input));
    if (!fixture.expectShared) {
      assert.equal(result.text, input);
      assert.equal(result.text, expected);
      return;
    }
    assert.deepEqual(cordisValue(result.text), cordisValue(expected));
    assert.deepEqual(
      cordisExpressions(result.text),
      cordisExpressions(expected),
    );
    const repeat = mergeCordisFixture(
      result.text,
      null,
      fixtureIndex.fixedRootExpression,
    );
    assert.equal(repeat.text, result.text);
  });
}

test('all Unified mutations require If-Match and advertise success ETags', () => {
  const spec = read('openapi/resource.yaml');
  for (const methods of Object.values(spec.paths))
    for (const [method, operation] of Object.entries(methods)) {
      if (
        !operation.operationId?.startsWith('Unified') ||
        !['post', 'put', 'patch', 'delete'].includes(method)
      )
        continue;
      assert(
        operation.parameters.some(
          (p) =>
            p.in === 'header' && p.name === 'If-Match' && p.required === true,
        ),
        operation.operationId,
      );
      assert(
        operation.parameters.some(
          (p) =>
            p.in === 'header' &&
            p.name === 'Idempotency-Key' &&
            p.required === true,
        ),
        operation.operationId,
      );
      for (const [status, response] of Object.entries(operation.responses))
        if (status.startsWith('2'))
          assert(response.headers?.ETag, operation.operationId);
    }
});
test('Unified distribution operations retain equivalent legacy privilege and step-up', () => {
  const spec = read('openapi/resource.yaml');
  for (const [next, method, old, oldMethod] of [
    [
      '/api/resource/v1/admin/catalog/distributions',
      'post',
      '/api/resource/v1/distributions',
      'post',
    ],
    [
      '/api/resource/v1/admin/catalog/distributions',
      'get',
      '/api/resource/v1/admin/distributions',
      'get',
    ],
    [
      '/api/resource/v1/admin/catalog/distributions/{distributionId}',
      'get',
      '/api/resource/v1/admin/distributions',
      'get',
    ],
  ]) {
    const current = spec.paths[next][method],
      baseline = spec.paths[old][oldMethod];
    assert.equal(current['x-permission'], baseline['x-permission']);
    assert.equal(
      current['x-step-up-max-age-seconds'],
      baseline['x-step-up-max-age-seconds'],
    );
    assert.equal(current['x-step-up-max-age-seconds'], 600);
    assert(current['x-error-codes'].includes('iam.step_up_required'));
  }
});
test('pagination binds member client and exposes limits and cursors', () => {
  const spec = read('openapi/resource.yaml');
  for (const path of [
    '/api/resource/v1/catalog/member/installations',
    '/api/resource/v1/admin/catalog/distributions',
  ]) {
    const params = spec.paths[path].get.parameters;
    for (const name of ['limit', 'cursor'])
      assert(params.some((p) => p.name === name));
    if (path.includes('/member/'))
      assert(params.some((p) => p.name === 'clientId' && p.required));
  }
});
test('same-origin locators and member report source reject hostile inputs', () => {
  const schema = read('resource/unified.schema.json').$defs;
  for (const field of [
    schema.UnifiedArtifactDownload.properties.downloadPath,
    schema.UnifiedResourceRef.properties.manifestDownloadPath,
  ]) {
    const validate = ajv.compile(field);
    assert(
      validate(
        '/internal/resource/v1/catalog/downloads/019a0000-0000-7000-8000-000000000001/manifest',
      ),
    );
    for (const path of [
      'https://other.example.com/manifest',
      '//other.example.com/manifest',
      '/api/resource/v1/catalog/../secret',
      '/internal/resource/v1/catalog/downloads/%2e%2e/manifest',
    ])
      assert(!validate(path));
  }
  assert.deepEqual(
    schema.UnifiedMemberInstallationReport.properties.reportSource,
    { type: 'string', const: 'member' },
  );
});
const transitions = read('resource/guardian-transitions.json');
test('guardian transition table covers all states and guards recovery', () => {
  assert.equal(transitions.default, 'reject');
  for (const state of transitions.states)
    assert(
      transitions.transitions.some(
        (t) =>
          t.from === state && t.event === 'restart' && t.to === 'verifying',
      ),
    );
  const entries = new Set();
  for (const transition of transitions.transitions) {
    const key =
      transition.from +
      '/' +
      transition.event +
      '/' +
      transition.guards.join(',');
    assert(!entries.has(key));
    entries.add(key);
    assert(transitions.states.includes(transition.to));
    if (transition.to === 'active')
      assert(transition.guards.includes('leaseValid'));
    if (transition.to === 'private_only' && transition.from !== 'private_only')
      assert(
        transition.guards.includes('processStopped') &&
          transition.guards.includes('viewCleared') &&
          transition.guards.includes('disableReceiptDurable'),
      );
  }
  assert.equal(transitions.clock.rollbackToleranceMillis, 120000);
  assert(
    transitions.transitions
      .find((t) => t.from === 'verifying' && t.to === 'active')
      .guards.includes('noPendingIsolation'),
  );
});

for (const fixture of read('resource/local-file-fixtures.json').cases) {
  test('local file schema ' + fixture.id, () =>
    assert.equal(
      check('unified', fixture.schema, fixture.input),
      fixture.expect,
    ),
  );
}

import { evaluateHostLock } from '../../services/contracts/scripts/unified-host-lock-fixtures.mjs';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { join, dirname, resolve } from 'node:path';
import { tmpdir } from 'node:os';
const locks = read('resource/host-lock-fixtures.json');
for (const vector of locks.cases) {
  test('directory lock policy ' + vector.id, () =>
    assert.deepEqual(evaluateHostLock(vector.input), vector.expect),
  );
}
test('Node mkdir arbitrates concurrent C/E processes and releases only after owner cleanup', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'p1k-lock-'));
  const path = join(directory, 'host.lock.d');
  assert.equal(dirname(directory), resolve(tmpdir()));
  assert.equal(dirname(path), directory);
  try {
    const source =
      "const {mkdir,rmdir}=require('node:fs/promises');let held;process.on('message',async p=>{try{if(p==='release'){if(!held)throw Error('not owner');await rmdir(held);held=undefined;process.send('released')}else{await mkdir(p);held=p;process.send('acquired')}}catch(e){process.send(e.code??e.message)}});process.send('ready')";
    const contenders = locks.concurrentAcquire.contenders.map(() =>
      spawn(process.execPath, ['-e', source], {
        windowsHide: true,
        stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
      }),
    );
    try {
      await Promise.all(
        contenders.map(
          (c) =>
            new Promise((resolve, reject) => {
              c.once('message', (m) =>
                m === 'ready' ? resolve() : reject(new Error(m)),
              );
              c.once('error', reject);
            }),
        ),
      );
      const results = await Promise.all(
        contenders.map(
          (c) =>
            new Promise((resolve, reject) => {
              c.once('message', resolve);
              c.once('error', reject);
              c.send(path);
            }),
        ),
      );
      assert.equal(
        results.filter((x) => x === 'acquired').length,
        locks.concurrentAcquire.expect.winners,
      );
      assert.equal(
        results.filter((x) => x === locks.concurrentAcquire.expect.loserError)
          .length,
        locks.concurrentAcquire.expect.losers,
      );
      const winner = contenders[results.indexOf('acquired')];
      const released = new Promise((resolve) =>
        winner.once('message', resolve),
      );
      winner.send('release');
      assert.equal(await released, 'released');
      await mkdir(path);
    } finally {
      await Promise.all(
        contenders.map(
          (c) =>
            new Promise((resolve) => {
              if (c.exitCode !== null) return resolve();
              c.once('exit', resolve);
              c.kill();
            }),
        ),
      );
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

for (const fixture of transitions.cases) {
  test('guardian recovery guard ' + fixture.id, () => {
    const matched = transitions.transitions.filter(
      (t) =>
        t.from === fixture.from &&
        t.event === fixture.event &&
        t.guards.every((g) => fixture.facts[g] === true),
    );
    assert.equal(
      matched.length === 1 ? matched[0].to : 'reject',
      fixture.expect,
    );
  });
}

import './security-review.test.mjs';

import './installation-revision.test.mjs';

test('resource refs carry positive safe installation revisions with additive transport compatibility', () => {
  const value = read('resource/installation-revision-fixtures.json').cases[0]
    .input.expected.resource;
  assert(check('unified', 'UnifiedResourceRef', value));
  for (const installationRevision of [0, -1, 1.5, 9007199254740992])
    assert(
      !check('unified', 'UnifiedResourceRef', {
        ...value,
        installationRevision,
      }),
    );
  const { installationRevision, ...legacy } = value;
  assert.equal(installationRevision, 4);
  assert(check('unified', 'UnifiedResourceRef', legacy));
  for (const file of ['resource', 'workspace']) {
    const spec = parseYaml(
      readFileSync(new URL('openapi/' + file + '.yaml', root), 'utf8'),
    );
    assert.equal(
      spec.components.schemas.UnifiedResourceRef.$ref,
      '../resource/unified.schema.json#/$defs/UnifiedResourceRef',
    );
  }
});
