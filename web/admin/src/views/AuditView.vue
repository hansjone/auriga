<script setup lang="ts">
import { onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../admin/api';
import {
  auditApi,
  type AuditEvent,
  type AuditFilters,
} from '../admin/auditApi';
import { useAuditI18n } from '../admin/auditMessages';
const { t } = useAuditI18n();
const filters = reactive<AuditFilters>({
  from: '',
  to: '',
  actor: '',
  action: '',
  result: '',
  q: '',
});
const rows = ref<AuditEvent[]>([]),
  selected = ref<AuditEvent>();
const loading = ref(false),
  error = ref(''),
  allowed = ref(false);
const next = ref<string | null>(),
  history = ref<(string | undefined)[]>([]);
let current: string | undefined,
  applied: AuditFilters = { ...filters },
  version = 0,
  alive = true;
const clear = () => {
  version++;
  allowed.value = false;
  rows.value = [];
  selected.value = undefined;
  next.value = undefined;
  history.value = [];
  current = undefined;
  loading.value = false;
};
watch(filters, clear, { flush: 'sync' });
onBeforeUnmount(() => {
  alive = false;
  clear();
});
function failure(e: unknown) {
  return isApiError(e) && (e.status === 401 || e.status === 403)
    ? t('forbidden')
    : t('failed');
}
async function check() {
  const member = await adminApi.membership();
  if (!member.permissions.includes('audit.events.read'))
    throw new Error('forbidden');
}
async function query(cursor?: string, back = false) {
  const id = ++version;
  rows.value = [];
  selected.value = undefined;
  error.value = '';
  loading.value = true;
  allowed.value = false;
  try {
    await check();
    if (!alive || id !== version) return;
    allowed.value = true;
    const normalized = {
      ...filters,
      from: filters.from ? new Date(filters.from).toISOString() : '',
      to: filters.to ? new Date(filters.to).toISOString() : '',
    };
    if (
      normalized.from &&
      normalized.to &&
      Date.parse(normalized.from) >= Date.parse(normalized.to)
    ) {
      error.value = t('invalid');
      return;
    }
    const page = await auditApi.list(normalized, cursor);
    if (!alive || id !== version) return;
    if (cursor && !back) history.value.push(current);
    if (!cursor && !back) history.value = [];
    current = cursor;
    applied = normalized;
    rows.value = page.items;
    next.value = page.nextCursor;
  } catch (e) {
    if (alive && id === version) {
      error.value =
        e instanceof Error && e.message === 'forbidden'
          ? t('forbidden')
          : failure(e);
      next.value = undefined;
    }
  } finally {
    if (alive && id === version) loading.value = false;
  }
}
async function detail(row: AuditEvent) {
  const id = ++version;
  loading.value = true;
  error.value = '';
  selected.value = undefined;
  try {
    await check();
    if (!alive || id !== version) return;
    const result = await auditApi.detail(row.id);
    if (alive && id === version) selected.value = result;
  } catch (e) {
    if (alive && id === version) {
      rows.value = [];
      allowed.value = false;
      error.value =
        e instanceof Error && e.message === 'forbidden'
          ? t('forbidden')
          : failure(e);
    }
  } finally {
    if (alive && id === version) loading.value = false;
  }
}
async function download() {
  const id = ++version;
  loading.value = true;
  error.value = '';
  try {
    await check();
    if (!alive || id !== version) return;
    const { csv } = await auditApi.export(applied);
    if (!alive || id !== version) return;
    const url = URL.createObjectURL(
      new globalThis.Blob([csv], { type: 'text/csv;charset=utf-8' }),
    );
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = 'audit.csv';
    anchor.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    if (alive && id === version) {
      if (
        (e instanceof Error && e.message === 'forbidden') ||
        (isApiError(e) && (e.status === 401 || e.status === 403))
      ) {
        rows.value = [];
        selected.value = undefined;
        next.value = undefined;
        allowed.value = false;
      }
      error.value =
        e instanceof Error && e.message === 'forbidden'
          ? t('forbidden')
          : failure(e);
    }
  } finally {
    if (alive && id === version) loading.value = false;
  }
}
function previous() {
  const cursor = history.value.pop();
  void query(cursor, true);
}
onMounted(() => void query());
</script>
<template>
  <section class="audit-page">
    <header>
      <h1>{{ t('title') }}</h1>
      <button
        data-export
        :disabled="loading || !allowed || !rows.length"
        @click="download"
      >
        {{ t('export') }}
      </button>
    </header>
    <form @submit.prevent="query()">
      <label
        >{{ t('from')
        }}<input v-model="filters.from" name="from" type="datetime-local"
      /></label>
      <label
        >{{ t('to')
        }}<input v-model="filters.to" name="to" type="datetime-local"
      /></label>
      <label
        >{{ t('actor')
        }}<input v-model="filters.actor" name="actor" maxlength="200"
      /></label>
      <label
        >{{ t('action')
        }}<input v-model="filters.action" name="action" maxlength="200"
      /></label>
      <label
        >{{ t('result')
        }}<select v-model="filters.result" name="result">
          <option value="">{{ t('all') }}</option>
          <option
            v-for="value in ['success', 'failure', 'denied']"
            :key="value"
            :value="value"
          >
            {{ t(value) }}
          </option>
        </select></label
      >
      <label
        >{{ t('q') }}<input v-model="filters.q" name="q" maxlength="200"
      /></label>
      <button :disabled="loading" type="submit">{{ t('query') }}</button>
    </form>
    <p class="muted">{{ t('scope') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <div class="audit-table">
      <table v-if="rows.length">
        <thead>
          <tr>
            <th
              v-for="key in [
                'time',
                'actor',
                'action',
                'target',
                'result',
                'ip',
                'trace',
                'detail',
              ]"
              :key="key"
            >
              {{ t(key) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.id">
            <td>{{ new Date(row.time).toLocaleString() }}</td>
            <td>{{ row.actor.id || row.actor.type }}</td>
            <td>{{ row.action }}</td>
            <td>{{ row.target?.name || row.target?.id || '—' }}</td>
            <td>{{ t(row.result) }}</td>
            <td>{{ row.actor.ip || '—' }}</td>
            <td class="trace">{{ row.traceId || '—' }}</td>
            <td>
              <button data-detail :disabled="loading" @click="detail(row)">
                {{ t('detail') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="!loading && !error && !rows.length">{{ t('empty') }}</p>
    <footer>
      <button :disabled="loading || !history.length" @click="previous">
        {{ t('previous') }}</button
      ><button data-next :disabled="loading || !next" @click="query(next!)">
        {{ t('next') }}</button
      ><span class="muted">{{ t('limit') }}</span>
    </footer>
    <aside
      v-if="selected"
      role="dialog"
      aria-modal="true"
      :aria-label="t('detail')"
      tabindex="-1"
      @keydown.esc="selected = undefined"
    >
      <header>
        <h2>{{ t('detail') }}</h2>
        <button autofocus @click="selected = undefined">
          {{ t('close') }}
        </button>
      </header>
      <p class="muted">{{ t('safe') }}</p>
      <dl>
        <template v-for="(value, key) in selected" :key="key"
          ><dt>{{ key }}</dt>
          <dd>
            <pre>{{
              typeof value === 'object' ? JSON.stringify(value, null, 2) : value
            }}</pre>
          </dd></template
        >
      </dl>
    </aside>
  </section>
</template>
<style scoped>
.audit-page {
  padding: 24px;
  color: var(--auriga-text);
  font-family: inherit;
}
header,
footer {
  display: flex;
  align-items: center;
  gap: 12px;
}
header {
  justify-content: space-between;
}
h1 {
  font-size: 24px;
  font-weight: 600;
}
h2 {
  font-size: 20px;
}
form {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: end;
  margin: 20px 0;
}
label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}
input,
select,
button {
  font: inherit;
  color: inherit;
  border: 1px solid var(--auriga-border);
  background: var(--auriga-surface);
  border-radius: 8px;
  padding: 8px 10px;
}
button {
  cursor: pointer;
}
button:disabled {
  opacity: 0.45;
  cursor: default;
}
button:focus-visible,
input:focus-visible,
select:focus-visible {
  outline: 2px solid var(--auriga-accent);
  outline-offset: 2px;
}
.audit-table {
  overflow: auto;
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
th,
td {
  text-align: left;
  padding: 12px;
  border-bottom: 1px solid var(--auriga-border);
}
th {
  font-weight: 500;
}
.trace {
  max-width: 180px;
  overflow-wrap: anywhere;
}
.muted {
  color: var(--auriga-text-muted);
  font-size: 13px;
}
footer {
  margin-top: 16px;
}
[role='alert'] {
  color: var(--auriga-danger);
}
aside {
  position: fixed;
  inset: 0 0 0 auto;
  width: min(560px, 90vw);
  background: var(--auriga-bg);
  border-left: 1px solid var(--auriga-border);
  padding: 24px;
  overflow: auto;
  z-index: 30;
}
dt {
  font-size: 13px;
  color: var(--auriga-text-muted);
}
dd {
  margin: 6px 0 18px;
}
pre {
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
