import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { mkdtemp, readFile, writeFile, mkdir, rm, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { collectRuntimeProofs, imageParts, maxProofBytes, strictJSON, verifyProof } from '../../deploy/compose/scripts/collect-runtime-proofs.mjs';

const hash = raw => `sha256:${createHash('sha256').update(raw).digest('hex')}`;
const runFile = promisify(execFile);
function fixture(mode = 'oci', config = Buffer.from('{\n "os": "linux", "architecture":"amd64"\n}')) {
  const media = mode === 'docker' ? 'application/vnd.docker.distribution.manifest.v2+json' : 'application/vnd.oci.image.manifest.v1+json';
  const type = mode === 'docker' ? 'application/vnd.docker.container.image.v1+json' : 'application/vnd.oci.image.config.v1+json';
  const m = Buffer.from(JSON.stringify({ schemaVersion: 2, mediaType: media, config: { mediaType: type, digest: hash(config), size: config.length }, layers: [] })+'\n');
  return { m, c: config, media, image: `127.0.0.1:5000/auriga/fake-dsh@${hash(m)}` };
}
async function registry(t, f, mutate = () => {}) {
  const server = createServer((req, res) => {
    const manifest = req.url.includes('/manifests/');
    const response = { status: 200, headers: { 'content-type': manifest ? f.media : 'application/octet-stream' }, raw: manifest ? f.m : f.c };
    mutate(response, manifest, req);
    res.writeHead(response.status, response.headers); res.end(response.raw);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  return `http://127.0.0.1:${server.address().port}`;
}
async function output(t) { const root = await mkdtemp(join(tmpdir(), 'auriga-proof-test-')); t.after(() => rm(root, { recursive: true, force: true })); return root; }

for (const mode of ['oci', 'docker']) test(`${mode} preserves exact registry bytes and reuses read-only`, async t => {
  const f = fixture(mode), origin = await registry(t, f), root = await output(t);
  const dest = await collectRuntimeProofs(f.image, root, origin);
  assert.deepEqual(await readFile(join(dest,'manifest.json')),f.m);
  assert.deepEqual(await readFile(join(dest,'config.json')),f.c);
  assert.equal(await collectRuntimeProofs(f.image,root,origin),dest);
});

for (const failure of ['manifest digest', 'config digest', 'oversize manifest', 'oversize config', 'redirect manifest', 'redirect config', 'manifest media', 'compressed', 'index', 'duplicate', 'descriptor urls', 'platform', 'size']) {
  test(`rejects ${failure} before publishing proof`, async t => {
    let f = fixture();
    if (['index', 'duplicate', 'descriptor urls', 'size'].includes(failure)) {
      let m = strictJSON(f.m);
      if (failure === 'index') m = { schemaVersion: 2, mediaType: 'application/vnd.oci.image.index.v1+json', manifests: [] };
      if (failure === 'descriptor urls') m.config.urls = ['http://example.com/config'];
      if (failure === 'size') m.config.size++;
      f.m = Buffer.from(JSON.stringify(m));
      if (failure === 'duplicate') f.m = Buffer.from(f.m.toString().replace('"schemaVersion":2', '"schemaVersion":2,"schemaVersion":2'));
      f.image = `127.0.0.1:5000/auriga/fake-dsh@${hash(f.m)}`;
    }
    if (failure === 'platform') f = fixture('oci',Buffer.from('{"os":"windows","architecture":"amd64"}'));
    const origin = await registry(t,f,(r,isM) => {
      if ((failure === 'manifest digest' && isM) || (failure === 'config digest' && !isM)) r.raw = Buffer.concat([r.raw,Buffer.from(' ')]);
      if ((failure === 'oversize manifest' && isM) || (failure === 'oversize config' && !isM)) r.raw = Buffer.alloc(maxProofBytes+1,32);
      if ((failure === 'redirect manifest' && isM) || (failure === 'redirect config' && !isM)) { r.status=302; r.headers.location='http://127.0.0.1:1/never'; }
      if (failure === 'manifest media' && isM) r.headers['content-type']='application/json';
      if (failure === 'compressed') r.headers['content-encoding']='gzip';
    });
    const root = await output(t);
    await assert.rejects(collectRuntimeProofs(f.image,root,origin));
    assert.deepEqual(await readdir(root),[]);
  });
}

test('damaged and incomplete existing proofs are never repaired', async t => {
  const f=fixture(), origin=await registry(t,f), root=await output(t);
  const dest=await collectRuntimeProofs(f.image,root,origin);
  await writeFile(join(dest,'config.json'),'tampered');
  await assert.rejects(collectRuntimeProofs(f.image,root,origin));
  assert.equal(await readFile(join(dest,'config.json'),'utf8'),'tampered');
  await rm(join(dest,'config.json'));
  await assert.rejects(collectRuntimeProofs(f.image,root,origin));
  assert.deepEqual(await readdir(dest),['manifest.json']);
  await mkdir(join(dest,'unexpected'));
  await assert.rejects(collectRuntimeProofs(f.image,root,origin));
});

test('rejects arbitrary registry/repository and duplicate nested/escaped keys', () => {
  const f=fixture();
  for (const image of [f.image.replace('127.0.0.1','localhost'),f.image.replace('fake-dsh','other'),f.image.replace('@sha256:',':tag@sha256:')]) assert.throws(() => verifyProof(image,f.m,f.c));
  for (const raw of ['{"a":1,"\\u0061":2}','{"nested":[{"a":1,"a":2}]}','{} {}','{"x":"\\ud800"}','{"x":"\\udc00"}']) assert.throws(() => strictJSON(Buffer.from(raw)));
});

test('JSON depth counts containers including the top-level object, at 32/33 boundary', () => {
  const raw = depth => Buffer.from('{"x":'+'['.repeat(depth-1)+'0'+']'.repeat(depth-1)+'}');
  assert.doesNotThrow(() => strictJSON(raw(32)));
  assert.throws(() => strictJSON(raw(33)), /nesting limit/);
});

// Context boundaries follow the actual VerifyOCI/Moby field tables. These are
// independent input documents, rather than assertions about producer source.
const structuralCases = [
  ['manifest', 'SchemaVersion', f => { const m = strictJSON(f.m); m.SchemaVersion = 2; return { m }; }],
  ['descriptor', 'MediaType', f => { const m = strictJSON(f.m); m.config.MediaType = m.config.mediaType; return { m }; }],
  ['layer', 'ſize', f => { const m = strictJSON(f.m); m.layers = [{ mediaType: 'application/vnd.oci.image.layer.v1.tar', digest: hash(Buffer.from('layer')), size: 5, 'ſize': 5 }]; return { m }; }],
  ['config', 'OS', () => ({ c: { OS: 'windows' } })],
  ['config', 'oſ', () => ({ c: { 'oſ': 'windows' } })],
  ['config', 'Docker_Version', () => ({ c: { Docker_Version: 'test' } })],
  ['rootfs', 'Type', () => ({ c: { rootfs: { Type: 'layers', type: 'layers', diff_ids: [] } } })],
  ['history', 'Empty_Layer', () => ({ c: { history: [{ Empty_Layer: true, empty_layer: true }] } })],
  ['execution', 'workingdir', () => ({ c: { config: { workingdir: '/', WorkingDir: '/' } } })],
  ['execution', 'healthchecK', () => ({ c: { config: { 'healthchecK': { Test: ['NONE'] }, Healthcheck: { Test: ['NONE'] } } } })],
  ['healthcheck', 'startinterval', () => ({ c: { config: { Healthcheck: { startinterval: 1, StartInterval: 1 } } } })],
  ['historical config', 'attachſtdin', () => ({ c: { container_config: { 'attachſtdin': false, AttachStdin: false } } })],
  ['historical config', 'macaddress', () => ({ c: { container_config: { macaddress: '', MacAddress: '' } } })],
  ['historical healthcheck', 'RETRIES', () => ({ c: { container_config: { Healthcheck: { Retries: 1, RETRIES: 1 } } } })],
];

for (const [context, alias, mutate] of structuralCases) {
  for (const order of ['before', 'after']) test(`${context} rejects structural alias ${alias} ${order} canonical members before publishing`, async t => {
    let f = fixture('oci', Buffer.from('{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":[]},"config":{}}'));
    const change = mutate(f);
    if (change.c) {
      const config = { ...strictJSON(f.c), ...change.c };
      // Reverse each object's own insertion order without altering any key/value.
      const reverse = v => Array.isArray(v) ? v.map(reverse) : v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).reverse().map(([k, value]) => [k, reverse(value)])) : v;
      f = fixture('oci', Buffer.from(JSON.stringify(order === 'before' ? reverse(config) : config)));
    }
    if (change.m) {
      if (order === 'before') {
        const reverse = v => Array.isArray(v) ? v.map(reverse) : v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).reverse().map(([k, value]) => [k, reverse(value)])) : v;
        change.m = reverse(change.m);
      }
      f.m = Buffer.from(JSON.stringify(change.m)); f.image = `127.0.0.1:5000/auriga/fake-dsh@${hash(f.m)}`;
    }
    const origin = await registry(t, f), root = await output(t);
    await assert.rejects(collectRuntimeProofs(f.image, root, origin), /noncanonical structural JSON member/);
    assert.deepEqual(await readdir(root), []);
  });
}

test('structural checks preserve exact user map keys and unrelated metadata extensions', async t => {
  const config = {
    os: 'linux', architecture: 'amd64', rootfs: { type: 'layers', diff_ids: [] },
    config: { Labels: { OS: 'windows', 'oſ': 'linux', 'Key': 'user' }, ExposedPorts: { OS: {}, 'oſ': {} }, Volumes: { '/OS': {}, '/oſ': {} } },
    container_config: { Labels: { OS: 'user' }, Volumes: { OS: {} }, ExposedPorts: { OS: {} }, future: { OS: 'extension' } },
    future: { OS: 'extension' }, 'İd': 'unrelated extension',
  };
  const f = fixture('oci', Buffer.from(JSON.stringify(config)));
  const manifest = strictJSON(f.m); manifest.annotations = { OS: 'user', 'oſ': 'user' }; manifest.config.annotations = { Size: 'user' }; manifest.future = { OS: 'extension' };
  f.m = Buffer.from(JSON.stringify(manifest)); f.image = `127.0.0.1:5000/auriga/fake-dsh@${hash(f.m)}`;
  const origin = await registry(t, f), root = await output(t);
  const directory = await collectRuntimeProofs(f.image, root, origin);
  assert.deepEqual(await readFile(join(directory, 'manifest.json')), f.m);
  assert.deepEqual(await readFile(join(directory, 'config.json')), f.c);
});

test('explicit approved repository fetches exact raw M/C from its random loopback port', async t => {
  const f = fixture();
  const origin = await registry(t, f, (_response, _manifest, request) => {
    assert.match(request.url, /^\/v2\/team\/runtime\/(?:manifests|blobs)\/sha256:[a-f0-9]{64}$/);
  });
  const repository = `${origin.slice('http://'.length)}/team/runtime`;
  const image = `${repository}@${hash(f.m)}`;
  const root = await output(t);
  const destination = await collectRuntimeProofs(image, root, undefined, repository);
  assert.deepEqual(await readFile(join(destination, 'manifest.json')), f.m);
  assert.deepEqual(await readFile(join(destination, 'config.json')), f.c);
  assert.equal(await collectRuntimeProofs(image, root, undefined, repository), destination);
  assert.equal(imageParts(image, repository).repository, 'team/runtime');
  assert.doesNotThrow(() => verifyProof(image, f.m, f.c, repository));
  assert.throws(() => imageParts(image));
});

test('explicit repository rejects wrong approval, wrong M and origin before any output', async t => {
  const f = fixture(), origin = await registry(t, f);
  const repository = `${origin.slice('http://'.length)}/team/runtime`;
  const root = await output(t);
  for (const [image, approved, override] of [
    [`${repository}@${hash(f.m)}`, `${origin.slice('http://'.length)}/team/other`, undefined],
    [`${repository}@sha256:${'a'.repeat(64)}`, repository, undefined],
    [`${repository}@sha256:${'A'.repeat(64)}`, repository, undefined],
    [`${repository}@${hash(f.m)}`, repository, 'http://127.0.0.1:1'],
  ]) {
    await assert.rejects(collectRuntimeProofs(image, root, override, approved));
    assert.deepEqual(await readdir(root), []);
  }
  assert.throws(() => verifyProof(`${repository}@${hash(f.m)}`, f.m, f.c));
});

test('collector CLI derives the only network origin from approved repository', async t => {
  const f = fixture(), origin = await registry(t, f);
  const repository = `${origin.slice('http://'.length)}/team/runtime`;
  const image = `${repository}@${hash(f.m)}`;
  const root = await output(t);
  const script = fileURLToPath(new URL('../../deploy/compose/scripts/collect-runtime-proofs.mjs', import.meta.url));
  await runFile(process.execPath, [script, image, root, '--repository', repository]);
  assert.deepEqual(await readFile(join(root, hash(f.m).slice(7), 'manifest.json')), f.m);
  const rejectedRoot = await output(t);
  await assert.rejects(runFile(process.execPath, [script, image, rejectedRoot, '--repository', `${origin.slice('http://'.length)}/team/other`]));
  assert.deepEqual(await readdir(rejectedRoot), []);
});
