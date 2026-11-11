<script setup lang="ts">
import { useAdminI18n } from '../../admin/messages';
import '../../admin/management.css';
const { t } = useAdminI18n();
</script>
<template>
  <section class="management">
    <h1>{{ t('opsBackups') }}</h1>
    <p role="status">{{ t('backupMissing') }}</p>
    <section
      v-for="part in ['Scope', 'Download', 'Restore']"
      :key="part"
      class="details"
    >
      <h2>{{ t(`backup${part}`) }}</h2>
      <p>{{ t(`backup${part}Text`) }}</p>
    </section>
  </section>
</template>
