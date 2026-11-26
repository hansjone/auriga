<script setup lang="ts">
import { ref, watch } from 'vue';
import { adminApi } from '../../admin/api';
import MemberPlacement from '../placements/MemberPlacement.vue';
const props = defineProps<{ memberId: string }>();
const name = ref('');
watch(
  () => props.memberId,
  async (id, _, cleanup) => {
    let active = true;
    cleanup(() => {
      active = false;
    });
    name.value = '';
    try {
      const member = await adminApi.member(id);
      if (active && member.id === id)
        name.value = member.displayName || member.email || '';
    } catch {
      /* Placement readers may lack org.members.read. */
    }
  },
  { immediate: true },
);
</script>
<template>
  <section>
    <h2 v-if="name">{{ name }}</h2>
    <MemberPlacement :member-id="memberId" />
  </section>
</template>
