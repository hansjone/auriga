<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 28 });
</script>

<template>
  <svg
    class="auriga-mark"
    :width="size"
    :height="size"
    viewBox="0 0 32 32"
    fill="none"
    aria-hidden="true"
  >
    <rect
      x="0.75"
      y="0.75"
      width="30.5"
      height="30.5"
      rx="9"
      stroke="currentColor"
      stroke-opacity="0.28"
      stroke-width="1.5"
    />
    <path
      d="M16 7.5 23.6 13 20.7 22.5H11.3L8.4 13Z"
      stroke="currentColor"
      stroke-opacity="0.5"
      stroke-width="1.2"
      stroke-linejoin="round"
    />
    <circle cx="16" cy="7.5" r="2.1" fill="currentColor" />
    <circle cx="23.6" cy="13" r="1.6" fill="currentColor" />
    <circle cx="20.7" cy="22.5" r="1.6" fill="currentColor" />
    <circle cx="11.3" cy="22.5" r="1.6" fill="currentColor" />
    <circle cx="8.4" cy="13" r="1.6" fill="currentColor" />
  </svg>
</template>
