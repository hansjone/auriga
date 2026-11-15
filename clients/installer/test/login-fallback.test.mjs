import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
import test from 'node:test';
import { registerFallbackLogin } from '../runtime/shell-auth.mjs';
test('only an explicit fallback control command opens the browser recovery entry', async () => {
  const handlers = new Map(),
    events = [];
  let opened = 0;
  registerFallbackLogin(
    { on: (name, fn) => handlers.set(name, fn) },
    () => ({
      openLoginFallback: async () => {
        opened++;
      },
    }),
    (e) => events.push(e),
  );
  assert.equal(opened, 0);
  assert.equal(handlers.has('login'), false);
  await handlers.get('login-fallback')();
  assert.equal(opened, 1);
  assert.deepEqual(events, [{ event: 'fallback-login', opened: true }]);
});
test('fallback reports unavailability without attempting another browser path', async () => {
  const handlers = new Map(),
    events = [];
  registerFallbackLogin(
    { on: (name, fn) => handlers.set(name, fn) },
    () => undefined,
    (e) => events.push(e),
  );
  await handlers.get('login-fallback')();
  assert.deepEqual(events, [{ event: 'fallback-login', opened: false }]);
});

test('compiled tray menu and launcher connect the same explicit fallback command', () => {
  const source = readFileSync(
    new URL('../windows/tray/Tray.cs', import.meta.url),
    'utf8',
  );
  assert.match(
    source,
    /fallbackLoginItem = new ToolStripMenuItem\(.*delegate \{ Send\("login-fallback"\); \}/,
  );
  assert.match(source, /openItem, fallbackLoginItem, statusItem/);
  const core = readFileSync(
    new URL('../runtime/core.mjs', import.meta.url),
    'utf8',
  );
  assert.match(
    core,
    /registerFallbackLogin\(control, \(\) => shellAuth, report\)/,
  );
});
