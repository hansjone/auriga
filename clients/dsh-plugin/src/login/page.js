import { createLoginView } from './index.js';
import { createLogoutView } from './logout.js';
import {
  createLocalLoginController,
  LOCAL_LOGIN_BASE,
} from './local-controller.js';

/** Own the anonymous page lifecycle; all navigation targets are fixed locally.
 * @param {HTMLElement} root
 * @param {{locale?:string,logout?:boolean,fetch?:typeof fetch,navigate:(path:string)=>void}} options */
export function mountLocalLoginPage(root, options) {
  const scope = new AbortController();
  const notice = root.ownerDocument.createElement('p');
  notice.setAttribute('role', 'status');
  const controller = createLocalLoginController({
    fetch: options.fetch,
    onStatus: (state) => {
      notice.textContent =
        state.remoteRevocation === 'pending'
          ? (options.locale ?? 'zh-CN').toLowerCase().startsWith('zh')
            ? '已退出本机；远端设备撤销正在重试。'
            : 'Signed out locally; retrying remote device revocation.'
          : '';
    },
  });
  const view = options.logout
    ? createLogoutView(root, {
        locale: options.locale,
        onConfirm: ({ signal }) => controller.logout(signal),
        onReturn: () => options.navigate('/'),
        onSignedOut: () => options.navigate(LOCAL_LOGIN_BASE),
      })
    : createLoginView(root, {
        locale: options.locale,
        onSubmit: controller.submit,
        onRestart: controller.restart,
        onCancel: () => controller.cancel(scope.signal),
        onComplete: () => {
          if (!scope.signal.aborted) options.navigate('/');
        },
      });
  if ('load' in view && typeof view.load === 'function') {
    root.prepend(notice);
    view.load(controller.start);
  }
  return {
    dispose() {
      scope.abort();
      view.dispose();
      notice.remove();
    },
  };
}
