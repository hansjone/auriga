<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import {
  ElButton,
  ElInput,
  ElDrawer,
  ElDialog,
  ElForm,
  ElFormItem,
} from 'element-plus';
import { isApiError, type OrgPaths } from '@auriga/shared';
import { adminApi } from '../admin/api';
import { orgAdminApi, type Group } from '../admin/orgApi';
import {
  resourceApi,
  strongEtag,
  type ResourcePackage,
  type ResourceVersion,
  type PackageInput,
  type PackagePatch,
} from '../admin/resourceApi';
import { useCursorList } from '../admin/useCursorList';
import { orgError } from '../admin/useOrgActions';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/resources.css';

type Membership =
  OrgPaths['/me']['get']['responses']['200']['content']['application/json'];
const { t } = useAdminI18n();
const levels = ['l0', 'l1', 'l2', 'l3', 'l4'];
const scopes = ['tenant', 'members', 'groups', 'private'] as const;
const levelOptions = computed(() => [
  { value: '', label: t('resourceAll') },
  ...levels.map((value) => ({ value, label: value.toUpperCase() })),
]);
const visibilityOptions = computed(() => [
  { value: '', label: t('resourceAll') },
  ...scopes.map((value) => ({
    value,
    label: t(`resourceVisibilityLabels.${value}`),
  })),
]);
const formScopes = computed(() =>
  (editing.value && detail.value?.level === 'l4' ? ['private'] : scopes).map(
    (value) => ({ value, label: t(`resourceVisibilityLabels.${value}`) }),
  ),
);
const membership = shallowRef<Membership>();
const canRead = computed(
  () =>
    membership.value?.permissions.includes('resource.packages.read') ?? false,
);
const canWrite = computed(
  () =>
    canRead.value &&
    membership.value?.permissions.includes('resource.packages.upload'),
);
const canGroups = computed(() =>
  membership.value?.permissions.includes('org.groups.read'),
);
const busy = ref(false);
const error = ref('');
const notice = ref('');
const search = ref('');
const level = ref('');
const visibility = ref('');
const selected = ref('');
const detail = shallowRef<ResourcePackage>();
const etag = ref<string | null>(null);
const detailLoading = ref(false);
const detailError = ref('');
const version = shallowRef<ResourceVersion>();
const versionLoading = ref(false);
const versionError = ref('');
const selectedVersion = ref('');
const editing = ref(false);
const conflict = ref(false);
const readback = ref(false);
const editor = ref(false);
const name = ref('');
const scope = ref<PackageInput['visibility']>('private');
const groupIds = ref<string[]>([]);
const changeGroups = ref(false);
const groups = shallowRef<Group[]>([]);
const groupsError = ref('');
const groupsLoading = ref(false);
const intent = shallowRef<{
  key: string;
  body: PackageInput;
  started: boolean;
  createdAt: number;
}>();
const pending = shallowRef<{ message: string; run: () => Promise<void> }>();
const locked = computed(() => busy.value || !!pending.value);
const editable = computed(
  () =>
    canWrite.value &&
    detail.value?.ownerId === membership.value?.member.id &&
    ['l3', 'l4'].includes(detail.value?.level ?? ''),
);
const shown = computed(() =>
  packages.items.value.filter(
    (item) =>
      (!search.value ||
        `${item.name} ${item.ownerId}`
          .toLowerCase()
          .includes(search.value.toLowerCase())) &&
      (!level.value || item.level === level.value) &&
      (!visibility.value || item.visibility === visibility.value),
  ),
);
let alive = true;
let authEpoch = 0;
let detailEpoch = 0;
let versionEpoch = 0;
let groupsEpoch = 0;
let sessionIdentity: string | undefined;
function closeDetail() {
  ++detailEpoch;
  ++versionEpoch;
  ++groupsEpoch;
  selected.value = '';
  detail.value = undefined;
  etag.value = null;
  version.value = undefined;
  selectedVersion.value = '';
  versions.clear();
  detailError.value = '';
  versionError.value = '';
  detailLoading.value = false;
  versionLoading.value = false;
  if (editing.value) {
    editor.value = false;
    name.value = '';
    groupIds.value = [];
  }
  editing.value = false;
  conflict.value = false;
}
function clearAccess() {
  ++authEpoch;
  membership.value = undefined;
  packages.clear(true);
  closeDetail();
  editor.value = false;
  name.value = '';
  groupIds.value = [];
  groups.value = [];
  pending.value = undefined;
}
function message(e: unknown) {
  if (isApiError(e)) {
    if (e.problem.code === 'iam.step_up_required')
      return `${t('resourceStepup')} (${e.problem.code})`;
    if (e.status === 401 || e.status === 403) clearAccess();
    if (e.status === 404) return t('resourceMissing');
    if (e.status === 503) return t('resourceUnavailable');
  }
  return orgError(e, t);
}
async function authorize() {
  const request = ++authEpoch;
  try {
    const result = await adminApi.membership();
    if (!alive || request !== authEpoch) return false;
    const identity = `${result.tenant.id}/${result.member.id}`;
    if (sessionIdentity && sessionIdentity !== identity) {
      clearAccess();
      error.value = t('resourceReadDenied');
      return false;
    }
    sessionIdentity = identity;
    membership.value = result;
    if (!canRead.value) {
      clearAccess();
      error.value = t('resourceReadDenied');
      return false;
    }
    if (!canWrite.value) {
      editor.value = false;
      name.value = '';
      groupIds.value = [];
    }
    if (!canGroups.value) {
      ++groupsEpoch;
      groups.value = [];
    }
    return true;
  } catch (e) {
    if (alive && request === authEpoch) {
      clearAccess();
      error.value = orgError(e, t);
    }
    return false;
  }
}
const packages = useCursorList<ResourcePackage>(async (cursor) => {
  if (!(await authorize())) return { items: [] };
  return resourceApi.list(cursor);
}, message);
const versions = useCursorList<ResourceVersion>(async (cursor) => {
  if (!selected.value || !(await authorize())) return { items: [] };
  return resourceApi.versions(selected.value, cursor);
}, message);
onMounted(() => void packages.load());
onBeforeUnmount(() => {
  alive = false;
  clearAccess();
  intent.value = undefined;
});

async function loadDetail(packageId: string) {
  closeDetail();
  selected.value = packageId;
  const request = detailEpoch;
  detailLoading.value = true;
  try {
    if (!(await authorize()) || request !== detailEpoch) return;
    const result = await resourceApi.get(packageId);
    if (!alive || request !== detailEpoch) return;
    detail.value = result.data;
    etag.value = result.etag;
    await versions.load();
  } catch (e) {
    if (alive && request === detailEpoch) detailError.value = message(e);
  } finally {
    if (alive && request === detailEpoch) detailLoading.value = false;
  }
}
async function loadVersion(versionId: string) {
  const packageId = selected.value;
  const request = ++versionEpoch;
  selectedVersion.value = versionId;
  version.value = undefined;
  versionError.value = '';
  versionLoading.value = true;
  try {
    if (!(await authorize()) || request !== versionEpoch || !packageId) return;
    const result = await resourceApi.version(packageId, versionId);
    if (alive && request === versionEpoch && packageId === selected.value)
      version.value = result;
  } catch (e) {
    if (alive && request === versionEpoch) versionError.value = message(e);
  } finally {
    if (alive && request === versionEpoch) versionLoading.value = false;
  }
}
async function loadGroups() {
  const request = ++groupsEpoch;
  groups.value = [];
  groupsError.value = '';
  if (!canGroups.value) return;
  groupsLoading.value = true;
  try {
    const result = await orgAdminApi.groups();
    if (alive && request === groupsEpoch && canGroups.value)
      groups.value = result.items;
  } catch (e) {
    if (alive && request === groupsEpoch) {
      groupsError.value = orgError(e, t);
      if (isApiError(e) && e.status === 401) clearAccess();
    }
  } finally {
    if (alive && request === groupsEpoch) groupsLoading.value = false;
  }
}
function openCreate() {
  if (!canWrite.value || locked.value || readback.value) return;
  editing.value = false;
  conflict.value = false;
  const original = intent.value?.started ? intent.value.body : undefined;
  name.value = original?.name ?? '';
  scope.value = original?.visibility ?? 'private';
  groupIds.value = [...(original?.groupIds ?? [])];
  changeGroups.value = true;
  editor.value = true;
  error.value = '';
  notice.value = '';
  void loadGroups();
}
function openEdit() {
  if (!editable.value || !detail.value || locked.value || readback.value)
    return;
  name.value = detail.value.name;
  scope.value = detail.value.visibility;
  groupIds.value = [...detail.value.groupIds];
  editing.value = true;
  conflict.value = false;
  changeGroups.value = false;
  editor.value = true;
  error.value = '';
  notice.value = '';
  void loadGroups();
}
function cancelEditor() {
  if (!busy.value) {
    editor.value = false;
    name.value = '';
    groupIds.value = [];
  }
}
function validForm() {
  const length = Array.from(name.value).length;
  if (
    !length ||
    length > 214 ||
    name.value.includes(String.fromCharCode(0)) ||
    /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(
      name.value,
    )
  ) {
    error.value = t('resourceNameInvalid');
    return false;
  }
  if (
    scope.value === 'groups' &&
    (!editing.value ||
      changeGroups.value ||
      detail.value?.visibility !== 'groups') &&
    (!canGroups.value ||
      !groupIds.value.length ||
      groupIds.value.length > 200 ||
      groupIds.value.some(
        (id) => !groups.value.some((group) => group.id === id),
      ))
  ) {
    error.value = t('resourceGroupsInvalid');
    return false;
  }
  return true;
}
const visibilityLabel = (value: string) =>
  t(`resourceVisibilityLabels.${value}`);
const stateLabel = (value: string) => t(`resourceStateLabels.${value}`);
function requestSave() {
  if (
    locked.value ||
    !canWrite.value ||
    readback.value ||
    conflict.value ||
    !validForm()
  )
    return;
  error.value = '';
  notice.value = '';
  if (editing.value) {
    const original = detail.value;
    if (!original || !editable.value || !strongEtag(etag.value)) {
      error.value = t('resourceEtag');
      return;
    }
    const body: PackagePatch = {};
    const changes: string[] = [];
    if (name.value !== original.name) {
      body.name = name.value;
      changes.push(
        t('resourceChanged', {
          field: t('name'),
          before: original.name,
          after: name.value,
        }),
      );
    }
    if (scope.value !== original.visibility) {
      body.visibility = scope.value;
      changes.push(
        t('resourceChanged', {
          field: t('resourceVisibility'),
          before: visibilityLabel(original.visibility),
          after: visibilityLabel(scope.value),
        }),
      );
    }
    if (
      scope.value === 'groups' &&
      (changeGroups.value || original.visibility !== 'groups')
    ) {
      const ids = [...groupIds.value].sort();
      if (
        JSON.stringify(ids) !== JSON.stringify([...original.groupIds].sort())
      ) {
        body.groupIds = ids;
        changes.push(
          t('resourceChanged', {
            field: t('resourceGroups'),
            before: original.groupIds.join(', '),
            after: ids
              .map((id) => groups.value.find((g) => g.id === id)?.name ?? id)
              .join(', '),
          }),
        );
      }
    } else if (scope.value !== 'groups' && original.groupIds.length)
      body.groupIds = [];
    if (!Object.keys(body).length) return;
    const originalEtag = etag.value;
    pending.value = {
      message: t('resourceEditConfirm', {
        name: original.name,
        changes: changes.join('; '),
      }),
      run: async () => {
        const result = await resourceApi.patch(original.id, body, originalEtag);
        if (!alive || !canRead.value) return;
        await afterWrite(result.id);
      },
    };
  } else {
    if (!intent.value?.started)
      intent.value = {
        key: globalThis.crypto.randomUUID(),
        body: {
          name: name.value,
          kind: 'plugin',
          visibility: scope.value,
          groupIds: scope.value === 'groups' ? [...groupIds.value].sort() : [],
        },
        started: false,
        createdAt: Date.now(),
      };
    const creation = intent.value;
    pending.value = {
      message: t('resourceCreateConfirm', {
        changes: `${t('name')}: ${creation.body.name}; ${t('resourceVisibility')}: ${visibilityLabel(creation.body.visibility)}${creation.body.groupIds?.length ? `; ${t('resourceGroups')}: ${creation.body.groupIds.join(', ')}` : ''}`,
      }),
      run: async () => {
        if (Date.now() - creation.createdAt >= 86_400_000) {
          error.value = t('resourceIntentExpired');
          return;
        }
        creation.started = true;
        try {
          const result = await resourceApi.create(creation.body, creation.key);
          if (!alive || !canRead.value) return;
          intent.value = undefined;
          await afterWrite(result.id);
        } catch (e) {
          if (alive) error.value = `${message(e)} ${t('resourceUncertain')}`;
        }
      },
    };
  }
}
async function confirm() {
  const action = pending.value;
  if (!action || busy.value) return;
  busy.value = true;
  try {
    if (!(await authorize())) return;
    if (!canWrite.value) {
      error.value = t('forbidden');
      return;
    }
    await action.run();
  } catch (e) {
    if (alive) {
      if (isApiError(e) && e.status === 412) {
        conflict.value = true;
        error.value = t('resourceConflict');
      } else error.value = message(e);
    }
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
async function afterWrite(packageId: string) {
  editor.value = false;
  name.value = '';
  groupIds.value = [];
  editing.value = false;
  notice.value = t('resourceSaved');
  readback.value = true;
  await retryRead(packageId);
}
async function retryRead(packageId = selected.value) {
  if (!alive) return;
  const wasReadback = readback.value;
  error.value = '';
  const listed = await packages.refresh();
  if (packageId && canRead.value) await loadDetail(packageId);
  if (!alive || !canRead.value) return;
  readback.value =
    wasReadback && (!listed || !!detailError.value || !!versions.error.value);
  if (readback.value) error.value = t('resourceReadback');
}
function reloadDraft() {
  if (locked.value) return;
  const packageId = selected.value;
  pending.value = {
    message: t('resourceReloadConfirm'),
    run: async () => {
      editor.value = false;
      await loadDetail(packageId);
    },
  };
}
function cancelConfirm() {
  if (!busy.value) pending.value = undefined;
}
</script>

<template>
  <div class="resource-page">
    <header class="resource-head">
      <div>
        <h1>{{ t('governance') }}</h1>
        <p>{{ t('resourceScope') }}</p>
      </div>
      <div class="resource-actions">
        <ElButton
          :disabled="locked || packages.loading.value"
          @click="retryRead()"
          >{{ t('refresh') }}</ElButton
        ><ElButton
          v-if="canWrite"
          type="primary"
          :disabled="locked || readback"
          data-testid="new-package"
          @click="openCreate"
          >{{
            intent?.started ? t('resourceIntent') : t('resourceCreate')
          }}</ElButton
        >
      </div>
    </header>
    <div class="resource-tabs">
      <span class="active">{{ t('resourceCatalog') }}</span
      ><RouterLink
        v-for="[path, label] in [
          ['review', 'resourceReview'],
          ['published', 'resourcePublished'],
          ['distribution', 'resourceDistribution'],
          ['tools', 'resourceTools'],
        ]"
        :key="path"
        :to="`/resources/${path}`"
        >{{ t(label!) }}</RouterLink
      >
    </div>
    <p class="resource-notice">{{ t('resourceBoundary') }}</p>
    <p v-if="error" role="alert" class="resource-error">
      {{ error }}
      <ElButton
        v-if="readback"
        :disabled="locked || packages.loading.value"
        @click="retryRead()"
        >{{ t('resourceRetryRead') }}</ElButton
      >
    </p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="!canRead && !packages.loading.value" role="alert">
      {{ t('resourceReadDenied') }}
    </p>
    <template v-if="canRead">
      <div class="resource-toolbar">
        <ElInput
          v-model="search"
          :placeholder="t('resourceSearch')"
          :aria-label="t('resourceSearch')"
          clearable
        /><select
          v-model="level"
          class="resource-select"
          :aria-label="t('resourceLevel')"
        >
          <option
            v-for="item in levelOptions"
            :key="item.value"
            :value="item.value"
          >
            {{ item.label }}
          </option></select
        ><select
          v-model="visibility"
          class="resource-select"
          :aria-label="t('resourceVisibility')"
        >
          <option
            v-for="item in visibilityOptions"
            :key="item.value"
            :value="item.value"
          >
            {{ item.label }}
          </option>
        </select>
      </div>
      <p class="resource-muted">{{ t('resourceCurrentPage') }}</p>
      <p v-if="packages.error.value" role="alert" class="resource-error">
        {{ packages.error.value }}
        <ElButton
          :disabled="locked || packages.loading.value"
          @click="retryRead()"
          >{{ t('retry') }}</ElButton
        >
      </p>
      <p v-if="packages.loading.value" role="status">{{ t('loading') }}</p>
      <div class="resource-table">
        <table>
          <thead>
            <tr>
              <th>{{ t('name') }}</th>
              <th>{{ t('resourceType') }}</th>
              <th>{{ t('resourceOwner') }}</th>
              <th>{{ t('resourceVisibility') }}</th>
              <th>{{ t('resourceLevel') }}</th>
              <th>{{ t('resourceEnabled') }}</th>
              <th>{{ t('resourceCreated') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in shown" :key="item.id">
              <td>
                <button
                  class="resource-name"
                  :disabled="locked"
                  @click="loadDetail(item.id)"
                >
                  {{ item.name }}</button
                ><small class="resource-mono">{{ item.id }}</small>
              </td>
              <td>{{ item.kind }}</td>
              <td class="resource-mono">{{ item.ownerId }}</td>
              <td>{{ visibilityLabel(item.visibility) }}</td>
              <td>
                <span class="resource-tag">{{ item.level.toUpperCase() }}</span>
              </td>
              <td>{{ item.enabled ? t('yes') : t('no') }}</td>
              <td>{{ item.createdAt }}</td>
            </tr>
          </tbody>
        </table>
        <p
          v-if="
            !packages.loading.value && !packages.error.value && !shown.length
          "
          class="resource-empty"
        >
          {{ t('empty') }}
        </p>
        <footer class="resource-pager">
          <ElButton
            :disabled="
              locked || packages.loading.value || !packages.history.value.length
            "
            @click="packages.previous"
            >{{ t('previous') }}</ElButton
          ><ElButton
            :disabled="
              locked || packages.loading.value || !packages.nextCursor.value
            "
            @click="packages.next"
            >{{ t('next') }}</ElButton
          >
        </footer>
      </div>
    </template>
    <ElDrawer
      :model-value="!!selected"
      size="600px"
      :title="detail?.name || t('details')"
      :before-close="
        (done) => {
          if (!locked) {
            closeDetail();
            done();
          }
        }
      "
      class="resource-drawer"
      @closed="() => undefined"
    >
      <p v-if="detailLoading" role="status">{{ t('loading') }}</p>
      <p v-if="detailError" role="alert" class="resource-error">
        {{ detailError }}
        <ElButton :disabled="locked" @click="loadDetail(selected)">{{
          t('retry')
        }}</ElButton>
      </p>
      <template v-if="detail"
        ><h3>{{ t('resourceVersions') }}</h3>
        <p v-if="versions.loading.value" role="status">{{ t('loading') }}</p>
        <p v-if="versions.error.value" role="alert" class="resource-error">
          {{ versions.error.value }}
          <ElButton
            :disabled="locked || versions.loading.value"
            @click="versions.refresh"
            >{{ t('retry') }}</ElButton
          >
        </p>
        <p
          v-if="
            !versions.loading.value &&
            !versions.error.value &&
            !versions.items.value.length
          "
        >
          {{ t('resourceNoVersions') }}
        </p>
        <div class="resource-version-list">
          <button
            v-for="item in versions.items.value"
            :key="item.id"
            :aria-pressed="selectedVersion === item.id"
            :disabled="locked"
            @click="loadVersion(item.id)"
          >
            <b>{{ item.version }}</b
            ><span class="resource-tag">{{ stateLabel(item.state) }}</span
            ><small class="resource-mono">{{ item.id }}</small>
          </button>
        </div>
        <div class="resource-actions">
          <ElButton
            :disabled="
              locked || versions.loading.value || !versions.history.value.length
            "
            @click="versions.previous"
            >{{ t('previous') }}</ElButton
          ><ElButton
            :disabled="
              locked || versions.loading.value || !versions.nextCursor.value
            "
            @click="versions.next"
            >{{ t('next') }}</ElButton
          >
        </div>
        <h3>{{ t('resourceVersion') }}</h3>
        <p v-if="versionLoading" role="status">{{ t('loading') }}</p>
        <p v-if="versionError" role="alert" class="resource-error">
          {{ versionError }}
          <ElButton :disabled="locked" @click="loadVersion(selectedVersion)">{{
            t('retry')
          }}</ElButton>
        </p>
        <p v-if="!version && !versionLoading && !versionError">
          {{ t('resourceNoVersion') }}
        </p>
        <dl v-if="version" class="resource-facts" data-testid="version-detail">
          <dt>{{ t('resourceVersionLabel') }}</dt>
          <dd>{{ version.version }}</dd>
          <dt>{{ t('status') }}</dt>
          <dd>{{ stateLabel(version.state) }}</dd>
          <dt>{{ t('resourceDigest') }}</dt>
          <dd class="resource-mono">{{ version.digest }}</dd>
          <dt>{{ t('resourceSize') }}</dt>
          <dd>{{ version.sizeBytes }}</dd>
          <dt>{{ t('resourceRevision') }}</dt>
          <dd>{{ version.revision }}</dd>
          <dt>{{ t('resourceRelease') }}</dt>
          <dd>{{ version.releaseSequence ?? t('resourceRawUnknown') }}</dd>
          <dt>{{ t('resourceCreated') }}</dt>
          <dd>{{ version.createdAt }}</dd>
        </dl>
        <p class="resource-notice">{{ t('resourceVersionBoundary') }}</p>
        <h3>{{ t('resourceMetadata') }}</h3>
        <dl class="resource-facts">
          <dt>{{ t('id') }}</dt>
          <dd>{{ detail.id }}</dd>
          <dt>{{ t('resourceOwner') }}</dt>
          <dd>{{ detail.ownerId }}</dd>
          <dt>{{ t('resourceVisibility') }}</dt>
          <dd>
            {{ visibilityLabel(detail.visibility)
            }}<small v-if="detail.groupIds.length" class="resource-mono">{{
              detail.groupIds.join(', ')
            }}</small>
          </dd>
          <dt>{{ t('resourceLevel') }}</dt>
          <dd>{{ detail.level.toUpperCase() }}</dd>
          <dt>{{ t('resourceDefault') }}</dt>
          <dd>{{ detail.defaultVersionId || t('resourceNoDefault') }}</dd>
          <dt>{{ t('resourceRevision') }}</dt>
          <dd>{{ detail.revision }}</dd>
        </dl>
        <p class="resource-muted">{{ t('resourceOwnerOnly') }}</p>
        <p v-if="editable && !strongEtag(etag)" role="alert">
          {{ t('resourceEtag') }}
        </p>
      </template>
      <template #footer
        ><ElButton
          v-if="readback"
          :disabled="locked || packages.loading.value"
          @click="retryRead()"
          >{{ t('resourceRetryRead') }}</ElButton
        ><ElButton :disabled="locked" @click="closeDetail">{{
          t('close')
        }}</ElButton
        ><ElButton
          v-if="editable"
          :disabled="locked || readback || !strongEtag(etag)"
          data-testid="edit-package"
          @click="openEdit"
          >{{ t('resourceEdit') }}</ElButton
        ></template
      >
    </ElDrawer>
    <ElDialog
      :model-value="editor"
      :title="editing ? t('resourceEdit') : t('resourceNew')"
      :close-on-click-modal="false"
      :close-on-press-escape="!locked"
      :show-close="!locked"
      @close="cancelEditor"
    >
      <p>{{ t('resourceOwnerOnly') }}</p>
      <p v-if="error" role="alert" class="resource-error">{{ error }}</p>
      <p v-if="!editing && intent?.started" class="resource-notice">
        {{ t('resourceUncertain') }}
      </p>
      <ElForm label-position="top"
        ><ElFormItem :label="t('name')"
          ><ElInput
            v-model="name"
            data-testid="package-name"
            :disabled="locked || (!editing && !!intent?.started)" /></ElFormItem
        ><ElFormItem :label="t('resourceVisibility')"
          ><select
            v-model="scope"
            class="resource-select"
            data-testid="package-visibility"
            :disabled="
              locked ||
              (!editing && !!intent?.started) ||
              (editing && detail?.level === 'l4')
            "
          >
            <option
              v-for="item in formScopes"
              :key="item.value"
              :value="item.value"
              :disabled="
                item.value === 'members' &&
                membership?.member.memberType === 'external'
              "
            >
              {{ item.label }}
            </option>
          </select></ElFormItem
        >
        <template v-if="scope === 'groups'"
          ><p>{{ t('resourceGroupsRead') }}</p>
          <p v-if="editing && !changeGroups && detail?.visibility === 'groups'">
            {{ t('resourceGroupsKeep') }}: {{ detail.groupIds.join(', ') }}
            <ElButton
              v-if="canGroups"
              :disabled="locked"
              @click="changeGroups = true"
              >{{ t('resourceGroupsChange') }}</ElButton
            >
          </p>
          <ElFormItem v-else :label="t('resourceGroups')"
            ><select
              v-model="groupIds"
              class="resource-select"
              data-testid="package-groups"
              multiple
              :disabled="
                locked ||
                !canGroups ||
                groupsLoading ||
                (!editing && !!intent?.started)
              "
            >
              <option v-for="group in groups" :key="group.id" :value="group.id">
                {{ group.name }}
              </option>
            </select></ElFormItem
          >
          <p v-if="groupsLoading" role="status">{{ t('loading') }}</p>
          <p v-if="groupsError" role="alert">
            {{ groupsError }}
            <ElButton :disabled="locked || groupsLoading" @click="loadGroups">{{
              t('retry')
            }}</ElButton>
          </p></template
        ></ElForm
      >
      <template #footer
        ><ElButton :disabled="locked" @click="cancelEditor">{{
          t('cancel')
        }}</ElButton
        ><ElButton v-if="conflict" :disabled="locked" @click="reloadDraft">{{
          t('resourceReload')
        }}</ElButton
        ><ElButton
          :disabled="locked || conflict || !canWrite"
          data-testid="save-package"
          type="primary"
          @click="requestSave"
          >{{
            !editing && intent?.started ? t('resourceIntent') : t('save')
          }}</ElButton
        ></template
      >
    </ElDialog>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="cancelConfirm"
    />
  </div>
</template>
