import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createSettingsView } from '../../src/settings/view.js';

function nodes(tree) {
  if (!tree || typeof tree !== 'object') return [];
  return [tree, ...(tree.children ?? []).flatMap(nodes)];
}
function text(tree) {
  if (typeof tree === 'string') return tree;
  return (tree?.children ?? []).map(text).join(' ');
}
for (const locale of ['zh', 'en'])
  test(
    'private editor uses only native local endpoints and preserves revision on save: ' +
      locale,
    async () => {
      let cursor = 0,
        values = [],
        effects = [];
      const React = {
        createElement: (type, props, ...children) => ({
          type,
          props: props ?? {},
          children: children.flat(Infinity),
        }),
        useState(initial) {
          const i = cursor++;
          if (!(i in values)) values[i] = initial;
          return [
            values[i],
            (value) => {
              values[i] = value;
            },
          ];
        },
        useEffect(fn) {
          const i = cursor++;
          if (!(i in values)) {
            values[i] = true;
            effects.push(fn);
          }
        },
        useReducer() {
          return [0, () => {}];
        },
      };
      let source =
        '---\nname: example\ndescription: "Example"\n---\nsecret private text';
      let row = { name: 'example', enabled: true, revision: 'sha256:old' };
      let blocked = false;
      const sent = [];
      const fetcher = async (url, options) => {
        assert.equal(url, 'http://localhost/auriga/settings/skills');
        if (!options.body)
          return Response.json({
            items: [row],
            ...(blocked
              ? {
                  writeLock: { path: 'C:/fixture/private-skills/write.lock.d' },
                }
              : {}),
          });
        const input = JSON.parse(options.body);
        sent.push(input);
        if (input.action === 'edit') {
          assert.equal(input.revision, row.revision);
          source = input.source;
          row = { ...row, revision: 'sha256:new' };
        }
        return Response.json({ ...row, files: { 'SKILL.md': source } });
      };
      const Settings = createSettingsView(React, {
        localURL: (path) => 'http://localhost/' + path,
        fetcher,
      });
      const parent = Settings({ locale });
      const Editor = nodes(parent).find(
        (node) =>
          typeof node.type === 'function' &&
          node.type.name === 'PrivateSkillsView',
      ).type;
      cursor = 0;
      values = [];
      effects = [];
      const render = () => {
        cursor = 0;
        return Editor({ locale });
      };
      let tree = render();
      effects.splice(0).forEach((fn) => fn());
      await setImmediate();
      tree = render();
      const click = async (label) => {
        const button = nodes(tree).find(
          (n) => n.type === 'button' && text(n) === label,
        );
        assert.ok(button, label);
        await button.props.onClick();
        tree = render();
      };
      await click(locale === 'zh' ? '打开' : 'Open');
      nodes(tree)
        .find((n) => n.type === 'textarea')
        .props.onChange({ target: { value: source + '\nChanged' } });
      tree = render();
      await click(locale === 'zh' ? '保存修改' : 'Save changes');
      assert.equal(sent.at(-1).revision, 'sha256:old');
      assert.match(sent.at(-1).source, /Changed$/);
      assert.match(text(tree), locale === 'zh' ? /新建会话/ : /new session/);
      blocked = true;
      await click(locale === 'zh' ? '刷新' : 'Refresh');
      assert.match(text(tree), /C:\/fixture\/private-skills\/write.lock.d/);
      assert.match(
        text(tree),
        locale === 'zh' ? /关闭此 DSH 的全部进程/ : /close all processes/i,
      );
    },
  );
