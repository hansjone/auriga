/** Command line entry of the installer core, run with the bundled Node. */
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { install, launch, rollback, uninstall } from './core.mjs';

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
    const result = install({
      payload,
      root: values.root,
      dataHome: values['data-home'],
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
  } else if (command === 'launch') {
    process.exitCode = await launch({ root: values.root, args: positional });
  } else {
    console.error(
      'usage: cli.mjs install|rollback|uninstall|launch --root <dir> ...',
    );
    process.exitCode = 2;
  }
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
