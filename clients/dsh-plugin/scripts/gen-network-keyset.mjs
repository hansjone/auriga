// Run from the plugin package: node scripts/gen-network-keyset.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { compile, compileFromFile } from 'json-schema-to-typescript';
import { format } from 'prettier';
import { fileURLToPath, pathToFileURL } from 'node:url';

const base = new URL('../../../contracts/network/', import.meta.url);
async function resolve(value, file = 'keyset.schema.json') {
  if (Array.isArray(value))
    return Promise.all(value.map((v) => resolve(v, file)));
  if (!value || typeof value !== 'object') return value;
  if (file === 'keyset.schema.json' && value.$ref?.startsWith('#/$defs/'))
    return { ...value, $ref: value.$ref.replace('#/$defs/', '#/definitions/') };
  if (value.$ref) {
    const [target, fragment] = value.$ref.split('#/');
    const schema = JSON.parse(
      await readFile(new URL(target || file, base), 'utf8'),
    );
    const node = fragment.split('/').reduce((v, k) => v[k], schema);
    return resolve(node, target || file);
  }
  return Object.fromEntries(
    await Promise.all(
      Object.entries(value).map(async ([k, v]) => [k, await resolve(v, file)]),
    ),
  );
}
const original = JSON.parse(
  await readFile(new URL('keyset.schema.json', base), 'utf8'),
);
const definitions = await resolve(original.$defs);
const schema = { definitions, $ref: '#/definitions/KeysetPayload' };
const output = new URL('../src/generated/network/', import.meta.url);
const style = JSON.parse(
  await readFile(new URL('../../../.prettierrc.json', import.meta.url), 'utf8'),
);
export const generatedKeysetFiles = {
  'keyset-types.d.ts': await format(
    await compile(schema, 'KeysetPayload', {
      unreachableDefinitions: true,
      ignoreMinAndMaxItems: true,
    }),
    { ...style, parser: 'typescript' },
  ),
  'keyset-schema.js': await format(
    '// Generated from contracts/network/keyset.schema.json; do not edit.\nexport const definitions = ' +
      JSON.stringify(definitions, null, 2) +
      ';\n',
    { ...style, parser: 'babel' },
  ),
};

// K owns generated artifacts. P exposes this in-memory output so K can publish
// the runtime schema alongside its existing v2 types, without a second schema
// definition in the consumer or a runtime dependency on the repository tree.
const renewalSource = await readFile(
  new URL('auto-renewal-v2.schema.json', base),
  'utf8',
);
export const generatedRenewalFiles = {
  'auto-renewal-v2.d.ts': await format(
    await compileFromFile(
      fileURLToPath(new URL('auto-renewal-v2.schema.json', base)),
      { unreachableDefinitions: true },
    ),
    { ...style, parser: 'typescript' },
  ),
  'auto-renewal-v2-schema.js': await format(
    '// Generated from contracts/network/auto-renewal-v2.schema.json; do not edit.\n' +
      'export const schema = ' +
      renewalSource +
      ';\n',
    { ...style, parser: 'babel' },
  ),
};

// Importing this module regenerates in memory; only the CLI writes artifacts.
if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== '--v2'))
    throw new Error('Usage: node scripts/gen-network-keyset.mjs [--v2]');
  const files =
    args[0] === '--v2' ? generatedRenewalFiles : generatedKeysetFiles;
  await mkdir(output, { recursive: true });
  for (const [name, content] of Object.entries(files)) {
    await writeFile(new URL(name, output), content);
  }
}
