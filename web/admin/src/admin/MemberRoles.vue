<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { ElButton, ElSelect, ElTable, ElTableColumn } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { rolesApi, type Role, type RoleBinding } from './rolesApi';
import { orgAdminApi, type Group } from './orgApi';
import { useAdminI18n } from './messages';
import { orgError, useOrgActions } from './useOrgActions';
import ActionConfirm from './ActionConfirm.vue';

const props = defineProps<{
  memberId: string;
  memberName: string;
  disabled: boolean;
}>();
const emit = defineEmits<{ locked: [value: boolean]; updated: [] }>();
const { t, locale } = useAdminI18n();
const access = useOrgActions('org.roles.read', 'org.roles.grant', t);
const { canRead, canWrite, permissions, error, pending, busy, locked } = access;
const loading = ref(false);
const ready = ref(false);
const roles = ref<Role[]>([]);
const bindings = ref<RoleBinding[]>([]);
const groups = ref<Group[]>([]);
const roleId = ref('');
const disabled = computed(
  () => props.disabled || loading.value || locked.value,
);
watch(
  () => loading.value || locked.value,
  (value) => emit('locked', value),
  { immediate: true },
);
function name(role: Role) {
  return (
    (locale.value.startsWith('zh')
      ? role.name.zh || role.name['zh-CN']
      : role.name[locale.value]) ||
    role.name.en ||
    role.key
  );
}
function direct(binding: RoleBinding) {
  return (
    binding.via === 'direct' &&
    binding.subject.type === 'member' &&
    binding.subject.id === props.memberId
  );
}
const options = computed(() =>
  roles.value.map((role) => ({
    value: role.id,
    label: `${name(role)} · ${t(role.builtin ? 'roleBuiltin' : 'roleCustom')}`,
    disabled:
      !role.permissions.every((p) => permissions.value.includes(p)) ||
      bindings.value.some((b) => direct(b) && b.roleId === role.id),
  })),
);
function roleName(binding: RoleBinding) {
  const role = roles.value.find((r) => r.id === binding.roleId);
  return role ? name(role) : binding.roleKey;
}
function source(binding: RoleBinding) {
  if (direct(binding)) return t('directBinding');
  return t('inheritedBinding', {
    group:
      groups.value.find((g) => g.id === binding.subject.id)?.name ||
      binding.subject.id,
  });
}
async function load() {
  if (disabled.value) return;
  loading.value = true;
  ready.value = false;
  roles.value = [];
  bindings.value = [];
  groups.value = [];
  roleId.value = '';
  error.value = '';
  try {
    if (!(await access.authorize())) return;
    const [catalog, current, sources] = await Promise.all([
      rolesApi.list(),
      rolesApi.memberBindings(props.memberId),
      permissions.value.includes('org.groups.read')
        ? orgAdminApi.groups()
        : Promise.resolve({ items: [] }),
    ]);
    if (!access.isAlive()) return;
    roles.value = catalog.items;
    bindings.value = current.items;
    groups.value = sources.items;
    ready.value = true;
  } catch (e) {
    if (access.isAlive()) {
      error.value = orgError(e, t);
      if (isApiError(e) && (e.status === 401 || e.status === 403))
        permissions.value = [];
    }
  } finally {
    if (access.isAlive()) loading.value = false;
  }
}
function grant() {
  if (disabled.value || !ready.value) return;
  const role = roles.value.find((r) => r.id === roleId.value);
  if (!role || options.value.find((o) => o.value === role.id)?.disabled) return;
  access.ask(
    t('memberGrantConfirm', {
      role: name(role),
      name: props.memberName,
      permissions: role.permissions.join(', ') || t('noneSelected'),
    }),
    async () => {
      await rolesApi.grant({
        roleId: role.id,
        subject: { type: 'member', id: props.memberId },
      });
      if (access.isAlive()) emit('updated');
    },
  );
}
function revoke(binding: RoleBinding) {
  if (disabled.value || !ready.value || !direct(binding)) return;
  access.ask(
    t('memberRevokeConfirm', {
      role: roleName(binding),
      name: props.memberName,
    }),
    async () => {
      await rolesApi.revoke(binding.id);
      if (access.isAlive()) emit('updated');
    },
  );
}
watch(
  () => props.disabled,
  (value) => {
    if (!value && !ready.value && !loading.value) void load();
  },
  { immediate: true },
);
</script>
<template>
  <section :aria-label="t('roleBindings')">
    <header class="head">
      <h3>{{ t('roleBindings') }}</h3>
      <ElButton :disabled="disabled" @click="load">{{ t('refresh') }}</ElButton>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <template v-if="canRead && ready">
      <ElTable :data="bindings" :empty-text="t('empty')">
        <ElTableColumn :label="t('roles')"
          ><template #default="{ row }">{{
            roleName(row as RoleBinding)
          }}</template></ElTableColumn
        >
        <ElTableColumn :label="t('source')"
          ><template #default="{ row }">{{
            source(row as RoleBinding)
          }}</template></ElTableColumn
        >
        <ElTableColumn v-if="canWrite" :label="t('revokeRole')"
          ><template #default="{ row }"
            ><ElButton
              v-if="direct(row as RoleBinding)"
              :disabled="disabled"
              @click="revoke(row as RoleBinding)"
              >{{ t('revokeRole') }}</ElButton
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <p class="muted">{{ t('memberRoleHint') }}</p>
      <div v-if="canWrite" class="actions">
        <ElSelect
          v-model="roleId"
          :aria-label="t('chooseMemberRole')"
          :placeholder="t('chooseMemberRole')"
          :options="options"
          :disabled="disabled"
          :no-data-text="t('empty')"
          style="width: 320px"
        />
        <ElButton :disabled="disabled || !roleId" @click="grant">{{
          t('grantRole')
        }}</ElButton>
      </div>
      <p v-else class="muted">{{ t('grantPermissionRequired') }}</p>
    </template>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </section>
</template>
