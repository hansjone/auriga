<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { RelayPool, RelayProvider } from '../../admin/relayApi';
import { useRelayI18n } from './relayMessages';
import {
  applyCapabilityPolicy,
  capabilityOrigin,
  captureUpstreamCapabilities,
  reconcileCapabilitySource,
  setCapabilityMode,
  sourceKey,
  upstreamCapabilityValue,
  type CatalogState,
} from './poolCapabilities';
const pool = defineModel<RelayPool>({ required: true });
const props = defineProps<{
  providers: RelayProvider[];
  catalogs: Record<string, CatalogState>;
}>();
const emit = defineEmits<{ refresh: [] }>();
const { t } = useRelayI18n();
const policy = computed(() => pool.value.capabilities!);
const adoptionNotice = ref('');
const contextMode = computed({
  get: () => policy.value.contextWindowMode,
  set: (mode) => changeMode('context', mode),
});
const outputMode = computed({
  get: () => policy.value.maxOutputTokensMode,
  set: (mode) => changeMode('output', mode),
});
function changeMode(
  dimension: 'context' | 'output',
  mode: 'inherit' | 'manual',
) {
  const preserved = setCapabilityMode(pool.value, dimension, mode);
  adoptionNotice.value = preserved ? t('capabilityManualPreserved') : '';
}
function adoptUpstream() {
  const contextPreserved = setCapabilityMode(pool.value, 'context', 'inherit');
  const outputPreserved = setCapabilityMode(pool.value, 'output', 'inherit');
  adoptionNotice.value = t(
    contextPreserved || outputPreserved
      ? 'capabilityManualPreserved'
      : 'capabilityPriorityAdopted',
  );
}
function upstreamText(dimension: 'context' | 'output') {
  return (
    upstreamCapabilityValue(pool.value, dimension)?.toLocaleString() ??
    t(policy.value.upstream ? 'capabilityNotProvided' : 'capabilityNotFetched')
  );
}
function effectiveText(dimension: 'context' | 'output') {
  const value =
    dimension === 'context'
      ? pool.value.contextWindow
      : pool.value.maxOutputTokens;
  return (
    t('capabilityEffectiveValue', {
      tokens: value > 0 ? value.toLocaleString() : t('unknown'),
    }) +
    ' · ' +
    t('capabilityOrigin_' + capabilityOrigin(pool.value, dimension))
  );
}
const selectedSource = computed({
  get: () => sourceKey(policy.value.sourceProviderId, policy.value.sourceModel),
  set(value: string) {
    const [providerId, model] = JSON.parse(value) as [string, string];
    policy.value.sourceProviderId = providerId;
    policy.value.sourceModel = model;
    delete policy.value.upstream;
    adoptionNotice.value = '';
    syncSource();
  },
});
function syncSource() {
  reconcileCapabilitySource(pool.value);
  const state = props.catalogs[policy.value.sourceProviderId];
  if (state && !state.loading && !state.error)
    captureUpstreamCapabilities(
      pool.value,
      state.items.find((model) => model.id === policy.value.sourceModel),
    );
}
watch(() => JSON.stringify(pool.value.routes), syncSource, { immediate: true });
watch(() => JSON.stringify(props.catalogs), syncSource);
watch(
  () => [
    policy.value.recommendedContextWindow,
    policy.value.recommendedMaxOutputTokens,
  ],
  () => applyCapabilityPolicy(pool.value),
);
</script>
<template>
  <div class="relay-wide relay-capability-editor">
    <label class="relay-wide"
      >{{ t('capabilitySourceRoute') }}
      <select v-model="selectedSource" name="capabilitySourceRoute">
        <option v-if="!policy.sourceModel" disabled :value="selectedSource">
          {{ t('capabilityChooseRoute') }}
        </option>
        <option
          v-for="(route, index) in pool.routes"
          :key="sourceKey(route.providerId, route.model)"
          :value="sourceKey(route.providerId, route.model)"
          :disabled="!route.providerId || !route.model"
        >
          {{ t('routeNumber', { number: index + 1 }) }} ·
          {{
            providers.find((p) => p.id === route.providerId)?.configuration.name
          }}
          / {{ route.model }}
        </option>
      </select>
    </label>
    <div class="relay-wide relay-toolbar relay-capability-actions">
      <button
        type="button"
        data-testid="apply-capabilities"
        :disabled="
          !policy.sourceProviderId ||
          !policy.sourceModel ||
          catalogs[policy.sourceProviderId]?.loading
        "
        @click="emit('refresh')"
      >
        {{ t('applyCapabilities') }}
      </button>
      <button
        type="button"
        class="relay-primary"
        data-testid="adopt-upstream-capabilities"
        :disabled="
          !policy.sourceProviderId ||
          !policy.sourceModel ||
          catalogs[policy.sourceProviderId]?.loading
        "
        @click="adoptUpstream"
      >
        {{ t('capabilityAdoptUpstream') }}
      </button>
      <p>{{ t('capabilityActionsNote') }}</p>
      <p
        v-if="adoptionNotice"
        role="status"
        data-testid="capability-adoption-notice"
      >
        {{ adoptionNotice }}
      </p>
    </div>
    <label class="relay-capability-field"
      >{{ t('context') }}
      <small data-testid="context-upstream">{{
        t('capabilityUpstreamValue', { tokens: upstreamText('context') })
      }}</small>
      <select v-model="contextMode" name="contextCapabilityMode">
        <option value="inherit">{{ t('capabilityInherit') }}</option>
        <option value="manual">{{ t('capabilityManual') }}</option>
      </select>
      <input
        v-model.number="pool.contextWindow"
        name="contextWindow"
        type="number"
        min="0"
        max="9007199254740991"
        :disabled="policy.contextWindowMode === 'inherit'"
        required
      />
      <small data-testid="context-effective">{{
        effectiveText('context')
      }}</small>
      <small
        v-if="contextMode === 'manual'"
        data-testid="context-manual-note"
        >{{ t('capabilityManualActive') }}</small
      >
    </label>
    <label class="relay-capability-field"
      >{{ t('output') }}
      <small data-testid="output-upstream">{{
        t('capabilityUpstreamValue', { tokens: upstreamText('output') })
      }}</small>
      <select v-model="outputMode" name="outputCapabilityMode">
        <option value="inherit">{{ t('capabilityInherit') }}</option>
        <option value="manual">{{ t('capabilityManual') }}</option>
      </select>
      <input
        v-model.number="pool.maxOutputTokens"
        name="maxOutputTokens"
        type="number"
        min="0"
        max="9007199254740991"
        :disabled="policy.maxOutputTokensMode === 'inherit'"
        required
      />
      <small data-testid="output-effective">{{
        effectiveText('output')
      }}</small>
      <small v-if="outputMode === 'manual'" data-testid="output-manual-note">{{
        t('capabilityManualActive')
      }}</small>
    </label>
    <label
      >{{ t('capabilityRecommendedContext') }}
      <input
        v-model.number="policy.recommendedContextWindow"
        name="recommendedContextWindow"
        type="number"
        min="0"
        max="9007199254740991"
        :placeholder="t('unknown')"
      />
    </label>
    <label
      >{{ t('capabilityRecommendedOutput') }}
      <input
        v-model.number="policy.recommendedMaxOutputTokens"
        name="recommendedMaxOutputTokens"
        type="number"
        min="0"
        max="9007199254740991"
        :placeholder="t('unknown')"
      />
    </label>
    <p
      v-if="
        policy.upstream?.model.maxInputTokens &&
        !policy.upstream.model.contextWindow
      "
      class="relay-wide"
      data-testid="input-only-capability"
    >
      {{
        t('capabilityInputOnly', {
          tokens: policy.upstream.model.maxInputTokens.toLocaleString(),
        })
      }}
    </p>
    <div class="relay-wide relay-limit-help">
      <p>{{ t('capabilityNote') }}</p>
    </div>
  </div>
</template>
