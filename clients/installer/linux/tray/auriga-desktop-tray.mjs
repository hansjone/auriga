#!/usr/bin/env node
/**
 * Linux Desktop tray substitute. Owns `cli.mjs launch --stdin-control` the same
 * way the Windows WinForms tray does: open the local DSH UI, forward stop /
 * login-fallback, and exit cleanly. Uses notify-send instead of a system tray
 * icon until a native indicator is added.
 */
import { spawn } from 'node:child_process';
import {
  closeSync,
  existsSync,
  mkdirSync,
  openSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const BRAND = 'Auriga DSH';
const PORT = 12721;

function usage() {
  console.error(`Usage:
  auriga-desktop-tray [--root <install-root>]
  auriga-desktop-tray --stop <install-root>
`);
  process.exit(2);
}

function parseArgs(argv) {
  const out = { root: null, stop: false };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--root') {
      out.root = argv[++i];
    } else if (argv[i] === '--stop') {
      out.stop = true;
      out.root = argv[++i] ?? out.root;
    } else if (argv[i] === '--help' || argv[i] === '-h') usage();
    else if (!out.root && !argv[i].startsWith('-')) out.root = argv[i];
    else usage();
  }
  return out;
}

function notify(message) {
  try {
    spawn('notify-send', [`--app-name=${BRAND}`, BRAND, message], {
      stdio: 'ignore',
      detached: true,
    }).unref();
  } catch {
    console.error(message);
  }
}

function openUi() {
  const url = `http://127.0.0.1:${PORT}/`;
  try {
    const child = spawn('xdg-open', [url], {
      stdio: 'ignore',
      detached: true,
    });
    child.unref();
  } catch {
    notify(`请在浏览器打开 ${url}`);
  }
}

function currentVersion(root) {
  return readFileSync(join(root, 'current.txt'), 'utf8').trim();
}

function nodeBin(root) {
  return join(
    root,
    'versions',
    currentVersion(root),
    'upstream',
    'node',
    'bin',
    'node',
  );
}

function cliPath(root) {
  return join(
    root,
    'versions',
    currentVersion(root),
    'auriga',
    'runtime',
    'cli.mjs',
  );
}

function lockPath(root) {
  const digest = createHash('sha256')
    .update(resolve(root).toLowerCase())
    .digest('hex')
    .slice(0, 16);
  const dir = join(
    process.env.XDG_RUNTIME_DIR || '/tmp',
    'auriga-desktop-tray',
  );
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  return join(dir, `lock-${digest}`);
}

function acquireSingleton(root) {
  const file = lockPath(root);
  const fd = openSync(file, 'a+', 0o600);
  try {
    // Soft exclusive marker: write our pid; refuse if live owner exists.
    const previous = readFileSync(file, 'utf8').trim();
    if (previous) {
      const pid = Number(previous);
      if (Number.isSafeInteger(pid) && pid > 0 && pid !== process.pid) {
        try {
          process.kill(pid, 0);
          closeSync(fd);
          throw new Error('tray already running');
        } catch (error) {
          if (error.code !== 'ESRCH' && error.message !== 'tray already running')
            throw error;
          if (error.message === 'tray already running') throw error;
        }
      }
    }
    writeFileSync(file, String(process.pid));
  } catch (error) {
    closeSync(fd);
    throw error;
  }
  return () => {
    try {
      writeFileSync(file, '');
    } catch {
      /* ignore */
    }
    closeSync(fd);
  };
}

function stopSignalFile(root) {
  return join(
    process.env.XDG_RUNTIME_DIR || '/tmp',
    'auriga-desktop-tray',
    `stop-${createHash('sha256').update(resolve(root)).digest('hex').slice(0, 16)}`,
  );
}

async function requestStop(root) {
  const file = stopSignalFile(root);
  mkdirSync(dirname(file), { recursive: true, mode: 0o700 });
  writeFileSync(file, String(Date.now()), { mode: 0o600 });
  // Wait briefly for a running tray to notice; uninstall continues regardless.
  await delay(1500);
}

async function runTray(root) {
  if (!existsSync(join(root, 'install.json')))
    throw new Error('desktop installation missing');
  const release = acquireSingleton(root);
  let launcher = null;
  let stopping = false;
  const stopFile = stopSignalFile(root);

  const send = (command) => {
    if (!launcher || launcher.killed || !launcher.stdin?.writable) return;
    try {
      launcher.stdin.write(command + '\n');
    } catch {
      /* ignore */
    }
  };

  const shutdown = async () => {
    if (stopping) return;
    stopping = true;
    send('stop');
    const deadline = Date.now() + 8000;
    while (launcher && !launcher.killed && Date.now() < deadline)
      await delay(100);
    if (launcher && !launcher.killed) {
      try {
        launcher.kill('SIGTERM');
      } catch {
        /* ignore */
      }
    }
    release();
    try {
      if (existsSync(stopFile)) writeFileSync(stopFile, '');
    } catch {
      /* ignore */
    }
  };

  process.on('SIGINT', () => void shutdown().then(() => process.exit(0)));
  process.on('SIGTERM', () => void shutdown().then(() => process.exit(0)));

  const watcher = setInterval(() => {
    if (existsSync(stopFile) && readFileSync(stopFile, 'utf8').trim())
      void shutdown().then(() => process.exit(0));
  }, 500);
  watcher.unref?.();

  launcher = spawn(
    nodeBin(root),
    [
      cliPath(root),
      'launch',
      '--root',
      root,
      '--stdin-control',
      '--',
      '--no-open',
      '--port',
      String(PORT),
    ],
    { stdio: ['pipe', 'pipe', 'pipe'] },
  );
  let buffer = '';
  let opened = false;
  launcher.stdout.setEncoding('utf8');
  launcher.stdout.on('data', (chunk) => {
    buffer += chunk;
    const lines = buffer.split('\n');
    buffer = lines.pop();
    for (const line of lines) {
      if (!line.startsWith('@@auriga ')) continue;
      let event;
      try {
        event = JSON.parse(line.slice('@@auriga '.length));
      } catch {
        continue;
      }
      if (event.event === 'ready' || event.phase === 'ready' || event.ready) {
        if (!opened) {
          opened = true;
          openUi();
          notify('Auriga Desktop 已启动');
        }
      }
      if (event.event === 'fallback-login' && event.opened)
        notify('已打开平台登录页');
    }
  });
  launcher.stderr.on('data', (chunk) => process.stderr.write(chunk));
  launcher.on('exit', (code) => {
    clearInterval(watcher);
    release();
    if (!stopping) {
      notify(`Desktop 已退出 (${code ?? 'signal'})`);
      process.exit(code ?? 1);
    }
  });

  // If status events never arrive, still open the UI after a short wait.
  await delay(2500);
  if (!opened && !stopping) {
    opened = true;
    openUi();
  }

  await new Promise((resolve) => launcher.on('exit', resolve));
  await shutdown();
}

const args = parseArgs(process.argv.slice(2));
const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(
  args.root ??
    process.env.AURIGA_INSTALL_DIR ??
    join(here, '..', '..', '..', '..'),
);

try {
  if (args.stop) await requestStop(root);
  else await runTray(root);
} catch (error) {
  console.error(error.message || error);
  process.exit(1);
}
