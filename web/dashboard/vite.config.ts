import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";

// Vite proxies /api/* to the local TokenOps daemon during development so
// the dashboard can be served separately from the daemon's listener.
// In production the dashboard is built into static assets and served by
// the daemon directly, eliminating the proxy hop.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/healthz": "http://127.0.0.1:7878",
      "/readyz": "http://127.0.0.1:7878",
      "/version": "http://127.0.0.1:7878",
      "/api": "http://127.0.0.1:7878",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
