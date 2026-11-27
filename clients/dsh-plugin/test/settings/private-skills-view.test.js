import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSettingsView } from '../../src/settings/view.js';
import { SETTINGS_CATEGORY_KEY } from '../../src/settings/ui.js';

function text(tree) {
  if (typeof tree === 'string') return tree;
  return (tree?.children ?? []).map(text).join(' ');
}

test('settings view mounts private skills UI', () => {
  const memory = new Map([[SETTINGS_CATEGORY_KEY, 'skills']]);
  globalThis.sessionStorage = {
    getItem: (key) => (memory.has(key) ? memory.get(key) : null),
    setItem: (key, value) => memory.set(key, String(value)),
    removeItem: (key) => memory.delete(key),
  };
  let cursor = 0;
  const values = [];
  const React = {
    Fragment: Symbol('Fragment'),
    createElement: (type, props, ...children) => {
      const flat = children.flat(Infinity);
      if (typeof type === 'function') {
        const merged = { ...(props ?? {}) };
        if (flat.length === 1) merged.children = flat[0];
        else if (flat.length > 1) merged.children = flat;
        return type(merged);
      }
      return { type, props: props ?? {}, children: flat };
    },
    useState(initial) {
      const i = cursor++;
      if (!(i in values))
        values[i] = typeof initial === 'function' ? initial() : initial;
      return [values[i], () => {}];
    },
    useEffect() {},
    useReducer() {
      return [0, () => {}];
    },
    useRef(initial) {
      return { current: initial };
    },
  };
  const Settings = createSettingsView(React, {
    localURL: (path) => 'http://localhost/' + path,
    fetcher: async () => {
      throw new Error('settings UI must not call private skills endpoints');
    },
  });
  const tree = Settings({ locale: 'zh' });
  const body = text(tree);
  assert.match(body, /私有技能/);
  assert.match(body, /SKILL\.md/);
  assert.match(body, /新建／导入|刷新/);
});
