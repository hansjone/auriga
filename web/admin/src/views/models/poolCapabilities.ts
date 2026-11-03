import type { RelayPool } from '../../admin/relayApi';
import type { components } from '../../../../shared/src/generated/ai';

export type CatalogModel = components['schemas']['RelayProviderModel'];
export type CapabilityPolicy = NonNullable<RelayPool['capabilities']>;
export type CatalogState = {
  items: CatalogModel[];
  loading: boolean;
  error: string;
};
const known = (value: number | undefined, minimum = 1) =>
  Number.isSafeInteger(value) && value! >= minimum ? value : undefined;
export const sourceKey = (providerId: string, model: string) =>
  JSON.stringify([providerId, model]);
export function createCapabilityPolicy(
  pool: RelayPool,
  mode: 'inherit' | 'manual' = 'inherit',
): CapabilityPolicy {
  const route = pool.routes.find(
    (r) => r.weight > 0 && r.providerId && r.model,
  );
  return {
    sourceProviderId: route?.providerId ?? '',
    sourceModel: route?.model ?? '',
    contextWindowMode: mode,
    maxOutputTokensMode: mode,
  };
}
export function reconcileCapabilitySource(pool: RelayPool) {
  const policy = pool.capabilities!;
  if (
    !pool.routes.some(
      (r) =>
        r.providerId === policy.sourceProviderId &&
        r.model === policy.sourceModel,
    )
  ) {
    const route = pool.routes.find(
      (r) => r.weight > 0 && r.providerId && r.model,
    );
    policy.sourceProviderId = route?.providerId ?? '';
    policy.sourceModel = route?.model ?? '';
    delete policy.upstream;
  }
  if (
    policy.upstream &&
    (policy.upstream.providerId !== policy.sourceProviderId ||
      policy.upstream.model.id !== policy.sourceModel)
  )
    delete policy.upstream;
  applyCapabilityPolicy(pool);
}
export function captureUpstreamCapabilities(
  pool: RelayPool,
  model: CatalogModel | undefined,
) {
  const policy = pool.capabilities!;
  const snapshot: CatalogModel = { id: policy.sourceModel };
  if (model?.id === policy.sourceModel) {
    if (known(model.contextWindow, 2) !== undefined)
      snapshot.contextWindow = model.contextWindow;
    if (known(model.maxInputTokens) !== undefined)
      snapshot.maxInputTokens = model.maxInputTokens;
    if (known(model.maxOutputTokens) !== undefined)
      snapshot.maxOutputTokens = model.maxOutputTokens;
  }
  policy.upstream = {
    providerId: policy.sourceProviderId,
    model: snapshot,
  };
  applyCapabilityPolicy(pool);
}
export function capabilityOrigin(
  pool: RelayPool,
  dimension: 'context' | 'output',
) {
  const policy = pool.capabilities!;
  const context = dimension === 'context';
  if (
    (context ? policy.contextWindowMode : policy.maxOutputTokensMode) ===
    'manual'
  )
    return 'manual';
  const snapshot =
    policy.upstream?.providerId === policy.sourceProviderId &&
    policy.upstream.model.id === policy.sourceModel
      ? policy.upstream.model
      : undefined;
  if (
    known(
      context ? snapshot?.contextWindow : snapshot?.maxOutputTokens,
      context ? 2 : 1,
    ) !== undefined
  )
    return 'upstream';
  return known(
    context
      ? policy.recommendedContextWindow
      : policy.recommendedMaxOutputTokens,
    context ? 2 : 1,
  ) !== undefined
    ? 'recommended'
    : 'unknown';
}
export function applyCapabilityPolicy(pool: RelayPool) {
  const policy = pool.capabilities;
  if (!policy) return;
  for (const field of [
    'recommendedContextWindow',
    'recommendedMaxOutputTokens',
  ] as const)
    if ((policy[field] as unknown) === '') delete policy[field];
  const snapshot =
    policy.upstream?.providerId === policy.sourceProviderId &&
    policy.upstream.model.id === policy.sourceModel
      ? policy.upstream.model
      : undefined;
  if (policy.contextWindowMode === 'inherit')
    pool.contextWindow =
      known(snapshot?.contextWindow, 2) ??
      known(policy.recommendedContextWindow, 2) ??
      0;
  if (policy.maxOutputTokensMode === 'inherit')
    pool.maxOutputTokens =
      known(snapshot?.maxOutputTokens) ??
      known(policy.recommendedMaxOutputTokens) ??
      0;
}
export function validCapabilityPolicy(pool: RelayPool) {
  const policy = pool.capabilities;
  if (!policy) return true;
  return (
    !!policy.sourceProviderId &&
    !!policy.sourceModel &&
    [policy.contextWindowMode, policy.maxOutputTokensMode].every((mode) =>
      ['inherit', 'manual'].includes(mode),
    ) &&
    [
      policy.recommendedContextWindow ?? 0,
      policy.recommendedMaxOutputTokens ?? 0,
    ].every((value) => Number.isSafeInteger(value) && value >= 0) &&
    policy.recommendedContextWindow !== 1
  );
}
