<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import {
  ElButton,
  ElTable,
  ElTableColumn,
  ElInput,
  ElSwitch,
  ElForm,
  ElFormItem,
} from 'element-plus';
import { adminApi, type Node } from '../admin/api';
import { useManagement } from '../admin/useManagement';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t, state } = useAdminI18n();
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
} = useManagement<Node>({
  read: 'workspace.nodes.read',
  write: 'workspace.nodes.admin',
  list: adminApi.nodes,
  detail: (node) => adminApi.node(node.id),
  t,
});
const name = ref('');
const schedulable = ref(false);
const labels = ref<{ key: string; value: string }[]>([]);
watch(selected, (node) => {
  name.value = node?.name || '';
  schedulable.value = node?.schedulable ?? false;
  labels.value = Object.entries(node?.labels || {}).map(([key, value]) => ({
    key,
    value,
  }));
});
onMounted(() => void load());
function save() {
  const node = selected.value;
  if (!node) return;
  const keys = labels.value.map((label) => label.key.trim());
  if (keys.some((key) => !key) || new Set(keys).size !== keys.length) {
    detailError.value = t('invalidLabels');
    return;
  }
  if (!name.value.trim() || name.value.length > 64) return;
  const body = {
    name: name.value.trim(),
    schedulable: schedulable.value,
    labels: Object.fromEntries(
      labels.value.map((label, index) => [keys[index], label.value]),
    ),
  };
  ask({
    message: t('nodeConfirm', { name: node.name }),
    run: () => adminApi.updateNode(node.id, body),
    success: t('saved'),
  });
}
function drain() {
  const node = selected.value;
  if (node)
    ask({
      message: t('drainConfirm', { name: node.name }),
      run: () => adminApi.drainNode(node.id),
      success: t('accepted'),
    });
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('nodes') }}</h1>
      <ElButton :disabled="locked" @click="load()">{{ t('refresh') }}</ElButton>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">
      {{ error }}
      <ElButton :disabled="locked" @click="load()">{{ t('retry') }}</ElButton>
    </p>
    <ElTable :data="items" :empty-text="loading || error ? ' ' : t('empty')">
      <ElTableColumn prop="name" :label="t('name')" />
      <ElTableColumn :label="t('status')"
        ><template #default="{ row }">{{
          state(row.status)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('schedulable')"
        ><template #default="{ row }">{{
          t(row.schedulable ? 'yes' : 'no')
        }}</template></ElTableColumn
      >
      <ElTableColumn prop="agentVersion" :label="t('agentVersion')" />
      <ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="locked" @click="select(row as Node)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      >
    </ElTable>
    <p v-if="detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="detailError" class="error" role="alert">{{ detailError }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <section v-if="selected" class="details" :aria-label="t('details')">
      <header class="head">
        <h2>{{ selected.name }}</h2>
        <ElButton :disabled="locked" @click="close">{{ t('close') }}</ElButton>
      </header>
      <dl>
        <dt>{{ t('id') }}</dt>
        <dd>{{ selected.id }}</dd>
        <dt>{{ t('status') }}</dt>
        <dd>{{ state(selected.status) }}</dd>
        <dt>{{ t('hostname') }}</dt>
        <dd>{{ selected.hostname }}</dd>
        <dt>{{ t('platform') }}</dt>
        <dd>{{ selected.os }} / {{ selected.arch }}</dd>
        <dt>{{ t('drivers') }}</dt>
        <dd>{{ selected.drivers.join(', ') }}</dd>
        <dt>{{ t('capacity') }}</dt>
        <dd>
          {{ selected.capacity.cpuMillis ?? '—' }} /
          {{ selected.capacity.memoryMb ?? '—' }} /
          {{ selected.capacity.diskGb ?? '—' }} /
          {{ selected.capacity.maxInstances ?? '—' }}
        </dd>
        <dt>{{ t('usage') }}</dt>
        <dd>
          {{ selected.usage?.instancesRunning ?? '—' }} /
          {{ selected.usage?.instancesTotal ?? '—' }}
        </dd>
        <dt>{{ t('lastSeen') }}</dt>
        <dd>{{ selected.lastSeenAt || '—' }}</dd>
      </dl>
      <p v-if="!canWrite" class="muted">{{ t('readonly') }}</p>
      <ElForm
        label-position="top"
        class="edit-form"
        :disabled="!canWrite || locked || selected.status === 'revoked'"
        @submit.prevent="save"
      >
        <ElFormItem :label="t('name')"
          ><ElInput v-model="name" maxlength="64" :aria-label="t('name')"
        /></ElFormItem>
        <ElFormItem :label="t('schedulable')"
          ><ElSwitch
            v-model="schedulable"
            :disabled="selected.status === 'draining'"
            :aria-label="t('schedulable')"
        /></ElFormItem>
        <ElFormItem :label="t('labels')"
          ><div>
            <div
              v-for="(label, index) in labels"
              :key="index"
              class="label-row"
            >
              <ElInput
                v-model="label.key"
                :aria-label="t('labelKey')"
                :placeholder="t('labelKey')"
              />
              <ElInput
                v-model="label.value"
                :aria-label="t('labelValue')"
                :placeholder="t('labelValue')"
              />
              <ElButton @click="labels.splice(index, 1)">{{
                t('remove')
              }}</ElButton>
            </div>
            <ElButton @click="labels.push({ key: '', value: '' })">{{
              t('addLabel')
            }}</ElButton>
          </div></ElFormItem
        >
        <ElButton
          v-if="canWrite"
          native-type="submit"
          type="primary"
          :disabled="!name.trim()"
          >{{ t('save') }}</ElButton
        >
      </ElForm>
      <div v-if="canWrite" class="actions">
        <ElButton
          type="warning"
          :disabled="
            locked ||
            selected.status === 'draining' ||
            selected.status === 'revoked'
          "
          @click="drain"
          >{{ t('drain') }}</ElButton
        >
      </div>
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
