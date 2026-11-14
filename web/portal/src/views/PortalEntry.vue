<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { createApiClient, createIamApi, isApiError } from '@auriga/shared';
import { useI18n } from 'vue-i18n';
import { portalMessages } from '../portal-messages';
import HomeView from './HomeView.vue';
import LandingView from './LandingView.vue';
const { t: globalT } = useI18n();
const { t } = useI18n({ useScope: 'local', messages: portalMessages });

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
  <HomeView v-if="state === 'member'" />
  <LandingView v-else-if="state === 'guest'" />
  <section v-else class="entry-status" role="status">
    <template v-if="state === 'loading'">{{ t('entryLoading') }}</template>
    <template v-else
      >{{ t('entryError')
      }}<button @click="load">{{ globalT('login.retry') }}</button
      ><a href="/login">{{ globalT('login.title') }}</a></template
    >
  </section>
</template>
