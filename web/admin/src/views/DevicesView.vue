<script setup lang="ts">
import { onMounted, ref } from 'vue';
import {
  ElButton,
  ElInput,
  ElSelect,
  ElOption,
  ElTable,
  ElTableColumn,
  ElDrawer,
} from 'element-plus';
import { devicesApi, type AdminDevice } from '../admin/devicesApi';
import { useManagement } from '../admin/useManagement';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';
const { t } = useAdminI18n();
const member = ref('');
const online = ref('');
const {
  items,
  selected,
  loading,
  detailLoading,
  busy,
  locked,
  error,
  detailError,
  notice,
  pending,
  canWrite,
  load,
  select,
  close,
  ask,
  confirm,
} = useManagement<AdminDevice>({
  read: 'iam.network.manage',
  write: 'iam.network.manage',
  list: () => devicesApi.list(member.value, online.value),
  detail: (device) => devicesApi.get(device.id),
  t,
});
const status = (d: AdminDevice) =>
  t(d.status === 'revoked' ? 'devicesRevoked' : 'devicesActive');
const mode = (d: AdminDevice) =>
  t(
    d.accessMode === 'native_tx'
      ? 'devicesNative'
      : d.accessMode === 'manual'
        ? 'devicesManual'
        : 'unknown',
  );
const connection = (d: AdminDevice) =>
  t(
    d.online === 'online'
      ? 'devicesOnlineValue'
      : d.online === 'offline'
        ? 'devicesOfflineValue'
        : 'devicesUnknown',
  );
function revoke() {
  const device = selected.value;
  if (device)
    ask({
      message: t('devicesRevokeConfirm', { name: device.name }),
      run: () => devicesApi.revoke(device.id),
      success: t('devicesSaved'),
    });
}
onMounted(() => void load());
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('devicesTitle') }}</h1>
      <ElInput
        v-model="member"
        :disabled="locked"
        :placeholder="t('devicesMemberFilter')"
        :aria-label="t('devicesMemberFilter')"
        clearable
        style="max-width: 260px"
        @keyup.enter="load(true)"
      />
      <ElSelect
        v-model="online"
        :disabled="locked"
        :aria-label="t('devicesOnline')"
        style="width: 160px"
      >
        <ElOption :label="t('devicesAll')" value="" />
        <ElOption :label="t('devicesOnlineValue')" value="online" />
        <ElOption :label="t('devicesOfflineValue')" value="offline" />
        <ElOption :label="t('devicesUnknown')" value="unknown" />
      </ElSelect>
      <ElButton :disabled="locked" @click="load(true)">{{
        t('refresh')
      }}</ElButton>
    </header>
    <p class="muted">{{ t('devicesTelemetry') }}</p>
    <p v-if="loading || detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <ElTable :data="items" :empty-text="loading || error ? ' ' : t('empty')">
      <ElTableColumn prop="name" :label="t('devicesName')" />
      <ElTableColumn prop="memberName" :label="t('devicesMember')" />
      <ElTableColumn :label="t('devicesPlatform')"
        ><template #default="{ row }">{{
          row.platform || t('devicesUnknown')
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('devicesVersion')"
        ><template #default="{ row }">{{
          row.clientVersion || t('devicesUnknown')
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('devicesMode')"
        ><template #default="{ row }">{{
          mode(row as AdminDevice)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('devicesOnline')"
        ><template #default="{ row }">{{
          connection(row as AdminDevice)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('devicesActivity')"
        ><template #default="{ row }">{{
          row.lastSeenAt || t('unknown')
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('status')"
        ><template #default="{ row }">{{
          status(row as AdminDevice)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="locked" @click="select(row as AdminDevice)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      >
    </ElTable>
    <p v-if="detailError" class="error" role="alert">{{ detailError }}</p>
    <ElDrawer
      :model-value="!!selected"
      :title="selected?.name"
      :size="560"
      :before-close="() => close()"
      :close-on-click-modal="!locked"
      :close-on-press-escape="!locked"
      :show-close="!locked"
      append-to-body
    >
      <section v-if="selected" class="management details">
        <dl>
          <dt>{{ t('id') }}</dt>
          <dd>{{ selected.id }}</dd>
          <dt>{{ t('devicesMember') }}</dt>
          <dd>{{ selected.memberName }} ({{ selected.accountId }})</dd>
          <dt>{{ t('devicesPlatform') }}</dt>
          <dd>{{ selected.platform || t('devicesUnknown') }}</dd>
          <dt>{{ t('devicesVersion') }}</dt>
          <dd>{{ selected.clientVersion || t('devicesUnknown') }}</dd>
          <dt>{{ t('devicesMode') }}</dt>
          <dd>{{ mode(selected) }}</dd>
          <dt>{{ t('devicesOnline') }}</dt>
          <dd>{{ connection(selected) }}</dd>
          <dt>{{ t('devicesHeartbeat') }}</dt>
          <dd>{{ t('devicesUnknown') }}</dd>
          <dt>{{ t('devicesActivity') }}</dt>
          <dd>{{ selected.lastSeenAt || t('unknown') }}</dd>
          <dt>{{ t('devicesNode') }}</dt>
          <dd>{{ selected.networkNode || '?' }}</dd>
          <dt>{{ t('status') }}</dt>
          <dd>{{ status(selected) }}</dd>
          <dt>{{ t('created') }}</dt>
          <dd>{{ selected.createdAt || '?' }}</dd>
          <dt v-if="selected.revokedAt">{{ t('devicesRevokedAt') }}</dt>
          <dd v-if="selected.revokedAt">{{ selected.revokedAt }}</dd>
        </dl>
        <p v-if="notice" class="notice" role="status">{{ notice }}</p>
        <p v-if="detailError" class="error" role="alert">{{ detailError }}</p>
        <ElButton
          v-if="canWrite && selected.status !== 'revoked'"
          type="danger"
          :disabled="locked"
          @click="revoke"
          >{{ t('devicesRevoke') }}</ElButton
        >
      </section>
    </ElDrawer>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
