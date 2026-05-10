<script setup lang="ts">
import { computed } from "vue";

interface Cell {
  rowKey: string;
  colKey: string;
  value: number;
}

const props = defineProps<{
  cells: Cell[];
  rowLabels: string[];
  colLabels: string[];
  unit?: string;
}>();

const max = computed(() =>
  props.cells.reduce((m, c) => (c.value > m ? c.value : m), 0),
);

function cellAt(row: string, col: string): number {
  const c = props.cells.find((c) => c.rowKey === row && c.colKey === col);
  return c?.value ?? 0;
}

function cellColor(row: string, col: string): string {
  if (max.value === 0) return "var(--tk-color-surface-2)";
  const v = cellAt(row, col);
  const intensity = Math.min(1, v / max.value);
  if (intensity === 0) return "var(--tk-color-surface-2)";
  // Lerp from a neutral surface tint to the accent. Using rgb()
  // because CSS color-mix() isn't universal yet.
  const alpha = 0.15 + intensity * 0.75;
  return `rgba(107, 138, 255, ${alpha.toFixed(3)})`;
}
</script>

<template>
  <div class="heatmap" :style="{ '--cols': colLabels.length }">
    <div class="corner"></div>
    <div v-for="c in colLabels" :key="`col-${c}`" class="col-label">{{ c }}</div>

    <template v-for="row in rowLabels" :key="`row-${row}`">
      <div class="row-label" :title="row">{{ row || "(unknown)" }}</div>
      <div
        v-for="col in colLabels"
        :key="`cell-${row}-${col}`"
        class="cell"
        :style="{ background: cellColor(row, col) }"
        :title="`${row} · ${col}: ${cellAt(row, col).toFixed(4)} ${unit ?? ''}`"
      ></div>
    </template>
  </div>
</template>

<style scoped>
.heatmap {
  display: grid;
  grid-template-columns: 160px repeat(var(--cols), minmax(20px, 1fr));
  gap: 2px;
  font-size: var(--tk-text-xs);
}
.corner {
  background: transparent;
}
.col-label {
  color: var(--tk-color-text-mute);
  text-align: center;
  font-family: var(--tk-font-mono);
}
.row-label {
  color: var(--tk-color-text-mute);
  font-family: var(--tk-font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cell {
  height: 20px;
  border-radius: 2px;
}
</style>
