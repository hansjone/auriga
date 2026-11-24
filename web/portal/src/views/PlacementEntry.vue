<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { HintTrigger, isApiError } from '@auriga/shared';
import { placementMessages } from './placement-messages';
import { apiClient } from '../api';
import {
  createPlacementApi,
  exchangePlacement,
  placementRequestKey,
  type Placement,
} from './placement-api';
const emit = defineEmits<{ fallback: [] }>();
const { t, locale } = useI18n({
  useScope: 'local',
  messages: placementMessages,
});
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
      const result = await api.open(placementRequestKey(), abort.signal);
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
  <section class="placement" :aria-busy="busy">
    <header class="placement-head">
      <h2>{{ t('title') }}</h2>
      <HintTrigger :label="locale === 'en' ? 'Placement notes' : '分配说明'">
        <p>
          <strong>{{ locale === 'en' ? 'Resources' : '资源' }}</strong>
          {{ t('resource') }}
        </p>
        <p>
          <strong>{{ locale === 'en' ? 'Risks' : '风险' }}</strong>
          {{ t('risk') }}
        </p>
        <p>
          <strong>{{ locale === 'en' ? 'Policy' : '策略' }}</strong>
          {{ t('policy') }}
        </p>
      </HintTrigger>
    </header>
    <p v-if="placement">
      {{ t(placement.mode) }} · {{ t('states.' + placement.state) }}
    </p>
    <p v-if="placement && placement.state !== 'ready'" role="status">
      {{ t('waiting') }}
    </p>
    <p v-if="empty">{{ t('empty') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <a v-if="expired" href="/login">{{ t('signIn') }}</a>
    <div class="actions">
      <ElButton
        type="primary"
        :disabled="busy || expired || (!empty && placement?.state !== 'ready')"
        @click="run(true)"
        >{{ t('open') }}</ElButton
      >
      <ElButton :disabled="busy || expired" @click="run()">{{
        t('refresh')
      }}</ElButton>
      <ElButton
        v-if="!expired && ((placement && placement.state !== 'ready') || error)"
        data-testid="fallback-personal"
        @click="emit('fallback')"
        >{{ t('fallbackAction') }}</ElButton
      >
    </div>
    <p
      v-if="!expired && ((placement && placement.state !== 'ready') || error)"
      role="status"
    >
      {{ t('fallbackHint') }}
    </p>
    <p v-if="placement?.state === 'ready'" class="market">
      <a href="/resources">{{ t('market') }}</a>
    </p>
  </section>
</template>
<style scoped>
.placement {
  margin: 0.25rem 0 1rem;
}
.placement-head {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  margin-bottom: 0.35rem;
}
h2 {
  margin: 0;
  font-size: 1.0625rem;
  font-weight: 600;
  letter-spacing: -0.02em;
}
p {
  margin: 0.35rem 0;
  font-size: 0.875rem;
  line-height: 1.55;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin: 0.75rem 0 0.35rem;
}
.actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.market {
  margin-top: 0.5rem;
}
a {
  color: var(--auriga-accent-line);
}
</style>
