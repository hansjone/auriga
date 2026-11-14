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
.stage:has(.landing) {
  display: block;
  padding: 0;
  min-height: 0;
  height: 100vh;
  height: 100dvh;
}
</style>
