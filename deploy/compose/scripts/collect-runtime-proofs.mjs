#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { lstat, mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { collectorArgs, localRepository, repositoryDigest } from './runtime-image-input.mjs';

export const maxProofBytes = 4 * 1024 * 1024;
const manifestTypes = ['application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json'];
const configTypes = ['application/vnd.oci.image.config.v1+json', 'application/vnd.docker.container.image.v1+json'];
const digestPattern = /^sha256:[a-f0-9]{64}$/;
const hash = (raw) => `sha256:${createHash('sha256').update(raw).digest('hex')}`;

// JSON.parse alone silently accepts duplicate keys. Scan the original JSON at
// every nesting level before interpreting it; strings are decoded by JSON.parse.
export function strictJSON(raw) {
  const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(raw);
  let i = 0;
  const ws = () => { while (/[\x20\t\r\n]/.test(text[i] || '\0')) i++; };
  const string = () => {
    const start = i++;
    while (i < text.length) {
      const ch = text[i++];
      if (ch === '\\') i++;
      else if (ch === '"') {
        const value = JSON.parse(text.slice(start, i));
        for (const scalar of value) {
          const cp = scalar.codePointAt(0);
          if (cp >= 0xd800 && cp <= 0xdfff) throw new Error('invalid JSON Unicode scalar');
        }
        return value;
      }
    }
    throw new Error('unterminated JSON string');
  };
  const value = (depth = 0) => {
    ws();
    if ((text[i] === '{' || text[i] === '[') && depth >= 32) throw new Error('JSON nesting limit');
    if (text[i] === '{') {
      i++; ws(); const keys = new Set();
      if (text[i] === '}') { i++; return; }
      for (;;) {
        ws(); if (text[i] !== '"') throw new Error('invalid JSON key');
        const key = string();
        if (keys.has(key)) throw new Error('duplicate JSON key');
        keys.add(key); ws(); if (text[i++] !== ':') throw new Error('invalid JSON object');
        value(depth + 1); ws(); const ch = text[i++];
        if (ch === '}') return;
        if (ch !== ',') throw new Error('invalid JSON object');
      }
    }
    if (text[i] === '[') {
      i++; ws(); if (text[i] === ']') { i++; return; }
      for (;;) { value(depth + 1); ws(); const ch = text[i++]; if (ch === ']') return; if (ch !== ',') throw new Error('invalid JSON array'); }
    }
    if (text[i] === '"') { string(); return; }
    const start = i;
    while (i < text.length && !/[\x20\t\r\n,}\]]/.test(text[i])) i++;
    if (i === start) throw new Error('invalid JSON value');
    JSON.parse(text.slice(start, i));
  };
  value(); ws(); if (i !== text.length) throw new Error('trailing JSON');
  return JSON.parse(text);
}

export function imageParts(image, approvedRepository) {
  if (approvedRepository !== undefined) {
    const { repository } = localRepository(approvedRepository);
    return { repository, digest: repositoryDigest(image, approvedRepository) };
  }
  const match = /^(127\.0\.0\.1:5000)\/(auriga\/(?:fake|real)-dsh)@(sha256:[a-f0-9]{64})$/.exec(image);
  if (!match) throw new Error('image must name this M2 registry and exact fixture repository by digest');
  return { repository: match[2], digest: match[3] };
}

// Reserved field names are ASCII. Unicode simple folding into ASCII adds only
// long-s and Kelvin sign to A-Z; generic Unicode lowercasing is not equivalent.
const structuralFold = key => key.replace(/[A-Z\u017f\u212a]/g, scalar => scalar === '\u017f' ? 's' : scalar === '\u212a' ? 'k' : scalar.toLowerCase());
function canonicalMembers(object, names, strict = false) {
  if (!object || typeof object !== 'object' || Array.isArray(object)) return;
  for (const key of Object.keys(object)) {
    for (const name of names) {
      if (key !== name && structuralFold(key) === structuralFold(name)) throw new Error(`noncanonical structural JSON member ${JSON.stringify(key)} (expected ${JSON.stringify(name)})`);
    }
    if (strict && !names.includes(key)) throw new Error(`unsupported structural JSON member ${JSON.stringify(key)}`);
  }
}

function structuralMembers(manifest, config) {
  canonicalMembers(manifest, ['schemaVersion', 'mediaType', 'config', 'layers', 'annotations', 'artifactType', 'subject', 'manifests']);
  const descriptor = ['mediaType', 'digest', 'size', 'annotations'];
  canonicalMembers(manifest?.config, descriptor, true);
  if (Array.isArray(manifest?.layers)) for (const layer of manifest.layers) canonicalMembers(layer, descriptor, true);
  canonicalMembers(config, ['created', 'author', 'architecture', 'os', 'os.version', 'os.features', 'variant', 'config', 'rootfs', 'history', 'id', 'parent', 'comment', 'container', 'container_config', 'docker_version', 'Size', 'artifactType', 'subject', 'manifests', 'mediaType']);
  const health = ['Test', 'Interval', 'Timeout', 'StartPeriod', 'StartInterval', 'Retries'];
  const historical = config?.container_config;
  canonicalMembers(historical, ['Hostname', 'Domainname', 'User', 'AttachStdin', 'AttachStdout', 'AttachStderr', 'ExposedPorts', 'Tty', 'OpenStdin', 'StdinOnce', 'Env', 'Cmd', 'Healthcheck', 'ArgsEscaped', 'Image', 'Volumes', 'WorkingDir', 'Entrypoint', 'NetworkDisabled', 'MacAddress', 'OnBuild', 'Labels', 'StopSignal', 'StopTimeout', 'Shell']);
  canonicalMembers(historical?.Healthcheck, health);
  canonicalMembers(config?.rootfs, ['type', 'diff_ids'], true);
  canonicalMembers(config?.config, ['User', 'WorkingDir', 'StopSignal', 'Env', 'Entrypoint', 'Cmd', 'OnBuild', 'Shell', 'ArgsEscaped', 'Labels', 'ExposedPorts', 'Volumes', 'Healthcheck'], true);
  canonicalMembers(config?.config?.Healthcheck, health, true);
  if (Array.isArray(config?.history)) for (const entry of config.history) canonicalMembers(entry, ['created', 'created_by', 'author', 'comment', 'empty_layer']);
}

export function verifyProof(image, manifest, config, approvedRepository) {
  const { digest } = imageParts(image, approvedRepository);
  if (!manifest.length || !config.length || manifest.length > maxProofBytes || config.length > maxProofBytes) throw new Error('proof size limit');
  if (hash(manifest) !== digest) throw new Error('manifest digest mismatch');
  const m = strictJSON(manifest), c = strictJSON(config);
  structuralMembers(m, c);
  if (m.schemaVersion !== 2 || !manifestTypes.includes(m.mediaType) || !m.config || !configTypes.includes(m.config.mediaType)
      || manifestTypes.indexOf(m.mediaType) !== configTypes.indexOf(m.config.mediaType) || !Array.isArray(m.layers)
      || 'manifests' in m || !digestPattern.test(m.config.digest) || !Number.isSafeInteger(m.config.size) || m.config.size !== config.length
      || hash(config) !== m.config.digest || c.os !== 'linux' || c.architecture !== 'amd64') throw new Error('invalid single-platform OCI proof');
  for (const d of [m, m.config, ...m.layers]) {
    if (!d || typeof d !== 'object' || 'urls' in d || 'data' in d) throw new Error('external or embedded descriptor rejected');
  }
  return m;
}

async function fetchRaw(url, accept) {
  const response = await fetch(url, { redirect: 'error', headers: { Accept: accept, 'Accept-Encoding': 'identity' }, signal: AbortSignal.timeout(30000) });
  if (response.status !== 200 || response.headers.has('content-encoding')) { await response.body?.cancel(); throw new Error('registry response rejected'); }
  const chunks = []; let size = 0;
  try {
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > maxProofBytes) throw new Error('proof size limit');
      chunks.push(chunk);
    }
  } catch (error) { throw new Error(`registry body rejected: ${error.message}`); }
  return { raw: Buffer.concat(chunks), type: response.headers.get('content-type')?.split(';')[0] };
}

// The optional origin is only a loopback, random-port fixture seam for tests;
// the CLI never accepts an origin override or arbitrary repository/URL.
export async function collectRuntimeProofs(image, output, origin, approvedRepository) {
  const { repository, digest } = imageParts(image, approvedRepository);
  const expectedOrigin = approvedRepository === undefined
    ? 'http://127.0.0.1:5000'
    : `http://${localRepository(approvedRepository).authority}`;
  if (approvedRepository !== undefined && origin !== undefined && origin !== expectedOrigin) throw new Error('approved repository origin required');
  origin ??= expectedOrigin;
  if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(origin) && origin !== expectedOrigin) throw new Error('local registry required');
  const m = await fetchRaw(`${origin}/v2/${repository}/manifests/${digest}`, manifestTypes.join(', '));
  if (!manifestTypes.includes(m.type)) throw new Error('manifest response media type rejected');
  const document = strictJSON(m.raw), descriptor = document.config;
  if (document.schemaVersion !== 2 || document.mediaType !== m.type || !Array.isArray(document.layers) || 'manifests' in document) throw new Error('single-platform manifest required');
  if (hash(m.raw) !== digest || !descriptor || !digestPattern.test(descriptor.digest) || !configTypes.includes(descriptor.mediaType)
      || !Number.isSafeInteger(descriptor.size) || descriptor.size < 1 || descriptor.size > maxProofBytes || 'urls' in descriptor || 'data' in descriptor) throw new Error('manifest descriptor rejected');
  const c = await fetchRaw(`${origin}/v2/${repository}/blobs/${descriptor.digest}`, descriptor.mediaType);
  verifyProof(image, m.raw, c.raw, approvedRepository);
  const root = resolve(output), destination = join(root, digest.slice(7));
  // Never repair or overwrite an existing proof directory, even if one file is
  // absent. prepare invokes this against a new isolated temporary directory.
  await mkdir(root, { recursive: true });
  const info = await lstat(root);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('invalid proof root');
  try {
    const existing = await lstat(destination);
    if (!existing.isDirectory() || existing.isSymbolicLink()) throw new Error('invalid existing proof directory');
    const names = (await readdir(destination)).sort();
    if (names.join(',') !== 'config.json,manifest.json') throw new Error('incomplete existing proof directory');
    for (const [name, raw] of [['manifest.json', m.raw], ['config.json', c.raw]]) {
      const path = join(destination, name), info = await lstat(path);
      if (!info.isFile() || info.isSymbolicLink() || !(await readFile(path)).equals(raw)) throw new Error('existing proof changed');
    }
    return destination;
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  // mkdir without recursive is the no-replacement boundary for first creation.
  await mkdir(destination);
  await writeFile(join(destination, 'manifest.json'), m.raw, { flag: 'wx', mode: 0o644 });
  await writeFile(join(destination, 'config.json'), c.raw, { flag: 'wx', mode: 0o644 });
  return destination;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = collectorArgs(process.argv.slice(2));
  await collectRuntimeProofs(args.image, args.output, undefined, args.repository);
}
