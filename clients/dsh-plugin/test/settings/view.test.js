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

test('settings show unconfigured state and persist edited address and deployment through save', async () => {
  let stored = { platformOrigin: '', activeOrigin: '', deployment: 'cloud' };
  const fixture = harness(async (url, init) => {
    if (url.endsWith('/status'))
      return Response.json({ loggedIn: false, connected: true });
    if (url.endsWith('/save'))
      stored = { ...JSON.parse(init.body), activeOrigin: '' };
    return Response.json(stored);
  });
  fixture.render();
  await setImmediate();
  let tree = fixture.render();
  assert.match(text(tree), /尚未配置平台连接/);
  assert.doesNotMatch(text(tree), /Team 已连接/);
  nodes(tree)
    .find((n) => n.type === 'input')
    .props.onChange({ target: { value: 'https://example.com' } });
  nodes(tree)
    .find((n) => n.type === 'select')
    .props.onChange({ target: { value: 'local' } });
  await button(fixture.render(), '保存').props.onClick();
  tree = fixture.render();
  assert.equal(stored.deployment, 'local');
  assert.equal(stored.platformOrigin, 'https://example.com');
  assert.match(text(tree), /尚未应用于登录连接/);
  assert.equal(
    nodes(tree).find((n) => n.type === 'a').props.href,
    'https://example.com/',
  );
  fixture.dispose();
});

test('account view uses sign-in identity, reports missing metadata, and reuses CSRF logout route', async () => {
  const requests = [];
  const fixture = harness(async (url, init) => {
    requests.push([url, init]);
    if (url.endsWith('/status'))
      return Response.json({
        loggedIn: true,
        connected: true,
        account: { subject: 'alice' },
        csrf: 'test-csrf',
      });
    return Response.json({
      platformOrigin: 'https://example.com',
      activeOrigin: 'https://example.com',
      deployment: 'cloud',
      deviceName: 'Auriga DSH',
    });
  });
  fixture.render();
  await setImmediate();
  const tree = fixture.render();
  assert.match(text(tree), /alice/);
  assert.match(text(tree), /Auriga DSH/);
  assert.match(text(tree), /当前登录接口未提供/);
  assert.match(text(tree), /模型授权需单独确认/);
  await button(tree, '退出本机登录').props.onClick();
  const [url, init] = requests.at(-1);
  assert.ok(url.endsWith('/auriga/local-login/logout'));
  assert.equal(init.headers['X-Auriga-CSRF'], 'test-csrf');
  assert.equal(init.body, '{}');
  assert.equal(fixture.navigated, 'https://example.com/auriga/local-login');
  fixture.dispose();
});

test('failed connection and failed logout remain visible without navigating or reporting success', async () => {
  const fixture = harness(async (url) => {
    if (url.endsWith('/status'))
      return Response.json({
        loggedIn: true,
        account: { subject: 'alice' },
        csrf: 'test-csrf',
      });
    if (url.endsWith('/logout')) return new Response(null, { status: 503 });
    if (url.endsWith('/test'))
      return Response.json({ reachable: false, reason: 'http', status: 503 });
    return Response.json({
      platformOrigin: 'https://example.com',
      activeOrigin: 'https://example.com',
      deployment: 'cloud',
    });
  }, 'en');
  fixture.render();
  await setImmediate();
  await button(fixture.render(), 'Test connection').props.onClick();
  assert.match(text(fixture.render()), /HTTP 503/);
  await button(fixture.render(), 'Sign out locally').props.onClick();
  assert.match(text(fixture.render()), /Please sign in again/);
  assert.equal(fixture.navigated, undefined);
  fixture.dispose();
});
