/** Names are presentation only. Every read uses the already verified identity;
 * a name never supplies an account, tenant, permission or login state.
 * @param {unknown} value */
export function memberName(value) {
  return typeof value === 'string' &&
    value.trim().length > 0 &&
    value.length <= 256 &&
    !Array.from(value).some(
      (character) =>
        character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127,
    )
    ? value.trim()
    : undefined;
}

/** Read the authenticated portal session before its login jar is discarded.
 * @param {any} platform @param {any} jar @param {string} subject
 * @param {AbortSignal} signal */
export async function loginMemberName(platform, jar, subject, signal) {
  const me = await platform.request('/api/iam/v1/me', { jar, signal });
  if (me?.account?.id !== subject) throw new Error('profile identity changed');
  return (
    memberName(me.account.displayName) ??
    me.identities
      ?.map((/** @type {any} */ row) => memberName(row.displayName))
      .find(Boolean)
  );
}

/** Existing device directory projection supplies the owner's current name.
 * It is tenant scoped and rechecks device/backing-session authority server-side.
 * @param {any} platform @param {string} token @param {string} subject
 * @param {AbortSignal} signal */
export async function deviceMemberName(platform, token, subject, signal) {
  let cursor = '';
  const seen = new Set();
  for (let page = 0; page < 10; page++) {
    const response = await platform.request(
      '/api/iam/v1/network/systems?limit=200' +
        (cursor ? '&cursor=' + encodeURIComponent(cursor) : ''),
      { token, signal },
    );
    if (!Array.isArray(response?.items)) throw new Error('invalid directory');
    for (const row of response.items) {
      const name = memberName(row.ownerDisplayName);
      if (row.ownerUserId === subject && name) return name;
    }
    cursor = response.nextCursor;
    if (!cursor) return undefined;
    if (typeof cursor !== 'string' || cursor.length > 1024 || seen.has(cursor))
      throw new Error('invalid directory cursor');
    seen.add(cursor);
  }
  return undefined;
}
