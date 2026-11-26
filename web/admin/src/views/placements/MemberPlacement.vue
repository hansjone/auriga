<script setup lang="ts">
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { HintTrigger, isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import { placementMessages } from './messages';
import {
  placementPort,
  placementAdminApi,
  selectable,
  placementPortKey,
  intentKey,
  type MemberPlacementSnapshot,
  type Switch,
} from './api';
const props = defineProps<{ memberId: string; disabled?: boolean }>();
const { t } = useI18n({ useScope: 'local', messages: placementMessages });
const port = inject(placementPortKey, placementPort);
const snapshot = ref<MemberPlacementSnapshot>();
const permissions = ref<string[]>([]);
const busy = ref(false),
  confirmed = ref(false),
  reason = ref(''),
  error = ref(''),
  operationId = ref('');
const operation = ref<Switch>();
const pendingKey = ref('');
const entitlementIntent = ref<{
  granted: boolean;
  reason: string;
  etag: string;
  key: string;
}>();
const saved = ref(false),
  migratable = ref(false);
const target = ref<'shared' | 'dedicated'>('dedicated');
let generation = 0;
let abort = new globalThis.AbortController();
const writable = computed(() =>
  permissions.value.includes('workspace.placements.write'),
);
const inFlight = computed(
  () =>
    !!operation.value &&
    !['succeeded', 'failed'].includes(operation.value.state),
);
const eligible = computed(
  () =>
    !pendingKey.value &&
    !entitlementIntent.value &&
    snapshot.value?.placement?.state === 'ready' &&
    (target.value === 'shared' || snapshot.value.dedicatedEntitled),
);
async function load() {
  if (pendingKey.value && !globalThis.confirm(t('abandonPending'))) return;
  pendingKey.value = '';
  const version = ++generation;
  abort.abort();
  abort = new globalThis.AbortController();
  snapshot.value = undefined;
  migratable.value = false;
  permissions.value = [];
  confirmed.value = false;
  error.value = '';
  busy.value = true;
  try {
    const access = await adminApi.membership();
    if (version !== generation) return;
    permissions.value = access.permissions;
    if (!access.permissions.includes('workspace.placements.read'))
      throw new Error('permission');
    const value = await port.readMember(props.memberId, abort.signal);
    if (version !== generation) return;
    if (
      (value.placement && value.placement.memberId !== props.memberId) ||
      !value.etag ||
      (value.placement &&
        !['shared', 'dedicated'].includes(value.placement.mode))
    )
      throw new Error('binding');
    snapshot.value = value;
    if (!value.placement && writable.value) {
      void placementAdminApi
        .candidates('', 0, abort.signal, props.memberId)
        .then((candidates) => {
          if (version === generation)
            migratable.value = candidates.items.some(
              (c) => c.memberId === props.memberId && selectable(c),
            );
        })
        .catch(() => {
          if (version === generation) migratable.value = false;
        });
    }
    target.value = value.placement?.mode === 'shared' ? 'dedicated' : 'shared';
  } catch (e) {
    if (version === generation)
      error.value =
        t('missing') + (isApiError(e) ? ` (${e.problem.code})` : '');
  } finally {
    if (version === generation) busy.value = false;
  }
}
async function submit() {
  if (
    busy.value ||
    props.disabled ||
    !writable.value ||
    !eligible.value ||
    !confirmed.value ||
    !reason.value.trim() ||
    inFlight.value
  )
    return;
  const value = snapshot.value!;
  const version = generation;
  busy.value = true;
  error.value = '';
  try {
    const access = await adminApi.membership();
    if (version !== generation) return;
    permissions.value = access.permissions;
    if (!writable.value) throw new Error('permission');
    pendingKey.value = intentKey();
    const result = await port.switchMember(
      props.memberId,
      {
        targetMode: target.value,
        reason: reason.value.trim(),
        sessionPolicy: 'discard',
        filePolicy: 'archive-and-reset',
      },
      value.etag,
      pendingKey.value,
      abort.signal,
    );
    if (version !== generation) return;
    if (result.memberId !== props.memberId) throw new Error('binding');
    operation.value = result;
    operationId.value = result.operationId;
    pendingKey.value = '';
    snapshot.value = undefined;
  } catch (e) {
    if (version === generation) {
      error.value = t('failed') + (isApiError(e) ? ` (${e.problem.code})` : '');
      snapshot.value = undefined;
      if (
        (isApiError(e) &&
          e.status >= 400 &&
          e.status < 500 &&
          ![408, 429].includes(e.status)) ||
        (e instanceof Error &&
          ['操作已取消', 'Operation cancelled'].includes(e.message))
      )
        pendingKey.value = '';
    }
  } finally {
    if (version === generation) {
      busy.value = false;
      confirmed.value = false;
    }
  }
}
async function lookupPending() {
  if (
    busy.value ||
    !pendingKey.value ||
    !permissions.value.includes('workspace.placements.read')
  )
    return;
  const version = generation;
  busy.value = true;
  error.value = '';
  try {
    const value = await port.lookup(
      props.memberId,
      pendingKey.value,
      abort.signal,
    );
    if (version !== generation) return;
    if (value.memberId !== props.memberId) throw new Error('binding');
    operation.value = value;
    operationId.value = value.operationId;
    pendingKey.value = '';
  } catch (e) {
    if (version === generation)
      error.value =
        t('uncertain') + (isApiError(e) ? ` (${e.problem.code})` : '');
  } finally {
    if (version === generation) busy.value = false;
  }
}
async function setEntitlement(retry = false) {
  if (
    busy.value ||
    props.disabled ||
    !writable.value ||
    pendingKey.value ||
    inFlight.value
  )
    return;
  if (!retry) {
    if (!snapshot.value || !confirmed.value || !reason.value.trim()) return;
    entitlementIntent.value = {
      granted: !snapshot.value.dedicatedEntitled,
      reason: reason.value.trim(),
      etag: snapshot.value.etag,
      key: intentKey(),
    };
  }
  const intent = entitlementIntent.value;
  if (!intent) return;
  const version = generation;
  busy.value = true;
  error.value = '';
  saved.value = false;
  try {
    const access = await adminApi.membership();
    if (version !== generation) return;
    permissions.value = access.permissions;
    if (!writable.value) throw new Error('permission');
    await port.entitlement(
      props.memberId,
      intent.granted,
      intent.reason,
      intent.etag,
      intent.key,
      abort.signal,
    );
    if (version !== generation) return;
    entitlementIntent.value = undefined;
    confirmed.value = false;
    saved.value = true;
    await load();
  } catch (e) {
    if (version === generation) {
      snapshot.value = undefined;
      error.value = t('failed') + (isApiError(e) ? ` (${e.problem.code})` : '');
      if (
        isApiError(e) &&
        e.status >= 400 &&
        e.status < 500 &&
        ![408, 429].includes(e.status)
      )
        entitlementIntent.value = undefined;
    }
  } finally {
    if (version === generation) busy.value = false;
  }
}
async function progress() {
  if (
    busy.value ||
    !operationId.value ||
    !permissions.value.includes('workspace.placements.read')
  )
    return;
  const version = generation;
  const id = operationId.value;
  busy.value = true;
  error.value = '';
  try {
    const value = await port.progress(id, abort.signal);
    if (version !== generation) return;
    if (value.memberId !== props.memberId || value.operationId !== id)
      throw new Error('binding');
    if (!operation.value || value.revision >= operation.value.revision)
      operation.value = value;
  } catch (e) {
    if (version === generation)
      error.value = t('failed') + (isApiError(e) ? ` (${e.problem.code})` : '');
  } finally {
    if (version === generation) busy.value = false;
  }
}
watch(
  () => props.memberId,
  () => {
    pendingKey.value = '';
    entitlementIntent.value = undefined;
    saved.value = false;
    reason.value = '';
    operation.value = undefined;
    operationId.value = '';
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  generation++;
  abort.abort();
});
</script>
<template>
  <section class="placement" :aria-busy="busy">
    <header class="placement-head">
      <h3>{{ t('title') }}</h3>
      <HintTrigger>
        <p>
          <strong>{{ t('risks') }}</strong>
        </p>
        <p>{{ t('impact') }}</p>
        <p>{{ t('budget') }}</p>
        <p>{{ t('resources') }}</p>
      </HintTrigger>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="!writable">{{ t('denied') }}</p>
    <p>
      {{ t('authorization') }}:
      {{
        t(
          snapshot
            ? snapshot.dedicatedEntitled
              ? 'granted'
              : 'notGranted'
            : 'unknown',
        )
      }}
    </p>
    <p v-if="snapshot?.placement">
      {{ t(snapshot.placement.mode) }} ·
      {{ t('placementStates.' + snapshot.placement.state) }}
    </p>
    <p v-else-if="snapshot">{{ t('unassigned') }}</p>
    <RouterLink
      v-if="migratable && writable && !disabled && !busy && !entitlementIntent"
      :to="{ path: '/instances/migration', query: { memberId } }"
      >{{ t('migrateLegacy') }}</RouterLink
    >
    <p v-if="saved" role="status">{{ t('entitlementSaved') }}</p>
    <p v-if="pendingKey" role="alert">
      {{ t('uncertain') }} <code>{{ pendingKey.slice(0, 8) }}</code>
    </p>
    <button
      v-if="pendingKey"
      type="button"
      :disabled="busy"
      @click="lookupPending"
    >
      {{ t('lookupPending') }}
    </button>
    <button
      v-if="entitlementIntent"
      type="button"
      :disabled="busy"
      @click="setEntitlement(true)"
    >
      {{ t('retryEntitlement') }}
    </button>
    <button
      type="button"
      :disabled="busy || disabled || !!entitlementIntent"
      @click="load"
    >
      {{ t('refresh') }}
    </button>
    <form v-if="snapshot && writable" @submit.prevent="submit">
      <header class="placement-head">
        <h4 v-if="snapshot.placement">{{ t(target) }}</h4>
        <HintTrigger>
          <p>{{ t('policy') }}</p>
          <p>{{ t('risks') }}</p>
          <p>{{ t('impact') }}</p>
          <p>{{ t('entitlementHelp') }}</p>
        </HintTrigger>
      </header>
      <label
        >{{ t('reason')
        }}<textarea
          v-model="reason"
          required
          maxlength="512"
          :disabled="busy || disabled"
        />
      </label>
      <label
        ><input
          v-model="confirmed"
          type="checkbox"
          :disabled="busy || disabled"
        />{{ t('confirm') }}</label
      >
      <button
        v-if="snapshot.placement"
        :disabled="
          busy ||
          disabled ||
          !eligible ||
          !confirmed ||
          !reason.trim() ||
          inFlight
        "
      >
        {{ t('switch') }}
      </button>
      <button
        type="button"
        :disabled="
          busy ||
          disabled ||
          !confirmed ||
          !reason.trim() ||
          inFlight ||
          !!pendingKey ||
          !!entitlementIntent ||
          snapshot.placement?.mode === 'dedicated' ||
          snapshot.placement?.state === 'switching'
        "
        @click="setEntitlement()"
      >
        {{ t(snapshot.dedicatedEntitled ? 'revoke' : 'grant') }}
      </button>
    </form>
    <label
      >{{ t('operation') }}<input v-model="operationId" :disabled="busy"
    /></label>
    <button
      type="button"
      :disabled="
        busy ||
        !operationId ||
        !permissions.includes('workspace.placements.read')
      "
      @click="progress"
    >
      {{ t('lookup') }}
    </button>
    <div v-if="operation" role="status">
      <p v-if="operation.state === 'accepted'">{{ t('accepted') }}</p>
      <p>{{ operation.operationId }} · {{ t(`states.${operation.state}`) }}</p>
      <p v-if="operation.failureCode">
        {{ t(`failures.${operation.failureCode}`) }}
      </p>
    </div>
  </section>
</template>
<style scoped>
.placement {
  border-top: 1px solid var(--auriga-border);
  margin-top: 1rem;
  padding-top: 1rem;
  line-height: 1.55;
}
.placement-head {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  margin-bottom: 0.5rem;
}
.placement-head h3,
.placement-head h4 {
  margin: 0;
}
label {
  display: block;
  margin: 1rem 0;
}
textarea {
  display: block;
  width: 100%;
  min-height: 4rem;
}
button,
input,
textarea {
  font: inherit;
  color: inherit;
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  padding: 0.4rem;
}
button {
  cursor: pointer;
  margin: 0.3rem;
}
button:disabled {
  opacity: 0.5;
  cursor: default;
}
input[type='checkbox'] {
  margin-right: 0.5rem;
}
p {
  overflow-wrap: anywhere;
}
</style>
