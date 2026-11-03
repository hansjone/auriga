import type { RelayPool } from '../../admin/relayApi';
import type { components } from '../../../../shared/src/generated/ai';

export type CatalogModel = components['schemas']['RelayProviderModel'];
export type ImportRow = {
  model: CatalogModel;
  id: string;
  context?: number;
  output?: number;
  key: string;
  status: 'pending' | 'saving' | 'saved' | 'failed' | 'conflict';
  message: string;
  body?: RelayPool;
  attempted?: boolean;
};
const positive = (value?: number) =>
  Number.isSafeInteger(value) && value! > 0 ? value : undefined;
export function capabilityLimits(model: CatalogModel) {
  const context = positive(model.contextWindow),
    input = positive(model.maxInputTokens),
    output = positive(model.maxOutputTokens);
  const contexts = [context, input].filter((v): v is number => v !== undefined);
  const effectiveContext = contexts.length
    ? Math.min(1048576, ...contexts)
    : undefined;
  return {
    context: effectiveContext,
    output: output === undefined ? undefined : Math.min(32768, output),
    inputOnly: context === undefined && input !== undefined,
    limited:
      contexts.some((v) => v > 1048576) ||
      (output !== undefined && output > 32768),
  };
}
export function applyKnownLimits(
  context: number,
  output: number,
  models: CatalogModel[],
) {
  const limits = models.map(capabilityLimits);
  if (
    !limits.length ||
    limits.some((v) => v.context === undefined || v.output === undefined)
  )
    return;
  return {
    context: Math.min(context, ...limits.map((v) => v.context!)),
    output: Math.min(output, ...limits.map((v) => v.output!)),
  };
}
export const suggestPublicId = (id: string) =>
  id
    .replace(/[^A-Za-z0-9._-]/g, '-')
    .replace(/^[^A-Za-z0-9]+/, '')
    .slice(0, 128);
export function rowError(
  row: ImportRow,
  rows: ImportRow[],
  existing: Set<string>,
) {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(row.id))
    return 'importInvalidId';
  if (rows.filter((v) => v.id === row.id).length > 1) return 'importDuplicate';
  if (existing.has(row.id) && row.status !== 'saved' && !row.body)
    return 'importExists';
  if (row.context === undefined || row.output === undefined)
    return 'importMissingLimits';
  if (
    !Number.isSafeInteger(row.context) ||
    !Number.isSafeInteger(row.output) ||
    row.context < 2 ||
    row.context > 1048576 ||
    row.output < 1 ||
    row.output > 32768 ||
    row.output >= row.context
  )
    return 'importInvalidLimits';
}
function instant(value: string) {
  const match =
    /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/.exec(
      value,
    );
  if (!match) return;
  const milliseconds = Date.parse(match[1] + match[3]);
  if (!Number.isFinite(milliseconds)) return;
  return (
    BigInt(milliseconds) * 1000000n + BigInt((match[2] ?? '').padEnd(9, '0'))
  );
}
export function samePool(a: RelayPool, b: RelayPool) {
  const effectiveAt = instant(a.price.effectiveAt);
  return (
    a.id === b.id &&
    a.enabled === b.enabled &&
    a.contextWindow === b.contextWindow &&
    a.maxOutputTokens === b.maxOutputTokens &&
    a.routes.length === b.routes.length &&
    a.routes.every(
      (r, i) =>
        r.providerId === b.routes[i].providerId &&
        r.model === b.routes[i].model &&
        r.weight === b.routes[i].weight,
    ) &&
    a.price.currency === b.price.currency &&
    a.price.inputMicrosPerMillionTokens ===
      b.price.inputMicrosPerMillionTokens &&
    a.price.outputMicrosPerMillionTokens ===
      b.price.outputMicrosPerMillionTokens &&
    a.price.version === b.price.version &&
    effectiveAt !== undefined &&
    effectiveAt === instant(b.price.effectiveAt)
  );
}
