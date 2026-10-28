import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile } from 'node:fs/promises';

export async function currentNativeProcess() {
  const pid = process.pid,
    parentPid = process.ppid;
  if (process.platform === 'win32') {
    const command = `$owned = [Diagnostics.Process]::GetProcessById(${pid}); [ordered]@{ pid = $owned.Id; creationTime = $owned.StartTime.ToUniversalTime().ToString('o') } | ConvertTo-Json -Compress`;
    const { stdout } = await promisify(execFile)(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', command],
      { windowsHide: true, timeout: 10_000 },
    );
    const record = JSON.parse(stdout);
    assert.equal(record.pid, pid);
    assert.ok(record.creationTime);
    return { ...record, parentPid };
  }
  const stat = await readFile(`/proc/${pid}/stat`, 'utf8');
  const fields = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
  assert.equal(Number(fields[1]), parentPid);
  return {
    pid,
    parentPid,
    bootId: (await readFile('/proc/sys/kernel/random/boot_id', 'utf8')).trim(),
    startTicks: fields[19],
  };
}
