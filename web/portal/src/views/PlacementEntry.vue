<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { placementMessages } from './placement-messages';
import { apiClient } from '../api';
import {
  createPlacementApi,
  exchangePlacement,
  type Placement,
} from './placement-api';
const { t } = useI18n({ useScope: 'local', messages: placementMessages });
const api = createPlacementApi(apiClient),
  placement = ref<Placement>(),
  busy = ref(false),
  error = ref(''),
  empty = ref(false),
  expired = ref(false);
let generation = 0;
let abort = new globalThis.AbortController();
async function run(open = false) {
  if (busy.value || expired.value) return;
  const version = ++generation;
  abort.abort();
  abort = new globalThis.AbortController();
  busy.value = true;
  error.value = '';
  try {
    if (open) {
      const result = await api.open(
        Array.from(
          globalThis.crypto.getRandomValues(new Uint8Array(16)),
          (byte) => byte.toString(16).padStart(2, '0'),
        ).join(''),
        abort.signal,
      );
      if (version !== generation) return;
      placement.value = result.placement;
      await exchangePlacement(result, abort.signal);
    } else {
      const result = await api.read(abort.signal);
      if (version !== generation) return;
      if (!['shared', 'dedicated'].includes(result.mode))
        throw new Error('mode');
      placement.value = result;
      empty.value = false;
    }
  } catch (e) {
    if (version !== generation) return;
    placement.value = undefined;
    if (
      !open &&
      isApiError(e) &&
      e.status === 404 &&
      e.problem.code === 'common.not_found'
    )
      empty.value = true;
    else {
      empty.value = false;
      expired.value = isApiError(e) && e.status === 401;
      error.value =
        t(expired.value ? 'expired' : 'failed') +
        (isApiError(e) ? ` (${e.problem.code})` : '');
    }
  } finally {
    if (version === generation) busy.value = false;
  }
}
onMounted(() => run());
onBeforeUnmount(() => {
  generation++;
  abort.abort();
});
</script>
<template>
  <section :aria-busy="busy">
    <h2>{{ t('title') }}</h2>
    <p v-if="placement">{{ t(placement.mode) }} ? {{ placement.state }}</p>
    <p v-if="placement && placement.state !== 'ready'" role="status">
      {{ t('waiting') }}
    </p>
    <p v-if="empty">{{ t('empty') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <ElButton
      type="primary"
      :disabled="busy || expired || (!empty && placement?.state !== 'ready')"
      @click="run(true)"
      >{{ t('open') }}</ElButton
    >
    <ElButton :disabled="busy || expired" @click="run()">{{
      t('refresh')
    }}</ElButton>
    <p>{{ t('resource') }}</p>
    <p>{{ t('risk') }}</p>
    <p>{{ t('policy') }}</p>
  </section>
</template>
