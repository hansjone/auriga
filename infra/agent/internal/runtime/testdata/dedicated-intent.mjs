// Pure Go artifact -> actual frozen Host parser. No DSH/plugin process runs.
// The SDK test locator supplies the already-declared yaml@2.9.1 dependency;
// this local test hook is never imported by a production launcher or config.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createRequire, registerHooks } from 'node:module';
import {
  readFileSync,
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  rmSync,
  realpathSync,
} from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

const [sdkAnchor, ...fixtures] = process.argv.slice(2);
assert.ok(
  sdkAnchor && fixtures.length,
  'usage: dedicated-intent.mjs <approved SDK package.json> <Go fixture.json>...',
);
const sdk = createRequire(resolve(sdkAnchor));
assert.equal(sdk('yaml/package.json').version, '2.9.1');
const yamlUrl = pathToFileURL(sdk.resolve('yaml')).href;
registerHooks({
  resolve(specifier, context, next) {
    return specifier === 'yaml'
      ? { url: yamlUrl, shortCircuit: true }
      : next(specifier, context);
  },
});
const { readIntent, loadResources } =
  await import('../../../../../clients/dsh-plugin/src/dedicated/intent.js');
for (const file of fixtures) {
  const fixture = JSON.parse(readFileSync(file, 'utf8'));
  const parent = realpathSync(dirname(resolve(file)));
  const root = realpathSync(mkdtempSync(join(parent, 'host-read-')));
  assert.ok(root.startsWith(parent + sep));
  try {
    for (const [name, encoded] of Object.entries(fixture.files)) {
      const path = resolve(root, name);
      assert.ok(path.startsWith(root + sep));
      mkdirSync(dirname(path), { recursive: true });
      writeFileSync(path, Buffer.from(encoded, 'base64'));
    }
    const intentFile = join(root, 'intent.json');
    writeFileSync(intentFile, fixture.intent);
    const intent = readIntent(intentFile, fixture.binding, () => fixture.now);
    const resources = await loadResources(intent, root);
    assert.equal(intent.intentDigest, fixture.intentDigest);
    assert.deepEqual(
      resources.map((row) => row.kind),
      [fixture.kind],
    );
    console.log(
      'PASS actual Go artifact -> frozen Host ' + fixture.kind + ' consumer',
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}
