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
  ElInput,
  ElForm,
  ElFormItem,
  ElDialog,
  ElSelect,
  ElCheckbox,
  ElCheckboxGroup,
} from 'element-plus';
import { ApiError, isApiError } from '@auriga/shared';
import { orgApi } from '../api';
import { adminApi } from '../admin/api';
import { orgAdminApi } from '../admin/orgApi';
import {
  rolesApi,
  type Role,
  type RoleInput,
  type Permission,
  type RoleBinding,
  type BindingSubject,
} from '../admin/rolesApi';
import { useOrgActions, orgError } from '../admin/useOrgActions';
import { hasPermission } from '../admin/permissions';
import { useCursorList } from '../admin/useCursorList';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t, locale } = useAdminI18n();
const access = useOrgActions('org.roles.read', 'org.roles.write', t);
const { permissions, canRead, canWrite, busy, locked, pending, error, notice } =
  access;
const canGrant = computed(
  () => canRead.value && hasPermission(permissions.value, 'org.roles.grant'),
);
const roles = shallowRef<Role[]>([]);
const catalog = shallowRef<Permission[]>([]);
const loading = ref(false);
const catalogLoading = ref(false);
const catalogError = ref('');
const catalogReady = ref(false);
const selected = shallowRef<Role>();
const detailLoading = ref(false);
const editor = ref(false);
const editTarget = shallowRef<Role>();
const key = ref('');
const nameEn = ref('');
const nameZh = ref('');
const descriptionEn = ref('');
const descriptionZh = ref('');
const editPermissions = ref<string[]>([]);
const subjectType = ref<'member' | 'group'>('member');
const subjectId = ref('');
const query = ref('');
let listVersion = 0;
let detailVersion = 0;
let catalogVersion = 0;
const disabled = computed(
  () => loading.value || detailLoading.value || locked.value,
);
function text(value: Role['name'] | undefined) {
  return locale.value.startsWith('zh')
    ? value?.zh || value?.['zh-CN'] || value?.en || ''
    : value?.[locale.value] || value?.en || '';
}
function forbidden() {
  return new ApiError({
    status: 403,
    code: 'common.forbidden',
    title: t('forbidden'),
  });
}
function failure(e: unknown) {
  const message = orgError(e, t);
  error.value = message;
  if (isApiError(e) && (e.status === 401 || e.status === 403))
    permissions.value = [];
  return message;
}
const bindings = useCursorList<RoleBinding>(
  (cursor) =>
    selected.value
      ? rolesApi.bindings(selected.value.id, cursor)
      : Promise.resolve({ items: [] }),
  failure,
);
const {
  items: bindingItems,
  loading: bindingLoading,
  error: bindingError,
  history: bindingHistory,
  nextCursor: bindingNext,
} = bindings;
const candidateRead = computed(() =>
  hasPermission(
    permissions.value,
    subjectType.value === 'member' ? 'org.members.read' : 'org.groups.read',
  ),
);
const candidates = useCursorList<{ id: string; label: string }>(
  async (cursor) => {
    if (!(await access.authorize())) return { items: [] };
    if (!canGrant.value || !candidateRead.value) return { items: [] };
    if (subjectType.value === 'group')
      return {
        items: (await orgAdminApi.groups()).items.map((group) => ({
          id: group.id,
          label: group.name,
        })),
      };
    const result = await orgApi.listMembers({
      q: query.value.trim() || undefined,
      limit: 50,
      cursor,
    });
    return {
      ...result,
      items: result.items
        .filter((member) => member.status === 'active')
        .map((member) => ({
          id: member.id,
          label: `${member.displayName} (${member.email || member.id})`,
        })),
    };
  },
  failure,
);
const {
  items: candidateItems,
  loading: candidateLoading,
  error: candidateError,
  history: candidateHistory,
  nextCursor: candidateNext,
} = candidates;
const candidateOptions = computed(() =>
  candidateItems.value.map((item) => ({ value: item.id, label: item.label })),
);
const subjectTypes = computed(() => [
  { value: 'member', label: t('memberSubject') },
  { value: 'group', label: t('groupSubject') },
]);
function clearCandidates() {
  candidates.clear();
  subjectId.value = '';
}
function clearDetail() {
  ++detailVersion;
  selected.value = undefined;
  detailLoading.value = false;
  bindings.clear();
  clearCandidates();
  editor.value = false;
  editTarget.value = undefined;
  key.value = '';
  nameEn.value = '';
  nameZh.value = '';
  descriptionEn.value = '';
  descriptionZh.value = '';
  editPermissions.value = [];
}
function clearAll() {
  ++listVersion;
  ++catalogVersion;
  roles.value = [];
  catalog.value = [];
  catalogReady.value = false;
  loading.value = false;
  catalogLoading.value = false;
  clearDetail();
}
watch(canRead, (allowed) => {
  if (!allowed) clearAll();
});
watch(canGrant, (allowed) => {
  if (!allowed) clearCandidates();
});
watch(candidateRead, (allowed) => {
  if (!allowed) clearCandidates();
});
watch(subjectType, clearCandidates);
watch(query, clearCandidates);
onBeforeUnmount(clearAll);
onMounted(() => void refresh());

async function refresh() {
  if (locked.value) return;
  const oldId = selected.value?.id;
  const request = ++listVersion;
  const catalogRequest = ++catalogVersion;
  clearDetail();
  roles.value = [];
  catalog.value = [];
  catalogReady.value = false;
  catalogError.value = '';
  error.value = '';
  loading.value = true;
  catalogLoading.value = true;
  try {
    if (
      !(await access.authorize()) ||
      !access.isAlive() ||
      request !== listVersion
    )
      return;
    const [roleResult, permissionResult] = await Promise.allSettled([
      rolesApi.list(),
      rolesApi.permissions(),
    ]);
    if (
      !access.isAlive() ||
      request !== listVersion ||
      catalogRequest !== catalogVersion
    )
      return;
    if (roleResult.status === 'fulfilled') roles.value = roleResult.value.items;
    else failure(roleResult.reason);
    if (permissionResult.status === 'fulfilled') {
      catalog.value = permissionResult.value.items;
      catalogReady.value = true;
    } else catalogError.value = failure(permissionResult.reason);
    if (!canRead.value) return;
    const previous = roles.value.find((role) => role.id === oldId);
    if (previous) await select(previous);
  } catch (e) {
    if (access.isAlive() && request === listVersion) failure(e);
  } finally {
    if (access.isAlive() && request === listVersion) {
      loading.value = false;
      catalogLoading.value = false;
    }
  }
}
async function retryCatalog() {
  if (disabled.value || catalogLoading.value) return;
  const request = ++catalogVersion;
  catalogLoading.value = true;
  catalogReady.value = false;
  catalog.value = [];
  catalogError.value = '';
  error.value = '';
  try {
    if (
      !(await access.authorize()) ||
      !access.isAlive() ||
      request !== catalogVersion
    )
      return;
    const result = await rolesApi.permissions();
    if (access.isAlive() && request === catalogVersion) {
      catalog.value = result.items;
      catalogReady.value = true;
    }
  } catch (e) {
    if (access.isAlive() && request === catalogVersion)
      catalogError.value = failure(e);
  } finally {
    if (access.isAlive() && request === catalogVersion)
      catalogLoading.value = false;
  }
}
async function select(role: Role) {
  const request = ++detailVersion;
  selected.value = undefined;
  detailLoading.value = true;
  editor.value = false;
  editTarget.value = undefined;
  bindings.clear();
  clearCandidates();
  error.value = '';
  try {
    const result = await rolesApi.get(role.id);
    if (!access.isAlive() || request !== detailVersion) return;
    selected.value = result;
    await bindings.load();
  } catch (e) {
    if (access.isAlive() && request === detailVersion) failure(e);
  } finally {
    if (access.isAlive() && request === detailVersion)
      detailLoading.value = false;
  }
}
function openEditor(role?: Role) {
  if (disabled.value || !canWrite.value || !catalogReady.value || role?.builtin)
    return;
  editTarget.value = role;
  key.value = role?.key || '';
  nameEn.value = role?.name.en || '';
  nameZh.value = role?.name.zh || role?.name['zh-CN'] || '';
  descriptionEn.value = role?.description?.en || '';
  descriptionZh.value =
    role?.description?.zh || role?.description?.['zh-CN'] || '';
  editPermissions.value = [...(role?.permissions || [])];
  error.value = '';
  editor.value = true;
}
const permissionOptions = computed(() => [
  ...catalog.value.map((item) => item.key),
  ...(editTarget.value?.permissions || []).filter(
    (permission) => !catalog.value.some((item) => item.key === permission),
  ),
]);
function sensitive(keys: string[]) {
  return (
    [
      ...catalog.value
        .filter((item) => item.sensitive && keys.includes(item.key))
        .map((item) => item.key),
      ...keys
        .filter((key) => !catalog.value.some((item) => item.key === key))
        .map((key) => `${key} (${t('unknownPermissionMetadata')})`),
    ].join(', ') || t('noneSelected')
  );
}
async function afterWrite(roleId?: string) {
  const request = ++listVersion;
  clearDetail();
  roles.value = [];
  loading.value = true;
  try {
    if (
      !(await access.authorize()) ||
      !access.isAlive() ||
      request !== listVersion
    )
      return false;
    const result = await rolesApi.list();
    if (!access.isAlive() || request !== listVersion) return false;
    roles.value = result.items;
    const role = roles.value.find((item) => item.id === roleId);
    if (roleId && !role)
      throw new ApiError({
        status: 404,
        code: 'common.not_found',
        title: t('failed'),
      });
    if (role) await select(role);
    return !error.value && !bindingError.value;
  } catch (e) {
    if (access.isAlive() && request === listVersion) failure(e);
    return false;
  } finally {
    if (access.isAlive() && request === listVersion) loading.value = false;
  }
}
async function finishWrite(message: string, roleId?: string) {
  if (!access.isAlive()) return;
  if (await afterWrite(roleId)) notice.value = message;
  else if (access.isAlive()) {
    notice.value = '';
    error.value = `${t('rolesReadbackFailed')} ${error.value}`;
  }
}
function save() {
  if (disabled.value || !catalogReady.value || !canWrite.value) return;
  const original = editTarget.value;
  if (
    !/^[a-z][a-z0-9_]{1,62}$/.test(original?.key || key.value) ||
    !nameEn.value.trim()
  ) {
    error.value = t('invalidRole');
    return;
  }
  const body: RoleInput = {
    key: original?.key || key.value,
    name: { en: nameEn.value.trim(), zh: nameZh.value.trim() },
    description: { en: descriptionEn.value, zh: descriptionZh.value },
    permissions: [...editPermissions.value],
  };
  access.ask(
    t('roleConfirm', {
      name: body.name.zh || body.name.en,
      permissions: body.permissions.join(', ') || t('noneSelected'),
      sensitive: sensitive(body.permissions),
    }),
    async () => {
      if (original) {
        const current = await rolesApi.get(original.id);
        if (!access.isAlive()) return;
        if (current.builtin)
          throw new ApiError({
            status: 409,
            code: 'org.role_builtin',
            title: t('roleBuiltin'),
          });
      }
      const result = original
        ? await rolesApi.update(original.id, body)
        : await rolesApi.create(body);
      await finishWrite(t('roleSaved'), result.id);
    },
  );
}
function deleteRole() {
  const role = selected.value;
  if (!role || role.builtin || disabled.value || !canWrite.value) return;
  access.ask(t('roleDeleteConfirm', { name: text(role.name) }), async () => {
    const current = await rolesApi.get(role.id);
    if (!access.isAlive()) return;
    if (current.builtin)
      throw new ApiError({
        status: 409,
        code: 'org.role_builtin',
        title: t('roleBuiltin'),
      });
    await rolesApi.delete(role.id);
    await finishWrite(t('roleDeleted'));
  });
}
async function loadSubjects() {
  if (
    disabled.value ||
    candidateLoading.value ||
    !canGrant.value ||
    !candidateRead.value
  )
    return;
  error.value = '';
  subjectId.value = '';
  candidates.clear();
  await candidates.load();
}
function grant() {
  const role = selected.value;
  const candidate = candidateItems.value.find(
    (item) => item.id === subjectId.value,
  );
  const type = subjectType.value;
  if (
    !role ||
    !candidate ||
    disabled.value ||
    !catalogReady.value ||
    !canGrant.value ||
    !candidateRead.value ||
    candidateLoading.value ||
    candidateError.value
  )
    return;
  access.ask(
    t('grantConfirm', {
      role: text(role.name),
      subject: candidate.label,
      sensitive: sensitive(role.permissions),
    }),
    async () => {
      if (
        !hasPermission(
          permissions.value,
          type === 'member' ? 'org.members.read' : 'org.groups.read',
        )
      )
        throw forbidden();
      const current = await rolesApi.get(role.id);
      if (!access.isAlive()) return;
      if (
        JSON.stringify([...current.permissions].sort()) !==
        JSON.stringify([...role.permissions].sort())
      )
        throw new ApiError({
          status: 409,
          code: 'common.conflict',
          title: t('conflict'),
        });
      if (type === 'member') {
        const member = await adminApi.member(candidate.id);
        if (member.status !== 'active')
          throw new ApiError({
            status: 409,
            code: 'org.conflict',
            title: t('activeMemberRequired'),
          });
      } else await orgAdminApi.group(candidate.id);
      if (!access.isAlive()) return;
      await rolesApi.grant({
        roleId: role.id,
        subject: { type, id: candidate.id },
      });
      await finishWrite(t('bindingSaved'), role.id);
    },
    'org.roles.grant',
  );
}
function revoke(binding: RoleBinding) {
  const role = selected.value;
  if (
    !role ||
    disabled.value ||
    !canGrant.value ||
    binding.subject.type === 'service_account' ||
    binding.roleId !== role.id
  )
    return;
  access.ask(
    t('revokeConfirm', {
      role: text(role.name),
      subject: `${binding.subject.type}: ${binding.subject.id}`,
    }),
    async () => {
      await rolesApi.revoke(binding.id);
      await finishWrite(t('bindingRevoked'), role.id);
    },
    'org.roles.grant',
  );
}
function subjectLabel(type: BindingSubject['type']) {
  return t(
    type === 'member'
      ? 'memberSubject'
      : type === 'group'
        ? 'groupSubject'
        : 'serviceSubject',
  );
}
async function retryBindings() {
  if (disabled.value || bindingLoading.value) return;
  error.value = '';
  await bindings.refresh();
}
async function confirm() {
  const bindingWrite = pending.value?.permission === 'org.roles.grant';
  const roleId = selected.value?.id;
  await access.confirm();
  if (!bindingWrite || !access.isAlive() || !error.value) return;
  const rejection = error.value;
  await afterWrite(roleId);
  if (access.isAlive())
    error.value = [rejection, error.value].filter(Boolean).join(' ');
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('roleTitle') }}</h1>
      <ElButton :disabled="disabled" @click="refresh">{{
        t('refresh')
      }}</ElButton
      ><ElButton
        v-if="canWrite"
        :disabled="disabled || !catalogReady"
        @click="openEditor()"
        >{{ t('createRole') }}</ElButton
      >
    </header>
    <p v-if="loading || detailLoading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert" class="error">
      {{ error }}
      <ElButton :disabled="disabled" @click="refresh">{{
        t('retry')
      }}</ElButton>
    </p>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <p v-if="canRead && !canWrite && !canGrant" class="muted">
      {{ t('readonly') }}
    </p>
    <ElTable :data="roles" :empty-text="loading || error ? ' ' : t('empty')"
      ><ElTableColumn prop="key" :label="t('roleKey')" /><ElTableColumn
        :label="t('name')"
        ><template #default="{ row }">{{
          text(row.name)
        }}</template></ElTableColumn
      ><ElTableColumn :label="t('roleType')"
        ><template #default="{ row }">{{
          t(row.builtin ? 'roleBuiltin' : 'roleCustom')
        }}</template></ElTableColumn
      ><ElTableColumn
        prop="bindingCount"
        :label="t('bindingCount')"
      /><ElTableColumn :label="t('details')"
        ><template #default="{ row }"
          ><ElButton :disabled="disabled" @click="select(row as Role)">{{
            t('details')
          }}</ElButton></template
        ></ElTableColumn
      ></ElTable
    >
    <section v-if="canRead" class="details">
      <h2>{{ t('permissionCatalog') }}</h2>
      <p v-if="catalogLoading" role="status">{{ t('loading') }}</p>
      <p v-if="catalogError" role="alert" class="error">
        {{ catalogError }}
        <ElButton
          :disabled="disabled || catalogLoading"
          @click="retryCatalog"
          >{{ t('reloadOptions') }}</ElButton
        >
      </p>
      <ElTable
        :data="catalog"
        max-height="320"
        :empty-text="catalogLoading || catalogError ? ' ' : t('empty')"
        ><ElTableColumn
          prop="key"
          :label="t('rolePermissions')"
        /><ElTableColumn :label="t('description')"
          ><template #default="{ row }">{{
            text(row.description)
          }}</template></ElTableColumn
        ><ElTableColumn :label="t('sensitivePermission')"
          ><template #default="{ row }">{{
            t(row.sensitive ? 'yes' : 'no')
          }}</template></ElTableColumn
        ></ElTable
      >
    </section>
    <section v-if="selected && canRead" class="details">
      <header class="head">
        <h2>{{ text(selected.name) }}</h2>
        <ElButton :disabled="disabled" @click="clearDetail">{{
          t('close')
        }}</ElButton>
      </header>
      <dl>
        <dt>{{ t('id') }}</dt>
        <dd>{{ selected.id }}</dd>
        <dt>{{ t('roleKey') }}</dt>
        <dd>{{ selected.key }}</dd>
        <dt>{{ t('description') }}</dt>
        <dd>{{ text(selected.description) || '—' }}</dd>
        <dt>{{ t('rolePermissions') }}</dt>
        <dd>{{ selected.permissions.join(', ') || t('noneSelected') }}</dd>
      </dl>
      <p v-if="selected.builtin" class="muted">{{ t('builtinReadOnly') }}</p>
      <div v-if="canWrite && !selected.builtin" class="actions">
        <ElButton
          :disabled="disabled || !catalogReady"
          @click="openEditor(selected)"
          >{{ t('editRole') }}</ElButton
        ><ElButton type="danger" :disabled="disabled" @click="deleteRole">{{
          t('deleteRole')
        }}</ElButton>
      </div>
      <h3>{{ t('roleBindings') }}</h3>
      <p>{{ t('serviceReadOnly') }}</p>
      <p v-if="!canGrant" class="muted">{{ t('grantPermissionRequired') }}</p>
      <p v-if="bindingLoading" role="status">{{ t('loading') }}</p>
      <p v-if="bindingError" role="alert" class="error">
        {{ bindingError }}
        <ElButton
          :disabled="disabled || bindingLoading"
          @click="retryBindings"
          >{{ t('retry') }}</ElButton
        >
      </p>
      <ElTable
        :data="bindingItems"
        :empty-text="bindingLoading || bindingError ? ' ' : t('empty')"
        ><ElTableColumn :label="t('subjectType')"
          ><template #default="{ row }">{{
            subjectLabel(row.subject.type)
          }}</template></ElTableColumn
        ><ElTableColumn :label="t('bindingSubject')"
          ><template #default="{ row }">{{
            row.subject.id
          }}</template></ElTableColumn
        ><ElTableColumn
          prop="grantedBy"
          :label="t('grantedBy')"
        /><ElTableColumn v-if="canGrant" :label="t('revokeRole')"
          ><template #default="{ row }"
            ><ElButton
              v-if="row.subject.type !== 'service_account'"
              :disabled="disabled || bindingLoading"
              @click="revoke(row as RoleBinding)"
              >{{ t('revokeRole') }}</ElButton
            ></template
          ></ElTableColumn
        ></ElTable
      >
      <div class="pager">
        <ElButton
          :disabled="disabled || bindingLoading || !bindingHistory.length"
          @click="bindings.previous"
          >{{ t('previous') }}</ElButton
        ><ElButton
          :disabled="disabled || bindingLoading || !bindingNext"
          @click="bindings.next"
          >{{ t('next') }}</ElButton
        >
      </div>
      <section v-if="canGrant" class="edit-form">
        <ElSelect
          v-model="subjectType"
          :aria-label="t('subjectType')"
          :options="subjectTypes"
          :disabled="disabled"
        />
        <p v-if="!candidateRead">{{ t('candidateReadRequired') }}</p>
        <template v-else
          ><ElForm
            :disabled="disabled || candidateLoading"
            @submit.prevent="loadSubjects"
            ><ElInput
              v-if="subjectType === 'member'"
              v-model="query"
              :aria-label="t('subjectSearch')"
              :placeholder="t('subjectSearch')"
            /><ElButton native-type="submit">{{
              t('loadSubjects')
            }}</ElButton></ElForm
          >
          <p v-if="candidateLoading" role="status">{{ t('loading') }}</p>
          <p v-if="candidateError" role="alert" class="error">
            {{ candidateError }}
          </p>
          <ElSelect
            v-model="subjectId"
            :aria-label="t('chooseSubject')"
            :placeholder="t('chooseSubject')"
            :no-data-text="t('empty')"
            :options="candidateOptions"
            :disabled="disabled || candidateLoading || !!candidateError"
            style="width: 100%"
          />
          <div class="pager">
            <ElButton
              :disabled="
                disabled || candidateLoading || !candidateHistory.length
              "
              @click="
                subjectId = '';
                candidates.previous();
              "
              >{{ t('previous') }}</ElButton
            ><ElButton
              :disabled="disabled || candidateLoading || !candidateNext"
              @click="
                subjectId = '';
                candidates.next();
              "
              >{{ t('next') }}</ElButton
            ><ElButton
              :disabled="
                disabled ||
                candidateLoading ||
                !catalogReady ||
                !subjectId ||
                !!candidateError
              "
              @click="grant"
              >{{ t('grantRole') }}</ElButton
            >
          </div></template
        >
      </section>
    </section>
    <ElDialog
      :model-value="editor && canWrite"
      :title="t(editTarget ? 'editRole' : 'createRole')"
      :close-on-click-modal="false"
      :close-on-press-escape="!locked"
      :show-close="!locked"
      @close="!locked && (editor = false)"
      ><p>{{ t('localizedRoleHint') }}</p>
      <p>{{ t('permissionLimit') }}</p>
      <p v-if="error" role="alert" class="error">{{ error }}</p>
      <ElForm
        label-position="top"
        :disabled="disabled || !catalogReady"
        @submit.prevent="save"
        ><ElFormItem :label="t('roleKey')"
          ><ElInput
            v-model="key"
            :disabled="!!editTarget"
            maxlength="63"
            :aria-label="t('roleKey')" /></ElFormItem
        ><ElFormItem :label="t('roleNameEn')"
          ><ElInput
            v-model="nameEn"
            :aria-label="t('roleNameEn')" /></ElFormItem
        ><ElFormItem :label="t('roleNameZh')"
          ><ElInput
            v-model="nameZh"
            :aria-label="t('roleNameZh')" /></ElFormItem
        ><ElFormItem :label="t('roleDescriptionEn')"
          ><ElInput
            v-model="descriptionEn"
            :aria-label="t('roleDescriptionEn')" /></ElFormItem
        ><ElFormItem :label="t('roleDescriptionZh')"
          ><ElInput
            v-model="descriptionZh"
            :aria-label="t('roleDescriptionZh')" /></ElFormItem
        ><ElFormItem :label="t('rolePermissions')"
          ><ElCheckboxGroup v-model="editPermissions" class="permission-options"
            ><ElCheckbox
              v-for="permission in permissionOptions"
              :key="permission"
              :value="permission"
              :disabled="
                !hasPermission(permissions, permission) &&
                !editPermissions.includes(permission)
              "
              >{{ permission }}
              {{
                catalog.find((item) => item.key === permission)?.sensitive
                  ? t('sensitivePermission')
                  : ''
              }}</ElCheckbox
            ></ElCheckboxGroup
          ></ElFormItem
        ><ElButton native-type="submit" type="primary">{{ t('save') }}</ElButton
        ><ElButton @click="editor = false">{{ t('cancel') }}</ElButton></ElForm
      ></ElDialog
    >
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
<style scoped>
.permission-options {
  display: flex;
  flex-direction: column;
  max-height: 320px;
  overflow: auto;
  width: 100%;
}
.permission-options .el-checkbox {
  margin-right: 0;
}
</style>
