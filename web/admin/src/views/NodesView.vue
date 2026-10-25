<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue';
import { ApiError, isApiError } from '@auriga/shared';
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
import JoinMaterials from './nodes/JoinMaterials.vue';
import NodeDiagnostics from './nodes/NodeDiagnostics.vue';
import { nodesApi } from '../admin/nodesApi';
import { retirementBlock, nodeProgress } from './nodes/state';
import { useNodesI18n } from './nodes/messages';
import '../admin/management.css';

const { t, state } = useAdminI18n();
const { t: nt, failure } = useNodesI18n();
const retirementError = ref('');
const accessDenied = ref(false);
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
  canWrite: managementCanWrite,
  load,
  select,
  close,
  ask,
  confirm: confirmAction,
} = useManagement<Node>({
  read: 'workspace.nodes.read',
  write: 'workspace.nodes.admin',
  list: adminApi.nodes,
  detail: (node) => adminApi.node(node.id),
  t: (key, params) =>
    key === 'failed' && retirementError.value
      ? retirementError.value
      : t(key, params ?? {}),
});
const canWrite = computed(
  () => managementCanWrite.value && !accessDenied.value,
);
const name = ref('');
const schedulable = ref(false);
const labels = ref<{ key: string; value: string }[]>([]);
watch(
  () => selected.value?.id,
  () => {
    const node = selected.value;
    name.value = node?.name || '';
    schedulable.value = node?.schedulable ?? false;
    labels.value = Object.entries(node?.labels || {}).map(([key, value]) => ({
      key,
      value,
    }));
  },
);
const refreshKey = ref(0);
const joinOpen = ref(false);
const pollError = ref('');
let alive = true;
let pollGeneration = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
function forbidden() {
  return new ApiError({
    status: 403,
    code: 'common.forbidden',
    title: t('forbidden'),
  });
}
function revokeAccess() {
  accessDenied.value = true;
  joinOpen.value = false;
  pending.value = undefined;
  selected.value = undefined;
  items.value = [];
  ++pollGeneration;
  globalThis.clearTimeout(timer);
}
async function writeNode(run: () => Promise<Node>) {
  if (!alive || !canWrite.value) throw forbidden();
  const result = await run();
  if (!alive || !canWrite.value) throw forbidden();
  return result;
}
function confirm() {
  if (canWrite.value) return confirmAction();
}
function schedule() {
  if (alive) timer = setTimeout(() => void poll(), 10_000);
}
async function poll() {
  if (locked.value || document.visibilityState === 'hidden') {
    schedule();
    return;
  }
  const generation = pollGeneration;
  const current = selected.value;
  try {
    const membership = await adminApi.membership();
    if (!alive || generation !== pollGeneration) return;
    if (
      !membership.permissions.includes('workspace.nodes.read') ||
      (canWrite.value &&
        !membership.permissions.includes('workspace.nodes.admin'))
    ) {
      throw forbidden();
    }
    const result = await adminApi.nodes();
    const detail = current ? await adminApi.node(current.id) : undefined;
    if (!alive || generation !== pollGeneration) return;
    if (!locked.value && selected.value === current) {
      items.value = result.items;
      if (detail) selected.value = detail;
      ++refreshKey.value;
    }
    schedule();
  } catch (e) {
    if (alive && generation === pollGeneration) {
      if (isApiError(e) && (e.status === 401 || e.status === 403))
        revokeAccess();
      pollError.value = `${nt('pollFailed')} ${failure(e)}`;
    }
  }
}
async function refresh() {
  const generation = ++pollGeneration;
  globalThis.clearTimeout(timer);
  pollError.value = '';
  if ((await load()) && alive && generation === pollGeneration) {
    accessDenied.value = false;
    ++refreshKey.value;
    schedule();
  }
}
onMounted(() => void refresh());
onBeforeUnmount(() => {
  alive = false;
  ++pollGeneration;
  globalThis.clearTimeout(timer);
});
function retire() {
  const node = selected.value;
  if (!canWrite.value || !node || retirementBlock(node)) return;
  retirementError.value = '';
  ask({
    message: nt('retireConfirm', { name: node.name }),
    run: () =>
      writeNode(async () => {
        const fresh = await adminApi.node(node.id);
        if (!alive || selected.value?.id !== node.id || !canWrite.value)
          throw new Error('cancelled');
        const reason = retirementBlock(fresh);
        if (reason) {
          retirementError.value = nt(reason);
          throw new Error('node-state-changed');
        }
        await nodesApi.retire(node.id);
        return { ...fresh, status: 'revoked', schedulable: false } as Node;
      }),
    success: nt('retired'),
  });
}
function save() {
  retirementError.value = '';
  const node = selected.value;
  if (!canWrite.value || !node) return;
  const keys = labels.value.map((label) => label.key.trim());
  if (keys.some((key) => !key) || new Set(keys).size !== keys.length) {
    detailError.value = t('invalidLabels');
    return;
  }
  if (!name.value.trim() || name.value.length > 64) return;
  const body = {
    name: name.value.trim(),
    schedulable: node.status === 'draining' ? false : schedulable.value,
    labels: Object.fromEntries(
      labels.value.map((label, index) => [keys[index], label.value]),
    ),
  };
  ask({
    message: t('nodeConfirm', { name: node.name }),
    run: () => writeNode(() => adminApi.updateNode(node.id, body)),
    success: t('saved'),
  });
}
function drain() {
  retirementError.value = '';
  const node = selected.value;
  if (canWrite.value && node)
    ask({
      message: t('drainConfirm', { name: node.name }),
      run: () => writeNode(() => adminApi.drainNode(node.id)),
      success: t('accepted'),
    });
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('nodes') }}</h1>
      <ElButton
        v-if="canWrite"
        :disabled="locked"
        :aria-expanded="joinOpen"
        aria-controls="node-join-materials"
        @click="joinOpen = !joinOpen"
        >{{ nt('materials') }}</ElButton
      >
      <ElButton :disabled="locked" @click="refresh()">{{
        t('refresh')
      }}</ElButton>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">
      {{ error }}
      <ElButton :disabled="locked" @click="refresh()">{{
        t('retry')
      }}</ElButton>
    </p>
    <p>{{ nt('polling') }}</p>
    <p v-if="pollError" role="alert">{{ pollError }}</p>
    <ElTable :data="items" :empty-text="loading || error ? ' ' : t('empty')">
      <ElTableColumn prop="name" :label="t('name')" />
      <ElTableColumn :label="t('status')"
        ><template #default="{ row }">{{
          nt(nodeProgress(row as Node))
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
      <NodeDiagnostics
        :node="selected"
        :can-write="canWrite"
        :locked="locked || !!pollError"
        @retire="retire"
      />
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
    <JoinMaterials
      v-if="joinOpen && canWrite"
      id="node-join-materials"
      :can-write="canWrite"
      :refresh-key="refreshKey"
      :locked="locked"
    />
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
