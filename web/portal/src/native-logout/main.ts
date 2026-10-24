import { mountPortalLogout } from './page';
import { mountDocumentPage } from './lifecycle';
import './style.css';

const root = document.getElementById('portal-logout');
export const disposeEntry = root
  ? mountDocumentPage(root, mountPortalLogout)
  : () => {};
