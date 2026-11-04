import type { RelayPool } from '../../admin/relayApi';
import type { components } from '../../../../shared/src/generated/ai';

export type CatalogModel = components['schemas']['RelayProviderModel'];
export type ImportRow = {
  model: CatalogModel;
  id: string;
  context?: number;
  output?: number;
  contextMode?: 'inherit' | 'manual';
  outputMode?: 'inherit' | 'manual';
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
  return {
    context: context !== undefined && context >= 2 ? context : undefined,
    output,
    inputOnly: (context === undefined || context < 2) && input !== undefined,
    limited: false,
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
  const context = row.context ?? 0,
    output = row.output ?? 0;
  if (
    !Number.isSafeInteger(context) ||
    !Number.isSafeInteger(output) ||
    context < 0 ||
    context === 1 ||
    output < 0 ||
    context > Number.MAX_SAFE_INTEGER ||
    output > Number.MAX_SAFE_INTEGER ||
    (context > 0 && output >= context)
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
    sameCapabilities(a.capabilities, b.capabilities) &&
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
function sameCapabilities(
  a: RelayPool['capabilities'],
  b: RelayPool['capabilities'],
) {
  if (!a || !b) return a === b;
  return (
    a.sourceProviderId === b.sourceProviderId &&
    a.sourceModel === b.sourceModel &&
    a.contextWindowMode === b.contextWindowMode &&
    a.maxOutputTokensMode === b.maxOutputTokensMode &&
    (a.recommendedContextWindow ?? 0) === (b.recommendedContextWindow ?? 0) &&
    (a.recommendedMaxOutputTokens ?? 0) ===
      (b.recommendedMaxOutputTokens ?? 0) &&
    a.upstream?.providerId === b.upstream?.providerId &&
    a.upstream?.model.id === b.upstream?.model.id &&
    a.upstream?.model.contextWindow === b.upstream?.model.contextWindow &&
    a.upstream?.model.maxInputTokens === b.upstream?.model.maxInputTokens &&
    a.upstream?.model.maxOutputTokens === b.upstream?.model.maxOutputTokens
  );
}
