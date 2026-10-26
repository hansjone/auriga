// Uses the locked workspace tools, as clients/dsh-plugin/scripts/gen-network-tools.mjs.
import { readFile, writeFile } from 'node:fs/promises';
import { compile } from '../../node_modules/json-schema-to-typescript/dist/src/index.js';
import { format } from '../../node_modules/prettier/index.mjs';

const documents = Object.fromEntries(
  await Promise.all(
    ['source-control', 'envelope', 'address'].map(async (name) => [
      `${name}.schema.json`,
      JSON.parse(
        await readFile(
          new URL(`./${name}.schema.json`, import.meta.url),
          'utf8',
        ),
      ),
    ]),
  ),
);
// Resolve selected acyclic definitions only. The CLI resolver cannot handle the
// nested external $defs references used by these network schemas.
function resolve(value, file = 'source-control.schema.json') {
  if (Array.isArray(value)) return value.map((item) => resolve(item, file));
  if (!value || typeof value !== 'object') return value;
  if (value.$ref) {
    const [name, pointer] = value.$ref.split('#');
    const targetFile = name || file;
    if (!documents[targetFile] || !pointer?.startsWith('/$defs/'))
      throw new Error('unsupported contract reference');
    const target = pointer
      .slice(1)
      .split('/')
      .reduce((node, key) => node[key], documents[targetFile]);
    const { $ref, ...siblings } = value;
    void $ref;
    return {
      ...resolve(target, targetFile),
      ...(target.type === 'object' ? { title: pointer.split('/').at(-1) } : {}),
      ...resolve(siblings, file),
    };
  }
  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [key, resolve(item, file)]),
  );
}
const schema = documents['source-control.schema.json'];
const generated = await compile(
  { title: 'SourceControl', oneOf: resolve(schema.oneOf) },
  'SourceControl',
  {
    bannerComment:
      '// Generated from contracts/network/source-control.schema.json by generate-source-control.mjs. Do not edit.',
  },
);
const text = await format(generated, {
  parser: 'typescript',
  singleQuote: true,
  trailingComma: 'all',
});
const output = new URL(
  '../../clients/dsh-plugin/src/generated/network/SourceControl.d.ts',
  import.meta.url,
);
if (process.argv.includes('--check')) {
  if ((await readFile(output, 'utf8')) !== text)
    throw new Error('generated SourceControl.d.ts differs');
} else await writeFile(output, text);
