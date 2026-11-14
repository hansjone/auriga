<template>
  <main class="stage auriga-stage"><router-view /></main>
</template>

<style scoped>
.stage:has(.shell),
.stage:has(.landing) {
  display: block;
  min-height: 100vh;
  padding: 0;
  place-items: unset;
}
</style>
