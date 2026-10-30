import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { validateAccess } from './checks.mjs';

export function launchBrowser(access, { loopback = false } = {}) {
  validateAccess(access);
  const args = [];
  if (loopback) {
    assert.equal(access.mode, 'domain');
    args.push(
      '--no-proxy-server',
      `--host-resolver-rules=MAP ${access.portalOrigin.host} 127.0.0.1,MAP ${access.adminOrigin.host} 127.0.0.1,MAP *.${access.instances.parentDomain} 127.0.0.1`,
    );
  }
  return chromium.launch({ channel: 'chromium', headless: true, args });
}
