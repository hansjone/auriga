import { mountInstanceLogin } from './page';
import { mountDocumentPage } from '../native-logout/lifecycle';
import './style.css';

const root = document.getElementById('instance-login');
export const disposeEntry = root
  ? mountDocumentPage(root, mountInstanceLogin)
  : () => {};
