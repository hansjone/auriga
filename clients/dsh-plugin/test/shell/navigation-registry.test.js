import assert from 'node:assert/strict';
import test from 'node:test';
import {
  AURIGA_NAVIGATION_ENTRIES,
  createAurigaNavigationRegistry,
  readActivityBarExpanded,
  writeActivityBarExpanded,
} from '../../src/shell/navigation-registry.js';

test('registry lists top and bottom placements in order', () => {
  const registry = createAurigaNavigationRegistry();
  assert.deepEqual(
    registry.list('top').map((item) => item.id),
    ['overview', 'market', 'mine', 'team'],
  );
  assert.deepEqual(
    registry.list('bottom').map((item) => item.id),
    ['inbox', 'settings'],
  );
});

test('register extends without disturbing bottom placement', () => {
  const registry = createAurigaNavigationRegistry();
  registry.register({
    id: 'knowledge',
    labelKey: 'tab_knowledge',
    shortLabelKey: 'tab_knowledge_short',
    icon: 'BookOpen',
    route: 'knowledge',
    group: 'workspace',
    order: 25,
    placement: 'top',
  });
  assert.deepEqual(
    registry.list('top').map((item) => item.id),
    ['overview', 'market', 'knowledge', 'mine', 'team'],
  );
  assert.deepEqual(
    registry.list('bottom').map((item) => item.id),
    ['inbox', 'settings'],
  );
});

test('visible and permission filters hide entries', () => {
  const registry = createAurigaNavigationRegistry([
    ...AURIGA_NAVIGATION_ENTRIES,
    {
      id: 'admin',
      labelKey: 'tab_admin',
      icon: 'Settings',
      route: 'admin',
      placement: 'bottom',
      order: 30,
      permission: 'admin',
    },
    {
      id: 'hidden',
      labelKey: 'tab_hidden',
      icon: 'Inbox',
      route: 'hidden',
      placement: 'top',
      order: 99,
      visible: false,
    },
  ]);
  assert.ok(!registry.list('top').some((item) => item.id === 'hidden'));
  assert.ok(
    !registry
      .list('bottom', { can: () => false })
      .some((i) => i.id === 'admin'),
  );
  assert.ok(
    registry.list('bottom', { can: () => true }).some((i) => i.id === 'admin'),
  );
});

test('activity bar expanded preference persists', () => {
  const memory = new Map();
  const storage = {
    getItem: (key) => (memory.has(key) ? memory.get(key) : null),
    setItem: (key, value) => memory.set(key, String(value)),
  };
  assert.equal(readActivityBarExpanded(storage), false);
  writeActivityBarExpanded(true, storage);
  assert.equal(readActivityBarExpanded(storage), true);
  writeActivityBarExpanded(false, storage);
  assert.equal(readActivityBarExpanded(storage), false);
});
