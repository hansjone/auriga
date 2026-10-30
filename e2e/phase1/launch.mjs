import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { validateAccess } from './checks.mjs';

// Resolve from the single E2E manifest, including in resumed worktrees with an
// older ignored phase1/node_modules directory that has not been cleaned.
const { chromium } = createRequire(new URL('../package.json', import.meta.url))(
  'playwright',
);

export function launchBrowser(access, { loopback = false, ...options } = {}) {
  if (access) validateAccess(access);
  const args = [...(options.args ?? [])];
  if (loopback) {
    assert.equal(access.mode, 'domain');
    args.push(
      '--no-proxy-server',
      `--host-resolver-rules=MAP ${access.portalOrigin.host} 127.0.0.1,MAP ${access.adminOrigin.host} 127.0.0.1,MAP *.${access.instances.parentDomain} 127.0.0.1`,
    );
  }
  return chromium.launch({
    channel: 'chromium',
    headless: true,
    ...options,
    args,
  });
}
