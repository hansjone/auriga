import type { WorkspacePolicy, WorkspaceValue } from './settingsApi';

export const workspaceFields = [
  'idleMinutes',
  'wakeOnSchedule',
  'alwaysOnRequiresApproval',
  'maxConcurrentStarts',
  'defaultDshVersion',
  'allowPersonalPlugins',
] as const satisfies readonly (keyof WorkspacePolicy)[];
export type WorkspaceField = (typeof workspaceFields)[number];
export type WorkspaceDraft = Record<
  WorkspaceField,
  { enabled: boolean; value: string }
>;
export const numericField = (key: WorkspaceField) =>
  key === 'idleMinutes' || key === 'maxConcurrentStarts';
export const booleanField = (key: WorkspaceField) =>
  !numericField(key) && key !== 'defaultDshVersion';
export const objectValue = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);

export function workspaceDraft(value: unknown): WorkspaceDraft {
  const raw = objectValue(value) ? value : {};
  return Object.fromEntries(
    workspaceFields.map((key) => [
      key,
      {
        enabled: Object.hasOwn(raw, key),
        value: Object.hasOwn(raw, key) ? String(raw[key]) : '',
      },
    ]),
  ) as WorkspaceDraft;
}
function unsafeNumber(value: unknown): boolean {
  if (typeof value === 'number') return !Number.isSafeInteger(value);
  if (Array.isArray(value)) return value.some(unsafeNumber);
  return objectValue(value) && Object.values(value).some(unsafeNumber);
}
export function unsafeWorkspace(value: unknown) {
  if (!objectValue(value) || unsafeNumber(value)) return true;
  return workspaceFields.some((key) => {
    if (!Object.hasOwn(value, key)) return false;
    const field = value[key];
    if (numericField(key))
      return (
        typeof field !== 'number' ||
        !Number.isSafeInteger(field) ||
        field < (key === 'idleMinutes' ? 5 : 1)
      );
    return typeof field !== (booleanField(key) ? 'boolean' : 'string');
  });
}
export function validWorkspaceDraft(draft: WorkspaceDraft) {
  return workspaceFields.every((key) => {
    const field = draft[key];
    if (!field.enabled) return true;
    if (numericField(key))
      return (
        /^\d+$/.test(field.value) &&
        Number.isSafeInteger(Number(field.value)) &&
        Number(field.value) >= (key === 'idleMinutes' ? 5 : 1)
      );
    return !booleanField(key) || ['true', 'false'].includes(field.value);
  });
}
export function workspaceValue(
  original: WorkspaceValue,
  draft: WorkspaceDraft,
): WorkspaceValue {
  // Preserve every unknown property, including nested future extensions.
  const value: Record<string, unknown> = { ...original };
  for (const key of workspaceFields) {
    const field = draft[key];
    if (!field.enabled) delete value[key];
    else
      value[key] = numericField(key)
        ? Number(field.value)
        : booleanField(key)
          ? field.value === 'true'
          : field.value;
  }
  return value as WorkspaceValue;
}
export function workspaceChanges(
  original: WorkspaceValue,
  next: WorkspaceValue,
) {
  return workspaceFields.filter(
    (key) =>
      Object.hasOwn(original, key) !== Object.hasOwn(next, key) ||
      original[key] !== next[key],
  );
}
