<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton, ElInput, ElTable, ElTableColumn } from 'element-plus';
import type { Member } from '@auriga/shared';
import { isApiError, mapProblemMessage } from '@auriga/shared';
import { orgApi } from '../api';

const { t } = useI18n();
const q = ref('');
const items = ref<Member[]>([]);
const nextCursor = ref<string | undefined>();
const cursors = ref<string[]>([]);
const error = ref('');
const loading = ref(false);

async function load(cursor?: string) {
  loading.value = true;
  error.value = '';
  try {
    const res = await orgApi.listMembers({
      q: q.value || undefined,
      limit: 2,
      cursor,
    });
    items.value = res.items;
    nextCursor.value = res.nextCursor;
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
}

onMounted(() => void load());

let timer: ReturnType<typeof setTimeout> | undefined;
watch(q, () => {
  if (timer) globalThis.clearTimeout(timer);
  timer = globalThis.setTimeout(() => {
    cursors.value = [];
    void load();
  }, 250);
});

function next() {
  if (!nextCursor.value) return;
  const last = items.value[items.value.length - 1]?.id;
  if (last) cursors.value.push(last);
  void load(nextCursor.value);
}

function prev() {
  cursors.value.pop();
  const cursor = cursors.value[cursors.value.length - 1];
  void load(cursor);
}
</script>

<template>
  <div class="members">
    <header class="head">
      <h1>{{ t('members.title') }}</h1>
      <ElInput
        v-model="q"
        clearable
        :placeholder="t('members.search')"
        style="max-width: 280px"
      />
    </header>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <ElTable v-loading="loading" :data="items" empty-text="">
      <ElTableColumn prop="displayName" :label="t('members.displayName')" />
      <ElTableColumn prop="email" :label="t('members.email')" />
      <ElTableColumn prop="memberType" :label="t('members.type')" width="120" />
      <ElTableColumn prop="status" :label="t('members.status')" width="120" />
      <ElTableColumn :label="t('members.roles')">
        <template #default="{ row }">
          {{ (row.roleKeys || []).join(', ') }}
        </template>
      </ElTableColumn>
    </ElTable>
    <p v-if="!loading && items.length === 0" class="muted">
      {{ t('members.empty') }}
    </p>
    <div class="pager">
      <ElButton :disabled="cursors.length === 0" @click="prev">{{
        t('members.prev')
      }}</ElButton>
      <ElButton :disabled="!nextCursor" @click="next">{{
        t('members.next')
      }}</ElButton>
    </div>
  </div>
</template>

<style scoped>
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 1rem;
  gap: 1rem;
}
h1 {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
}
.pager {
  display: flex;
  gap: 0.5rem;
  margin-top: 1rem;
}
.muted {
  color: var(--auriga-muted);
}
.error {
  color: #d27a7a;
}
</style>
