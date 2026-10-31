// Report paths only: never include credential values in probe diagnostics.
export function classifyModelExposure(value, surface) {
  const warnings = [];
  const failures = [];
  const walk = (node, path, namespace) => {
    if (typeof node === 'string' && node.includes('amc.')) {
      const accepted =
        surface === 'describe' &&
        namespace === 'llm-pi-ai' &&
        path.length === 7 &&
        path[0] === 'namespaces' &&
        /^\d+$/.test(path[1]) &&
        path.slice(2).join('/') ===
          'value/providers/auriga-managed/headers/Authorization';
      (accepted ? warnings : failures).push('$.' + path.join('.'));
    } else if (node && typeof node === 'object') {
      for (const [key, child] of Object.entries(node)) {
        if (key.includes('amc.')) {
          failures.push('$.' + [...path, '[redacted-field]'].join('.'));
          walk(child, [...path, '[redacted-field]'], namespace);
          continue;
        }
        const ns =
          path.length === 1 && path[0] === 'namespaces' ? child?.ns : namespace;
        walk(child, [...path, key], ns);
      }
    }
  };
  walk(value, [], undefined);
  return { warnings, failures };
}
