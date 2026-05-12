<script setup lang="ts">
import { onMounted, ref } from "vue";
import { RouterLink, RouterView } from "vue-router";
import { api, type Version } from "@/api/client";

const version = ref<Version | null>(null);
const ready = ref<"unknown" | "ready" | "not_ready">("unknown");

onMounted(async () => {
  try {
    version.value = await api.version();
  } catch {
    version.value = null;
  }
  try {
    const r = await api.ready();
    ready.value = r.status;
  } catch {
    ready.value = "not_ready";
  }
});

const navItems = [
  { to: "/", label: "Overview" },
  { to: "/spend", label: "Spend" },
  { to: "/workflows", label: "Workflows" },
  { to: "/optimizations", label: "Optimizations" },
  { to: "/rules", label: "Rules" },
  { to: "/events", label: "Events" },
  { to: "/audit", label: "Audit" },
];
</script>

<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">TokenOps</div>
      <nav>
        <RouterLink v-for="item in navItems" :key="item.to" :to="item.to">
          {{ item.label }}
        </RouterLink>
      </nav>
      <div class="footer">
        <div class="status" :class="ready">
          <span class="dot"></span>
          {{ ready === "ready" ? "Daemon ready" : ready === "not_ready" ? "Daemon offline" : "…" }}
        </div>
        <div v-if="version" class="version">v{{ version.version }}</div>
      </div>
    </aside>
    <main class="content">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: 220px 1fr;
  height: 100vh;
}
.sidebar {
  background: var(--tk-color-surface);
  border-right: 1px solid var(--tk-color-border);
  display: flex;
  flex-direction: column;
  padding: var(--tk-space-5) var(--tk-space-4);
  gap: var(--tk-space-4);
}
.brand {
  font-size: var(--tk-text-lg);
  font-weight: 700;
  letter-spacing: 0.02em;
}
nav {
  display: flex;
  flex-direction: column;
  gap: var(--tk-space-1);
}
nav a {
  color: var(--tk-color-text-mute);
  padding: var(--tk-space-2) var(--tk-space-3);
  border-radius: var(--tk-radius-sm);
  font-size: var(--tk-text-sm);
}
nav a:hover {
  background: var(--tk-color-surface-2);
  color: var(--tk-color-text);
  text-decoration: none;
}
nav a.router-link-active {
  background: var(--tk-color-surface-2);
  color: var(--tk-color-text);
}
.footer {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: var(--tk-space-1);
  font-size: var(--tk-text-xs);
  color: var(--tk-color-text-mute);
}
.status {
  display: inline-flex;
  align-items: center;
  gap: var(--tk-space-2);
}
.status .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--tk-color-text-mute);
}
.status.ready .dot {
  background: var(--tk-color-success);
}
.status.not_ready .dot {
  background: var(--tk-color-danger);
}
.content {
  overflow-y: auto;
  padding: var(--tk-space-5);
}
</style>
