import { commandJson } from './command-json.mjs';

// Operator transport: one query URL on stdin, original Loki JSON on stdout.
export async function queryLoki(command, url, timeout = 15000) {
  return commandJson(command, url.href + '\n', timeout);
}
