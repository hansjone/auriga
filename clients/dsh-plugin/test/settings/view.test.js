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
      const tree = View({ locale, consent: 'consent-capability-marker' });
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
  test(
    'managed model status directs native manual synchronization without claiming invocation readiness: ' +
      locale,
    async () => {
      const f = harness(
        async (url) =>
          Response.json(
            url.endsWith('/status')
              ? {
                  ...signedIn,
                  mode: 'managed',
                  session: { available: true },
                  models: { state: 'stopped', available: false },
                }
              : { ...settings, mode: 'managed' },
          ),
        locale,
      );
      try {
        f.render();
        await setImmediate();
        const content = text(f.render());
        assert.match(
          content,
          locale === 'zh'
            ? /原生模型设置中获取并保存目录/
            : /Fetch and save the catalog in native Models settings/,
        );
        assert.doesNotMatch(content, /中转站未就绪|Relay unavailable|stopped/);
      } finally {
        f.dispose();
      }
    },
  );
for (const phase of ['signed-out', 'unavailable', 'pending'])
  test(
    'R7 managed identity never falls back to signed out: ' + phase,
    async () => {
      const fixture = harness(async (url) =>
        Response.json(
          url.endsWith('/status')
            ? {
                loggedIn: phase !== 'signed-out',
                session: { available: false },
                account: { subject: 'member-id' },
              }
            : { ...settings, mode: 'managed' },
        ),
      );
      try {
        fixture.render();
        await setImmediate();
        const content = fixture
          .render()
          .children.filter((n) => n?.type !== 'details')
          .map(text)
          .join(' ');
        assert.doesNotMatch(content, /未登录|未提供/);
        assert.match(content, /系统托管/);
        assert.match(content, /实例会话已失效，请从门户重新进入/);
      } finally {
        fixture.dispose();
      }
    },
  );
for (const consent of [false, true, undefined])
  test(
    'R2 settings only mount first-contact controls for an advertised capability: ' +
      consent,
    async () => {
      const fixture = harness(async (url) =>
        Response.json(
          url.endsWith('/status')
            ? { ...signedIn, capabilities: { consent } }
            : settings,
        ),
      );
      try {
        fixture.render();
        await setImmediate();
        assert.equal(
          text(fixture.render()).includes('consent-capability-marker'),
          consent === true,
        );
      } finally {
        fixture.dispose();
      }
    },
  );
for (const managed of [false, true])
  test(
    'status fix: missing names and optional facts never produce UUID or empty rows: ' +
      managed,
    async () => {
      const fixture = harness(async (url) =>
        Response.json(
          url.endsWith('/status')
            ? {
                ...signedIn,
                account: { subject: '00000000-0000-4000-8000-000000000002' },
                deviceName: null,
                loginTime: null,
                connection: null,
              }
            : {
                ...settings,
                deviceName: null,
                mode: managed ? 'managed' : 'connect',
              },
        ),
      );
      try {
        fixture.render();
        await setImmediate();
        const tree = fixture.render();
        const content = tree.children
          .filter((n) => n?.type !== 'details')
          .map(text)
          .join(' ');
        assert.match(
          text(nodes(tree).find((n) => n.type === 'details')),
          /00000000/,
        );
        assert.doesNotMatch(
          content,
          /00000000|设备名|登录时间|本机端点|未提供姓名|当前凭据未提供/,
        );
        assert.match(content, /成员/);
      } finally {
        fixture.dispose();
      }
    },
  );
for (const locale of ['zh', 'en'])
  test(
    'R4 saved device offline is recovering, not signed out: ' + locale,
    async () => {
      const fixture = harness(
        async (url) =>
          Response.json(
            url.endsWith('/status')
              ? {
                  loggedIn: false,
                  connected: false,
                  resumeAvailable: true,
                  account: { subject: 'member-id' },
                  models: { reauthenticationRequired: false },
                }
              : settings,
          ),
        locale,
      );
      try {
        fixture.render();
        await setImmediate();
        const tree = fixture.render();
        assert.match(
          text(tree),
          locale === 'zh'
            ? /总部不可达，联网后自动恢复/
            : /Platform unreachable; reconnects automatically/,
        );
        assert.equal(
          button(tree, locale === 'zh' ? '登录' : 'Sign in'),
          undefined,
        );
        assert.equal(
          button(tree, locale === 'zh' ? '退出登录' : 'Sign out'),
          undefined,
        );
      } finally {
        fixture.dispose();
      }
    },
  );
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
                  'Auriga DSH status',
                  locale === 'zh' ? '账号' : 'Account',
                  locale === 'zh' ? '消息网络' : 'Message network',
                ]
              : [
                  'Auriga DSH status',
                  locale === 'zh' ? '平台' : 'Platform',
                  locale === 'zh' ? '账号' : 'Account',
                  locale === 'zh' ? '消息网络' : 'Message network',
                ],
          );
        } finally {
          fixture.dispose();
        }
      });

test('R2 logout sends the explicit shell command with CSRF and refreshes status', async () => {
  let status = signedIn,
    sent;
  const fixture = harness(async (url, init) => {
    if (url.endsWith('/shell/command')) {
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
    await setImmediate();
    assert.equal(sent.method, 'POST');
    assert.deepEqual(JSON.parse(sent.body), { action: 'logout' });
    assert.equal(sent.headers['x-auriga-csrf'], 'test-csrf');
    fixture.render();
    await setImmediate();
    assert.equal(button(fixture.render(), '退出登录'), undefined);
    assert.equal(fixture.navigated, undefined);
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

for (const locale of ['zh', 'en'])
  test(
    'Desktop binding confirmation is visible and update controls stay disabled: ' +
      locale,
    async () => {
      const fixture = harness(
        async (url) =>
          Response.json(
            url.endsWith('/status')
              ? signedIn
              : {
                  ...settings,
                  desktop: {
                    enabled: false,
                    available: null,
                    restartRequired: false,
                    confirmationRequired: true,
                  },
                },
          ),
        locale,
      );
      try {
        fixture.render();
        await setImmediate();
        const tree = fixture.render();
        assert.equal(
          nodes(tree).filter((n) => n.props.role === 'alert').length,
          1,
        );
        assert.match(
          text(tree),
          locale === 'en' ? /Reinstall Desktop/ : /Desktop/,
        );
        assert.equal(
          button(
            tree,
            locale === 'en'
              ? 'Check for updates'
              : String.fromCodePoint(0x68c0, 0x67e5, 0x66f4, 0x65b0),
          ),
          undefined,
        );
        assert.equal(
          nodes(tree).find(
            (n) =>
              n.type === 'input' &&
              n.props.type === 'checkbox' &&
              n.props.disabled === true,
          )?.props.disabled,
          true,
        );
      } finally {
        fixture.dispose();
      }
    },
  );
