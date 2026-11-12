import { parseDocument, isSeq } from '../node_modules/yaml/dist/index.js';
import { isDeepStrictEqual } from 'node:util';
const jsTag = { tag: 'tag:yaml.org,2002:js', resolve: (value) => value };
export function parseCordisFixture(text) {
  const doc = parseDocument(text, { customTags: [jsTag], uniqueKeys: true });
  if (doc.errors.length || !isSeq(doc.contents))
    throw new Error('invalid Cordis YAML');
  return doc;
}
// Reference transformation only. Never rewrite a shipped or user preset row.
export function mergeCordisFixture(input, _upstream, expression) {
  try {
    const doc = parseCordisFixture(input);
    const rows = doc.contents.items.flatMap((row) => [
      row,
      ...(row?.get?.('insert')?.items ?? []),
    ]);
    const own = rows.filter(
      (row) => row?.get?.('id') === 'auriga-shared-skills',
    );
    if (own.length > 1) throw new Error('duplicate shared row');
    const config = {
      providerName: 'auriga-shared',
      includeDefaultRoots: false,
      customSkillDirs: [expression],
      watch: true,
    };
    for (const row of rows) {
      if (
        row?.getIn?.(['config', 'providerName']) === config.providerName &&
        row.get('id') !== 'auriga-shared-skills'
      )
        throw new Error('provider collision');
    }
    if (own.length) {
      const row = own[0];
      const dirs = row.getIn(['config', 'customSkillDirs']);
      if (
        row.get('name') !== '@deepseek-ai/dsh-skill-filesystem' ||
        row.get('disabled') === true ||
        !isDeepStrictEqual(row.get('config')?.toJSON(), config) ||
        dirs?.items?.[0]?.tag !== jsTag.tag
      )
        throw new Error('conflicting shared row');
      return { shared: true, text: input };
    }
    const row =
      "- id: auriga-shared-skills\n  name: '@deepseek-ai/dsh-skill-filesystem'\n  config:\n    providerName: auriga-shared\n    includeDefaultRoots: false\n    customSkillDirs:\n      - !!js " +
      expression +
      '\n    watch: true\n';
    const prefix = doc.contents.items.length
      ? input + (input.endsWith('\n') ? '' : '\n')
      : '';
    return {
      shared: true,
      text:
        prefix +
        '- insert:\n' +
        row
          .split('\n')
          .filter(Boolean)
          .map((line) => '    ' + line)
          .join('\n') +
        '\n',
    };
  } catch {
    return { shared: false, text: input };
  }
}
export function cordisValue(text) {
  return parseCordisFixture(text).toJS();
}
export function cordisExpressions(text) {
  const values = [];
  const visit = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.tag === jsTag.tag) values.push(node.value);
    for (const child of node.items ?? []) {
      visit(child);
      visit(child.key);
      visit(child.value);
    }
  };
  visit(parseCordisFixture(text).contents);
  return values;
}
