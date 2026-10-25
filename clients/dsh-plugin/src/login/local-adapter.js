import { createLoginView } from './index.js';
/* global HTMLElement, location, addEventListener */
const BASE = '/auriga/local-login';
let csrf = '';
/** @param {string} path @param {object} [body] @param {AbortSignal} [signal] */
async function call(path, body, signal) {
  const response = await fetch(BASE + '/' + path, {
    method: body ? 'POST' : 'GET',
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    signal,
    headers: body
      ? { 'content-type': 'application/json', 'X-Auriga-CSRF': csrf }
      : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) throw new Error('local sign-in failed');
  return response.json();
}
const root = document.querySelector('#auriga-login');
if (root instanceof HTMLElement) {
  const controller = new AbortController();
  const view = createLoginView(root, {
    locale: document.documentElement.lang,
    onSubmit: async (input, { signal }) =>
      (await call('login', { action: 'advance', input }, signal)).step,
    onRestart: async ({ signal }) => {
      const fresh = await call('status', undefined, signal);
      csrf = fresh.csrf;
      await call('logout', {}, signal);
      const state = await call('status', undefined, signal);
      csrf = state.csrf;
      return (await call('login', { action: 'begin' }, signal)).step;
    },
    onComplete: () => {
      void call('status')
        .then((state) => {
          if (state.loggedIn === true && state.connected === true)
            location.assign('/');
        })
        .catch(() => {});
    },
  });
  void call('status', undefined, controller.signal)
    .then(async (state) => {
      if (state.remoteRevocation === 'pending') {
        const notice = document.createElement('p');
        notice.setAttribute('role', 'status');
        notice.textContent = document.documentElement.lang.startsWith('zh')
          ? '已退出本机；远端设备撤销正在重试。'
          : 'Signed out locally; retrying remote device revocation.';
        root.prepend(notice);
      }
      if (state.loggedIn === true && state.connected === true) {
        location.assign('/');
        return;
      }
      csrf = state.csrf;
      view.setStep(
        (
          await call(
            'login',
            { action: state.resumeAvailable ? 'resume' : 'begin' },
            controller.signal,
          )
        ).step,
      );
    })
    .catch(() =>
      view.setStep({
        type: 'fail',
        code: 'common.unavailable',
        retryable: true,
      }),
    );
  addEventListener(
    'pagehide',
    () => {
      controller.abort();
      view.dispose();
    },
    { once: true },
  );
}
