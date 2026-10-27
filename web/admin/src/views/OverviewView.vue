<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { orgApi } from '../api';
import { adminApi } from '../admin/api';
import { modelUsageApi } from '../admin/modelUsageApi';
import { readAllPages } from './models/modelUsageState';

const { t } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      title: '概览',
      description: '已登记数据快照；运行状态与详细用量请查看对应列表。',
      refresh: '刷新',
      loading: '读取中…',
      empty: '暂无数据',
      forbidden: '无权查看',
      failed: '读取失败，请重试',
      unauthenticated: '会话已过期，请重新登录',
      updated: '更新时间',
      members: '成员',
      nodes: '节点',
      instances: '实例',
      usage: '中转站用量记录',
      details: '查看详情',
    },
    en: {
      title: 'Overview',
      description:
        'Registered data snapshot. Open the corresponding list for runtime state and detailed usage.',
      refresh: 'Refresh',
      loading: 'Loading…',
      empty: 'No data',
      forbidden: 'Permission denied',
      failed: 'Could not load. Try again.',
      unauthenticated: 'Session expired. Sign in again.',
      updated: 'Updated',
      members: 'Members',
      nodes: 'Nodes',
      instances: 'Instances',
      usage: 'Relay usage records',
      details: 'View details',
    },
  },
});
const cards = [
  {
    key: 'members',
    path: '/members',
    read: (signal: globalThis.AbortSignal) =>
      readAllPages(
        (cursor) => orgApi.listMembers({ cursor, limit: 100 }),
        signal,
      ),
  },
  {
    key: 'nodes',
    path: '/nodes',
    read: async () => (await adminApi.nodes()).items,
  },
  {
    key: 'instances',
    path: '/instances',
    read: (signal: globalThis.AbortSignal) =>
      readAllPages(adminApi.instances, signal),
  },
  {
    key: 'usage',
    path: '/models/usage',
    read: (signal: globalThis.AbortSignal) =>
      readAllPages(modelUsageApi.usage, signal),
  },
];
const values = reactive<
  Record<string, { count?: number; error?: string; updated?: string }>
>({});
const loading = ref(false);
let version = 0;
let controller: globalThis.AbortController | undefined;
async function reload() {
  const current = ++version;
  controller?.abort();
  controller = new globalThis.AbortController();
  const signal = controller.signal;
  loading.value = true;
  await Promise.all(
    cards.map(async (card) => {
      values[card.key] = {};
      try {
        const rows = await card.read(signal);
        if (current === version)
          values[card.key] = {
            count: rows.length,
            updated: new Date().toLocaleString(),
          };
      } catch (error) {
        if (current === version)
          values[card.key] = {
            error:
              isApiError(error) && error.status === 403
                ? 'forbidden'
                : isApiError(error) && error.status === 401
                  ? 'unauthenticated'
                  : 'failed',
          };
      }
    }),
  );
  if (current === version) loading.value = false;
}
onMounted(reload);
onBeforeUnmount(() => {
  version++;
  controller?.abort();
});
</script>

<template>
  <section>
    <header class="overview-heading">
      <div>
        <h1>{{ t('title') }}</h1>
        <p>{{ t('description') }}</p>
      </div>
      <ElButton :disabled="loading" @click="reload">{{
        t('refresh')
      }}</ElButton>
    </header>
    <div class="overview-cards" :aria-busy="loading">
      <section
        v-for="card in cards"
        :key="card.key"
        class="overview-card"
        :data-summary="card.key"
      >
        <h2>{{ t(card.key) }}</h2>
        <p v-if="values[card.key]?.error" role="alert">
          {{ t(values[card.key]!.error!) }}
        </p>
        <p v-else-if="values[card.key]?.count === undefined" role="status">
          {{ t('loading') }}
        </p>
        <template v-else
          ><strong>{{ values[card.key]!.count }}</strong>
          <p v-if="values[card.key]!.count === 0">{{ t('empty') }}</p>
          <p>{{ t('updated') }}：{{ values[card.key]!.updated }}</p></template
        >
        <RouterLink :to="card.path">{{ t('details') }}</RouterLink>
      </section>
    </div>
  </section>
</template>

<style scoped>
.overview-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
.overview-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
  gap: 1rem;
}
.overview-card {
  padding: 1.25rem;
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
}
.overview-card strong {
  font-size: 2rem;
}
</style>
