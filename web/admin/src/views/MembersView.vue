<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ElButton, ElInput, ElTable, ElTableColumn } from 'element-plus';
import type { Member } from '@auriga/shared';
import { orgApi } from '../api';
import { adminApi } from '../admin/api';
import { useManagement } from '../admin/useManagement';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import MemberRoles from '../admin/MemberRoles.vue';
import '../admin/management.css';

const { t, state } = useAdminI18n();
const q = ref('');
const roleLocked = ref(false);
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
  reset,
  ask,
  confirm,
  next,
  previous,
} = useManagement<Member>({
  read: 'org.members.read',
  write: 'org.members.write',
  list: (cursor) =>
    orgApi.listMembers({ q: q.value || undefined, limit: 50, cursor }),
  detail: (member) => adminApi.member(member.id),
  t,
});
const pageLocked = computed(() => locked.value || roleLocked.value);
watch(selected, () => {
  roleLocked.value = false;
});
async function rolesUpdated() {
  const refreshed = await load();
  notice.value =
    !refreshed || detailError.value
      ? t('refreshFailedAfterWrite')
      : t('memberRolesSaved');
}
onMounted(() => void load());
let timer: ReturnType<typeof setTimeout> | undefined;
watch(q, () => {
  if (timer) globalThis.clearTimeout(timer);
  reset();
  timer = setTimeout(() => void load(true), 250);
});
onBeforeUnmount(() => {
  if (timer) globalThis.clearTimeout(timer);
});
function setStatus(action: 'enable' | 'disable') {
  const member = selected.value;
  if (member)
    ask({
      message: t('memberConfirm', {
        name: member.displayName,
        action: t(action),
      }),
      run: () => adminApi.setMemberStatus(member.id, action),
      success: t('memberSaved'),
    });
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('members') }}</h1>
      <ElInput
        v-model="q"
        :disabled="roleLocked || busy || !!pending"
        clearable
        :placeholder="t('search')"
        :aria-label="t('search')"
        style="max-width: 280px"
      /><ElButton :disabled="pageLocked" @click="load()">{{
        t('refresh')
      }}</ElButton>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">
      {{ error }}
      <ElButton :disabled="pageLocked" @click="load()">{{
        t('retry')
      }}</ElButton>
    </p>
    <ElTable :data="items" :empty-text="loading || error ? ' ' : t('empty')">
      <ElTableColumn prop="displayName" :label="t('displayName')" />
      <ElTableColumn prop="email" :label="t('email')" />
      <ElTableColumn :label="t('type')"
        ><template #default="{ row }">{{
          state(row.memberType)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('status')"
        ><template #default="{ row }">{{
          state(row.status)
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('roles')"
        ><template #default="{ row }">{{
          row.roleKeys.join(', ')
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="pageLocked" @click="select(row as Member)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      >
    </ElTable>
    <div class="pager">
      <ElButton :disabled="pageLocked || !history.length" @click="previous">{{
        t('previous')
      }}</ElButton
      ><ElButton :disabled="pageLocked || !nextCursor" @click="next">{{
        t('next')
      }}</ElButton>
    </div>
    <p v-if="detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="detailError" class="error" role="alert">{{ detailError }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <section v-if="selected" class="details" :aria-label="t('details')">
      <header class="head">
        <h2>{{ selected.displayName }}</h2>
        <ElButton :disabled="pageLocked" @click="close">{{
          t('close')
        }}</ElButton>
      </header>
      <dl>
        <dt>{{ t('id') }}</dt>
        <dd>{{ selected.id }}</dd>
        <dt>{{ t('email') }}</dt>
        <dd>{{ selected.email || '—' }}</dd>
        <dt>{{ t('type') }}</dt>
        <dd>{{ state(selected.memberType) }}</dd>
        <dt>{{ t('status') }}</dt>
        <dd>{{ state(selected.status) }}</dd>
        <dt>{{ t('roles') }}</dt>
        <dd>{{ selected.roleKeys.join(', ') || '—' }}</dd>
        <dt>{{ t('groups') }}</dt>
        <dd>{{ selected.groupIds.join(', ') || '—' }}</dd>
        <dt>{{ t('created') }}</dt>
        <dd>{{ selected.createdAt }}</dd>
      </dl>
      <MemberRoles
        :key="selected.id"
        :member-id="selected.id"
        :member-name="selected.displayName"
        :disabled="locked"
        @locked="roleLocked = $event"
        @updated="rolesUpdated"
      />
      <p v-if="!canWrite" class="muted">{{ t('readonly') }}</p>
      <div v-if="canWrite" class="actions">
        <ElButton
          v-if="selected.status === 'active'"
          type="warning"
          :disabled="pageLocked"
          @click="setStatus('disable')"
          >{{ t('disable') }}</ElButton
        ><ElButton
          v-if="selected.status === 'disabled'"
          :disabled="pageLocked"
          @click="setStatus('enable')"
          >{{ t('enable') }}</ElButton
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
