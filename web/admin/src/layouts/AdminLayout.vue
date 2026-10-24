<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { ElButton } from 'element-plus';
import type { Me } from '@auriga/shared';
import { iamApi } from '../api';
import { useAdminI18n } from '../admin/messages';
import { navigation, iconPaths } from '../admin/navigation';
import '../admin/resources.css';
const { t, locale } = useI18n();
const { t: adminT } = useAdminI18n();
const router = useRouter();
const route = useRoute();
const me = ref<Me | null>(null);
const dark = ref(document.documentElement.dataset.adminTheme === 'dark');
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
  document.documentElement.dataset.adminTheme = dark.value ? 'dark' : 'light';
}
async function logout() {
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
    <aside class="admin-side">
      <RouterLink class="admin-brand" to="/overview"
        ><span>A</span>
        <div>
          Auriga<small>{{ adminT('navConsole') }}</small>
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
          {{ active ? adminT(active.key) : '' }}
        </div>
        <div class="admin-top-actions">
          <span>{{ me?.account.displayName || '' }}</span
          ><ElButton text @click="language">{{ t('language') }}</ElButton
          ><ElButton text @click="theme">{{ t('theme') }}</ElButton
          ><ElButton text @click="logout">{{ t('login.logout') }}</ElButton>
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
