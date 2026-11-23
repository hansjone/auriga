<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

const props = withDefaults(
  defineProps<{ label?: string; open?: boolean }>(),
  { open: false },
);
const { locale } = useI18n();
const shown = ref(props.open);
const root = ref<globalThis.HTMLElement>();
watch(
  () => props.open,
  (value) => {
    shown.value = value;
  },
);

function toggle() {
  shown.value = !shown.value;
}
function close(event: globalThis.Event) {
  if (!shown.value) return;
  if (event instanceof globalThis.KeyboardEvent && event.key === 'Escape') {
    shown.value = false;
    return;
  }
  if (
    event.target instanceof globalThis.Node &&
    root.value &&
    !root.value.contains(event.target)
  )
    shown.value = false;
}
watch(shown, (value) => {
  if (value) {
    document.addEventListener('click', close, true);
    document.addEventListener('keydown', close, true);
  } else {
    document.removeEventListener('click', close, true);
    document.removeEventListener('keydown', close, true);
  }
});
onBeforeUnmount(() => {
  document.removeEventListener('click', close, true);
  document.removeEventListener('keydown', close, true);
});
const aria = () =>
  props.label || (locale.value === 'en' ? 'Details' : '说明');
</script>
<template>
  <span ref="root" class="auriga-hint">
    <button
      type="button"
      class="auriga-hint-trigger"
      :aria-label="aria()"
      :aria-expanded="shown"
      :title="aria()"
      @click.stop="toggle"
    >
      <svg
        viewBox="0 0 24 24"
        width="16"
        height="16"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        stroke-linecap="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="9" />
        <path d="M12 11v6M12 8h.01" />
      </svg>
    </button>
    <span v-if="shown" class="auriga-hint-panel" role="note" @click.stop>
      <slot />
    </span>
  </span>
</template>
<style scoped>
.auriga-hint {
  position: relative;
  display: inline-flex;
  align-items: center;
  vertical-align: middle;
}
.auriga-hint-trigger {
  display: inline-grid;
  place-items: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--auriga-muted);
  cursor: pointer;
}
.auriga-hint-trigger:hover,
.auriga-hint-trigger[aria-expanded='true'] {
  color: var(--auriga-accent-line, var(--auriga-accent));
  background: var(--auriga-field-hover, rgba(120, 160, 192, 0.08));
}
.auriga-hint-panel {
  position: absolute;
  z-index: 40;
  top: calc(100% + 6px);
  left: 0;
  width: min(360px, 72vw);
  padding: 12px 14px;
  border-radius: 10px;
  border: 1px solid var(--auriga-border);
  background: var(--auriga-surface, #161e27);
  color: var(--auriga-text);
  box-shadow: 0 16px 40px -24px rgba(0, 0, 0, 0.55);
  font-size: 12.5px;
  line-height: 1.55;
  text-align: left;
  white-space: normal;
}
.auriga-hint-panel :deep(p) {
  margin: 0 0 0.55rem;
  color: var(--auriga-muted);
}
.auriga-hint-panel :deep(p:last-child) {
  margin-bottom: 0;
}
.auriga-hint-panel :deep(strong) {
  color: var(--auriga-text);
  font-weight: 600;
}
</style>
