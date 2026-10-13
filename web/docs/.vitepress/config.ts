import { defineConfig } from "vitepress";

const DESCRIPTION =
  "Guardrails for coding agents: what is left on every AI plan, in-session nudges, and limits your agents respect. Local-first, about 90 providers; prompts never leave your machine.";
// The published site, for the absolute URLs link previews need.
const SITE = "https://klarlabs-studio.github.io/tokenops/";

export default defineConfig({
  title: "TokenOps",
  description: DESCRIPTION,
  // GitHub Pages serves at https://klarlabs-studio.github.io/tokenops/.
  // Base must match the repo name so asset URLs resolve under the
  // subpath. CI sets DOCS_BASE=/tokenops/ explicitly; local dev keeps
  // the root base so `npm run dev` still works at /.
  base: process.env.DOCS_BASE || "/",
  cleanUrls: true,
  lastUpdated: true,
  head: [
    ["meta", { name: "theme-color", content: "#2742F5" }],
    // head entries are not prefixed with base, unlike themeConfig.logo.
    ["link", { rel: "icon", type: "image/svg+xml", href: `${process.env.DOCS_BASE || "/"}favicon.svg` }],
    // Link previews need an absolute image URL; assets/brand/make.py draws it.
    ["meta", { property: "og:type", content: "website" }],
    ["meta", { property: "og:site_name", content: "tokenops" }],
    ["meta", { property: "og:title", content: "tokenops — know what's left, set what's allowed" }],
    ["meta", { property: "og:description", content: DESCRIPTION }],
    ["meta", { property: "og:image", content: `${SITE}social.png` }],
    ["meta", { property: "og:image:width", content: "1200" }],
    ["meta", { property: "og:image:height", content: "630" }],
    ["meta", { property: "og:image:alt", content: "tokenops: Know what's left. Set what's allowed. Let your agents work." }],
    ["meta", { name: "twitter:card", content: "summary_large_image" }],
    ["meta", { name: "twitter:image", content: `${SITE}social.png` }],
  ],
  themeConfig: {
    logo: { light: "/wordmark.svg", dark: "/wordmark-dark.svg", alt: "tokenops" },
    siteTitle: false,
    nav: [
      { text: "Guide", link: "/guide/quickstart" },
      { text: "Integrations", link: "/integrations/sdk-overview" },
      { text: "Architecture", link: "/architecture/overview" },
      { text: "Runbook", link: "/runbook/" },
      { text: "Changelog", link: "/changelog" },
      { text: "GitHub", link: "https://github.com/klarlabs-studio/tokenops" },
    ],
    sidebar: {
      "/guide/": [
        {
          text: "Getting started",
          items: [
            { text: "Quickstart", link: "/guide/quickstart" },
            { text: "Configuration", link: "/guide/configuration" },
            { text: "CLI", link: "/guide/cli" },
            { text: "Daemon API", link: "/guide/api" },
            { text: "Action boundaries", link: "/guide/boundaries" },
            { text: "Team plane", link: "/guide/team" },
          ],
        },
      ],
      "/integrations/": [
        {
          text: "Start here",
          items: [
            { text: "Coverage", link: "/integrations/coverage" },
          ],
        },
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
            { text: "opencode", link: "/integrations/opencode" },
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
      { icon: "github", link: "https://github.com/klarlabs-studio/tokenops" },
    ],
    footer: {
      message: "Apache 2.0 licensed.",
      copyright: "© 2026 TokenOps contributors.",
    },
  },
});
