import type { components } from '../generated/iam';
import type { IamApi } from '../api/iam';
import { isApiError } from '../api/problem';
import { qrcodegen } from './qr/qrcodegen';
import { validCorrelationId } from '../api/correlation';

type Navigation = components['schemas']['UacNavigationContext'];
type Challenge = components['schemas']['UacChallenge'];
type Result =
  | components['schemas']['UacCompleteResult']
  | components['schemas']['UacSsoLoginResult'];
const messages = {
  'zh-CN': {
    preparing: '正在获取二维码',
    sso: '正在尝试企业登录',
    continue_required: '请选择继续企业登录或获取二维码',
    waiting: '请扫码登录',
    scanned: '已扫码，请在手机确认',
    completing: '正在完成登录',
    authenticated: '登录成功',
    additional_step_required: '请完成额外验证',
    expired: '二维码已过期，请重新获取',
    denied: '本次登录已被拒绝',
    unavailable: '企业登录暂不可用，请稍后重试',
    cancelled: '已取消登录',
    failed: '登录失败，请重新获取二维码',
    invalid_request: '登录请求已失效，请刷新页面',
    limited: '请求过于频繁，请稍后重试',
    uncertain: '已取消等待，登录结果尚未确认，请刷新页面',
    retry: '重新获取二维码',
    cancel: '取消',
    proceed: '继续企业登录',
    reload: '刷新页面',
    warning: '请仅为本人扫码，避免转发二维码或共享屏幕。',
    qr: '登录二维码',
    reference: '关联编号',
  },
  en: {
    preparing: 'Preparing QR code',
    sso: 'Trying company sign-in',
    continue_required: 'Continue with company sign-in or request a QR code',
    waiting: 'Scan to sign in',
    scanned: 'Confirm on your phone',
    completing: 'Completing sign-in',
    authenticated: 'Signed in',
    additional_step_required: 'Complete additional verification',
    expired: 'QR code expired. Request a new code.',
    denied: 'Sign-in was denied',
    unavailable: 'Company sign-in unavailable. Try again later.',
    cancelled: 'Sign-in cancelled',
    failed: 'Sign-in failed. Request a new code.',
    invalid_request: 'Sign-in request expired. Reload the page.',
    limited: 'Too many requests. Try again later.',
    uncertain: 'Stopped waiting. Sign-in is not confirmed. Reload the page.',
    retry: 'New QR code',
    cancel: 'Cancel',
    proceed: 'Continue with company sign-in',
    reload: 'Reload page',
    warning: 'Scan only for yourself. Do not share this code or your screen.',
    qr: 'Sign-in QR code',
    reference: 'Reference',
  },
};
type State = Exclude<
  keyof typeof messages.en,
  'retry' | 'cancel' | 'proceed' | 'reload' | 'warning' | 'qr' | 'reference'
>;
type Stage = 'sso' | 'challenge' | 'status' | 'complete' | 'cancel';

/** Local observer projection; IDs come exclusively from the K2 response header. */
export interface UacPageEvent {
  stage: Stage;
  phase: 'start' | 'end';
  result: string;
  correlationId: string | null;
}
export interface UacLoginOptions {
  targetName?: components['schemas']['LocalizedText'];
  initialCorrelationId?: string;
  api: IamApi;
  providerId: string;
  locale?: string;
  navigation?: Navigation;
  invitationCode?: string;
  onComplete(result: Result): void;
  onBusyChange?(busy: boolean): void;
  onEvent?(event: UacPageEvent): void;
}

export function createUacLoginView(
  root: HTMLElement,
  options: UacLoginOptions,
) {
  const doc = root.ownerDocument;
  let locale: 'en' | 'zh-CN' = options.locale?.startsWith('en')
    ? 'en'
    : 'zh-CN';
  let state: State = 'preparing',
    epoch = 0,
    disposed = false,
    completed = false;
  let challenge: Challenge | undefined,
    proof = options.navigation;
  let timer: ReturnType<typeof setTimeout> | undefined,
    expiry: ReturnType<typeof setTimeout> | undefined;
  let flight: Promise<unknown> | undefined,
    mutating: Stage | undefined,
    correlationId = validCorrelationId(options.initialCorrelationId);
  let operation: Promise<void> | undefined;
  let cleaning = 0;
  let retryAt = 0;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  const busy = () => !!mutating || cleaning > 0;
  function busyChanged() {
    options.onBusyChange?.(busy());
    render();
  }
  const status = doc.createElement('p');
  const target = doc.createElement('h2');
  target.dataset.testid = 'uac-target';
  status.setAttribute('role', 'status');
  const reference = doc.createElement('p');
  reference.dataset.testid = 'uac-reference';
  const qr = doc.createElement('div'),
    warning = doc.createElement('p');
  const retry = doc.createElement('button'),
    cancel = doc.createElement('button');
  const proceed = doc.createElement('button'),
    reload = doc.createElement('button');
  for (const button of [retry, cancel, proceed, reload]) button.type = 'button';
  root.replaceChildren(
    target,
    status,
    reference,
    qr,
    warning,
    proceed,
    retry,
    cancel,
    reload,
  );
  const current = (version: number) => !disposed && epoch === version;
  function render() {
    if (disposed) return;
    const words = messages[locale];
    target.textContent =
      options.targetName?.[locale] ??
      options.targetName?.en ??
      (locale === 'en' ? 'Company sign-in' : '企业登录');
    root.dataset.uacState = state;
    status.textContent = words[state];
    reference.textContent = correlationId
      ? words.reference + ': ' + correlationId
      : '';
    reference.hidden = !correlationId;
    warning.textContent = words.warning;
    retry.textContent = words.retry;
    cancel.textContent = words.cancel;
    proceed.textContent = words.proceed;
    reload.textContent = words.reload;
    const needsReload = state === 'uncertain' || state === 'invalid_request';
    retry.hidden = completed || needsReload;
    retry.disabled = busy() || Date.now() < retryAt;
    cancel.hidden =
      completed ||
      needsReload ||
      [
        'cancelled',
        'expired',
        'denied',
        'failed',
        'unavailable',
        'limited',
      ].includes(state);
    proceed.hidden = !proof || completed || needsReload;
    proceed.disabled = busy();
    reload.hidden = !needsReload;
    root.setAttribute('aria-busy', String(busy()));
    qr.querySelector('svg')?.setAttribute('aria-label', words.qr);
  }
  function show(value: State) {
    state = value;
    render();
  }
  function clear() {
    clearTimeout(timer);
    clearTimeout(expiry);
    clearTimeout(retryTimer);
    retryAt = 0;
    qr.replaceChildren();
  }
  function event(
    stage: Stage,
    phase: UacPageEvent['phase'],
    result: string,
    response?: unknown,
    updateReference = true,
  ) {
    try {
      const candidate = validCorrelationId(
        options.api.getCorrelationId(response),
      );
      if (candidate && updateReference) correlationId = candidate;
      options.onEvent?.({
        stage,
        phase,
        result,
        correlationId: candidate ?? correlationId ?? null,
      });
    } catch {
      /* Observability cannot change authentication. */
    }
  }
  async function request<T>(
    stage: Stage,
    mutation: boolean,
    action: () => Promise<T>,
  ): Promise<T> {
    const version = epoch;
    event(stage, 'start', 'pending');
    if (mutation) {
      mutating = stage;
      busyChanged();
    }
    const pending = action();
    flight = pending;
    try {
      const response = await pending;
      if (current(version)) {
        const result =
          response && typeof response === 'object' && 'state' in response
            ? response.state
            : undefined;
        const outcome =
          typeof result === 'string' &&
          [
            'waiting',
            'scanned',
            'verified',
            'expired',
            'denied',
            'unavailable',
            'cancelled',
            'failed',
            'authenticated',
            'additional_step_required',
            'scan_required',
          ].includes(result)
            ? result
            : 'success';
        event(stage, 'end', outcome, response);
      } else event(stage, 'end', 'cancelled', response, false);
      return response;
    } catch (error) {
      if (current(version)) event(stage, 'end', 'failure', error);
      else event(stage, 'end', 'cancelled', error, false);
      throw error;
    } finally {
      if (flight === pending) flight = undefined;
      if (mutation) {
        mutating = undefined;
        busyChanged();
      }
    }
  }
  async function cancelChallenge(value: Challenge | undefined) {
    if (value)
      await request('cancel', true, () =>
        options.api.cancelUacChallenge(value.id),
      );
  }
  async function invalidate() {
    epoch++;
    clear();
    cleaning++;
    busyChanged();
    const previous = challenge;
    challenge = undefined;
    const pending = flight;
    if (pending) await pending.catch(() => {});
    try {
      await cancelChallenge(previous);
    } catch {
      /* Terminal UI remains safe. */
    } finally {
      cleaning--;
      busyChanged();
    }
  }
  function image(content: string) {
    const code = qrcodegen.QrCode.encodeText(
      content,
      qrcodegen.QrCode.Ecc.MEDIUM,
    );
    const ns = 'http://www.w3.org/2000/svg',
      svg = doc.createElementNS(ns, 'svg');
    svg.setAttribute(
      'viewBox',
      '0 0 ' + (code.size + 8) + ' ' + (code.size + 8),
    );
    svg.setAttribute('width', '220');
    svg.setAttribute('height', '220');
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', messages[locale].qr);
    const bg = doc.createElementNS(ns, 'rect');
    bg.setAttribute('width', '100%');
    bg.setAttribute('height', '100%');
    bg.setAttribute('fill', 'white');
    svg.append(bg);
    let path = '';
    for (let y = 0; y < code.size; y++)
      for (let x = 0; x < code.size; x++)
        if (code.getModule(x, y))
          path += 'M' + (x + 4) + ',' + (y + 4) + 'h1v1h-1z';
    const ink = doc.createElementNS(ns, 'path');
    ink.setAttribute('d', path);
    ink.setAttribute('fill', 'black');
    svg.append(ink);
    qr.append(svg);
  }
  function finish(result: Result, version: number) {
    if (!current(version)) return;
    if (
      result.state !== 'authenticated' &&
      result.state !== 'additional_step_required'
    )
      throw new Error('Invalid login result');
    completed = true;
    challenge = undefined;
    clear();
    show(result.state);
    options.onComplete(result);
  }
  function failed(error: unknown, version: number) {
    if (!current(version)) return;
    clear();
    if (isApiError(error) && error.status === 429) {
      const seconds = error.retryAfter;
      const delay =
        seconds && /^\d+$/.test(seconds)
          ? Number(seconds) * 1000
          : Date.parse(seconds ?? '') - Date.now();
      const wait =
        Number.isFinite(delay) && delay > 0
          ? Math.min(delay, 2147483647)
          : 1000;
      retryAt = Date.now() + wait;
      retryTimer = setTimeout(() => {
        if (current(version)) render();
      }, wait);
      show('limited');
    } else if (isApiError(error) && [400, 401, 403, 409].includes(error.status))
      show('invalid_request');
    else show('unavailable');
  }
  async function poll(version: number) {
    const value = challenge;
    if (!value || !current(version)) return;
    try {
      const response = await request('status', false, () =>
        options.api.getUacChallenge(value.id),
      );
      if (!current(version)) return;
      if (response.id !== value.id) throw new Error('Mismatched challenge');
      if (response.state === 'verified') {
        clear();
        show('completing');
        finish(
          await request('complete', true, () =>
            options.api.completeUacChallenge(value.id),
          ),
          version,
        );
      } else if (response.state === 'waiting' || response.state === 'scanned') {
        show(response.state);
        timer = setTimeout(
          () => {
            operation = poll(version);
          },
          Math.max(1000, Math.min(30000, value.pollIntervalSeconds * 1000)),
        );
      } else {
        clear();
        if (
          ['expired', 'denied', 'unavailable', 'cancelled', 'failed'].includes(
            response.state,
          )
        )
          show(response.state as State);
        else throw new Error('Invalid challenge state');
      }
    } catch (error) {
      failed(error, version);
    }
  }
  async function scan() {
    if (disposed || busy() || completed || state === 'uncertain') return;
    proof = undefined;
    correlationId = undefined;
    const cleanup = invalidate(),
      version = epoch;
    await cleanup;
    if (!current(version)) return;
    show('preparing');
    try {
      const value = await request('challenge', true, () =>
        options.api.createUacChallenge({
          providerId: options.providerId,
          purpose: 'login',
          invitationCode: options.invitationCode,
        }),
      );
      if (!current(version)) {
        await cancelChallenge(value).catch(() => {});
        return;
      }
      const remaining = Date.parse(value.expiresAt) - Date.now();
      if (!Number.isFinite(remaining) || remaining <= 0) {
        show('expired');
        return;
      }
      if (
        !Number.isFinite(value.pollIntervalSeconds) ||
        value.pollIntervalSeconds <= 0
      )
        throw new Error('Invalid polling interval');
      challenge = value;
      image(value.qrContent);
      expiry = setTimeout(
        () => {
          if (current(version)) {
            void invalidate();
            show('expired');
          }
        },
        Math.min(remaining, 120000),
      );
      await poll(version);
    } catch (error) {
      failed(error, version);
    }
  }
  async function sso(mode: 'auto' | 'continue') {
    if (disposed || busy() || completed) return;
    const context = proof;
    proof = undefined;
    if (
      !context ||
      !Number.isFinite(Date.parse(context.expiresAt)) ||
      Date.parse(context.expiresAt) <= Date.now() ||
      (mode === 'auto' &&
        (context.purpose !== 'portal' ||
          !context.autoEligible ||
          context.autoSuppressed))
    )
      return scan();
    const cleanup = invalidate(),
      version = epoch;
    await cleanup;
    if (!current(version)) return;
    show('sso');
    try {
      const result = await request('sso', true, () =>
        options.api.loginUacSso({
          providerId: options.providerId,
          navigationProof: context.navigationProof,
          mode,
          invitationCode: options.invitationCode,
        }),
      );
      if (!current(version)) return;
      if (result.state === 'scan_required') await scan();
      else finish(result, version);
    } catch (error) {
      if (!current(version)) return;
      if (isApiError(error) && [400, 401, 403, 409, 429].includes(error.status))
        failed(error, version);
      else await scan();
    }
  }
  retry.onclick = () => {
    if (!retry.disabled) operation = scan();
  };
  cancel.onclick = () => {
    const uncertain = mutating === 'sso' || mutating === 'complete';
    void invalidate();
    show(uncertain ? 'uncertain' : 'cancelled');
  };
  proceed.onclick = () => {
    operation = sso('continue');
  };
  reload.onclick = () => doc.defaultView?.location.reload();
  render();
  if (
    proof?.purpose === 'portal' &&
    proof.autoEligible &&
    !proof.autoSuppressed
  )
    operation = sso('auto');
  else if (proof) show('continue_required');
  else operation = scan();
  return {
    setLocale(value: string) {
      locale = value.startsWith('en') ? 'en' : 'zh-CN';
      render();
    },
    async dispose() {
      disposed = true;
      const cleanup = invalidate();
      root.replaceChildren();
      await Promise.allSettled([cleanup, operation]);
    },
  };
}
