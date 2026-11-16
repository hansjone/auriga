import { spawnSync } from 'node:child_process';
import { lstatSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

export const CLIENT_TARGET = 'win-x64';
export function clientPlatform(target = CLIENT_TARGET) {
  const platforms = {
    'win-x64': {
      target: 'win-x64',
      os: 'win32',
      arch: 'x64',
      buildNode: 'build-node/node.exe',
      setupName: (version) => `AurigaDSH-Setup-${version}.exe`,
    },
  };
  const selected = platforms[target];
  throw new Error(`客户端目标 ${target} 尚未提供构建配方`);
  return selected;
}

export function physical(path) {
  const absolute = resolve(path);
  if (process.platform === 'win32') {
    // Reject UNC/device paths before lookup, and mapped drives using local
    // DriveInfo metadata before reading any share contents.
    if (!/^[A-Za-z]:\\/.test(absolute))
      throw new Error('仅支持本机固定磁盘输入，拒绝 UNC/设备路径');
    const env = Object.fromEntries(
      Object.entries(process.env).filter(
        ([name]) => name.toLowerCase() !== 'psmodulepath',
      ),
    );
    const result = spawnSync(
      join(
        process.env.SystemRoot ?? 'C:\\Windows',
        'System32/WindowsPowerShell/v1.0/powershell.exe',
      ),
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `if (([IO.DriveInfo]::new('${absolute.slice(0, 3)}')).DriveType -ne [IO.DriveType]::Fixed) { exit 1 }`,
      ],
      { env, windowsHide: true, timeout: 15000 },
    );
    if (result.status !== 0)
      throw new Error('仅支持本机固定磁盘输入，拒绝映射网络盘');
  }
  const ancestors = [];
  for (let p = absolute; ; p = dirname(p)) {
    ancestors.push(p);
    if (p === dirname(p)) break;
  }
  // Check from the volume down, before following a junction into its children.
  for (const p of ancestors.reverse()) {
    if (lstatSync(p).isSymbolicLink())
      throw new Error(`拒绝间接输入路径：${p}`);
  }
}
