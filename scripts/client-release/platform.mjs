import { spawnSync } from 'node:child_process';
import { lstatSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

/** Default remains Windows until site tooling selects a target explicitly. */
export const CLIENT_TARGET = 'win-x64';

const PLATFORMS = {
  'win-x64': {
    target: 'win-x64',
    os: 'win32',
    arch: 'x64',
    npmOs: 'win32',
    npmCpu: 'x64',
    nodeArchive: (version) => `node-v${version}-win-x64.zip`,
    nodeExtractDir: (version) => `node-v${version}-win-x64`,
    nodeBin: 'node.exe',
    nodeShaField: 'nodeWinX64Sha256',
    buildNode: 'build-node/node.exe',
    libreofficeKit: 'libreoffice-kit-win32-x64',
    setupName: (version) => `AurigaDSH-Setup-${version}.exe`,
    stagedPackageName: 'desktop.exe',
    packageMedia: 'exe',
    shellFamily: 'windows',
  },
  'linux-x64': {
    target: 'linux-x64',
    os: 'linux',
    arch: 'x64',
    npmOs: 'linux',
    npmCpu: 'x64',
    nodeArchive: (version) => `node-v${version}-linux-x64.tar.xz`,
    nodeExtractDir: (version) => `node-v${version}-linux-x64`,
    nodeBin: 'bin/node',
    nodeShaField: 'nodeLinuxX64Sha256',
    buildNode: 'build-node/bin/node',
    libreofficeKit: 'libreoffice-kit-linux-x64',
    setupName: (version) => `auriga-desktop-${version}-linux-x64.tar.gz`,
    stagedPackageName: 'desktop.tar.gz',
    packageMedia: 'tar.gz',
    shellFamily: 'linux',
  },
};

export function supportedClientTargets() {
  return Object.keys(PLATFORMS);
}

export function clientPlatform(target = CLIENT_TARGET) {
  const selected = PLATFORMS[target];
  if (!selected) throw new Error(`客户端目标 ${target} 尚未提供构建配方`);
  return selected;
}

/** Map the running OS/arch to a Desktop target when packaging locally. */
export function hostClientTarget() {
  if (process.platform === 'win32' && process.arch === 'x64') return 'win-x64';
  if (process.platform === 'linux' && process.arch === 'x64') return 'linux-x64';
  throw new Error(
    `本机 ${process.platform}/${process.arch} 尚无 Desktop 客户端构建配方`,
  );
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
  } else if (process.platform === 'linux') {
    if (!absolute.startsWith('/') || absolute.startsWith('//'))
      throw new Error('仅支持本机绝对路径输入');
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
