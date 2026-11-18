/** Real tray process against a stand-in launcher: single instance, open/login, clean exit. */
import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { compileStub, compileTray } from '../scripts/make-exe.mjs';
import { tempBase } from './helpers.mjs';

const windows = process.platform === 'win32';
const here = fileURLToPath(new URL('.', import.meta.url));
const icon = resolve(here, '../../../assets/brand/auriga-dsh.ico');

const CRLF = (file) => readFileSync(file, 'latin1').replace(/\r?\n/g, '\r\n');

test('launchers keep the exit line where older launchers resume after an update', () => {
  const launcher = CRLF(join(here, '../windows/launcher/auriga-desktop.cmd'));
  const rollback = CRLF(join(here, '../windows/launcher/rollback.cmd'));
  // cmd.exe re-reads a running batch file at its old byte offset. Released
  // 0.1.x launchers resume at byte 244 (auriga-desktop.cmd) and 195 (rollback.cmd).
  assert.equal(launcher.indexOf('exit /b %ERRORLEVEL%\r\n'), 244);
  assert.equal(launcher.slice(242, 244), '\r\n');
  assert.equal(rollback.indexOf('exit /b %ERRORLEVEL%\r\n'), 195);
  assert.equal(rollback.slice(193, 195), '\r\n');
  // Long-running commands finish on their own line and never re-read the file.
  for (const text of [launcher, rollback])
    for (const line of text.split('\r\n').filter((l) => /node\.exe/.test(l)))
      assert.match(line, /& call exit \/b %%ERRORLEVEL%%$/);
  assert.match(launcher, /start "" "%VER%\\auriga\\tray\\AurigaDesktop\.exe"/);
  assert.match(rollback, /rollback\.ps1".*& call exit \/b %%ERRORLEVEL%%/);
});

test(
  'setup stub and tray compile with the Windows C# compiler',
  { skip: !windows, timeout: 120000 },
  (t) => {
    const base = tempBase(t);
    compileStub(join(base, 'stub.exe'), icon);
    compileTray(join(base, 'tray', 'AurigaDesktop.exe'), icon);
    assert.ok(existsSync(join(base, 'stub.exe')));
    assert.ok(existsSync(join(base, 'tray', 'AurigaDesktop.exe')));
  },
);

/** Named-object key shared by tray and setup. */
function key(root) {
  const normal = resolve(root).replace(/\\+$/, '').toLowerCase();
  return (
    'Local\\AurigaDesktop-' +
    createHash('sha256')
      .update(normal)
      .digest()
      .subarray(0, 8)
      .toString('hex')
      .toUpperCase()
  );
}

function signal(name) {
  return spawnSync(
    'powershell.exe',
    [
      '-NoProfile',
      '-Command',
      `$h=$null; if([System.Threading.EventWaitHandle]::TryOpenExisting('${name}',[ref]$h)){ $h.Set() | Out-Null; 'set' } else { 'missing' }`,
    ],
    { encoding: 'utf8' },
  ).stdout.trim();
}

function alive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function until(check, ms = 20000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    const value = check();
    if (value) return value;
    await delay(100);
  }
  throw new Error('timed out');
}

test(
  'tray: single instance, DSH sign-in modal, open hand-off and clean stop of the launcher',
  { skip: !windows, timeout: 120000 },
  async (t) => {
    const base = tempBase(t);
    const root = join(base, 'install');
    const id = 'test-1';
    const version = join(root, 'versions', id);
    const tray = join(version, 'auriga', 'tray', 'AurigaDesktop.exe');
    compileTray(tray, icon);
    mkdirSync(join(version, 'upstream', 'node'), { recursive: true });
    mkdirSync(join(version, 'auriga', 'runtime'), { recursive: true });
    copyFileSync(
      process.execPath,
      join(version, 'upstream', 'node', 'node.exe'),
    );
    writeFileSync(join(root, '.auriga-desktop-install'), '{}');
    writeFileSync(join(root, 'current.txt'), id + '\n');
    writeFileSync(
      join(root, 'shell.json'),
      JSON.stringify({ shortcuts: [], registryKey: 'AurigaDesktop-tray-test' }),
    );
    const journal = join(base, 'journal.log');
    const opened = join(base, 'opened.log');
    const env = { ...process.env, AURIGA_TEST_OPEN_LOG: opened };
    // Stand-in for runtime/cli.mjs: status, and a launcher speaking the control protocol.
    writeFileSync(
      join(version, 'auriga', 'runtime', 'cli.mjs'),
      `import { appendFileSync } from 'node:fs';
const log = (s) => appendFileSync(${JSON.stringify(journal)}, s + '\\n');
const [command] = process.argv.slice(2);
if (command === 'status') {
  console.log(JSON.stringify({ version: '9.9.9', dataHome: ${JSON.stringify(join(base, 'home'))}, logs: ${JSON.stringify(join(base, 'home', 'logs'))}, platformOrigin: null, updatesConfigured: false, update: { enabled: true, available: null }, components: {} }));
} else if (command === 'launch') {
  log('launch ' + process.pid + ' ' + process.argv.slice(2).join(' '));
  console.log('DSH ready at http://127.0.0.1:1/?token=abcdefgh');
  console.log('@@auriga ' + JSON.stringify({ event: 'platform', state: 'signed_out' }));
  let buffer = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (c) => {
    buffer += c;
    const lines = buffer.split('\\n'); buffer = lines.pop();
    for (const l of lines) {
      const cmd = l.trim();
      log('command ' + cmd);
      if (cmd === 'login') console.log('@@auriga ' + JSON.stringify({ event: 'login', opened: true }));
      if (cmd === 'stop') { log('stopped'); process.exit(0); }
    }
  });
  process.stdin.on('end', () => { log('stdin-end'); process.exit(0); });
}
`,
    );
    const journalText = () =>
      existsSync(journal) ? readFileSync(journal, 'utf8') : '';
    const first = spawn(tray, [], { stdio: 'ignore', env });
    t.after(() => {
      if (alive(first.pid))
        spawnSync('taskkill', ['/PID', String(first.pid), '/T', '/F']);
    });
    const launchLine = await until(() =>
      journalText()
        .split('\n')
        .find((l) => l.startsWith('launch ')),
    );
    assert.match(launchLine, /--stdin-control -- --no-open/);
    const launcherPid = Number(launchLine.split(' ')[1]);
    // Startup opens DSH directly and never requests the loopback login page.
    await delay(500);
    assert.doesNotMatch(journalText(), /command login/);
    // A second start reopens DSH without asking the launcher for browser login.
    const second = spawnSync(tray, [], { timeout: 20000, env });
    assert.equal(second.status, 0);
    assert.doesNotMatch(journalText(), /command login/);
    assert.equal(
      journalText()
        .split('\n')
        .filter((l) => l.startsWith('launch ')).length,
      1,
      'second start must not create another launcher',
    );
    assert.equal(signal(key(root) + '-exit'), 'set');
    await until(() => !alive(first.pid), 30000);
    await until(() => !alive(launcherPid), 10000);
    assert.match(journalText(), /command stop\nstopped/);
    assert.equal(signal(key(root) + '-exit'), 'missing');
    // Both starts hand DSH to the browser, recorded instead of really opened.
    assert.match(
      readFileSync(opened, 'utf8'),
      /^http:\/\/127\.0\.0\.1:1\/\?token=abcdefgh/m,
    );
  },
);
