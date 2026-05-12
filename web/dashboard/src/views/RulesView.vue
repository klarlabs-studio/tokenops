<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  api,
  type RuleDocumentSummary,
  type RuleFinding,
  type RuleCompressResult,
} from "@/api/client";

// Rule Intelligence dashboard (issue #12). Three panels:
//  1. Leaderboard — per-document token cost + section count, sorted by
//     total tokens descending.
//  2. Conflicts — redundant / drift / anti_pattern findings.
//  3. Compression — original vs compressed tokens per document, with the
//     quality gate's accepted flag.
//
// All three panels load on mount; the daemon's /api/rules/* endpoints
// re-ingest on every request so edits on disk surface immediately.

const docs = ref<RuleDocumentSummary[]>([]);
const findings = ref<RuleFinding[]>([]);
const compress = ref<RuleCompressResult[]>([]);
const error = ref<string | null>(null);
const loading = ref(true);

async function load() {
  loading.value = true;
  try {
    const [a, c, cmp] = await Promise.all([
      api.rulesAnalyze(),
      api.rulesConflicts(),
      api.rulesCompress(),
    ]);
    docs.value = a.documents ?? [];
    findings.value = c.findings ?? [];
    compress.value = cmp.results ?? [];
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const totalTokens = computed(() =>
  docs.value.reduce((sum, d) => sum + d.TotalTokens, 0),
);
const sortedDocs = computed(() =>
  [...docs.value].sort((a, b) => b.TotalTokens - a.TotalTokens),
);
const findingsByKind = computed(() => {
  const groups: Record<string, RuleFinding[]> = {
    redundant: [],
    drift: [],
    anti_pattern: [],
  };
  for (const f of findings.value) {
    (groups[f.Kind] ?? (groups[f.Kind] = [])).push(f);
  }
  return groups;
});

function badge(accepted: boolean): string {
  return accepted ? "accepted" : "rejected";
}

function compressionRatio(r: RuleCompressResult): string {
  if (r.original_tokens === 0) return "—";
  const pct = ((r.original_tokens - r.compressed_tokens) / r.original_tokens) * 100;
  return pct >= 0 ? `-${pct.toFixed(1)}%` : `+${(-pct).toFixed(1)}%`;
}
</script>

<template>
  <h1>Rules</h1>
  <p class="hint">
    Operational rule artifacts (CLAUDE.md, AGENTS.md, Cursor, MCP policies) as
    first-class telemetry — token ROI, conflicts, distillation.
  </p>

  <div v-if="error" class="error">{{ error }}</div>
  <div v-if="loading && !docs.length" class="empty">Loading rule corpus…</div>

  <section class="kpis" v-if="docs.length > 0">
    <article class="card">
      <header>Sources</header>
      <div class="value">{{ docs.length }}</div>
      <div class="hint">rule artifacts indexed</div>
    </article>
    <article class="card">
      <header>Total tokens</header>
      <div class="value">{{ totalTokens.toLocaleString() }}</div>
      <div class="hint">across all rule corpora</div>
    </article>
    <article class="card">
      <header>Conflicts</header>
      <div class="value">{{ findings.length }}</div>
      <div class="hint">redundant + drift + anti-pattern</div>
    </article>
  </section>

  <section class="card" v-if="sortedDocs.length > 0">
    <header class="header-row">
      <h2>Token cost leaderboard</h2>
      <span class="hint">descending by total tokens</span>
    </header>
    <table>
      <thead>
        <tr>
          <th>Path</th>
          <th>Kind</th>
          <th class="num">Tokens</th>
          <th class="num">Chars</th>
          <th class="num">Sections</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="d in sortedDocs" :key="d.SourceID">
          <td>{{ d.Path || d.SourceID }}</td>
          <td class="kind">{{ d.Source }}</td>
          <td class="num">{{ d.TotalTokens.toLocaleString() }}</td>
          <td class="num">{{ d.TotalChars.toLocaleString() }}</td>
          <td class="num">{{ d.Sections }}</td>
        </tr>
      </tbody>
    </table>
  </section>

  <section class="card" v-if="findings.length > 0">
    <header class="header-row">
      <h2>Conflicts</h2>
      <span class="hint">redundant / drift / anti-pattern</span>
    </header>
    <div v-for="(kindFindings, kind) in findingsByKind" :key="kind">
      <h3 v-if="kindFindings.length > 0">{{ kind }} ({{ kindFindings.length }})</h3>
      <article v-for="(f, i) in kindFindings" :key="kind + i" class="finding">
        <div class="detail">{{ f.Detail }}</div>
        <ul class="members">
          <li v-for="m in f.Members" :key="m">{{ m }}</li>
        </ul>
        <div v-if="f.Triggers && f.Triggers.length" class="triggers">
          triggers: {{ f.Triggers.join(", ") }}
        </div>
      </article>
    </div>
  </section>

  <section class="card" v-if="compress.length > 0">
    <header class="header-row">
      <h2>Compression preview</h2>
      <span class="hint">original vs distilled tokens</span>
    </header>
    <table>
      <thead>
        <tr>
          <th>Path</th>
          <th class="num">Original</th>
          <th class="num">Compressed</th>
          <th class="num">Ratio</th>
          <th class="num">Dropped</th>
          <th>Quality</th>
          <th>Gate</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in compress" :key="r.source_id">
          <td>{{ r.path }}</td>
          <td class="num">{{ r.original_tokens.toLocaleString() }}</td>
          <td class="num">{{ r.compressed_tokens.toLocaleString() }}</td>
          <td class="num">{{ compressionRatio(r) }}</td>
          <td class="num">{{ r.dropped_sections }}</td>
          <td>{{ r.quality_score.toFixed(2) }}</td>
          <td :class="`gate gate-${badge(r.accepted)}`">{{ badge(r.accepted) }}</td>
        </tr>
      </tbody>
    </table>
  </section>

  <div v-if="!loading && docs.length === 0 && !error" class="empty">
    No rule artifacts found under the daemon root. Confirm
    <code>rules.enabled</code> and <code>rules.root</code> in config.yaml.
  </div>
</template>

<style scoped>
h1 {
  font-size: var(--tk-text-2xl);
  margin: 0 0 var(--tk-space-2);
}
.hint {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
}
.error {
  color: var(--tk-color-danger);
  margin: var(--tk-space-4) 0;
}
.empty {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-sm);
  margin: var(--tk-space-4) 0;
}
.kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: var(--tk-space-4);
  margin: var(--tk-space-4) 0;
}
.card {
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-border);
  border-radius: var(--tk-radius-md);
  padding: var(--tk-space-4);
  box-shadow: var(--tk-shadow-card);
  margin-bottom: var(--tk-space-4);
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
  text-transform: none;
  letter-spacing: 0;
}
.header-row h2 {
  margin: 0;
  font-size: var(--tk-text-md);
}
.value {
  font-size: var(--tk-text-xl);
  font-weight: 600;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--tk-text-sm);
  margin-top: var(--tk-space-2);
}
th,
td {
  padding: var(--tk-space-2);
  border-bottom: 1px solid var(--tk-color-border);
  text-align: left;
}
th {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.kind {
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
}
.finding {
  border-left: 3px solid var(--tk-color-border);
  padding: var(--tk-space-2) var(--tk-space-3);
  margin: var(--tk-space-2) 0;
}
.detail {
  font-size: var(--tk-text-sm);
  margin-bottom: var(--tk-space-1);
}
.members {
  list-style: none;
  padding: 0;
  margin: 0;
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
}
.members li::before {
  content: "→ ";
}
.triggers {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
  margin-top: var(--tk-space-1);
}
.gate {
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-xs);
}
.gate-accepted {
  color: var(--tk-color-success);
}
.gate-rejected {
  color: var(--tk-color-danger);
}
</style>
