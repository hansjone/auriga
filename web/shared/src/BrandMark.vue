<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 28 });
</script>
<template>
  <svg
    class="auriga-mark"
    :width="size"
    :height="size"
    viewBox="0 0 64 64"
    fill="none"
    aria-hidden="true"
  >
    <rect width="64" height="64" rx="16" fill="#10283b" />
    <path
      d="M31 10 51 25 44 51 18 49 11 24Z"
      fill="none"
      stroke="#8fb9d6"
      stroke-width="3"
      stroke-linejoin="round"
    />
    <path
      d="m18 49 13-26 13 28M24 37h14"
      fill="none"
      stroke="#e8f0f7"
      stroke-width="3"
      stroke-linejoin="round"
    />
    <circle cx="31" cy="10" r="4" fill="#e8f0f7" />
    <circle cx="51" cy="25" r="3" fill="#8fb9d6" />
    <circle cx="44" cy="51" r="3" fill="#8fb9d6" />
    <circle cx="18" cy="49" r="3" fill="#8fb9d6" />
    <circle cx="11" cy="24" r="3" fill="#8fb9d6" />
  </svg>
</template>
