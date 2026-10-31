import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createSettingsView } from '../../src/settings/view.js';

function harness(fetcher, locale = 'zh') {
  const values = [],
    effects = [];
  let cursor = 0,
    navigated;
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
        (next) => {
          values[i] = typeof next === 'function' ? next(values[i]) : next;
        },
      ];
    },
    useReducer(fn, initial) {
      const [value, set] = React.useState(initial);
      return [value, () => set(fn)];
    },
    useEffect(fn, deps) {
      const i = cursor++;
      if (!values[i] || values[i].deps.some((v, j) => v !== deps[j])) {
        values[i]?.cleanup?.();
        values[i] = { deps };
        effects.push(() => {
          values[i].cleanup = fn();
        });
      }
    },
  };
  const View = createSettingsView(React, {
    localURL: (p) => 'https://example.com/' + p,
    fetcher,
    navigate: (to) => {
      navigated = to;
    },
  });
  return {
    render() {
      cursor = 0;
      const tree = View({ locale });
      effects.splice(0).forEach((fn) => fn());
      return tree;
    },
    get navigated() {
      return navigated;
    },
    dispose() {
      values.forEach((v) => v?.cleanup?.());
    },
  };
}
function nodes(tree) {
  return tree && typeof tree === 'object'
    ? [tree, ...tree.children.flatMap(nodes)]
    : [];
}
function text(tree) {
  return tree && typeof tree === 'object'
    ? tree.children.map(text).join(' ')
    : String(tree ?? '');
}
const button = (tree, label) =>
  nodes(tree).find((n) => n.type === 'button' && text(n) === label);

const settings = {
  mode: 'connect',
  platformOrigin: 'https://example.com',
  activeOrigin: 'https://example.com',
  deployment: 'local',
  deviceName: 'Test DSH',
};
const signedIn = {
  loggedIn: true,
  connected: true,
  account: { subject: 'member-id', displayName: 'Alice' },
  deviceName: 'Desk',
  loginTime: '2026-10-31T00:00:00Z',
  csrf: 'test-csrf',
  network: { connected: true, state: 'ready' },
  models: { available: false },
};
for (const locale of ['zh', 'en'])
  for (const mode of ['connect', 'managed'])
    for (const phase of [
      'unconfigured',
      'signed-out',
      'ready',
      'offline',
      'expired',
      'invalid',
    ])
      test('R2 settings ' + locale + ' ' + mode + ' ' + phase, async () => {
        const state = {
          ...settings,
          mode,
          ...(phase === 'unconfigured'
            ? { platformOrigin: '', activeOrigin: '' }
            : {}),
        };
        const status =
          phase === 'invalid'
            ? { loggedIn: 'false' }
            : phase === 'signed-out' ||
                phase === 'unconfigured' ||
                phase === 'expired'
              ? {
                  loggedIn: false,
                  connected: false,
                  models: { reauthenticationRequired: phase === 'expired' },
                }
              : {
                  ...signedIn,
                  ...(phase === 'offline'
                    ? {
                        connected: false,
                        communication: 'unavailable',
                        network: { connected: false, state: 'reconnecting' },
                      }
                    : {}),
                };
        const fixture = harness(
          async (url) =>
            Response.json(url.endsWith('/status') ? status : state),
          locale,
        );
        try {
          fixture.render();
          await setImmediate();
          const tree = fixture.render(),
            content = text(tree);
          const managed = mode === 'managed';
          assert.equal(
            nodes(tree).some(
              (n) =>
                n.type === 'input' &&
                n.props['aria-label'] ===
                  (locale === 'zh' ? '平台地址' : 'Platform address'),
            ),
            !managed,
          );
          assert.equal(
            !!button(tree, locale === 'zh' ? '测试连接' : 'Test connection'),
            !managed,
          );
          assert.equal(
            !!button(tree, locale === 'zh' ? '退出登录' : 'Sign out'),
            !managed && ['ready', 'offline'].includes(phase),
          );
          assert.equal(
            !!button(tree, locale === 'zh' ? '登录' : 'Sign in'),
            !managed && ['signed-out', 'expired'].includes(phase),
          );
          assert.ok(
            nodes(tree).some(
              (n) => n.type === 'details' && n.props.open !== true,
            ),
          );
          assert.doesNotMatch(
            content,
            /应急账号|emergency account|总部连接|Team connected|Team 已连接/,
          );
          if (['ready', 'offline'].includes(phase))
            assert.match(content, /Alice/);
          if (phase === 'offline' && !managed)
            assert.match(
              content,
              locale === 'zh'
                ? /本地可正常使用，平台功能暂停/
                : /Local DSH available/,
            );
          if (phase === 'invalid')
            assert.match(
              content,
              locale === 'zh' ? /状态响应格式无效/ : /Invalid status response/,
            );
          const sections = nodes(tree)
            .filter((n) => n.type === 'section' && n.props['aria-label'])
            .map((n) => n.props['aria-label']);
          assert.deepEqual(
            sections,
            managed
              ? [
                  'Auriga status',
                  locale === 'zh' ? '账号' : 'Account',
                  locale === 'zh' ? '消息网络' : 'Message network',
                ]
              : [
                  'Auriga status',
                  locale === 'zh' ? '平台' : 'Platform',
                  locale === 'zh' ? '账号' : 'Account',
                  locale === 'zh' ? '消息网络' : 'Message network',
                ],
          );
        } finally {
          fixture.dispose();
        }
      });

test('R2 logout preserves the existing explicit confirmation page', async () => {
  let status = signedIn,
    sent;
  const fixture = harness(async (url, init) => {
    if (url.endsWith('/logout')) {
      sent = init;
      status = { loggedIn: false, connected: false };
      return Response.json({});
    }
    return Response.json(url.endsWith('/status') ? status : settings);
  });
  try {
    fixture.render();
    await setImmediate();
    await button(fixture.render(), '退出登录').props.onClick();
    assert.equal(sent, undefined);
    assert.ok(button(fixture.render(), '退出登录'));
    assert.equal(
      fixture.navigated,
      'https://example.com/auriga/local-login?logout=1',
    );
  } finally {
    fixture.dispose();
  }
});
for (const failure of ['http', 'timeout', 'invalid-json'])
  test('R2 precise status failure ' + failure, async () => {
    const fixture = harness(async (url) => {
      if (!url.endsWith('/status')) return Response.json(settings);
      if (failure === 'http') return Response.json({}, { status: 503 });
      if (failure === 'timeout')
        throw Object.assign(new Error('private'), { name: 'TimeoutError' });
      return new Response('private');
    });
    try {
      fixture.render();
      await setImmediate();
      const content = text(fixture.render());
      assert.match(
        content,
        failure === 'http'
          ? /HTTP 503/
          : failure === 'timeout'
            ? /超时/
            : /格式无效/,
      );
      assert.doesNotMatch(content, /private|请重试/);
      assert.equal(button(fixture.render(), '退出登录'), undefined);
    } finally {
      fixture.dispose();
    }
  });
