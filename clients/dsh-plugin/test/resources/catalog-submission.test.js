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
  let manifestDigest;
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
