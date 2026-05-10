<script setup lang="ts">
import { onMounted, ref } from "vue";
import { api, type Health, type Version } from "@/api/client";

const health = ref<Health | null>(null);
const version = ref<Version | null>(null);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    health.value = await api.health();
    version.value = await api.version();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
});

function formatUptime(ns: number): string {
  const s = Math.floor(ns / 1e9);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return `${h}h ${m}m`;
}
</script>

<template>
  <h1>Overview</h1>
  <p class="lead">
    TokenOps observes every prompt flowing through the local proxy.
    Use the navigation to drill into spend, workflows, and optimization
    opportunities.
  </p>

  <section class="cards">
    <article class="card">
      <header>Daemon health</header>
      <div v-if="health" class="metric">
        <span class="value">{{ health.status }}</span>
        <span class="hint">uptime {{ formatUptime(health.uptime_ns) }}</span>
      </div>
      <div v-else-if="error" class="error">{{ error }}</div>
      <div v-else class="hint">…</div>
    </article>

    <article class="card">
      <header>Version</header>
      <div v-if="version" class="metric">
        <span class="value">{{ version.version }}</span>
        <span class="hint mono">{{ version.commit }}</span>
      </div>
      <div v-else class="hint">…</div>
    </article>

    <article class="card">
      <header>Quick links</header>
      <ul class="links">
        <li><a href="https://github.com/felixgeelhaar/tokenops">github.com/felixgeelhaar/tokenops</a></li>
        <li>SDK shims: <code>docs/sdk/</code></li>
        <li>CLI replay: <code>tokenops replay &lt;session&gt;</code></li>
      </ul>
    </article>
  </section>
</template>

<style scoped>
h1 {
  font-size: var(--tk-text-2xl);
  margin: 0 0 var(--tk-space-2);
}
.lead {
  color: var(--tk-color-text-mute);
  max-width: 60ch;
  margin: 0 0 var(--tk-space-5);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: var(--tk-space-4);
}
.card {
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-border);
  border-radius: var(--tk-radius-md);
  padding: var(--tk-space-4);
  box-shadow: var(--tk-shadow-card);
}
.card header {
  font-size: var(--tk-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--tk-color-text-mute);
  margin-bottom: var(--tk-space-3);
}
.metric { display: flex; flex-direction: column; gap: var(--tk-space-1); }
.value { font-size: var(--tk-text-xl); font-weight: 600; }
.hint { font-size: var(--tk-text-xs); color: var(--tk-color-text-mute); }
.mono { font-family: var(--tk-font-mono); }
.error { color: var(--tk-color-danger); font-size: var(--tk-text-sm); }
.links { list-style: none; padding: 0; margin: 0; font-size: var(--tk-text-sm); }
.links li { padding: var(--tk-space-1) 0; }
</style>
