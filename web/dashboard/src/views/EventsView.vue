<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { api, type DomainEventsResponse } from "@/api/client";

const data = ref<DomainEventsResponse | null>(null);
const error = ref<string | null>(null);
const loading = ref(true);

async function load() {
  loading.value = true;
  try {
    data.value = await api.domainEvents();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const rows = computed(() => {
  if (!data.value) return [];
  return Object.entries(data.value.counts)
    .map(([kind, count]) => ({ kind, count }))
    .sort((a, b) => a.kind.localeCompare(b.kind));
});
</script>

<template>
  <h1>Domain events</h1>
  <p class="hint">In-process event counters from the daemon's domain bus.</p>

  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="loading && !data" class="empty">Loading…</div>

  <section class="kpis" v-if="data">
    <article class="card">
      <header>Total events</header>
      <div class="value">{{ data.total.toLocaleString() }}</div>
    </article>
    <article class="card" v-if="data.audit_dropped !== undefined">
      <header>Audit drops</header>
      <div class="value">{{ data.audit_dropped.toLocaleString() }}</div>
      <div class="hint">events shed under backpressure</div>
    </article>
  </section>

  <section class="card" v-if="rows.length > 0">
    <header class="header-row">
      <h2>Per-kind counts</h2>
      <button @click="load">refresh</button>
    </header>
    <table>
      <thead>
        <tr><th>Kind</th><th class="num">Count</th></tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.kind">
          <td>{{ r.kind }}</td>
          <td class="num">{{ r.count.toLocaleString() }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
h1 { font-size: var(--tk-text-2xl); margin: 0 0 var(--tk-space-2); }
.hint { font-size: var(--tk-text-xs); color: var(--tk-color-text-mute); }
.error { color: var(--tk-color-danger); margin: var(--tk-space-4) 0; }
.empty { color: var(--tk-color-text-mute); margin: var(--tk-space-4) 0; }
.kpis { display: grid; grid-template-columns: repeat(auto-fit,minmax(180px,1fr)); gap: var(--tk-space-4); margin: var(--tk-space-4) 0; }
.card { background: var(--tk-color-surface); border: 1px solid var(--tk-color-border); border-radius: var(--tk-radius-md); padding: var(--tk-space-4); }
.card header { font-size: var(--tk-text-xs); text-transform: uppercase; color: var(--tk-color-text-mute); margin-bottom: var(--tk-space-2); }
.header-row { display: flex; justify-content: space-between; align-items: baseline; text-transform: none; }
.header-row h2 { margin: 0; font-size: var(--tk-text-md); }
.value { font-size: var(--tk-text-xl); font-weight: 600; }
table { width: 100%; border-collapse: collapse; font-size: var(--tk-text-sm); }
th, td { padding: var(--tk-space-2); border-bottom: 1px solid var(--tk-color-border); text-align: left; }
.num { text-align: right; font-variant-numeric: tabular-nums; }
button { background: transparent; border: 1px solid var(--tk-color-border); border-radius: var(--tk-radius-sm); padding: var(--tk-space-1) var(--tk-space-3); cursor: pointer; }
</style>
