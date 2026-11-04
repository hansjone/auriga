<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import { relayApi, type Usage } from '../../admin/relayApi';
import { requestStepUp } from '../../admin/stepUp';
import { useRelayI18n } from './relayMessages';
import UsageSummary from './UsageSummary.vue';
import UsageRecords from './UsageRecords.vue';
import './modelUsage.css';
const { t } = useRelayI18n();
const kind = ref<'accountId' | 'keyId'>('accountId'),
  identifier = ref(''),
  rows = shallowRef<Usage[]>([]),
  loading = ref(false),
  error = ref(''),
  snapshot = ref(''),
  next = ref<string>(),
  verify = ref(false);
let generation = 0,
  alive = true,
  controller: InstanceType<typeof globalThis.AbortController> | undefined;
function reset() {
  generation++;
  controller?.abort();
  rows.value = [];
  next.value = undefined;
  snapshot.value = '';
  error.value = '';
  verify.value = false;
  loading.value = false;
}
watch([kind, identifier], reset);
onBeforeUnmount(() => {
  alive = false;
  reset();
});
onMounted(() => {
  rows.value = [];
});
async function query(cursor?: string) {
  if (loading.value) return;
  const value = identifier.value.trim();
  if (
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      value,
    )
  ) {
    error.value = t('invalid');
    return;
  }
  const version = ++generation;
  controller?.abort();
  controller = new globalThis.AbortController();
  const signal = controller.signal;
  loading.value = true;
  error.value = '';
  verify.value = false;
  rows.value = [];
  snapshot.value = '';
  next.value = undefined;
  try {
    const membership = await adminApi.membership();
    signal.throwIfAborted();
    if (!membership.permissions.includes('ai.usage.admin')) {
      error.value = t('forbidden');
      return;
    }
    const result = await relayApi.usage(kind.value, value, cursor, signal);
    if (alive && version === generation) {
      rows.value = result.items;
      next.value = result.nextCursor;
      snapshot.value = new Date().toISOString();
    }
  } catch (e) {
    if (alive && version === generation) {
      verify.value = isApiError(e) && e.problem.code === 'iam.step_up_required';
      error.value = t(
        verify.value
          ? 'stepup'
          : isApiError(e) && (e.status === 401 || e.status === 403)
            ? 'forbidden'
            : 'failed',
      );
    }
  } finally {
    if (alive && version === generation) loading.value = false;
  }
}
async function verifyAndQuery() {
  const current = generation;
  try {
    await requestStepUp();
    if (alive && current === generation) await query();
  } catch {
    if (alive && current === generation) error.value = t('stepup');
  }
}
</script>
<template>
  <section class="model-usage">
    <h1>{{ t('usage') }}</h1>
    <form class="usage-filters" @submit.prevent="query()">
      <label
        >{{ t('selector')
        }}<select v-model="kind" name="usageKind">
          <option value="accountId">{{ t('member') }}</option>
          <option value="keyId">{{ t('keys') }}</option>
        </select></label
      ><label
        >{{ t(kind === 'accountId' ? 'account' : 'keyId')
        }}<input v-model="identifier" name="usageId" required /></label
      ><button :disabled="loading" type="submit">{{ t('query') }}</button>
    </form>
    <p>{{ t('queryNote') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button v-if="verify" :disabled="loading" @click="verifyAndQuery">
      {{ t('verify') }}</button
    ><template v-if="snapshot"
      ><p data-testid="usage-snapshot">{{ t('snapshot') }}: {{ snapshot }}</p>
      <UsageSummary :rows="rows" /><UsageRecords :rows="rows" /><button
        v-if="next"
        :disabled="loading"
        @click="query(next)"
      >
        {{ t('next') }}
      </button></template
    >
    <p>{{ t('scope') }}</p>
  </section>
</template>
