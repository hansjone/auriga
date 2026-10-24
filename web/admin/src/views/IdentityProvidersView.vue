<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import { ElButton, ElDrawer, ElDialog } from 'element-plus';
import { isApiError, pickLocalized } from '@auriga/shared';
import { useAdminI18n } from '../admin/messages';
import { useOrgActions, orgError } from '../admin/useOrgActions';
import ActionConfirm from '../admin/ActionConfirm.vue';
import {
  providersApi,
  type Provider,
  type ConnectorType,
  type ProviderInput,
  type ProviderPatch,
} from '../admin/providersApi';
import {
  configForm,
  configError,
  displayDraft,
  providerBody,
  type ConfigForm,
} from '../admin/providersForm';
const { t, locale } = useAdminI18n();
const access = useOrgActions('iam.providers.read', 'iam.providers.write', t);
const { canRead, canWrite, busy, locked, error, notice, pending } = access;
const providers = shallowRef<Provider[]>([]);
const connectors = shallowRef<ConnectorType[]>([]);
const loading = ref(false);
const listError = ref('');
const connectorError = ref('');
const tab = ref('providers');
const search = ref('');
const selectedId = ref('');
const detail = shallowRef<Provider>();
const detailLoading = ref(false);
const detailError = ref('');
const editor = ref(false);
const creating = ref(false);
const connectorType = ref('');
const display = ref(displayDraft());
const enabled = ref(true);
const fields = ref<ConfigForm>({
  fields: [],
  unsupported: [],
  createBlocked: false,
});
const readback = ref(false);
const lifetime = new globalThis.AbortController();
let writes = new globalThis.AbortController();
let listEpoch = 0;
let detailEpoch = 0;
let editEpoch = 0;
const builtin = (provider: Provider) =>
  provider.builtin || provider.connectorType === 'emergency';
const label = (provider: Provider) =>
  pickLocalized(provider.display.name, locale.value) || provider.connectorType;
const visible = computed(() =>
  providers.value.filter((provider) =>
    `${label(provider)} ${provider.connectorType}`
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
);
const currentConnector = computed(() =>
  connectors.value.find((c) => c.descriptor.type === connectorType.value),
);
const available = computed(() =>
  connectors.value.filter(
    (c) =>
      c.descriptor.type !== 'emergency' &&
      !c.loadError &&
      (c.descriptor.multipleInstances !== false ||
        !providers.value.some((p) => p.connectorType === c.descriptor.type)),
  ),
);
const detailConnector = computed(() =>
  connectors.value.find(
    (c) => c.descriptor.type === detail.value?.connectorType,
  ),
);
const readFields = computed(() =>
  configForm(detailConnector.value, detail.value?.config, false),
);
function clearEditor() {
  ++editEpoch;
  editor.value = false;
  fields.value = { fields: [], unsupported: [], createBlocked: false };
  display.value = displayDraft();
}
function closeDetail() {
  ++detailEpoch;
  selectedId.value = '';
  detail.value = undefined;
  detailLoading.value = false;
  detailError.value = '';
  clearEditor();
}
function clearReads() {
  ++listEpoch;
  providers.value = [];
  connectors.value = [];
  loading.value = false;
  closeDetail();
}
watch(
  canRead,
  (allowed) => {
    if (!allowed) clearReads();
  },
  { flush: 'sync' },
);
watch(
  canWrite,
  (allowed) => {
    if (!allowed) {
      writes.abort();
      writes = new globalThis.AbortController();
      clearEditor();
      access.pending.value = undefined;
    }
  },
  { flush: 'sync' },
);
onBeforeUnmount(() => {
  lifetime.abort();
  writes.abort();
  clearReads();
});
function failure(e: unknown) {
  if (isApiError(e) && e.problem.code === 'iam.step_up_required')
    return `${t('idpStepup')} (${e.problem.code})`;
  if (isApiError(e) && (e.status === 401 || e.status === 403))
    access.permissions.value = [];
  return orgError(e, t);
}
async function load() {
  const request = ++listEpoch;
  loading.value = true;
  listError.value = '';
  connectorError.value = '';
  providers.value = [];
  connectors.value = [];
  try {
    if (!(await access.authorize()) || request !== listEpoch) return false;
    const results = await Promise.allSettled([
      providersApi.list(lifetime.signal),
      providersApi.connectors(lifetime.signal),
    ]);
    if (!access.isAlive() || request !== listEpoch || !canRead.value)
      return false;
    const [instances, types] = results;
    if (instances.status === 'fulfilled')
      providers.value = instances.value.items;
    else listError.value = failure(instances.reason);
    if (!canRead.value || request !== listEpoch) return false;
    if (types.status === 'fulfilled') connectors.value = types.value.items;
    else connectorError.value = failure(types.reason);
    return instances.status === 'fulfilled';
  } catch (e) {
    if (access.isAlive() && request === listEpoch) listError.value = failure(e);
    return false;
  } finally {
    if (access.isAlive() && request === listEpoch) loading.value = false;
  }
}
async function select(id: string) {
  closeDetail();
  selectedId.value = id;
  const request = detailEpoch;
  detailLoading.value = true;
  try {
    if (!(await access.authorize()) || request !== detailEpoch) return false;
    const result = await providersApi.get(id, lifetime.signal);
    if (!access.isAlive() || request !== detailEpoch || !canRead.value)
      return false;
    detail.value = result;
    return true;
  } catch (e) {
    if (access.isAlive() && request === detailEpoch)
      detailError.value = failure(e);
    return false;
  } finally {
    if (access.isAlive() && request === detailEpoch)
      detailLoading.value = false;
  }
}
onMounted(() => void load());
function changeConnector() {
  fields.value = configForm(currentConnector.value, {}, true);
}
function newProvider() {
  if (
    !canWrite.value ||
    locked.value ||
    readback.value ||
    loading.value ||
    !available.value.length
  )
    return;
  clearEditor();
  creating.value = true;
  connectorType.value = available.value[0]!.descriptor.type;
  enabled.value = true;
  changeConnector();
  editor.value = true;
  error.value = '';
  notice.value = '';
}
function editProvider() {
  if (
    !detail.value ||
    builtin(detail.value) ||
    !canWrite.value ||
    locked.value ||
    readback.value
  )
    return;
  clearEditor();
  creating.value = false;
  connectorType.value = detail.value.connectorType;
  display.value = displayDraft(detail.value);
  enabled.value = detail.value.enabled;
  fields.value = configForm(detailConnector.value, detail.value.config, false);
  editor.value = true;
  error.value = '';
  notice.value = '';
}
function cancelEditor() {
  if (!locked.value) clearEditor();
}
async function readAfterWrite(id: string, message: string) {
  clearEditor();
  notice.value = message;
  readback.value = true;
  await retryRead(id);
}
async function retryRead(id = selectedId.value) {
  if (loading.value || !access.isAlive()) return;
  error.value = '';
  const listed = await load();
  if (!access.isAlive() || !canRead.value) return;
  const got = id ? await select(id) : true;
  if (!access.isAlive() || !canRead.value) return;
  if (readback.value) {
    readback.value = !listed || !got;
    if (readback.value) error.value = t('idpReadback');
  }
}
function save() {
  if (!canWrite.value || locked.value || readback.value) return;
  const fieldError = configError(fields.value);
  if (
    !display.value.nameEn.trim() ||
    !/^-?\d+$/.test(display.value.order) ||
    !Number.isSafeInteger(Number(display.value.order)) ||
    fieldError
  ) {
    error.value = t('idpInvalid', { field: fieldError ?? t('idpNameEn') });
    return;
  }
  const original = creating.value ? undefined : detail.value;
  if (
    (!creating.value && (!original || builtin(original))) ||
    (creating.value &&
      !available.value.some((c) => c.descriptor.type === connectorType.value))
  )
    return;
  const body = providerBody(
    fields.value,
    display.value,
    enabled.value,
    connectorType.value,
    original,
  );
  if (!Object.keys(body).length) {
    notice.value = t('idpNoChanges');
    return;
  }
  const changes = [
    body.display ? t('idpDisplayChanged') : '',
    body.enabled !== undefined ? t('idpStateChanged') : '',
    body.config
      ? t('idpConfigChanged', {
          fields: fields.value.fields
            .filter((f) => f.set && !f.readonly)
            .map((f) => f.path.join('.'))
            .join(', '),
        })
      : '',
  ]
    .filter(Boolean)
    .join('; ');
  const name = display.value.nameEn;
  const epoch = editEpoch;
  const signal = writes.signal;
  access.ask(
    original
      ? t('idpSaveConfirm', { name, changes })
      : t('idpCreateConfirm', { name, type: connectorType.value }),
    async () => {
      if (
        !access.isAlive() ||
        epoch !== editEpoch ||
        signal.aborted ||
        !canWrite.value
      )
        return;
      try {
        const result = original
          ? await providersApi.patch(original.id, body as ProviderPatch, signal)
          : await providersApi.create(body as ProviderInput, signal);
        if (!access.isAlive() || signal.aborted || !canRead.value) return;
        await readAfterWrite(result.id, t('idpSaved'));
      } catch (e) {
        if (!access.isAlive() || signal.aborted) return;
        // Never retain a newly entered secret after any unconfirmed write response.
        fields.value.fields
          .filter((f) => f.secret)
          .forEach((f) => {
            f.text = '';
            f.set = creating.value && f.required;
          });
        error.value = `${failure(e)} ${t('idpSecretRemoved')}`;
      }
    },
  );
}
function toggle() {
  const original = detail.value;
  if (
    !original ||
    builtin(original) ||
    !canWrite.value ||
    locked.value ||
    readback.value
  )
    return;
  const signal = writes.signal;
  access.ask(
    t(original.enabled ? 'idpDisableConfirm' : 'idpEnableConfirm', {
      name: label(original),
    }),
    async () => {
      if (!access.isAlive() || signal.aborted || !canWrite.value) return;
      try {
        if (original.enabled) await providersApi.disable(original.id, signal);
        else await providersApi.patch(original.id, { enabled: true }, signal);
        if (!access.isAlive() || signal.aborted || !canRead.value) return;
        await readAfterWrite(
          original.id,
          t(original.enabled ? 'idpDisabled' : 'idpSaved'),
        );
      } catch (e) {
        if (access.isAlive() && !signal.aborted) error.value = failure(e);
      }
    },
  );
}
function cancelConfirm() {
  if (busy.value) return;
  access.pending.value = undefined;
  fields.value.fields
    .filter((f) => f.secret)
    .forEach((f) => {
      f.text = '';
      f.set = creating.value && f.required;
    });
}
</script>

<template>
  <section class="resource-page providers-page">
    <header class="resource-head">
      <h1>{{ t('idpTitle') }}</h1>
      <div class="resource-actions">
        <ElButton
          :disabled="locked || loading"
          data-testid="refresh-providers"
          @click="retryRead()"
          >{{ t('refresh') }}</ElButton
        ><ElButton
          v-if="canWrite"
          :disabled="locked || loading || readback || !available.length"
          data-testid="new-provider"
          type="primary"
          @click="newProvider"
          >{{ t('idpNew') }}</ElButton
        >
      </div>
    </header>
    <div class="resource-tabs">
      <button
        :class="{ active: tab === 'providers' }"
        @click="tab = 'providers'"
      >
        {{ t('idpInstances') }}</button
      ><button
        :class="{ active: tab === 'connectors' }"
        @click="tab = 'connectors'"
      >
        {{ t('idpConnectors') }}
      </button>
    </div>
    <p class="resource-notice">{{ t('idpDisableHelp') }}</p>
    <p v-if="error" class="resource-error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="readback">
      <ElButton :disabled="locked || loading" @click="retryRead()">{{
        t('idpReadAgain')
      }}</ElButton>
    </p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="!loading && !canRead" role="alert">{{ t('idpReadPermission') }}</p>
    <p v-if="canRead && !canWrite" class="resource-muted">
      {{ t('readonly') }}
    </p>
    <p v-if="listError || connectorError" class="resource-error" role="alert">
      {{ listError }} {{ connectorError }}
      <ElButton :disabled="locked || loading" @click="retryRead()">{{
        t('retry')
      }}</ElButton>
    </p>
    <template v-if="canRead && tab === 'providers'"
      ><input
        v-model="search"
        class="resource-select"
        :placeholder="t('idpSearch')"
        :aria-label="t('idpSearch')"
      />
      <div class="resource-table">
        <table>
          <thead>
            <tr>
              <th>{{ t('name') }}</th>
              <th>{{ t('idpConnector') }}</th>
              <th>{{ t('idpEnabled') }}</th>
              <th>{{ t('idpOrder') }}</th>
              <th>{{ t('idpBuiltin') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="provider in visible" :key="provider.id">
              <td>
                <button
                  class="resource-name"
                  :disabled="locked"
                  data-testid="provider-row"
                  @click="select(provider.id)"
                >
                  {{ label(provider) }}</button
                ><small class="resource-mono">{{ provider.id }}</small>
              </td>
              <td>{{ provider.connectorType }}</td>
              <td>{{ provider.enabled ? t('yes') : t('no') }}</td>
              <td>{{ provider.display.order }}</td>
              <td>{{ builtin(provider) ? t('yes') : t('no') }}</td>
            </tr>
          </tbody>
        </table>
        <p
          v-if="!loading && !listError && !visible.length"
          class="resource-empty"
        >
          {{ t('empty') }}
        </p>
      </div></template
    >
    <div v-if="canRead && tab === 'connectors'" class="resource-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('idpConnector') }}</th>
            <th>{{ t('idpSource') }}</th>
            <th>{{ t('idpSteps') }}</th>
            <th>{{ t('idpCapabilities') }}</th>
            <th>{{ t('status') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="connector in connectors" :key="connector.descriptor.type">
            <td>
              {{ pickLocalized(connector.descriptor.displayName, locale)
              }}<small>{{ connector.descriptor.type }}</small>
            </td>
            <td>{{ connector.source }}</td>
            <td>{{ connector.descriptor.steps.join(', ') }}</td>
            <td>{{ connector.descriptor.capabilities?.join(', ') || '—' }}</td>
            <td>
              {{
                connector.loadError ||
                (connector.descriptor.multipleInstances === false
                  ? t('idpSingle')
                  : t('idpMultiple'))
              }}
            </td>
          </tr>
        </tbody>
      </table>
      <p
        v-if="!loading && !connectorError && !connectors.length"
        class="resource-empty"
      >
        {{ t('empty') }}
      </p>
    </div>
    <p class="resource-muted">{{ t('idpUnsupported') }}</p>
    <ElDrawer
      :model-value="!!selectedId"
      :title="detail ? label(detail) : t('details')"
      size="600px"
      class="resource-drawer"
      :before-close="
        (done) => {
          if (!locked) {
            closeDetail();
            done();
          }
        }
      "
    >
      <p v-if="detailLoading" role="status">{{ t('loading') }}</p>
      <p v-if="detailError" class="resource-error" role="alert">
        {{ detailError }}
        <ElButton :disabled="locked" @click="select(selectedId)">{{
          t('retry')
        }}</ElButton>
      </p>
      <p v-if="error" class="resource-error" role="alert">{{ error }}</p>
      <p v-if="notice" role="status">{{ notice }}</p>
      <template v-if="detail"
        ><p v-if="builtin(detail)" class="resource-notice">
          {{ t('idpBuiltinHelp') }}
        </p>
        <dl class="resource-facts">
          <dt>ID</dt>
          <dd>{{ detail.id }}</dd>
          <dt>{{ t('idpConnector') }}</dt>
          <dd>{{ detail.connectorType }}</dd>
          <dt>{{ t('idpEnabled') }}</dt>
          <dd>{{ detail.enabled ? t('yes') : t('no') }}</dd>
          <dt>{{ t('idpCallback') }}</dt>
          <dd>{{ detail.callbackUrl }}</dd>
        </dl>
        <h3>{{ t('idpConfig') }}</h3>
        <dl class="resource-facts">
          <template
            v-for="field in readFields.fields"
            :key="field.path.join('.')"
            ><dt>{{ field.path.join('.') }}</dt>
            <dd>
              {{
                field.secret
                  ? field.configured
                    ? t('idpSecretSet')
                    : t('idpSecretNotSet')
                  : field.readonly
                    ? t('idpUnknown')
                    : field.present
                      ? field.text
                      : '—'
              }}
            </dd></template
          >
        </dl>
        <p v-if="readFields.unsupported.length" class="resource-notice">
          {{
            t('idpSchemaReadonly', { paths: readFields.unsupported.join(', ') })
          }}
        </p>
        <p v-if="!detailConnector">{{ t('idpNoConnector') }}</p>
        <h3>{{ t('idpAdmission') }}</h3>
        <dl class="resource-facts">
          <dt>mode</dt>
          <dd>{{ detail.admission?.mode || '—' }}</dd>
          <dt>MFA</dt>
          <dd>{{ detail.admission?.mfa || '—' }}</dd>
        </dl>
        <p class="resource-muted">{{ t('idpAdmissionHelp') }}</p></template
      >
      <template #footer
        ><ElButton
          v-if="readback"
          :disabled="locked || loading"
          @click="retryRead()"
          >{{ t('idpReadAgain') }}</ElButton
        ><ElButton :disabled="locked" @click="closeDetail">{{
          t('close')
        }}</ElButton
        ><template v-if="detail && canWrite && !builtin(detail)"
          ><ElButton
            :disabled="locked || readback"
            data-testid="toggle-provider"
            @click="toggle"
            >{{ detail.enabled ? t('idpDisable') : t('idpEnable') }}</ElButton
          ><ElButton
            :disabled="locked || readback"
            data-testid="edit-provider"
            @click="editProvider"
            >{{ t('idpEdit') }}</ElButton
          ></template
        ></template
      >
    </ElDrawer>
    <ElDialog
      :model-value="editor"
      :title="creating ? t('idpNew') : t('idpEdit')"
      :close-on-click-modal="false"
      :close-on-press-escape="!locked"
      :show-close="!locked"
      @close="cancelEditor"
    >
      <p v-if="error" class="resource-error" role="alert">{{ error }}</p>
      <form class="provider-form" @submit.prevent="save">
        <label v-if="creating"
          >{{ t('idpConnector')
          }}<select
            v-model="connectorType"
            :disabled="locked"
            data-testid="connector-type"
            @change="changeConnector"
          >
            <option
              v-for="connector in available"
              :key="connector.descriptor.type"
              :value="connector.descriptor.type"
            >
              {{ pickLocalized(connector.descriptor.displayName, locale) }}
            </option>
          </select></label
        >
        <label
          v-for="[key, labelKey] in [
            ['nameEn', 'idpNameEn'],
            ['nameZh', 'idpNameZh'],
            ['buttonEn', 'idpButtonEn'],
            ['buttonZh', 'idpButtonZh'],
            ['icon', 'idpIcon'],
            ['order', 'idpOrder'],
          ]"
          :key="key"
          >{{ t(labelKey!)
          }}<input
            v-model="display[key as keyof typeof display]"
            :disabled="locked"
            :data-testid="`display-${key}`" /></label
        ><label
          ><input
            v-model="enabled"
            :disabled="locked"
            type="checkbox"
            data-testid="provider-enabled"
          />
          {{ t('idpEnabled') }}</label
        >
        <h3>{{ t('idpConfig') }}</h3>
        <p class="resource-muted">{{ t('idpSecretHelp') }}</p>
        <div
          v-for="field in fields.fields"
          :key="field.path.join('.')"
          class="provider-field"
        >
          <label
            ><input
              v-model="field.set"
              type="checkbox"
              :disabled="
                locked || field.readonly || (creating && field.required)
              "
              :data-testid="`set-${field.path.join('.')}`"
            />
            {{ field.path.join('.') }} · {{ t('idpSet')
            }}<span v-if="field.secret">
              ·
              {{
                field.configured ? t('idpSecretSet') : t('idpSecretNotSet')
              }}</span
            ></label
          >
          <p v-if="field.schema.description" class="resource-muted">
            {{ field.schema.description }}
          </p>
          <template v-if="!field.readonly"
            ><select
              v-if="field.kind === 'boolean'"
              v-model="field.text"
              :disabled="locked || !field.set"
              :aria-label="field.path.join('.')"
              :data-testid="`config-${field.path.join('.')}`"
            >
              <option value="">—</option>
              <option value="true">{{ t('yes') }}</option>
              <option value="false">{{ t('no') }}</option></select
            ><select
              v-else-if="
                Array.isArray(field.schema.enum) &&
                field.kind === 'string' &&
                !field.secret
              "
              v-model="field.text"
              :disabled="locked || !field.set"
              :aria-label="field.path.join('.')"
              :data-testid="`config-${field.path.join('.')}`"
            >
              <option value="">—</option>
              <option
                v-for="value in field.schema.enum"
                :key="String(value)"
                :value="String(value)"
              >
                {{ value }}
              </option></select
            ><textarea
              v-else-if="field.kind === 'array'"
              v-model="field.text"
              :disabled="locked || !field.set"
              :aria-label="field.path.join('.')"
              :data-testid="`config-${field.path.join('.')}`"
            /><input
              v-else
              v-model="field.text"
              :type="field.secret ? 'password' : 'text'"
              :autocomplete="field.secret ? 'new-password' : 'off'"
              :disabled="locked || !field.set"
              :aria-label="field.path.join('.')"
              :data-testid="`config-${field.path.join('.')}`"
            /><small v-if="field.kind === 'array'">{{
              t('idpArrayHelp')
            }}</small></template
          >
        </div>
        <p v-if="fields.unsupported.length" class="resource-notice">
          {{ t('idpSchemaReadonly', { paths: fields.unsupported.join(', ') }) }}
        </p>
        <p class="resource-muted">{{ t('idpAdmissionHelp') }}</p>
      </form>
      <template #footer
        ><ElButton :disabled="locked" @click="cancelEditor">{{
          t('cancel')
        }}</ElButton
        ><ElButton
          :disabled="locked || !canWrite || fields.createBlocked"
          type="primary"
          data-testid="save-provider"
          @click="save"
          >{{ t('save') }}</ElButton
        ></template
      >
    </ElDialog>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="cancelConfirm"
    />
  </section>
</template>
<style scoped>
.providers-page .resource-tabs button {
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  padding: 0 0 12px;
  color: var(--auriga-muted);
  font: inherit;
  cursor: pointer;
}
.providers-page .resource-tabs button.active {
  border-bottom-color: var(--auriga-text);
  color: var(--auriga-text);
}
.provider-form {
  display: grid;
  gap: 14px;
}
.provider-form label {
  display: flex;
  gap: 7px;
  flex-wrap: wrap;
  align-items: center;
  font-size: 12px;
}
.provider-form label > input:not([type='checkbox']),
.provider-form label > select {
  flex-basis: 100%;
}
.provider-form input:not([type='checkbox']),
.provider-form select,
.provider-form textarea {
  box-sizing: border-box;
  width: 100%;
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  padding: 7px 10px;
  background: var(--auriga-surface);
  color: var(--auriga-text);
  font: inherit;
}
.provider-form textarea {
  min-height: 80px;
  resize: vertical;
}
.provider-form h3 {
  margin: 10px 0 0;
  font-size: 13px;
}
.provider-form input:disabled,
.provider-form select:disabled,
.provider-form textarea:disabled {
  opacity: 0.55;
}
.provider-field {
  display: grid;
  gap: 6px;
  padding: 10px 0;
  border-bottom: 1px solid var(--auriga-border);
}
.provider-field p {
  margin: 0;
}
.provider-form small {
  color: var(--auriga-muted);
}
</style>
