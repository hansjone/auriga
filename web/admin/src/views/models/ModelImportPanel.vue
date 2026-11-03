<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  relayApi,
  type RelayPool,
  type RelayProvider,
} from '../../admin/relayApi';
import { useRelayI18n } from './relayMessages';
import {
  capabilityLimits,
  rowError,
  samePool,
  suggestPublicId,
  type CatalogModel,
  type ImportRow,
} from './modelImport';
import {
  captureUpstreamCapabilities,
  createCapabilityPolicy,
} from './poolCapabilities';
const props = defineProps<{
  providers: RelayProvider[];
  pools: RelayPool[];
  providerId: string;
  permissions: string[];
}>();
const emit = defineEmits<{
  close: [];
  imported: [];
  access: [permissions: string[]];
  running: [value: boolean];
}>();
const { t } = useRelayI18n();
const providerId = ref(props.providerId),
  query = ref(''),
  models = ref<CatalogModel[]>([]),
  selected = ref<string[]>([]),
  rows = ref<ImportRow[]>([]);
const loading = ref(false),
  running = ref(false),
  preview = ref(false),
  confirmed = ref(false),
  failure = ref('');
const inputPrice = ref<number | ''>(''),
  outputPrice = ref<number | ''>('');
const recommendedContext = ref(0),
  recommendedOutput = ref(0);
let catalogController: globalThis.AbortController | undefined,
  batchController: globalThis.AbortController | undefined,
  epoch = 0,
  alive = true;
const writable = computed(
  () =>
    props.permissions.includes('ai.providers.read') &&
    props.permissions.includes('ai.providers.write'),
);
const visible = computed(() =>
  models.value.filter((v) =>
    v.id.toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const existing = computed(() => new Set(props.pools.map((v) => v.id)));
const frozen = computed(() => rows.value.some((v) => !!v.body));
const pendingRows = computed(() =>
  rows.value.filter((v) => v.status !== 'saved' && v.status !== 'conflict'),
);
const validPrice = computed(
  () =>
    Number.isSafeInteger(inputPrice.value) &&
    Number(inputPrice.value) > 0 &&
    Number.isSafeInteger(outputPrice.value) &&
    Number(outputPrice.value) > 0,
);
const validRecommendations = computed(
  () =>
    [recommendedContext.value, recommendedOutput.value].every(
      (value) => Number.isSafeInteger(value) && value >= 0,
    ) && recommendedContext.value !== 1,
);
const ready = computed(
  () =>
    !!rows.value.length &&
    validPrice.value &&
    validRecommendations.value &&
    pendingRows.value.length > 0 &&
    pendingRows.value.every((v) => !rowError(v, rows.value, existing.value)),
);
const selectedCount = computed(
  () => rows.value.filter((v) => v.status === 'saved').length,
);
watch(
  () =>
    JSON.stringify([
      inputPrice.value,
      outputPrice.value,
      rows.value.map((row) => [
        row.id,
        row.context,
        row.output,
        row.contextMode,
        row.outputMode,
      ]),
    ]),
  () => {
    confirmed.value = false;
  },
);
watch([recommendedContext, recommendedOutput], () => {
  if ((recommendedContext.value as unknown) === '')
    recommendedContext.value = 0;
  if ((recommendedOutput.value as unknown) === '') recommendedOutput.value = 0;
  for (const row of rows.value) updateRow(row);
  confirmed.value = false;
});
function stop() {
  batchController?.abort();
}
function close() {
  stop();
  catalogController?.abort();
  emit('close');
}
onBeforeUnmount(() => {
  alive = false;
  epoch++;
  catalogController?.abort();
  stop();
});
watch(writable, (value) => {
  if (!value) {
    stop();
    catalogController?.abort();
    rows.value = [];
    models.value = [];
  }
});
async function fetchModels() {
  epoch++;
  const current = epoch;
  catalogController?.abort();
  models.value = [];
  selected.value = [];
  rows.value = [];
  preview.value = false;
  confirmed.value = false;
  failure.value = '';
  query.value = '';
  inputPrice.value = '';
  outputPrice.value = '';
  recommendedContext.value = 0;
  recommendedOutput.value = 0;
  if (!providerId.value || !writable.value) return;
  catalogController = new globalThis.AbortController();
  loading.value = true;
  try {
    const result = await relayApi.providerModels(
      providerId.value,
      catalogController.signal,
    );
    if (alive && current === epoch) models.value = result.items;
  } catch (e) {
    if (alive && current === epoch && !catalogController.signal.aborted)
      failure.value =
        isApiError(e) && [401, 403].includes(e.status)
          ? t('forbidden')
          : t('catalog_failed');
  } finally {
    if (alive && current === epoch) loading.value = false;
  }
}
onMounted(fetchModels);
function selectVisible(value: boolean) {
  const ids = visible.value
    .filter((v) => !value || !existing.value.has(suggestPublicId(v.id)))
    .map((v) => v.id);
  selected.value = value
    ? [...new Set([...selected.value, ...ids])]
    : selected.value.filter((v) => !ids.includes(v));
}
function aliasChanged(row: ImportRow) {
  if (row.status === 'conflict' && !row.body) {
    row.status = 'pending';
    row.message = '';
  }
}
function prepare() {
  rows.value = models.value
    .filter((v) => selected.value.includes(v.id))
    .map((model) => {
      const limits = capabilityLimits(model);
      return {
        model,
        id: suggestPublicId(model.id),
        context: limits.context,
        output: limits.output,
        contextMode: 'inherit',
        outputMode: 'inherit',
        key: globalThis.crypto.randomUUID(),
        status: 'pending',
        message: '',
      };
    });
  preview.value = true;
  confirmed.value = false;
}
function suggestUnknown() {
  for (const row of rows.value) {
    if (row.body) continue;
    row.context ??= 0;
    row.output ??= 0;
  }
  confirmed.value = false;
}
function updateRow(row: ImportRow) {
  if (row.body) return;
  const limits = capabilityLimits(row.model);
  if (row.contextMode !== 'manual')
    row.context = limits.context ?? recommendedContext.value;
  if (row.outputMode !== 'manual')
    row.output = limits.output ?? recommendedOutput.value;
}
function source(model: CatalogModel) {
  const limits = capabilityLimits(model);
  return [
    limits.context === undefined || limits.output === undefined
      ? t('importUnknown')
      : t('importFromProvider'),
    limits.inputOnly ? t('importInputOnly') : '',
    limits.limited ? t('importPlatformLimit') : '',
  ]
    .filter(Boolean)
    .join(' · ');
}
async function run() {
  if (!ready.value || !confirmed.value || running.value || !writable.value)
    return;
  running.value = true;
  emit('running', true);
  failure.value = '';
  batchController = new globalThis.AbortController();
  const signal = batchController.signal;
  try {
    for (const row of pendingRows.value) {
      if (signal.aborted || !alive || !writable.value) break;
      row.status = 'saving';
      row.message = '';
      try {
        const membership = await adminApi.membership();
        signal.throwIfAborted();
        emit('access', membership.permissions);
        if (
          !membership.permissions.includes('ai.providers.write') ||
          !membership.permissions.includes('ai.providers.read')
        ) {
          failure.value = t('forbidden');
          break;
        }
        const current = await relayApi.pools(signal);
        signal.throwIfAborted();
        const found = current.items.find((v) => v.id === row.id);
        if (found) {
          if (row.attempted && row.body && samePool(found, row.body)) {
            row.status = 'saved';
            continue;
          }
          row.status = 'conflict';
          row.message = t('importExists');
          continue;
        }
        if (!row.body) {
          updateRow(row);
          row.body = {
            id: row.id,
            enabled: true,
            contextWindow: row.context ?? 0,
            maxOutputTokens: row.output ?? 0,
            routes: [
              { providerId: providerId.value, model: row.model.id, weight: 1 },
            ],
            price: {
              currency: 'USD',
              inputMicrosPerMillionTokens: Number(inputPrice.value),
              outputMicrosPerMillionTokens: Number(outputPrice.value),
              version: globalThis.crypto.randomUUID(),
              effectiveAt: new Date().toISOString(),
            },
          };
          row.body.capabilities = createCapabilityPolicy(row.body);
          row.body.capabilities.contextWindowMode =
            row.contextMode ?? 'inherit';
          row.body.capabilities.maxOutputTokensMode =
            row.outputMode ?? 'inherit';
          row.body.capabilities.recommendedContextWindow =
            recommendedContext.value;
          row.body.capabilities.recommendedMaxOutputTokens =
            recommendedOutput.value;
          captureUpstreamCapabilities(row.body, row.model);
        }
        signal.throwIfAborted();
        row.attempted = true;
        await relayApi.createPool(row.body, row.key, signal);
        row.status = 'saved';
      } catch (e) {
        row.status =
          isApiError(e) && [409, 412].includes(e.status)
            ? 'conflict'
            : 'failed';
        row.message =
          row.status === 'conflict'
            ? t('importExists')
            : isApiError(e) && e.problem.code === 'iam.step_up_required'
              ? t('stepup')
              : isApiError(e) && [401, 403].includes(e.status)
                ? t('forbidden')
                : t('importWriteUnknown');
        if (signal.aborted || (isApiError(e) && [401, 403].includes(e.status)))
          break;
      }
    }
  } finally {
    for (const row of rows.value)
      if (row.status === 'saving') {
        row.status = 'failed';
        row.message = failure.value || t('importWriteUnknown');
      }
    if (alive) {
      running.value = false;
      emit('running', false);
      emit('imported');
    }
  }
}
</script>
<template>
  <section
    class="relay-import"
    data-testid="model-import-panel"
    :aria-label="t('importTitle')"
  >
    <header>
      <div>
        <h2>{{ t('importTitle') }}</h2>
        <p>{{ t('importIntro') }}</p>
      </div>
      <button type="button" @click="close">{{ t('close') }}</button>
    </header>
    <p class="relay-hint">{{ t('importCatalogOnly') }}</p>
    <p v-if="failure" role="alert">{{ failure }}</p>
    <div class="relay-toolbar">
      <label
        >{{ t('providers')
        }}<select
          v-model="providerId"
          name="importProvider"
          :disabled="running || frozen"
          @change="fetchModels"
        >
          <option
            v-for="p in providers"
            :key="p.id"
            :value="p.id"
            :disabled="!p.hasCredential || !p.configuration.enabled"
          >
            {{ p.configuration.name }}
          </option>
        </select></label
      >
      <button
        type="button"
        :disabled="loading || running || frozen || !writable"
        @click="fetchModels"
      >
        {{ t('fetchModels') }}
      </button>
    </div>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <template v-if="!preview">
      <div class="relay-toolbar">
        <label class="relay-search"
          >{{ t('importSearch')
          }}<input
            v-model="query"
            name="importSearch"
            type="search"
            :placeholder="t('importSearch')" /></label
        ><button type="button" @click="selectVisible(true)">
          {{ t('importSelectVisible') }}</button
        ><button type="button" @click="selectVisible(false)">
          {{ t('importClearVisible') }}</button
        ><span>{{ t('importSelected', { count: selected.length }) }}</span>
      </div>
      <div class="relay-catalog" :aria-label="t('models')">
        <label
          v-for="model in visible"
          :key="model.id"
          class="relay-catalog-row"
          ><input
            v-model="selected"
            type="checkbox"
            :value="model.id"
            :disabled="!writable"
          /><span
            ><strong>{{ model.id }}</strong
            ><small>{{
              existing.has(suggestPublicId(model.id))
                ? t('importExistingAlias')
                : source(model)
            }}</small></span
          ><span class="relay-capacity"
            >{{ capabilityLimits(model).context?.toLocaleString() ?? '—' }} /
            {{ capabilityLimits(model).output?.toLocaleString() ?? '—' }}</span
          ></label
        >
        <p v-if="!loading && !visible.length">{{ t('catalogEmpty') }}</p>
      </div>
      <button
        type="button"
        class="relay-primary"
        data-testid="import-preview"
        :disabled="!selected.length || !writable"
        @click="prepare"
      >
        {{ t('importPreview', { count: selected.length }) }}
      </button>
    </template>
    <template v-else>
      <div class="relay-toolbar">
        <button
          type="button"
          :disabled="running || frozen"
          @click="
            preview = false;
            confirmed = false;
          "
        >
          {{ t('importBack') }}</button
        ><button
          type="button"
          :disabled="running || frozen"
          data-testid="import-use-suggested"
          @click="suggestUnknown"
        >
          {{ t('importSuggest') }}
        </button>
        <p>{{ t('importSuggestNote') }}</p>
      </div>
      <div class="usage-table relay-import-table">
        <table>
          <thead>
            <tr>
              <th>{{ t('model') }}</th>
              <th>{{ t('poolId') }}</th>
              <th>{{ t('context') }}</th>
              <th>{{ t('output') }}</th>
              <th>{{ t('source') }}</th>
              <th>{{ t('status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(row, index) in rows"
              :key="row.key"
              :data-testid="'import-row-' + index"
            >
              <td>{{ row.model.id }}</td>
              <td>
                <input
                  v-model="row.id"
                  :aria-label="t('poolId') + ' ' + row.model.id"
                  :name="'importAlias' + index"
                  :disabled="running || !!row.body"
                  maxlength="128"
                  @input="aliasChanged(row)"
                /><small v-if="rowError(row, rows, existing)" role="alert">{{
                  t(rowError(row, rows, existing)!)
                }}</small>
              </td>
              <td>
                <select
                  v-model="row.contextMode"
                  :name="'importContextMode' + index"
                  :disabled="running || !!row.body"
                  @change="updateRow(row)"
                >
                  <option value="inherit">{{ t('capabilityInherit') }}</option>
                  <option value="manual">{{ t('capabilityManual') }}</option>
                </select>
                <input
                  v-model.number="row.context"
                  :name="'importContext' + index"
                  type="number"
                  min="0"
                  max="9007199254740991"
                  :aria-label="t('context') + ' ' + row.model.id"
                  :disabled="running || !!row.body"
                  @input="row.contextMode = 'manual'"
                />
              </td>
              <td>
                <select
                  v-model="row.outputMode"
                  :name="'importOutputMode' + index"
                  :disabled="running || !!row.body"
                  @change="updateRow(row)"
                >
                  <option value="inherit">{{ t('capabilityInherit') }}</option>
                  <option value="manual">{{ t('capabilityManual') }}</option>
                </select>
                <input
                  v-model.number="row.output"
                  :name="'importOutput' + index"
                  type="number"
                  min="0"
                  max="9007199254740991"
                  :aria-label="t('output') + ' ' + row.model.id"
                  :disabled="running || !!row.body"
                  @input="row.outputMode = 'manual'"
                />
              </td>
              <td>{{ source(row.model) }}</td>
              <td>
                <span :class="'relay-status-' + row.status">{{
                  t('importStatus_' + row.status)
                }}</span
                ><small v-if="row.message">{{ row.message }}</small>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="relay-form-grid">
        <label
          >{{ t('capabilityRecommendedContext')
          }}<input
            v-model.number="recommendedContext"
            name="importRecommendedContext"
            type="number"
            min="0"
            max="9007199254740991"
            :disabled="running || frozen"
        /></label>
        <label
          >{{ t('capabilityRecommendedOutput')
          }}<input
            v-model.number="recommendedOutput"
            name="importRecommendedOutput"
            type="number"
            min="0"
            max="9007199254740991"
            :disabled="running || frozen"
        /></label>
        <label
          >{{ t('priceInput')
          }}<input
            v-model.number="inputPrice"
            name="importInputPrice"
            type="number"
            min="1"
            step="1"
            :disabled="running || frozen" /></label
        ><label
          >{{ t('priceOutput')
          }}<input
            v-model.number="outputPrice"
            name="importOutputPrice"
            type="number"
            min="1"
            step="1"
            :disabled="running || frozen"
        /></label>
        <p class="relay-wide">{{ t('importPriceNote') }}</p>
      </div>
      <label class="relay-check"
        ><input
          v-model="confirmed"
          type="checkbox"
          name="importConfirmed"
          :disabled="running"
        />{{ t('importConfirm', { count: pendingRows.length }) }}</label
      >
      <div class="relay-toolbar">
        <button
          type="button"
          class="relay-primary"
          data-testid="import-run"
          :disabled="!ready || !confirmed || running || !writable"
          @click="run"
        >
          {{ t(frozen ? 'importRetry' : 'importRun') }}</button
        ><button
          v-if="running"
          type="button"
          data-testid="import-stop"
          @click="stop"
        >
          {{ t('importStop') }}</button
        ><span role="status">{{
          t('importProgress', { saved: selectedCount, total: rows.length })
        }}</span>
      </div>
      <p v-if="selectedCount" class="relay-hint">{{ t('importSavedNote') }}</p>
    </template>
  </section>
</template>
