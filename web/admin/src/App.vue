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
  background: radial-gradient(ellipse at 50% 25%, #0b22342e, transparent 70%);
}
</style>
