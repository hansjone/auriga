import type { Me } from '../api/iam';
import { isApiError } from '../api/problem';

type LoginInfo = Me['uacLoginInfo'];

export function loginInfoText(info: LoginInfo, locale: string): string {
  if (!info) return '';
  const en = locale.startsWith('en');
  if (info.status === 'available' && info.department?.trim()) {
    const updated =
      info.updatedAt && Number.isFinite(Date.parse(info.updatedAt))
        ? ` · ${new Date(info.updatedAt).toLocaleString(locale)}`
        : '';
    return `${en ? 'Department' : '所属部门'}：${info.department}${updated}`;
  }
  return en
    ? 'Department information is temporarily unavailable'
    : '部门信息暂不可用';
}

/** Read only the current session projection; never request a department lookup. */
export function watchLoginInfo(
  initial: Me,
  readMe: () => Promise<Me>,
  callbacks: {
    onInfo(info: LoginInfo): void;
    onIdentityChanged(): void;
    onExpired(): void;
  },
) {
  let active = true;
  let attempts = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  function identityKey(me: Me) {
    const source = me.session.uacSessionBinding;
    return `${me.account.tenantId}/${me.account.id}/${me.session.id}/${me.session.audience}/${source?.sourceSessionId}/${source?.providerId}/${source?.authGeneration}`;
  }
  const owner = identityKey(initial);
  const unavailable: LoginInfo = {
    purpose: 'login_info',
    status: 'unavailable',
  };
  function dispose() {
    active = false;
    clearTimeout(timer);
  }
  function receive(me: Me) {
    if (!active) return;
    const identity = identityKey(me);
    if (
      identity !== owner ||
      !me.session.current ||
      me.account.status !== 'active'
    ) {
      dispose();
      callbacks.onInfo(undefined);
      callbacks.onIdentityChanged();
      return;
    }
    callbacks.onInfo(me.uacLoginInfo);
    if (me.uacLoginInfo?.status === 'pending') {
      if (attempts >= 30) callbacks.onInfo(unavailable);
      else timer = setTimeout(() => void refresh(), 2000);
    }
  }
  async function refresh() {
    if (!active) return;
    attempts++;
    try {
      receive(await readMe());
    } catch (error) {
      if (!active) return;
      if (isApiError(error) && error.status === 401) {
        dispose();
        callbacks.onInfo(undefined);
        callbacks.onExpired();
      } else callbacks.onInfo(unavailable);
    }
  }
  receive(initial);
  return { dispose };
}
