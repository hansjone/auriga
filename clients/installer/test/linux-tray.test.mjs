import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const trayJs = join(here, '../linux/tray/auriga-desktop-tray.mjs');
const traySh = join(here, '../linux/tray/auriga-desktop-tray');

test('linux tray supervisor sources exist and pin the desktop port', () => {
  const source = readFileSync(trayJs, 'utf8');
  assert.match(source, /12721/);
  assert.match(source, /stdin-control/);
  assert.match(source, /xdg-open/);
  assert.match(source, /notify-send/);
  const wrapper = readFileSync(traySh, 'utf8');
  assert.match(wrapper, /auriga-desktop-tray\.mjs/);
  assert.match(wrapper, /upstream\/node\/bin\/node/);
});
