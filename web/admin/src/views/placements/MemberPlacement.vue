<script setup lang="ts">
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import { placementMessages } from './messages';
import {
  placementPort,
  placementPortKey,
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
    snapshot.value?.placement.state === 'ready' &&
    (target.value === 'shared' || snapshot.value.dedicatedEntitled),
);
async function load() {
  const version = ++generation;
  abort.abort();
  abort = new globalThis.AbortController();
  snapshot.value = undefined;
  permissions.value = [];
  confirmed.value = false;
  operation.value = undefined;
  operationId.value = '';
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
      value.placement.memberId !== props.memberId ||
      !value.etag ||
      !['shared', 'dedicated'].includes(value.placement.mode)
    )
      throw new Error('binding');
    snapshot.value = value;
    target.value = value.placement.mode === 'shared' ? 'dedicated' : 'shared';
  } catch {
    if (version === generation) error.value = t('missing');
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
    const result = await port.switchMember(
      props.memberId,
      {
        targetMode: target.value,
        reason: reason.value.trim(),
        sessionPolicy: 'discard',
        filePolicy: 'archive-and-reset',
      },
      value.etag,
      Array.from(
        globalThis.crypto.getRandomValues(new Uint8Array(16)),
        (byte) => byte.toString(16).padStart(2, '0'),
      ).join(''),
      abort.signal,
    );
    if (version !== generation) return;
    if (result.memberId !== props.memberId) throw new Error('binding');
    operation.value = result;
    operationId.value = result.operationId;
    snapshot.value = undefined;
  } catch (e) {
    if (version === generation) {
      error.value = t('failed') + (isApiError(e) ? ` (${e.problem.code})` : '');
      snapshot.value = undefined;
    }
  } finally {
    if (version === generation) {
      busy.value = false;
      confirmed.value = false;
    }
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
watch(() => props.memberId, load, { immediate: true });
onBeforeUnmount(() => {
  generation++;
  abort.abort();
});
</script>
<template>
  <section class="placement" :aria-busy="busy">
    <h3>{{ t('title') }}</h3>
    <p>{{ t('risks') }}</p>
    <p>{{ t('impact') }}</p>
    <p>{{ t('budget') }}</p>
    <p>{{ t('resources') }}</p>
    <p>{{ t('stepup') }}</p>
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
    <p v-if="snapshot">
      {{ t(snapshot.placement.mode) }} ? {{ snapshot.placement.state }}
    </p>
    <button
      type="button"
      :disabled="busy || disabled || inFlight"
      @click="load"
    >
      {{ t('refresh') }}
    </button>
    <form v-if="snapshot && writable" @submit.prevent="submit">
      <h4>{{ t(target) }}</h4>
      <p>{{ t('policy') }}</p>
      <p>{{ t('risks') }}</p>
      <p>{{ t('impact') }}</p>
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
      <p>{{ operation.operationId }} ? {{ t(`states.${operation.state}`) }}</p>
      <p v-if="operation.failureCode">
        {{ t(`failures.${operation.failureCode}`) }}
      </p>
    </div>
    <h4>{{ t('preparingList') }}</h4>
    <p>{{ t('listHelp') }}</p>
  </section>
</template>
<style scoped>
.placement {
  border-top: 1px solid var(--auriga-border);
  margin-top: 1rem;
  padding-top: 1rem;
  line-height: 1.7;
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
