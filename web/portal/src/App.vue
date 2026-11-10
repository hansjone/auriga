<template>
  <main class="stage auriga-stage"><router-view /></main>
</template>

<style scoped>
.stage:has(.shell) {
  display: block;
  min-height: 100vh;
  padding: 0;
  place-items: unset;
}
</style>
