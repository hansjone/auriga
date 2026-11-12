<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import { ElButton, ElInput, ElForm, ElFormItem } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  settingsApi,
  policyEtag,
  type Policy,
  type WorkspaceValue,
} from '../../admin/settingsApi';
import { unsafeWorkspace } from '../../admin/settingsForm';
import { orgError } from '../../admin/useOrgActions';
import { useAdminI18n } from '../../admin/messages';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/management.css';
const { t } = useAdminI18n();
const policy = shallowRef<Policy>();
const etag = ref<string | null>(null);
const permissions = ref<string[]>([]);
const idle = ref('');
const starts = ref('');
const loading = ref(false);
const busy = ref(false);
const error = ref('');
const notice = ref('');
const pending = shallowRef<{
  value: WorkspaceValue;
  etag: string;
  message: string;
}>();
const locked = computed(() => loading.value || busy.value || !!pending.value);
const writable = computed(
  () =>
    permissions.value.includes('org.policies.write') &&
    !!policy.value &&
    policy.value.key === 'workspace' &&
    policyEtag(etag.value) &&
    !unsafeWorkspace(policy.value.value),
);
let alive = true;
const controller = new globalThis.AbortController();
async function authorize() {
  const result = await adminApi.membership();
  if (!alive) return false;
  permissions.value = result.permissions;
  if (!result.permissions.includes('org.policies.read')) {
    policy.value = undefined;
    etag.value = null;
    throw new Error('permission');
  }
  return true;
}
async function read() {
  const result = await settingsApi.workspace();
  if (!alive) return;
  policy.value = result.data;
  etag.value = result.etag;
  const raw = result.data.value as WorkspaceValue;
  idle.value = String(raw.idleMinutes ?? 30);
  starts.value = String(raw.maxConcurrentStarts ?? 4);
}
function fail(e: unknown) {
  error.value =
    e instanceof Error && e.message === 'permission'
      ? t('forbidden')
      : orgError(e, t);
  if (isApiError(e) && [401, 403, 412].includes(e.status)) {
    etag.value = null;
    policy.value = undefined;
  }
}
async function load() {
  if (locked.value) return;
  loading.value = true;
  error.value = '';
  notice.value = '';
  policy.value = undefined;
  etag.value = null;
  try {
    if (await authorize()) await read();
  } catch (e) {
    if (alive) fail(e);
  } finally {
    if (alive) loading.value = false;
  }
}
function ask() {
  if (
    locked.value ||
    !writable.value ||
    !policy.value ||
    !policyEtag(etag.value)
  )
    return;
  const valid = (v: string, min: number) =>
    /^\d+$/.test(v) && Number(v) >= min && Number(v) <= 2147483647;
  if (!valid(idle.value, 5) || !valid(starts.value, 1)) {
    error.value = t('lifecycleInvalid');
    return;
  }
  error.value = '';
  notice.value = '';
  const raw = policy.value.value as WorkspaceValue;
  pending.value = {
    value: {
      ...raw,
      idleMinutes: Number(idle.value),
      maxConcurrentStarts: Number(starts.value),
    },
    etag: etag.value,
    message: t('lifecycleConfirm', {
      idle: `${raw.idleMinutes ?? 30} → ${idle.value}`,
      starts: `${raw.maxConcurrentStarts ?? 4} → ${starts.value}`,
    }),
  };
}
async function confirm() {
  const action = pending.value;
  if (!action || busy.value) return;
  busy.value = true;
  try {
    if (!(await authorize()) || !alive) return;
    if (!permissions.value.includes('org.policies.write')) {
      error.value = t('forbidden');
      return;
    }
    await settingsApi.putWorkspace(
      action.value,
      action.etag,
      controller.signal,
    );
    if (!alive) return;
    // Invalidate the old ETag before readback; a failed readback must never replay this write.
    etag.value = null;
    notice.value = t('settingsReadback');
    await read();
    if (alive) notice.value = t('saved');
  } catch (e) {
    if (alive) fail(e);
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
onMounted(load);
onBeforeUnmount(() => {
  alive = false;
  controller.abort();
  pending.value = undefined;
});
</script>
<template>
  <section class="management">
    <div class="head">
      <h1>{{ t('opsLifecycle') }}</h1>
      <ElButton :disabled="locked" @click="load">{{ t('refresh') }}</ElButton>
    </div>
    <p class="muted">{{ t('lifecycleScope') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="policy" class="details">
      <p v-if="!writable">{{ t('readonly') }}</p>
      <ElForm label-position="top" class="edit-form" @submit.prevent="ask">
        <ElFormItem :label="t('lifecycleIdle')"
          ><ElInput
            v-model="idle"
            data-testid="idle-minutes"
            :disabled="locked || !writable"
        /></ElFormItem>
        <ElFormItem :label="t('lifecycleStarts')"
          ><ElInput
            v-model="starts"
            data-testid="concurrent-starts"
            :disabled="locked || !writable"
        /></ElFormItem>
        <ElButton
          data-testid="save-lifecycle"
          :disabled="locked || !writable"
          @click="ask"
          >{{ t('save') }}</ElButton
        >
      </ElForm>
    </section>
    <section class="details">
      <h2>{{ t('lifecycleLinks') }}</h2>
      <p>{{ t('lifecycleLinksText') }}</p>
      <div class="actions">
        <RouterLink to="/quotas">{{ t('quotaTitle') }}</RouterLink
        ><RouterLink to="/instances">{{ t('instances') }}</RouterLink>
      </div>
    </section>
    <section class="details">
      <h2>{{ t('lifecycleDrain') }}</h2>
      <p>{{ t('lifecycleDrainText') }}</p>
      <RouterLink to="/nodes">{{ t('nodes') }}</RouterLink>
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </section>
</template>
