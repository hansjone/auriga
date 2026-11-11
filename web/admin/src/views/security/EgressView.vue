<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import { ElButton, ElInput, ElForm, ElFormItem } from 'element-plus';
import {
  securityApi,
  type EgressValue,
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
        (key) => !['categories', 'allowDomains', 'denyDomains'].includes(key),
      ) ||
      (value.categories !== undefined &&
        (!Array.isArray(value.categories) || value.categories.length > 0)) ||
      [value.allowDomains, value.denyDomains].some(
        (list) =>
          list !== undefined &&
          (!Array.isArray(list) ||
            list.some((item) => typeof item !== 'string')),
      );
    allow.value = Array.isArray(value.allowDomains)
      ? value.allowDomains.join('\n')
      : '';
    deny.value = Array.isArray(value.denyDomains)
      ? value.denyDomains.join('\n')
      : '';
    uncertain.value = false;
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
function confirm() {
  if (locked.value) return;
  const split = (value: string) =>
    value
      .split(/\r?\n/)
      .map((v) => v.trim())
      .filter(Boolean);
  pending.value = {
    categories: [],
    allowDomains: split(allow.value),
    denyDomains: split(deny.value),
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
    await securityApi.save(value, version, abort.signal);
    if (!alive) return;
    notice.value = t('saved');
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
        <dd>{{ policy.version }}</dd>
        <dt>{{ t('securityApplied') }}</dt>
        <dd>{{ t('unknown') }}</dd>
        <dt>{{ t('securityReport') }}</dt>
        <dd>{{ t('securityPending') }}</dd>
      </dl>
      <p>{{ t('securityUpstream') }}</p>
      <p v-if="unsupported" role="alert">{{ t('securityUnsupported') }}</p>
      <p v-if="!canWrite">{{ t('readonly') }}</p>
      <ElForm class="edit-form" label-position="top" @submit.prevent="confirm">
        <ElFormItem :label="t('securityAllow')"
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
          @click="confirm"
          >{{ t('save') }}</ElButton
        >
      </ElForm>
    </div>
    <ActionConfirm
      :message="pending ? t('securityConfirm') : undefined"
      :busy="busy"
      @confirm="save"
      @cancel="pending = undefined"
    />
  </section>
</template>
