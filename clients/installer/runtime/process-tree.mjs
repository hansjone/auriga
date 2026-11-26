import { BRAND_NAME } from './brand.mjs';
import { execFileSync, spawnSync } from 'node:child_process';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmdirSync,
  unlinkSync,
} from 'node:fs';
import { join, parse } from 'node:path';
import { randomUUID } from 'node:crypto';
import { assertLocalPath, durableJson, seedBootId } from './host-lock.mjs';
import { parseStrictJson } from './json.mjs';
import { execWinHelper } from './win-helper.mjs';

const ps = (code) =>
  execFileSync(
    'powershell.exe',
    [
      '-NoProfile',
      '-NonInteractive',
      '-Command',
      '$ErrorActionPreference="Stop"; ' + code,
    ],
    { encoding: 'utf8', timeout: 15000, windowsHide: true },
  );

let cachedNtfs = null;
let cachedStartupProcesses = null;

function validateNtfsInfo(info) {
  if (info.type !== 3 || info.fs !== 'NTFS')
    throw new Error('only local fixed NTFS volumes are supported');
  if (info.reparse) throw new Error('reparse point rejected');
}

/**
 * Run a single native helper invocation at startup to collect NTFS validation,
 * host bootId, and the initial process snapshot in one shot (~150-200ms total
 * instead of 10+ sequential PowerShell cold starts).
 */
export function primeStartupProbe(home) {
  if (process.platform !== 'win32') return false;
  assertLocalPath(home);
  const raw = execWinHelper(['startup-probe', home], { timeout: 15000 });
  if (raw === null) return false;
  const data = JSON.parse(raw);
  if (data.ntfs) {
    validateNtfsInfo(data.ntfs);
    cachedNtfs = { home: home.toLowerCase(), at: Date.now() };
  }
  if (data.bootId) seedBootId(data.bootId);
  if (Array.isArray(data.processes)) {
    const me = data.processes.find((p) => p.pid === process.pid);
    if (me) ownIdentity = me;
    cachedStartupProcesses = { list: data.processes, at: Date.now() };
  }
  return true;
}

export function assertNTFS(home) {
  assertLocalPath(home);
  if (process.platform !== 'win32')
    throw new Error('Desktop guardian requires local Windows NTFS');
  if (
    cachedNtfs &&
    cachedNtfs.home === home.toLowerCase() &&
    Date.now() - cachedNtfs.at < 5000
  )
    return;
  const root = parse(home).root;
  if (!/^[A-Za-z]:\\$/.test(root)) throw new Error('invalid local drive');
  const helperOut = execWinHelper(['ntfs-check', home], { timeout: 15000 });
  if (helperOut !== null) {
    validateNtfsInfo(JSON.parse(helperOut));
    return;
  }
  const drive = root[0];
  const info = JSON.parse(
    ps(
      `$d=Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='${drive}:'"; @{type=[int]$d.DriveType; fs=$d.FileSystem}|ConvertTo-Json -Compress`,
    ),
  );
  if (info.type !== 3 || info.fs !== 'NTFS')
    throw new Error('only local fixed NTFS volumes are supported');
  // FileAttributes.ReparsePoint also catches mount points that Node does not label symlinks.
  let path = root;
  for (const part of home.slice(root.length).split(/[\\/]/).filter(Boolean)) {
    path = join(path, part);
    if (!existsSync(path)) continue;
    // Pass the path as JSON-encoded PowerShell single-quoted literal, without interpolation.
    const literal = "'" + path.replaceAll("'", "''") + "'";
    if (
      ps(
        `if ((Get-Item -LiteralPath ${literal} -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { 'reparse' }`,
      ).trim()
    )
      throw new Error('reparse point rejected');
  }
}

export function processList() {
  if (cachedStartupProcesses && Date.now() - cachedStartupProcesses.at < 1500) {
    const list = cachedStartupProcesses.list;
    cachedStartupProcesses = null;
    return list;
  }
  const helperOut = execWinHelper(['proc-list'], { timeout: 15000 });
  if (helperOut !== null) {
    const list = JSON.parse(helperOut);
    return Array.isArray(list) ? list : [list];
  }
  const output = ps(
    "@(Get-CimInstance Win32_Process | ForEach-Object { @{pid=[int]$_.ProcessId; parent=[int]$_.ParentProcessId; createdAt=$_.CreationDate.ToUniversalTime().ToString('o'); executable=$_.ExecutablePath} }) | ConvertTo-Json -Compress",
  );
  const list = JSON.parse(output);
  return Array.isArray(list) ? list : [list];
}
let ownIdentity;
export function processIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid < 1)
    throw new Error('invalid process id');
  if (pid === process.pid && ownIdentity) return ownIdentity;
  try {
    const helperOut = execWinHelper(['proc-info', String(pid)], {
      timeout: 15000,
    });
    const output = (
      helperOut !== null
        ? helperOut
        : ps(
            `$p=Get-CimInstance Win32_Process -Filter "ProcessId=${pid}"; if ($p -and $p.CreationDate) { @{pid=[int]$p.ProcessId; createdAt=$p.CreationDate.ToUniversalTime().ToString('o'); executable=$p.ExecutablePath} | ConvertTo-Json -Compress }`,
          )
    ).trim();
    const identity = output ? JSON.parse(output) : null;
    if (pid === process.pid) ownIdentity = identity;
    return identity;
  } catch {
    return null;
  }
}
export const sameProcess = (a, b) =>
  a && b && a.pid === b.pid && a.createdAt === b.createdAt;
const same = sameProcess;
export function notifyPrivateOnly() {
  notifyDesktop('共享技能已停用，建议新建会话。');
}
export function notifySharedRestart() {
  notifyDesktop('共享技能状态已更新，Desktop 正在重新启动，建议新建会话。');
}
function notifyDesktop(message) {
  console.error(message);
  if (process.platform === 'win32')
    spawnSync(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `$w=New-Object -ComObject WScript.Shell; $null=$w.Popup('${message.replaceAll("'", "''")}',8,'${BRAND_NAME}',64)`,
      ],
      { windowsHide: true, timeout: 10000, stdio: 'ignore' },
    );
}

export class ProcessTree {
  constructor(home, list = processList) {
    this.directory = join(home, 'auriga', 'guardian');
    this.file = join(this.directory, 'process.json');
    this.list = list;
    assertLocalPath(this.directory);
    assertLocalPath(this.file);
    mkdirSync(this.directory, { recursive: true });
    this.record = existsSync(this.file)
      ? parseStrictJson(readFileSync(this.file), true)
      : { schemaVersion: 1, launcher: null, root: null, children: [] };
    if (
      this.record.schemaVersion !== 1 ||
      !Array.isArray(this.record.children) ||
      Object.keys(this.record).sort().join() !==
        'children,launcher,root,schemaVersion'
    )
      throw new Error('unknown process journal');
    for (const item of [
      this.record.root,
      this.record.launcher,
      ...this.record.children,
    ].filter(Boolean)) {
      if (
        !Number.isSafeInteger(item.pid) ||
        item.pid < 1 ||
        !Number.isFinite(Date.parse(item.createdAt))
      )
        throw new Error('invalid process identity');
    }
  }
  save() {
    durableJson(this.file, this.record);
  }
  singleton() {
    const directory = join(this.directory, 'supervisor.lock.d');
    assertLocalPath(directory);
    const owner = join(directory, 'owner.json');
    const processes = this.list();
    const me = processes.find((p) => p.pid === process.pid);
    if (!me) throw new Error('cannot establish launcher identity');
    try {
      mkdirSync(directory);
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      assertLocalPath(owner);
      const old = parseStrictJson(readFileSync(owner), true);
      if (
        !old.token ||
        !old.process ||
        !Number.isSafeInteger(old.process.pid) ||
        old.process.pid < 1 ||
        !Number.isFinite(Date.parse(old.process.createdAt))
      )
        throw new Error('unknown supervisor lock');
      if (
        same(
          processes.find((p) => p.pid === old.process.pid),
          old.process,
        )
      )
        throw new Error('guardian already running');
      // No writer can start through this launcher until the new singleton is acquired.
      const retired = `${directory}.${randomUUID()}.retired`;
      if (JSON.parse(readFileSync(owner)).token !== old.token)
        throw new Error('supervisor changed');
      renameSync(directory, retired);
      unlinkSync(join(retired, 'owner.json'));
      rmdirSync(retired);
      mkdirSync(directory);
    }
    const token = randomUUID();
    durableJson(owner, { token, process: me });
    this.record.launcher = me;
    this.save();
    return () => {
      if (JSON.parse(readFileSync(owner)).token !== token)
        throw new Error('supervisor ownership lost');
      unlinkSync(owner);
      rmdirSync(directory);
    };
  }
  attach(pid) {
    // Query only the newly spawned PID (~20-50ms) instead of enumerating every process on the host.
    const info =
      this.list === processList
        ? processIdentity(pid)
        : this.list().find((p) => p.pid === pid);
    if (!info)
      throw new Error('DSH exited before process identity was recorded');
    this.record.root = info;
    this.record.children = [];
    this.save();
  }
  refresh() {
    const processes = this.list();
    const known = [this.record.root, ...this.record.children].filter(Boolean);
    const live = known.filter((p) =>
      same(
        processes.find((x) => x.pid === p.pid),
        p,
      ),
    );
    const ids = new Set(live.map((p) => p.pid));
    for (let changed = true; changed;) {
      changed = false;
      for (const p of processes)
        if (
          ids.has(p.parent) &&
          !ids.has(p.pid) &&
          Date.parse(p.createdAt) >=
            Date.parse(live.find((parent) => parent.pid === p.parent).createdAt)
        ) {
          live.push(p);
          ids.add(p.pid);
          changed = true;
        }
    }
    const children = live.filter((p) => !same(p, this.record.root));
    if (JSON.stringify(children) !== JSON.stringify(this.record.children)) {
      this.record.children = children;
      this.save();
    }
    return live;
  }
  async stopTree() {
    if (!this.record.root && this.record.children.length === 0) return;
    const live = this.refresh();
    this.stoppedWriters = [this.record.root, ...this.record.children].filter(
      Boolean,
    );
    for (const owned of live) {
      const current = this.list().find((p) => p.pid === owned.pid);
      if (!same(current, owned)) continue;
      const result = spawnSync(
        'taskkill.exe',
        ['/PID', String(owned.pid), '/T', '/F'],
        { encoding: 'utf8', windowsHide: true, timeout: 15000 },
      );
      if (
        result.status !== 0 &&
        same(
          this.list().find((p) => p.pid === owned.pid),
          owned,
        )
      )
        throw new Error('owned process tree stop failed');
    }
    if (live.length > 0 && !(await this.isStopped()))
      throw new Error('owned processes remain alive');
    this.record.root = null;
    this.record.children = [];
    this.save();
  }
  async isStopped() {
    const known = [this.record.root, ...this.record.children].filter(Boolean);
    if (known.length === 0) return true;
    const processes = this.list();
    return !known.some((p) =>
      same(
        processes.find((x) => x.pid === p.pid),
        p,
      ),
    );
  }
}
