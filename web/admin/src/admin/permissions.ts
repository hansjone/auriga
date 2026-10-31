// UI availability only; the service independently authorizes every request.
export function hasPermission(
  permissions: readonly string[],
  required: string,
) {
  return permissions.includes(required);
}
