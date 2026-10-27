import { isApiError } from '../api/problem';
import type { LoginTransaction } from '../api/iam';

export class RedirectStepUpError extends Error {}

/** Keep the original write and its draft in the parent document. */
export function startRedirectStepUp(
  transaction: LoginTransaction,
  returnTo: string,
  read: (id: string) => Promise<LoginTransaction>,
) {
  const expires = Math.min(
    Date.parse(transaction.expiresAt),
    Date.now() + 300_000,
  );
  if (!Number.isFinite(expires) || expires <= Date.now())
    throw new RedirectStepUpError('popupExpired');
  if (
    transaction.purpose !== 'stepup' ||
    transaction.status !== 'pending' ||
    transaction.step.type !== 'redirect'
  )
    throw new RedirectStepUpError('popupFailed');
  const target = new URL(transaction.step.url, globalThis.location.href);
  const returned = new URL(returnTo, globalThis.location.href);
  if (
    !['https:', 'http:'].includes(target.protocol) ||
    target.username ||
    target.password ||
    returned.origin !== globalThis.location.origin
  )
    throw new RedirectStepUpError('popupFailed');
  // Called directly by the explicit button click, before any await.
  const opened = globalThis.open(
    'about:blank',
    '_blank',
    'popup,width=520,height=720',
  );
  if (!opened) throw new RedirectStepUpError('popupBlocked');
  const child: Window = opened;
  try {
    child.opener = null;
    if (transaction.step.method === 'POST') {
      const form = child.document.createElement('form');
      form.method = 'POST';
      form.action = target.href;
      const submit = form.submit.bind(form);
      for (const [name, value] of Object.entries(
        transaction.step.params ?? {},
      )) {
        const input = child.document.createElement('input');
        input.type = 'hidden';
        input.name = name;
        input.setAttribute('value', value);
        form.appendChild(input);
      }
      child.document.body.appendChild(form);
      submit();
    } else child.location.replace(target.href);
  } catch {
    child.close();
    throw new RedirectStepUpError('popupFailed');
  }

  let stopped = false;
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined;
  let finish!: (value: LoginTransaction | null, error?: unknown) => void;
  const done = new Promise<LoginTransaction | null>((resolve, reject) => {
    finish = (value, error) => {
      if (stopped) return;
      stopped = true;
      globalThis.clearTimeout(timer);
      globalThis.clearTimeout(deadline);
      child.close();
      if (error) reject(error);
      else resolve(value);
    };
  });
  function hasReturned() {
    // Cross-origin IdP documents are deliberately never inspected.
    try {
      return child.location.href === returned.href;
    } catch {
      return false;
    }
  }
  async function poll() {
    if (stopped) return;
    if (child.closed) {
      finish(null, new RedirectStepUpError('popupClosed'));
      return;
    }
    try {
      const value = await read(transaction.id);
      if (stopped) return;
      if (child.closed) {
        finish(null, new RedirectStepUpError('popupClosed'));
        return;
      }
      if (
        value.id !== transaction.id ||
        value.purpose !== 'stepup' ||
        value.provider.id !== transaction.provider.id ||
        !['pending', 'completed'].includes(value.status) ||
        (value.status === 'completed' && value.step.type !== 'complete')
      )
        throw new RedirectStepUpError('popupFailed');
      if (value.status === 'completed' || value.step.type !== 'redirect') {
        finish(value);
        return;
      }
    } catch (error) {
      if (stopped) return;
      if (child.closed) {
        finish(null, new RedirectStepUpError('popupClosed'));
        return;
      }
      if (
        isApiError(error) &&
        error.status === 404 &&
        ['common.not_found', 'iam.not_found'].includes(error.problem.code)
      ) {
        // IAM's successful callback clears its HttpOnly transaction cookie.
        // Only its exact private return URL can complete this branch. The
        // caller must recheck the same session; the retried write still has
        // its original server-side recent-step-up requirement and retry bound.
        if (hasReturned()) {
          finish(null);
          return;
        }
      } else {
        finish(null, error);
        return;
      }
    }
    if (!stopped)
      timer = globalThis.setTimeout(() => {
        void poll();
      }, 1000);
  }
  const deadline = globalThis.setTimeout(
    () => finish(null, new RedirectStepUpError('popupExpired')),
    expires - Date.now(),
  );
  void poll();
  return {
    done,
    close: () => finish(null, new RedirectStepUpError('popupClosed')),
  };
}
