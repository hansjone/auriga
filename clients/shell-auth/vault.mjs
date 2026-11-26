import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';
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
  } else if (process.platform === 'linux') {
    chmodSync(path, 0o700);
    if (lstatSync(path).uid !== process.getuid())
      throw new Error('wrong directory owner');
  } else {
    chmodSync(path, 0o700);
    if (lstatSync(path).uid !== process.getuid())
      throw new Error('wrong directory owner');
  }
  return path;
}

const LINUX_SEAL_PREFIX = Buffer.from('auriga.shell-auth.aes.v1\0');

/** AES-256-GCM seal for Linux: key lives in a 0600 file under the vault dir. */
export function linuxSeal(key, plaintext) {
  const iv = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, iv);
  const body = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  const tag = cipher.getAuthTag();
  return Buffer.concat([LINUX_SEAL_PREFIX, iv, tag, body]);
}

export function linuxUnseal(key, sealed) {
  if (
    sealed.length < LINUX_SEAL_PREFIX.length + 12 + 16 + 1 ||
    !sealed.subarray(0, LINUX_SEAL_PREFIX.length).equals(LINUX_SEAL_PREFIX)
  )
    throw new Error('invalid sealed credentials');
  const iv = sealed.subarray(
    LINUX_SEAL_PREFIX.length,
    LINUX_SEAL_PREFIX.length + 12,
  );
  const tag = sealed.subarray(
    LINUX_SEAL_PREFIX.length + 12,
    LINUX_SEAL_PREFIX.length + 28,
  );
  const body = sealed.subarray(LINUX_SEAL_PREFIX.length + 28);
  const decipher = createDecipheriv('aes-256-gcm', key, iv);
  decipher.setAuthTag(tag);
  return Buffer.concat([decipher.update(body), decipher.final()]);
}

export class Vault {
  constructor(directory, helper) {
    this.directory = privateDirectory(resolve(directory), helper);
    this.helper = helper;
    this.file =
      process.platform === 'win32'
        ? join(this.directory, 'credentials.dpapi')
        : join(this.directory, 'credentials.sealed');
    this.keyFile = join(this.directory, 'credentials.key');
  }
  linuxKey() {
    safePath(this.keyFile);
    if (existsSync(this.keyFile)) {
      const key = readFileSync(this.keyFile);
      if (key.length !== 32) throw new Error('invalid vault key');
      const st = lstatSync(this.keyFile);
      if (st.uid !== process.getuid() || (st.mode & 0o077) !== 0)
        throw new Error('vault key permissions');
      return key;
    }
    const key = randomBytes(32);
    const temp = join(this.directory, randomUUID() + '.key.tmp');
    try {
      writeFileSync(temp, key, { flag: 'wx', mode: 0o600 });
      renameSync(temp, this.keyFile);
    } finally {
      if (existsSync(temp)) unlinkSync(temp);
    }
    return key;
  }
  crypt(mode, value) {
    if (process.platform === 'linux') {
      const key = this.linuxKey();
      return mode === 'protect'
        ? linuxSeal(key, value)
        : linuxUnseal(key, value);
    }
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
    const encrypted = this.crypt(
      'protect',
      Buffer.from(JSON.stringify(value)),
    );
    const temp = join(dirname(this.file), randomUUID() + '.tmp');
    try {
      writeFileSync(temp, encrypted, { flag: 'wx', mode: 0o600 });
      renameSync(temp, this.file);
    } finally {
      if (existsSync(temp)) unlinkSync(temp);
    }
  }
}
