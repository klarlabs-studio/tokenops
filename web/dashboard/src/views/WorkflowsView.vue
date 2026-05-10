<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  api,
  type WorkflowDetailResponse,
  type WorkflowSummary,
} from "@/api/client";

// dashboard-timeline: list of workflows on the left, drilldown into a
// trace on the right. Steps are rendered as a stacked timeline showing
// context delta + latency + agent.

const workflows = ref<WorkflowSummary[]>([]);
const detail = ref<WorkflowDetailResponse | null>(null);
const selected = ref<string | null>(null);
const error = ref<string | null>(null);

async function loadList() {
  try {
    const res = await api.workflows({ since: "7d" });
    workflows.value = res.workflows.sort((a, b) => b.cost_usd - a.cost_usd);
    if (!selected.value && workflows.value.length > 0) {
      selected.value = workflows.value[0].workflow_id;
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function loadDetail(id: string) {
  try {
    detail.value = await api.workflowDetail(id);
  } catch (err) {
    detail.value = null;
    error.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(loadList);
watch(selected, (id) => {
  if (id) loadDetail(id);
});

const maxContext = computed(() => {
  const trace = detail.value?.trace;
  if (!trace) return 0;
  return trace.Steps.reduce((m, s) => Math.max(m, s.Prompt.input_tokens), 0);
});

function pct(value: number): number {
  if (maxContext.value === 0) return 0;
  return Math.min(100, (value / maxContext.value) * 100);
}

function fmtMs(ns: number): string {
  return `${(ns / 1e6).toFixed(0)}ms`;
}
function fmtMoney(v: number, c: string): string {
  return `${v.toFixed(4)} ${c}`;
}
</script>

<template>
  <h1>Workflows</h1>
  <div v-if="error" class="error">{{ error }}</div>

  <div class="layout" v-if="workflows.length > 0">
    <aside class="list">
      <div class="header">{{ workflows.length }} workflows · last 7d</div>
      <ul>
        <li
          v-for="wf in workflows"
          :key="wf.workflow_id"
          :class="{ active: selected === wf.workflow_id }"
          @click="selected = wf.workflow_id"
        >
          <div class="id">{{ wf.workflow_id || "(unknown)" }}</div>
          <div class="meta">
            {{ wf.requests }} req · {{ wf.tokens.toLocaleString() }} tok ·
            <strong>{{ fmtMoney(wf.cost_usd, detail?.currency ?? "USD") }}</strong>
          </div>
        </li>
      </ul>
    </aside>

    <section class="detail" v-if="detail">
      <header>
        <h2>{{ detail.trace.WorkflowID }}</h2>
        <div class="hint">
          {{ detail.trace.StepCount }} steps · max context
          {{ detail.trace.MaxContextSize.toLocaleString() }} tokens · total
          {{ fmtMoney(detail.trace.TotalCostUSD, detail.currency) }}
        </div>
      </header>

      <div v-if="detail.findings && detail.findings.length > 0" class="findings">
        <h3>Coaching findings</h3>
        <ul>
          <li v-for="(f, i) in detail.findings" :key="i">
            <span class="kind">{{ f.Kind }}</span>
            <span class="summary">{{ f.Summary }}</span>
            <span v-if="f.EstimatedSavingsTokens" class="save">
              save ~{{ f.EstimatedSavingsTokens.toLocaleString() }} tok
            </span>
          </li>
        </ul>
      </div>

      <h3>Step timeline</h3>
      <table class="steps">
        <thead>
          <tr>
            <th>#</th>
            <th>Model</th>
            <th>Agent</th>
            <th>In tok</th>
            <th>Δ ctx</th>
            <th>Latency</th>
            <th>Context</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="step in detail.trace.Steps" :key="step.Index">
            <td>{{ step.Index + 1 }}</td>
            <td>{{ step.Prompt.request_model }}</td>
            <td>{{ step.Prompt.agent_id ?? "—" }}</td>
            <td>{{ step.Prompt.input_tokens.toLocaleString() }}</td>
            <td :class="{ neg: step.ContextDelta < 0, pos: step.ContextDelta > 0 }">
              {{ step.ContextDelta > 0 ? "+" : "" }}{{ step.ContextDelta }}
            </td>
            <td>{{ fmtMs(step.Latency) }}</td>
            <td class="bar">
              <div class="bar-fill" :style="{ width: pct(step.Prompt.input_tokens) + '%' }"></div>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
  <div v-else class="empty">
    No workflows observed yet. Tag prompts with
    <code>X-Tokenops-Workflow-Id</code> to populate this view.
  </div>
</template>

<style scoped>
h1 {
  font-size: var(--tk-text-2xl);
  margin: 0 0 var(--tk-space-4);
}
h3 {
  font-size: var(--tk-text-md);
  margin: var(--tk-space-4) 0 var(--tk-space-2);
}
.error {
  color: var(--tk-color-danger);
  margin-bottom: var(--tk-space-4);
}
.layout {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: var(--tk-space-4);
  align-items: start;
}
.list {
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-border);
  border-radius: var(--tk-radius-md);
  overflow: hidden;
}
.list .header {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
  padding: var(--tk-space-3) var(--tk-space-4);
  border-bottom: 1px solid var(--tk-color-border);
}
.list ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
.list li {
  padding: var(--tk-space-3) var(--tk-space-4);
  border-bottom: 1px solid var(--tk-color-border);
  cursor: pointer;
}
.list li:last-child {
  border-bottom: none;
}
.list li.active,
.list li:hover {
  background: var(--tk-color-surface-2);
}
.id {
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-sm);
}
.meta {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
  margin-top: 2px;
}
.detail {
  background: var(--tk-color-surface);
  border: 1px solid var(--tk-color-border);
  border-radius: var(--tk-radius-md);
  padding: var(--tk-space-5);
  box-shadow: var(--tk-shadow-card);
}
.detail header h2 {
  margin: 0;
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-lg);
}
.hint {
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
  margin-top: var(--tk-space-1);
}
.findings ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
.findings li {
  display: flex;
  gap: var(--tk-space-3);
  align-items: baseline;
  padding: var(--tk-space-2) 0;
  font-size: var(--tk-text-sm);
}
.findings .kind {
  font-family: var(--tk-font-mono);
  font-size: var(--tk-text-xs);
  background: var(--tk-color-surface-2);
  padding: 2px 6px;
  border-radius: var(--tk-radius-sm);
}
.findings .save {
  margin-left: auto;
  color: var(--tk-color-success);
  font-size: var(--tk-text-xs);
}
.steps {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--tk-text-sm);
}
.steps th,
.steps td {
  padding: var(--tk-space-2);
  border-bottom: 1px solid var(--tk-color-border);
  text-align: left;
}
.steps th {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.steps .pos {
  color: var(--tk-color-warning);
}
.steps .neg {
  color: var(--tk-color-success);
}
.steps .bar {
  width: 200px;
}
.bar-fill {
  height: 8px;
  background: var(--tk-color-accent);
  border-radius: var(--tk-radius-sm);
}
.empty {
  color: var(--tk-color-text-mute);
  font-size: var(--tk-text-sm);
}
</style>
