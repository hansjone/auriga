<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { BrandMark, type Me } from '@auriga/shared';
import { apiClient, iamApi } from '../api';
import StepUpDialog from '../admin/StepUpDialog.vue';
import { useAdminI18n } from '../admin/messages';
import { navigation, iconPaths } from '../admin/navigation';
import '../admin/resources.css';
const { t, locale } = useI18n();
const { t: adminT } = useAdminI18n();
const router = useRouter();
const route = useRoute();
const me = ref<Me | null>(null);
const THEME_KEY = 'auriga.admin.theme';
function storedTheme(): string | null {
  try {
    return globalThis.localStorage.getItem(THEME_KEY);
  } catch {
    return null;
  }
}
const dark = ref(
  (storedTheme() ?? document.documentElement.dataset.adminTheme) === 'dark',
);
document.documentElement.dataset.adminTheme = dark.value ? 'dark' : 'light';
const logoutError = ref('');
let alive = true;
onBeforeUnmount(() => {
  alive = false;
});
const active = computed(() =>
  navigation.find(
    (item) =>
      item.path === route.path ||
      item.children.some(([path]) => path === route.path),
  ),
);
onMounted(async () => {
  try {
    const result = await iamApi.getMe();
    if (alive) me.value = result;
  } catch {
    if (alive)
      void router.replace({
        path: '/login',
        query: { returnTo: route.fullPath },
      });
  }
});
function language() {
  locale.value = locale.value === 'en' ? 'zh-CN' : 'en';
  document.documentElement.lang = locale.value;
}
function theme() {
  dark.value = !dark.value;
  const value = dark.value ? 'dark' : 'light';
  document.documentElement.dataset.adminTheme = value;
  try {
    globalThis.localStorage.setItem(THEME_KEY, value);
  } catch {
    /* storage unavailable: theme still applies for this page */
  }
}
const displayName = computed(() => me.value?.account.displayName || '');
const initial = computed(() =>
  displayName.value ? displayName.value.trim().charAt(0).toUpperCase() : '',
);
async function logout() {
  apiClient.invalidatePendingRequests();
  logoutError.value = '';
  try {
    await iamApi.logout();
    if (alive) void router.replace('/login');
  } catch {
    if (alive) logoutError.value = adminT('failed');
  }
}
</script>
<template>
  <div class="admin-shell" :data-admin-theme="dark ? 'dark' : 'light'">
    <StepUpDialog :me="me" />
    <aside class="admin-side">
      <RouterLink class="admin-brand" to="/overview"
        ><BrandMark :size="30" />
        <div>
          <strong>Auriga</strong><small>{{ adminT('navConsole') }}</small>
        </div></RouterLink
      >
      <nav :aria-label="adminT('navConsole')">
        <RouterLink
          v-for="item in navigation"
          :key="item.path"
          :to="item.path"
          class="admin-navlink"
          :aria-current="active?.key === item.key ? 'page' : undefined"
          ><svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path :d="iconPaths[item.icon]" /></svg
          ><span>{{ adminT(item.key) }}</span></RouterLink
        >
      </nav>
      <div class="admin-side-bottom">{{ adminT('navControl') }}</div>
    </aside>
    <div class="admin-workspace">
      <header class="admin-top">
        <div class="admin-crumb">
          {{ adminT('navConsole') }} <span>/</span>
          <strong>{{ active ? adminT(active.key) : '' }}</strong>
        </div>
        <div class="admin-top-actions">
          <button
            type="button"
            class="admin-icon-btn"
            :title="t('language')"
            :aria-label="t('language')"
            @click="language"
          >
            {{ locale === 'en' ? '中' : 'EN' }}
          </button>
          <button
            type="button"
            class="admin-icon-btn"
            :title="t('theme')"
            :aria-label="t('theme')"
            @click="theme"
          >
            <svg
              v-if="dark"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              aria-hidden="true"
            >
              <circle cx="12" cy="12" r="4" />
              <path
                d="M12 2.5v2M12 19.5v2M4.6 4.6l1.4 1.4M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"
              />
            </svg>
            <svg
              v-else
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z" />
            </svg>
          </button>
          <button
            type="button"
            class="admin-icon-btn"
            :title="t('login.logout')"
            :aria-label="t('login.logout')"
            @click="logout"
          >
            <svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path
                d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4M9 16l-4-4 4-4M5 12h11"
              />
            </svg>
          </button>
          <div v-if="displayName" class="admin-user">
            <span class="admin-avatar" aria-hidden="true">{{ initial }}</span
            ><span>{{ displayName }}</span>
          </div>
        </div>
      </header>
      <main class="admin-main">
        <p v-if="logoutError" role="alert" class="resource-error">
          {{ logoutError }}
        </p>
        <nav
          v-if="active?.children.length"
          class="admin-subnav"
          :aria-label="adminT(active.key)"
        >
          <RouterLink
            v-for="[path, key] in active.children"
            :key="path"
            :to="path"
            :aria-current="route.path === path ? 'page' : undefined"
            >{{ adminT(key) }}</RouterLink
          >
        </nav>
        <router-view />
      </main>
    </div>
  </div>
</template>
