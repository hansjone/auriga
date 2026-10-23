<script setup lang="ts">
import { onMounted } from 'vue';
import { ElButton, ElTable, ElTableColumn } from 'element-plus';
import { adminApi, type Instance } from '../admin/api';
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
  history,
  nextCursor,
  load,
  select,
  close,
  ask,
  confirm,
  next,
  previous,
} = useManagement<Instance>({
  read: 'workspace.instances.read',
  write: 'workspace.instances.admin',
  list: adminApi.instances,
  // The existing GET authorizes owners/admin operators, not read-only administrators.
  detail: (instance, writable) =>
    writable ? adminApi.instance(instance.id) : Promise.resolve(instance),
  t,
});
onMounted(() => void load());
function operate(action: 'start' | 'stop' | 'restart') {
  const instance = selected.value;
  if (instance)
    ask({
      message: t('instanceConfirm', { name: instance.name, action: t(action) }),
      run: () => adminApi.operateInstance(instance.id, action),
      success: t('accepted'),
    });
}
function toggleAlwaysOn() {
  const instance = selected.value;
  if (instance)
    ask({
      message: t('alwaysOnConfirm', {
        name: instance.name,
        value: t(instance.alwaysOn ? 'no' : 'yes'),
      }),
      run: () =>
        adminApi.updateInstance(instance, { alwaysOn: !instance.alwaysOn }),
      success: t('saved'),
    });
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('instances') }}</h1>
      <ElButton :disabled="locked" @click="load()">{{ t('refresh') }}</ElButton>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">
      {{ error }}
      <ElButton :disabled="locked" @click="load()">{{ t('retry') }}</ElButton>
    </p>
    <ElTable :data="items" :empty-text="loading || error ? ' ' : t('empty')">
      <ElTableColumn prop="name" :label="t('name')" />
      <ElTableColumn prop="ownerId" :label="t('owner')" />
      <ElTableColumn :label="t('node')"
        ><template #default="{ row }">{{
          row.node?.name || '—'
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('phase')"
        ><template #default="{ row }">{{
          state(row.phase)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="locked" @click="select(row as Instance)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      >
    </ElTable>
    <div class="pager">
      <ElButton :disabled="locked || !history.length" @click="previous">{{
        t('previous')
      }}</ElButton
      ><ElButton :disabled="locked || !nextCursor" @click="next">{{
        t('next')
      }}</ElButton>
    </div>
    <p v-if="detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="detailError" class="error" role="alert">{{ detailError }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <section v-if="selected" class="details" :aria-label="t('details')">
      <header class="head">
        <h2>{{ selected.name }}</h2>
        <ElButton :disabled="locked" @click="close">{{ t('close') }}</ElButton>
      </header>
      <p v-if="!canWrite" class="muted">
        {{ t('readonly') }} · {{ t('projection') }}
      </p>
      <dl>
        <dt>{{ t('id') }}</dt>
        <dd>{{ selected.id }}</dd>
        <dt>{{ t('owner') }}</dt>
        <dd>{{ selected.ownerId }}</dd>
        <dt>{{ t('node') }}</dt>
        <dd>{{ selected.node?.name || '—' }} {{ selected.node?.id }}</dd>
        <dt>{{ t('runtime') }}</dt>
        <dd>{{ selected.runtime }}</dd>
        <dt>{{ t('version') }}</dt>
        <dd>{{ selected.dshVersion }}</dd>
        <dt>{{ t('desired') }}</dt>
        <dd>{{ state(selected.desired) }}</dd>
        <dt>{{ t('phase') }}</dt>
        <dd>{{ state(selected.phase) }}</dd>
        <dt>{{ t('ready') }}</dt>
        <dd>{{ t(selected.ready ? 'yes' : 'no') }}</dd>
        <dt>{{ t('generation') }}</dt>
        <dd>{{ selected.generation }} / {{ selected.observedGeneration }}</dd>
        <dt>{{ t('pendingRestart') }}</dt>
        <dd>{{ t(selected.pendingRestart ? 'yes' : 'no') }}</dd>
        <dt>{{ t('reasons') }}</dt>
        <dd>{{ selected.pendingRestartReasons?.join(', ') || '—' }}</dd>
        <dt>{{ t('alwaysOn') }}</dt>
        <dd>{{ t(selected.alwaysOn ? 'yes' : 'no') }}</dd>
        <dt>{{ t('resources') }}</dt>
        <dd>
          {{ selected.resources.cpuMillis ?? '—' }} mCPU /
          {{ selected.resources.memoryMb ?? '—' }} MiB /
          {{ selected.resources.diskGb ?? '—' }} GiB
        </dd>
        <dt>{{ t('error') }}</dt>
        <dd>
          {{
            selected.error
              ? `${selected.error.code}: ${selected.error.message || ''}`
              : '—'
          }}
        </dd>
        <dt>{{ t('lastUpdated') }}</dt>
        <dd>{{ selected.updatedAt }}</dd>
      </dl>
      <div v-if="canWrite" class="actions">
        <ElButton
          :disabled="
            locked ||
            selected.desired === 'running' ||
            selected.phase === 'deleting'
          "
          @click="operate('start')"
          >{{ t('start') }}</ElButton
        >
        <ElButton
          :disabled="
            locked ||
            selected.desired === 'stopped' ||
            selected.phase === 'deleting'
          "
          @click="operate('stop')"
          >{{ t('stop') }}</ElButton
        >
        <ElButton
          :disabled="locked || selected.phase === 'deleting'"
          @click="operate('restart')"
          >{{ t('restart') }}</ElButton
        >
        <ElButton
          :disabled="locked || selected.phase === 'deleting'"
          @click="toggleAlwaysOn"
          >{{ t('alwaysOn') }}:
          {{ t(selected.alwaysOn ? 'no' : 'yes') }}</ElButton
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
