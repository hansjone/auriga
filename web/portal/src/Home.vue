<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { Placeholder } from '@auriga/shared';
const { t, locale } = useI18n();
const light = ref(false);
function language() {
  locale.value = locale.value === 'en' ? 'zh-CN' : 'en';
  document.documentElement.lang = locale.value;
}
function theme() {
  light.value = !light.value;
  document.documentElement.dataset.theme = light.value ? 'light' : 'dark';
}
</script>
<template>
  <Placeholder :title="t('portal')"
    ><nav :aria-label="t('portal')">
      <ElButton @click="language">{{ t('language') }}</ElButton
      ><ElButton @click="theme">{{ t('theme') }}</ElButton>
    </nav></Placeholder
  >
</template>
