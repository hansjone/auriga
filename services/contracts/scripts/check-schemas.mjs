import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
const directory = fileURLToPath(new URL('../../../contracts/', import.meta.url));
function find(folder) {
  return readdirSync(folder, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(folder, entry.name);
    return entry.isDirectory() ? find(file) : entry.name.endsWith('.schema.json') ? [file] : [];
  });
}
const ajv = new Ajv2020({ strict: true, allowUnionTypes: true, allErrors: true });
addFormats(ajv);
const files = find(directory).sort();
for (const file of files) ajv.addSchema(JSON.parse(readFileSync(file, 'utf8')), pathToFileURL(file).href);
let failures = 0;
for (const file of files) {
  try {
    // Compile every definition as well: some contract files contain only $defs.
    const schema = JSON.parse(readFileSync(file, 'utf8'));
    const uri = pathToFileURL(file).href;
    ajv.getSchema(uri);
    for (const name of Object.keys(schema.$defs ?? {})) {
      ajv.getSchema(uri + '#/$defs/' + name.replaceAll('~', '~0').replaceAll('/', '~1'));
    }
    console.log('OK ' + path.relative(directory, file));
  } catch (error) {
    failures++;
    console.error(path.relative(directory, file) + ': ' + error.message);
  }
}
if (failures) process.exitCode = 1;
console.log(files.length + ' schema files, ' + failures + ' failed');
if (!failures) {
  const checks = [
    ['--test', fileURLToPath(new URL('../../../contracts/network/fixtures/source-control.test.mjs', import.meta.url))],
    [fileURLToPath(new URL('../../../contracts/network/generate-source-control.mjs', import.meta.url)), '--check'],
  ];
  for (const args of checks) {
    const result = spawnSync(process.execPath, args, { stdio: 'inherit' });
    if (result.error) throw result.error;
    if (result.status !== 0) {
      process.exitCode = result.status ?? 1;
      break;
    }
  }
}
