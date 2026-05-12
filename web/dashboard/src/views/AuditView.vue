<script setup lang="ts">
import { onMounted, ref } from "vue";
import { api, type AuditEntry } from "@/api/client";

const entries = ref<AuditEntry[]>([]);
const error = ref<string | null>(null);
const loading = ref(true);

async function load() {
  loading.value = true;
  try {
    const res = await api.audit({ limit: 100 });
    entries.value = res.entries ?? [];
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}
onMounted(load);
</script>

<template>
  <h1>Audit log</h1>
  <p class="hint">Security-relevant domain events recorded by the audit subscriber.</p>

  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="loading && !entries.length" class="empty">Loading…</div>

  <section class="card" v-if="entries.length > 0">
    <header class="header-row">
      <h2>Recent entries</h2>
      <button @click="load">refresh</button>
    </header>
    <table>
      <thead>
        <tr>
          <th>When</th>
          <th>Action</th>
          <th>Actor</th>
          <th>Target</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="e in entries" :key="e.ID">
          <td>{{ new Date(e.Timestamp).toLocaleString() }}</td>
          <td class="kind">{{ e.Action }}</td>
          <td>{{ e.Actor }}</td>
          <td>{{ e.Target || "—" }}</td>
          <td class="details">{{ e.Details ? JSON.stringify(e.Details) : "" }}</td>
        </tr>
      </tbody>
    </table>
  </section>

  <div v-if="!loading && entries.length === 0 && !error" class="empty">
    No audit entries yet. Trigger an OptimizationApplied or BudgetExceeded
    domain event to populate the log.
  </div>
</template>

<style scoped>
h1 { font-size: var(--tk-text-2xl); margin: 0 0 var(--tk-space-2); }
.hint { font-size: var(--tk-text-xs); color: var(--tk-color-text-mute); }
.error { color: var(--tk-color-danger); margin: var(--tk-space-4) 0; }
.empty { color: var(--tk-color-text-mute); margin: var(--tk-space-4) 0; }
.card { background: var(--tk-color-surface); border: 1px solid var(--tk-color-border); border-radius: var(--tk-radius-md); padding: var(--tk-space-4); }
.header-row { display: flex; justify-content: space-between; align-items: baseline; }
.header-row h2 { margin: 0; font-size: var(--tk-text-md); }
table { width: 100%; border-collapse: collapse; font-size: var(--tk-text-sm); }
th, td { padding: var(--tk-space-2); border-bottom: 1px solid var(--tk-color-border); text-align: left; }
th { color: var(--tk-color-text-mute); font-size: var(--tk-text-xs); text-transform: uppercase; }
.kind { font-family: var(--tk-font-mono); font-size: var(--tk-text-xs); }
.details { font-family: var(--tk-font-mono); font-size: var(--tk-text-xs); color: var(--tk-color-text-mute); }
button { background: transparent; border: 1px solid var(--tk-color-border); border-radius: var(--tk-radius-sm); padding: var(--tk-space-1) var(--tk-space-3); cursor: pointer; }
</style>
