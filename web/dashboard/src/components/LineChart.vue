<script setup lang="ts">
import { computed } from "vue";

// Minimal dependency-free SVG line chart. Two optional bands:
//  - `series`: the primary (history) line
//  - `forecast`: an additional line drawn dotted to the right of history
//  - `confidence`: optional [lower, upper] arrays paired with `forecast`
//
// The chart auto-scales the Y axis to the combined max across all
// series. X axis is ordinal (one tick per point) — fine for the
// hourly/daily buckets the dashboard sources.

const props = defineProps<{
  labels: string[];
  series: number[];
  forecast?: number[];
  forecastLower?: number[];
  forecastUpper?: number[];
  height?: number;
  unit?: string;
}>();

const W = 720;
const H = computed(() => props.height ?? 240);
const PAD = { top: 16, right: 12, bottom: 26, left: 48 };

const allValues = computed(() => {
  const fv = props.forecast ?? [];
  const fu = props.forecastUpper ?? [];
  return [...props.series, ...fv, ...fu];
});

const yMax = computed(() => {
  const m = Math.max(0, ...allValues.value);
  return m > 0 ? m * 1.1 : 1;
});

const xCount = computed(() => props.labels.length);

function xCoord(i: number): number {
  if (xCount.value <= 1) return PAD.left;
  const inner = W - PAD.left - PAD.right;
  return PAD.left + (i / (xCount.value - 1)) * inner;
}

function yCoord(v: number): number {
  const inner = H.value - PAD.top - PAD.bottom;
  return PAD.top + inner - (v / yMax.value) * inner;
}

function pathFor(values: number[], offset = 0): string {
  return values
    .map((v, i) => {
      const x = xCoord(i + offset);
      const y = yCoord(v);
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

function bandPath(lower: number[], upper: number[], offset = 0): string {
  if (lower.length === 0) return "";
  const top = upper.map((v, i) => `${xCoord(i + offset).toFixed(1)},${yCoord(v).toFixed(1)}`);
  const bot = lower
    .slice()
    .reverse()
    .map((v, i) => {
      const idx = lower.length - 1 - i;
      return `${xCoord(idx + offset).toFixed(1)},${yCoord(v).toFixed(1)}`;
    });
  return "M" + top.join(" L") + " L" + bot.join(" L") + " Z";
}

const seriesPath = computed(() => pathFor(props.series));
const forecastPath = computed(() =>
  props.forecast ? pathFor(props.forecast, props.series.length - 1) : "",
);
const bandPathStr = computed(() =>
  props.forecastLower && props.forecastUpper
    ? bandPath(props.forecastLower, props.forecastUpper, props.series.length - 1)
    : "",
);

// Simple Y-axis ticks: 0, mid, max.
const yTicks = computed(() => {
  const m = yMax.value;
  return [0, m / 2, m].map((v) => ({ v, y: yCoord(v) }));
});

// Pick at most 6 X labels so they don't crowd.
const xTicks = computed(() => {
  const labels = props.labels;
  if (labels.length === 0) return [];
  const stride = Math.max(1, Math.ceil(labels.length / 6));
  return labels
    .map((label, i) => ({ label, x: xCoord(i), i }))
    .filter((_, i) => i % stride === 0 || i === labels.length - 1);
});

function fmtY(v: number): string {
  if (v >= 1000) return v.toFixed(0);
  if (v >= 1) return v.toFixed(2);
  return v.toFixed(4);
}
</script>

<template>
  <svg :viewBox="`0 0 ${W} ${H}`" class="chart" preserveAspectRatio="xMidYMid meet">
    <!-- Y grid -->
    <g class="grid">
      <line
        v-for="t in yTicks"
        :key="`grid-${t.v}`"
        :x1="PAD.left"
        :x2="W - PAD.right"
        :y1="t.y"
        :y2="t.y"
      />
    </g>

    <!-- Confidence band -->
    <path v-if="bandPathStr" :d="bandPathStr" class="band" />

    <!-- Forecast -->
    <path v-if="forecastPath" :d="forecastPath" class="forecast" />

    <!-- Primary series -->
    <path v-if="seriesPath" :d="seriesPath" class="series" />

    <!-- Y axis labels -->
    <g class="axis">
      <text
        v-for="t in yTicks"
        :key="`yl-${t.v}`"
        :x="PAD.left - 6"
        :y="t.y"
        text-anchor="end"
        dominant-baseline="middle"
      >
        {{ fmtY(t.v) }}{{ unit ? ` ${unit}` : "" }}
      </text>
    </g>

    <!-- X axis labels -->
    <g class="axis">
      <text
        v-for="t in xTicks"
        :key="`xl-${t.i}`"
        :x="t.x"
        :y="H - 8"
        text-anchor="middle"
      >
        {{ t.label }}
      </text>
    </g>
  </svg>
</template>

<style scoped>
.chart {
  width: 100%;
  height: auto;
  display: block;
}
.grid line {
  stroke: var(--tk-color-border);
  stroke-width: 1;
  stroke-dasharray: 2 4;
  opacity: 0.6;
}
.series {
  fill: none;
  stroke: var(--tk-color-accent);
  stroke-width: 2;
}
.forecast {
  fill: none;
  stroke: var(--tk-color-accent);
  stroke-width: 2;
  stroke-dasharray: 4 4;
  opacity: 0.85;
}
.band {
  fill: var(--tk-color-accent);
  opacity: 0.12;
  stroke: none;
}
.axis text {
  fill: var(--tk-color-text-mute);
  font-size: 11px;
  font-family: var(--tk-font-mono);
}
</style>
