// Run from the plugin package: node test/network/trust/generate-keyset.mjs
import { readFile, writeFile } from 'node:fs/promises';
import { compile } from 'json-schema-to-typescript';
import { format } from 'prettier';

const base = new URL('../../../../../contracts/network/', import.meta.url);
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
const output = new URL('../../../src/network/trust/', import.meta.url);
const style = JSON.parse(
  await readFile(
    new URL('../../../../../.prettierrc.json', import.meta.url),
    'utf8',
  ),
);
await writeFile(
  new URL('keyset-types.d.ts', output),
  await format(
    await compile(schema, 'KeysetPayload', {
      unreachableDefinitions: true,
      ignoreMinAndMaxItems: true,
    }),
    { ...style, parser: 'typescript' },
  ),
);
await writeFile(
  new URL('keyset-schema.js', output),
  await format(
    '// Generated from contracts/network/keyset.schema.json; do not edit.\nexport const definitions = ' +
      JSON.stringify(definitions, null, 2) +
      ';\n',
    { ...style, parser: 'babel' },
  ),
);
