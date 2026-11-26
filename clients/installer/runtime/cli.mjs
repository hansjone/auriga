/** Command line entry of the installer core, run with the bundled Node. */
import { closeSync, existsSync, openSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  CancelledError,
  install,
  launch,
  rollback,
  uninstall,
  pythonCommand,
} from './core.mjs';
import { desktopInfo, updateCommand } from './desktop-control.mjs';
import { createControl } from './tray-control.mjs';
import { pythonUserFailureReason } from './python-user.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const [command, ...rest] = process.argv.slice(2);

function options(argv) {
  const values = {};
  const positional = [];
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--') {
      positional.push(...argv.slice(i + 1));
      break;
    }
    if (argv[i].startsWith('--')) {
      const key = argv[i].slice(2);
      const next = argv[i + 1];
      if (next === undefined || next.startsWith('--')) values[key] = true;
      else {
        values[key] = next;
        i += 1;
      }
    } else positional.push(argv[i]);
  }
  return { values, positional };
}

try {
  const { values, positional } = options(rest);
  if (command === 'install') {
    const payload = values.payload ?? join(here, '..', '..');
    const defaultsFile = join(payload, 'auriga', 'defaults.json');
    const defaults = existsSync(defaultsFile)
      ? JSON.parse(readFileSync(defaultsFile, 'utf8'))
      : {};
    const cancelFile =
      typeof values['cancel-file'] === 'string' ? values['cancel-file'] : null;
    const result = install({
      ...(values.progress === true
        ? {
            // Machine-readable steps for the setup wizard (no paths or secrets).
            progress: (stage, fraction) =>
              console.log(`@@progress ${JSON.stringify({ stage, fraction })}`),
          }
        : {}),
      ...(cancelFile
        ? {
            cancelled: () => existsSync(cancelFile),
            beginCommit() {
              // Cancel and commit compete for the same exclusive decision file.
              try {
                closeSync(openSync(cancelFile, 'wx', 0o600));
              } catch (error) {
                if (error.code === 'EEXIST') throw new CancelledError();
                throw error;
              }
            },
          }
        : {}),
      payload,
      root: values.root,
      dataHome: values['data-home'],
      movePayload: values['move-payload'] === true,
      platformBinding:
        typeof values['platform-origin'] === 'string'
          ? {
              accessMode: values['platform-access-mode'],
              confirmedOrigin: values['confirm-platform-origin'],
            }
          : defaults.platformBinding,
      platformOrigin:
        typeof values['platform-origin'] === 'string'
          ? values['platform-origin']
          : defaults.platformOrigin,
    });
    console.log(JSON.stringify(result));
  } else if (command === 'rollback') {
    console.log(JSON.stringify(rollback({ root: values.root })));
  } else if (command === 'uninstall') {
    console.log(
      JSON.stringify(
        uninstall({
          root: values.root,
          purgeData: values['purge-data'] === true,
        }),
      ),
    );
  } else if (command === 'diagnostics' && positional[0] === 'export') {
    const packaged = join(here, '../shell-auth/diagnostics.mjs');
    const source = existsSync(packaged)
      ? packaged
      : join(here, '../../shell-auth/diagnostics.mjs');
    const { exportDiagnostics } = await import(pathToFileURL(source).href);
    const result = await exportDiagnostics({
      root: values.root,
      output: values.output,
      helper: join(here, '../shell-auth/windows/shell-helper.exe'),
      from: values.from,
      to: values.to,
      tenantId: values.tenant,
      providerId: values.provider,
    });
    console.log(JSON.stringify(result));
    if (result.partial) process.exitCode = 3;
  } else if (command === 'launch') {
    const tray = values['stdin-control'] === true;
    try {
      process.exitCode = await launch({
        root: values.root,
        args: positional,
        ...(tray ? { control: createControl() } : {}),
      });
    } finally {
      // DSH may also end on its own; an open control pipe must not keep us alive.
      if (tray) process.stdin.destroy();
    }
  } else if (command === 'python') {
    console.log(
      JSON.stringify(
        pythonCommand(values.root, positional[0], {
          index: values.index === 'none' ? null : values.index,
        }),
      ),
    );
  } else if (command === 'status') {
    console.log(JSON.stringify(desktopInfo(values.root)));
  } else if (command === 'update') {
    await updateCommand(values.root, positional[0], {
      enabled:
        values.enabled === 'true'
          ? true
          : values.enabled === 'false'
            ? false
            : undefined,
    });
  } else {
    console.error(
      'usage: cli.mjs install|rollback|uninstall|launch|status|update check|download|approve|configure|python reset|configure --index <url|none>|diagnostics export --root <dir> ...',
    );
    process.exitCode = 2;
  }
} catch (error) {
  console.error(
    command === 'python'
      ? `Python 操作失败。[${pythonUserFailureReason(error)}]`
      : error instanceof Error
        ? error.message
        : String(error),
  );
  process.exitCode = error instanceof CancelledError ? 2 : 1;
}
