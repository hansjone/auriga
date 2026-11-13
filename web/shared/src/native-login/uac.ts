import type { components } from '../generated/iam';
import type { IamApi } from '../api/iam';
import { isApiError } from '../api/problem';
import { qrcodegen } from './qr/qrcodegen';

type Navigation = components['schemas']['UacNavigationContext'];
type Result =
  | components['schemas']['UacCompleteResult']
  | components['schemas']['UacSsoLoginResult'];

/** Transport-free DOM host; cookies and upstream credentials never enter state. */
export function createUacLoginView(
  root: HTMLElement,
  options: {
    api: IamApi;
    providerId: string;
    locale?: string;
    navigation?: Navigation;
    invitationCode?: string;
    onComplete: (result: Result) => void;
  },
) {
  const doc = root.ownerDocument;
  const en = options.locale?.startsWith('en');
  let epoch = 0;
  let disposed = false;
  let mutationPending = false;
  let challenge: components['schemas']['UacChallenge'] | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let expiry: ReturnType<typeof setTimeout> | undefined;
  let proof = options.navigation;
  const status = doc.createElement('p');
  status.setAttribute('role', 'status');
  const qr = doc.createElement('div');
  const warning = doc.createElement('p');
  warning.textContent = en
    ? 'Scan only for yourself. Do not share this code or your screen.'
    : '请仅为本人扫码，避免转发二维码或共享屏幕。';
  const retry = doc.createElement('button');
  retry.type = 'button';
  retry.textContent = en ? 'New QR code' : '重新获取二维码';
  const cancel = doc.createElement('button');
  cancel.type = 'button';
  cancel.textContent = en ? 'Cancel' : '取消';
  const proceed = doc.createElement('button');
  proceed.type = 'button';
  proceed.textContent = en ? 'Continue with company sign-in' : '继续企业登录';
  proceed.hidden = !proof;
  root.replaceChildren(status, qr, warning, proceed, retry, cancel);
  const current = (version: number) => !disposed && epoch === version;
  function clear() {
    clearTimeout(timer);
    clearTimeout(expiry);
    qr.replaceChildren();
  }
  function text(zh: string, english: string) {
    status.textContent = en ? english : zh;
  }
  async function invalidate() {
    epoch++;
    clear();
    const old = challenge;
    challenge = undefined;
    if (old) await options.api.cancelUacChallenge(old.id).catch(() => {});
  }
  function image(content: string) {
    const code = qrcodegen.QrCode.encodeText(
      content,
      qrcodegen.QrCode.Ecc.MEDIUM,
    );
    const ns = 'http://www.w3.org/2000/svg';
    const svg = doc.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', `0 0 ${code.size + 8} ${code.size + 8}`);
    svg.setAttribute('width', '220');
    svg.setAttribute('height', '220');
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', en ? 'Sign-in QR code' : '登录二维码');
    const bg = doc.createElementNS(ns, 'rect');
    bg.setAttribute('width', '100%');
    bg.setAttribute('height', '100%');
    bg.setAttribute('fill', 'white');
    svg.append(bg);
    let path = '';
    for (let y = 0; y < code.size; y++)
      for (let x = 0; x < code.size; x++)
        if (code.getModule(x, y)) path += `M${x + 4},${y + 4}h1v1h-1z`;
    const ink = doc.createElementNS(ns, 'path');
    ink.setAttribute('d', path);
    ink.setAttribute('fill', 'black');
    svg.append(ink);
    qr.append(svg);
  }
  async function poll(version: number) {
    if (!challenge || !current(version)) return;
    try {
      const value = await options.api.getUacChallenge(challenge.id);
      if (!current(version) || !challenge) return;
      if (value.id !== challenge.id) throw new Error('Mismatched challenge');
      if (value.state === 'verified') {
        mutationPending = true;
        retry.disabled = true;
        proceed.disabled = true;
        clear();
        try {
          const result = await options.api.completeUacChallenge(challenge.id);
          if (current(version)) {
            challenge = undefined;
            options.onComplete(result);
          }
        } finally {
          mutationPending = false;
          retry.disabled = false;
          proceed.disabled = false;
        }
      } else if (value.state === 'waiting' || value.state === 'scanned') {
        text(
          value.state === 'scanned' ? '已扫码，请在手机确认' : '请扫码登录',
          value.state === 'scanned'
            ? 'Confirm on your phone'
            : 'Scan to sign in',
        );
        timer = setTimeout(
          () => void poll(version),
          Math.max(1000, Math.min(30000, challenge.pollIntervalSeconds * 1000)),
        );
      } else {
        clear();
        text(
          '本次扫码已结束，请重新获取二维码',
          'This attempt ended. Request a new QR code.',
        );
      }
    } catch {
      if (current(version)) {
        clear();
        text('登录暂不可用，请重试', 'Sign-in unavailable. Please retry.');
      }
    }
  }
  async function scan() {
    if (mutationPending || disposed) return;
    mutationPending = true;
    proceed.disabled = true;
    retry.disabled = true;
    const pending = invalidate();
    const version = epoch;
    await pending;
    if (!current(version)) {
      mutationPending = false;
      retry.disabled = false;
      proceed.disabled = false;
      return;
    }
    text('正在获取二维码', 'Preparing QR code');
    try {
      const value = await options.api.createUacChallenge({
        providerId: options.providerId,
        purpose: 'login',
        invitationCode: options.invitationCode,
      });
      if (!current(version)) {
        await options.api.cancelUacChallenge(value.id).catch(() => {});
        return;
      }
      const remaining = Date.parse(value.expiresAt) - Date.now();
      if (
        !Number.isFinite(remaining) ||
        remaining <= 0 ||
        !Number.isFinite(value.pollIntervalSeconds)
      )
        throw new Error('Invalid challenge');
      challenge = value;
      image(value.qrContent);
      expiry = setTimeout(
        () => {
          if (current(version)) {
            void invalidate();
            retry.disabled = false;
            text(
              '二维码已过期，请重新获取',
              'QR code expired. Request a new code.',
            );
          }
        },
        Math.min(remaining, 120000),
      );
      await poll(version);
    } catch {
      if (current(version)) {
        clear();
        text('登录暂不可用，请重试', 'Sign-in unavailable. Please retry.');
      }
    } finally {
      mutationPending = false;
      retry.disabled = false;
      proceed.disabled = false;
    }
  }
  async function sso(mode: 'auto' | 'continue') {
    if (mutationPending || disposed) return;
    const context = proof;
    proof = undefined;
    proceed.hidden = true;
    if (
      !context ||
      Date.parse(context.expiresAt) <= Date.now() ||
      (mode === 'auto' &&
        (context.purpose !== 'portal' ||
          !context.autoEligible ||
          context.autoSuppressed))
    )
      return scan();
    mutationPending = true;
    const pending = invalidate();
    const version = epoch;
    await pending;
    if (!current(version)) {
      mutationPending = false;
      retry.disabled = false;
      return;
    }
    retry.disabled = true;
    try {
      const result = await options.api.loginUacSso({
        providerId: options.providerId,
        navigationProof: context.navigationProof,
        mode,
        invitationCode: options.invitationCode,
      });
      if (!current(version)) return;
      if (result.state === 'scan_required') {
        mutationPending = false;
        await scan();
      } else options.onComplete(result);
    } catch (error) {
      if (current(version)) {
        mutationPending = false;
        if (isApiError(error) && [400, 401, 403, 409].includes(error.status))
          text(
            '登录请求已失效，请刷新页面',
            'Sign-in request expired. Reload the page.',
          );
        else await scan();
      }
    } finally {
      mutationPending = false;
      retry.disabled = false;
    }
  }
  retry.onclick = () => void scan();
  cancel.onclick = () => {
    void invalidate();
    text('已取消', 'Cancelled');
    retry.disabled = mutationPending;
  };
  proceed.onclick = () => void sso('continue');
  if (
    proof?.purpose === 'portal' &&
    proof.autoEligible &&
    !proof.autoSuppressed
  )
    void sso('auto');
  else void scan();
  return {
    dispose() {
      disposed = true;
      void invalidate();
      root.replaceChildren();
    },
  };
}
