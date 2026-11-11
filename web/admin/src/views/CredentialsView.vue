<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import { ElButton, ElTable, ElTableColumn } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { credentialsApi, type Credential } from '../admin/credentialsApi';
import { adminApi } from '../admin/api';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';
const { t } = useAdminI18n();
const rows = shallowRef<Credential[]>([]);
const pending = shallowRef<Credential>();
const type = ref(''),
  status = ref(''),
  soon = ref(false);
const loading = ref(false),
  busy = ref(false),
  allowed = ref(false);
const services = ref(true),
  error = ref(''),
  notice = ref('');
let alive = true;
onBeforeUnmount(() => {
  alive = false;
});
function failure(e: unknown) {
  return isApiError(e) && e.status === 403 ? t('forbidden') : t('failed');
}
async function load() {
  if (loading.value || busy.value || pending.value) return;
  loading.value = true;
  error.value = '';
  rows.value = [];
  allowed.value = false;
  try {
    const membership = await adminApi.membership();
    if (!alive) return;
    if (!membership.permissions.includes('iam.keys.write')) {
      error.value = t('forbidden');
      return;
    }
    const result = await credentialsApi.list(
      type.value,
      status.value,
      soon.value,
    );
    if (!alive) return;
    allowed.value = true;
    rows.value = result.items;
    services.value = result.serviceMetadataAvailable;
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) loading.value = false;
  }
}
function ask(id: string) {
  if (!allowed.value || busy.value || loading.value || pending.value) return;
  pending.value = rows.value.find(
    (row) =>
      row.id === id && row.type === 'managed' && row.status !== 'revoked',
  );
}
async function confirm() {
  const row = pending.value;
  if (!row?.instanceId || !row.version || busy.value) return;
  let succeeded = false;
  busy.value = true;
  error.value = '';
  notice.value = '';
  try {
    await credentialsApi.revoke(row.instanceId, row.version);
    succeeded = true;
    if (alive) notice.value = t('credentialRevoked');
  } catch (e) {
    if (alive) {
      error.value = failure(e);
      if (isApiError(e) && e.status === 403) {
        rows.value = [];
        allowed.value = false;
      }
    }
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
      if (succeeded) await load();
    }
  }
}
onMounted(load);
</script>
<template>
  <section class="management credentials">
    <header class="head">
      <h1>{{ t('credentialTitle') }}</h1>
      <RouterLink to="/models/keys">{{ t('credentialRelay') }}</RouterLink>
    </header>
    <div class="label-row">
      <label
        >{{ t('credentialType') }}
        <select
          v-model="type"
          name="credentialType"
          :disabled="loading || busy || !!pending"
        >
          <option value="">{{ t('credentialAll') }}</option>
          <option value="managed">{{ t('credentialManaged') }}</option>
          <option value="signing">{{ t('credentialSigning') }}</option>
        </select></label
      >
      <label
        >{{ t('credentialStatus') }}
        <select
          v-model="status"
          name="credentialStatus"
          :disabled="loading || busy || !!pending"
        >
          <option value="">{{ t('credentialAll') }}</option>
          <option
            v-for="value in [
              'active',
              'revoked',
              'expired',
              'next',
              'retiring',
            ]"
            :key="value"
            :value="value"
          >
            {{ t(`credentialStates.${value}`) }}
          </option>
        </select></label
      >
      <label
        ><input
          v-model="soon"
          type="checkbox"
          name="expiringSoon"
          :disabled="loading || busy || !!pending"
        />{{ t('credentialSoon') }}</label
      >
      <ElButton
        data-testid="refresh"
        :disabled="loading || busy || !!pending"
        @click="load"
        >{{ t('refresh') }}</ElButton
      >
    </div>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <template v-if="allowed">
      <ElTable :data="rows" :empty-text="t('credentialEmpty')">
        <ElTableColumn :label="t('credentialType')" width="140"
          ><template #default="{ row }">{{
            t(
              row.type === 'managed'
                ? 'credentialManaged'
                : 'credentialSigning',
            )
          }}</template></ElTableColumn
        >
        <ElTableColumn prop="id" :label="t('credentialId')" min-width="260" />
        <ElTableColumn :label="t('credentialVersion')" width="100"
          ><template #default="{ row }">{{
            row.version ?? row.id
          }}</template></ElTableColumn
        >
        <ElTableColumn :label="t('credentialStatus')" width="100"
          ><template #default="{ row }">{{
            row.status ? t(`credentialStates.${row.status}`) : ''
          }}</template></ElTableColumn
        >
        <ElTableColumn :label="t('credentialIssued')" min-width="180"
          ><template #default="{ row }">{{
            row.issuedAt ?? t('credentialUnknown')
          }}</template></ElTableColumn
        >
        <ElTableColumn :label="t('credentialExpiry')" min-width="180"
          ><template #default="{ row }">{{
            row.expiresAt ?? t('credentialUnknown')
          }}</template></ElTableColumn
        >
        <ElTableColumn width="220"
          ><template #default="{ row }"
            ><ElButton
              v-if="row.type === 'managed'"
              data-testid="revoke"
              :disabled="
                loading || busy || !!pending || row.status === 'revoked'
              "
              @click="ask(row.id)"
              >{{ t('credentialRevoke') }}</ElButton
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <p v-if="!services" class="muted">{{ t('credentialServices') }}</p>
    </template>
    <ActionConfirm
      :message="pending ? t('credentialConfirm') : undefined"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
<style scoped>
.credentials select {
  color: var(--auriga-text);
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  padding: 6px;
}
.credentials .label-row {
  margin-bottom: 1.25rem;
}
</style>
