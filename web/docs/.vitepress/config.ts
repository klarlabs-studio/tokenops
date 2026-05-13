import { defineConfig } from "vitepress";

export default defineConfig({
  title: "TokenOps",
  description: "Predict rate-limit cutoffs inside your AI agent. Local MCP server + CLI that tracks Claude Max, ChatGPT Plus/Pro/Team, GitHub Copilot, and Cursor plan windows.",
  // GitHub Pages serves at https://felixgeelhaar.github.io/tokenops/.
  // Base must match the repo name so asset URLs resolve under the
  // subpath. CI sets DOCS_BASE=/tokenops/ explicitly; local dev keeps
  // the root base so `npm run dev` still works at /.
  base: process.env.DOCS_BASE || "/",
  cleanUrls: true,
  lastUpdated: true,
  head: [
    ["meta", { name: "theme-color", content: "#4159d6" }],
  ],
  themeConfig: {
    nav: [
      { text: "Guide", link: "/guide/quickstart" },
      { text: "Integrations", link: "/integrations/sdk-overview" },
      { text: "Architecture", link: "/architecture/overview" },
      { text: "Runbook", link: "/runbook/" },
      { text: "GitHub", link: "https://github.com/felixgeelhaar/tokenops" },
    ],
    sidebar: {
      "/guide/": [
        {
          text: "Getting started",
          items: [
            { text: "Quickstart", link: "/guide/quickstart" },
            { text: "Configuration", link: "/guide/configuration" },
            { text: "CLI", link: "/guide/cli" },
          ],
        },
      ],
      "/integrations/": [
        {
          text: "SDK shims",
          items: [
            { text: "Overview", link: "/integrations/sdk-overview" },
            { text: "OpenAI", link: "/integrations/openai" },
            { text: "Anthropic", link: "/integrations/anthropic" },
            { text: "Gemini", link: "/integrations/gemini" },
          ],
        },
        {
          text: "CLI tools",
          items: [
            { text: "Claude Code", link: "/integrations/claude-code" },
            { text: "Codex", link: "/integrations/codex-cli" },
            { text: "Gemini CLI", link: "/integrations/gemini-cli" },
          ],
        },
        {
          text: "Other",
          items: [
            { text: "MCP server", link: "/integrations/mcp-server" },
            { text: "OTLP exporter", link: "/integrations/otlp" },
          ],
        },
      ],
      "/architecture/": [
        {
          text: "Architecture",
          items: [
            { text: "Overview", link: "/architecture/overview" },
            { text: "Event schema", link: "/architecture/event-schema" },
            { text: "Optimization pipeline", link: "/architecture/optimization-pipeline" },
            { text: "Storage + retention", link: "/architecture/storage" },
          ],
        },
      ],
      "/runbook/": [
        {
          text: "Operations",
          items: [
            { text: "Overview", link: "/runbook/" },
            { text: "Health + readiness", link: "/runbook/health" },
            { text: "Cache", link: "/runbook/cache" },
            { text: "Performance", link: "/runbook/performance" },
          ],
        },
      ],
    },
    socialLinks: [
      { icon: "github", link: "https://github.com/felixgeelhaar/tokenops" },
    ],
    footer: {
      message: "Apache 2.0 licensed.",
      copyright: "© 2026 TokenOps contributors.",
    },
  },
});
