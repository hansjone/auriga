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
    <!-- Regular pentagon centred on (32,33), circumradius 21. -->
    <path
      d="M32 12 51.97 26.51 44.34 49.99H19.66L12.03 26.51Z"
      fill="none"
      stroke="#8fb9d6"
      stroke-width="3"
      stroke-linejoin="round"
    />
    <path
      d="M23.5 44 32 23l8.5 21M26.5 37h11"
      fill="none"
      stroke="#e8f0f7"
      stroke-width="3"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <circle cx="32" cy="12" r="3" fill="#8fb9d6" />
    <circle cx="51.97" cy="26.51" r="3" fill="#8fb9d6" />
    <circle cx="44.34" cy="49.99" r="3" fill="#8fb9d6" />
    <circle cx="19.66" cy="49.99" r="3" fill="#8fb9d6" />
    <circle cx="12.03" cy="26.51" r="3" fill="#8fb9d6" />
  </svg>
</template>
