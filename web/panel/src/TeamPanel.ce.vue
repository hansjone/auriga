<script setup lang="ts">
import { computed } from 'vue';
import { messages } from '@auriga/shared';
const props = defineProps<{
  context?: { locale?: string; theme?: 'dark' | 'light' };
}>();
const text = computed(
  () => messages[props.context?.locale === 'en' ? 'en' : 'zh-CN'],
);
</script>
<template>
  <aside class="auriga-glass" :data-theme="context?.theme ?? 'dark'">
    <h2>{{ text.panel }}</h2>
    <p>{{ text.placeholder }}</p>
  </aside>
</template>
<style src="@auriga/shared/theme.css"></style>
<style>
:host {
  display: block;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
}
h2 {
  font-weight: 500;
}
</style>
