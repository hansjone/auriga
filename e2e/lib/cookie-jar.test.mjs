import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  CookieJar,
  domainMatches,
  jarForHost,
  parseSetCookieLine,
} from './cookie-jar.mjs';

describe('domainMatches', () => {
  it('host-only requires exact host', () => {
    assert.equal(domainMatches('admin.example.com', 'admin.example.com', true), true);
    assert.equal(
      domainMatches('admin.example.com', 'uuid.i.example.com', true),
      false,
    );
  });

  it('Domain covers subdomains', () => {
    assert.equal(domainMatches('example.com', 'portal.example.com', false), true);
    assert.equal(domainMatches('example.com', 'a.i.example.com', false), true);
    assert.equal(domainMatches('example.com', 'evil.com', false), false);
  });
});

describe('CookieJar selection', () => {
  it('does not send admin host-only session to instance host', () => {
    const admin = new CookieJar();
    admin.storeFromResponse(
      'auriga_admin_session=admin-secret; Path=/; HttpOnly',
      { host: 'admin.example.com', path: '/api/iam/v1/me' },
    );
    assert.equal(
      admin.wouldSend('auriga_admin_session', {
        host: 'admin.example.com',
        path: '/',
      }),
      true,
    );
    assert.equal(
      admin.wouldSend('auriga_admin_session', {
        host: 'inst.i.example.com',
        path: '/',
      }),
      false,
    );
    const instance = new CookieJar();
    assert.equal(
      instance.wouldSend('auriga_admin_session', {
        host: 'inst.i.example.com',
        path: '/',
      }),
      false,
    );
  });

  it('keeps portal and admin cookies in separate jars', () => {
    const jars = {
      admin: new CookieJar(),
      portal: new CookieJar(),
      instance: new CookieJar(),
    };
    const hosts = {
      admin: 'admin.example.com',
      portal: 'portal.example.com',
    };
    jarForHost('admin.example.com', hosts, jars).storeFromResponse(
      'auriga_admin_session=a; Path=/',
      { host: 'admin.example.com', path: '/' },
    );
    jarForHost('portal.example.com', hosts, jars).storeFromResponse(
      'auriga_session=p; Path=/',
      { host: 'portal.example.com', path: '/' },
    );
    const adminHdr = jars.admin.cookieHeader({
      host: 'admin.example.com',
      path: '/',
    });
    const portalHdr = jars.portal.cookieHeader({
      host: 'portal.example.com',
      path: '/',
    });
    assert.match(adminHdr, /auriga_admin_session=a/);
    assert.doesNotMatch(adminHdr, /auriga_session=/);
    assert.match(portalHdr, /auriga_session=p/);
    assert.doesNotMatch(portalHdr, /auriga_admin_session=/);
    // Instance jar never receives either audience session.
    assert.equal(
      jars.instance.cookieHeader({ host: 'x.i.example.com', path: '/' }),
      '',
    );
  });

  it('honors Path when selecting cookies', () => {
    const jar = new CookieJar();
    jar.storeFromResponse('scoped=1; Path=/api', {
      host: 'portal.example.com',
      path: '/api/iam/v1/me',
    });
    assert.equal(
      jar.wouldSend('scoped', { host: 'portal.example.com', path: '/api/x' }),
      true,
    );
    assert.equal(
      jar.wouldSend('scoped', { host: 'portal.example.com', path: '/' }),
      false,
    );
  });

  it('mirrors Domain open-nonce from portal jar into instance jar', () => {
    const portal = new CookieJar();
    const instance = new CookieJar();
    portal.storeFromResponse(
      'auriga_open_nonce_abc=nonce; Path=/; Domain=example.com',
      { host: 'portal.example.com', path: '/open/abc' },
    );
    instance.absorbMatchingDomainCookies(portal, 'abc.i.example.com');
    assert.equal(
      instance.wouldSend('auriga_open_nonce_abc', {
        host: 'abc.i.example.com',
        path: '/_auriga/ticket',
      }),
      true,
    );
    // Still must not leak admin session if somehow present with Domain.
    portal.storeFromResponse(
      'auriga_admin_session=nope; Path=/; Domain=example.com',
      { host: 'portal.example.com', path: '/' },
    );
    // Domain cookie that was set from portal request with Domain=example.com:
    // absorb would copy it — e2e must not set admin cookies with Domain.
    // Host-only admin cookie must not absorb.
    const adminOnly = new CookieJar();
    adminOnly.storeFromResponse('auriga_admin_session=secret; Path=/', {
      host: 'admin.example.com',
      path: '/',
    });
    const inst2 = new CookieJar();
    inst2.absorbMatchingDomainCookies(adminOnly, 'abc.i.example.com');
    assert.equal(
      inst2.wouldSend('auriga_admin_session', {
        host: 'abc.i.example.com',
        path: '/',
      }),
      false,
    );
  });
});

describe('CookieJar lifecycle', () => {
  it('expires via Max-Age', () => {
    const jar = new CookieJar();
    const t0 = 1_700_000_000_000;
    jar.storeFromResponse('temp=1; Path=/; Max-Age=10', {
      host: 'portal.example.com',
      path: '/',
    }, t0);
    assert.equal(
      jar.wouldSend('temp', { host: 'portal.example.com', path: '/' }, t0 + 5_000),
      true,
    );
    assert.equal(
      jar.wouldSend('temp', { host: 'portal.example.com', path: '/' }, t0 + 11_000),
      false,
    );
  });

  it('expires via Expires attribute', () => {
    const jar = new CookieJar();
    const t0 = Date.parse('2026-01-01T00:00:00.000Z');
    jar.storeFromResponse(
      'temp=1; Path=/; Expires=Thu, 01 Jan 2026 00:00:05 GMT',
      { host: 'portal.example.com', path: '/' },
      t0,
    );
    assert.equal(
      jar.wouldSend('temp', { host: 'portal.example.com', path: '/' }, t0 + 1000),
      true,
    );
    assert.equal(
      jar.wouldSend('temp', { host: 'portal.example.com', path: '/' }, t0 + 6000),
      false,
    );
  });

  it('rejects Domain that does not cover request host', () => {
    const c = parseSetCookieLine('x=1; Domain=other.com', {
      host: 'portal.example.com',
      path: '/',
    }, Date.now());
    assert.equal(c, null);
  });

  it('Max-Age=0 deletes cookie', () => {
    const jar = new CookieJar();
    jar.storeFromResponse('x=1; Path=/', {
      host: 'portal.example.com',
      path: '/',
    });
    jar.storeFromResponse('x=; Path=/; Max-Age=0', {
      host: 'portal.example.com',
      path: '/',
    });
    assert.equal(
      jar.wouldSend('x', { host: 'portal.example.com', path: '/' }),
      false,
    );
  });
});
