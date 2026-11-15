/* global AbortSignal */
import { normalizeOrigin } from '../settings/store.js';

/** One bounded migration pass. Failed revocations remain encrypted for retry.
 * @param {any} saved @param {any} config */
export async function revokeLegacyCredentials(saved, config) {
  const credential = saved.credential;
  const entries = [
    ...(saved.legacyRevocations ?? []),
    ...(saved.revocations ?? []),
    ...(credential ? [credential] : []),
  ];
  const pending = entries.slice(8);
  await Promise.all(
    entries.slice(0, 8).map(async (entry) => {
      const token = entry.refreshToken ?? entry.accessToken;
      if (typeof token !== 'string' || !token || token.length > 16384) return;
      try {
        const origin = normalizeOrigin(
          entry.platformOrigin ??
            credential?.platformOrigin ??
            config.platformOrigin,
        );
        const issuer = new URL(
          entry.issuer ?? credential?.issuer ?? config.issuer ?? origin,
        );
        if (
          issuer.origin !== origin ||
          issuer.search ||
          issuer.hash ||
          issuer.username ||
          issuer.password
        )
          throw new Error();
        const clientId =
          entry.clientId ?? credential?.clientId ?? config.clientId;
        if (typeof clientId !== 'string' || !/^[\w.-]{1,128}$/.test(clientId))
          throw new Error();
        const response = await fetch(
          new URL(
            issuer.pathname.replace(/\/$/, '') + '/oauth2/revoke',
            origin,
          ),
          {
            method: 'POST',
            redirect: 'error',
            credentials: 'omit',
            signal: AbortSignal.timeout(3000),
            headers: { 'content-type': 'application/x-www-form-urlencoded' },
            body: new URLSearchParams({
              client_id: clientId,
              token,
            }).toString(),
          },
        );
        await response.body?.cancel();
        if (!response.ok) throw new Error();
      } catch {
        pending.push({
          ...entry,
          platformOrigin:
            entry.platformOrigin ??
            credential?.platformOrigin ??
            config.platformOrigin,
          issuer: entry.issuer ?? credential?.issuer ?? config.issuer,
          clientId: entry.clientId ?? credential?.clientId ?? config.clientId,
        });
      }
    }),
  );
  return pending;
}
