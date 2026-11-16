/** workflow_dispatch accepts public roots only, as JSON text or standard base64. */
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateResourceRoots } from './build.mjs';

export function prepareResourceTrustInput(input, output) {
  if (input === '' || input === undefined) return [];
  if (typeof input !== 'string' || input.length > 16384)
    throw new Error('Invalid resource trust input size');
  let text = input.trim().replace(/^\uFEFF/, '');
  if (!text.startsWith('{')) {
    const encoded = text.replace(/\s/g, '');
    const bytes = Buffer.from(encoded, 'base64');
    if (!encoded || bytes.toString('base64') !== encoded)
      throw new Error('Resource trust input must be JSON or standard base64');
    text = bytes.toString('utf8');
  }
  let trust;
  try {
    trust = JSON.parse(text);
  } catch {
    throw new Error('Invalid resource trust JSON');
  }
  trust = validateResourceRoots(trust);
  writeFileSync(output, JSON.stringify(trust), { flag: 'wx', mode: 0o600 });
  return ['--resource-trust-json', output];
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  try {
    const output = process.argv[2];
    if (!output) throw new Error('Resource trust output path required');
    const args = prepareResourceTrustInput(
      process.env.RESOURCE_TRUST_JSON,
      output,
    );
    if (args.length)
      console.log(
        `resourceTrustSha256=${createHash('sha256').update(readFileSync(output)).digest('hex')}`,
      );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
