<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import {
  ElButton,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
} from 'element-plus';
import { isApiError, type Me } from '@auriga/shared';
import { iamApi } from '../api';
import InstanceCard from '../components/InstanceCard.vue';

const { t, locale } = useI18n();
const router = useRouter();
const me = ref<Me | null>(null);
const light = ref(document.documentElement.dataset.theme === 'light');
const loadError = ref(false);
const loading = ref(false);
let active = true;
onBeforeUnmount(() => {
  active = false;
});

async function loadMe() {
  if (loading.value) return;
  loading.value = true;
  loadError.value = false;
  try {
    const value = await iamApi.getMe();
    if (active) me.value = value;
  } catch (error) {
    if (!active) return;
    if (isApiError(error) && error.status === 401)
      void router.replace({
        path: '/login',
        query: { returnTo: '/' },
      });
    else loadError.value = true;
  } finally {
    if (active) loading.value = false;
  }
}
onMounted(loadMe);

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
  me.value = null;
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
      <InstanceCard v-if="me" />
      <p v-if="loadError" role="alert">{{ t('errors.unknown') }}</p>
      <ElButton v-if="loadError" @click="loadMe">{{
        t('login.retry')
      }}</ElButton>
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
