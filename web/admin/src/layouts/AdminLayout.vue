<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { ElButton, ElMenu, ElMenuItem, ElSubMenu } from 'element-plus';
import type { Me } from '@auriga/shared';
import { iamApi } from '../api';
import { useAdminI18n } from '../admin/messages';

const { t, locale } = useI18n();
const { t: adminT } = useAdminI18n();
const router = useRouter();
const me = ref<Me | null>(null);
const light = ref(document.documentElement.dataset.theme === 'light');

const items = [
  { path: '/providers', key: 'adminNav.providers' },
  { path: '/settings', key: 'adminNav.settings' },
] as const;

onMounted(async () => {
  try {
    me.value = await iamApi.getMe();
  } catch {
    void router.replace({ path: '/login', query: { returnTo: '/members' } });
  }
});

function language() {
  locale.value = locale.value === 'en' ? 'zh-CN' : 'en';
  document.documentElement.lang = locale.value;
}

function theme() {
  light.value = !light.value;
  document.documentElement.dataset.theme = light.value ? 'light' : 'dark';
}

async function logout() {
  await iamApi.logout();
  void router.replace('/login');
}
</script>

<template>
  <div class="admin-shell">
    <aside class="side">
      <p class="brand">{{ t('brand') }}</p>
      <ElMenu
        :default-active="$route.path"
        router
        background-color="transparent"
        text-color="var(--auriga-text)"
        active-text-color="var(--auriga-accent)"
        :default-openeds="['environment', 'membership']"
      >
        <ElSubMenu index="environment">
          <template #title>{{ adminT('environment') }}</template>
          <ElMenuItem index="/instances">{{ adminT('instances') }}</ElMenuItem>
          <ElMenuItem index="/nodes">{{ adminT('nodes') }}</ElMenuItem>
        </ElSubMenu>
        <ElSubMenu index="membership">
          <template #title>{{ adminT('membership') }}</template>
          <ElMenuItem index="/members">{{ t('adminNav.members') }}</ElMenuItem>
          <ElMenuItem index="/groups">{{ t('adminNav.groups') }}</ElMenuItem>
          <ElMenuItem index="/roles">{{ t('adminNav.roles') }}</ElMenuItem>
        </ElSubMenu>
        <ElMenuItem v-for="item in items" :key="item.path" :index="item.path">
          {{ t(item.key) }}
        </ElMenuItem>
      </ElMenu>
    </aside>
    <div class="main">
      <header class="top">
        <span class="muted">{{ me?.account.displayName || '' }}</span>
        <div class="actions">
          <ElButton text @click="language">{{ t('language') }}</ElButton>
          <ElButton text @click="theme">{{ t('theme') }}</ElButton>
          <ElButton text @click="logout">{{ t('login.logout') }}</ElButton>
        </div>
      </header>
      <section class="page">
        <router-view />
      </section>
    </div>
  </div>
</template>

<style scoped>
.admin-shell {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 220px 1fr;
}
.side {
  border-right: 1px solid var(--auriga-border);
  padding: 1.25rem 0.75rem;
  background: rgba(7, 22, 36, 0.55);
  backdrop-filter: blur(16px);
}
.brand {
  letter-spacing: 0.16em;
  text-transform: uppercase;
  font-size: 0.75rem;
  padding: 0 0.75rem 1rem;
  margin: 0;
}
.main {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.75rem 1.25rem;
  border-bottom: 1px solid var(--auriga-border);
}
.page {
  padding: 1.25rem;
}
.muted {
  color: var(--auriga-muted);
}
.actions {
  display: flex;
  gap: 0.25rem;
}
</style>
