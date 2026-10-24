/** Optional local probe against the pinned, already installed DSH SlotCore. Not a CI dependency. */
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve, dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { surfaceHost, response } from './shell-host.mjs';
const entry = resolve(process.argv[2] ?? '');
const pkg = JSON.parse(
  await readFile(join(dirname(entry), '../package.json'), 'utf8'),
);
assert.equal(pkg.name, '@deepseek-ai/dsh-client-ui-slots');
assert.equal(pkg.version, '0.2.0-rc.2');
const { SlotCore } = await import(pathToFileURL(entry).href);
const core = new SlotCore();
const removeRoot = core.register(
  {
    name: 'root',
    children: {
      main: { kind: 'keyed', scope: 'root' },
      'sidebar.panellist': { kind: 'list', scope: 'root' },
      'settings.section': { kind: 'list', scope: 'root' },
    },
  },
  () => null,
);
const host = surfaceHost(async () => response(), { slotCore: core });
try {
  for (const target of ['main', 'sidebar.panellist', 'settings.section'])
    assert.equal(core.entriesOfSlot(target).length, 1);
  assert.equal(
    core.entriesOfSlot('main')[0].options.key,
    core.entriesOfSlot('sidebar.panellist')[0].options.id,
  );
  assert.equal(core.entriesOfSlot('main')[0].locale, 'auriga.shell');
  assert.throws(
    () => core.register({ name: 'auriga.shell', key: 'x' }, () => null),
    /not declared/,
  );
  assert.throws(
    () => core.register({ name: 'main' }, () => null),
    /requires options.key/,
  );
  assert.throws(
    () => core.register({ name: 'sidebar.panellist' }, () => null),
    /requires options.id/,
  );
  host.dispose();
  for (const target of ['main', 'sidebar.panellist', 'settings.section'])
    assert.equal(core.entriesOfSlot(target).length, 0);
  console.log(
    JSON.stringify({
      entry,
      version: pkg.version,
      accepted: ['main', 'sidebar.panellist', 'settings.section'],
      rejectedNamespace: true,
      requiredMetadataValidated: true,
      disposed: true,
    }),
  );
} finally {
  host.dispose();
  host.close();
  removeRoot();
}
