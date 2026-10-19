<template>
  <main class="stage"><router-view /></main>
</template>
<style scoped>
.stage {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 1rem;
  box-sizing: border-box;
  background:
    radial-gradient(80% 60% at 50% 38%, #0b2234 0%, rgba(11, 34, 52, 0) 70%),
    linear-gradient(180deg, #06121d 0%, var(--auriga-bg) 100%);
}
.stage:has(.admin-shell) {
  display: block;
  padding: 0;
  place-items: unset;
}
</style>
