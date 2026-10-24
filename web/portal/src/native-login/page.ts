import {
  createLoginView,
  type LoginView,
} from '../../../../clients/dsh-plugin/src/login/index.js';
import type {
  ClientStep,
  StepInput,
} from '../../../../clients/dsh-plugin/src/generated/steps.js';
import { createApiClient } from '../../../shared/src/api/client';
import {
  createIamApi,
  type LoginProvider,
  type LoginTransaction,
} from '../../../shared/src/api/iam';
import { isApiError } from '../../../shared/src/api/problem';
import { createWorkspaceApi, instanceEntry, type Instance } from '../workspace';

const entry = '/instance-login.html';
const transactionIdPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const messages = {
  'zh-CN': {
    title: '登录并进入我的 DSH',
    scope: '此入口支持表单登录。其他登录方式、邀请和扫码确认请使用原有门户。',
    portal: '打开原有登录门户',
    language: '语言 / Language',
    checking: '正在确认登录会话…',
    providers: '请选择登录方式',
    empty: '当前没有可用的登录方式，请使用原有门户。',
    signing: '正在处理登录…',
    form: '请完成登录表单',
    unsupported: '此步骤尚未接入本页，请使用原有登录门户完成认证。',
    preparing: '正在准备我的 DSH…',
    waiting: '正在等待 DSH 就绪…',
    entering: 'DSH 已就绪，正在进入…',
    failed: 'DSH 启动失败，请重试。',
    stopped: 'DSH 已停止或正在删除，请重新准备。',
    error: '暂时无法完成请求，请重试。',
    forbidden: '当前不允许准备此工作空间，请联系管理员。',
    limited: '当前容量已满或请求过于频繁，请稍后重试。',
    expired: '登录或认证事务已失效，请重新登录。',
    timeout: '等待超时，可重新尝试。',
    cancelled: '已取消。本次晚到的响应不会启动或进入 DSH。',
    retry: '重试',
    cancel: '取消',
    change: '更换登录方式',
    logout: '退出登录',
  },
  en: {
    title: 'Sign in and enter my DSH',
    scope:
      'This entry supports form sign-in. Use the existing portal for other methods, invitations and QR confirmation.',
    portal: 'Open the existing sign-in portal',
    language: '语言 / Language',
    checking: 'Checking your sign-in session…',
    providers: 'Choose a sign-in method',
    empty: 'No sign-in methods are available. Use the existing portal.',
    signing: 'Processing sign-in…',
    form: 'Complete the sign-in form',
    unsupported:
      'This step is not available here. Complete sign-in in the existing portal.',
    preparing: 'Preparing my DSH…',
    waiting: 'Waiting for DSH to be ready…',
    entering: 'DSH is ready. Entering…',
    failed: 'DSH failed to start. Please retry.',
    stopped: 'DSH is stopped or being deleted. Prepare it again.',
    error: 'The request could not be completed. Please retry.',
    forbidden: 'You cannot prepare this workspace. Contact your administrator.',
    limited:
      'Capacity is full or requests are too frequent. Please retry later.',
    expired: 'Your session or sign-in transaction expired. Sign in again.',
    timeout: 'Waiting timed out. You can try again.',
    cancelled: 'Cancelled. Late responses will not start or enter DSH.',
    retry: 'Retry',
    cancel: 'Cancel',
    change: 'Choose another sign-in method',
    logout: 'Sign out',
  },
};
type Locale = keyof typeof messages;
type Message = keyof (typeof messages)['en'];
class ExpiredLogin extends Error {}

/** Dependencies belong to this page, never to a shared portal/admin client. */
export interface InstanceLoginOptions {
  fetch?: typeof fetch;
  navigate?: (path: string) => void;
}

/** Mount the cloud-only entry. Credentials and transactions stay in memory/cookies. */
export function mountInstanceLogin(
  root: HTMLElement,
  options: InstanceLoginOptions = {},
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
  let authBusy = false;
  let workBusy = false;
  let status: Message = 'checking';
  let alert = false;
  let retryable = false;
  let retryAt = 0;
  let restoreRetry = false;
  let providers: LoginProvider[] = [];
  let tx: LoginTransaction | undefined;
  let providerId: string | undefined;
  let view: LoginView | undefined;
  let workAbort: AbortController | undefined;
  let timeout: ReturnType<typeof setTimeout> | undefined;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  const shell = doc.createElement('section');
  shell.className = 'instance-login';
  const header = doc.createElement('header');
  const title = doc.createElement('h1');
  const languageLabel = doc.createElement('label');
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
  languageLabel.append(language);
  header.append(title, languageLabel);
  const scope = doc.createElement('p');
  const notice = doc.createElement('p');
  const choices = doc.createElement('div');
  choices.className = 'providers';
  const loginRoot = doc.createElement('div');
  const actions = doc.createElement('div');
  actions.className = 'actions';
  const retry = doc.createElement('button');
  const cancel = doc.createElement('button');
  const change = doc.createElement('button');
  for (const button of [retry, cancel, change]) button.type = 'button';
  actions.append(retry, cancel, change);
  const portal = doc.createElement('a');
  portal.className = 'portal-link';
  // The legacy Vue router cannot HTTP-load this independent HTML entry from
  // router.replace(returnTo). Its fallback therefore keeps its normal flow.
  portal.href = '/login';
  const logout = doc.createElement('a');
  logout.href = '/logout.html';
  shell.append(
    header,
    scope,
    notice,
    choices,
    loginRoot,
    actions,
    portal,
    logout,
  );
  root.append(shell);

  const client = createApiClient({
    getLocale: () => locale,
    getCookie: () => doc.cookie,
    fetch: (input, init) => {
      // All callers below use fixed same-origin API paths. Refuse redirects,
      // including a successful HTML login page masquerading as an API response.
      const target = new URL(String(input), win.location.href);
      if (target.origin !== win.location.origin)
        return Promise.reject(new Error('Same-origin API required'));
      return (options.fetch ?? globalThis.fetch)(input, {
        ...init,
        credentials: 'same-origin',
        redirect: 'error',
      });
    },
  });
  const iam = createIamApi(client);
  const workspace = createWorkspaceApi(client);
  const current = (version: number) => !disposed && version === epoch;
  const words = () => messages[locale];

  function render() {
    if (disposed) return;
    shell.lang = locale;
    title.textContent = words().title;
    scope.textContent = words().scope;
    language.setAttribute('aria-label', words().language);
    language.disabled = authBusy;
    notice.textContent = words()[status];
    notice.setAttribute('role', alert ? 'alert' : 'status');
    shell.setAttribute('aria-busy', String(authBusy || workBusy));
    retry.textContent = words().retry;
    cancel.textContent = words().cancel;
    change.textContent = words().change;
    portal.textContent = words().portal;
    logout.textContent = words().logout;
    logout.hidden = !authenticated;
    retry.hidden = !retryable;
    retry.disabled = authBusy || workBusy || Date.now() < retryAt;
    cancel.hidden = !(authBusy || workBusy || tx);
    cancel.disabled = status === 'cancelled';
    change.hidden = !tx || authenticated;
    change.disabled = authBusy || workBusy;
    choices.replaceChildren();
    if (!tx && !authenticated && !workBusy) {
      for (const provider of providers) {
        const button = doc.createElement('button');
        button.type = 'button';
        const text = provider.buttonText ?? provider.displayName;
        button.textContent = text[locale] ?? text.en ?? '';
        button.disabled = authBusy;
        button.onclick = () => void selectProvider(provider.id);
        choices.append(button);
      }
    }
  }

  function show(message: Message, isAlert = false, canRetry = false) {
    status = message;
    alert = isAlert;
    retryable = canRetry;
    render();
  }

  function stopWork() {
    workBusy = false;
    workAbort?.abort();
    win!.clearTimeout(timeout);
    win!.clearTimeout(pollTimer);
    timeout = pollTimer = undefined;
  }

  function invalidate() {
    epoch++;
    stopWork();
    win!.clearTimeout(retryTimer);
    retryTimer = undefined;
    retryAt = 0;
    // Set epoch first: the renderer abort callback cannot revive this lifecycle.
    view?.dispose();
    view = undefined;
  }

  function fail(error: unknown) {
    stopWork();
    let message: Message = 'error';
    if (error instanceof ExpiredLogin) message = 'expired';
    if (isApiError(error)) {
      if (error.status === 401) {
        authenticated = false;
        tx = undefined;
        message = 'expired';
      } else if (error.status === 403) message = 'forbidden';
      else if (error.status === 429) {
        message = 'limited';
        const seconds = error.retryAfter;
        const delay =
          seconds && /^\d+$/.test(seconds)
            ? Number(seconds) * 1000
            : Date.parse(seconds ?? '') - Date.now();
        const wait = Math.min(
          2_147_483_647,
          Number.isFinite(delay) && delay > 0 ? delay : 1000,
        );
        retryAt = Date.now() + wait;
        retryTimer = setTimeout(render, wait);
      }
    }
    show(message, true, true);
  }

  async function cancelTransaction(transaction: LoginTransaction | undefined) {
    if (
      transaction?.purpose === 'login' &&
      transaction.status === 'pending' &&
      transactionIdPattern.test(transaction.id)
    )
      await iam.cancelLoginTransaction(transaction.id);
  }

  async function cancelAuthentication() {
    const previous = tx;
    tx = undefined;
    const wasBusy = authBusy;
    invalidate();
    show('cancelled', false, true);
    // A pending create/advance owns cleanup until it settles. Do not race a
    // replacement transaction against a late Set-Cookie from that operation.
    if (wasBusy) return;
    if (previous?.status === 'pending') {
      authBusy = true;
      render();
      try {
        await cancelTransaction(previous);
      } catch {
        /* Cancellation failure never exposes server details. */
      } finally {
        authBusy = false;
        render();
      }
    }
  }

  function validateTransaction(value: LoginTransaction): ClientStep {
    if (
      !value ||
      value.purpose !== 'login' ||
      typeof value.id !== 'string' ||
      !transactionIdPattern.test(value.id) ||
      !value.step ||
      !['pending', 'completed', 'failed', 'expired', 'cancelled'].includes(
        value.status,
      )
    )
      throw new Error('Invalid login transaction');
    if (value.status === 'expired' || value.status === 'cancelled')
      throw new ExpiredLogin();
    if ((value.status === 'completed') !== (value.step.type === 'complete'))
      throw new Error('Inconsistent login completion');
    if (value.status === 'failed' && value.step.type !== 'fail')
      throw new Error('Inconsistent login failure');
    return value.step as ClientStep;
  }

  async function mutation(
    action: () => Promise<LoginTransaction>,
    version: number,
    signal?: AbortSignal,
  ) {
    if (authBusy || !current(version)) throw new Error('Inactive login');
    authBusy = true;
    const previous = tx;
    let cleaned = false;
    render();
    const onAbort = () => {
      if (current(version)) void cancelAuthentication();
    };
    signal?.addEventListener('abort', onAbort, { once: true });
    try {
      if (signal?.aborted) throw new Error('Cancelled login');
      const result = await action();
      if (!current(version) || signal?.aborted) {
        try {
          await cancelTransaction(result);
        } catch {
          /* Best effort, no retry loop. */
        }
        cleaned = true;
        throw new Error('Inactive login');
      }
      const step = validateTransaction(result);
      tx = result;
      status = ['form', 'fail', 'complete'].includes(step.type)
        ? 'form'
        : 'unsupported';
      alert = status === 'unsupported';
      return step;
    } catch (error) {
      if (!current(version) && !cleaned) {
        try {
          await cancelTransaction(previous);
        } catch {
          /* Best effort. */
        }
      }
      throw error;
    } finally {
      signal?.removeEventListener('abort', onAbort);
      authBusy = false;
      render();
    }
  }

  function installView(step: ClientStep) {
    const version = epoch;
    view = createLoginView(loginRoot, {
      locale,
      onSubmit: (input: StepInput, { signal }) => {
        const transaction = tx;
        if (!transaction || transaction.status !== 'pending')
          return Promise.reject(new Error('No pending login'));
        return mutation(
          () => iam.advanceLoginTransaction(transaction.id, input),
          version,
          signal,
        ).catch((error: unknown) => {
          if (current(version)) fail(error);
          throw error;
        });
      },
      onRestart: ({ signal }) =>
        mutation(
          async () => {
            await cancelTransaction(tx);
            if (!current(version)) throw new Error('Inactive login');
            return iam.createLoginTransaction({
              providerId: providerId!,
              purpose: 'login',
              returnTo: entry,
            });
          },
          version,
          signal,
        ).catch((error: unknown) => {
          if (current(version)) fail(error);
          throw error;
        }),
      onComplete: () => {
        if (current(version) && tx?.status === 'completed' && !workBusy)
          void verifyAndOpen();
      },
    });
    view.setStep(step);
    render();
  }

  async function selectProvider(id: string) {
    if (authBusy || workBusy || disposed) return;
    const previous = tx;
    invalidate();
    tx = undefined;
    providerId = id;
    const version = epoch;
    show('signing');
    try {
      const step = await mutation(async () => {
        await cancelTransaction(previous);
        if (!current(version)) throw new Error('Inactive login');
        return iam.createLoginTransaction({
          providerId: id,
          purpose: 'login',
          returnTo: entry,
        });
      }, version);
      if (current(version)) installView(step);
    } catch (error) {
      if (current(version)) fail(error);
    }
  }

  async function loadProviders(version: number) {
    const result = await iam.listLoginProviders();
    if (!current(version)) return;
    if (
      !Array.isArray(result?.items) ||
      result.items.some(
        (provider) =>
          !provider ||
          typeof provider.id !== 'string' ||
          typeof provider.displayName?.en !== 'string' ||
          (provider.buttonText && typeof provider.buttonText.en !== 'string'),
      )
    )
      throw new Error('Invalid login providers');
    providers = result.items;
    show(providers.length ? 'providers' : 'empty');
  }

  async function verifyAndOpen() {
    if (disposed || authBusy || workBusy) return;
    const completing = tx?.status === 'completed';
    restoreRetry = false;
    invalidate();
    const version = epoch;
    workBusy = true;
    workAbort = new globalThis.AbortController();
    const signal = workAbort.signal;
    show('checking');
    timeout = setTimeout(() => {
      if (!current(version)) return;
      invalidate();
      show('timeout', true, true);
    }, 120_000);
    try {
      const me = await iam.getMe();
      if (!current(version)) return;
      if (
        me?.session?.audience !== 'portal' ||
        me.session.current !== true ||
        !me.account?.id ||
        me.account.status !== 'active'
      )
        throw new Error('Portal session required');
      authenticated = true;
      tx = undefined;
      show('preparing');
      await observe((await workspace.open(signal)).instance, version, signal);
    } catch (error) {
      if (!current(version)) return;
      if (
        isApiError(error) &&
        error.status === 401 &&
        !authenticated &&
        !completing
      ) {
        stopWork();
        tx = undefined;
        try {
          await loadProviders(version);
        } catch (providerError) {
          if (current(version)) fail(providerError);
        }
      } else fail(error);
    }
  }

  async function observe(
    value: Instance,
    version: number,
    signal: AbortSignal,
  ) {
    if (!current(version) || signal.aborted) return;
    if (!value?.id) throw new Error('Missing instance');
    if (value.phase === 'failed' || value.error) {
      stopWork();
      show('failed', true, true);
    } else if (value.desired === 'stopped' || value.phase === 'deleting') {
      stopWork();
      show('stopped', true, true);
    } else if (
      value.ready === true &&
      ['running', 'pending_restart'].includes(value.phase)
    ) {
      const path = instanceEntry(value.id);
      stopWork();
      show('entering');
      (options.navigate ?? ((target) => win!.location.assign(target)))(path);
    } else {
      show('waiting');
      pollTimer = setTimeout(() => {
        void (async () => {
          try {
            await observe(
              await workspace.get(value.id, signal),
              version,
              signal,
            );
          } catch (error) {
            if (current(version)) fail(error);
          }
        })();
      }, 2000);
    }
  }

  async function start() {
    if (disposed || authBusy || workBusy) return;
    const params = new URLSearchParams(win!.location.search);
    const ids = params.getAll('tx');
    if (ids.length) {
      restoreRetry = true;
      invalidate();
      const version = epoch;
      show('checking');
      if (ids.length !== 1 || !transactionIdPattern.test(ids[0])) {
        show('expired', true);
        return;
      }
      try {
        const step = await mutation(
          () => iam.getLoginTransaction(ids[0]),
          version,
        );
        if (!current(version)) return;
        providerId = tx?.provider.id;
        installView(step);
      } catch (error) {
        if (current(version)) fail(error);
      }
    } else await verifyAndOpen();
  }

  function setLocale(value: string) {
    if (authBusy) {
      language.value = locale;
      return;
    }
    locale = value.startsWith('en') ? 'en' : 'zh-CN';
    language.value = locale;
    view?.setLocale(locale);
    render();
  }
  const onLanguage = () => setLocale(language.value);
  const onRetry = () => {
    if (!retry.disabled && !disposed)
      void (restoreRetry ? start() : verifyAndOpen());
  };
  const onCancel = () => void cancelAuthentication();
  const onChange = () => void cancelAuthentication();
  function dispose() {
    if (disposed) return;
    disposed = true;
    const previous = tx;
    tx = undefined;
    invalidate();
    if (!authBusy) void cancelTransaction(previous).catch(() => {});
    client.invalidatePendingRequests();
    language.removeEventListener('change', onLanguage);
    retry.removeEventListener('click', onRetry);
    cancel.removeEventListener('click', onCancel);
    change.removeEventListener('click', onChange);
    win!.removeEventListener('pagehide', dispose);
    choices.replaceChildren();
    shell.remove();
  }
  language.addEventListener('change', onLanguage);
  retry.addEventListener('click', onRetry);
  cancel.addEventListener('click', onCancel);
  change.addEventListener('click', onChange);
  win.addEventListener('pagehide', dispose);
  render();
  void start();
  return { setLocale, dispose };
}
