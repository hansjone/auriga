import { existsSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';

export const SHARED_ROW = `- insert:
    - id: auriga-shared-skills
      name: '@deepseek-ai/dsh-skill-filesystem'
      config:
        providerName: auriga-shared
        includeDefaultRoots: false
        customSkillDirs:
          - !!js process.getBuiltinModule('node:path').join(process.env.DSH_HOME, 'auriga', 'skills-active', 'current')
        watch: true
`;

export function yamlParser(yaml) {
  return (source) => {
    const doc = yaml.parseDocument(source, {
      uniqueKeys: true,
      customTags: [{ tag: 'tag:yaml.org,2002:js', resolve: (value) => value }],
    });
    if (doc.errors.length || doc.warnings.length)
      throw new Error('invalid Cordis YAML');
    return doc.toJS({ maxAliasCount: 0 });
  };
}

// Parse only. Never evaluate the upstream !!js expressions or serialize user YAML.
export function mergeSkillPatch(source, parse, dataHome) {
  if (!isAbsolute(dataHome)) throw new Error('DSH_HOME must be absolute');
  const rows = parse(source);
  if (!Array.isArray(rows)) throw new Error('Cordis patch must be a sequence');
  const expected = parse(SHARED_ROW)[0].insert[0];
  const owned = [];
  const providers = [];
  function visit(value) {
    if (!value || typeof value !== 'object') return;
    if (value.id === expected.id) owned.push(value);
    if (value.config?.providerName === 'auriga-shared') providers.push(value);
    for (const child of Object.values(value)) {
      if (Array.isArray(child)) child.forEach(visit);
      else visit(child);
    }
  }
  rows.forEach(visit);
  if (owned.length || providers.length) {
    if (
      owned.length !== 1 ||
      providers.length !== 1 ||
      owned[0] !== providers[0]
    )
      throw new Error('duplicate shared provider');
    const row = owned[0];
    const host = rows.some((r) => r.insert?.includes(row));
    const equal = (a, b) => {
      if (a === b) return true;
      if (!a || !b || typeof a !== 'object' || typeof b !== 'object')
        return false;
      const keys = Object.keys(a);
      return (
        keys.length === Object.keys(b).length &&
        keys.every((k) => equal(a[k], b[k]))
      );
    };
    if (!host || !equal(row, expected))
      throw new Error('conflicting shared provider');
    return source;
  }
  return source + (source && !source.endsWith('\n') ? '\n' : '') + SHARED_ROW;
}

export function upgradeSkillPatch(file, parse, dataHome) {
  const source = existsSync(file) ? readFileSync(file, 'utf8') : '';
  const merged = mergeSkillPatch(source, parse, dataHome);
  if (merged !== source) {
    writeFileSync(`${file}.shared.tmp`, merged);
    renameSync(`${file}.shared.tmp`, file);
  }
}
