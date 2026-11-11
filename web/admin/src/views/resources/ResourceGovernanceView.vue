<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { adminApi } from '../../admin/api';
import { resourceApi, type ResourceVersion } from '../../admin/resourceApi';
import { useAdminI18n } from '../../admin/messages';
import '../../admin/resources.css';

const props = defineProps<{ phase: 'review' | 'published' | 'distribution' }>();
const { t } = useAdminI18n();
const items = shallowRef<ResourceVersion[]>([]);
const selected = shallowRef<ResourceVersion>();
const error = ref('');
const loading = ref(false);
const allowed = ref(false);
const nextCursor = ref<string>();
const history = ref<(string | undefined)[]>([]);
const cursor = ref<string>();
let epoch = 0;
let alive = true;
const permission = computed(() =>
  props.phase === 'review'
    ? 'resource.versions.review'
    : props.phase === 'published'
      ? 'resource.versions.publish'
      : 'resource.distributions.manage',
);
async function load(target?: string, direction?: 'next' | 'previous') {
  const request = ++epoch;
  const phase = props.phase;
  items.value = [];
  selected.value = undefined;
  nextCursor.value = undefined;
  allowed.value = false;
  error.value = '';
  loading.value = true;
  try {
    const member = await adminApi.membership();
    if (!alive || request !== epoch) return;
    if (!member.permissions.includes(permission.value)) {
      error.value = t('forbidden');
      return;
    }
    allowed.value = true;
    if (phase === 'distribution') return;
    const page = await resourceApi.governance(phase, target);
    if (!alive || request !== epoch) return;
    items.value = page.items;
    nextCursor.value = page.nextCursor;
    if (direction === 'next') history.value.push(cursor.value);
    if (direction === 'previous') history.value.pop();
    cursor.value = target;
  } catch {
    if (alive && request === epoch) error.value = t('marketLoadFailed');
  } finally {
    if (alive && request === epoch) loading.value = false;
  }
}
watch(
  () => props.phase,
  () => {
    history.value = [];
    cursor.value = undefined;
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
});
</script>
<template>
  <section class="resource-page market-governance">
    <header class="market-toolbar">
      <h1>{{ t('market' + phase) }}</h1>
      <button :disabled="loading" @click="load(cursor)">
        {{ t('refresh') }}
      </button>
    </header>
    <nav class="market-tabs" :aria-label="t('marketNavigation')">
      <RouterLink to="/resources">{{ t('marketDirectory') }}</RouterLink>
      <RouterLink
        v-for="tab in ['review', 'published', 'distribution']"
        :key="tab"
        :to="'/resources/' + tab"
        >{{ t('market' + tab) }}</RouterLink
      >
    </nav>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <template v-if="allowed && !loading && !error">
      <p
        v-if="phase === 'distribution'"
        data-testid="not-connected"
        role="status"
      >
        {{ t('marketDistributionMissing') }}
      </p>
      <template v-else>
        <p role="note">{{ t('marketScope') }}</p>
        <p data-testid="not-connected" role="status">
          {{
            t(
              phase === 'review'
                ? 'marketReviewMissing'
                : 'marketPublishMissing',
            )
          }}
        </p>
        <div class="market-table">
          <table>
            <thead>
              <tr>
                <th>{{ t('marketPackage') }}</th>
                <th>{{ t('marketVersion') }}</th>
                <th>{{ t('status') }}</th>
                <th>{{ t('marketCreated') }}</th>
                <th>{{ t('details') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in items" :key="row.id">
                <td>{{ row.packageId }}</td>
                <td>{{ row.version }}</td>
                <td>{{ t('resourceStateLabels.' + row.state) }}</td>
                <td>{{ row.createdAt }}</td>
                <td>
                  <button @click="selected = row">{{ t('details') }}</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!items.length">{{ t('empty') }}</p>
        <div class="market-toolbar">
          <button
            :disabled="!history.length || loading"
            @click="load(history[history.length - 1], 'previous')"
          >
            {{ t('marketPrevious') }}
          </button>
          <button
            data-testid="next"
            :disabled="!nextCursor || loading"
            @click="load(nextCursor, 'next')"
          >
            {{ t('marketNext') }}
          </button>
        </div>
        <aside v-if="selected" :aria-label="t('details')">
          <header class="market-toolbar">
            <h2>{{ selected.version }}</h2>
            <button @click="selected = undefined">{{ t('close') }}</button>
          </header>
          <dl>
            <dt>{{ t('marketPackage') }}</dt>
            <dd>{{ selected.packageId }}</dd>
            <dt>{{ t('marketDigest') }}</dt>
            <dd>{{ selected.digest }}</dd>
            <dt>{{ t('marketSize') }}</dt>
            <dd>{{ selected.sizeBytes }}</dd>
            <dt>{{ t('marketRevision') }}</dt>
            <dd>{{ selected.revision }}</dd>
          </dl>
          <p>{{ t('marketEvidenceMissing') }}</p>
        </aside>
      </template>
    </template>
  </section>
</template>
<style scoped>
.market-governance {
  padding: 24px;
}
.market-toolbar,
.market-tabs {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
}
.market-toolbar h1,
.market-toolbar h2 {
  flex: 1;
  margin: 0;
}
.market-tabs {
  border-bottom: 1px solid var(--border-color, #304253);
  padding-bottom: 12px;
}
.market-table {
  overflow-x: auto;
}
table {
  width: 100%;
  text-align: left;
  border-collapse: collapse;
}
th,
td {
  padding: 12px;
  border-bottom: 1px solid var(--border-color, #304253);
}
aside {
  margin-top: 24px;
  padding: 20px;
  border: 1px solid var(--border-color, #304253);
  border-radius: 16px;
}
dd {
  overflow-wrap: anywhere;
  margin: 4px 0 16px;
}
</style>
