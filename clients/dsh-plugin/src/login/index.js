import { loginStyles } from './styles.js';
import { failureReason } from './errors.js';

/** @typedef {import('../generated/steps.js').ClientStep} ClientStep */
/** @typedef {import('../generated/steps.js').StepInput} StepInput */
/** @typedef {import('../generated/steps.js').LocalizedText} LocalizedText */
/**
 * @typedef {object} LoginOptions
 * @property {string} [locale] Host locale; defaults to zh-CN.
 * @property {(input: StepInput, context: {signal: AbortSignal}) => Promise<ClientStep>} onSubmit
 * @property {(context: {signal: AbortSignal}) => Promise<ClientStep>} onRestart
 * @property {() => void} onComplete The host owns session verification and navigation.
 * @property {() => Promise<void>} [onCancel] Cancel only this anonymous attempt.
 */

let nextId = 0;
const messages = {
  'zh-CN': {
    title: '登录',
    submit: '继续',
    retry: '重新开始',
    cancel: '取消',
    waiting: '正在处理…',
    complete: '登录步骤已完成',
    unavailable: '当前登录方式暂不可用，请联系管理员。',
    failed: '登录失败，请重试。',
    invalid: '用户名或口令不正确。',
    expired: '登录已过期，请重新开始。',
    limited: '请求过于频繁，请稍后重试。',
    network: '网络不可达，请检查连接后重试。',
    unconfigured: '平台地址尚未配置，请联系安装者配置后重试。',
    credentialExpired: '本机凭据已过期或失效，请重新登录。',
    cancelled: '已取消登录，可以重新开始。',
    qr: '扫码登录',
    redirect: '跳转登录',
    webauthn: 'Passkey 登录',
    device: '设备确认',
  },
  en: {
    title: 'Sign in',
    submit: 'Continue',
    retry: 'Start again',
    cancel: 'Cancel',
    waiting: 'Working…',
    complete: 'Sign-in step completed',
    unavailable:
      'This sign-in method is unavailable. Contact your administrator.',
    failed: 'Sign-in failed. Please try again.',
    invalid: 'Invalid username or password.',
    expired: 'Sign-in expired. Please start again.',
    limited: 'Too many requests. Try again later.',
    network: 'Network unreachable. Check your connection and retry.',
    unconfigured:
      'Platform address is not configured. Ask the installer to configure it, then retry.',
    credentialExpired:
      'Local credentials expired or became invalid. Sign in again.',
    cancelled: 'Sign-in cancelled. You can start again.',
    qr: 'Scan to sign in',
    redirect: 'External sign-in',
    webauthn: 'Passkey sign-in',
    device: 'Device confirmation',
  },
};

/**
 * Mount an isolated native step renderer; no transport, credentials or routing.
 * @param {HTMLElement} root An exclusive login container owned by the host.
 * @param {LoginOptions} options
 */
export function createLoginView(root, options) {
  const doc = root.ownerDocument;
  if (!doc.defaultView)
    throw new Error('Login view requires a browser document');
  const InputElement = doc.defaultView.HTMLInputElement;
  const id = `auriga-login-${++nextId}`;
  let locale = options.locale || 'zh-CN';
  /** @type {ClientStep | undefined} */
  let step;
  /** @type {import('./errors.js').LoginFailureReason | undefined} */
  let notice;
  let cancelling = false;
  let disposed = false;
  let version = 0;
  let completionVersion = 0;
  /** @type {AbortController | undefined} */
  let request;
  /** @type {(() => void)[]} */
  let cleanup = [];
  /** @type {HTMLInputElement[]} */
  let sensitive = [];
  const shell = doc.createElement('div');
  shell.className = 'auriga-login';
  root.append(shell);

  const words = () =>
    messages[locale.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'];
  /** @param {LocalizedText | undefined} value */
  const localize = (value) => value?.[locale] ?? value?.en ?? '';
  /** @param {string | undefined} code */
  function errorText(code) {
    if (code === 'iam.invalid_credentials') return words().invalid;
    if (code === 'iam.login_tx_expired') return words().expired;
    if (code === 'common.unauthenticated') return words().credentialExpired;
    if (code === 'common.unavailable') return words().network;
    if (code === 'common.rate_limited') return words().limited;
    return words().failed;
  }
  function clearSensitive() {
    for (const input of sensitive) input.value = '';
  }
  /** @param {boolean} [preserveCompletion] Locale changes preserve the current step's pending notification. */
  function invalidate(preserveCompletion = false) {
    version++;
    if (!preserveCompletion) completionVersion++;
    request?.abort();
    request = undefined;
    clearSensitive();
    sensitive = [];
    for (const remove of cleanup) remove();
    cleanup = [];
  }
  /** @param {HTMLElement} target @param {string} event @param {EventListener} callback */
  function listen(target, event, callback) {
    target.addEventListener(event, callback);
    cleanup.push(() => target.removeEventListener(event, callback));
  }
  /** @template {keyof HTMLElementTagNameMap} K @param {K} tag @param {string} [text] @param {string} [className] */
  function element(tag, text = '', className = '') {
    const node = doc.createElement(tag);
    node.textContent = text;
    node.className = className;
    return node;
  }
  /** @param {ClientStep} next */
  function setStep(next) {
    if (disposed) return;
    invalidate();
    notice = undefined;
    step = next;
    render();
    if (next.type === 'complete') {
      const current = completionVersion;
      // A synchronous host replacement/disposal must invalidate completion too.
      void Promise.resolve()
        .then(() => {
          if (!disposed && completionVersion === current) options.onComplete();
        })
        .catch(() => {
          /* Host callback errors must never expose sensitive details. */
        });
    }
  }
  /** @param {(signal: AbortSignal) => Promise<ClientStep>} action @param {HTMLElement} card */
  async function run(action, card) {
    if (request || cancelling || disposed) return;
    card.querySelector('[data-action=cancel]')?.remove();
    const current = version;
    const controller = new AbortController();
    request = controller;
    card.setAttribute('aria-busy', 'true');
    const controls = card.querySelectorAll('input, select, button');
    for (const control of controls) control.setAttribute('disabled', '');
    const status = element('p', words().waiting);
    status.setAttribute('role', 'status');
    const cancel = element('button', words().cancel);
    cancel.type = 'button';
    cancel.dataset.action = 'cancel';
    listen(cancel, 'click', () => {
      void cancelAttempt();
    });
    card.append(status, cancel);
    try {
      const next = await action(controller.signal);
      if (!disposed && current === version && !controller.signal.aborted)
        setStep(next);
    } catch (error) {
      if (!disposed && current === version && !controller.signal.aborted) {
        invalidate();
        notice = failureReason(error);
        render();
      }
    }
  }
  async function cancelAttempt() {
    if (disposed || cancelling) return;
    invalidate();
    const current = version;
    cancelling = true;
    notice = 'cancelled';
    render();
    try {
      await options.onCancel?.();
    } catch (error) {
      if (!disposed && current === version) notice = failureReason(error);
    } finally {
      cancelling = false;
      if (!disposed) {
        if (current === version) invalidate();
        render();
      }
    }
  }
  /** @param {HTMLElement} card */
  function restartButton(card) {
    const retry = element('button', words().retry);
    retry.type = 'button';
    retry.dataset.action = 'restart';
    retry.disabled = cancelling;
    listen(retry, 'click', () => {
      void run((signal) => options.onRestart({ signal }), card);
    });
    card.append(retry);
  }
  /** @param {HTMLElement} card */
  function cancelButton(card) {
    const cancel = element('button', words().cancel);
    cancel.type = 'button';
    cancel.dataset.action = 'cancel';
    cancel.disabled = cancelling;
    listen(cancel, 'click', () => {
      void cancelAttempt();
    });
    card.append(cancel);
  }
  function render() {
    for (const remove of cleanup) remove();
    cleanup = [];
    shell.replaceChildren();
    shell.lang = locale;
    const style = element('style', loginStyles);
    const brand = element('div', '', 'brand');
    const mark = doc.createElementNS('http://www.w3.org/2000/svg', 'svg');
    mark.setAttribute('viewBox', '0 0 32 32');
    mark.setAttribute('aria-hidden', 'true');
    const path = doc.createElementNS(mark.namespaceURI, 'path');
    path.setAttribute(
      'd',
      'M16 2.5 27.7 9.25v13.5L16 29.5 4.3 22.75V9.25z M16 9.5V16l-5.6 3.2M16 16l5.6 3.2',
    );
    path.setAttribute('fill', 'none');
    path.setAttribute('stroke', 'currentColor');
    mark.append(path);
    brand.append(mark, doc.createTextNode('Auriga'));
    const card = element('section', '', 'card');
    card.dataset.step = step?.type ?? '';
    const title = element('h2', words().title);
    title.id = `${id}-title`;
    card.setAttribute('aria-labelledby', title.id);
    card.append(title);
    shell.append(style, brand, card);
    if (notice) {
      const text =
        notice === 'expired' ? words().credentialExpired : words()[notice];
      const status = element(
        'p',
        text,
        notice === 'cancelled' ? 'muted' : 'error',
      );
      status.setAttribute('role', notice === 'cancelled' ? 'status' : 'alert');
      card.append(status);
      restartButton(card);
    }
    if (!step) return;
    if (step.type === 'form') {
      const formStep = step;
      title.textContent = localize(formStep.title) || words().title;
      if (formStep.description)
        card.append(element('p', localize(formStep.description), 'muted'));
      const form = element('form');
      /** @type {{name: string, control: HTMLInputElement | HTMLSelectElement, checkbox: boolean, hiddenValue?: string | boolean}[]} */
      const fields = [];
      for (const [index, field] of formStep.fields.entries()) {
        if (field.kind === 'info') {
          form.append(element('p', localize(field.label), 'muted'));
          if (field.help)
            form.append(element('p', localize(field.help), 'muted'));
          continue;
        }
        const row = element('div', '', 'field');
        const control =
          field.kind === 'select' ? element('select') : element('input');
        const secret = field.kind === 'password' || field.kind === 'otp';
        control.id = `${id}-field-${index}`;
        control.name = field.name;
        control.required = !!field.required;
        if (control instanceof InputElement) {
          control.type =
            field.kind === 'otp'
              ? 'text'
              : field.kind === 'select'
                ? 'text'
                : field.kind;
          control.placeholder = localize(field.placeholder);
          if (field.pattern) control.pattern = field.pattern;
          if (field.minLength !== undefined)
            control.minLength = field.minLength;
          if (field.maxLength !== undefined)
            control.maxLength = field.maxLength;
          if (field.kind === 'checkbox') control.checked = field.value === true;
          if (field.kind === 'otp') control.inputMode = 'numeric';
          if (secret) sensitive.push(control);
        } else {
          for (const option of field.options || []) {
            const item = element('option', localize(option.label));
            item.value = option.value;
            control.append(item);
          }
        }
        if (field.autocomplete)
          control.setAttribute('autocomplete', field.autocomplete);
        else if (field.kind === 'otp')
          control.setAttribute('autocomplete', 'one-time-code');
        if (!secret && typeof field.value === 'string')
          control.value = field.value;
        fields.push({
          name: field.name,
          control,
          checkbox: field.kind === 'checkbox',
          hiddenValue:
            field.kind === 'hidden' ? (field.value ?? '') : undefined,
        });
        if (field.kind === 'hidden') {
          form.append(control);
          continue;
        }
        const label = element('label', localize(field.label) || field.name);
        label.htmlFor = control.id;
        row.append(label, control);
        const descriptions = [];
        if (field.help) {
          const help = element('p', localize(field.help), 'muted');
          help.id = `${control.id}-help`;
          descriptions.push(help.id);
          row.append(help);
        }
        const fieldError = formStep.fieldErrors?.[field.name];
        if (fieldError) {
          const error = element('p', errorText(fieldError.code), 'error');
          error.id = `${control.id}-error`;
          error.setAttribute('role', 'alert');
          control.setAttribute('aria-invalid', 'true');
          descriptions.push(error.id);
          row.append(error);
        }
        if (descriptions.length)
          control.setAttribute('aria-describedby', descriptions.join(' '));
        form.append(row);
      }
      if (formStep.error) {
        const error = element('p', errorText(formStep.error.code), 'error');
        error.setAttribute('role', 'alert');
        form.append(error);
      }
      const submit = element(
        'button',
        localize(formStep.submitLabel) || words().submit,
      );
      submit.type = 'submit';
      form.append(submit);
      listen(form, 'submit', (event) => {
        event.preventDefault();
        if (
          request ||
          cancelling ||
          notice === 'cancelled' ||
          disposed ||
          !form.reportValidity()
        )
          return;
        /** @type {Record<string, string | boolean>} */
        const values = Object.create(null);
        for (const { name, control, checkbox, hiddenValue } of fields) {
          values[name] =
            hiddenValue !== undefined
              ? hiddenValue
              : checkbox && control instanceof InputElement
                ? control.checked
                : control.value;
        }
        clearSensitive();
        void run(
          (signal) =>
            options.onSubmit(
              { type: 'form', formId: formStep.formId, values },
              { signal },
            ),
          card,
        );
      });
      card.append(form);
      if (notice === 'cancelled' || cancelling)
        for (const control of form.querySelectorAll('input, select, button'))
          control.setAttribute('disabled', '');
      if (options.onCancel && !notice) cancelButton(card);
    } else if (step.type === 'complete') {
      const status = element('p', words().complete);
      status.setAttribute('role', 'status');
      card.append(status);
    } else {
      const method = ['qr', 'redirect', 'webauthn', 'device'].includes(
        step.type,
      );
      if (method) {
        const type = /** @type {'qr'|'redirect'|'webauthn'|'device'} */ (
          step.type
        );
        title.textContent = words()[type];
      }
      let expired = false;
      if (step.type === 'qr' || step.type === 'device') {
        const remaining = Date.parse(step.expiresAt) - Date.now();
        expired = Number.isFinite(remaining) && remaining <= 0;
        if (remaining > 0) {
          const timer = setTimeout(
            () => {
              if (!disposed) {
                invalidate();
                render();
              }
            },
            Math.min(remaining, 2147483647),
          );
          cleanup.push(() => clearTimeout(timer));
        }
      }
      const error = element(
        'p',
        step.type === 'fail'
          ? errorText(step.code)
          : expired
            ? words().expired
            : words().unavailable,
        'error',
      );
      error.setAttribute('role', 'alert');
      card.append(error);
      if (!notice && ((step.type === 'fail' && step.retryable) || method)) {
        restartButton(card);
        if (method) cancelButton(card);
      }
    }
  }
  render();
  return {
    setStep,
    /** @param {import('./errors.js').LoginFailureReason} reason */
    setFailure(reason) {
      if (disposed) return;
      invalidate();
      step = undefined;
      notice = reason;
      render();
    },
    /** @param {(context:{signal:AbortSignal})=>Promise<ClientStep>} action */
    load(action) {
      if (disposed) return;
      const card = shell.querySelector('.card');
      if (card instanceof doc.defaultView.HTMLElement)
        void run((signal) => action({ signal }), card);
    },
    /** @param {string} next */
    setLocale(next) {
      if (disposed) return;
      invalidate(true);
      locale = next || 'zh-CN';
      render();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      invalidate();
      step = undefined;
      shell.remove();
    },
  };
}
