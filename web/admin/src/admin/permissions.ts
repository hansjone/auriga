// UI availability only; the service independently authorizes every request.
export function hasPermission(
  permissions: readonly string[],
  required: string,
) {
  return permissions.some(
    (permission) =>
      permission === required ||
      permission === '*' ||
      (permission.endsWith('.*') &&
        !permission.slice(0, -2).includes('*') &&
        required.startsWith(permission.slice(0, -1))),
  );
}
