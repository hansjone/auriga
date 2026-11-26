/**
 * Kept user data after uninstall must stay reusable on the next install.
 * Upstream dump-config always rewrites profiles/<name>/cordis.yml; Windows
 * read-only attributes and elevated-install DACLs commonly leave that file
 * unwritable and turn a keep-data reinstall into a smoke failure.
 */
import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  openSync,
  closeSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';
import { systemTool } from './system-tools.mjs';
import { spawnWinHelper } from './win-helper.mjs';

const PROFILE = 'auriga';

const PROFILE_REWRITE_FILES = [
  'cordis.yml',
  'cordis.patch.yml',
  'package.json',
];

const grantScript = `
$ErrorActionPreference = 'Stop'
$path = $env:AURIGA_DATA_HOME_PATH
if (-not [IO.Directory]::Exists($path)) { return }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$dirItem = Get-Item -LiteralPath $path
$acl = $dirItem.GetAccessControl([Security.AccessControl.AccessControlSections]::Access)
$inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
$propagate = [Security.AccessControl.PropagationFlags]::None
$rule = New-Object Security.AccessControl.FileSystemAccessRule(
  $identity.User, 'Modify', $inherit, $propagate, 'Allow')
$acl.AddAccessRule($rule)
[IO.Directory]::SetAccessControl($path, $acl)
`;

function clearReadOnly(file) {
  if (!existsSync(file)) return;
  try {
    // On Windows this clears FILE_ATTRIBUTE_READONLY; elsewhere it widens mode.
    chmodSync(file, 0o666);
  } catch {
    /* Probe below reports a durable failure. */
  }
  if (process.platform === 'win32') {
    // Node open('w') on Windows also fails with EPERM when FILE_ATTRIBUTE_HIDDEN
    // or FILE_ATTRIBUTE_SYSTEM is set; clear them best-effort via attrib.exe.
    try {
      spawnSync(systemTool('attrib'), ['-R', '-H', '-S', file], {
        windowsHide: true,
        timeout: 10000,
      });
    } catch {
      /* Probe below reports a durable failure. */
    }
  }
}

function grantCurrentUserModify(directory) {
  const result =
    spawnWinHelper(['grant-modify', directory], { timeout: 60000 }) ??
    spawnSync(
      systemTool('WindowsPowerShell/v1.0/powershell'),
      ['-NoProfile', '-NonInteractive', '-Command', grantScript],
      {
        env: { ...process.env, AURIGA_DATA_HOME_PATH: directory },
        windowsHide: true,
        encoding: 'utf8',
        timeout: 60000,
      },
    );
  if (result.status !== 0)
    throw Object.assign(
      new Error(
        `无法修复用户数据目录写权限，请关闭占用程序后重试，或检查杀软/受控文件夹访问。[desktop.data_home_access_denied]`,
      ),
      {
        code: 'desktop.data_home_access_denied',
        cause: result.error || new Error(result.stderr || 'grant failed'),
      },
    );
}

function probeRewrite(file) {
  mkdirSync(dirname(file), { recursive: true });
  assertLocalPath(file);
  clearReadOnly(file);
  const existed = existsSync(file);
  // Prefer opening for write like upstream writeFileSync(cordis.yml).
  let fd;
  try {
    fd = openSync(file, existed ? 'r+' : 'wx');
    if (!existed) writeFileSync(fd, '# auriga desktop writability probe\n');
  } catch (error) {
    throw Object.assign(
      new Error(
        `保留的用户数据目录无法写入（例如 cordis.yml 只读或 ACL 拒绝）。请关闭占用该目录的程序后重试；若仍失败，检查杀软或受控文件夹访问。[desktop.data_home_not_writable]`,
      ),
      {
        code: 'desktop.data_home_not_writable',
        cause: error,
      },
    );
  } finally {
    if (fd !== undefined) closeSync(fd);
  }
  if (!existed) {
    try {
      unlinkSync(file);
    } catch {
      /* Leave the probe file if cleanup races; install will rewrite it. */
    }
  }
}

/**
 * Make a previously kept data home usable before profile seed / smoke.
 * Preserves contents; only clears read-only bits, grants the current user
 * Modify on Windows, and probes the upstream cordis.yml rewrite path.
 */
export function ensureDataHomeWritable(dataHome) {
  dataHome = resolve(dataHome);
  assertLocalPath(dataHome);
  if (!existsSync(dataHome)) return { repaired: false };

  const profile = join(dataHome, 'profiles', PROFILE);
  const hadProfile = existsSync(profile);
  // Fresh installs must not create profile directories here: first-install cancel
  // and failure cleanup expect an empty or wholly owned data home.
  // Reject junctions/reparse points before any chmod or ACL mutation so a
  // linked profile cannot clear read-only bits on an outside target.
  const candidates = hadProfile
    ? [
        ...PROFILE_REWRITE_FILES.map((name) => join(profile, name)),
        join(dataHome, 'cordis.patch.yml'),
      ]
    : [];
  if (hadProfile) {
    assertLocalPath(profile);
    for (const file of candidates) {
      if (!existsSync(file)) continue;
      assertLocalPath(file);
      clearReadOnly(file);
    }
  }

  if (process.platform === 'win32') grantCurrentUserModify(dataHome);

  if (hadProfile) probeRewrite(join(profile, 'cordis.yml'));
  return { repaired: true };
}
