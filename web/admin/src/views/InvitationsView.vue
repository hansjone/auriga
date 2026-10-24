<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  ElButton,
  ElTable,
  ElTableColumn,
  ElSelect,
  ElInput,
  ElForm,
  ElFormItem,
  ElDialog,
} from 'element-plus';
import { isApiError } from '@auriga/shared';
import { useAdminI18n } from '../admin/messages';
import {
  orgAdminApi,
  type Invitation,
  type InvitationInput,
  type InvitationStatus,
} from '../admin/orgApi';
import { useCursorList } from '../admin/useCursorList';
import { useOrgActions, orgError } from '../admin/useOrgActions';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t } = useAdminI18n();
const access = useOrgActions(
  'org.invitations.read',
  'org.invitations.write',
  t,
);
const { busy, error, notice, pending, canRead, canWrite, locked } = access;
const status = ref<InvitationStatus | ''>('');
const statuses: InvitationStatus[] = ['active', 'used', 'expired', 'revoked'];
const statusOptions = computed(() => [
  { value: '', label: t('allStatuses') },
  ...statuses.map((value) => ({
    value,
    label: t('invitationStates.' + value),
  })),
]);
const list = useCursorList<Invitation>(
  async (cursor) => {
    if (!(await access.authorize())) return { items: [] };
    return orgAdminApi.invitations(status.value || undefined, cursor);
  },
  (e) => {
    const message = orgError(e, t);
    if (isApiError(e) && (e.status === 401 || e.status === 403)) {
      error.value = message;
      access.permissions.value = [];
    }
    return message;
  },
);
const { items, loading, error: listError, nextCursor, history } = list;
const creating = ref(false);
const email = ref('');
const displayName = ref('');
const code = ref('');
const invitationUrl = ref('');
const copyNotice = ref('');
let resultVersion = 0;
function clearCode() {
  ++resultVersion;
  code.value = '';
  invitationUrl.value = '';
  copyNotice.value = '';
}
onBeforeUnmount(clearCode);
watch(canRead, (allowed) => {
  if (!allowed) {
    clearCode();
    list.clear(true);
  }
});
onMounted(() => void list.load());
watch(status, () => {
  clearCode();
  creating.value = false;
  list.clear();
  void list.load();
});
async function refresh() {
  if (locked.value) return;
  clearCode();
  creating.value = false;
  await list.refresh();
}
async function retryList() {
  if (locked.value || loading.value) return;
  error.value = '';
  await list.refresh();
}
function newInvitation() {
  clearCode();
  email.value = '';
  displayName.value = '';
  error.value = '';
  creating.value = true;
}
function create() {
  if (loading.value || locked.value) return;
  const normalizedEmail = email.value.trim();
  if (normalizedEmail && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail)) {
    error.value = t('invalidEmail');
    return;
  }
  // Explicitly avoid inherited/default role or group grants. Providers remain unrestricted.
  const body: InvitationInput = {
    memberType: 'member',
    maxUses: 1,
    roleIds: [],
    groupIds: [],
    providerIds: [],
    ...(normalizedEmail ? { email: normalizedEmail } : {}),
    ...(displayName.value.trim()
      ? { displayName: displayName.value.trim() }
      : {}),
  };
  access.ask(
    t('invitationConfirm', { email: normalizedEmail || t('anyEmail') }),
    async () => {
      clearCode();
      const request = resultVersion;
      let result: Awaited<ReturnType<typeof orgAdminApi.createInvitation>>;
      try {
        result = await orgAdminApi.createInvitation(body);
      } catch (e) {
        if (!access.isAlive() || request !== resultVersion) return;
        if (isApiError(e) && e.status === 503) {
          error.value = `${t('invitationSourceUnavailable')} ${orgError(e, t)}`;
          return;
        }
        throw e;
      }
      if (!access.isAlive() || request !== resultVersion) return;
      // Keep the exact API URL in component memory; do not derive it from the admin origin.
      let valid =
        typeof result?.code === 'string' &&
        result.code.length > 0 &&
        typeof result?.url === 'string';
      try {
        const parsed = new URL(result.url);
        valid =
          valid &&
          (parsed.protocol === 'http:' || parsed.protocol === 'https:');
      } catch {
        valid = false;
      }
      if (valid) {
        code.value = result.code;
        invitationUrl.value = result.url;
        notice.value = t('invitationCreated');
      } else error.value = t('invitationResultInvalid');
      copyNotice.value = '';
      creating.value = false;
      email.value = '';
      displayName.value = '';
      list.clear();
      if (!(await list.load()) && access.isAlive())
        error.value = t('refreshFailedAfterWrite');
    },
  );
}
function revoke(invitation: Invitation) {
  clearCode();
  access.ask(t('revokeConfirm', { id: invitation.id }), async () => {
    await orgAdminApi.revokeInvitation(invitation.id);
    if (!access.isAlive()) return;
    notice.value = t('invitationRevoked');
    if (!(await list.refresh()) && access.isAlive())
      error.value = t('refreshFailedAfterWrite');
  });
}
async function copyResult(kind: 'code' | 'url') {
  const value = kind === 'code' ? code.value : invitationUrl.value;
  if (!value) return;
  const request = resultVersion;
  try {
    await globalThis.navigator.clipboard.writeText(value);
    if (access.isAlive() && request === resultVersion)
      copyNotice.value = t('copied');
  } catch {
    if (access.isAlive() && request === resultVersion)
      copyNotice.value = t('copyFailed');
  }
}
</script>
<template>
  <div class="management">
    <header class="head">
      <h1>{{ t('invitationTitle') }}</h1>
      <ElSelect
        v-model="status"
        :disabled="locked || !!code"
        :aria-label="t('status')"
        :no-data-text="t('empty')"
        style="width: 180px"
        :options="statusOptions"
      />
      <ElButton :disabled="locked || loading" @click="refresh">{{
        t('refresh')
      }}</ElButton>
      <ElButton
        v-if="canWrite"
        :disabled="locked || loading"
        @click="newInvitation"
        >{{ t('newInvitation') }}</ElButton
      >
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="listError" role="alert" class="error">
      {{ listError }}
      <ElButton :disabled="locked || loading" @click="retryList">{{
        t('retry')
      }}</ElButton>
    </p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <p v-if="canRead && !canWrite" class="muted">{{ t('readonly') }}</p>
    <ElTable
      :data="items"
      :empty-text="loading || listError ? ' ' : t('empty')"
    >
      <ElTableColumn prop="id" :label="t('id')" /><ElTableColumn
        prop="email"
        :label="t('email')"
      /><ElTableColumn prop="displayName" :label="t('displayName')" />
      <ElTableColumn :label="t('status')"
        ><template #default="{ row }">{{
          statuses.includes(row.status)
            ? t(`invitationStates.${row.status}`)
            : row.status
        }}</template></ElTableColumn
      >
      <ElTableColumn :label="t('uses')"
        ><template #default="{ row }"
          >{{ row.uses }} / {{ row.maxUses }}</template
        ></ElTableColumn
      >
      <ElTableColumn prop="expiresAt" :label="t('expires')" />
      <ElTableColumn v-if="canWrite" :label="t('revokeInvitation')"
        ><template #default="{ row }"
          ><ElButton
            :disabled="locked || loading || row.status !== 'active'"
            @click="revoke(row as Invitation)"
            >{{ t('revokeInvitation') }}</ElButton
          ></template
        ></ElTableColumn
      >
    </ElTable>
    <div class="pager">
      <ElButton
        :disabled="locked || loading || !history.length"
        @click="list.previous"
        >{{ t('previous') }}</ElButton
      ><ElButton
        :disabled="locked || loading || !nextCursor"
        @click="list.next"
        >{{ t('next') }}</ElButton
      >
    </div>
    <section v-if="creating && canWrite" class="details">
      <h2>{{ t('newInvitation') }}</h2>
      <p>{{ t('invitationDefaults') }}</p>
      <ElForm
        :disabled="locked || loading"
        label-position="top"
        @submit.prevent="create"
      >
        <ElFormItem :label="t('email')"
          ><ElInput
            v-model="email"
            type="email"
            :aria-label="t('email')"
            :placeholder="t('optional')"
        /></ElFormItem>
        <ElFormItem :label="t('displayName')"
          ><ElInput
            v-model="displayName"
            :aria-label="t('displayName')"
            :placeholder="t('optional')"
        /></ElFormItem>
        <ElButton native-type="submit" type="primary">{{
          t('newInvitation')
        }}</ElButton
        ><ElButton @click="creating = false">{{ t('cancel') }}</ElButton>
      </ElForm>
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="!busy && (pending = undefined)"
    />
    <ElDialog
      :model-value="!!code"
      :title="t('oneTimeCode')"
      :close-on-click-modal="false"
      @close="clearCode"
    >
      <p>{{ t('codeWarning') }}</p>
      <p v-if="loading" role="status">{{ t('loading') }}</p>
      <p v-if="listError" class="error" role="alert">
        {{ t('refreshFailedAfterWrite') }} {{ listError }}
      </p>
      <p>{{ t('invitationLink') }}</p>
      <pre class="invitation-code" data-testid="invitation-url">{{
        invitationUrl
      }}</pre>
      <p>{{ t('oneTimeCode') }}</p>
      <pre class="invitation-code" data-testid="invitation-code">{{
        code
      }}</pre>
      <p v-if="copyNotice" role="status">{{ copyNotice }}</p>
      <template #footer
        ><ElButton
          v-if="listError"
          :disabled="locked || loading"
          @click="retryList"
          >{{ t('retry') }}</ElButton
        ><ElButton @click="copyResult('url')">{{
          t('copyInvitationLink')
        }}</ElButton
        ><ElButton @click="copyResult('code')">{{ t('copyCode') }}</ElButton
        ><ElButton @click="clearCode">{{ t('clearCode') }}</ElButton></template
      >
    </ElDialog>
  </div>
</template>
<style scoped>
.invitation-code {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  user-select: all;
}
</style>
