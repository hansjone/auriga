import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, readdir, readFile, writeFile, lstat, readlink } from 'node:fs/promises';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';

export const HERE = dirname(fileURLToPath(import.meta.url));
export const sha256 = value => createHash('sha256').update(value).digest('hex');
export const redact = value => value.replace(/([?&]token=)[^\s"'<>]+/g, '$1<redacted>');
export const json = async path => JSON.parse(await readFile(path, 'utf8'));
export async function writeJson(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, JSON.stringify(value, null, 2) + '\n');
}
export function options(argv, allowed) {
  const result = {};
  for (let i = 0; i < argv.length; i += 2) {
    const key = argv[i]?.replace(/^--/, '');
    if (!argv[i]?.startsWith('--') || !allowed.includes(key) || !argv[i + 1] || argv[i + 1].startsWith('--') || key in result) {
      throw new Error(`Expected --${allowed.join(' / --')} <value>; invalid argument ${argv[i]}`);
    }
    result[key] = argv[i + 1];
  }
  return result;
}
export function within(root, path) {
  const rel = relative(resolve(root), resolve(path));
  if (!rel || rel.startsWith('..' + sep) || rel === '..' || resolve(path) === rel || /^[A-Za-z]:/.test(rel)) {
    throw new Error(`Path must be a child of the spike directory: ${path}`);
  }
  return resolve(path);
}
export async function hashFile(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}
export async function run(command, args, opts = {}) {
  const child = spawn(command, args, { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], ...opts });
  let output = '';
  for (const stream of [child.stdout, child.stderr]) stream?.on('data', data => {
    output = (output + data).slice(-200000);
    if (opts.verbose) process.stderr.write(data);
  });
  const [code] = await once(child, 'exit');
  if (code !== 0) throw new Error(`${command} ${args[0]} exited ${code}\n${output}`);
  return output.trim();
}
// Reject escaping links; files are ordered by codepoint rather than host locale.
export async function inventory(root, prefix = '', hashes = true) {
  const result = [];
  for (const name of (await readdir(join(root, prefix))).sort()) {
    const path = prefix ? `${prefix}/${name}` : name;
    const full = join(root, path);
    const stat = await lstat(full);
    if (stat.isSymbolicLink()) {
      const target = (await readlink(full)).replaceAll('\\', '/');
      within(root, resolve(dirname(full), target));
      if (target.startsWith('/') || /^[A-Za-z]:/.test(target)) throw new Error(`Absolute link: ${path}`);
      result.push({ path, type: 'symlink', target, sha256: sha256(target), size: 0, mode: '0777' });
    } else if (stat.isDirectory()) {
      result.push({ path, type: 'directory', size: 0, mode: '0755' });
      result.push(...await inventory(root, path, hashes));
    } else if (stat.isFile()) {
      // Preserve executability across Windows cross builds; npm tar extraction loses Unix modes.
      const executable = path === 'node/bin/node' || path === 'bin/dsh' || /\/(?:bin|prebuilds)\//.test(path) || /\.(?:node|exe)$/.test(path);
      result.push({ path, type: 'file', size: stat.size, ...(hashes ? { sha256: await hashFile(full) } : {}), mode: executable ? '0755' : '0644' });
    } else throw new Error(`Unsupported filesystem entry: ${path}`);
  }
  return result;
}
export async function checkManifest(root) {
  const manifest = await json(join(root, 'manifest.json'));
  const actual = (await inventory(root)).filter(f => f.path !== 'manifest.json');
  // Permission enforcement belongs to the runtime driver; compare contents and entry types here.
  const comparable = files => files.map(({ mode, ...entry }) => entry);
  if (JSON.stringify(comparable(actual)) !== JSON.stringify(comparable(manifest.files))) throw new Error('Manifest file set/content mismatch');
  return manifest;
}
