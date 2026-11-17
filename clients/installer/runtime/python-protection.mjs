import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { assertLocalPath } from './host-lock.mjs';
import { systemTool } from './system-tools.mjs';

// Explicit current-user SID avoids OWNER RIGHTS/default Administrators ownership.
// DELETE and WriteAttributes support Windows/libuv unlink during version cleanup.
// WriteData/AppendData deny file contents, directory entries and cache creation.
// Owners retain DACL control. Enabled SeRestorePrivilege with backup semantics
// can override write denies, including Node/libuv file opens on an elevated host.
// Package verification and launcher repair diagnostics cover those modified bytes.
const script = `
$ErrorActionPreference = 'Stop'
$path = $env:AURIGA_PYTHON_RUNTIME_DIRECTORY
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = New-Object Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
$inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
$propagate = [Security.AccessControl.PropagationFlags]::None
$allow = [Security.AccessControl.AccessControlType]::Allow
$system = New-Object Security.Principal.SecurityIdentifier('S-1-5-18')
$acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($system, 'FullControl', $inherit, $propagate, $allow)))
if ($env:AURIGA_PYTHON_RUNTIME_MODE -eq 'create') {
  $acl.SetOwner($sid)
  if ([IO.Directory]::Exists($path)) { throw 'Python runtime directory already exists' }
  $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, 'FullControl', $inherit, $propagate, $allow)))
  (New-Object IO.DirectoryInfo($path)).Create($acl)
} else {
  $rights = [Security.AccessControl.FileSystemRights]'ReadAndExecute, Delete, ChangePermissions, WriteAttributes'
  $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, $rights, $inherit, $propagate, $allow)))
  $denyWrite = [Security.AccessControl.FileSystemRights]'WriteData, AppendData, WriteExtendedAttributes'
  $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, $denyWrite, $inherit, $propagate, 'Deny')))
  (New-Object IO.DirectoryInfo($path)).SetAccessControl($acl)
}
`;

function apply(directory, mode) {
  assertLocalPath(directory);
  const result = spawnSync(
    systemTool('WindowsPowerShell/v1.0/powershell'),
    ['-NoProfile', '-NonInteractive', '-Command', script],
    {
      env: {
        ...process.env,
        AURIGA_PYTHON_RUNTIME_DIRECTORY: directory,
        AURIGA_PYTHON_RUNTIME_MODE: mode,
      },
      windowsHide: true,
      encoding: 'utf8',
      timeout: 120000,
    },
  );
  if (result.status !== 0)
    throw Object.assign(
      new Error(
        `Python运行时只读保护失败，请重新运行安装包修复。${result.stderr || result.error || ''}`,
      ),
      { code: 'desktop.python_runtime_protection_failed', repair: 'reinstall' },
    );
}

export function createPythonDirectory(version) {
  const directory = join(version, 'runtime/python');
  mkdirSync(dirname(directory), { recursive: true });
  if (process.platform === 'win32') apply(directory, 'create');
  else mkdirSync(directory);
}

export function protectPythonDirectory(version) {
  const directory = join(version, 'runtime/python');
  if (process.platform === 'win32' && existsSync(directory))
    apply(directory, 'seal');
}
