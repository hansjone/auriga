import { readFile, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import openapiTS, { astToString } from 'openapi-typescript';
import prettier from 'prettier';

/** Generate only the data-plane schema dependency closure, with no copied types. */
export async function generateContract() {
  const source = JSON.parse(
    await readFile(
      new URL('../../../../contracts/openapi/ai.yaml', import.meta.url),
      'utf8',
    ),
  );
  const schemas = {};
  const visit = (name) => {
    if (schemas[name]) return;
    schemas[name] = source.components.schemas[name];
    const refs = JSON.stringify(schemas[name]).matchAll(
      /"\$ref":"#\/components\/schemas\/([^"]+)"/g,
    );
    for (const ref of refs) visit(ref[1]);
  };
  for (const name of [
    'ChatCompletionRequest',
    'ChatCompletion',
    'ChatCompletionChunk',
    'ModelList',
    'AiError',
  ])
    visit(name);
  const nodes = await openapiTS({
    openapi: source.openapi,
    info: source.info,
    paths: {},
    components: { schemas },
  });
  return prettier.format(
    `/** Generated from contracts/openapi/ai.yaml by test/ai/generate-contract.js. */\n${astToString(nodes)}`,
    { parser: 'typescript', singleQuote: true, trailingComma: 'all' },
  );
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  await writeFile(
    new URL('../../src/ai/contract.d.ts', import.meta.url),
    await generateContract(),
  );
}
