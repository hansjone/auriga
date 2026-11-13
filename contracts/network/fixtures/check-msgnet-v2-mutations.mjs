// 契约判定器的删除变异测试，扫描所有if检查及成熟/边界表达式，无需额外解析器依赖。
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const directory = new URL('./', import.meta.url);
function closing(source, start) {
  let depth = 0,
    quote = '';
  for (let i = start; i < source.length; i++) {
    const char = source[i];
    if (quote) {
      if (char === '\\') i++;
      else if (char === quote) quote = '';
      continue;
    }
    if (char === '"' || char === "'" || char === '`') {
      quote = char;
      continue;
    }
    if (char === '(') depth++;
    if (char === ')' && --depth === 0) return i;
  }
  throw Error('unbalanced predicate');
}
function leaves(source, start, end) {
  while (source[start] === '(' && closing(source, start) === end - 1) {
    start++;
    end--;
  }
  let depth = 0,
    quote = '',
    last = start;
  const parts = [];
  for (let i = start; i < end; i++) {
    const char = source[i];
    if (quote) {
      if (char === '\\') i++;
      else if (char === quote) quote = '';
      continue;
    }
    if (char === '"' || char === "'" || char === '`') {
      quote = char;
      continue;
    }
    if ('([{'.includes(char)) depth++;
    if (')]}'.includes(char)) depth--;
    if (depth === 0 && ['&&', '||'].includes(source.slice(i, i + 2))) {
      parts.push([last, i]);
      last = i + 2;
      i++;
    }
  }
  if (!parts.length) return [{ start, end }];
  parts.push([last, end]);
  return parts.flatMap(([a, b]) => {
    while (/\s/.test(source[a])) a++;
    while (/\s/.test(source[b - 1])) b--;
    return leaves(source, a, b);
  });
}
export function discoverPredicates(source) {
  const targets = [];
  for (const match of source.matchAll(/\bif\s*\(/g)) {
    const start = match.index + match[0].length,
      end = closing(source, start - 1);
    // 删除整个检查，以及复合拒绝条件中的各独立分支。
    targets.push({ start, end, replacement: 'false' });
    // 所有复合条件逐项删除，新增条件自动纳入。
    for (const atom of leaves(source, start, end))
      targets.push({ ...atom, replacement: 'false' });
  }
  for (const boundary of source.matchAll(
    /const (?:boundary|bad)\s*=\s*([\s\S]*?);/g,
  )) {
    const start = boundary.index + boundary[0].indexOf('=') + 1;
    for (const atom of leaves(
      source,
      start,
      boundary.index + boundary[0].length - 1,
    ))
      targets.push({ ...atom, replacement: 'false' });
  }
  const mature = source.indexOf('const mature =');
  if (mature >= 0) {
    const start = source.indexOf('return (', mature) + 'return ('.length,
      end = closing(source, start - 1);
    for (const atom of leaves(source, start, end))
      targets.push({ ...atom, replacement: 'true' });
  }
  // 跳过字符串和注释，逐个翻转比较运算符，包含回调表达式。
  const tokens =
    /\/\/[^\n]*|\/\*[\s\S]*?\*\/|'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`|===|!==|<=|>=|(?<![=<>])<(?![=<])|(?<![=<>])>(?![=>])/g;
  const flips = {
    '===': '!==',
    '!==': '===',
    '<': '<=',
    '<=': '<',
    '>': '>=',
    '>=': '>',
  };
  for (const match of source.matchAll(tokens)) {
    if (flips[match[0]])
      targets.push({
        start: match.index,
        end: match.index + match[0].length,
        comparison: true,
        replacement: flips[match[0]],
      });
  }
  return [
    ...new Map(
      targets.map((t) => [`${t.start}:${t.end}:${t.replacement}`, t]),
    ).values(),
  ];
}
export async function checkMutations() {
  const fixture = JSON.parse(
    readFileSync(new URL('auto-renewal-v2-vectors.json', directory)),
  );
  const modules = [
    ['msgnet-v2-oracle.mjs', [['cases', 'evaluate']]],
    [
      'msgnet-v2-boundary-oracle.mjs',
      [
        ['issuer_cases', 'evaluateIssuer'],
        ['boundary_cases', 'evaluateBoundary'],
      ],
    ],
  ];
  const survivors = [],
    coverage = [];
  let count = 0;
  for (const [name, groups] of modules) {
    const original = readFileSync(new URL(name, directory), 'utf8');
    const baseline = await import(new URL(name, directory).href);
    for (const [group, fn] of groups)
      for (const vector of fixture[group])
        if (baseline[fn](vector) !== vector.expected)
          throw Error('baseline failed: ' + group + '/' + vector.id);
    for (const target of discoverPredicates(original)) {
      let source =
        original.slice(0, target.start) +
        target.replacement +
        original.slice(target.end);
      source = source.replace(
        /from\s+(['"])(\.\.?\/[^'"]+)\1/g,
        (_, q, relative) => 'from ' + q + new URL(relative, directory).href + q,
      );
      source = source.replaceAll(
        'import.meta.url',
        JSON.stringify(new URL(name, directory).href),
      );
      const mutant = await import(
        'data:text/javascript;base64,' +
          Buffer.from(source).toString('base64') +
          '#' +
          count++
      );
      let witness;
      for (const [group, fn] of groups) {
        for (const vector of fixture[group]) {
          let result;
          try {
            result = mutant[fn](vector);
          } catch (error) {
            result = 'exception:' + error.message;
          }
          if (result !== vector.expected) {
            witness = group + '/' + vector.id;
            break;
          }
        }
        if (witness) break;
      }
      const record = {
        file: name,
        line: original.slice(0, target.start).split('\n').length,
        expression: original
          .slice(
            target.comparison
              ? original.lastIndexOf('\n', target.start) + 1
              : target.start,
            target.comparison ? original.indexOf('\n', target.end) : target.end,
          )
          .trim()
          .replace(/\s+/g, ' '),
        replacement: target.replacement,
      };
      if (witness) coverage.push({ ...record, witness });
      else survivors.push(record);
    }
  }
  const equivalents = JSON.parse(
    readFileSync(new URL('msgnet-v2-equivalent-mutations.json', directory)),
  );
  const identity = (record) =>
    JSON.stringify([record.file, record.expression, record.replacement]);
  const declared = new Map(
    equivalents.map((record) => [identity(record), record]),
  );
  const actual = new Set(survivors.map(identity));
  const unexpected = survivors.filter(
    (record) => !declared.has(identity(record)),
  );
  const stale = equivalents.filter(
    (record) => !actual.has(identity(record)) || !record.reason?.trim(),
  );
  if (
    declared.size !== equivalents.length ||
    survivors.length !== actual.size ||
    unexpected.length ||
    stale.length
  )
    throw Error(
      JSON.stringify(
        { mutants: count, killed: coverage.length, unexpected, stale },
        null,
        2,
      ),
    );
  return {
    count,
    killed: coverage.length,
    equivalent: equivalents.length,
    coverage,
    survivors,
  };
}
if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  console.log(JSON.stringify(await checkMutations(), null, 2));
}
