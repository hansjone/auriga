<script setup lang="ts">
import { ElButton, ElDialog } from 'element-plus';
import { useAdminI18n } from './messages';
defineProps<{ message?: string; busy: boolean }>();
const emit = defineEmits<{ confirm: []; cancel: [] }>();
const { t } = useAdminI18n();
</script>
<template>
  <ElDialog
    :model-value="!!message"
    :title="t('confirm')"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="emit('cancel')"
  >
    <p>{{ message }}</p>
    <template #footer>
      <ElButton :disabled="busy" @click="emit('cancel')">{{
        t('cancel')
      }}</ElButton>
      <ElButton
        type="primary"
        :loading="busy"
        :disabled="busy"
        data-testid="confirm-action"
        @click="emit('confirm')"
        >{{ t('confirm') }}</ElButton
      >
    </template>
  </ElDialog>
</template>
