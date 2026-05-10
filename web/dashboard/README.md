# TokenOps dashboard

Vue 3 + Vite single-page app served alongside the TokenOps daemon. The
skeleton wires routing, layout, design tokens, and an API client; the
data-driven views (burn graph, heatmap, timeline, forecast,
optimization list) land under their own tasks.

## Develop

```bash
cd web/dashboard
npm install
npm run dev
```

The dev server runs on `http://localhost:5173` and proxies all daemon
endpoints (`/healthz`, `/readyz`, `/version`, `/api/*`) to
`127.0.0.1:7878`. Start the daemon in a second terminal:

```bash
TOKENOPS_STORAGE_ENABLED=true tokenops start
```

## Build

```bash
npm run build
```

Output lands in `dist/`. The daemon embeds these assets in production
builds and serves the SPA from the same listener as the proxy
endpoints.

## Layout

```
src/
  api/      — typed daemon client
  router/   — vue-router config (4 routes + 404)
  styles/   — design tokens (single CSS file, all custom properties)
  views/    — one file per route (placeholders for sub-task work)
  App.vue   — sidebar + RouterView shell
  main.ts   — bootstrap
```

## Sub-tasks that consume this scaffold

- `dashboard-auth` — local auth gate
- `dashboard-burn-graph` — live SSE/WebSocket spend chart
- `dashboard-heatmap` — workflow × time density
- `dashboard-timeline` — per-workflow step timeline
- `dashboard-forecast` — Holt-forecast charts with confidence bands
- `dashboard-opt-list` — optimization recommendations + accept/reject
