<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import { useRoute } from 'vue-router';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import { relayApi, type RelayKey, type Usage } from '../../admin/relayApi';
import { requestStepUp } from '../../admin/stepUp';
import { useRelayI18n } from './relayMessages';
import UsageSummary from './UsageSummary.vue';
import UsageRecords from './UsageRecords.vue';
import { useMemberNames } from './relayFormat';
import './modelUsage.css';
import './relayManagement.css';
const { t } = useRelayI18n();
const route = useRoute();
const members = useMemberNames();
const MANUAL = '__manual';
const keys = shallowRef<RelayKey[]>([]),
  target = ref('');
const memberKeys = computed(() =>
  keys.value
    .filter((k) => k.source === 'member')
    .sort((a, b) =>
      members.keyLabel(a).localeCompare(members.keyLabel(b), undefined),
    ),
);
const externalKeys = computed(() =>
  keys.value
    .filter((k) => k.source !== 'member')
    .sort((a, b) => a.name.localeCompare(b.name, undefined)),
);
const manual = computed(() => target.value === MANUAL);
const lifetime = new globalThis.AbortController();
onBeforeUnmount(() => lifetime.abort());
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
watch(
  target,
  (value) => {
    if (value === MANUAL) {
      identifier.value = '';
      return;
    }
    kind.value = 'keyId';
    identifier.value = value;
  },
  { flush: 'sync' },
);
onMounted(async () => {
  rows.value = [];
  const preset = typeof route.query.key === 'string' ? route.query.key : '';
  await Promise.all([
    relayApi
      .keys(lifetime.signal)
      .then((page) => {
        if (alive) keys.value = page.items;
      })
      .catch(() => {
        /* key directory unavailable: manual ID entry still works */
      }),
    members.load(lifetime.signal),
  ]);
  if (!alive) return;
  if (preset) {
    target.value = keys.value.some((k) => k.id === preset) ? preset : MANUAL;
    kind.value = 'keyId';
    identifier.value = preset;
    await nextTick();
    await query();
  } else if (!keys.value.length) target.value = MANUAL;
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
    <header>
      <h1>{{ t('usage') }}</h1>
    </header>
    <form class="usage-filters" @submit.prevent="query()">
      <label
        >{{ t('selector')
        }}<select v-model="target" name="usageTarget">
          <option value="" disabled>{{ t('chooseTarget') }}</option>
          <optgroup v-if="memberKeys.length" :label="t('memberKeys')">
            <option v-for="k in memberKeys" :key="k.id" :value="k.id">
              {{ members.keyLabel(k) }}
            </option>
          </optgroup>
          <optgroup v-if="externalKeys.length" :label="t('externalKeys')">
            <option v-for="k in externalKeys" :key="k.id" :value="k.id">
              {{ k.name }}{{ k.active ? '' : ' · ' + t('inactive') }}
            </option>
          </optgroup>
          <option :value="MANUAL">{{ t('manualTarget') }}</option>
        </select></label
      ><template v-if="manual"
        ><label
          >{{ t('selector')
          }}<select v-model="kind" name="usageKind">
            <option value="accountId">{{ t('member') }}</option>
            <option value="keyId">{{ t('keys') }}</option>
          </select></label
        ><label class="usage-id"
          >{{ t(kind === 'accountId' ? 'account' : 'keyId')
          }}<input v-model="identifier" name="usageId" required /></label
      ></template>
      <button
        class="relay-primary"
        :disabled="loading || !identifier"
        type="submit"
      >
        {{ t('query') }}
      </button>
    </form>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="relay-error" role="alert">{{ error }}</p>
    <button v-if="verify" :disabled="loading" @click="verifyAndQuery">
      {{ t('verify') }}</button
    ><template v-if="snapshot"
      ><p data-testid="usage-snapshot">
        {{ t('pageSummary', { count: rows.length }) }}
      </p>
      <UsageSummary :rows="rows" /><UsageRecords
        :rows="rows"
        :member-name="members.memberName"
      /><button v-if="next" :disabled="loading" @click="query(next)">
        {{ t('next') }}
      </button></template
    >
    <p class="relay-footnote">{{ t('scope') }}</p>
  </section>
</template>
