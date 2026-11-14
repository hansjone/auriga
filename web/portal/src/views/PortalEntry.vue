<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { createApiClient, createIamApi, isApiError } from '@auriga/shared';
import HomeView from './HomeView.vue';
import LandingView from './LandingView.vue';

// A public entry must not invoke the authenticated client's 401 redirect.
const session = createIamApi(createApiClient());
const state = ref<'loading' | 'guest' | 'member' | 'error'>('loading');
let active = true;
onUnmounted(() => {
  active = false;
});
async function load() {
  state.value = 'loading';
  try {
    await session.getMe();
    if (active) state.value = 'member';
  } catch (error) {
    if (active)
      state.value =
        isApiError(error) && error.status === 401 ? 'guest' : 'error';
  }
}
onMounted(load);
</script>

<template>
  <div v-if="state === 'member'">
    <nav class="member-nav">
      <RouterLink to="/account/security"
        >账号安全 / Account security</RouterLink
      >
    </nav>
    <HomeView />
  </div>
  <LandingView v-else-if="state === 'guest'" />
  <section v-else class="entry-status" role="status">
    <template v-if="state === 'loading'">正在打开 Auriga DSH…</template>
    <template v-else
      >暂时无法确认登录状态。<button @click="load">重试</button
      ><a href="/login">前往登录</a></template
    >
  </section>
</template>

<style scoped>
.member-nav {
  display: flex;
  justify-content: flex-end;
  padding: 12px 24px;
}
.member-nav a {
  color: var(--auriga-muted);
}
</style>
