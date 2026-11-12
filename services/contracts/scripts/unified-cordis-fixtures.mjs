import {
  parseDocument,
  isSeq,
  Scalar,
} from '../node_modules/yaml/dist/index.js';
const jsTag = { tag: 'tag:yaml.org,2002:js', resolve: (value) => value };
export function parseCordisFixture(text) {
  const doc = parseDocument(text, { customTags: [jsTag], uniqueKeys: true });
  if (doc.errors.length || !isSeq(doc.contents))
    throw new Error('invalid Cordis YAML');
  return doc;
}
const byId = (seq, id) => seq.items.find((row) => row?.get?.('id') === id);
// Reference transformation for fixtures. D/E retain independent production implementations.
export function mergeCordisFixture(input, upstream, expression) {
  try {
    const doc = parseCordisFixture(input);
    let preset = byId(doc.contents, 'preset-cordis');
    if (!preset) {
      if (!upstream) throw new Error('missing trusted preset');
      const source = parseCordisFixture(upstream);
      const candidates = source.contents.items
        .flatMap((row) => row?.get?.('insert')?.items ?? [])
        .filter((row) => row.get('id') === 'preset-cordis');
      if (candidates.length !== 1) throw new Error('ambiguous preset');
      preset = candidates[0].clone();
      doc.contents.add(preset);
    }
    const plugins = preset.getIn(['config', 'plugins']);
    if (!isSeq(plugins)) throw new Error('missing plugin list');
    const rows = plugins.items.filter(
      (row) => row?.get?.('id') === 'skill-filesystem',
    );
    if (rows.length !== 1) throw new Error('ambiguous skill provider');
    const dirs = rows[0].getIn(['config', 'customSkillDirs']);
    if (!isSeq(dirs))
      throw new Error('unsupported skill root expression shape');
    if (
      !dirs.items.some(
        (node) => node.tag === jsTag.tag && node.value === expression,
      )
    ) {
      const scalar = new Scalar(expression);
      scalar.tag = jsTag.tag;
      dirs.add(scalar);
    }
    return { shared: true, text: doc.toString() };
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
