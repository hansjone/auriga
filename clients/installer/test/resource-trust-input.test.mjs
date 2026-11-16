import test from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash, generateKeyPairSync } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { tempBase } from './helpers.mjs';
import { build, validateResourceRoots } from '../scripts/build.mjs';
import { prepareResourceTrustInput } from '../scripts/resource-trust-input.mjs';

function roots() {
  return Object.fromEntries(
    ['release', 'revocation', 'downgrade'].map((role) => [
      role,
      {
        kid: `example-${role}`,
        publicKeySpkiBase64: generateKeyPairSync('ed25519')
          .publicKey.export({ type: 'spki', format: 'der' })
          .toString('base64'),
      },
    ]),
  );
}

test('CI input accepts JSON and base64 and preserves the no-input build', (t) => {
  const base = tempBase(t);
  const trust = roots();
  const json = JSON.stringify(trust);
  for (const [index, input] of [
    json,
    Buffer.from(json).toString('base64'),
  ].entries()) {
    const output = join(base, `roots-${index}.json`);
    assert.deepEqual(prepareResourceTrustInput(input, output), [
      '--resource-trust-json',
      output,
    ]);
    assert.equal(readFileSync(output, 'utf8'), json);
    assert.throws(() => prepareResourceTrustInput(input, output), /EEXIST/);
  }
  const unused = join(base, 'unused.json');
  assert.deepEqual(prepareResourceTrustInput('', unused), []);
  assert.equal(existsSync(unused), false);
});

test('CI input and builder reject malformed roots and public test keys before staging', async (t) => {
  const base = tempBase(t);
  const valid = roots();
  const fixtures = JSON.parse(
    readFileSync(
      new URL(
        '../../../contracts/resource/unified-vectors.json',
        import.meta.url,
      ),
    ),
  ).trust;
  const rsa = generateKeyPairSync('rsa', { modulusLength: 2048 })
    .publicKey.export({ type: 'spki', format: 'der' })
    .toString('base64');
  for (const invalid of [
    null,
    [],
    {},
    { ...valid, extra: {} },
    { ...valid, release: { ...valid.release, kid: 123 } },
    { ...valid, release: { ...valid.release, privateKey: 'forbidden' } },
    { ...valid, release: { ...valid.release, publicKeySpkiBase64: rsa } },
    {
      ...valid,
      release: {
        ...valid.release,
        publicKeySpkiBase64: valid.release.publicKeySpkiBase64 + '!',
      },
    },
    { ...valid, revocation: valid.release },
  ]) {
    assert.throws(() => validateResourceRoots(invalid));
    assert.throws(() =>
      prepareResourceTrustInput(
        JSON.stringify(invalid),
        join(base, 'invalid.json'),
      ),
    );
  }
  for (const role of Object.keys(fixtures)) {
    const invalid = {
      ...valid,
      [role]: { ...fixtures[role], kid: `renamed-${role}` },
    };
    assert.throws(() => validateResourceRoots(invalid), /test resource key/);
    const file = join(base, `${role}.json`);
    writeFileSync(file, JSON.stringify(invalid));
    const out = join(base, `stage-${role}`);
    await assert.rejects(
      build({ 'broker-artifact': 'unused', 'resource-trust-json': file, out }),
      /test resource key/,
    );
    assert.equal(existsSync(out), false);
  }
  for (const input of ['not json', 'e30=!', 'e30=', '{', ' '.repeat(17000)])
    assert.throws(() =>
      prepareResourceTrustInput(input, join(base, 'invalid.json')),
    );
  assert.equal(existsSync(join(base, 'invalid.json')), false);
});

test('workflow input reaches the actual bash build argument array with a checked file', (t) => {
  const base = tempBase(t);
  const workflow = readFileSync(
    new URL('../../../.github/workflows/installer.yml', import.meta.url),
    'utf8',
  );
  assert.match(workflow, /resource_trust_json:\s*\n\s+description:/);
  assert.match(
    workflow,
    /RESOURCE_TRUST_JSON: \$\{\{ inputs\.resource_trust_json \}\}/,
  );
  assert.match(workflow, /node scripts\/build\.mjs .*"\$\{extra\[@\]\}"/);
  const block = workflow.match(
    / {10}if \[ -n "\$RESOURCE_TRUST_JSON" \]; then\n[\s\S]*? {10}fi/,
  )[0];
  const bash =
    process.platform === 'win32'
      ? join(process.env.ProgramFiles, 'Git/bin/bash.exe')
      : '/bin/bash';
  const trust = roots();
  for (const [index, input] of [
    '',
    JSON.stringify(trust),
    Buffer.from(JSON.stringify(trust)).toString('base64'),
    '{invalid',
  ].entries()) {
    const runnerTemp = join(base, `runner ${index}`);
    mkdirSync(runnerTemp);
    const output = join(runnerTemp, 'resource-roots.json');
    const result = spawnSync(
      bash,
      [
        '-c',
        `set -euo pipefail\nextra=()\n${block}\nprintf '<%s>\\n' "\${extra[@]}"`,
      ],
      {
        cwd: fileURLToPath(new URL('..', import.meta.url)),
        env: {
          ...process.env,
          RESOURCE_TRUST_JSON: input,
          RUNNER_TEMP: runnerTemp.replaceAll('\\', '/'),
        },
        encoding: 'utf8',
      },
    );
    if (input === '{invalid') {
      assert.notEqual(result.status, 0);
      continue;
    }
    assert.equal(result.status, 0, result.stderr);
    if (!input) assert.equal(result.stdout.trim(), '<>');
    else {
      assert.match(result.stdout, /<--resource-trust-json>/);
      assert.ok(
        result.stdout.includes(
          runnerTemp.replaceAll('\\', '/') + '/resource-roots.json',
        ),
      );
      assert.deepEqual(JSON.parse(readFileSync(output)), trust);
      assert.ok(
        result.stdout.includes(
          createHash('sha256').update(readFileSync(output)).digest('hex'),
        ),
      );
    }
  }
});
