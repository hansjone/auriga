import { execFileSync } from 'node:child_process';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  renameSync,
  unlinkSync,
  chmodSync,
} from 'node:fs';
import { dirname, resolve, parse, join } from 'node:path';
import { randomUUID } from 'node:crypto';

export function safePath(path) {
  path = resolve(path);
  const root = parse(path).root;
  let current = root;
  for (const part of path.slice(root.length).split(/[\\/]/).filter(Boolean)) {
    current = join(current, part);
    if (existsSync(current) && lstatSync(current).isSymbolicLink())
      throw new Error('reparse path rejected');
  }
  return path;
}
export function privateDirectory(path, helper) {
  safePath(path);
  mkdirSync(path, { recursive: true, mode: 0o700 });
  safePath(path);
  if (process.platform === 'win32') {
    if (!helper || !existsSync(helper))
      throw new Error('Windows shell helper required');
    execFileSync(helper, ['secure', path], {
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
  } else {
    chmodSync(path, 0o700);
    if (lstatSync(path).uid !== process.getuid())
      throw new Error('wrong directory owner');
  }
  return path;
}
export class Vault {
  constructor(directory, helper) {
    this.directory = privateDirectory(resolve(directory), helper);
    this.helper = helper;
    this.file = join(this.directory, 'credentials.dpapi');
  }
  crypt(mode, value) {
    if (process.platform !== 'win32')
      throw new Error('protected credential storage unavailable on this OS');
    return Buffer.from(
      execFileSync(this.helper, [mode], {
        input: value.toString('base64') + '\n',
        windowsHide: true,
        maxBuffer: 2097152,
        stdio: ['pipe', 'pipe', 'pipe'],
      })
        .toString()
        .trim(),
      'base64',
    );
  }
  async load() {
    safePath(this.file);
    if (!existsSync(this.file)) return null;
    if (lstatSync(this.file).size > 1048576) throw new Error('vault limit');
    return JSON.parse(
      this.crypt('unprotect', readFileSync(this.file)).toString('utf8'),
    );
  }
  async save(value) {
    safePath(this.directory);
    safePath(this.file);
    const encrypted = this.crypt('protect', Buffer.from(JSON.stringify(value)));
    const temp = join(dirname(this.file), randomUUID() + '.tmp');
    try {
      writeFileSync(temp, encrypted, { flag: 'wx', mode: 0o600 });
      renameSync(temp, this.file);
    } finally {
      if (existsSync(temp)) unlinkSync(temp);
    }
  }
}
