import type { Quota } from './quotaApi';

export const quotaFields = [
  {
    id: 'cpuMillis',
    section: 'instance',
    minimum: 100,
    nullable: false,
    unit: 'mCPU',
  },
  {
    id: 'memoryMb',
    section: 'instance',
    minimum: 256,
    nullable: false,
    unit: 'MB',
  },
  {
    id: 'diskGb',
    section: 'instance',
    minimum: 1,
    nullable: false,
    unit: 'GB',
  },
  {
    id: 'maxInstances',
    section: 'instance',
    minimum: 0,
    nullable: true,
    unit: 'count',
  },
] as const;
export type QuotaField = (typeof quotaFields)[number];
export type NumericId = QuotaField['id'];
export type QuotaDraft = Record<
  NumericId,
  { mode: 'unset' | 'value' | 'unlimited'; value: string }
> & {
  alwaysOnAllowed: 'unset' | 'allow' | 'deny';
};
export function fromQuota(quota: Quota): QuotaDraft {
  const draft = {
    alwaysOnAllowed:
      quota.instance?.alwaysOnAllowed === undefined
        ? 'unset'
        : quota.instance.alwaysOnAllowed
          ? 'allow'
          : 'deny',
  } as QuotaDraft;
  for (const field of quotaFields) {
    const value = quota.instance?.[field.id];
    draft[field.id] = {
      mode:
        value === undefined ? 'unset' : value === null ? 'unlimited' : 'value',
      value: typeof value === 'number' ? String(value) : '',
    };
  }
  return draft;
}
export function validateDraft(draft: QuotaDraft): string | undefined {
  for (const field of quotaFields) {
    const entry = draft[field.id];
    if (entry.mode === 'unset') continue;
    if (entry.mode === 'unlimited') {
      if (!field.nullable) return field.id;
      continue;
    }
    if (!/^\d+$/.test(entry.value.trim())) return field.id;
    const value = Number(entry.value);
    if (!Number.isSafeInteger(value) || value < field.minimum) return field.id;
  }
  return undefined;
}
export function toQuota(draft: QuotaDraft): Quota {
  if (validateDraft(draft)) throw new Error('Invalid quota draft');
  const instance: NonNullable<Quota['instance']> = {};
  for (const key of ['cpuMillis', 'memoryMb', 'diskGb'] as const) {
    if (draft[key].mode === 'value') instance[key] = Number(draft[key].value);
  }
  if (draft.maxInstances.mode !== 'unset')
    instance.maxInstances =
      draft.maxInstances.mode === 'unlimited'
        ? null
        : Number(draft.maxInstances.value);
  if (draft.alwaysOnAllowed !== 'unset')
    instance.alwaysOnAllowed = draft.alwaysOnAllowed === 'allow';
  return {
    ...(Object.keys(instance).length ? { instance } : {}),
  };
}
export function clearedFields(previous: Quota, next: Quota): string[] {
  const cleared: string[] = [];
  for (const section of ['instance'] as const) {
    for (const key of Object.keys(previous[section] || {})) {
      if (!Object.hasOwn(next[section] || {}, key)) cleared.push(key);
    }
  }
  return cleared;
}

/** Replace editable fields while retaining unrecognized server fields. */
export function preserveUnknownQuota(previous: Quota, edited: Quota): Quota {
  const result: Quota = { ...previous };
  for (const section of ['instance'] as const) {
    const combined = { ...previous[section], ...edited[section] };
    const known = [
      'cpuMillis',
      'memoryMb',
      'diskGb',
      'maxInstances',
      'alwaysOnAllowed',
    ];
    for (const key of known) {
      if (!Object.hasOwn(edited[section] || {}, key))
        Reflect.deleteProperty(combined, key);
    }
    if (Object.keys(combined).length) result[section] = combined;
    else delete result[section];
  }
  return result;
}

export function unsafeQuotaFields(value: unknown, prefix = ''): string[] {
  if (typeof value === 'number')
    return Number.isSafeInteger(value) ? [] : [prefix];
  if (!value || typeof value !== 'object') return [];
  return Object.entries(value).flatMap(([key, child]) =>
    unsafeQuotaFields(child, prefix ? `${prefix}.${key}` : key),
  );
}
