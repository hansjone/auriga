import { createHash, randomUUID } from 'node:crypto';
import {
  lstat,
  mkdir,
  open,
  readdir,
  readFile,
  rename,
  unlink,
  rmdir,
} from 'node:fs/promises';
import { isAbsolute, join, parse, relative, resolve, sep } from 'node:path';
import { TextDecoder } from 'node:util';
import { refuse } from './trust.js';

const decoder = new TextDecoder('utf-8', { fatal: true });
const namePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const hash = (/** @type {Uint8Array} */ bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
/** Strict authoring subset: JSON-quoted YAML scalars and literal booleans.
 * Never interpret YAML tags, aliases, duplicate keys or arbitrary object graphs.
 * @param {string} source */
export function validatePrivateSkill(source) {
  if (
    typeof source !== 'string' ||
    Buffer.byteLength(source) > 262144 ||
    source.includes('\0')
  )
    refuse('common.validation_failed');
  const match = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(source);
  if (!match) refuse('common.validation_failed');
  /** @type {Record<string,any>} */ const fields = Object.create(null);
  for (const line of match[1].split(/\r?\n/)) {
    if (!line.trim() || line.startsWith('#')) continue;
    const field = /^([a-zA-Z-]+):[ \t]*(.*)$/.exec(line);
    if (
      !field ||
      Object.hasOwn(fields, field[1]) ||
      ![
        'name',
        'description',
        'whenToUse',
        'disable-model-invocation',
        'user-invocable',
      ].includes(field[1])
    )
      refuse('common.validation_failed');
    const [, key, raw] = field;
    if (['disable-model-invocation', 'user-invocable'].includes(key)) {
      if (!['true', 'false'].includes(raw)) refuse('common.validation_failed');
      fields[key] = raw === 'true';
    } else {
      let value;
      if (raw.startsWith('"')) {
        try {
          value = JSON.parse(raw);
        } catch {
          refuse('common.validation_failed');
        }
      } else {
        if (!raw || /[[\]{}&*!|>:#'"`]/.test(raw))
          refuse('common.validation_failed');
        value = raw;
      }
      if (typeof value !== 'string' || !value.trim() || value.includes('\0'))
        refuse('common.validation_failed');
      fields[key] = value;
    }
  }
  if (
    typeof fields.name !== 'string' ||
    fields.name.length > 64 ||
    !namePattern.test(fields.name) ||
    typeof fields.description !== 'string' ||
    fields.description.length > 2048 ||
    (fields.whenToUse?.length ?? 0) > 2048
  )
    refuse('common.validation_failed');
  return {
    name: /** @type {string} */ (fields.name),
    description: /** @type {string} */ (fields.description),
  };
}

/** Local owner-only private store. No platform calls, shared roots or host config.
 * Paths come exclusively from trusted installation DSH_HOME, never browser input.
 * @param {{home:string}} options */
export function createPrivateSkills({ home }) {
  if (!isAbsolute(home) || resolve(home) !== home || home.startsWith('\\\\'))
    refuse('resource.storage_unavailable');
  const root = join(home, 'skills'),
    hidden = join(home, 'auriga', 'private-skills');
  /** Reject every existing ancestor link, including Windows junctions.
   * @param {string} path @param {boolean} [missing] */
  async function safe(path, missing = false) {
    if (relative(home, path).startsWith('..') || !isAbsolute(path))
      refuse('resource.storage_unavailable');
    let current = parse(path).root;
    for (const part of path.slice(current.length).split(sep)) {
      current = join(current, part);
      try {
        const stat = await lstat(current);
        if (stat.isSymbolicLink()) refuse('resource.storage_unavailable');
      } catch (error) {
        if (missing && /** @type {any} */ (error).code === 'ENOENT') return;
        throw error;
      }
    }
  }
  /** @param {string} name */
  function validName(name) {
    if (
      typeof name !== 'string' ||
      name.length > 64 ||
      !namePattern.test(name) ||
      /^(con|prn|aux|nul|com[0-9]|lpt[0-9])$/.test(name)
    )
      refuse('common.validation_failed');
    return name;
  }
  async function initialize() {
    await safe(home);
    await safe(hidden, true);
    await mkdir(hidden, { recursive: true });
    await safe(root, true);
    await mkdir(root, { recursive: true });
  }
  /** Cross-process private writes fail closed after crashes; no stale lock removal.
   * @template T @param {()=>Promise<T>} work */
  async function locked(work) {
    await initialize();
    const lock = join(hidden, 'write.lock.d');
    try {
      await mkdir(lock);
    } catch {
      refuse('common.conflict');
    }
    try {
      return await work();
    } finally {
      await rmdir(lock);
    }
  }
  /** @param {string} name @param {boolean} enabled */
  const directory = (name, enabled) =>
    join(enabled ? root : join(hidden, 'disabled'), validName(name));
  /** Bounded text-only snapshot; reject links, executable bits, unsafe names and case aliases.
   * @param {string} dir */
  async function snapshot(dir) {
    await safe(dir);
    /** @type {Record<string,string>} */ const files = Object.create(null);
    let size = 0,
      entries = 0;
    const seen = new Set();
    /** @param {string} parent @param {string} prefix */
    async function visit(parent, prefix) {
      for (const entry of await readdir(parent, { withFileTypes: true })) {
        const path = prefix + entry.name;
        if (
          ++entries > 512 ||
          Buffer.byteLength(path) > 240 ||
          path.split('/').length > 8 ||
          /[\\:]/.test(path) ||
          [...path].some((c) => c.charCodeAt(0) < 32) ||
          /[. ]$/.test(entry.name) ||
          /^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\.|$)/i.test(entry.name)
        )
          refuse('common.validation_failed');
        const key = path.normalize('NFC').toLowerCase();
        if (seen.has(key)) refuse('common.validation_failed');
        seen.add(key);
        const target = join(parent, entry.name),
          stat = await lstat(target);
        if (stat.isSymbolicLink()) refuse('common.validation_failed');
        if (stat.isDirectory()) {
          if (!['references', 'assets'].includes(path.split('/')[0]))
            refuse('common.validation_failed');
          await visit(target, path + '/');
        } else {
          if (
            !stat.isFile() ||
            stat.size > 2097152 ||
            (process.platform !== 'win32' && stat.mode & 0o111) ||
            (path !== 'SKILL.md' &&
              (!/^(references|assets)\//.test(path) ||
                !/\.(md|txt|json|yaml|yml)$/.test(path)))
          )
            refuse('common.validation_failed');
          const bytes = await readFile(target);
          if (bytes.length > 2097152 || (size += bytes.length) > 20971520)
            refuse('common.validation_failed');
          const content = decoder.decode(bytes);
          if (content.includes('\0')) refuse('common.validation_failed');
          files[path] = content;
        }
      }
    }
    await visit(dir, '');
    const metadata = validatePrivateSkill(files['SKILL.md']);
    const ordered = Object.keys(files)
      .sort()
      .map((key) => [key, files[key]]);
    return {
      ...metadata,
      files,
      revision: hash(Buffer.from(JSON.stringify(ordered))),
    };
  }
  /** @param {string} name @param {boolean} enabled */
  async function read(name, enabled) {
    const value = await snapshot(directory(name, enabled));
    if (value.name !== name) refuse('common.validation_failed');
    return { ...value, enabled };
  }
  return {
    async list() {
      await initialize();
      const result = [];
      for (const enabled of [true, false]) {
        const parent = enabled ? root : join(hidden, 'disabled');
        await safe(parent, true);
        let entries;
        try {
          entries = await readdir(parent, { withFileTypes: true });
        } catch (error) {
          if (/** @type {any} */ (error).code === 'ENOENT') continue;
          throw error;
        }
        if (entries.length > 1000) refuse('resource.storage_unavailable');
        for (const entry of entries) {
          if (
            !entry.isDirectory() ||
            entry.isSymbolicLink() ||
            !namePattern.test(entry.name)
          )
            continue;
          try {
            const value = await read(entry.name, enabled);
            result.push({
              name: value.name,
              description: value.description,
              revision: value.revision,
              enabled,
            });
          } catch {
            result.push({ name: entry.name, enabled, unavailable: true });
          }
        }
      }
      return result;
    },
    read,
    /** Explicit create/import. No overwrite and no archive execution.
     * @param {string} source */
    async create(source) {
      const { name } = validatePrivateSkill(source);
      validName(name);
      return locked(async () => {
        const dir = directory(name, true);
        await safe(dir, true);
        await mkdir(dir);
        const file = await open(join(dir, 'SKILL.md'), 'wx', 0o600);
        try {
          await file.writeFile(source, 'utf8');
          await file.sync();
        } finally {
          await file.close();
        }
        return read(name, true);
      });
    },
    /** CAS checks include all attachments; never overwrite an externally edited bundle.
     * @param {{name:string,enabled:boolean,revision:string,action:string,source?:string}} input */
    change(input) {
      return locked(async () => {
        const { name, enabled, revision, action } = input;
        if (typeof enabled !== 'boolean') refuse('common.validation_failed');
        const before = await read(name, enabled);
        if (before.revision !== revision) refuse('common.conflict');
        const dir = directory(name, enabled);
        if (action === 'edit') {
          if (validatePrivateSkill(input.source ?? '').name !== name)
            refuse('common.validation_failed');
          const temp = join(dir, '.auriga-' + randomUUID());
          const handle = await open(temp, 'wx', 0o600);
          try {
            await handle.writeFile(input.source ?? '', 'utf8');
            await handle.sync();
          } finally {
            await handle.close();
          }
          try {
            await rename(temp, join(dir, 'SKILL.md'));
          } catch (error) {
            await unlink(temp);
            throw error;
          }
          return read(name, enabled);
        }
        if (!['enable', 'disable', 'remove', 'freeze'].includes(action))
          refuse('common.validation_failed');
        if (action === 'freeze') {
          const id = randomUUID(),
            parent = join(hidden, 'frozen');
          await safe(parent, true);
          await mkdir(parent, { recursive: true });
          const file = await open(join(parent, id + '.json'), 'wx', 0o600);
          try {
            await file.writeFile(JSON.stringify(before));
            await file.sync();
          } finally {
            await file.close();
          }
          return { snapshotId: id, revision: before.revision };
        }
        if (
          (action === 'enable' && enabled) ||
          (action === 'disable' && !enabled)
        )
          return before;
        const target =
          action === 'remove'
            ? join(hidden, 'removed', randomUUID())
            : directory(name, action === 'enable');
        const parent = resolve(target, '..');
        await safe(parent, true);
        await mkdir(parent, { recursive: true });
        await safe(target, true);
        try {
          await lstat(target);
          refuse('common.conflict');
        } catch (error) {
          if (/** @type {any} */ (error).code !== 'ENOENT') throw error;
        }
        // Preserve the complete user bundle, including attachments, outside discovery.
        await rename(dir, target);
        return {
          name,
          enabled: action === 'enable',
          removed: action === 'remove',
        };
      });
    },
  };
}
