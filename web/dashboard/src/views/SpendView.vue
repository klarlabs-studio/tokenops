<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import LineChart from "@/components/LineChart.vue";
import Heatmap from "@/components/Heatmap.vue";
import {
  api,
  type SpendSummaryResponse,
  type SeriesResponse,
  type ForecastResponse,
} from "@/api/client";

// dashboard-burn-graph + dashboard-forecast + dashboard-heatmap all live
// here. The view loads three series in parallel and refreshes the burn
// graph every 30s so the live feel matches a real workload.

const summary = ref<SpendSummaryResponse | null>(null);
const burnSeries = ref<SeriesResponse | null>(null);
const forecast = ref<ForecastResponse | null>(null);
const heatmapSeries = ref<SeriesResponse | null>(null);
const error = ref<string | null>(null);
let pollHandle: number | undefined;

async function refresh() {
  try {
    const [s, b, f, h] = await Promise.all([
      api.spendSummary({ since: "7d" }),
      api.spendSeries({ since: "24h", bucket: "hour" }),
      api.spendForecast({ horizon_days: 7 }),
      api.spendSeries({ since: "24h", bucket: "hour", group: "model" }),
    ]);
    summary.value = s;
    burnSeries.value = b;
    forecast.value = f;
    heatmapSeries.value = h;
    error.value = null;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(async () => {
  await refresh();
  pollHandle = window.setInterval(refresh, 30_000);
});
onUnmounted(() => {
  if (pollHandle !== undefined) window.clearInterval(pollHandle);
});

// burn-graph data shaping.
const burnLabels = computed(() =>
  (burnSeries.value?.rows ?? []).map((r) => formatHour(r.BucketStart)),
);
const burnValues = computed(() => (burnSeries.value?.rows ?? []).map((r) => r.CostUSD));

// forecast: combine the daily history + predicted points into one chart.
const forecastLabels = computed(() => [
  ...(forecast.value?.history ?? []).map((r) => formatDay(r.BucketStart)),
  ...(forecast.value?.forecast ?? []).map((p) => formatDay(p.At)),
]);
const forecastHistory = computed(() => (forecast.value?.history ?? []).map((r) => r.CostUSD));
const forecastValues = computed(() => (forecast.value?.forecast ?? []).map((p) => p.Value));
const forecastLower = computed(() => (forecast.value?.forecast ?? []).map((p) => p.Lower));
const forecastUpper = computed(() => (forecast.value?.forecast ?? []).map((p) => p.Upper));

// heatmap: rows = model, cols = hour bucket.
const heatmapModels = computed(() => {
  const rows = heatmapSeries.value?.rows ?? [];
  const set = new Set<string>();
  for (const r of rows) set.add(r.GroupKey);
  return [...set];
});
const heatmapHours = computed(() => {
  const rows = heatmapSeries.value?.rows ?? [];
  const set = new Set<string>();
  for (const r of rows) set.add(formatHour(r.BucketStart));
  return [...set].sort();
});
const heatmapCells = computed(() =>
  (heatmapSeries.value?.rows ?? []).map((r) => ({
    rowKey: r.GroupKey,
    colKey: formatHour(r.BucketStart),
    value: r.CostUSD,
  })),
);

function formatHour(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
function formatDay(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}
</script>

<template>
  <h1>Spend</h1>

  <div v-if="error" class="error">{{ error }}</div>

  <section class="cards" v-if="summary">
    <article class="card">
      <header>Total spend (7d)</header>
      <div class="value">{{ summary.summary.CostUSD.toFixed(4) }} {{ summary.currency }}</div>
    </article>
    <article class="card">
      <header>Total tokens</header>
      <div class="value">{{ summary.summary.TotalTokens.toLocaleString() }}</div>
    </article>
    <article class="card">
      <header>Requests</header>
      <div class="value">{{ summary.summary.Requests.toLocaleString() }}</div>
    </article>
  </section>

  <section class="card chart-card" v-if="burnSeries">
    <header>
      <h2>Burn graph (last 24h)</h2>
      <span class="hint">refreshes every 30s</span>
    </header>
    <LineChart
      :labels="burnLabels"
      :series="burnValues"
      :unit="burnSeries.currency"
    />
  </section>

  <section class="card chart-card" v-if="forecast">
    <header>
      <h2>Spend forecast</h2>
      <span class="hint">history (solid) + {{ forecast.horizon_days }}d forecast (dashed) with 95% CI band</span>
    </header>
    <LineChart
      :labels="forecastLabels"
      :series="forecastHistory"
      :forecast="forecastValues"
      :forecast-lower="forecastLower"
      :forecast-upper="forecastUpper"
      :unit="forecast.currency"
    />
  </section>

  <section class="card heatmap-card" v-if="heatmapSeries && heatmapModels.length > 0">
    <header>
      <h2>Token heatmap</h2>
      <span class="hint">model × hour cost density</span>
    </header>
    <Heatmap
      :cells="heatmapCells"
      :row-labels="heatmapModels"
      :col-labels="heatmapHours"
      :unit="heatmapSeries.currency"
    />
  </section>

  <div v-if="summary && summary.summary.Requests === 0" class="empty">
    No prompts observed in the selected window. Send a request through
    the proxy and reload — the daemon writes events asynchronously.
  </div>
</template>

<style scoped>
h1 {
  font-size: var(--tk-text-2xl);
  margin: 0 0 var(--tk-space-4);
}
.error {
  color: var(--tk-color-danger);
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-danger);
  padding: var(--tk-space-3);
  border-radius: var(--tk-radius-md);
  margin-bottom: var(--tk-space-4);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: var(--tk-space-4);
  margin-bottom: var(--tk-space-5);
}
.card {
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-border);
  border-radius: var(--tk-radius-md);
  padding: var(--tk-space-4);
  box-shadow: var(--tk-shadow-card);
  margin-bottom: var(--tk-space-5);
}
.card header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  font-size: var(--tk-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--tk-color-text-mute);
  margin-bottom: var(--tk-space-3);
}
.card h2 {
  font-size: var(--tk-text-md);
  margin: 0;
  text-transform: none;
  letter-spacing: 0;
  color: var(--tk-color-text);
}
.hint {
  font-size: var(--tk-text-xs);
  text-transform: none;
  letter-spacing: 0;
  color: var(--tk-color-text-mute);
}
.value {
  font-size: var(--tk-text-xl);
  font-weight: 600;
}
.chart-card {
  padding-bottom: var(--tk-space-3);
}
.heatmap-card {
  padding-bottom: var(--tk-space-3);
}
.empty {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-sm);
  margin-top: var(--tk-space-4);
}
</style>
