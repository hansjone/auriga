<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import {
  allPluginPackages,
  filterPlugins,
  marketAuthor,
  marketDate,
  marketDepartment,
  marketOptions,
  PluginPaginationError,
  type MarketSort,
  type PluginMarket,
} from '../../../shared/src/plugin-market';
import { apiClient } from '../api';
import { createPlacementApi, type Placement } from './placement-api';
import {
  createResourceMarketApi,
  versionIdentity,
  type Installation,
  type InstallationPage,
  type ResourcePackage,
  type ResourceVersion,
  type Selection,
} from './resource-market-api';
import { resourceMarketMessages } from './resource-market-messages';

const { t, locale } = useI18n({
  useScope: 'local',
  messages: resourceMarketMessages,
});
const api = createResourceMarketApi(apiClient);
const placement = ref<Placement>();
const state = ref<InstallationPage>();
const packages = ref<ResourcePackage[]>([]);
const filters = ref({
  search: '',
  author: '',
  department: '',
  tag: '',
  sort: '' as MarketSort,
});
const hasMarket = computed(() => packages.value.some((pack) => pack.market));
const options = computed(() => marketOptions(packages.value, locale.value));
const displayed = computed(() =>
  kind.value === 'plugin'
    ? filterPlugins(packages.value, filters.value)
    : packages.value,
);
function facts(market: PluginMarket): [string, string | number][] {
  const date = marketDate(market);
  return [
    ['author', marketAuthor(market) ?? t('unknown')],
    ['department', marketDepartment(market) ?? t('unknown')],
    [
      'publishedAt',
      date === undefined
        ? t('unknown')
        : new Intl.DateTimeFormat(locale.value, {
            dateStyle: 'medium',
            timeStyle: 'short',
          }).format(date),
    ],
    ['version', market.version],
    ['dshRange', market.dshVersionRange],
    ['tags', market.tags.join(' · ') || '—'],
    ['installationCount', market.installationCount],
    ['starCount', market.starCount],
  ];
}
const names = ref<Record<string, string>>({});
const selected = ref<ResourcePackage>();
const latest = ref<ResourceVersion>();
const kind = ref<'plugin' | 'skill'>('skill');
const next = ref<string>();
const busy = ref(false),
  expired = ref(false),
  error = ref(''),
  message = ref('');
const ready = computed(
  () =>
    placement.value?.state === 'ready' &&
    state.value &&
    state.value.placementId === placement.value.placementId,
);
const dedicated = computed(
  () => ready.value && state.value?.mode === 'dedicated',
);
const named = (row: Installation) =>
  names.value[row.identity.packageId] ?? t(row.identity.kind);
const current = computed(() =>
  state.value?.items.find(
    (row) => row.identity.packageId === selected.value?.id,
  ),
);
const same = computed(
  () =>
    current.value?.identity.versionId === latest.value?.id &&
    current.value?.desiredState !== 'REMOVED',
);
let generation = 0;
let abort = new globalThis.AbortController();
function fail(value: unknown) {
  expired.value = isApiError(value) && value.status === 401;
  error.value =
    t(
      expired.value
        ? 'expired'
        : value instanceof PluginPaginationError
          ? 'incomplete'
          : isApiError(value) &&
              value.problem.code === 'resource.shared_requires_dedicated'
            ? 'sharedHint'
            : 'failed',
    ) + (isApiError(value) ? ` (${value.problem.code})` : '');
}
async function run(action: (signal: globalThis.AbortSignal) => Promise<void>) {
  if (busy.value || expired.value) return;
  const version = ++generation;
  abort.abort();
  abort = new globalThis.AbortController();
  busy.value = true;
  error.value = '';
  try {
    await action(abort.signal);
  } catch (value) {
    if (version === generation) fail(value);
  } finally {
    if (version === generation) busy.value = false;
  }
}
async function load(signal: globalThis.AbortSignal) {
  state.value = undefined;
  packages.value = [];
  next.value = undefined;
  names.value = {};
  selected.value = undefined;
  latest.value = undefined;
  const value = await createPlacementApi(apiClient).read(signal);
  if (signal.aborted) return;
  placement.value = value;
  if (value.state !== 'ready' || !['shared', 'dedicated'].includes(value.mode))
    return;
  const snapshot = await api.installations(value.placementId, signal);
  if (signal.aborted) return;
  if (
    snapshot.mode !== value.mode ||
    snapshot.epoch !== value.epoch ||
    snapshot.revision !== value.revision
  )
    throw new Error('Placement changed; refresh');
  state.value = snapshot;
  // Names are display metadata from the same catalog, never installation authority.
  const ids = [...new Set(snapshot.items.map((row) => row.identity.packageId))];
  for (let index = 0; index < ids.length; index += 4) {
    const batch = ids.slice(index, index + 4);
    const results = await Promise.allSettled(
      batch.map((id) => api.package(id, signal)),
    );
    if (signal.aborted) return;
    results.forEach((result, i) => {
      if (
        result.status === 'fulfilled' &&
        typeof result.value.name === 'string'
      )
        names.value[batch[i]] = result.value.name;
    });
  }
  if (value.mode === 'dedicated') await catalog(signal);
}
async function catalog(signal: globalThis.AbortSignal, cursor?: string) {
  const requestedKind = kind.value;
  const read = async (cursor?: string) => {
    const result = await api.packages(requestedKind, signal, cursor);
    if (
      !Array.isArray(result.items) ||
      result.items.length > 200 ||
      result.items.some((pack) => !pack || pack.kind !== requestedKind) ||
      (result.nextCursor !== undefined && typeof result.nextCursor !== 'string')
    )
      throw new Error('Invalid resource page');
    return { packages: result.items, cursor: result.nextCursor || undefined };
  };
  const result =
    requestedKind === 'plugin'
      ? await allPluginPackages(read)
      : await read(cursor);
  if (signal.aborted) return;
  packages.value = cursor
    ? [...packages.value, ...result.packages]
    : result.packages;
  result.packages.forEach((pack) => {
    names.value[pack.id] = pack.name;
  });
  next.value = result.cursor;
}
function filter(value: 'plugin' | 'skill') {
  if (busy.value || expired.value || value === kind.value) return;
  kind.value = value;
  packages.value = [];
  next.value = undefined;
  selected.value = undefined;
  latest.value = undefined;
  run((signal) => catalog(signal));
}
function star(pack: ResourcePackage) {
  const market = pack.market;
  if (!market) return;
  run(async (signal) => {
    const result = await api.star(pack.id, !market.starred, signal);
    if (signal.aborted) return;
    const updated = { ...pack, market: { ...market, ...result } };
    packages.value = packages.value.map((item) =>
      item.id === pack.id ? updated : item,
    );
    if (selected.value?.id === pack.id) selected.value = updated;
  });
}
function choose(pack: ResourcePackage) {
  run(async (signal) => {
    latest.value = undefined;
    selected.value = pack;
    const result = await api.latest(pack, signal);
    if (!signal.aborted) latest.value = result;
  });
}
function select(selection: Selection, revision: number) {
  if (!dedicated.value || !placement.value) return;
  const id = placement.value.placementId;
  run(async (signal) => {
    message.value = '';
    await api.select(id, selection, revision, signal);
    if (signal.aborted) return;
    // HTTP 202 acknowledges intent only. Reload the authoritative view and receipt.
    state.value = undefined;
    message.value = t('accepted');
    const result = await api.installations(id, signal);
    if (!signal.aborted) state.value = result;
  });
}
function install() {
  if (!selected.value || !latest.value) return;
  select(
    {
      identity: versionIdentity(selected.value, latest.value),
      action: 'install',
    },
    current.value?.selectionRevision ?? 0,
  );
}
onMounted(() => run(load));
onBeforeUnmount(() => {
  generation++;
  abort.abort();
});
</script>
<template>
  <main class="resources auriga-glass" :aria-busy="busy">
    <a href="/">{{ t('home') }}</a>
    <h1>{{ t('title') }}</h1>
    <p v-if="ready">{{ t(state!.mode) }}</p>
    <p v-if="placement && !ready && !error" role="status">{{ t('waiting') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <a v-if="expired" href="/login">{{ t('signIn') }}</a>
    <ElButton :disabled="busy || expired" @click="run(load)">{{
      t('refresh')
    }}</ElButton>
    <p v-if="message" role="status">{{ message }}</p>
    <template v-if="ready">
      <p v-if="!dedicated">{{ t('sharedHint') }}</p>
      <h2>{{ t('installed') }}</h2>
      <p v-if="!state!.items.length">{{ t('empty') }}</p>
      <ul class="resource-list">
        <li v-for="row in state!.items" :key="row.identity.packageId">
          <div>
            <strong>{{ named(row) }}</strong> {{ row.identity.version }}
          </div>
          <p role="status">
            {{ t('states.' + row.state) }} · {{ t(row.installationMode) }}
          </p>
          <div
            v-if="
              dedicated &&
              row.state !== 'available' &&
              row.desiredState !== 'REMOVED'
            "
            class="actions"
          >
            <ElButton
              v-if="row.desiredState === 'DISABLED'"
              :disabled="busy"
              @click="
                select(
                  { identity: row.identity, action: 'enable' },
                  row.selectionRevision,
                )
              "
              >{{ t('enable') }}</ElButton
            >
            <template v-if="row.installationMode !== 'required'">
              <ElButton
                v-if="row.desiredState === 'ENABLED'"
                :disabled="busy"
                @click="
                  select(
                    { identity: row.identity, action: 'disable' },
                    row.selectionRevision,
                  )
                "
                >{{ t('disable') }}</ElButton
              >
              <ElButton
                :disabled="busy"
                @click="
                  select(
                    { identity: row.identity, action: 'remove' },
                    row.selectionRevision,
                  )
                "
                >{{ t('remove') }}</ElButton
              >
            </template>
          </div>
        </li>
      </ul>
      <section v-if="dedicated" :aria-label="t('catalog')">
        <h2>{{ t('catalog') }}</h2>
        <div class="actions">
          <ElButton
            v-for="value in ['skill', 'plugin'] as const"
            :key="value"
            :disabled="busy"
            :aria-pressed="kind === value"
            @click="filter(value)"
            >{{ t(value) }}</ElButton
          >
        </div>
        <div v-if="kind === 'plugin' && packages.length" class="market-filters">
          <input
            v-model="filters.search"
            type="search"
            :aria-label="t('search')"
            :placeholder="t('search')"
          />
          <template v-if="hasMarket">
            <label
              >{{ t('author') }}
              <select v-model="filters.author">
                <option value="">{{ t('all') }}</option>
                <option
                  v-for="[id, name] in options.authors"
                  :key="id"
                  :value="id"
                >
                  {{ name ?? `${t('unknown')} (${id})` }}
                </option>
              </select>
            </label>
            <label
              >{{ t('department') }}
              <select v-model="filters.department">
                <option value="">{{ t('all') }}</option>
                <option v-for="value in options.departments" :key="value">
                  {{ value }}
                </option>
              </select>
            </label>
            <label
              >{{ t('tags') }}
              <select v-model="filters.tag">
                <option value="">{{ t('all') }}</option>
                <option v-for="value in options.tags" :key="value">
                  {{ value }}
                </option>
              </select>
            </label>
            <label
              >{{ t('sort') }}
              <select v-model="filters.sort">
                <option value="">{{ t('catalogOrder') }}</option>
                <option
                  v-for="value in [
                    'publishedAt',
                    'installationCount',
                    'starCount',
                  ]"
                  :key="value"
                  :value="value"
                >
                  {{ t(value) }}
                </option>
              </select>
            </label>
          </template>
        </div>
        <p v-if="packages.length && !displayed.length" role="status">
          {{ t('noMatches') }}
        </p>
        <ul class="resource-list">
          <li v-for="pack in displayed" :key="pack.id">
            <strong>{{ pack.name }}</strong>
            <template v-if="pack.market">
              <p>{{ pack.market.description }}</p>
              <dl class="market-facts">
                <template
                  v-for="[label, value] in facts(pack.market)"
                  :key="label"
                >
                  <dt>{{ t(label) }}</dt>
                  <dd>{{ value }}</dd>
                </template>
              </dl>
              <ElButton
                :disabled="busy || expired"
                :aria-pressed="pack.market.starred"
                @click="star(pack)"
              >
                {{ t(pack.market.starred ? 'unstar' : 'star') }}
              </ElButton>
            </template>
            <ElButton :disabled="busy || !pack.enabled" @click="choose(pack)">{{
              t('versions')
            }}</ElButton>
          </li>
        </ul>
        <ElButton
          v-if="next"
          :disabled="busy"
          @click="run((signal) => catalog(signal, next))"
          >{{ t('more') }}</ElButton
        >
        <section v-if="selected" class="version" :aria-label="selected.name">
          <h3>{{ selected.name }}</h3>
          <p v-if="selected.market">
            {{ t('authorId') }} {{ selected.market.authorId }}
          </p>
          <template v-if="latest">
            <p>{{ latest.version }}</p>
            <p
              v-if="
                latest.manifest.kind === 'plugin' &&
                latest.manifest.plugin?.dshVersionRange
              "
            >
              {{ t('dshRange') }} {{ latest.manifest.plugin.dshVersionRange }}
            </p>
            <p v-if="same">{{ t('same') }}</p>
            <ElButton
              type="primary"
              :disabled="busy || same"
              @click="install"
              >{{ t('install') }}</ElButton
            >
          </template>
          <p v-else-if="!busy">{{ t('noVersion') }}</p>
        </section>
      </section>
    </template>
  </main>
</template>
<style scoped>
.resources {
  width: min(760px, calc(100% - 2rem));
  margin: 1.75rem auto 2.5rem;
}
.resources > a:first-child {
  display: inline-block;
  margin-bottom: 0.85rem;
  font-size: 0.8125rem;
  color: var(--auriga-muted);
  text-decoration: none;
}
.resources > a:first-child:hover {
  color: var(--auriga-accent-line);
}
.resources h1 {
  margin: 0 0 0.4rem;
  font-size: 1.5rem;
  font-weight: 600;
  letter-spacing: -0.03em;
  line-height: 1.3;
}
.resources h2 {
  margin: 1.5rem 0 0.75rem;
  font-size: 1.0625rem;
  font-weight: 600;
}
.resources > p {
  margin: 0.35rem 0 0.75rem;
  color: var(--auriga-muted);
  font-size: 0.875rem;
  line-height: 1.55;
}
a {
  color: var(--auriga-accent-line);
}
.resource-list {
  padding: 0;
  margin: 0;
  list-style: none;
}
.resource-list li {
  padding: 0.9rem 0;
  border-bottom: 1px solid var(--auriga-border);
  overflow-wrap: anywhere;
}
.resource-list li:last-child {
  border-bottom: 0;
}
.resource-list p {
  color: var(--auriga-muted);
  margin: 0.4rem 0;
  font-size: 0.8125rem;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin: 0.5rem 0;
}
.actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.market-filters {
  display: flex;
  align-items: end;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin: 0.75rem 0;
}
.market-filters label {
  display: grid;
  gap: 0.25rem;
  font-size: 0.8125rem;
}
.market-filters :is(input, select) {
  max-width: min(100%, 250px);
  font: inherit;
  color: inherit;
  background: var(--auriga-surface, Canvas);
  border: 1px solid var(--auriga-border);
  border-radius: 4px;
  padding: 0.35rem 0.5rem;
}
.market-facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  column-gap: 1rem;
  row-gap: 0.25rem;
  font-size: 0.8125rem;
}
.market-facts dd {
  margin: 0;
}
.market-facts dt {
  color: var(--auriga-muted);
}
.version {
  margin-top: 1rem;
  border-left: 2px solid var(--auriga-accent-line);
  padding: 0.25rem 0 0.25rem 1rem;
}
.version h3 {
  margin: 0 0 0.4rem;
  font-size: 1rem;
  font-weight: 600;
}
@media (max-width: 600px) {
  .resources {
    margin-inline: 1rem;
    width: calc(100% - 2rem);
  }
}
</style>
