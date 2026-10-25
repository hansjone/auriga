import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { createLoginView } from '../src/login/index.js';

const recovery = {
  type: 'form',
  formId: 'recovery',
  title: { en: 'Recovery', 'zh-CN': '应急恢复' },
  fields: [{ name: 'recoveryCode', kind: 'password', required: true }],
};
const tick = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};
function mount(t, overrides = {}) {
  const dom = new JSDOM('<main></main>', {
    url: 'http://localhost/auriga/local-login',
  });
  const root = dom.window.document.querySelector('main');
  let completed = 0;
  const view = createLoginView(root, {
    onSubmit: async () => recovery,
    onRestart: async () => recovery,
    onComplete: () => completed++,
    ...overrides,
  });
  t.after(() => {
    view.dispose();
    dom.window.close();
  });
  return { dom, root, view, completed: () => completed };
}
const methods = [
  {
    type: 'qr',
    content: 'https://example.com/qr',
    matchCode: '42',
    expiresAt: '2099-01-01T00:00:00Z',
  },
  {
    type: 'redirect',
    url: 'https://example.com/login',
    method: 'POST',
    params: { assertion: 'secret' },
  },
  { type: 'webauthn', ceremony: 'get', publicKey: { challenge: 'secret' } },
  {
    type: 'device',
    userCode: 'secret',
    verificationUri: 'https://example.com/device',
    expiresAt: '2099-01-01T00:00:00Z',
  },
];
for (const step of methods) {
  test(`${step.type}: unsupported local transport is explicit, cancellable and restartable; no secret or external navigation`, async (t) => {
    let cancelled = 0,
      restarted = 0;
    const { root, view, completed } = mount(t, {
      onCancel: async () => {
        cancelled++;
      },
      onRestart: async () => {
        restarted++;
        return recovery;
      },
    });
    view.setStep(step);
    assert.match(root.textContent, /暂不可用/);
    assert.equal(root.querySelector('.card').dataset.step, step.type);
    assert.doesNotMatch(root.textContent, /secret|https:/);
    assert.equal(root.querySelector('a,iframe,form'), null);
    root.querySelector('[data-action=cancel]').click();
    await tick();
    assert.equal(cancelled, 1);
    assert.match(root.textContent, /已取消/);
    root.querySelector('[data-action=restart]').click();
    assert.equal(root.querySelector('.card').getAttribute('aria-busy'), 'true');
    await tick();
    assert.equal(restarted, 1);
    assert.equal(root.querySelector('[name=recoveryCode]').value, '');
    assert.equal(completed(), 0);
  });
}
for (const type of ['qr', 'device']) {
  test(`${type}: expiry remains expired through locale changes, never completes`, (t) => {
    const { root, view, completed } = mount(t);
    view.setStep({
      ...methods.find((s) => s.type === type),
      expiresAt: '2000-01-01T00:00:00Z',
    });
    assert.match(root.textContent, /过期/);
    view.setLocale('en');
    assert.match(root.textContent, /expired/);
    assert.equal(completed(), 0);
  });
}
test('recovery form failure clears secret, retries explicitly, and ignores cancelled late completion', async (t) => {
  let resolve, signal;
  const { root, dom, view, completed } = mount(t, {
    onSubmit: (_input, context) => {
      signal = context.signal;
      return new Promise((done) => {
        resolve = done;
      });
    },
  });
  view.setStep(recovery);
  const secret = root.querySelector('input');
  secret.value = 'test-only-recovery';
  root
    .querySelector('form')
    .dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
  assert.equal(secret.value, '');
  root.querySelector('[data-action=cancel]').click();
  assert.equal(signal.aborted, true);
  resolve({ type: 'complete', redirectTo: '/untrusted' });
  await tick();
  assert.equal(completed(), 0);
  assert.match(root.textContent, /已取消/);
  view.setStep({ ...recovery, error: { code: 'iam.invalid_credentials' } });
  assert.match(root.textContent, /用户名或口令不正确/);
});
for (const [reason, pattern] of [
  ['network', /网络|不可达/],
  ['unconfigured', /平台地址.*配置/],
  ['expired', /凭据.*过期/],
]) {
  test(`${reason}: controlled bilingual error and explicit retry`, async (t) => {
    const { root, view } = mount(t);
    view.setFailure(reason);
    assert.match(root.textContent, pattern);
    view.setLocale('en');
    assert.ok(root.querySelector('[data-action=restart]'));
    root.querySelector('[data-action=restart]').click();
    await tick();
    assert.ok(root.querySelector('form'));
  });
}
test('initial loading is visible; cancelled loading ignores late response and cleans up', async (t) => {
  let signal;
  const { root, view } = mount(t, { onCancel: async () => {} });
  view.load(({ signal: current }) => {
    signal = current;
    return new Promise(() => {});
  });
  assert.match(root.textContent, /正在处理/);
  root.querySelector('[data-action=cancel]').click();
  await tick();
  assert.equal(signal.aborted, true);
  assert.match(root.textContent, /已取消/);
});
test('locale change while cancellation is pending does not leave restart disabled', async (t) => {
  let finish;
  const { root, view } = mount(t, {
    onCancel: () =>
      new Promise((done) => {
        finish = done;
      }),
  });
  view.setStep(methods[0]);
  root.querySelector('[data-action=cancel]').click();
  view.setLocale('en');
  assert.equal(root.querySelector('[data-action=restart]').disabled, true);
  finish();
  await tick();
  assert.equal(root.querySelector('[data-action=restart]').disabled, false);
});
test('cancelled recovery form cannot resubmit using an expired attempt proof', async (t) => {
  let submitted = 0;
  const { root, dom, view } = mount(t, {
    onCancel: async () => {},
    onSubmit: async () => {
      submitted++;
      return recovery;
    },
  });
  view.setStep(recovery);
  root.querySelector('[data-action=cancel]').click();
  await tick();
  root.querySelector('input').value = 'test-recovery';
  root
    .querySelector('form')
    .dispatchEvent(new dom.window.Event('submit', { cancelable: true }));
  await tick();
  assert.equal(submitted, 0);
});
test('QR expires during a pending restart, aborts transport and ignores late completion', async (t) => {
  let finish, signal;
  const { root, view, completed } = mount(t, {
    onRestart: (context) => {
      signal = context.signal;
      return new Promise((done) => {
        finish = done;
      });
    },
  });
  const now = Date.parse('2026-01-01T00:00:00Z');
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now });
  view.setStep({
    ...methods[0],
    expiresAt: new Date(now + 1000).toISOString(),
  });
  root.querySelector('[data-action=restart]').click();
  t.mock.timers.tick(1000);
  assert.equal(signal.aborted, true);
  assert.match(root.textContent, /过期/);
  finish({ type: 'complete', redirectTo: '/' });
  await tick();
  assert.equal(completed(), 0);
});
