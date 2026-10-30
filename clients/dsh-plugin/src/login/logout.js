import { loginStyles } from './styles.js';

/** Explicit confirmation of local device logout; the host retains ownership of tasks.
 * @param {HTMLElement} root
 * @param {{locale?:string,onConfirm:(context:{signal:AbortSignal})=>Promise<{loggedIn:boolean,remoteRevocation?:string}>,onReturn:()=>void,onSignedOut:()=>void}} options */
export function createLogoutView(root, options) {
  const doc = root.ownerDocument;
  const zh = (options.locale ?? 'zh-CN').toLowerCase().startsWith('zh');
  const text = zh
    ? {
        title: '确认退出本机登录？',
        hint: '退出后本机身份失效，DSH 和正在运行的任务继续。',
        confirm: '确认退出',
        back: '取消',
        waiting: '正在退出…',
        failed: '退出未完成，请检查连接后重试。',
        complete: '已退出本机登录。',
        pending: '已退出本机；远端设备撤销正在重试。',
        login: '返回登录',
      }
    : {
        title: 'Sign out locally?',
        hint: 'Local identity access will end. DSH and running tasks will continue.',
        confirm: 'Confirm sign out',
        back: 'Cancel',
        waiting: 'Signing out…',
        failed: 'Sign-out did not complete. Check the connection and retry.',
        complete: 'Signed out locally.',
        pending: 'Signed out locally; retrying remote device revocation.',
        login: 'Back to sign in',
      };
  const shell = doc.createElement('div');
  shell.className = 'auriga-login';
  shell.lang = options.locale ?? 'zh-CN';
  root.append(shell);
  let disposed = false;
  /** @type {AbortController | undefined} */
  let request;
  /** @type {(() => void)[]} */
  let cleanup = [];
  /** @template {keyof HTMLElementTagNameMap} K @param {K} tag @param {string} value */
  function element(tag, value) {
    const node = doc.createElement(tag);
    node.textContent = value;
    return node;
  }
  /** @param {HTMLElement} card @param {string} label @param {string} action @param {()=>void} callback */
  function button(card, label, action, callback) {
    const control = element('button', label);
    control.type = 'button';
    control.dataset.action = action;
    control.addEventListener('click', callback);
    cleanup.push(() => control.removeEventListener('click', callback));
    card.append(control);
  }
  /** @param {'confirm'|'waiting'|'failed'|'complete'|'pending'} phase */
  function render(phase) {
    for (const remove of cleanup) remove();
    cleanup = [];
    shell.replaceChildren(element('style', loginStyles));
    const card = element('section', '');
    card.className = 'card';
    card.setAttribute('aria-label', text.title);
    card.setAttribute('aria-busy', String(phase === 'waiting'));
    shell.append(card);
    card.append(
      element(
        'h2',
        phase === 'complete' || phase === 'pending'
          ? text.complete
          : text.title,
      ),
    );
    card.append(element('p', text.hint));
    if (phase !== 'confirm') {
      const status = element('p', text[phase]);
      status.setAttribute('role', phase === 'failed' ? 'alert' : 'status');
      card.append(status);
    }
    if (phase === 'complete' || phase === 'pending') {
      button(card, text.login, 'login', options.onSignedOut);
    } else if (phase !== 'waiting') {
      button(card, text.confirm, 'confirm', () => {
        void confirm();
      });
      button(card, text.back, 'return', options.onReturn);
    }
  }
  async function confirm() {
    if (request || disposed) return;
    const controller = new AbortController();
    request = controller;
    render('waiting');
    try {
      const result = await options.onConfirm({ signal: controller.signal });
      if (disposed || controller.signal.aborted) return;
      if (
        result?.loggedIn !== false ||
        !['pending', 'complete'].includes(result.remoteRevocation ?? '')
      )
        throw new Error('unconfirmed local logout');
      render(result.remoteRevocation === 'pending' ? 'pending' : 'complete');
    } catch {
      if (!disposed && !controller.signal.aborted) render('failed');
    } finally {
      if (request === controller) request = undefined;
    }
  }
  render('confirm');
  return {
    dispose() {
      if (disposed) return;
      disposed = true;
      request?.abort();
      for (const remove of cleanup) remove();
      cleanup = [];
      shell.remove();
    },
  };
}
