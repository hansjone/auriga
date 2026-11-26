/**
 * Runtime Desktop host platform facts. Kept inside the installer closure so
 * packaged CLI/update code never imports scripts/client-release/.
 */
const PLATFORMS = {
  'win-x64': {
    target: 'win-x64',
    stagedPackageName: 'desktop.exe',
    packageMedia: 'exe',
  },
  'linux-x64': {
    target: 'linux-x64',
    stagedPackageName: 'desktop.tar.gz',
    packageMedia: 'tar.gz',
  },
};

export function hostDesktopTarget() {
  if (process.platform === 'win32' && process.arch === 'x64') return 'win-x64';
  if (process.platform === 'linux' && process.arch === 'x64')
    return 'linux-x64';
  return 'win-x64';
}

export function desktopPlatform(target = hostDesktopTarget()) {
  return PLATFORMS[target] ?? PLATFORMS['win-x64'];
}
