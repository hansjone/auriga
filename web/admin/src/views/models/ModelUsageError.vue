<script setup lang="ts">
import { useModelUsageI18n } from './modelUsageState';
defineProps<{ error: string; returnTo: '/models/relay' | '/models/usage' }>();
const { t } = useModelUsageI18n();
</script>

<template>
  <div role="alert">
    <p>{{ t(error) }}</p>
    <a
      v-if="error === 'stepUpRequired'"
      data-test="verify-login"
      :href="`/login?returnTo=${encodeURIComponent(returnTo)}`"
      >{{ t('verifyLogin') }}</a
    >
  </div>
</template>
