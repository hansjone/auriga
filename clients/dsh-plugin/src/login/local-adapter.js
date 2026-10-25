import { mountLocalLoginPage } from './page.js';
/* global HTMLElement, location, addEventListener */
const root = document.querySelector('#auriga-login');
if (root instanceof HTMLElement) {
  const page = mountLocalLoginPage(root, {
    locale: document.documentElement.lang,
    logout: new URL(location.href).searchParams.get('logout') === '1',
    navigate: (path) => location.assign(path),
  });
  addEventListener('pagehide', () => page.dispose(), { once: true });
}
