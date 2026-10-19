<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import {
  ElButton,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
} from 'element-plus';
import type { Me } from '@auriga/shared';
import { iamApi } from '../api';

const { t, locale } = useI18n();
const router = useRouter();
const me = ref<Me | null>(null);
const light = ref(document.documentElement.dataset.theme === 'light');

onMounted(async () => {
  try {
    me.value = await iamApi.getMe();
  } catch {
    void router.replace({
      path: '/login',
      query: { returnTo: '/' },
    });
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
  <div class="shell">
    <header class="topbar">
      <strong class="brand">{{ t('brand') }}</strong>
      <nav class="actions">
        <ElButton text @click="language">{{ t('language') }}</ElButton>
        <ElButton text @click="theme">{{ t('theme') }}</ElButton>
        <ElDropdown v-if="me">
          <ElButton text>{{ me.account.displayName }}</ElButton>
          <template #dropdown>
            <ElDropdownMenu>
              <ElDropdownItem @click="logout">{{
                t('login.logout')
              }}</ElDropdownItem>
            </ElDropdownMenu>
          </template>
        </ElDropdown>
      </nav>
    </header>
    <main class="content auriga-glass">
      <h1>{{ t('home.title') }}</h1>
      <p class="muted">{{ t('home.empty') }}</p>
    </main>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1.5rem;
  border-bottom: 1px solid var(--auriga-border);
  background: rgba(7, 22, 36, 0.55);
  backdrop-filter: blur(16px);
}
.brand {
  letter-spacing: 0.12em;
}
.actions {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}
.content {
  margin: 2rem auto;
  width: min(720px, calc(100% - 2rem));
}
.muted {
  color: var(--auriga-muted);
}
</style>
