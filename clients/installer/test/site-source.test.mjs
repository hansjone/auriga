import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  rmSync,
} from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import {
  ROOT,
  digest,
  sealInputs,
  verifyInputs,
} from '../../../scripts/client-release/inputs.mjs';
import { signInputs } from '../../../scripts/client-release/sign-inputs.mjs';
import { generateDesktopKeys } from '../../../scripts/client-release/keys.mjs';
import { buildPayload } from './helpers.mjs';
import { makeRelease } from '../scripts/make-release.mjs';
import {
  sourceIdentity,
  snapshotInventory,
  assertBuildSourceClean,
} from '../../../scripts/client-release/source.mjs';
import { verifySignedInventory } from '../../../scripts/client-release/trust.mjs';
import { parseMetadataJson } from '../../../scripts/client-release/trust.mjs';

test('signed Windows addition shares PR373 issuer trust and rejects metadata tampering', (t) => {
  const root = mkdtempSync(join(ROOT, 'clients/installer/target/addon-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const kit = join(root, 'kit');
  mkdirSync(join(kit, 'build-node'), { recursive: true });
  writeFileSync(join(kit, 'build-node/node.exe'), 'test node');
  const m = sealInputs(kit, []);
  const keys = join(root, 'issuer');
  generateDesktopKeys(keys);
  const issued = signInputs(kit, join(keys, 'desktop-private.pem'), '0.2.0', {
    checkTools: false,
  });
  assert.equal(issued.manifestSha256, m.manifestSha256);
  assert.equal(
    verifyInputs(kit, undefined, ROOT, {
      checkTools: false,
      verifyKey: join(keys, 'desktop-public.pem'),
    }).manifestSha256,
    m.manifestSha256,
  );
  assert.equal(
    verifyInputs(kit, undefined, ROOT, {
      checkTools: false,
      trustFingerprint: issued.issuerFingerprint,
    }).manifestSha256,
    m.manifestSha256,
  );
  writeFileSync(join(kit, 'client-inputs-release.json'), '{}');
  assert.throws(
    () =>
      verifyInputs(kit, undefined, ROOT, {
        checkTools: false,
        trustFingerprint: issued.issuerFingerprint,
      }),
    /摘要/,
  );
});
test('makeRelease records signed archive provenance without invoking Git', (t) => {
  const f = fixture(t);
  const source = sourceIdentity(f.root, { verifyKey: f.publicKey });
  const base = mkdtempSync(join(ROOT, 'clients/installer/target/record-test-'));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  const payload = buildPayload(base),
    archive = join(base, 'setup.exe');
  writeFileSync(archive, 'fixture');
  const previous = process.env.PATH;
  process.env.PATH = '';
  try {
    const record = makeRelease({
      payload: payload.dir,
      archive,
      exe: archive,
      output: base,
      sequence: 1,
      minimumVersion: '0.0.0',
      notAfter: new Date(Date.now() + 86400000).toISOString(),
      sourceInfo: source,
      siteOffline: { schema: 1 },
    });
    assert.equal(record.sourceCommit, 'a'.repeat(40));
    assert.equal(record.source.sourceType, 'signed-archive');
    assert.equal(record.siteChanges, false);
  } finally {
    process.env.PATH = previous;
  }
});
test('large source metadata permits thousands of files but rejects duplicate escaped keys and unsafe integers', () => {
  const files = Object.fromEntries(
    Array.from({ length: 1500 }, (_, i) => [
      `f${i}.js`,
      { sha256: 'a'.repeat(64), mode: 420 },
    ]),
  );
  assert.equal(
    Object.keys(parseMetadataJson(Buffer.from(JSON.stringify({ files }))).files)
      .length,
    1500,
  );
  assert.throws(
    () => parseMetadataJson(Buffer.from('{"files":{},"\\u0066iles":{}}')),
    /重复键/,
  );
  assert.throws(
    () => parseMetadataJson(Buffer.from('{"n":9007199254740993}')),
    /不安全/,
  );
});
function fixture(t, { ui = false } = {}) {
  const root = mkdtempSync(join(ROOT, 'clients/installer/target/source-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(join(root, 'main.go'), 'package example\n');
  writeFileSync(join(root, 'auriga'), '#!/bin/sh\n');
  const uiPath = 'clients/dsh-plugin/ui/panel.js';
  if (ui) {
    mkdirSync(join(root, 'clients/dsh-plugin/ui'), { recursive: true });
    writeFileSync(join(root, uiPath), 'baseline source');
  }
  const { files, sha256 } = snapshotInventory(root, ui ? { [uiPath]: {} } : {});
  const manifest = {
    schemaVersion: 1,
    source: {
      version: '1.0',
      upstreamVersion: '1.0',
      commit: 'a'.repeat(40),
      branch: 'main',
      dirty: false,
      siteChanges: false,
      snapshotSha256: sha256,
      builtAt: '2026-11-16T00:00:00Z',
    },
    tree: 'b'.repeat(40),
    locks: { digest: 'c'.repeat(64), files: {} },
    files,
  };
  writeFileSync(join(root, 'auriga-source.json'), JSON.stringify(manifest));
  const pair = generateKeyPairSync('ed25519');
  const publicKey = join(root, 'release-public.pem');
  writeFileSync(
    publicKey,
    pair.publicKey.export({ type: 'spki', format: 'pem' }),
  );
  const sums = `${digest(readFileSync(join(root, 'auriga-source.json')))}  auriga-source.json\n`;
  writeFileSync(join(root, 'SHA256SUMS'), sums);
  writeFileSync(
    join(root, 'SHA256SUMS.sig'),
    sign(null, Buffer.from(sums), pair.privateKey).toString('base64') + '\n',
  );
  return {
    root,
    publicKey,
    fingerprint: digest(pair.publicKey.export({ type: 'spki', format: 'der' })),
  };
}
mkdirSync(join(ROOT, 'clients/installer/target'), { recursive: true });
test('signed archive source works without Git, prefers signed baseline, tracks edits/additions/deletions and CRLF', (t) => {
  const f = fixture(t),
    trust = { verifyKey: f.publicKey };
  const original = sourceIdentity(f.root, trust);
  assert.equal(original.sourceType, 'signed-archive');
  assert.equal(original.sourceCommit, 'a'.repeat(40));
  assert.equal(original.siteChanges, false);
  mkdirSync(join(f.root, 'clients/dsh-plugin/ui'), { recursive: true });
  writeFileSync(
    join(f.root, 'clients/dsh-plugin/ui/auriga-team-panel.js'),
    'generated',
  );
  assert.equal(sourceIdentity(f.root, trust).siteChanges, false);
  mkdirSync(join(f.root, '.git'));
  writeFileSync(join(f.root, '.git/HEAD'), 'invalid git');
  writeFileSync(join(f.root, 'main.go'), 'package example\r\n');
  assert.equal(sourceIdentity(f.root, trust).siteChanges, false);
  writeFileSync(join(f.root, 'main.go'), 'package site\n');
  writeFileSync(join(f.root, 'added.txt'), 'new');
  rmSync(join(f.root, 'auriga'));
  const changed = sourceIdentity(f.root, trust);
  assert.equal(changed.siteChanges, true);
  assert.equal(changed.dirty, true);
  assert.notEqual(changed.sourceTreeSha256, original.sourceTreeSha256);
  assert.equal(changed.sourceBaselineSha256, original.sourceTreeSha256);
});
test('source manifest missing/tampered/unsigned/self-trusted/fingerprint mismatch is rejected', (t) => {
  const f = fixture(t);
  assert.throws(() => sourceIdentity(f.root), /批准|指纹/);
  assert.throws(
    () => sourceIdentity(f.root, { trustFingerprint: '0'.repeat(64) }),
    /指纹/,
  );
  assert.equal(
    sourceIdentity(f.root, { trustFingerprint: f.fingerprint })
      .sourceIssuerFingerprint,
    f.fingerprint,
  );
  const m = join(f.root, 'auriga-source.json'),
    bytes = readFileSync(m);
  writeFileSync(m, '{}');
  assert.throws(
    () => sourceIdentity(f.root, { verifyKey: f.publicKey }),
    /摘要/,
  );
  writeFileSync(m, bytes);
  writeFileSync(join(f.root, 'SHA256SUMS.sig'), 'bad');
  assert.throws(
    () => sourceIdentity(f.root, { verifyKey: f.publicKey }),
    /签名/,
  );
  rmSync(m);
  assert.throws(
    () => sourceIdentity(f.root, { verifyKey: f.publicKey }),
    /Git|源码/,
  );
});

test('build preflight refuses unrecorded UI, node_modules and dist even when provenance reports no site changes', (t) => {
  for (const path of [
    'clients/dsh-plugin/ui',
    'node_modules',
    'web/panel/node_modules',
    'dist',
    'clients/dsh-plugin/dist',
  ]) {
    const f = fixture(t);
    const trust = { verifyKey: f.publicKey };
    mkdirSync(join(f.root, path), { recursive: true });
    writeFileSync(join(f.root, path, 'preset.js'), 'unrecorded build input');
    assert.equal(sourceIdentity(f.root, trust).siteChanges, false);
    assert.throws(
      () => sourceIdentity(f.root, trust, { forBuild: true }),
      /预置生成内容/,
    );
    assert.throws(
      () => assertBuildSourceClean(f.root),
      (error) =>
        error.message.includes(path) &&
        /Remove-Item -LiteralPath/.test(error.message),
    );
  }
});

test('build preflight permits UI only when its files are included in the source baseline', (t) => {
  const f = fixture(t);
  assert.doesNotThrow(() => assertBuildSourceClean(f.root));
  assert.equal(
    sourceIdentity(f.root, { verifyKey: f.publicKey }, { forBuild: true })
      .siteChanges,
    false,
  );
  mkdirSync(join(f.root, 'clients/dsh-plugin/ui'), { recursive: true });
  writeFileSync(
    join(f.root, 'clients/dsh-plugin/ui/panel.js'),
    'baseline source',
  );
  assert.doesNotThrow(() =>
    assertBuildSourceClean(f.root, ['clients/dsh-plugin/ui/panel.js']),
  );
  const signedUI = fixture(t, { ui: true });
  assert.equal(
    sourceIdentity(
      signedUI.root,
      { verifyKey: signedUI.publicKey },
      { forBuild: true },
    ).siteChanges,
    false,
  );
});

test('Git build preflight rejects ignored generated inputs before a clean Git tree can produce a release', (t) => {
  const root = mkdtempSync(
    join(ROOT, 'clients/installer/target/git-source-test-'),
  );
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const git = (args) => {
    const result = spawnSync(
      'git',
      [
        '-c',
        'user.name=Fixture',
        '-c',
        'user.email=fixture@example.com',
        '-c',
        'commit.gpgsign=false',
        ...args,
      ],
      { cwd: root, encoding: 'utf8', windowsHide: true },
    );
    assert.equal(result.status, 0, result.stderr);
  };
  git(['init', '--quiet']);
  writeFileSync(
    join(root, '.gitignore'),
    'node_modules/\nclients/dsh-plugin/ui/\n',
  );
  git(['add', '.gitignore']);
  git([
    '-c',
    'core.hooksPath=.unused-hooks',
    'commit',
    '--quiet',
    '-m',
    'fixture',
  ]);
  assert.equal(sourceIdentity(root, {}, { forBuild: true }).siteChanges, false);
  for (const path of ['clients/dsh-plugin/ui', 'node_modules']) {
    mkdirSync(join(root, path), { recursive: true });
    writeFileSync(join(root, path, 'preset.js'), 'ignored input');
    assert.equal(sourceIdentity(root).siteChanges, false);
    assert.throws(
      () => sourceIdentity(root, {}, { forBuild: true }),
      /预置生成内容/,
    );
    rmSync(join(root, path), { recursive: true });
  }
});
test('SHA256SUMS verifier follows PR373 base64 Ed25519 signature and binds metadata bytes', (t) => {
  const f = fixture(t);
  const approved = verifySignedInventory(f.root, { verifyKey: f.publicKey });
  assert.equal(approved.verifyMetadata('auriga-source.json').schemaVersion, 1);
  writeFileSync(
    join(f.root, 'SHA256SUMS'),
    '0'.repeat(64) + '  auriga-source.json\n',
  );
  assert.throws(
    () => verifySignedInventory(f.root, { verifyKey: f.publicKey }),
    /签名/,
  );
});
