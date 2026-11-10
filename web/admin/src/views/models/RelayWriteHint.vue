<script setup lang="ts">
import { useRelayI18n } from './relayMessages';

/**
 * Uncertain-write notice with the only safe next step: retrying the original
 * write. Rendered inside an open editor dialog (the page copy is hidden behind
 * the modal) and on the page otherwise.
 */
defineProps<{ disabled: boolean }>();
const emit = defineEmits<{ retry: [] }>();
const { t } = useRelayI18n();
</script>

<template>
  <div class="relay-hint" role="status" data-testid="relay-write-uncertain">
    <p>{{ t('writeUncertain') }}</p>
    <button
      type="button"
      data-testid="retry-relay-write"
      :disabled="disabled"
      @click="emit('retry')"
    >
      {{ t('retryOriginalWrite') }}
    </button>
  </div>
</template>
