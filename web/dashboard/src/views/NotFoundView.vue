<script setup lang="ts">
import { useRoute } from "vue-router";

const route = useRoute();
</script>

<template>
  <h1>Not found</h1>
  <p>
    No view registered for <code>{{ route.fullPath }}</code>.
  </p>
</template>

<style scoped>
h1 { font-size: var(--tk-text-2xl); margin: 0 0 var(--tk-space-4); }
</style>
