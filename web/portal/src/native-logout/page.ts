import { createApiClient } from '../../../shared/src/api/client';
import { createIamApi } from '../../../shared/src/api/iam';

/** Only this page's transport may attach a trusted HTTP status to an error. */
class SessionHttpError extends Error {
  constructor(readonly status: number) {
    super('Session HTTP request failed');
  }
}

const messages = {
  'zh-CN': {
    title: '退出登录',
    scope: '退出后，运行中的任务会继续。',
    language: '语言 / Language',
    checking: '正在确认登录状态…',
    confirm: '确认退出当前账号？',
    submitting: '正在退出登录…',
    success: '已退出登录。',
    expired: '尚未登录或登录已失效。',
    wrongEntry: '当前入口无法处理，请返回原入口操作。',
    checkFailed: '暂时无法确认登录状态，请刷新后重试。',
    forbidden: '当前入口无法处理，请返回原入口操作或刷新后重试。',
    logoutFailed: '退出结果尚未确认，请刷新登录状态或重试。',
    cancelled: '已取消。若要退出，请重新确认。',
    cancelledLogout: '已取消等待。退出结果尚未确认，请刷新登录状态。',
    submit: '确认退出',
    retryLogout: '重试退出',
    check: '刷新登录状态',
    cancel: '取消',
    login: '重新登录',
    back: '返回',
  },
  en: {
    title: 'Sign out',
    scope: 'Your running tasks will continue after you sign out.',
    language: '语言 / Language',
    checking: 'Checking sign-in status…',
    confirm: 'Sign out of the current account?',
    submitting: 'Signing out…',
    success: 'You have signed out.',
    expired: 'You are not signed in, or your sign-in has expired.',
    wrongEntry:
      'This entry cannot handle this action. Return to your original entry.',
    checkFailed: 'Sign-in status could not be checked. Refresh and try again.',
    forbidden:
      'This entry cannot handle this action. Return to your original entry or refresh and try again.',
    logoutFailed:
      'Sign-out has not been confirmed. Refresh sign-in status or try again.',
    cancelled: 'Cancelled. Confirm again if you want to sign out.',
    cancelledLogout:
      'Stopped waiting. Sign-out has not been confirmed. Refresh sign-in status.',
    submit: 'Confirm sign-out',
    retryLogout: 'Retry sign-out',
    check: 'Refresh sign-in status',
    cancel: 'Cancel',
    login: 'Sign in again',
    back: 'Back',
  },
};
type Locale = keyof typeof messages;
type Status = Exclude<
  keyof (typeof messages)['en'],
  | 'title'
  | 'scope'
  | 'language'
  | 'submit'
  | 'retryLogout'
  | 'check'
  | 'cancel'
  | 'login'
  | 'back'
>;

export interface PortalLogoutOptions {
  fetch?: typeof fetch;
}

/** A read-only GET landing page; only a user confirmation can submit logout. */
export function mountPortalLogout(
  root: HTMLElement,
  options: PortalLogoutOptions = {},
) {
  const doc = root.ownerDocument;
  const win = doc.defaultView;
  if (!win) throw new Error('A browser document is required');
  let locale: Locale = doc.documentElement.lang.startsWith('en')
    ? 'en'
    : 'zh-CN';
  let disposed = false;
  let epoch = 0;
  let authenticated = false;
  let inFlight: 'check' | 'logout' | undefined;
  let status: Status = 'checking';
  const shell = doc.createElement('section');
  shell.className = 'portal-logout';
  const header = doc.createElement('header');
  const title = doc.createElement('h1');
  const language = doc.createElement('select');
  for (const [value, label] of [
    ['zh-CN', '中文'],
    ['en', 'English'],
  ]) {
    const option = doc.createElement('option');
    option.value = value;
    option.textContent = label;
    language.append(option);
  }
  language.value = locale;
  header.append(title, language);
  const scope = doc.createElement('p');
  const notice = doc.createElement('p');
  const actions = doc.createElement('div');
  actions.className = 'actions';
  const confirm = doc.createElement('button');
  const check = doc.createElement('button');
  const cancel = doc.createElement('button');
  for (const button of [confirm, check, cancel]) button.type = 'button';
  actions.append(confirm, check, cancel);
  const login = doc.createElement('a');
  login.className = 'login-link';
  login.href = '/instance-login.html';
  const back = doc.createElement('a');
  back.className = 'back-link';
  back.href = '/';
  shell.append(header, scope, notice, actions, login, back);
  root.append(shell);

  const client = createApiClient({
    getLocale: () => locale,
    getCookie: () => doc.cookie,
    fetch: async (input, init) => {
      const target = new URL(String(input), win.location.href);
      const method = init?.method ?? 'GET';
      const logout =
        target.pathname === '/api/iam/v1/logout' && method === 'POST';
      if (
        target.origin !== win.location.origin ||
        target.search ||
        target.hash ||
        !(logout || (target.pathname === '/api/iam/v1/me' && method === 'GET'))
      )
        throw new Error('Fixed same-origin session endpoints required');
      const response = await (options.fetch ?? globalThis.fetch)(input, {
        ...init,
        credentials: 'same-origin',
        redirect: 'error',
      });
      // The shared void API accepts other 2xx responses. This page requires the
      // existing logout contract's exact acknowledgement before showing success.
      if (
        response.redirected ||
        (logout && response.ok && response.status !== 204)
      ) {
        void response.body?.cancel().catch(() => {});
        throw new Error('Unexpected session response');
      }
      // Do not classify errors through Problem.status: the shared client may
      // prefer that untrusted body field over the actual transport status.
      if (!response.ok) {
        void response.body?.cancel().catch(() => {});
        throw new SessionHttpError(response.status);
      }
      return response;
    },
  });
  const iam = createIamApi(client);
  const current = (version: number) => !disposed && version === epoch;
  const failedLogout = () =>
    status === 'logoutFailed' || status === 'forbidden';

  function render() {
    if (disposed) return;
    const words = messages[locale];
    shell.lang = locale;
    shell.setAttribute('aria-busy', String(!!inFlight));
    title.textContent = words.title;
    scope.textContent = words.scope;
    language.setAttribute('aria-label', words.language);
    notice.textContent = words[status];
    notice.setAttribute(
      'role',
      ['checkFailed', 'forbidden', 'logoutFailed', 'wrongEntry'].includes(
        status,
      )
        ? 'alert'
        : 'status',
    );
    confirm.textContent = failedLogout() ? words.retryLogout : words.submit;
    confirm.hidden = !authenticated;
    confirm.disabled = !authenticated || !!inFlight;
    check.textContent = words.check;
    check.hidden = ![
      'checkFailed',
      'forbidden',
      'logoutFailed',
      'cancelled',
      'cancelledLogout',
    ].includes(status);
    check.disabled = !!inFlight || check.hidden;
    cancel.textContent = words.cancel;
    cancel.hidden = !inFlight && !authenticated;
    cancel.disabled = status === 'cancelled' || status === 'cancelledLogout';
    login.textContent = words.login;
    login.hidden = status !== 'success' && status !== 'expired';
    back.textContent = words.back;
    back.hidden = !login.hidden;
  }

  async function readSession() {
    if (disposed || inFlight) return;
    const version = ++epoch;
    authenticated = false;
    inFlight = 'check';
    status = 'checking';
    render();
    try {
      const me = await iam.getMe();
      if (!current(version)) return;
      if (!me?.session || !me.account?.id)
        throw new Error('Invalid session response');
      if (me.session.audience !== 'portal' || me.session.current !== true) {
        status = 'wrongEntry';
      } else {
        authenticated = true;
        status = 'confirm';
      }
    } catch (error) {
      if (!current(version)) return;
      status =
        error instanceof SessionHttpError && error.status === 401
          ? 'expired'
          : 'checkFailed';
    } finally {
      inFlight = undefined;
      render();
    }
  }

  async function submitLogout() {
    if (disposed || inFlight || !authenticated) return;
    const version = epoch;
    inFlight = 'logout';
    status = 'submitting';
    render();
    try {
      await iam.logout();
      if (!current(version)) return;
      authenticated = false;
      status = 'success';
    } catch (error) {
      if (!current(version)) return;
      if (error instanceof SessionHttpError && error.status === 401) {
        authenticated = false;
        status = 'expired';
      } else
        status =
          error instanceof SessionHttpError && error.status === 403
            ? 'forbidden'
            : 'logoutFailed';
    } finally {
      inFlight = undefined;
      render();
    }
  }

  function cancelWaiting() {
    if (disposed || cancel.disabled || (!inFlight && !authenticated)) return;
    epoch++;
    authenticated = false;
    status = inFlight === 'logout' ? 'cancelledLogout' : 'cancelled';
    // Keep inFlight until the real HTTP settles: cancellation cannot abort the
    // shared IAM API, and must never permit a concurrent replacement POST.
    render();
  }
  function setLocale(value: string) {
    locale = value.startsWith('en') ? 'en' : 'zh-CN';
    language.value = locale;
    render();
  }
  const onLanguage = () => setLocale(language.value);
  const onConfirm = () => void submitLogout();
  const onCheck = () => {
    if (!check.disabled) void readSession();
  };
  function dispose() {
    if (disposed) return;
    disposed = true;
    epoch++;
    authenticated = false;
    client.invalidatePendingRequests();
    language.removeEventListener('change', onLanguage);
    confirm.removeEventListener('click', onConfirm);
    check.removeEventListener('click', onCheck);
    cancel.removeEventListener('click', cancelWaiting);
    win!.removeEventListener('pagehide', dispose);
    shell.remove();
  }
  language.addEventListener('change', onLanguage);
  confirm.addEventListener('click', onConfirm);
  check.addEventListener('click', onCheck);
  cancel.addEventListener('click', cancelWaiting);
  win.addEventListener('pagehide', dispose);
  render();
  void readSession();
  return { setLocale, dispose };
}
