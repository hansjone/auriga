<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import {
  ElButton,
  ElTable,
  ElTableColumn,
  ElSelect,
  ElOption,
  ElInput,
  ElForm,
  ElFormItem,
  ElDialog,
} from 'element-plus';
import type { Member } from '@auriga/shared';
import { ApiError } from '@auriga/shared';
import { orgApi } from '../api';
import { useAdminI18n } from '../admin/messages';
import { orgAdminApi, type Group, type GroupInput } from '../admin/orgApi';
import { useCursorList } from '../admin/useCursorList';
import { useOrgActions, orgError } from '../admin/useOrgActions';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t } = useAdminI18n();
const access = useOrgActions('org.groups.read', 'org.groups.write', t);
const { busy, error, notice, pending, canRead, canWrite, locked, permissions } =
  access;
const list = useCursorList<Group>(
  async () => {
    if (!(await access.authorize())) return { items: [] };
    return orgAdminApi.groups();
  },
  (e) => orgError(e, t),
);
const { items, loading, error: listError } = list;
const selected = shallowRef<Group>();
const detailLoading = ref(false);
let detailVersion = 0;
const editor = ref(false);
const editTarget = shallowRef<Group>();
const name = ref('');
const description = ref('');
const parentId = ref('');
const memberQuery = ref('');
const candidateId = ref('');
const canPickMembers = computed(() =>
  permissions.value.includes('org.members.read'),
);
const writableGroup = computed(
  () => canWrite.value && selected.value?.source === 'manual',
);
const disabled = computed(
  () => locked.value || loading.value || detailLoading.value,
);
const members = useCursorList<Member>(
  (cursor) =>
    selected.value
      ? orgAdminApi.groupMembers(selected.value.id, cursor)
      : Promise.resolve({ items: [] }),
  (e) => orgError(e, t),
);
const candidates = useCursorList<Member>(
  (cursor) =>
    orgApi.listMembers({
      q: memberQuery.value.trim() || undefined,
      limit: 50,
      cursor,
    }),
  (e) => orgError(e, t),
);
const {
  items: memberItems,
  loading: membersLoading,
  error: membersError,
  history: memberHistory,
  nextCursor: memberNext,
} = members;
const {
  items: candidateItems,
  loading: candidatesLoading,
  error: candidatesError,
  history: candidateHistory,
  nextCursor: candidateNext,
} = candidates;
function clearDetail() {
  ++detailVersion;
  selected.value = undefined;
  detailLoading.value = false;
  members.clear();
  candidates.clear();
  candidateId.value = '';
  editor.value = false;
}
onBeforeUnmount(clearDetail);
watch(canRead, (allowed) => {
  if (!allowed) clearDetail();
});
watch(canPickMembers, (allowed) => {
  if (!allowed) {
    candidates.clear();
    candidateId.value = '';
  }
});
watch(memberQuery, () => {
  candidates.clear();
  candidateId.value = '';
});
onMounted(() => void list.load());
async function select(group: Group) {
  const version = ++detailVersion;
  selected.value = undefined;
  detailLoading.value = true;
  members.clear();
  candidates.clear();
  candidateId.value = '';
  memberQuery.value = '';
  editor.value = false;
  error.value = '';
  try {
    const result = await orgAdminApi.group(group.id);
    if (!access.isAlive() || version !== detailVersion) return;
    selected.value = result;
    await members.load();
  } catch (e) {
    if (access.isAlive() && version === detailVersion)
      error.value = orgError(e, t);
  } finally {
    if (access.isAlive() && version === detailVersion)
      detailLoading.value = false;
  }
}
async function refresh() {
  if (locked.value) return;
  const groupId = selected.value?.id;
  clearDetail();
  if (await list.refresh()) {
    const group = items.value.find((item) => item.id === groupId);
    if (group) await select(group);
  }
}
function openEditor(group?: Group) {
  if (disabled.value || !canWrite.value || (group && group.source !== 'manual'))
    return;
  editTarget.value = group;
  name.value = group?.name || '';
  description.value = group?.description || '';
  parentId.value = group?.parentId || '';
  error.value = '';
  editor.value = true;
}
function validParent(candidate: Group) {
  if (!editTarget.value) return true;
  const visited = new Set<string>();
  let current: Group | undefined = candidate;
  while (current) {
    if (current.id === editTarget.value.id || visited.has(current.id))
      return false;
    visited.add(current.id);
    current = items.value.find((group) => group.id === current?.parentId);
  }
  return true;
}
const parents = computed(() => items.value.filter(validParent));
async function afterWrite(id?: string) {
  clearDetail();
  if (!(await list.refresh())) {
    if (access.isAlive()) error.value = t('refreshFailedAfterWrite');
    return;
  }
  if (!access.isAlive()) return;
  const group = items.value.find((item) => item.id === id);
  if (group) await select(group);
}
function save() {
  if (
    disabled.value ||
    listError.value ||
    !name.value.trim() ||
    name.value.length > 100 ||
    description.value.length > 500
  )
    return;
  const parent = items.value.find((group) => group.id === parentId.value);
  if (parentId.value && (!parent || !validParent(parent))) {
    error.value = t('invalidParent');
    return;
  }
  const target = editTarget.value;
  // parentId:null is not in the current contract and is ignored by the existing update implementation.
  const body: GroupInput = {
    name: name.value.trim(),
    description: description.value,
    ...(parentId.value ? { parentId: parentId.value } : {}),
  };
  access.ask(t('groupConfirm', { name: body.name }), async () => {
    const result = target
      ? await orgAdminApi.updateGroup(target.id, body)
      : await orgAdminApi.createGroup(body);
    if (!access.isAlive()) return;
    notice.value = t('groupSaved');
    editor.value = false;
    await afterWrite(result.id);
  });
}
function removeGroup() {
  const group = selected.value;
  if (!group || !writableGroup.value || disabled.value) return;
  access.ask(t('deleteGroupConfirm', { name: group.name }), async () => {
    await orgAdminApi.deleteGroup(group.id);
    if (!access.isAlive()) return;
    notice.value = t('groupDeleted');
    await afterWrite();
  });
}
async function searchMembers() {
  if (disabled.value || candidatesLoading.value || !canPickMembers.value)
    return;
  candidateId.value = '';
  candidates.clear();
  await candidates.load();
}
function addMember() {
  const group = selected.value;
  const member = candidateItems.value.find(
    (item) => item.id === candidateId.value,
  );
  if (
    !group ||
    !member ||
    !canPickMembers.value ||
    !writableGroup.value ||
    disabled.value ||
    candidatesLoading.value ||
    candidatesError.value
  )
    return;
  access.ask(
    t('addMemberConfirm', { name: member.displayName, group: group.name }),
    async () => {
      if (!canPickMembers.value)
        throw new ApiError({
          status: 403,
          code: 'common.forbidden',
          title: t('forbidden'),
        });
      await orgAdminApi.addMember(group.id, member.id);
      if (!access.isAlive()) return;
      notice.value = t('membershipSaved');
      await afterWrite(group.id);
    },
  );
}
function removeMember(member: Member) {
  const group = selected.value;
  if (!group || !writableGroup.value || disabled.value) return;
  access.ask(
    t('removeMemberConfirm', { name: member.displayName, group: group.name }),
    async () => {
      await orgAdminApi.removeMember(group.id, member.id);
      if (!access.isAlive()) return;
      notice.value = t('membershipSaved');
      await afterWrite(group.id);
    },
  );
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('groupTitle') }}</h1>
      <ElButton :disabled="disabled" @click="refresh">{{
        t('refresh')
      }}</ElButton
      ><ElButton
        v-if="canWrite"
        :disabled="disabled || !!listError"
        @click="openEditor()"
        >{{ t('newGroup') }}</ElButton
      >
    </header>
    <p v-if="loading || detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="listError" class="error" role="alert">
      {{ listError }}
      <ElButton :disabled="disabled" @click="refresh">{{
        t('retry')
      }}</ElButton>
    </p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="canRead && !canWrite" class="muted">{{ t('readonly') }}</p>
    <ElTable
      :data="items"
      :empty-text="loading || listError ? ' ' : t('empty')"
    >
      <ElTableColumn prop="name" :label="t('name')" /><ElTableColumn
        prop="description"
        :label="t('description')"
      /><ElTableColumn prop="memberCount" :label="t('memberCount')" />
      <ElTableColumn :label="t('source')"
        ><template #default="{ row }">{{
          row.source === 'manual'
            ? t('manual')
            : row.source === 'mapped'
              ? t('mapped')
              : row.source
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="disabled" @click="select(row as Group)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      >
    </ElTable>
    <section v-if="selected" class="details">
      <header class="head">
        <h2>{{ selected.name }}</h2>
        <ElButton :disabled="disabled" @click="clearDetail">{{
          t('close')
        }}</ElButton>
      </header>
      <dl>
        <dt>{{ t('id') }}</dt>
        <dd>{{ selected.id }}</dd>
        <dt>{{ t('description') }}</dt>
        <dd>{{ selected.description || '—' }}</dd>
        <dt>{{ t('parentGroup') }}</dt>
        <dd>
          {{
            items.find((group) => group.id === selected?.parentId)?.name ||
            selected.parentId ||
            t('noParent')
          }}
        </dd>
        <dt>{{ t('memberCount') }}</dt>
        <dd>{{ selected.memberCount }}</dd>
      </dl>
      <p v-if="selected.source === 'mapped'" class="muted">
        {{ t('mappedWarning') }}
      </p>
      <div v-if="writableGroup" class="actions">
        <ElButton
          :disabled="disabled || !!listError"
          @click="openEditor(selected)"
          >{{ t('editGroup') }}</ElButton
        ><ElButton type="danger" :disabled="disabled" @click="removeGroup">{{
          t('deleteGroup')
        }}</ElButton>
      </div>
      <h3>{{ t('groupMembers') }}</h3>
      <p v-if="membersLoading" role="status">{{ t('loading') }}</p>
      <p v-if="membersError" class="error" role="alert">
        {{ membersError }}
        <ElButton
          :disabled="disabled || membersLoading"
          @click="members.refresh"
          >{{ t('retry') }}</ElButton
        >
      </p>
      <ElTable
        :data="memberItems"
        :empty-text="membersLoading || membersError ? ' ' : t('empty')"
        ><ElTableColumn
          prop="displayName"
          :label="t('displayName')"
        /><ElTableColumn prop="email" :label="t('email')" /><ElTableColumn
          v-if="writableGroup"
          :label="t('removeMember')"
          ><template #default="{ row }"
            ><ElButton
              :disabled="disabled || membersLoading"
              @click="removeMember(row as Member)"
              >{{ t('removeMember') }}</ElButton
            ></template
          ></ElTableColumn
        ></ElTable
      >
      <div class="pager">
        <ElButton
          :disabled="disabled || membersLoading || !memberHistory.length"
          @click="members.previous"
          >{{ t('previous') }}</ElButton
        ><ElButton
          :disabled="disabled || membersLoading || !memberNext"
          @click="members.next"
          >{{ t('next') }}</ElButton
        >
      </div>
      <section v-if="writableGroup" class="edit-form">
        <h3>{{ t('memberSelection') }}</h3>
        <p v-if="!canPickMembers" class="muted">
          {{ t('memberReadRequired') }}
        </p>
        <template v-else>
          <ElForm
            :disabled="disabled || candidatesLoading"
            @submit.prevent="searchMembers"
            ><ElInput
              v-model="memberQuery"
              :aria-label="t('search')"
              :placeholder="t('search')"
            /><ElButton native-type="submit">{{
              t('searchMembers')
            }}</ElButton></ElForm
          >
          <p v-if="candidatesLoading" role="status">{{ t('loading') }}</p>
          <p v-if="candidatesError" class="error" role="alert">
            {{ t('optionsFailed') }} {{ candidatesError }}
            <ElButton
              :disabled="disabled || candidatesLoading"
              @click="searchMembers"
              >{{ t('reloadOptions') }}</ElButton
            >
          </p>
          <ElSelect
            v-model="candidateId"
            :aria-label="t('selectMember')"
            :no-data-text="t('empty')"
            :placeholder="t('selectMember')"
            :disabled="disabled || candidatesLoading || !!candidatesError"
            style="width: 100%"
            ><ElOption
              v-for="member in candidateItems"
              :key="member.id"
              :value="member.id"
              :disabled="member.groupIds.includes(selected.id)"
              :label="`${member.displayName} (${member.email || member.id})`"
          /></ElSelect>
          <div class="pager">
            <ElButton
              :disabled="
                disabled || candidatesLoading || !candidateHistory.length
              "
              @click="
                candidateId = '';
                candidates.previous();
              "
              >{{ t('previous') }}</ElButton
            ><ElButton
              :disabled="disabled || candidatesLoading || !candidateNext"
              @click="
                candidateId = '';
                candidates.next();
              "
              >{{ t('next') }}</ElButton
            ><ElButton
              :disabled="
                disabled ||
                candidatesLoading ||
                !!candidatesError ||
                !candidateId
              "
              @click="addMember"
              >{{ t('addMember') }}</ElButton
            >
          </div>
        </template>
      </section>
    </section>
    <ElDialog
      :model-value="editor && canWrite"
      :title="t(editTarget ? 'editGroup' : 'newGroup')"
      :close-on-click-modal="false"
      :show-close="!locked"
      :close-on-press-escape="!locked"
      @close="!locked && (editor = false)"
    >
      <p v-if="editTarget?.parentId">{{ t('parentLimit') }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <ElForm
        :disabled="disabled || !!listError"
        label-position="top"
        @submit.prevent="save"
      >
        <ElFormItem :label="t('name')"
          ><ElInput v-model="name" maxlength="100" :aria-label="t('name')"
        /></ElFormItem>
        <ElFormItem :label="t('description')"
          ><ElInput
            v-model="description"
            maxlength="500"
            type="textarea"
            :aria-label="t('description')"
        /></ElFormItem>
        <ElFormItem :label="t('parentGroup')"
          ><ElSelect
            v-model="parentId"
            :aria-label="t('parentGroup')"
            :no-data-text="t('empty')"
            style="width: 100%"
            ><ElOption
              v-if="!editTarget?.parentId"
              value=""
              :label="t('noParent')" /><ElOption
              v-for="parent in parents"
              :key="parent.id"
              :value="parent.id"
              :label="parent.name" /></ElSelect
        ></ElFormItem>
        <ElButton
          native-type="submit"
          type="primary"
          :disabled="!name.trim()"
          >{{ t('save') }}</ElButton
        ><ElButton @click="editor = false">{{ t('cancel') }}</ElButton>
      </ElForm>
    </ElDialog>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
