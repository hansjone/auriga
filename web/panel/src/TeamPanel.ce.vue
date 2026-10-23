<script setup lang="ts">
/* global HTMLElement, AbortController, AbortSignal, CustomEvent, clearTimeout */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { messages } from './messages';
import {
  canReadResources,
  createResourceReader,
  ResourceError,
  type Failure,
  type PanelContext,
  type ResourcePackage,
  type ResourceVersion,
} from './resources';

const props = defineProps<{ context?: PanelContext }>();
const text = computed(
  () => messages[props.context?.locale?.startsWith('en') ? 'en' : 'zh-CN'],
);
const root = ref<HTMLElement>();
const tab = ref<'market' | 'mine'>('market');
const search = ref('');
const packages = ref<ResourcePackage[]>([]);
const nextCursor = ref<string>();
const loaded = ref(false);
const selectedId = ref<string>();
const detail = ref<ResourcePackage>();
const versions = ref<ResourceVersion[]>([]);
const versionCursor = ref<string>();
const busy = ref(false);
const failure = ref<Failure>();
const readable = computed(() => canReadResources(props.context));
const blocked = computed(() => {
  if (props.context?.protocol !== undefined && props.context.protocol !== 1)
    return text.value.protocol;
  return props.context?.mode === 'managed'
    ? text.value.managed
    : text.value.disconnected;
});
const filtered = computed(() =>
  packages.value.filter((item) =>
    item.name
      .toLocaleLowerCase()
      .includes(search.value.trim().toLocaleLowerCase()),
  ),
);
let epoch = 0;
let controller: AbortController | undefined;
let retryAction: (() => void) | undefined;

function cancel() {
  epoch++;
  controller?.abort();
  busy.value = false;
}

function clearData() {
  packages.value = [];
  nextCursor.value = undefined;
  loaded.value = false;
  detail.value = undefined;
  versions.value = [];
  versionCursor.value = undefined;
}

async function run<T>(
  operation: (signal: AbortSignal) => Promise<T>,
  apply: (result: T) => void,
  retry: () => void,
) {
  cancel();
  if (!readable.value) return;
  const request = epoch;
  const active = new AbortController();
  controller = active;
  busy.value = true;
  failure.value = undefined;
  retryAction = retry;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let onAbort: (() => void) | undefined;
  try {
    const result = await Promise.race([
      operation(active.signal),
      new Promise<never>((_, reject) => {
        onAbort = () => reject(new ResourceError('unavailable'));
        active.signal.addEventListener('abort', onAbort, { once: true });
        timer = setTimeout(() => {
          active.abort();
        }, 15_000);
      }),
    ]);
    if (request === epoch) apply(result);
  } catch (error) {
    if (request !== epoch) return;
    failure.value =
      error instanceof ResourceError ? error.reason : 'unavailable';
    // A failed live read must not leave a cached market looking available.
    clearData();
    if (failure.value === 'auth')
      root.value?.dispatchEvent(
        new CustomEvent('auriga-auth-required', {
          bubbles: true,
          composed: true,
        }),
      );
  } finally {
    clearTimeout(timer);
    if (onAbort) active.signal.removeEventListener('abort', onAbort);
    active.abort();
    if (request === epoch) busy.value = false;
  }
}

function loadPackages(more = false) {
  if (!readable.value) return;
  const reader = createResourceReader(props.context!);
  const cursor = more ? nextCursor.value : undefined;
  if (more && !cursor) return;
  if (!more) {
    packages.value = [];
    nextCursor.value = undefined;
    loaded.value = false;
  }
  void run(
    (signal) => reader.list(signal, cursor),
    (page) => {
      // Repeated cursors must never turn into an unbounded pagination loop.
      if (cursor && page.nextCursor === cursor)
        throw new ResourceError('invalid');
      packages.value = [
        ...new Map(
          [...packages.value, ...page.items].map((item) => [item.id, item]),
        ).values(),
      ];
      nextCursor.value = page.nextCursor;
      loaded.value = true;
    },
    () => loadPackages(more && !!nextCursor.value),
  );
}

function openDetail(packageId: string) {
  if (!readable.value) return;
  selectedId.value = packageId;
  detail.value = undefined;
  versions.value = [];
  versionCursor.value = undefined;
  const reader = createResourceReader(props.context!);
  void run(
    (signal) =>
      Promise.all([
        reader.detail(packageId, signal),
        reader.versions(packageId, signal),
      ]),
    ([item, page]) => {
      detail.value = item;
      versions.value = page.items;
      versionCursor.value = page.nextCursor;
    },
    () => openDetail(packageId),
  );
}

function moreVersions() {
  if (!readable.value || !selectedId.value || !versionCursor.value) return;
  const packageId = selectedId.value;
  const cursor = versionCursor.value;
  const reader = createResourceReader(props.context!);
  void run(
    (signal) => reader.versions(packageId, signal, cursor),
    (page) => {
      if (page.nextCursor === cursor) throw new ResourceError('invalid');
      versions.value = [
        ...new Map(
          [...versions.value, ...page.items].map((item) => [item.id, item]),
        ).values(),
      ];
      versionCursor.value = page.nextCursor;
    },
    () => (detail.value ? moreVersions() : openDetail(packageId)),
  );
}

function showTab(value: 'market' | 'mine') {
  cancel();
  tab.value = value;
  selectedId.value = undefined;
  detail.value = undefined;
  versions.value = [];
  failure.value = undefined;
  retryAction = undefined;
  if (value === 'market' && !loaded.value) loadPackages();
}

watch(
  () => props.context,
  () => {
    cancel();
    clearData();
    selectedId.value = undefined;
    failure.value = undefined;
    retryAction = undefined;
    if (tab.value === 'market') loadPackages();
  },
  { immediate: true },
);
onBeforeUnmount(cancel);
</script>

<template>
  <aside
    ref="root"
    class="resources"
    :data-theme="context?.theme ?? 'dark'"
    :aria-label="text.panel"
  >
    <span class="sr-only">{{ text.panel }}</span>
    <nav :aria-label="text.panel">
      <button
        type="button"
        :aria-pressed="tab === 'market'"
        @click="showTab('market')"
      >
        {{ text.market }}
      </button>
      <button
        type="button"
        :aria-pressed="tab === 'mine'"
        @click="showTab('mine')"
      >
        {{ text.mine }}
      </button>
    </nav>
    <section v-if="tab === 'mine'" class="notice" role="status">
      <p>{{ text.mineHint }}</p>
      <p>{{ text.installHint }}</p>
    </section>
    <section v-else-if="!readable" class="notice" role="status">
      {{ blocked }}
    </section>
    <section v-else :aria-busy="busy">
      <div v-if="!selectedId" class="toolbar">
        <span class="kind">{{ text.plugins }}</span>
        <input
          v-model="search"
          type="search"
          :aria-label="text.search"
          :placeholder="text.search"
        />
        <button type="button" :disabled="busy" @click="loadPackages()">
          {{ text.refresh }}
        </button>
      </div>
      <div v-else class="toolbar">
        <button type="button" @click="showTab('market')">
          ← {{ text.back }}
        </button>
        <button type="button" :disabled="busy" @click="openDetail(selectedId)">
          {{ text.refresh }}
        </button>
      </div>
      <div v-if="failure" class="notice error" role="alert">
        <p>{{ text[failure] }}</p>
        <button type="button" :disabled="busy" @click="retryAction?.()">
          {{ text.retry }}
        </button>
      </div>
      <p v-if="busy" role="status">{{ text.loading }}</p>
      <template v-if="!selectedId">
        <p v-if="loaded" class="muted">
          {{ text.loaded }} {{ packages.length }}
        </p>
        <div class="cards">
          <article v-for="item in filtered" :key="item.id" class="card">
            <h3>{{ item.name }}</h3>
            <p>{{ text.level }} · {{ item.level.toUpperCase() }}</p>
            <p class="muted">{{ text.author }} · {{ item.ownerId }}</p>
            <p>
              {{ text.availability }} ·
              {{ item.enabled ? text.enabled : text.disabled }}
            </p>
            <p class="muted">{{ text.installState }} · {{ text.unknown }}</p>
            <button type="button" @click="openDetail(item.id)">
              {{ text.details }}
            </button>
          </article>
        </div>
        <p
          v-if="loaded && !filtered.length && !busy && !failure"
          class="notice"
        >
          {{ packages.length ? text.noMatch : text.empty }}
        </p>
        <button
          v-if="nextCursor"
          type="button"
          :disabled="busy"
          @click="loadPackages(true)"
        >
          {{ text.more }}
        </button>
      </template>
      <article v-else-if="detail" class="detail">
        <h2>{{ detail.name }}</h2>
        <dl>
          <dt>{{ text.author }}</dt>
          <dd>{{ detail.ownerId }}</dd>
          <dt>{{ text.level }}</dt>
          <dd>{{ detail.level.toUpperCase() }}</dd>
          <dt>{{ text.availability }}</dt>
          <dd>{{ detail.enabled ? text.enabled : text.disabled }}</dd>
          <dt>{{ text.defaultVersion }}</dt>
          <dd>{{ detail.defaultVersionId ?? text.noDefault }}</dd>
          <dt>{{ text.installState }}</dt>
          <dd>{{ text.unknown }}</dd>
        </dl>
        <p class="notice">{{ text.installHint }}</p>
        <h3>{{ text.versions }}</h3>
        <ul class="versions">
          <li v-for="version in versions" :key="version.id">
            <strong>{{ version.version }}</strong
            ><span>{{ text[version.state] }}</span>
            <p class="muted">{{ text.digest }} · {{ version.digest }}</p>
          </li>
        </ul>
        <p v-if="!versions.length && !busy">{{ text.versionEmpty }}</p>
        <button
          v-if="versionCursor"
          type="button"
          :disabled="busy"
          @click="moreVersions"
        >
          {{ text.more }}
        </button>
      </article>
    </section>
  </aside>
</template>

<style src="@auriga/shared/theme.css"></style>
<style>
:host {
  display: block;
  height: 100%;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
}
.resources {
  --surface: var(--dsw-alias-bg-layer-1, #151b23);
  --ink: var(--dsw-alias-label-primary, #e6edf3);
  --line: var(--dsw-alias-border-l1, #384653);
  --muted: var(--dsw-alias-label-secondary, #a6b5c4);
  box-sizing: border-box;
  min-height: 100%;
  padding: 24px;
  background: var(--surface);
  color: var(--ink);
}
.resources[data-theme='light'] {
  --surface: var(--dsw-alias-bg-layer-1, #fff);
  --ink: var(--dsw-alias-label-primary, #202c38);
  --line: var(--dsw-alias-border-l1, #c4ced8);
  --muted: var(--dsw-alias-label-secondary, #526779);
}
* {
  box-sizing: border-box;
}
nav {
  display: flex;
  gap: 8px;
  border-bottom: 1px solid var(--line);
  padding-bottom: 12px;
  margin-bottom: 24px;
}
button,
input {
  font: inherit;
  color: inherit;
  border: 1px solid var(--line);
  border-radius: 7px;
  background: transparent;
  padding: 8px 12px;
}
button {
  cursor: pointer;
}
button:hover,
button[aria-pressed='true'] {
  background: color-mix(in srgb, var(--ink) 9%, transparent);
}
button:disabled {
  opacity: 0.55;
  cursor: default;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid #5794bc;
  outline-offset: 3px;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
}
.toolbar input {
  flex: 1;
  min-width: 180px;
}
.kind {
  font-weight: 600;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 270px), 1fr));
  gap: 16px;
  margin-bottom: 20px;
}
.card {
  padding: 20px;
  border: 1px solid var(--line);
  border-radius: 10px;
}
h2,
h3 {
  margin: 0 0 12px;
  font-weight: 600;
}
p,
dd,
h2,
h3 {
  overflow-wrap: anywhere;
}
.muted,
dt {
  color: var(--muted);
  font-size: 13px;
}
.notice {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 18px;
  line-height: 1.7;
  margin: 16px 0;
}
.notice p:first-child {
  margin-top: 0;
}
.notice p:last-child {
  margin-bottom: 0;
}
.error {
  border-inline-start: 3px solid #bf7955;
}
dl {
  display: grid;
  grid-template-columns: minmax(100px, auto) 1fr;
  gap: 12px 20px;
}
dd {
  margin: 0;
}
.versions {
  list-style: none;
  padding: 0;
}
.versions li {
  padding: 16px 0;
  border-bottom: 1px solid var(--line);
}
.versions span {
  margin-inline-start: 16px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
@media (max-width: 480px) {
  .resources {
    padding: 16px;
  }
  dl {
    display: block;
  }
  dd {
    margin: 6px 0 16px;
  }
}
</style>
