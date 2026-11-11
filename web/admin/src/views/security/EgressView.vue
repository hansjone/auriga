<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import {
  ElButton,
  ElInput,
  ElForm,
  ElFormItem,
  ElRadioGroup,
  ElRadioButton,
} from 'element-plus';
import {
  securityApi,
  type EgressValue,
  type EgressStatus,
  type Policy,
} from '../../admin/securityApi';
import { adminApi } from '../../admin/api';
import { isApiError } from '@auriga/shared';
import { policyEtag } from '../../admin/settingsApi';
import { useAdminI18n } from '../../admin/messages';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/management.css';
const { t } = useAdminI18n();
const policy = shallowRef<Policy>(),
  etag = ref<string | null>(null);
const permissions = ref<string[]>([]),
  allow = ref(''),
  deny = ref('');
const loading = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref(''),
  uncertain = ref(false),
  unsupported = ref(false);
const pending = shallowRef<EgressValue>();
const mode = ref<'open' | 'allowlist'>('open');
const status = shallowRef<EgressStatus>();
const phase = computed(() => {
  const keys = {
    unconfigured: 'securityUnconfigured',
    saved: 'securitySaved',
    distributed: 'securityDistributed',
    effective: 'securityEffective',
    failed: 'securityFailed',
    stale: 'securityStale',
    disabled: 'securityDisabled',
  } as const;
  return status.value ? t(keys[status.value.phase]) : t('unavailable');
});
const confirmation = computed(() => {
  const value = pending.value;
  if (!value) return undefined;
  if (value.mode === 'allowlist' && !value.allowDomains?.length)
    return t('securityDenyAllConfirm');
  if (value.mode === 'open' && !value.denyDomains?.length)
    return t('securityRestoreConfirm');
  return t('securityConfirm');
});
let alive = true;
const abort = new globalThis.AbortController();
onBeforeUnmount(() => {
  alive = false;
  abort.abort();
});
const canWrite = computed(() =>
  permissions.value.includes('org.policies.write'),
);
const locked = computed(
  () =>
    loading.value ||
    busy.value ||
    !!pending.value ||
    uncertain.value ||
    unsupported.value ||
    !canWrite.value ||
    !policyEtag(etag.value) ||
    !policy.value,
);
async function load() {
  if (loading.value || busy.value) return;
  pending.value = undefined;
  policy.value = undefined;
  status.value = undefined;
  etag.value = null;
  permissions.value = [];
  loading.value = true;
  error.value = '';
  notice.value = '';
  try {
    const membership = await adminApi.membership();
    if (!alive) return;
    permissions.value = membership.permissions;
    if (!permissions.value.includes('org.policies.read')) {
      error.value = t('forbidden');
      return;
    }
    const result = await securityApi.egress();
    if (!alive) return;
    policy.value = result.data;
    etag.value = result.etag;
    const value = result.data.value as EgressValue;
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
      policy.value = undefined;
      etag.value = null;
      throw new Error('Invalid policy');
    }
    unsupported.value =
      result.data.key !== 'egress' ||
      Object.keys(value).some(
        (key) =>
          !['mode', 'categories', 'allowDomains', 'denyDomains'].includes(key),
      ) ||
      (value.mode !== undefined &&
        !['open', 'allowlist'].includes(value.mode)) ||
      (value.categories !== undefined &&
        (!Array.isArray(value.categories) || value.categories.length > 0)) ||
      [value.allowDomains, value.denyDomains].some(
        (list) =>
          list !== undefined &&
          (!Array.isArray(list) ||
            list.some((item) => typeof item !== 'string')),
      );
    mode.value =
      value.mode ?? (value.allowDomains?.length ? 'allowlist' : 'open');
    allow.value = Array.isArray(value.allowDomains)
      ? value.allowDomains.join('\n')
      : '';
    deny.value = Array.isArray(value.denyDomains)
      ? value.denyDomains.join('\n')
      : '';
    uncertain.value = false;
    try {
      const observation = await securityApi.status();
      if (alive) status.value = observation;
    } catch (failure) {
      if (alive) {
        status.value = undefined;
        if (
          isApiError(failure) &&
          (failure.status === 401 || failure.status === 403)
        ) {
          policy.value = undefined;
          permissions.value = [];
          etag.value = null;
          error.value = t('forbidden');
        }
      }
    }
  } catch (failure) {
    if (alive) {
      policy.value = undefined;
      etag.value = null;
      error.value = t(
        isApiError(failure) && failure.status === 403
          ? 'forbidden'
          : 'unavailable',
      );
    }
  } finally {
    if (alive) loading.value = false;
  }
}
function confirm(restore = false) {
  if (locked.value) return;
  const split = (value: string) =>
    value
      .split(/\r?\n/)
      .map((v) => v.trim())
      .filter(Boolean);
  pending.value = {
    mode: restore ? 'open' : mode.value,
    categories: [],
    allowDomains:
      !restore && mode.value === 'allowlist' ? split(allow.value) : [],
    denyDomains: restore ? [] : split(deny.value),
  };
}
async function save() {
  if (!pending.value || busy.value || !policyEtag(etag.value)) return;
  const value = pending.value,
    version = etag.value;
  busy.value = true;
  error.value = '';
  notice.value = '';
  try {
    const member = await adminApi.membership();
    if (!alive) return;
    permissions.value = member.permissions;
    if (!permissions.value.includes('org.policies.read') || !canWrite.value) {
      error.value = t('forbidden');
      policy.value = undefined;
      return;
    }
    const saved = await securityApi.save(value, version, abort.signal);
    if (!alive) return;
    policy.value = saved;
    const savedValue = saved.value as EgressValue;
    mode.value =
      savedValue.mode ??
      (savedValue.allowDomains?.length ? 'allowlist' : 'open');
    allow.value = savedValue.allowDomains?.join('\n') ?? '';
    deny.value = savedValue.denyDomains?.join('\n') ?? '';
    notice.value = t('securitySaved');
    status.value = {
      savedVersion: String(saved.version),
      phase: 'saved',
      nodeId: status.value?.nodeId ?? t('unknown'),
    };
    uncertain.value = true;
  } catch (failure) {
    if (alive) {
      if (
        isApiError(failure) &&
        (failure.status === 401 || failure.status === 403)
      ) {
        permissions.value = [];
        policy.value = undefined;
        etag.value = null;
      }
      error.value = t('securityReload');
      uncertain.value = true;
    }
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
onMounted(load);
</script>
<template>
  <section class="management">
    <div class="head">
      <h1>{{ t('securityEgress') }}</h1>
      <ElButton :disabled="busy" :loading="loading" @click="load">{{
        t('refresh')
      }}</ElButton>
    </div>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p class="muted">{{ t('securityBoundary') }}</p>
    <div v-if="policy" class="details">
      <dl>
        <dt>{{ t('securityStored') }}</dt>
        <dd>{{ status?.savedVersion ?? policy.version }}</dd>
        <dt>{{ t('securityDistributedVersion') }}</dt>
        <dd>{{ status?.distributedVersion ?? t('unknown') }}</dd>
        <dt>{{ t('securityApplied') }}</dt>
        <dd>{{ status?.appliedVersion ?? t('unknown') }}</dd>
        <dt>{{ t('securityReport') }}</dt>
        <dd data-testid="egress-phase">{{ phase }}</dd>
        <dt>{{ t('securityNode') }}</dt>
        <dd>{{ status?.nodeId ?? t('unknown') }}</dd>
        <dt>{{ t('securityReportedAt') }}</dt>
        <dd>{{ status?.reportedAt ?? t('unknown') }}</dd>
      </dl>
      <p class="muted">{{ t('securityEmptyAllow') }}</p>
      <p v-if="status?.legacyAdjusted" role="alert">
        {{ t('securityLegacy') }}
      </p>
      <p v-if="status?.reportError" role="alert">
        {{ t('securityReportInvalid') }}
      </p>
      <dl>
        <dt>{{ t('securityRelay') }}</dt>
        <dd>{{ status?.relayPublicUrl ?? t('unknown') }}</dd>
        <dt>{{ t('securityExit') }}</dt>
        <dd>{{ t('securityExitUnobserved') }}</dd>
      </dl>
      <p v-if="unsupported" role="alert">{{ t('securityUnsupported') }}</p>
      <p v-if="!canWrite">{{ t('readonly') }}</p>
      <ElForm
        class="edit-form"
        label-position="top"
        @submit.prevent="confirm()"
      >
        <ElFormItem :label="t('securityMode')">
          <ElRadioGroup
            v-model="mode"
            data-testid="egress-mode"
            :disabled="locked"
          >
            <ElRadioButton value="open">{{ t('securityOpen') }}</ElRadioButton>
            <ElRadioButton value="allowlist">{{
              t('securityAllowlist')
            }}</ElRadioButton>
          </ElRadioGroup>
        </ElFormItem>
        <ElFormItem v-if="mode === 'allowlist'" :label="t('securityAllow')"
          ><ElInput
            v-model="allow"
            type="textarea"
            :rows="5"
            :disabled="locked"
        /></ElFormItem>
        <ElFormItem :label="t('securityDeny')"
          ><ElInput v-model="deny" type="textarea" :rows="5" :disabled="locked"
        /></ElFormItem>
        <p class="muted">{{ t('securityInvalid') }}</p>
        <ElButton
          data-testid="save-policy"
          type="primary"
          :disabled="locked"
          @click="confirm()"
          >{{ t('save') }}</ElButton
        >
        <ElButton
          data-testid="restore-default"
          :disabled="locked"
          @click="confirm(true)"
          >{{ t('securityRestore') }}</ElButton
        >
      </ElForm>
    </div>
    <ActionConfirm
      :message="confirmation"
      :busy="busy"
      @confirm="save"
      @cancel="pending = undefined"
    />
  </section>
</template>
