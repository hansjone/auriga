import assert from 'node:assert/strict';
import { nextTotp } from './run.mjs';

// The real page starts/resumes its own attempt when mounted. Only submit its
// rendered forms; issuing a second API begin races and can cancel that attempt.
export async function loginInstalledDesktop(page, member, request) {
  async function state(allowPassword = false) {
    const result = await page.waitForFunction((allowPassword) => {
      if (globalThis.location.pathname === '/') return 'complete';
      const root = document.querySelector('#auriga-login');
      if (root?.querySelector('[role="alert"]')) return 'failed';
      if (root?.querySelector('[aria-busy="true"]')) return false;
      if (allowPassword && root?.querySelector('input[name="username"]'))
        return 'password';
      if (root?.querySelector('input[autocomplete="one-time-code"]'))
        return 'otp';
      return false;
    }, allowPassword);
    const value = await result.jsonValue();
    await result.dispose();
    assert.notEqual(value, 'failed', 'installed desktop login page failed');
    return value;
  }
  let current = await state(true);
  const resumed = current === 'complete';
  if (current === 'password') {
    await page
      .locator('#auriga-login input[name="username"]')
      .fill(member.username);
    await page
      .locator('#auriga-login input[name="password"]')
      .fill(member.password);
    const otp = page.locator(
      '#auriga-login input[autocomplete="one-time-code"]',
    );
    if (await otp.count()) await otp.fill(await nextTotp(member));
    await page.locator('#auriga-login button[type="submit"]').click();
    current = await state();
  }
  if (current === 'otp') {
    await page
      .locator('#auriga-login input[autocomplete="one-time-code"]')
      .fill(await nextTotp(member));
    await page.locator('#auriga-login button[type="submit"]').click();
    current = await state();
  }
  assert.equal(current, 'complete', 'installed desktop login must complete');
  const status = await request('/auriga/local-login/status');
  assert.equal(status.loggedIn, true);
  assert.equal(status.available, true);
  assert.equal(
    status.account?.subject,
    member.accountId,
    'same authenticated test member',
  );
  return {
    loggedIn: true,
    available: true,
    accountId: status.account.subject,
    resumed,
  };
}
