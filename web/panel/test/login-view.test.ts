import { afterEach, expect, it, vi } from 'vitest';
import { createLoginView } from '../../../clients/dsh-plugin/src/login/index.js';
import type { LoginOptions } from '../../../clients/dsh-plugin/src/login/index.js';
import type {
  ClientStep,
  FormStep,
} from '../../../clients/dsh-plugin/src/generated/steps';

const disposals: (() => void)[] = [];
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  document.body.replaceChildren();
});
const passwordStep: FormStep = {
  type: 'form',
  formId: 'password',
  title: { en: 'Emergency account', 'zh-CN': '应急账号' },
  fields: [
    {
      kind: 'text',
      name: 'username',
      label: { en: 'Username' },
      required: true,
      autocomplete: 'username',
    },
    {
      kind: 'password',
      name: 'password',
      label: { en: 'Password' },
      required: true,
      autocomplete: 'current-password',
    },
  ],
};
const otpStep: FormStep = {
  type: 'form',
  formId: 'totp',
  fields: [{ kind: 'otp', name: 'otp', label: { en: 'Code' }, required: true }],
};
function deferred() {
  let resolve!: (step: ClientStep) => void;
  const promise = new Promise<ClientStep>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

it.each([true, false, 'opaque', ''])(
  'preserves hidden value %s and its original type through form submission',
  async (value) => {
    const { root, view, onSubmit } = mount();
    view.setStep({
      type: 'form',
      formId: 'hidden-binding',
      fields: [{ kind: 'hidden', name: 'binding', value }],
    });
    submit(root);
    const received = onSubmit.mock.calls[0][0];
    expect(received.type).toBe('form');
    if (received.type !== 'form') throw new Error('Expected form submission');
    expect(received.values.binding).toBe(value);
    await flush();
  },
);

it.each([
  'synchronous-locale',
  'transport-queued-locale',
  'locale-after-delivery',
])('delivers the current completion exactly once across %s', async (mode) => {
  const { root, view, onSubmit, onComplete } = mount();
  const complete: ClientStep = { type: 'complete', redirectTo: '/safe' };
  if (mode === 'transport-queued-locale') {
    onSubmit.mockResolvedValueOnce(complete);
    view.setStep(passwordStep);
    input(root, 'username', 'alice');
    input(root, 'password', 'test-only');
    submit(root);
    queueMicrotask(() => view.setLocale('en'));
  } else {
    view.setStep(complete);
    if (mode === 'locale-after-delivery') await flush();
    view.setLocale('en');
  }
  await flush();
  expect(root.textContent).toContain('Sign-in step completed');
  expect(onComplete).toHaveBeenCalledTimes(1);
  view.setLocale('zh-CN');
  view.setLocale('en');
  await flush();
  expect(onComplete).toHaveBeenCalledTimes(1);
});

it.each(['new-step', 'dispose'])(
  'invalidates an undelivered completion after locale then %s',
  async (action) => {
    const { root, view, onComplete } = mount();
    view.setStep({ type: 'complete', redirectTo: '/safe' });
    view.setLocale('en');
    if (action === 'new-step') view.setStep(otpStep);
    else view.dispose();
    await flush();
    expect(onComplete).not.toHaveBeenCalled();
    if (action === 'new-step')
      expect(root.querySelector('[name=otp]')).not.toBeNull();
    else expect(root.childNodes).toHaveLength(0);
  },
);
function mount(overrides: Partial<LoginOptions> = {}) {
  const root = document.createElement('div');
  document.body.append(root);
  const onSubmit = vi.fn<LoginOptions['onSubmit']>(async () => otpStep);
  const onRestart = vi.fn(async (): Promise<ClientStep> => passwordStep);
  const onComplete = vi.fn();
  const view = createLoginView(root, {
    onSubmit,
    onRestart,
    onComplete,
    ...overrides,
  });
  disposals.push(view.dispose);
  return { root, view, onSubmit, onRestart, onComplete };
}
function input(root: HTMLElement, name: string, value: string) {
  const node = root.querySelector<HTMLInputElement>(`[name="${name}"]`)!;
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
  return node;
}
function submit(root: HTMLElement) {
  root
    .querySelector('form')!
    .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
}
async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

it('submits real emergency inputs through multiple steps and clears sensitive detached inputs', async () => {
  const { root, view, onSubmit, onComplete } = mount();
  view.setStep(passwordStep);
  expect(root.querySelector('label')!.htmlFor).toBe(
    root.querySelector('input')!.id,
  );
  expect(
    root.querySelector('[name=password]')!.getAttribute('autocomplete'),
  ).toBe('current-password');
  input(root, 'username', 'alice');
  const password = input(root, 'password', 'test-only-password');
  submit(root);
  submit(root);
  expect(password.value).toBe('');
  expect(onSubmit).toHaveBeenCalledTimes(1);
  expect(onSubmit.mock.calls[0][0]).toEqual({
    type: 'form',
    formId: 'password',
    values: { username: 'alice', password: 'test-only-password' },
  });
  await flush();
  expect(root.querySelector('[name=otp]')!.getAttribute('autocomplete')).toBe(
    'one-time-code',
  );
  onSubmit.mockResolvedValueOnce({ type: 'complete', redirectTo: '/safe' });
  const otp = input(root, 'otp', '123456');
  submit(root);
  expect(otp.value).toBe('');
  await flush();
  expect(onComplete).toHaveBeenCalledTimes(1);
  expect(root.querySelector('a')).toBeNull();
  view.setLocale('en');
  await flush();
  expect(onComplete).toHaveBeenCalledTimes(1);
});

it('uses native validation before transport and handles every non-secret field kind', async () => {
  const { root, view, onSubmit } = mount();
  view.setStep({
    ...passwordStep,
    fields: [
      ...passwordStep.fields,
      { kind: 'email', name: 'email', label: { en: 'Email' } },
      { kind: 'tel', name: 'phone', label: { en: 'Phone' } },
      {
        kind: 'select',
        name: 'source',
        label: { en: 'Source' },
        options: [
          { value: 'local', label: { en: 'Local' } },
          { value: 'other', label: { en: 'Other' } },
        ],
      },
      { kind: 'checkbox', name: 'remember', label: { en: 'Remember' } },
      { kind: 'hidden', name: 'binding', value: 'opaque' },
      {
        kind: 'info',
        name: 'note',
        label: { en: '<script>untrusted</script>' },
        help: { en: 'Help' },
      },
    ],
  });
  submit(root);
  expect(onSubmit).not.toHaveBeenCalled();
  input(root, 'username', 'alice');
  input(root, 'password', 'test-only');
  input(root, 'email', 'alice@example.com');
  input(root, 'phone', '123');
  root.querySelector<HTMLSelectElement>('select')!.value = 'other';
  root.querySelector<HTMLInputElement>('[type=checkbox]')!.checked = true;
  submit(root);
  expect(onSubmit.mock.calls[0][0]).toMatchObject({
    values: {
      email: 'alice@example.com',
      phone: '123',
      source: 'other',
      remember: true,
      binding: 'opaque',
    },
  });
  const submitted = onSubmit.mock.calls[0][0];
  expect(submitted.type === 'form' && submitted.values).not.toHaveProperty(
    'note',
  );
  expect(root.querySelector('script')).toBeNull();
  await flush();
});

it('localizes trusted labels but shows controlled form and field errors', () => {
  const { root, view } = mount();
  view.setStep({
    ...passwordStep,
    description: { en: 'Fallback description' },
    error: {
      code: 'iam.invalid_credentials',
      message: { en: 'SECRET reason' },
    },
    fieldErrors: {
      username: { code: 'unknown', message: { en: '<img src=x>' } },
    },
  });
  expect(root.textContent).toContain('应急账号');
  expect(root.textContent).toContain('Fallback description');
  expect(root.textContent).toContain('用户名或口令不正确');
  expect(root.textContent).not.toContain('SECRET');
  const control = root.querySelector<HTMLInputElement>('[name=username]')!;
  expect(control.getAttribute('aria-invalid')).toBe('true');
  expect(
    root
      .querySelector(`#${control.getAttribute('aria-describedby')}`)
      ?.getAttribute('role'),
  ).toBe('alert');
  view.setLocale('en');
  expect(root.textContent).toContain('Emergency account');
  expect(root.textContent).toContain('Invalid username or password');
});

it.each(['qr', 'redirect', 'webauthn', 'device', 'done', 'future'])(
  'fails closed for %s without completion, links or transport',
  async (type) => {
    const { root, view, onComplete, onSubmit } = mount();
    view.setStep({
      type,
      url: 'https://example.com',
      redirectTo: '/safe',
    } as unknown as ClientStep);
    await flush();
    expect(root.textContent).toContain('当前登录方式暂不可用');
    expect(onComplete).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(root.querySelector('a, form, iframe')).toBeNull();
  },
);

it('shows controlled transport failures and permits explicit retry', async () => {
  const onSubmit = vi.fn().mockRejectedValue(new Error('SECRET stack/token'));
  const { root, view, onRestart } = mount({ onSubmit });
  view.setStep(passwordStep);
  input(root, 'username', 'alice');
  input(root, 'password', 'test-only');
  submit(root);
  await flush();
  expect(root.textContent).toContain('登录失败');
  expect(root.textContent).not.toContain('SECRET');
  expect(
    root.querySelector('[name=password]')!.getAttribute('value'),
  ).toBeNull();
  view.setStep({
    type: 'fail',
    code: 'iam.login_tx_expired',
    message: { en: 'SECRET' },
    retryable: false,
  });
  expect(root.querySelector('button')).toBeNull();
  view.setStep({ type: 'fail', code: 'iam.login_tx_expired', retryable: true });
  root.querySelector('button')!.click();
  root.querySelector('button')!.click();
  await flush();
  expect(onRestart).toHaveBeenCalledTimes(1);
  expect(root.querySelector('form')).not.toBeNull();
});

it.each(['cancel', 'locale', 'step', 'dispose'])(
  'aborts %s and ignores late completion',
  async (action) => {
    const pending = deferred();
    const onSubmit = vi.fn<LoginOptions['onSubmit']>(() => pending.promise);
    const { root, view, onComplete } = mount({ onSubmit });
    view.setStep(passwordStep);
    input(root, 'username', 'alice');
    const password = input(root, 'password', 'test-only');
    const oldForm = root.querySelector('form')!;
    submit(root);
    const signal = onSubmit.mock.calls[0][1].signal as AbortSignal;
    if (action === 'cancel') root.querySelectorAll('button')[1].click();
    if (action === 'locale') view.setLocale('en');
    if (action === 'step') view.setStep(otpStep);
    if (action === 'dispose') {
      view.dispose();
      view.dispose();
    }
    expect(signal.aborted).toBe(true);
    expect(password.value).toBe('');
    oldForm.dispatchEvent(new Event('submit', { cancelable: true }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
    pending.resolve({ type: 'complete', redirectTo: '/safe' });
    await flush();
    expect(onComplete).not.toHaveBeenCalled();
    if (action === 'dispose') expect(root.childNodes).toHaveLength(0);
    else expect(root.querySelector('form')).not.toBeNull();
  },
);

it('invalidates pending restart and completion notifications on replacement/disposal', async () => {
  const pending = deferred();
  const onRestart = vi.fn(() => pending.promise);
  const { root, view, onComplete } = mount({ onRestart });
  view.setStep({ type: 'fail', code: 'unknown', retryable: true });
  root.querySelector('button')!.click();
  view.setStep(passwordStep);
  pending.resolve({ type: 'complete', redirectTo: '/safe' });
  await flush();
  expect(onComplete).not.toHaveBeenCalled();
  view.setStep({ type: 'complete', redirectTo: '/safe' });
  view.dispose();
  await flush();
  expect(onComplete).not.toHaveBeenCalled();
});

it('inherits host typography and DSH tokens, leaves sibling content and styles intact', () => {
  const { root, view } = mount();
  root.style.fontFamily = 'serif';
  root.style.setProperty('--dsw-alias-bg-layer-1', 'Canvas');
  const sibling = document.createElement('p');
  sibling.textContent = 'host';
  root.prepend(sibling);
  view.setStep(passwordStep);
  const css = root.querySelector('style')!.textContent!;
  expect(css).toContain('font: inherit');
  expect(css).toContain('--dsw-alias-bg-layer-1');
  expect(css).not.toMatch(/#[0-9a-f]{3,8}|prefers-color-scheme|:root/i);
  root.style.setProperty('--dsw-alias-bg-layer-1', 'CanvasText');
  expect(root.querySelector('style')!.textContent).toBe(css);
  view.dispose();
  expect(root.textContent).toBe('host');
  expect(root.style.fontFamily).toBe('serif');
});

it('clears typed secrets on locale and step replacement without seeding secret defaults', () => {
  const { root, view } = mount();
  view.setStep({
    ...otpStep,
    fields: [{ ...otpStep.fields[0], value: 'do-not-seed' }],
  });
  expect(root.querySelector<HTMLInputElement>('input')!.value).toBe('');
  const otp = input(root, 'otp', '123456');
  view.setLocale('en');
  expect(otp.value).toBe('');
  view.setStep(passwordStep);
  const password = input(root, 'password', 'test-only');
  view.setStep(otpStep);
  expect(password.value).toBe('');
});

it('cancels a restart and suppresses its late rejection', async () => {
  let reject!: (error: Error) => void;
  const pending = new Promise<ClientStep>((_resolve, fail) => {
    reject = fail;
  });
  const onRestart = vi.fn<LoginOptions['onRestart']>(() => pending);
  const { root, view, onComplete } = mount({ onRestart });
  view.setStep({ type: 'fail', code: 'common.rate_limited', retryable: true });
  root.querySelector('button')!.click();
  const signal = onRestart.mock.calls[0][0].signal;
  root.querySelectorAll('button')[1].click();
  expect(signal.aborted).toBe(true);
  reject(new Error('SECRET restart failure'));
  await flush();
  expect(root.textContent).toContain('请求过于频繁');
  expect(root.textContent).not.toContain('SECRET');
  expect(onComplete).not.toHaveBeenCalled();
});
