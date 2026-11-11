import { readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
const source = new URL(
  '../../../contracts/deployment/egress.schema.json',
  import.meta.url,
);
const schema = JSON.parse(readFileSync(source, 'utf8'));
const type = (s) =>
  s.$ref
    ? s.$ref.split('/').at(-1)
    : s.type === 'string'
      ? 'string'
      : s.type === 'integer'
        ? 'int64'
        : s.type === 'array'
          ? `[]${type(s.items)}`
          : (() => {
              throw new Error('unsupported schema type');
            })();
let output =
  '// Code generated from contracts/deployment/egress.schema.json; DO NOT EDIT.\npackage policy\n';
for (const [name, s] of Object.entries(schema.$defs)) {
  output += `\ntype ${name} struct {\n`;
  for (const [field, property] of Object.entries(s.properties))
    output += `${field[0].toUpperCase() + field.slice(1)} ${type(property)} \`json:"${field}"\`\n`;
  output += '}\n';
}
const target = new URL(
  '../../../infra/egress/internal/policy/wire_generated.go',
  import.meta.url,
);
if (process.argv.includes('--check')) {
  // Contract-only CI needs Node, not a Go toolchain. Compare every generated token;
  // the Go job independently enforces gofmt on the checked-in model.
  if (
    readFileSync(target, 'utf8').replace(/\s/g, '') !==
    output.replace(/\s/g, '')
  )
    throw new Error('egress generated model drift');
} else {
  const formatted = spawnSync('gofmt', [], { input: output, encoding: 'utf8' });
  if (formatted.status !== 0)
    throw new Error(formatted.stderr || 'gofmt failed');
  writeFileSync(target, formatted.stdout);
}
