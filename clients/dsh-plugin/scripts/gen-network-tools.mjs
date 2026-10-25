// Regenerate with: npm run gen:network-tools (in clients/dsh-plugin).
// Uses the repository's locked development tools; no runtime dependencies.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { Buffer } from 'node:buffer';
import { compile } from 'json-schema-to-typescript';
import { format } from 'prettier';
import Ajv from 'ajv/dist/2020.js';
import standalone from 'ajv/dist/standalone/index.js';

const require = createRequire(import.meta.url);
const license = await readFile(require.resolve('ajv/LICENSE'), 'utf8');
const root = new URL('../../../', import.meta.url);
const out = new URL('clients/dsh-plugin/src/generated/network/', root);
const typesOut = new URL('clients/dsh-plugin/src/generated/network/', root);
const load = async (name) =>
  JSON.parse(
    await readFile(new URL(`contracts/network/${name}`, root), 'utf8'),
  );
const [send, envelope, address, limits] = await Promise.all([
  load('send.schema.json'),
  load('envelope.schema.json'),
  load('address.schema.json'),
  load('limits.json'),
]);
const documents = {
  'send.schema.json': send,
  'envelope.schema.json': envelope,
  'address.schema.json': address,
};
// These three local contract files have acyclic refs. Resolve only the selected
// definition, never fetch the documentation $id URLs or copy unused definitions.
function resolve(value, file = 'send.schema.json') {
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
    return { ...resolve(target, targetFile), ...resolve(siblings, file) };
  }
  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [key, resolve(item, file)]),
  );
}
const inputSchema = resolve(send.$defs.AgentSendToolInput);
const ajv = new Ajv({
  strict: false,
  validateFormats: false,
  ownProperties: true,
  code: { source: true, esm: true },
  messages: false,
});
const validate = ajv.compile(inputSchema);
let validator = standalone(ajv, validate);
// Inline the sole Ajv helper so the published plugin needs no Ajv package.
const helper =
  /require\("(?:@redocly\/)?ajv\/dist\/runtime\/ucs2length"\)\.default/g;
validator = validator.replace(
  helper,
  () => `(${require('ajv/dist/runtime/ucs2length.js').default.toString()})`,
);
if (/\brequire\(/.test(validator))
  throw new Error(
    `unexpected generated runtime dependency: ${validator.match(/require\([^)]*\)/g)}`,
  );
const banner =
  '// Generated from contracts/network by scripts/gen-network-tools.mjs. Do not edit.\n';
const artifacts = new Map([
  [
    'validate-input.js',
    '// @ts-nocheck\n' +
      banner +
      `/* Inlined Ajv runtime helper license:\n${license}\n*/\n` +
      validator,
  ],
  [
    'contract.js',
    banner +
      `export const inputSchema = ${JSON.stringify(inputSchema)};\nexport const encoding = ${JSON.stringify(limits.encoding)};\nexport const opaqueIdPattern = ${JSON.stringify(envelope.$defs.OpaqueId.pattern)};\n`,
  ],
]);
for (const name of ['AgentSendToolInput', 'SubmitRequest', 'SendResult']) {
  // Compile each root separately to preserve the contract's exported names.
  const types = await compile(
    { ...resolve(send.$defs[name]), title: name },
    name,
    {
      bannerComment: banner.trim(),
    },
  );
  artifacts.set(`${name}.d.ts`, types);
}
if (!process.argv.includes('--check')) {
  await mkdir(out, { recursive: true });
  await mkdir(typesOut, { recursive: true });
}
for (const [name, source] of artifacts) {
  const text = await format(source, {
    parser: name.endsWith('.d.ts') ? 'typescript' : 'babel',
    singleQuote: true,
    trailingComma: 'all',
  });
  const target = new URL(name, name.endsWith('.d.ts') ? typesOut : out);
  if (process.argv.includes('--check')) {
    if (!(await readFile(target)).equals(Buffer.from(text)))
      throw new Error(`generated artifact differs: ${name}`);
  } else await writeFile(target, text);
}
