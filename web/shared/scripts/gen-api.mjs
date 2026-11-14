import { fileURLToPath } from 'node:url';
import { mkdir, writeFile } from 'node:fs/promises';
import openapiTS, { astToString, COMMENT_HEADER } from 'openapi-typescript';
import { format } from 'prettier';
import { compileFromFile } from 'json-schema-to-typescript';
const output = new URL('../src/generated/', import.meta.url);
await mkdir(output, { recursive: true });
await writeFile(
  new URL('deployment-access.d.ts', output),
  await format(
    await compileFromFile(
      fileURLToPath(
        new URL(
          '../../../contracts/deployment/access.schema.json',
          import.meta.url,
        ),
      ),
      { unreachableDefinitions: true },
    ),
    { parser: 'typescript', singleQuote: true },
  ),
);
for (const name of [
  'network-keys-v2',
  'iam',
  'org',
  'workspace',
  'ai',
  'resource',
  'shell-auth',
]) {
  const source = new URL(
    '../../../contracts/openapi/' + name + '.yaml',
    import.meta.url,
  );
  const ast = await openapiTS(source);
  const body = await format(astToString(ast), {
    parser: 'typescript',
    singleQuote: true,
  });
  await writeFile(fileURLToPath(new URL(name + '.ts', output)), body);
}

// These contracts are consumed by the admin application only.
for (const [name, filename] of [
  ['audit', 'audit.generated'],
  ['ops', 'opsSchema'],
  ['ops-runtime', 'runtimeContract'],
]) {
  await writeFile(
    new URL('../../admin/src/admin/' + filename + '.ts', import.meta.url),
    await format(
      COMMENT_HEADER +
        astToString(
          await openapiTS(
            new URL(
              '../../../contracts/openapi/' + name + '.yaml',
              import.meta.url,
            ),
          ),
        ),
      { parser: 'typescript', singleQuote: true },
    ),
  );
}
