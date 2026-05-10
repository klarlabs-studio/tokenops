<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { api, type OptimizationEntry } from "@/api/client";

// dashboard-opt-list: every optimization recommendation in the window,
// grouped by kind with totals. Apply / reject buttons are disabled in
// the MVP — the daemon needs an interactive-mode endpoint for that.
// Decision live indicators (applied/accepted/rejected/skipped) come
// directly from the underlying OptimizationEvent.

const items = ref<OptimizationEntry[]>([]);
const currency = ref("USD");
const error = ref<string | null>(null);

async function load() {
  try {
    const res = await api.optimizations({ since: "7d" });
    items.value = res.optimizations;
    currency.value = res.currency;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(load);

interface KindRow {
  kind: string;
  count: number;
  totalTokens: number;
  totalUSD: number;
}

const byKind = computed<KindRow[]>(() => {
  const totals = new Map<string, KindRow>();
  for (const it of items.value) {
    const cur = totals.get(it.kind) ?? {
      kind: it.kind,
      count: 0,
      totalTokens: 0,
      totalUSD: 0,
    };
    cur.count += 1;
    cur.totalTokens += it.estimated_savings_tokens;
    cur.totalUSD += it.estimated_savings_usd;
    totals.set(it.kind, cur);
  }
  return [...totals.values()].sort((a, b) => b.totalTokens - a.totalTokens);
});

function decisionBadge(d: string): string {
  switch (d) {
    case "applied":
      return "applied";
    case "accepted":
      return "accepted";
    case "rejected":
      return "rejected";
    case "skipped":
      return "skipped";
    default:
      return d;
  }
}

function fmtMoney(v: number): string {
  return `${v.toFixed(4)} ${currency.value}`;
}
</script>

<template>
  <h1>Optimizations</h1>
  <div v-if="error" class="error">{{ error }}</div>

  <section class="kinds" v-if="byKind.length > 0">
    <article v-for="row in byKind" :key="row.kind" class="card">
      <header>{{ row.kind }}</header>
      <div class="value">
        {{ row.totalTokens.toLocaleString() }} <span class="unit">tok saved</span>
      </div>
      <div class="hint">
        {{ row.count }} recommendations · {{ fmtMoney(row.totalUSD) }}
      </div>
    </article>
  </section>

  <section class="card">
    <header class="header-row">
      <h2>Recent recommendations</h2>
      <span class="hint">last 7 days · top 500</span>
    </header>
    <table class="opts" v-if="items.length > 0">
      <thead>
        <tr>
          <th>When</th>
          <th>Kind</th>
          <th>Mode</th>
          <th>Decision</th>
          <th>Save tok</th>
          <th>Save $</th>
          <th>Quality</th>
          <th>Reason</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(it, i) in items" :key="i">
          <td>{{ new Date(it.timestamp).toLocaleString() }}</td>
          <td>{{ it.kind }}</td>
          <td>{{ it.mode }}</td>
          <td :class="`decision decision-${it.decision}`">{{ decisionBadge(it.decision) }}</td>
          <td>{{ it.estimated_savings_tokens.toLocaleString() }}</td>
          <td>{{ fmtMoney(it.estimated_savings_usd) }}</td>
          <td>{{ it.quality_score.toFixed(2) }}</td>
          <td class="reason">{{ it.reason }}</td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty">
      No optimization events recorded yet. Run
      <code>tokenops replay &lt;workflow_id&gt;</code> against a workflow
      with chat messages to populate this list.
    </div>
  </section>
</template>

<style scoped>
h1 {
  font-size: var(--tk-text-2xl);
  margin: 0 0 var(--tk-space-4);
}
.error {
  color: var(--tk-color-danger);
  margin-bottom: var(--tk-space-4);
}
.kinds {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: var(--tk-space-4);
  margin-bottom: var(--tk-space-5);
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
  margin-bottom: var(--tk-space-2);
}
.header-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  margin-bottom: var(--tk-space-3);
  text-transform: none;
  letter-spacing: 0;
}
.header-row h2 {
  margin: 0;
  font-size: var(--tk-text-md);
  color: var(--tk-color-text);
}
.hint {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
  text-transform: none;
  letter-spacing: 0;
}
.value {
  font-size: var(--tk-text-xl);
  font-weight: 600;
}
.unit {
  font-size: var(--tk-text-xs);
  font-weight: 400;
  color: var(--tk-color-text-mute);
  margin-left: var(--tk-space-1);
}
.opts {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--tk-text-sm);
}
.opts th,
.opts td {
  padding: var(--tk-space-2);
  border-bottom: 1px solid var(--tk-color-border);
  text-align: left;
}
.opts th {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.decision {
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-xs);
}
.decision-applied {
  color: var(--tk-color-success);
}
.decision-accepted {
  color: var(--tk-color-info);
}
.decision-rejected {
  color: var(--tk-color-danger);
}
.decision-skipped {
  color: var(--tk-color-text-mute);
}
.reason {
  color: var(--tk-color-text-mute);
}
.empty {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-sm);
}
</style>
