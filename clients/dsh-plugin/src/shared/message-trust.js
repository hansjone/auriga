// @ts-nocheck
import { createFileLedgerStore } from '../network/host/store.js';
import { openV2TrustStore } from '../network/trust/v2-store.js';
import { publicKey, positiveVersion } from '../network/trust/keyset.js';
import { verifyEnvelopeWithRefresh } from '../network/trust/verifier.js';
import { verifyFetchPage } from '../network/session/page.js';
import {
  FETCH_MAX_BYTES,
  FETCH_MIN_BYTES,
} from '../network/session/constants.js';
import { canonical, fail } from './protocol.js';

/** Lazy public trust state; no process credential, URL or alternate trust root. */
export function createSharedMessageTrust({
  settings,
  tenantId,
  physicalInstanceId,
  now = Date.now,
  store = createFileLedgerStore({
    dir: `/var/lib/auriga/pools/${physicalInstanceId}/host/message-trust`,
  }),
}) {
  let closed = false;
  let pinned;
  let handle;
  let pending;
  let current;
  let queue = Promise.resolve();
  const configuration = () => {
    if (closed) fail('common.unavailable');
    const config = settings().messageTrust;
    if (
      !config ||
      Object.keys(config).sort().join(',') !== 'realm,roots' ||
      !positiveVersion(config.realm) ||
      !Array.isArray(config.roots) ||
      config.roots.length < 1 ||
      config.roots.length > 16
    )
      fail('common.unavailable');
    const kids = new Set();
    for (const root of config.roots) {
      if (
        !root ||
        Object.keys(root).sort().join(',') !== 'kid,public_key_spki' ||
        !/^[A-Za-z0-9._-]{1,128}$/.test(root.kid) ||
        kids.has(root.kid)
      )
        fail('common.unavailable');
      publicKey(root.public_key_spki);
      kids.add(root.kid);
    }
    const text = canonical(config);
    if (pinned && text !== pinned) fail('common.unavailable');
    pinned ??= text;
    return JSON.parse(text);
  };
  const open = async () => {
    const config = configuration();
    pending ??= (async () => {
      handle = await store.open();
      current = await openV2TrustStore({
        cache: handle,
        realm: config.realm,
        roots: config.roots,
        tenant_id: tenantId,
        requireAnnouncement: true,
        now,
      });
      if (closed) fail('common.unavailable');
      return current;
    })();
    return pending;
  };
  return {
    open,
    assertUsable() {
      configuration();
      if (!current) fail('common.unavailable');
      current.assertUsable();
    },
    verify(
      value,
      connection,
      requestId,
      material,
      check = () => {},
      maxBytes = FETCH_MAX_BYTES,
    ) {
      const work = queue.then(async () => {
        const trust = await open();
        check();
        if (connection.realm !== configuration().realm)
          fail('shared.binding_mismatch');
        if (material !== undefined) {
          if (
            !material ||
            Object.keys(material).sort().join(',') !==
              'announcement,control,publication'
          )
            fail('common.validation_failed');
          await trust.installControl(material.control);
          check();
          await trust.update(material.publication);
          check();
          await trust.announce(material.announcement);
          check();
        }
        trust.assertUsable();
        const context = {
          realm: connection.realm,
          node: connection.node,
          nodeIncarnation: connection.incarnation,
          epoch: connection.epoch,
          channelNonce: connection.channelNonce,
          requestId,
        };
        if (value?.type === 'fetch_result') {
          if (
            !Number.isSafeInteger(maxBytes) ||
            maxBytes < FETCH_MIN_BYTES ||
            maxBytes > FETCH_MAX_BYTES
          )
            fail('common.validation_failed');
          const page = await verifyFetchPage({
            trust,
            context,
            now,
            header: value.page,
            items: value.items,
            maxBytes,
          });
          if (page.rejections.length) fail('shared.signature_invalid');
        } else await verifyEnvelopeWithRefresh(value, { trust, context, now });
        check();
        trust.assertUsable();
        return true;
      });
      queue = work.catch(() => {});
      return work;
    },
    async close() {
      if (closed) return;
      closed = true;
      await queue;
      await pending?.catch(() => {});
      await handle?.close();
    },
  };
}
