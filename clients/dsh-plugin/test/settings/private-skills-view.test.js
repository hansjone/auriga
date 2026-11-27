import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSettingsView } from '../../src/settings/view.js';

function nodes(tree) {
  if (!tree || typeof tree !== 'object') return [];
  return [tree, ...(tree.children ?? []).flatMap(nodes)];
}

function text(tree) {
  if (typeof tree === 'string') return tree;
  return (tree?.children ?? []).map(text).join(' ');
}

test('settings view no longer mounts private skills UI', () => {
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
      if (!(i in values)) values[i] = initial;
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
  assert.equal(
    nodes(tree).some(
      (node) =>
        typeof node.type === 'function' &&
        node.type.name === 'PrivateSkillsView',
    ),
    false,
  );
  assert.doesNotMatch(text(tree), /私有技能|Private skills|SKILL\.md/);
});
