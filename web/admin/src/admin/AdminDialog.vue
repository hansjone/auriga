<script setup lang="ts">
import { ElDialog } from 'element-plus';

/**
 * Console-wide modal for edit forms and detail panes. It renders in place (not
 * teleported) so it inherits the admin theme tokens; a click on the backdrop
 * never discards a draft, and `locked` keeps it open while work is running.
 * The lock is enforced in the close handler rather than by toggling ElDialog
 * props: flipping show-close/escape while a write runs re-renders ElDialog in
 * a loop together with panes that report their own busy state.
 */
const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    width?: string;
    locked?: boolean;
  }>(),
  { width: '640px', locked: false },
);
const emit = defineEmits<{ close: [] }>();
function update(value: boolean) {
  if (!value && !props.locked) emit('close');
}
</script>

<template>
  <ElDialog
    v-if="open"
    :model-value="open"
    :title="title"
    :width="width"
    class="admin-dialog"
    align-center
    :close-on-click-modal="false"
    @update:model-value="update"
  >
    <slot />
  </ElDialog>
</template>
