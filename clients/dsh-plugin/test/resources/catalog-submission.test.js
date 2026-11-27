import { test } from 'node:test';
import assert from 'node:assert/strict';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { createPrivateSkills } from '../../src/resources/private-skills.js';
import { unpackSkill } from '../../src/resources/skill-package.js';
import { sha } from '../../src/resources/desktop-skills.js';
test('submission uploads the exact frozen private snapshot and its declarations, independent of later local edits', async (t) => {
  const f = catalogFixture(t),
    privateSkills = createPrivateSkills({ home: f.home });
  const source =
    '---\nname: example\ndescription: "Public fixture"\n---\nBEFORE FREEZE';
  let row = await privateSkills.create(source);
  const frozen = await privateSkills.change({ ...row, action: 'freeze' });
  await privateSkills.change({
    ...row,
    action: 'edit',
    source: source.replace('BEFORE FREEZE', 'LATER PRIVATE EDIT'),
  });
  const requests = [];
  let manifestDigest, uploadedManifest;
  const pack = {
    id: f.packageId,
    kind: 'skill',
    tenantId: f.who.tenantId,
    ownerId: f.who.ownerId,
    name: 'example',
    revision: 1,
  };
  const fetchImpl = async (url, options) => {
    requests.push({ path: url.pathname, options });
    const base = '/api/resource/v1/catalog/packages';
    if (url.pathname === base && options.method === 'GET')
      return Response.json({
        items: [
          pack,
          {
            ...pack,
            id: '019a0000-0000-7000-8000-000000000099',
            ownerId: '019a0000-0000-7000-8000-000000000098',
          },
        ],
      });
    if (url.pathname === base && options.method === 'POST')
      return Response.json(pack, { status: 201, headers: { etag: '"1"' } });
    if (url.pathname === base + '/' + f.packageId)
      return Response.json(pack, { headers: { etag: '"1"' } });
    if (url.pathname === base + '/' + f.packageId + '/versions') {
      assert.equal(options.headers['if-match'], '"1"');
      assert.equal(options.headers['content-type'], undefined);
      const bytes = Buffer.from(
          await options.body.get('manifest').arrayBuffer(),
        ),
        manifest = JSON.parse(bytes),
        metadata = JSON.parse(await options.body.get('metadata').text());
      manifestDigest = sha(bytes);
      uploadedManifest = manifest;
      assert.equal(metadata.expectedDigest, manifestDigest);
      assert.deepEqual(manifest.permissions, {
        tools: ['terminal.run'],
        outboundHosts: ['example.com'],
        dataAccess: ['workspace_read'],
      });
      const archive = Buffer.from(
          await options.body.get('artifacts').arrayBuffer(),
        ),
        files = unpackSkill(archive, manifest);
      assert.match(files['SKILL.md'].toString(), /BEFORE FREEZE/);
      assert.doesNotMatch(files['SKILL.md'].toString(), /LATER PRIVATE/);
      assert.equal(manifest.artifacts[0].digest, sha(archive));
      return Response.json(
        {
          id: f.versionId,
          packageId: f.packageId,
          digest: manifestDigest,
          manifest,
          kind: 'skill',
          version: '1.0.0',
          state: 'draft',
          revision: 2,
        },
        { status: 201, headers: { etag: '"2"' } },
      );
    }
    if (url.pathname.endsWith('/submissions')) {
      assert.equal(options.headers['if-match'], '"2"');
      assert.deepEqual(JSON.parse(options.body), {
        reason: 'Review commands and outbound access',
      });
      return Response.json(
        {
          reviewId: '019a0000-0000-7000-8000-000000000055',
          authorId: f.who.ownerId,
          status: 'pending',
          version: {
            id: f.versionId,
            packageId: f.packageId,
            digest: manifestDigest,
            manifest: uploadedManifest,
            kind: 'skill',
            version: '1.0.0',
            state: 'pending_review',
          },
        },
        { status: 201 },
      );
    }
    throw new Error('unexpected submission path');
  };
  const s = catalogService({ ...f, privateSkills, fetchImpl, host: {} });
  assert.equal(requests.length, 0);
  assert.equal((await s.targets(frozen.snapshotId)).items.length, 1);
  const input = {
    snapshotId: frozen.snapshotId,
    version: '1.0.0',
    license: 'MIT',
    author: 'Example',
    reason: 'Review commands and outbound access',
    permissions: {
      tools: ['terminal.run'],
      outboundHosts: ['example.com'],
      dataAccess: ['workspace_read'],
    },
    key: 'frozen-submit',
  };
  const result = await s.submit(input);
  assert.equal(result.manifestDigest, manifestDigest);
  assert.equal(result.state, 'pending_review');
  const creates = requests.filter(
    (r) =>
      r.path === '/api/resource/v1/catalog/packages' &&
      r.options.method === 'POST',
  ).length;
  await s.submit({ ...input, packageId: f.packageId, key: 'existing-package' });
  assert.equal(
    requests.filter(
      (r) =>
        r.path === '/api/resource/v1/catalog/packages' &&
        r.options.method === 'POST',
    ).length,
    creates,
  );
  row = await privateSkills.read('example', true);
  assert.match(row.files['SKILL.md'], /LATER PRIVATE EDIT/);
});

test('prepared plugin submission replays attach with its initial ETag and fresh upload refs', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const base = '/api/resource/v1/catalog/packages/' + f.packageId;
  const pack = {
    id: f.packageId,
    kind: 'plugin',
    tenantId: f.who.tenantId,
    ownerId: f.who.ownerId,
    name: f.manifest.name,
    revision: 1,
  };
  const version = {
    id: f.versionId,
    packageId: f.packageId,
    kind: 'plugin',
    version: f.manifest.version,
    state: 'draft',
    revision: 1,
    digest: sha(Buffer.from(JSON.stringify(f.manifest))),
    manifest: f.manifest,
  };
  const submitKeys = [];
  let uploadAttempts = 0;
  const s = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    fetchImpl: async (url, options) => {
      if (url.pathname === base)
        return Response.json(pack, {
          headers: { etag: '"' + pack.revision + '"' },
        });
      if (url.pathname === base + '/versions') {
        assert.equal(options.headers['idempotency-key'], 'attachv');
        assert.equal(options.headers['if-match'], '"1"');
        assert.deepEqual(
          JSON.parse(await options.body.get('metadata').text()),
          {
            version: f.manifest.version,
            expectedDigest: version.digest,
          },
        );
        assert.deepEqual(
          Buffer.from(await options.body.get('artifacts').arrayBuffer()),
          f.archive,
        );
        if (++uploadAttempts === 1) {
          pack.revision++;
          return Response.json({}, { status: 503 });
        }
        return Response.json(version, {
          status: 201,
          headers: { etag: '"1"' },
        });
      }
      assert.equal(
        url.pathname,
        base + '/versions/' + f.versionId + '/submissions',
      );
      assert.equal(options.headers['if-match'], '"1"');
      submitKeys.push(options.headers['idempotency-key']);
      return Response.json(
        {
          authorId: f.who.ownerId,
          reviewId: '019a0000-0000-7000-8000-000000000055',
          status: 'pending',
          version: { ...version, state: 'pending_review' },
        },
        { status: 201 },
      );
    },
  });
  const input = {
    manifest: f.manifest,
    archive: f.archive,
    packageId: f.packageId,
    key: 'attach',
    reason: 'review prepared plugin',
  };
  await assert.rejects(
    s.submitPrepared({ ...input, archive: Buffer.from('modified') }),
    {
      code: 'resource.digest_mismatch',
    },
  );
  await assert.rejects(s.submitPrepared(input), { code: 'common.unavailable' });
  await assert.rejects(
    s.submitPrepared({ ...input, reason: 'changed intent' }),
    {
      code: 'common.conflict',
    },
  );
  await assert.rejects(s.submitPrepared({ ...input, packageId: f.versionId }), {
    code: 'common.conflict',
  });
  const result = await s.submitPrepared(input);
  assert.deepEqual(result, {
    packageId: f.packageId,
    versionId: f.versionId,
    state: 'pending_review',
    digest: input.manifest.artifacts[0].digest,
    manifestDigest: version.digest,
  });
  assert.equal(uploadAttempts, 2);
  assert.deepEqual(submitKeys, ['attachs']);
});

test('prepared submission fences a device generation change during broker staging', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  let generation = 1;
  f.runtime.catalogGeneration = () => generation;
  const calls = [];
  const s = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    fetchImpl: async (url, options) => {
      calls.push([url.pathname, options.method]);
      return Response.json(
        {
          id: f.packageId,
          ownerId: f.who.ownerId,
          tenantId: f.who.tenantId,
          kind: 'plugin',
          name: f.manifest.name,
          revision: 1,
        },
        { status: 201, headers: { etag: '"1"' } },
      );
    },
  });
  const stage = f.runtime.upload;
  f.runtime.upload = async (bytes, type) => {
    const ref = await stage(bytes, type);
    generation++;
    return ref;
  };
  await assert.rejects(
    s.submitPrepared({
      manifest: f.manifest,
      archive: f.archive,
      key: 'new',
      reason: 'review',
    }),
    { code: 'common.unauthenticated' },
  );
  assert.deepEqual(calls, [['/api/resource/v1/catalog/packages', 'POST']]);
});

for (const loseDuring of [
  'queue entry',
  'multipart staging',
  'upload response',
]) {
  test(
    'prepared submission stops when trusted execution ends at ' + loseDuring,
    async (t) => {
      const f = catalogFixture(t, undefined, { kind: 'plugin' });
      const requests = [];
      let active = loseDuring !== 'queue entry';
      const s = catalogService({
        ...f,
        privateSkills: {},
        host: {},
        fetchImpl: async (url) => {
          requests.push(url.pathname);
          if (url.pathname.endsWith('/versions')) {
            active = false;
            return Response.json({}, { status: 201 });
          }
          return Response.json(
            {
              id: f.packageId,
              ownerId: f.who.ownerId,
              tenantId: f.who.tenantId,
              kind: 'plugin',
              name: f.manifest.name,
              revision: 1,
            },
            { status: 201, headers: { etag: '"1"' } },
          );
        },
      });
      const upload = f.runtime.upload;
      let staged = 0;
      f.runtime.upload = async (bytes, type) => {
        const ref = await upload(bytes, type);
        if (++staged === 2 && loseDuring === 'multipart staging')
          active = false;
        return ref;
      };
      await assert.rejects(
        s.submitPrepared(
          {
            manifest: f.manifest,
            archive: f.archive,
            key: 'cancelled',
            reason: 'review',
          },
          async () => {
            await Promise.resolve();
            if (!active)
              throw Object.assign(new Error('execution ended'), {
                code: 'common.forbidden',
              });
          },
        ),
        { code: 'common.forbidden' },
      );
      const base = '/api/resource/v1/catalog/packages';
      assert.deepEqual(
        requests,
        loseDuring === 'queue entry'
          ? []
          : loseDuring === 'multipart staging'
            ? [base]
            : [base, base + '/' + f.packageId + '/versions'],
      );
    },
  );
}

test('new package upload refreshes its verified package after a definite 412', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const base = '/api/resource/v1/catalog/packages';
  const packagePath = base + '/' + f.packageId;
  const pack = {
    id: f.packageId,
    ownerId: f.who.ownerId,
    tenantId: f.who.tenantId,
    kind: 'plugin',
    name: f.manifest.name,
  };
  const version = {
    id: f.versionId,
    packageId: f.packageId,
    kind: 'plugin',
    version: f.manifest.version,
    revision: 1,
    state: 'draft',
    digest: sha(Buffer.from(JSON.stringify(f.manifest))),
    manifest: f.manifest,
  };
  const requests = [];
  const s = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    fetchImpl: async (url, options) => {
      const path = url.pathname;
      const etag = options.headers['if-match'];
      requests.push([path, options.method, etag]);
      if (path === base)
        return Response.json({ ...pack, revision: 1 }, { status: 201 });
      if (path === packagePath) return Response.json({ ...pack, revision: 2 });
      if (path.endsWith('/versions'))
        return etag === '"1"'
          ? Response.json({}, { status: 412 })
          : Response.json(version, { status: 201 });
      return Response.json(
        {
          authorId: f.who.ownerId,
          reviewId: '019a0000-0000-7000-8000-000000000055',
          status: 'pending',
          version: { ...version, state: 'pending_review' },
        },
        { status: 201 },
      );
    },
  });
  const input = {
    manifest: f.manifest,
    archive: f.archive,
    key: 'fresh',
    reason: 'review',
  };
  await assert.rejects(s.submitPrepared(input), {
    code: 'common.precondition_failed',
  });
  assert.equal((await s.submitPrepared(input)).state, 'pending_review');
  assert.deepEqual(requests, [
    [base, 'POST', '"0"'],
    [packagePath + '/versions', 'POST', '"1"'],
    [packagePath, 'GET', undefined],
    [packagePath + '/versions', 'POST', '"2"'],
    [packagePath + '/versions/' + f.versionId + '/submissions', 'POST', '"1"'],
  ]);
});

test('prepared submission rejects missing, non-payload or conflicting payload digest in upload and submit receipts', async (t) => {
  for (const mode of [
    'upload-missing',
    'upload-conflict',
    'upload-role',
    'submit-conflict',
  ]) {
    const f = catalogFixture(t, undefined, { kind: 'plugin' });
    const base = '/api/resource/v1/catalog/packages';
    const pack = {
      id: f.packageId,
      ownerId: f.who.ownerId,
      tenantId: f.who.tenantId,
      kind: 'plugin',
      name: f.manifest.name,
      revision: 1,
    };
    const goodVersion = {
      id: f.versionId,
      packageId: f.packageId,
      kind: 'plugin',
      version: f.manifest.version,
      revision: 1,
      state: 'draft',
      digest: sha(Buffer.from(JSON.stringify(f.manifest))),
      manifest: f.manifest,
    };
    const badVersion = {
      ...goodVersion,
      ...(mode === 'upload-missing' ? { manifest: undefined } : {}),
      ...(mode === 'upload-conflict' || mode === 'submit-conflict'
        ? {
            manifest: {
              ...f.manifest,
              artifacts: [
                {
                  ...f.manifest.artifacts[0],
                  digest: 'sha256:' + 'f'.repeat(64),
                },
              ],
            },
          }
        : {}),
      ...(mode === 'upload-role'
        ? {
            manifest: {
              ...f.manifest,
              artifacts: [{ ...f.manifest.artifacts[0], role: 'signature' }],
            },
          }
        : {}),
    };
    const s = catalogService({
      ...f,
      privateSkills: {},
      host: {},
      fetchImpl: async (url) => {
        if (url.pathname === base) return Response.json(pack, { status: 201 });
        if (url.pathname.endsWith('/versions'))
          return Response.json(
            mode === 'submit-conflict' ? goodVersion : badVersion,
            { status: 201 },
          );
        return Response.json(
          {
            authorId: f.who.ownerId,
            reviewId: '019a0000-0000-7000-8000-000000000055',
            status: 'pending',
            version: { ...badVersion, state: 'pending_review' },
          },
          { status: 201 },
        );
      },
    });
    await assert.rejects(
      s.submitPrepared({
        manifest: f.manifest,
        archive: f.archive,
        key: 'bad-' + mode,
        reason: 'review',
      }),
      { code: 'resource.digest_mismatch' },
    );
  }
});
