import { generateKeyPairSync, createPublicKey } from 'node:crypto';
import { Buffer } from 'node:buffer';
import {
  closeSync,
  mkdirSync,
  openSync,
  writeFileSync,
  statSync,
  readFileSync,
} from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { digest, physical, ROOT } from './inputs.mjs';

function windows(path, kind) {
  if (process.platform !== 'win32') return;
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([name]) => name.toLowerCase() !== 'psmodulepath',
    ),
  );
  const script =
    kind === 'verify'
      ? fileURLToPath(new URL('./verify-private-path.ps1', import.meta.url))
      : join(ROOT, 'scripts/resource-keys/protect-private-path.ps1');
  const result = spawnSync(
    join(
      process.env.SystemRoot ?? 'C:\\Windows',
      'System32/WindowsPowerShell/v1.0/powershell.exe',
    ),
    [
      '-NoProfile',
      '-NonInteractive',
      '-File',
      script,
      ...(kind === 'verify' ? [] : ['-Kind', kind]),
    ],
    {
      env: { ...env, AURIGA_RESOURCE_KEY_PATH: path },
      encoding: 'utf8',
      windowsHide: true,
    },
  );
  if (result.status !== 0) throw new Error('桌面私钥权限校验或保护失败');
}
export function assertPrivateKey(file) {
  physical(file);
  physical(dirname(file));
  if (!statSync(file).isFile() || statSync(file).size > 4096)
    throw new Error('非法私钥文件');
  if (process.platform === 'win32') {
    windows(file, 'verify');
    windows(dirname(file), 'verify');
  } else
    for (const path of [file, dirname(file)]) {
      const stat = statSync(path);
      if (stat.uid !== process.getuid() || stat.mode & 0o077)
        throw new Error('私钥及目录必须仅当前用户可访问');
    }
}
export function generateDesktopKeys(output) {
  output = resolve(output);
  physical(dirname(output));
  mkdirSync(output, { mode: 0o700 });
  windows(output, 'directory');
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const privateBytes = Buffer.from(
    privateKey.export({ type: 'pkcs8', format: 'pem' }),
  );
  try {
    for (const [name, bytes] of [
      ['desktop-private.pem', privateBytes],
      ['desktop-public.pem', publicKey.export({ type: 'spki', format: 'pem' })],
    ]) {
      const file = join(output, name);
      const fd = openSync(file, 'wx', 0o600);
      try {
        windows(file, 'file');
        writeFileSync(fd, bytes);
      } finally {
        closeSync(fd);
      }
    }
  } finally {
    privateBytes.fill(0);
  }
  assertPrivateKey(join(output, 'desktop-private.pem'));
  return {
    publicKeyFingerprint: fingerprint(
      readFileSync(join(output, 'desktop-public.pem')),
    ),
  };
}
export function fingerprint(pem) {
  const key = createPublicKey(pem);
  if (key.asymmetricKeyType !== 'ed25519')
    throw new Error('桌面发行公钥必须为 Ed25519');
  return digest(key.export({ type: 'spki', format: 'der' }));
}
