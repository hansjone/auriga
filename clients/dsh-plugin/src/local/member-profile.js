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
