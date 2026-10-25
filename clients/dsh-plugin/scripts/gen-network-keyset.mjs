// Run from the plugin package: node scripts/gen-network-keyset.mjs
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { compile } from 'json-schema-to-typescript';
import { format } from 'prettier';
import { pathToFileURL } from 'node:url';

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

// Importing this module regenerates in memory; only the CLI writes artifacts.
if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  await mkdir(output, { recursive: true });
  for (const [name, content] of Object.entries(generatedKeysetFiles)) {
    await writeFile(new URL(name, output), content);
  }
}
