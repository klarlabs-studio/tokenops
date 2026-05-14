---
layout: home
hero:
  name: TokenOps
  text: Never lose a 4-hour session to a mid-task rate-limit cutoff
  tagline: Local MCP server + CLI for flat-rate AI subscriptions (Claude Max, ChatGPT Plus / Pro / Team, GitHub Copilot, Cursor, Mistral Le Chat Pro, Codex Plus). Your agent asks `tokenops_session_budget` mid-task and gets back a headroom gauge plus one of four recommended actions — instead of you tab-flipping to a vendor `/status` page.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
features:
  - title: Predict
    details: '`tokenops_session_budget` returns a coloured headroom gauge and a closed-enum `recommended_action ∈ continue | slow_down | switch_model | wait_for_reset`. 13-plan catalog with dated vendor source URLs pinned in code (Claude Max 5x / 20x / Pro, Claude Code, ChatGPT Plus / Pro / Team, Copilot, Cursor, Mistral, Codex).'
  - title: Integrate
    details: '25 MCP tools agents call directly. `tokenops init --detect` sniffs your installed clients (Claude Code/Desktop, Cursor, ChatGPT, env-var API keys) and prints the exact `tokenops plan set …` commands. Works with Claude Code, Cursor, aider, Codex.'
  - title: Visualize
    details: 'Vue + D3 dashboard at `http://tokenops.local:7878/dashboard` — per-model stacked area cost chart, tokens-per-bucket stacked bar, provider + model filters that persist across refresh, 15s auto-refresh. Inline SVG sparkline + headroom gauge also render directly in MCP tool responses.'
  - title: Trust
    details: 'Every prediction carries `signal_quality.level` (low / medium / high) plus a one-line caveat and upgrade paths. Heuristic mode is labelled; proxied mode is labelled. Default install reports low confidence — earn high in 5 minutes with an Anthropic Admin API key.'
  - title: Self-host
    details: 'Local SQLite, no cloud account, no telemetry. Daemon advertises `tokenops.local` over mDNS so the dashboard URL is memorable. Shared-secret auth on `/dashboard` + `/api/*` (mint-and-rotate via `tokenops dashboard rotate-token`). Apache 2.0.'
---

<script setup>
import { withBase } from 'vitepress'
</script>

<video autoplay loop muted playsinline preload="metadata" :poster="withBase('/media/dashboard-poster.jpg')" style="width:100%;max-width:960px;display:block;margin:24px auto;border-radius:8px;border:1px solid var(--vp-c-divider);">
  <source :src="withBase('/media/dashboard.webm')" type="video/webm">
  <em>Local TokenOps dashboard: cost-over-time, per-model stacked area, filter dropdowns. Captured live via the Scout MCP browser-recorder against the v0.11.0 daemon.</em>
</video>

## Instead of `/status` and a billing tab

Today, when your agent is mid-refactor and Claude returns "limit reached, resets in 4h," your options are: open the Anthropic console in another tab, paste `/status` into your terminal, refresh the billing page, or just eat the wait. None of those tell the *agent* anything.

TokenOps puts the headroom check inside the agent's loop. The agent calls one MCP tool, sees `62% headroom, 1h41m to reset, recommended_action: continue` (or `slow_down`, `switch_model`, `wait_for_reset`), and proceeds without you alt-tabbing.

## For agents

The MCP surface is a deterministic guardrail, not vibes-in-the-loop. Closed action enum, calibrated confidence:

```json
{
  "tool": "tokenops_session_budget",
  "response": {
    "headroom_pct": 62,
    "window_resets_in": "1h41m",
    "recommended_action": "continue",
    "signal_quality": {
      "level": "high",
      "source": "vendor_usage_api",
      "caveat": null
    }
  }
}
```

`recommended_action` is a closed enum (`continue | slow_down | switch_model | wait_for_reset`) so the agent picks the right branch without parsing prose. `signal_quality.level` lets the agent decide how much to trust the call: stay aggressive on `high`, defer to the human on `low`.

## What's new in v0.11.0

- **Per-model stacked area on the cost panel.** When no model filter is active, the dashboard cost chart stacks one area layer per model so operators see the spend mix at a glance. Top-5 legend + "+N more" chip; colour scale stable across refresh.
- **`tokenops vendor-usage backfill --hours N`.** One-shot pull of historical Anthropic Admin API usage. Deterministic envelope IDs, so re-running or running alongside the live poller never double-counts. `--dry-run` previews without writing.
- **`tokenops dashboard rotate-token`.** Mint a fresh 32-byte URL-safe secret and revoke the old one. Useful after sharing a dashboard URL with a colleague.
- **Mistral Le Chat Pro + Codex Plus** added to the plan catalog. `eventschema.ProviderMistral` plus mistral-large/medium/small + codestral list prices ship in the default spend table.
- **Dashboard filter persistence + favicon.** Window / provider / model picks survive page refresh via localStorage. Inline SVG favicon for the browser tab.

## Earlier highlights (v0.10.x)

- **`tokenops.local` via mDNS (v0.10.1)** — The daemon advertises itself over zeroconf on Start, so the dashboard URL becomes `http://tokenops.local:7878/dashboard` instead of a bare loopback address. The `tokenops_dashboard` MCP tool prefers it; falls back to `127.0.0.1` when `.local` resolution isn't available.
- **Vendor /usage ingestion (v0.10.2)** — Two new signal sources upgrade Anthropic confidence beyond the heuristic default. The **Claude Code stats cache reader** parses `~/.claude/stats-cache.json` and emits per-(date, model) deltas (signal_quality → medium). The **Anthropic Admin API poller** calls `/v1/organizations/usage_report/messages` every 5min with an admin key (signal_quality → high). Both wired through `config.vendor_usage.*`; both honest about the Claude Max 5h-window blind spot.
- **Dashboard auth (v0.10.3)** — `/dashboard` + `/api/*` now require a shared-secret token (`/healthz`, `/readyz`, `/version` stay public). Daemon mints + persists the token automatically at `~/.tokenops/dashboard.token`; the MCP tool returns a clickable URL with the token pre-attached so the operator gets a one-click authenticated visit. Browser-style auth mints a session cookie and 303s to a clean URL so the token never lingers in history.
- **Auto-detect on init (v0.10.0)** — `tokenops init --detect` reads your installed AI clients (Claude Code/Desktop, Cursor, ChatGPT Desktop, env-var API keys) and prints the exact plan-set commands. Run it once, paste what fits.
- **Interactive dashboard (v0.10.0)** — A Vue + D3 dashboard ships with the daemon at `/dashboard`. Hourly cost line, tokens-per-bucket stacked bar, KPI tiles, 15s auto-refresh. Driven by the same `/api/spend/*` endpoints the CLI uses.
- **Inline charts in MCP responses (v0.10.0)** — `tokenops_session_budget` leads with a coloured headroom gauge (green / amber / red by overage band); `tokenops_burn_rate` ships a sparkline. Rendered inline in markdown so every MCP client shows them today.
- **Dynamic-cheapest coaching router (v0.10.0)** — The coaching pipeline picks the lowest blended-rate model per provider from the pricing table at runtime. No hardcoded model names; pricing updates flow through automatically.

## Why TokenOps exists

Every major vendor — Anthropic, OpenAI, GitHub, Cursor, Google — publishes *rate-limit windows*, not monthly token caps, for their flat-rate plans. The vendor dashboard tells you you've hit the cap **after** the cap hits. There's no early signal in the agent context where you're actually working.

TokenOps closes that gap for every provider it tracks. One CLI, one MCP server, one event schema. Configure as many plans as you use:

```bash
tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-plus
tokenops plan set github copilot-business
tokenops plan set cursor cursor-pro
```

Every plan you bind contributes to a unified headroom view your agent can query mid-conversation.

## Honest about what it sees today

TokenOps reports its own signal quality on every prediction. Four sources, ranked by faithfulness:

- **`mcp_tool_pings` (low)** — Default. Counts MCP invocations as an activity proxy. Useful as a "is the agent talking to me?" signal, not a quota meter.
- **`claude_code_stats_cache` (medium)** — Daemon reads `~/.claude/stats-cache.json` on a tick (config: `vendor_usage.claude_code.enabled: true`). Per-model daily totals; can't resolve the 5h rolling window but gives real attribution. Schema is undocumented — every response carries a caveat.
- **`proxy_traffic` (high)** — Wire your SDK's base URL through the local proxy (`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GEMINI_BASE_URL`). Captures every request per-event.
- **`vendor_usage_api` (high)** — Anthropic Admin API poller (config: `vendor_usage.anthropic.{enabled, admin_key}`) reads `/v1/organizations/usage_report/messages` directly from Anthropic. Covers metered API usage only; Claude Max plan window state has no documented endpoint and stays heuristic.

## Who this is for

- Solo founders running coding agents 6+ hours a day across multiple AI subscriptions
- Staff engineers billing client time against AI sessions, juggling Claude + GPT + Copilot stacks
- Anyone who's lost focus to a mid-task rate-limit cutoff on any provider

If you don't recognise the struggling moment, this isn't the product for you yet.

## Looking for early users

If you'd trade a 15-minute call for hands-on help wiring TokenOps to your workflow, [open an issue on GitHub](https://github.com/felixgeelhaar/tokenops/issues/new) or DM `@felixgeelhaar`. Current focus is the first ten real users — across any provider mix.
