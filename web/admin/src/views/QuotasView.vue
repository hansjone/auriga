<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  shallowRef,
  watch,
} from 'vue';
import { ElButton, ElSelect, ElInput, ElForm, ElFormItem } from 'element-plus';
import { ApiError, isApiError } from '@auriga/shared';
import { orgApi } from '../api';
import { adminApi } from '../admin/api';
import { orgAdminApi } from '../admin/orgApi';
import { quotaApi, type Quota, type QuotaTarget } from '../admin/quotaApi';
import {
  quotaFields,
  fromQuota,
  toQuota,
  validateDraft,
  clearedFields,
  preserveUnknownQuota,
  unsafeQuotaFields,
  type QuotaField,
} from '../admin/quotaForm';
import { useCursorList } from '../admin/useCursorList';
import { useOrgActions, orgError } from '../admin/useOrgActions';
import { useAdminI18n } from '../admin/messages';
import ActionConfirm from '../admin/ActionConfirm.vue';
import '../admin/management.css';

const { t } = useAdminI18n();
const access = useOrgActions('org.quotas.read', 'org.quotas.write', t);
const { permissions, canRead, canWrite, busy, pending, locked, error, notice } =
  access;
const scope = ref<QuotaTarget['scope']>('default');
const selectedId = ref('');
const search = ref('');
const layer = shallowRef<Quota>();
const effective = shallowRef<Quota>();
const absent = ref(false);
const loading = ref(false);
const draft = reactive(fromQuota({}));
let version = 0;
const scopeOptions = computed(() =>
  ['default', 'group', 'member'].map((value) => ({
    value,
    label: t(
      value === 'default'
        ? 'defaultQuota'
        : value === 'group'
          ? 'groupQuota'
          : 'memberQuota',
    ),
  })),
);
const candidatePermission = computed(
  () =>
    scope.value === 'default' ||
    permissions.value.includes(
      scope.value === 'group' ? 'org.groups.read' : 'org.members.read',
    ),
);
function forbidden() {
  return new ApiError({
    status: 403,
    code: 'common.forbidden',
    title: t('forbidden'),
  });
}
function ensureTargetPermission(target: QuotaTarget) {
  if (
    target.scope !== 'default' &&
    !permissions.value.includes(
      target.scope === 'group' ? 'org.groups.read' : 'org.members.read',
    )
  )
    throw forbidden();
}
async function verifyTarget(target: QuotaTarget) {
  ensureTargetPermission(target);
  if (target.scope === 'group') await orgAdminApi.group(target.id);
  if (target.scope === 'member') await adminApi.member(target.id);
}
const candidates = useCursorList<{ id: string; label: string }>(
  async (cursor) => {
    if (!(await access.authorize())) return { items: [] };
    if (!candidatePermission.value) return { items: [] };
    if (scope.value === 'group')
      return {
        items: (await orgAdminApi.groups()).items.map((group) => ({
          id: group.id,
          label: group.name,
        })),
      };
    if (scope.value === 'member') {
      const result = await orgApi.listMembers({
        q: search.value.trim() || undefined,
        limit: 50,
        cursor,
      });
      return {
        ...result,
        items: result.items.map((member) => ({
          id: member.id,
          label: `${member.displayName} (${member.email || member.id})`,
        })),
      };
    }
    return { items: [] };
  },
  (e) => {
    if (isApiError(e) && (e.status === 401 || e.status === 403)) {
      error.value = orgError(e, t);
      permissions.value = [];
    }
    return orgError(e, t);
  },
);
const {
  items,
  loading: candidatesLoading,
  error: candidatesError,
  history,
  nextCursor,
} = candidates;
const target = computed<QuotaTarget | undefined>(() =>
  scope.value === 'default'
    ? { scope: 'default' }
    : items.value.some((item) => item.id === selectedId.value)
      ? { scope: scope.value, id: selectedId.value }
      : undefined,
);
const targetName = computed(() =>
  scope.value === 'default'
    ? t('defaultQuota')
    : items.value.find((item) => item.id === selectedId.value)?.label || '',
);
const targetOptions = computed(() =>
  items.value.map((item) => ({ value: item.id, label: item.label })),
);
const unsafeFields = computed(() => unsafeQuotaFields(layer.value));
const readonly = computed(
  () =>
    !canWrite.value ||
    loading.value ||
    locked.value ||
    !layer.value ||
    !!unsafeFields.value.length,
);
function clearData() {
  ++version;
  layer.value = undefined;
  effective.value = undefined;
  absent.value = false;
  loading.value = false;
  Object.assign(draft, fromQuota({}));
}
function clearAll() {
  clearData();
  candidates.clear(true);
  selectedId.value = '';
}
watch(canRead, (allowed) => {
  if (!allowed) clearAll();
});
watch(candidatePermission, (allowed) => {
  if (!allowed) clearAll();
});
onBeforeUnmount(clearAll);
watch(selectedId, () => {
  clearData();
  if (scope.value !== 'default' && selectedId.value && target.value) {
    error.value = '';
    notice.value = '';
    void readCurrent();
  }
});
watch(scope, () => {
  clearAll();
  search.value = '';
  error.value = '';
  notice.value = '';
  if (scope.value === 'default') void readCurrent();
  else void loadCandidates();
});
watch(search, () => {
  if (scope.value === 'member') {
    clearAll();
    error.value = '';
    notice.value = '';
  }
});
onMounted(() => void readCurrent());

async function readCurrent() {
  const selected = target.value;
  if (!selected) return false;
  clearData();
  const request = version;
  loading.value = true;
  error.value = '';
  const current = () => access.isAlive() && version === request;
  try {
    if (!(await access.authorize()) || !current()) return false;
    await verifyTarget(selected);
    if (!current()) return false;
    let result: Quota;
    let missing = false;
    try {
      result = await quotaApi.get(selected);
    } catch (e) {
      if (!current()) return false;
      // Only the existing org quota-row absence is normal, and only after target revalidation.
      if (
        selected.scope === 'default' ||
        !isApiError(e) ||
        e.status !== 404 ||
        e.problem.code !== 'org.not_found'
      )
        throw e;
      await verifyTarget(selected);
      if (!current()) return false;
      result = {};
      missing = true;
    }
    if (!current()) return false;
    const resolved =
      selected.scope === 'member'
        ? await quotaApi.effective(selected.id)
        : undefined;
    if (!current()) return false;
    layer.value = result;
    effective.value = resolved;
    absent.value = missing;
    Object.assign(draft, fromQuota(result));
    return true;
  } catch (e) {
    if (current()) {
      error.value = orgError(e, t);
      if (isApiError(e) && (e.status === 401 || e.status === 403)) {
        permissions.value = [];
        clearAll();
      }
    }
    return false;
  } finally {
    if (current()) loading.value = false;
  }
}
async function loadCandidates() {
  if (locked.value || loading.value || candidatesLoading.value) return;
  clearData();
  selectedId.value = '';
  candidates.clear();
  await candidates.load();
}
function modes(field: QuotaField) {
  return [
    { value: 'unset', label: t('layerUnset') },
    { value: 'value', label: t('quotaValue') },
    ...(field.nullable ? [{ value: 'unlimited', label: t('unlimited') }] : []),
  ];
}
const booleanModes = computed(() => [
  { value: 'unset', label: t('layerUnset') },
  { value: 'allow', label: t('allow') },
  { value: 'deny', label: t('deny') },
]);
function save() {
  const selected = target.value;
  if (!selected || readonly.value || !layer.value) return;
  const invalid = validateDraft(draft);
  if (invalid) {
    error.value = t('invalidQuota', { field: t(`fields.${invalid}`) });
    return;
  }
  const body = preserveUnknownQuota(layer.value, toQuota(draft));
  const removed =
    clearedFields(layer.value, body)
      .map((field) => t(`fields.${field}`))
      .join(', ') || t('noneCleared');
  access.ask(
    t('replaceQuota', { target: targetName.value, fields: removed }),
    async () => {
      await verifyTarget(selected);
      if (!access.isAlive()) return;
      await quotaApi.put(selected, body);
      if (!access.isAlive()) return;
      notice.value = t('quotaSaved');
      if (!(await readCurrent()) && access.isAlive()) {
        notice.value = '';
        error.value = `${t('quotaRefreshFailed')} ${error.value}`;
      }
    },
  );
}
function remove() {
  const selected = target.value;
  if (
    !selected ||
    selected.scope === 'default' ||
    readonly.value ||
    absent.value
  )
    return;
  access.ask(
    t('deleteQuotaConfirm', { target: targetName.value }),
    async () => {
      await verifyTarget(selected);
      if (!access.isAlive()) return;
      await quotaApi.delete(selected);
      if (!access.isAlive()) return;
      notice.value = t('quotaDeleted');
      if (!(await readCurrent()) && access.isAlive()) {
        notice.value = '';
        error.value = `${t('quotaRefreshFailed')} ${error.value}`;
      }
    },
  );
}
function effectiveValue(field: QuotaField) {
  const section = effective.value?.[field.section];
  const value = section?.[field.id as keyof typeof section];
  if (typeof value === 'number' && !Number.isSafeInteger(value))
    return t('unsafeEffective');
  return value === undefined
    ? t('effectiveUnknown')
    : value === null
      ? t('unlimited')
      : String(value);
}
</script>
<template>
  <div class="management quota-page">
    <header class="head">
      <h1>{{ t('quotaTitle') }}</h1>
      <ElSelect
        v-model="scope"
        :aria-label="t('quotaScope')"
        :options="scopeOptions"
        :disabled="locked"
        style="width: 200px"
      /><ElButton
        :disabled="locked || loading || !target"
        @click="readCurrent"
        >{{ t('refresh') }}</ElButton
      >
    </header>
    <p>{{ t('quotaSemantics') }}</p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <section v-if="scope !== 'default' && canRead" class="edit-form">
      <p v-if="!candidatePermission">{{ t('candidatePermission') }}</p>
      <template v-else>
        <ElForm
          :disabled="locked || loading || candidatesLoading"
          @submit.prevent="loadCandidates"
          ><ElInput
            v-if="scope === 'member'"
            v-model="search"
            :aria-label="t('quotaSearch')"
            :placeholder="t('quotaSearch')"
          /><ElButton native-type="submit">{{
            t('loadCandidates')
          }}</ElButton></ElForm
        >
        <p v-if="candidatesLoading" role="status">{{ t('loading') }}</p>
        <p v-if="candidatesError" role="alert" class="error">
          {{ candidatesError }}
        </p>
        <ElSelect
          v-model="selectedId"
          :aria-label="t('quotaTarget')"
          :placeholder="t('quotaTarget')"
          :no-data-text="t('empty')"
          :options="targetOptions"
          :disabled="locked || candidatesLoading || !!candidatesError"
          style="width: 100%"
        />
        <div v-if="scope === 'member'" class="pager">
          <ElButton
            :disabled="locked || candidatesLoading || !history.length"
            @click="
              selectedId = '';
              candidates.previous();
            "
            >{{ t('previous') }}</ElButton
          ><ElButton
            :disabled="locked || candidatesLoading || !nextCursor"
            @click="
              selectedId = '';
              candidates.next();
            "
            >{{ t('next') }}</ElButton
          >
        </div>
      </template>
    </section>
    <section v-if="layer && canRead" class="details">
      <h2>{{ t('currentLayer') }} · {{ targetName }}</h2>
      <p v-if="unsafeFields.length" class="error" role="alert">
        {{ t('unsafeQuota', { fields: unsafeFields.join(', ') }) }}
      </p>
      <p v-if="absent" role="status">{{ t('layerAbsent') }}</p>
      <p v-if="!canWrite" class="muted">{{ t('readonly') }}</p>
      <ElForm label-position="top" :disabled="readonly" @submit.prevent="save">
        <ElFormItem
          v-for="field in quotaFields"
          :key="field.id"
          :label="`${t(`fields.${field.id}`)} (${t(`units.${field.unit}`)})`"
        >
          <div class="quota-field">
            <ElSelect
              v-model="draft[field.id].mode"
              :aria-label="`${t(`fields.${field.id}`)} ${t('quotaScope')}`"
              :options="modes(field)"
            />
            <ElInput
              v-if="draft[field.id].mode === 'value'"
              v-model="draft[field.id].value"
              inputmode="numeric"
              :aria-label="t(`fields.${field.id}`)"
              :placeholder="t('numericHint', { min: String(field.minimum) })"
            />
          </div>
          <small>{{ t('numericHint', { min: String(field.minimum) }) }}</small>
        </ElFormItem>
        <ElFormItem :label="t('fields.alwaysOnAllowed')"
          ><ElSelect
            v-model="draft.alwaysOnAllowed"
            :aria-label="t('fields.alwaysOnAllowed')"
            :options="booleanModes"
        /></ElFormItem>
        <ElButton v-if="canWrite" native-type="submit" type="primary">{{
          t('saveQuota')
        }}</ElButton>
      </ElForm>
      <ElButton
        v-if="canWrite && scope !== 'default'"
        type="danger"
        :disabled="readonly || absent"
        @click="remove"
        >{{ t('deleteQuota') }}</ElButton
      >
      <p class="muted">{{ t('quotaImpact') }}</p>
    </section>
    <section v-if="effective && canRead" class="details">
      <h2>{{ t('effectiveQuota') }}</h2>
      <p>{{ t('effectiveOrder') }}</p>
      <dl>
        <template v-for="field in quotaFields" :key="field.id"
          ><dt>
            {{ t(`fields.${field.id}`) }} ({{ t(`units.${field.unit}`) }})
          </dt>
          <dd>{{ effectiveValue(field) }}</dd></template
        >
        <dt>{{ t('fields.alwaysOnAllowed') }}</dt>
        <dd>
          {{
            effective.instance?.alwaysOnAllowed === undefined
              ? t('effectiveUnknown')
              : t(effective.instance.alwaysOnAllowed ? 'allow' : 'deny')
          }}
        </dd>
      </dl>
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="!busy && (pending = undefined)"
    />
  </div>
</template>
<style scoped>
.quota-field {
  display: flex;
  flex-wrap: wrap;
  width: 100%;
  gap: 0.75rem;
}
.quota-field .el-select {
  width: 210px;
}
.quota-field .el-input {
  flex: 1;
  min-width: 200px;
}
.quota-page small {
  color: var(--auriga-muted);
}
</style>
