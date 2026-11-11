import { commandJson } from './command-json.mjs';

// Operator transport: one query URL on stdin, original Loki JSON on stdout.
export async function queryLoki(command, url, timeout = 15000) {
  return commandJson(command, url.href + '\n', timeout);
}

// Keep all sources: selecting only stage logs would hide the portal and plugin spans.
export function startupTraceUrl(traceId, { start, end } = {}) {
  if (!/^[0-9a-f]{32}$/.test(traceId) || /^0{32}$/.test(traceId))
    throw new Error('valid trace ID required');
  const url = new URL('http://127.0.0.1:3100/loki/api/v1/query_range');
  url.searchParams.set(
    'query',
    '{job=~"auriga-nas|auriga-plugin|auriga-compose"} | json | trace_id="' + traceId + '"',
  );
  url.searchParams.set('direction', 'forward');
  url.searchParams.set('limit', '5000');
  if (start) url.searchParams.set('start', start);
  if (end) url.searchParams.set('end', end);
  return url;
}
