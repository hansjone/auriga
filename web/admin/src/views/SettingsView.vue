<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  shallowRef,
} from 'vue';
import {
  ElButton,
  ElInput,
  ElForm,
  ElFormItem,
  ElCheckbox,
} from 'element-plus';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../admin/api';
import {
  settingsApi,
  policyEtag,
  type Tenant,
  type Policy,
  type TenantPatch,
  type WorkspaceValue,
} from '../admin/settingsApi';
import {
  workspaceFields,
  workspaceDraft,
  booleanField,
  validWorkspaceDraft,
  unsafeWorkspace,
  workspaceValue,
  workspaceChanges,
} from '../admin/settingsForm';
import { orgError } from '../admin/useOrgActions';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t } = useAdminI18n();
type Section = 'team' | 'workspace';
const permissions = ref<string[]>([]);
const tenant = shallowRef<Tenant>();
const policy = shallowRef<Policy>();
const etag = ref<string | null>(null);
const teamDraft = reactive({ displayName: '', locale: '' });
const draft = reactive(workspaceDraft(undefined));
const state = reactive({
  team: { loading: false, error: '', notice: '', readback: false },
  workspace: { loading: false, error: '', notice: '', readback: false },
});
const absent = ref(false);
const conflict = ref(false);
const busy = ref(false);
const pending = shallowRef<{
  section: Section;
  message: string;
  run: () => Promise<unknown>;
}>();
const locked = computed(() => busy.value || !!pending.value);
const canReadPolicy = computed(() =>
  permissions.value.includes('org.policies.read'),
);
const canWriteTeam = computed(() =>
  permissions.value.includes('org.tenant.write'),
);
const canWritePolicy = computed(
  () => canReadPolicy.value && permissions.value.includes('org.policies.write'),
);
const unsafe = computed(
  () =>
    !!policy.value &&
    (policy.value.key !== 'workspace' || unsafeWorkspace(policy.value.value)),
);
const teamLocked = computed(
  () =>
    locked.value ||
    state.team.loading ||
    state.team.readback ||
    !canWriteTeam.value ||
    !tenant.value,
);
const policyLocked = computed(
  () =>
    locked.value ||
    state.workspace.loading ||
    state.workspace.readback ||
    !canWritePolicy.value ||
    !policy.value ||
    unsafe.value ||
    !policyEtag(etag.value) ||
    conflict.value,
);
let alive = true;
let authEpoch = 0;
const versions = { team: 0, workspace: 0 };
let authRequest: Promise<boolean> | undefined;

function clear(section: Section) {
  ++versions[section];
  state[section].loading = false;
  state[section].readback = false;
  state[section].notice = '';
  if (pending.value?.section === section && !busy.value)
    pending.value = undefined;
  if (section === 'team') {
    tenant.value = undefined;
    Object.assign(teamDraft, { displayName: '', locale: '' });
  } else {
    policy.value = undefined;
    etag.value = null;
    absent.value = false;
    conflict.value = false;
    Object.assign(draft, workspaceDraft(undefined));
  }
}
function handleError(section: Section, error: unknown) {
  if (!alive) return;
  state[section].error = orgError(error, t);
  if (isApiError(error) && error.status === 401) {
    ++authEpoch;
    permissions.value = [];
    clear('team');
    clear('workspace');
  } else if (isApiError(error) && error.status === 403) {
    if (section === 'workspace') {
      permissions.value = permissions.value.filter(
        (key) => !key.startsWith('org.policies.'),
      );
      clear('workspace');
    } else {
      permissions.value = permissions.value.filter(
        (key) => key !== 'org.tenant.write',
      );
    }
  }
}
async function authorize() {
  if (authRequest) return authRequest;
  const epoch = authEpoch;
  authRequest = (async () => {
    try {
      const membership = await adminApi.membership();
      if (!alive || epoch !== authEpoch) return false;
      permissions.value = membership.permissions;
      if (!canReadPolicy.value) clear('workspace');
      return true;
    } catch (e) {
      if (alive) {
        permissions.value = [];
        clear('workspace');
        if (isApiError(e) && (e.status === 401 || e.status === 403))
          clear('team');
        handleError('team', e);
        state.workspace.error = orgError(e, t);
      }
      return false;
    } finally {
      authRequest = undefined;
    }
  })();
  return authRequest;
}
async function read(section: Section) {
  if (!alive || (section === 'workspace' && !canReadPolicy.value)) return false;
  const version = ++versions[section];
  const current = () => alive && versions[section] === version;
  state[section].loading = true;
  state[section].error = '';
  try {
    if (section === 'team') {
      const result = await settingsApi.tenant();
      if (!current()) return false;
      tenant.value = result;
      Object.assign(teamDraft, {
        displayName: result.displayName,
        locale: result.locale,
      });
    } else {
      const result = await settingsApi.workspace();
      if (!current()) return false;
      policy.value = result.data;
      etag.value = result.etag;
      Object.assign(draft, workspaceDraft(result.data.value));
      absent.value = false;
      conflict.value = false;
    }
    state[section].readback = false;
    return true;
  } catch (e) {
    if (current()) {
      if (
        section === 'workspace' &&
        isApiError(e) &&
        e.status === 404 &&
        e.problem.code === 'org.not_found'
      ) {
        clear('workspace');
        absent.value = true;
      } else handleError(section, e);
    }
    return false;
  } finally {
    if (current()) state[section].loading = false;
  }
}
async function reload(section: Section) {
  if (locked.value || state[section].loading) return;
  // Explicit reload discards this section only. A readback retry never replays a write.
  const readback = state[section].readback;
  clear(section);
  state[section].readback = readback;
  if (readback) state[section].notice = t('settingsReadback');
  state[section].error = '';
  state[section].loading = true;
  if (await authorize()) await read(section);
  if (alive) state[section].loading = false;
}
function askTeam() {
  if (teamLocked.value || !tenant.value) return;
  const body: TenantPatch = {};
  if (teamDraft.displayName !== tenant.value.displayName)
    body.displayName = teamDraft.displayName;
  if (teamDraft.locale !== tenant.value.locale) body.locale = teamDraft.locale;
  if (!Object.keys(body).length) return;
  if (teamDraft.displayName.length > 100) {
    state.team.error = t('settingsInvalid');
    return;
  }
  const changes = Object.entries(body)
    .map(
      ([key, value]) =>
        `${t(key === 'displayName' ? 'settingsName' : 'settingsLocale')}: ${JSON.stringify(tenant.value![key as keyof Tenant])} → ${JSON.stringify(value)}`,
    )
    .join('; ');
  pending.value = {
    section: 'team',
    message: t('settingsChanges', { changes, scope: t('settingsTeamScope') }),
    run: () => settingsApi.patchTenant(body),
  };
}
function askWorkspace() {
  if (policyLocked.value || !policy.value || !policyEtag(etag.value)) return;
  if (!validWorkspaceDraft(draft)) {
    state.workspace.error = t('settingsInvalid');
    return;
  }
  const original = policy.value.value as WorkspaceValue;
  const value = workspaceValue(original, draft);
  const keys = workspaceChanges(original, value);
  if (!keys.length) return;
  const display = (v: WorkspaceValue, key: keyof WorkspaceValue) =>
    Object.hasOwn(v, key) ? JSON.stringify(v[key]) : t('settingsUnset');
  const changes = keys
    .map(
      (key) => `${t(key)}: ${display(original, key)} → ${display(value, key)}`,
    )
    .join('; ');
  const originalEtag = etag.value;
  pending.value = {
    section: 'workspace',
    message: t('settingsChanges', {
      changes,
      scope: t('settingsWorkspaceScope'),
    }),
    run: () => settingsApi.putWorkspace(value, originalEtag),
  };
}
async function confirm() {
  const action = pending.value;
  if (!action || busy.value) return;
  busy.value = true;
  const section = action.section;
  state[section].error = '';
  state[section].notice = '';
  try {
    if (!(await authorize()) || !alive) return;
    if (section === 'team' ? !canWriteTeam.value : !canWritePolicy.value) {
      state[section].error = t('forbidden');
      return;
    }
    await action.run();
    if (!alive) return;
    state[section].readback = true;
    if (await read(section)) state[section].notice = t('saved');
    else if (alive) state[section].notice = t('settingsReadback');
  } catch (e) {
    if (alive) {
      if (section === 'workspace' && isApiError(e) && e.status === 412) {
        conflict.value = true;
        state.workspace.error = t('settingsConflict');
      } else handleError(section, e);
    }
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
onMounted(async () => {
  state.team.loading = true;
  state.workspace.loading = true;
  if (await authorize())
    await Promise.allSettled([read('team'), read('workspace')]);
  if (alive) {
    state.team.loading = false;
    state.workspace.loading = false;
  }
});
onBeforeUnmount(() => {
  alive = false;
  clear('team');
  clear('workspace');
  pending.value = undefined;
});
</script>

<template>
  <section class="management">
    <h1>{{ t('systemSettings') }}</h1>
    <section class="details" data-testid="team-section">
      <h2>{{ t('settingsTeam') }}</h2>
      <p class="muted">{{ t('settingsTeamScope') }}</p>
      <p v-if="state.team.loading" role="status">{{ t('loading') }}</p>
      <p v-if="state.team.error" class="error" role="alert">
        {{ state.team.error }}
      </p>
      <p v-if="state.team.notice" class="notice" role="status">
        {{ state.team.notice }}
      </p>
      <p v-if="!canWriteTeam">{{ t('readonly') }}</p>
      <template v-if="tenant">
        <dl>
          <dt>{{ t('id') }}</dt>
          <dd>{{ tenant.id }}</dd>
          <dt>slug</dt>
          <dd>{{ tenant.slug }}</dd>
          <dt>{{ t('settingsHosts') }}</dt>
          <dd>{{ tenant.hosts.join(', ') || t('empty') }}</dd>
          <dt>{{ t('settingsCreated') }}</dt>
          <dd>{{ tenant.createdAt }}</dd>
        </dl>
        <ElForm
          label-position="top"
          class="edit-form"
          @submit.prevent="askTeam"
        >
          <ElFormItem :label="t('settingsName')"
            ><ElInput
              v-model="teamDraft.displayName"
              :disabled="teamLocked"
              data-testid="team-name"
          /></ElFormItem>
          <ElFormItem :label="t('settingsLocale')"
            ><ElInput
              v-model="teamDraft.locale"
              :disabled="teamLocked"
              data-testid="team-locale"
          /></ElFormItem>
          <ElButton
            :disabled="teamLocked"
            data-testid="save-team"
            @click="askTeam"
            >{{ t('save') }}</ElButton
          >
        </ElForm>
      </template>
      <ElButton
        :disabled="locked || state.team.loading"
        data-testid="reload-team"
        @click="reload('team')"
        >{{
          t(state.team.readback ? 'settingsReadRetry' : 'settingsReload')
        }}</ElButton
      >
    </section>
    <section class="details" data-testid="workspace-section">
      <h2>{{ t('settingsWorkspace') }}</h2>
      <p class="muted">{{ t('settingsWorkspaceScope') }}</p>
      <p v-if="state.workspace.loading" role="status">{{ t('loading') }}</p>
      <p v-if="state.workspace.error" class="error" role="alert">
        {{ state.workspace.error }}
      </p>
      <p v-if="state.workspace.notice" class="notice" role="status">
        {{ state.workspace.notice }}
      </p>
      <p v-if="!canReadPolicy">{{ t('forbidden') }}</p>
      <p v-else-if="!canWritePolicy">{{ t('readonly') }}</p>
      <p v-if="absent">{{ t('settingsMissing') }}</p>
      <template v-if="policy && canReadPolicy">
        <p v-if="!policyEtag(etag)">{{ t('settingsEtag') }}</p>
        <p v-if="unsafe" role="alert">{{ t('settingsUnsafe') }}</p>
        <ElForm
          label-position="top"
          class="edit-form"
          @submit.prevent="askWorkspace"
        >
          <ElFormItem v-for="key in workspaceFields" :key="key" :label="t(key)">
            <ElCheckbox
              v-model="draft[key].enabled"
              :disabled="policyLocked"
              :data-testid="`present-${key}`"
              >{{ t('settingsPresent') }}</ElCheckbox
            >
            <select
              v-if="booleanField(key)"
              v-model="draft[key].value"
              :aria-label="t(key)"
              :disabled="policyLocked || !draft[key].enabled"
              :data-testid="`field-${key}`"
            >
              <option value="" disabled>{{ t('settingsUnset') }}</option>
              <option value="true">{{ t('yes') }}</option>
              <option value="false">{{ t('no') }}</option>
            </select>
            <ElInput
              v-else
              v-model="draft[key].value"
              :disabled="policyLocked || !draft[key].enabled"
              :data-testid="`field-${key}`"
            />
          </ElFormItem>
          <ElButton
            :disabled="policyLocked"
            data-testid="save-workspace"
            @click="askWorkspace"
            >{{ t('save') }}</ElButton
          >
        </ElForm>
        <details>
          <summary>{{ t('settingsRaw') }}</summary>
          <pre>{{ JSON.stringify(policy.value, null, 2) }}</pre>
        </details>
      </template>
      <ElButton
        :disabled="locked || state.workspace.loading"
        data-testid="reload-workspace"
        @click="reload('workspace')"
        >{{
          t(state.workspace.readback ? 'settingsReadRetry' : 'settingsReload')
        }}</ElButton
      >
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @cancel="!busy && (pending = undefined)"
      @confirm="confirm"
    />
  </section>
</template>
