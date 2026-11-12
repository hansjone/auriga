import { execFileSync } from 'node:child_process';
import { closeSync, fsyncSync, openSync, renameSync, statSync } from 'node:fs';
import { dirname, isAbsolute } from 'node:path';
/** Persistent same-volume replacements. Windows uses MoveFileEx WRITE_THROUGH.
 * No generated executable, native npm module or credential on argv.
 * @param {[string,string][]} moves */
export function commitMoves(moves) {
  if (!moves.length) return;
  for (const [source, target] of moves)
    if (
      !isAbsolute(source) ||
      !isAbsolute(target) ||
      statSync(source).dev !== statSync(dirname(target)).dev
    )
      throw new Error('same-volume absolute commit paths required');
  if (process.platform === 'win32') {
    const script = `Add-Type @"\nusing System; using System.Runtime.InteropServices;\npublic class AurigaResourceFile { [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] public static extern bool MoveFileEx(string a,string b,uint flags); }\n"@\n$taskReader=New-Object System.IO.StreamReader([Console]::OpenStandardInput(),[Text.Encoding]::UTF8)\n$taskMoves=ConvertFrom-Json ($taskReader.ReadToEnd())\nforeach($taskMove in $taskMoves){if(-not [AurigaResourceFile]::MoveFileEx($taskMove[0],$taskMove[1],9)){Write-Output ('{"win32":'+[Runtime.InteropServices.Marshal]::GetLastWin32Error()+'}');exit 1}}\n`;
    try {
      execFileSync(
        'powershell.exe',
        ['-NoProfile', '-NonInteractive', '-Command', script],
        {
          input: JSON.stringify(moves),
          encoding: 'utf8',
          windowsHide: true,
          timeout: 10000,
          maxBuffer: 32768,
        },
      );
    } catch (error) {
      let win32;
      try {
        win32 = JSON.parse(
          String(/** @type {any} */ (error).stdout).trim(),
        ).win32;
      } catch {
        /* Preserve an unclassified I/O failure. */
      }
      const code =
        win32 === 32 || win32 === 33
          ? 'EBUSY'
          : win32 === 5
            ? 'EACCES'
            : win32 === 145
              ? 'ENOTEMPTY'
              : win32 === 2 || win32 === 3
                ? 'ENOENT'
                : 'EIO';
      throw Object.assign(new Error('resource durable replacement failed'), {
        code,
        cause: error,
      });
    }
    return;
  }
  const parents = new Set();
  for (const [source, target] of moves) {
    renameSync(source, target);
    parents.add(dirname(source));
    parents.add(dirname(target));
  }
  for (const parent of parents) {
    const fd = openSync(parent, 'r');
    try {
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  }
}
