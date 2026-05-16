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

<div class="hero-cycle" role="img" aria-label="Local TokenOps dashboard cycling through 1h, 6h, 24h, 7d filters. 5 KPI tiles incl. CACHE HIT, daily cost-over-time, hourly tokens-per-bucket. Captured live against v0.17.">
  <img :src="withBase('/media/frames/1h.jpg')" class="f1" alt="1-hour window" />
  <img :src="withBase('/media/frames/6h.jpg')" class="f2" alt="6-hour window" />
  <img :src="withBase('/media/frames/24h.jpg')" class="f3" alt="24-hour window" />
  <img :src="withBase('/media/frames/7d.jpg')" class="f4" alt="7-day window" />
</div>

<style>
.hero-cycle {
  position: relative;
  width: 100%;
  max-width: 1200px;
  aspect-ratio: 741 / 973;
  margin: 24px auto;
  border-radius: 12px;
  overflow: hidden;
  border: 1px solid var(--vp-c-divider);
  box-shadow: 0 8px 24px rgba(0,0,0,0.08);
}
.hero-cycle img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0;
  animation: hero-cycle 12s linear infinite;
  image-rendering: -webkit-optimize-contrast;
}
.hero-cycle .f1 { animation-delay: 0s; }
.hero-cycle .f2 { animation-delay: 3s; }
.hero-cycle .f3 { animation-delay: 6s; }
.hero-cycle .f4 { animation-delay: 9s; }
@keyframes hero-cycle {
  0%   { opacity: 0; }
  3%   { opacity: 1; }
  22%  { opacity: 1; }
  25%  { opacity: 0; }
  100% { opacity: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .hero-cycle img { animation: none; opacity: 0; }
  .hero-cycle .f4 { opacity: 1; }
}
</style>

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

## What's new in v0.16.0

- **Per-project rollups for Claude Code.** Poller stamps `agent_id = "claude-code:<project>"` and `workflow_id = "claude-code:<project>:<session>"` on every event, derived from the JSONL parent directory. `group=agent` series queries answer "which project burns the most".
- **Cache hit-rate tile + `/api/spend/cache_stats` endpoint.** The dashboard now shows `Cache hit: XX.X%` alongside the four KPI tiles. For agent workflows the ratio routinely sits >95% — the single number that separates the naive billing-math estimate from real spend (~10x correction).
- **Waste-detector profiles.** `claude-code:` workflows now flag at 900k peak / 2M growth; `codex:` workflows at 250k / 500k. Stops the short-workflow defaults (32k/16k) from firing on every code-agent session.
- **Codex parity for v0.14.x JSONL improvements.** `codexjsonl` poller now sets `SessionID`, `AgentID="codex"`, `WorkflowID="codex:<session>"`, and `CachedInputTokens` (was dropped — same ~10x over-estimate Claude Code had before v0.14.2).
- **`tokenops coach prompts` auto-discovers Codex.** A single invocation now scans both `~/.claude/projects` AND `~/.codex/sessions`. Each dialect parsed per source; filename-derived timestamp fallback for Codex.

## Earlier highlights (v0.14.x – v0.15.0)

- **`tokenops coach prompts` (v0.15.0).** Heuristic prompt-quality feedback for Claude Code users. Walks `~/.claude/projects/**/*.jsonl`, extracts human-typed turns, reports length distribution, vague/ack/repeat counts, and concrete recommendations. Prompt text is read at scan time and is **never persisted** to the event store. MCP tool `tokenops_coach_prompts` exposes the same surface to agent hosts.
- **Coach wiring for Claude Code (v0.14.3).** JSONL events now carry `session_id` + `workflow_id` on the indexed columns (not just attributes), so `tokenops replay` + the waste detector resolve Claude Code sessions. Coach surface was dark for JSONL data; now it surfaces oversized-context + runaway-growth findings per session.
- **Cache-aware pricing (v0.14.2).** Dashboard cost over-estimated by ~9x because cache reads billed at the new-input rate ($15/M for claude-opus-4-7) instead of the cache rate ($1.50/M). Poller now writes the cache split; aggregator reads it back via `json_extract`. On real 7-day data: **$94k → $10k**.
- **`Summarize` cost recompute (v0.14.1).** Dashboard TOTAL COST showed `$0.00` for any data from vendor-usage-jsonl sources (those readers ship token counts but no prices). Fixed by recomputing via `spend.Engine` in the same path `AggregateBy` already used.
- **`tokenops vendor-usage enable <source>` (v0.14.0).** Writes a vendor-usage source's config block so operators don't hand-edit YAML to flip the v0.13.0 pollers on. Six sources: `anthropic-cookie`, `cursor`, `github-copilot`, `codex-jsonl`, `claude-code-jsonl`, `anthropic-admin`. Secrets accept env-var fallback (`TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY`, etc.).
- **Four new vendor-usage sources (v0.13.0).**
  - **Codex CLI JSONL reader** — parses `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`, surfaces OpenAI's authoritative `rate_limits` block (5h primary + weekly secondary used_percent + resets_at).
  - **GitHub Copilot quota poller** — calls `api.github.com/copilot_internal/user` with the OAuth token Copilot IDE plugins already manage. Auto-discovers from `~/.config/github-copilot`.
  - **Cursor `/api/usage` poller** — cookie-based scrape of cursor.com.
  - **Anthropic cookie scraper** — polls `claude.ai/api/organizations/{org_id}/usage` with the operator's browser `sessionKey`. **The only source that surfaces the official Claude Max weekly utilization %.**
- **Claude Code JSONL reader (v0.12.0).** Parses `~/.claude/projects/<project>/<session>.jsonl` — Claude Code's live per-turn conversation record — and emits one PromptEvent per assistant turn with the full `message.usage` block. The v0.10.2 stats-cache reader was lagging by days; deprecated in favour of this.

## Earlier highlights (v0.10.x – v0.11.0)

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
