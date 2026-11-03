<script setup lang="ts">
import { computed, watch } from 'vue';
import type { RelayPool, RelayProvider } from '../../admin/relayApi';
import { useRelayI18n } from './relayMessages';
import {
  applyCapabilityPolicy,
  capabilityOrigin,
  captureUpstreamCapabilities,
  reconcileCapabilitySource,
  sourceKey,
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
const selectedSource = computed({
  get: () => sourceKey(policy.value.sourceProviderId, policy.value.sourceModel),
  set(value: string) {
    const [providerId, model] = JSON.parse(value) as [string, string];
    policy.value.sourceProviderId = providerId;
    policy.value.sourceModel = model;
    delete policy.value.upstream;
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
    policy.value.contextWindowMode,
    policy.value.maxOutputTokensMode,
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
    <label
      >{{ t('context') }}
      <select v-model="policy.contextWindowMode" name="contextCapabilityMode">
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
      <small>{{
        t('capabilityOrigin_' + capabilityOrigin(pool, 'context'))
      }}</small>
    </label>
    <label
      >{{ t('output') }}
      <select v-model="policy.maxOutputTokensMode" name="outputCapabilityMode">
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
      <small>{{
        t('capabilityOrigin_' + capabilityOrigin(pool, 'output'))
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
      <p>{{ t('capabilityNote') }}</p>
    </div>
  </div>
</template>
