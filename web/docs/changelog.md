# Release highlights

The curated arc of what changed and why. For every commit, see the
[full CHANGELOG](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md);
for binaries, the [releases page](https://github.com/klarlabs-studio/tokenops/releases).

Current release: **v0.57.0**.

## v0.57.0 — the events that never arrived

Last release found four readers that measured nothing. This one follows the
same method past the reader and into the store, and finds the events being
counted correctly and then thrown away.

Enabling every reader on one real machine and counting the source stores
independently: opencode held 48,540 assistant messages with token usage and
the store had 33,316. Codex had 3,597 turns and the store had 845. Three
separate paths were discarding events while reporting success.

The event bus dropped envelopes whenever its queue filled. That is the right
trade for the proxy, which must not block on storage, and the wrong one for a
backfill: a poller advances its watermark before publishing, so a dropped
envelope is never revisited. The loss was permanent, surfaced only as one line
at shutdown, and reproduced identically on every restart. It was not even a
clean truncation — one month lost 63% of its rows, the next none — so every
period was understated by a different amount, which is harder to notice than
losing everything.

Underneath it, a write-ahead log that had grown to 372MB beside a 396MB
database, because SQLite can only checkpoint up to the oldest snapshot a
reader still holds and this store is read continuously. Inserts stopped
fitting in their deadline, and a rejected batch was discarded whole rather
than retried.

And retention, which could only be configured per event type — while every
vendor-usage reader writes the same type. One window governed Claude Code,
Codex, opencode and Cursor together with the live stream, so importing
history meant watching it be deleted four minutes later. `keep_by_source`
now sets a window per reader: keep what you imported, still trim what
streams in.

## v0.56.0 — every client, and four silent zeros

The arc of this release is one defect wearing six different outfits: a path
that reports success while measuring nothing. Four were found by checking each
reader against the real store on disk instead of against its own fixtures, and
comparing to a count taken independently.

- **`dx --source codex` reported "0 instructions across 37 sessions"** on 343
  real instructions. The reader keyed on an event current Codex stopped
  emitting, and skipped the record that replaced it to avoid double-counting.
  Zero-of-many is the worst version of this: an idle machine looks identical.
- **Codex spend had no model at all.** `Turn.Model` was declared and never
  assigned, so every Codex turn entered the store unpriceable and the whole
  client reported `$0`. 3597 turns, none of them costed.
- **The coach priced from the wrong card.** It used the catalog embedded in the
  binary, not the effective-dated one the daemon builds from your snapshots —
  so a machine whose snapshot knew `gpt-5.5` still had its budget measured
  against a card that did not. On one real rollout that was the difference
  between `$0.00` and `$0.68`, and the nudge that never fired.
- **`dx` was grading a benchmark harness.** Sessions run in throwaway
  directories were 94% of a 7-day window here, and set every grade: a median of
  2 turns per instruction against the operator's real 17, and straight `A`s.
- **Prompt text reached only one reader**, so `story` titled 1258 of 1258
  opencode tasks "(no instruction text)" — and every instruction opened its own
  task, because the continuation lexicon cannot judge words it does not have.

### Every client, and an honest matrix

Claude Code, Codex, Cursor and opencode now all read, all carry a coaching
nudge, and the two whose hooks can decline a read now refuse one. Codex's
payload matches Claude Code's field for field; Cursor carries its tokens
inline; opencode needed a generated plugin and delivers through a TUI toast.

Three cache conventions had to be told apart to price them — Claude Code keeps
input and cache disjoint, Codex nests cache inside input, Cursor nests both —
and getting that wrong overstates a Cursor turn by five orders of magnitude on
the uncached line.

Where a client *cannot* support something, the matrix says so with the reason
rather than calling it pending. Codex has no file-read tool; Cursor's
`beforeReadFile` cannot decline. `hooks install` refuses both.

### Also

- **Smart routing** decides per turn from task class, plan-window pressure and
  the live rate card, with no rules table — and `tokenops_routing_advise`
  reaches every MCP client, including the ones with no proxy.
- **The rate card refreshes itself** daily and applies to the running daemon,
  rather than writing a snapshot nobody reads until a restart.
- **GPT-6 Astra** priced and pinned, with the two published rules this schema
  cannot express written down rather than left to be discovered from a number
  that looks low.
- **`story` gained two more audiences** — evidence for someone you bill, state
  of the world for a teammate — plus an MCP tool so the agent reads its own
  history back.
- **`coaching.quiet`** rate-limits the proactive channel, and the coach now
  argues for `intervene` from your own ledger instead of waiting to be asked.

## v0.45.0 – v0.54.3 — agent experience, and honest units

The arc since v0.44: the product stopped accounting only in dollars — which
are always `$0.00` on a flat-rate subscription — and gained a second
dimension, what sessions are like to work with.

- **`tokenops dx`** (v0.46–v0.49). Agent developer-experience metrics derived
  from transcripts every client already writes: turns per instruction,
  first-try rate, rework, interrupts, escalation to subagents. Graded A–F on
  the worst dimension rather than the average. Reads Claude Code, Codex,
  opencode, and Cursor, and reports per upstream provider.
- **Quality vs context** (v0.53.0). Answers "does quality degrade as context
  grows" from the operator's own transcripts — rejection rate and repeated
  tool calls per context band — and says plainly when the corpus is too noisy
  to call it.
- **Coaching completes across clients** (v0.50–v0.51). Prompt coach and reply
  coach both read opencode, closing coverage over every client that keeps a
  local record.
- **Tokens and the rate-limit window became first-class** (v0.45.0). Two KPIs
  stopped grading data that was never measured, and `init` became the single
  command that wires the machine.
- **Counterfactual cost is labelled as such** (v0.51.1). On a subscription,
  "cost" is what the session would have cost at list price. Every tier now
  says so, and "your budget" is reserved for one the operator actually set.
- **The MCP server stopped counting itself** (v0.54.0). `mcp-session` pings
  carry no tokens and no cost but were landing in request totals.
  `--include-source` re-admits excluded sources by name.
- **Security and release-path fixes** (v0.54.1–v0.54.3). A reachable
  `golang.org/x/text` infinite loop (GO-2026-5970) closed, and three attempts
  to get that fix in front of Homebrew users — the first two failing in the
  release workflow's credentials, not the product.

## v0.44.0

- **Supervise ingestion.** `tokenops daemon install` writes a launchd LaunchAgent (macOS) or systemd user unit (Linux) that keeps `tokenops start` alive across reboot. `tokenops serve` is the MCP server and does not ingest.
- **Dashboard token file is `0600`.** `~/.tokenops/daemon.url` carries the auth token; it is no longer world-readable.
- **Gemini 2.5 rates pinned** against Google's pricing page (cache-read is 10% of input: Pro `$0.125`, Flash `$0.03`, Flash-Lite `$0.01`).

See the [changelog](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md) for the full 0.22–0.44 arc (pricing research, fmt, coaching hooks, Homebrew cask, the 27-day ingestion outage).

## v0.43.0

- **Dead ingestion is visible.** `tokenops status` names how long a source has been silent and tells you to run `tokenops start` (not `serve`). Spend tools add `measurement.trusted: false` when the pipeline is dead. `tokenops_status` reports when no ingestion daemon is reachable.

## v0.21.1

- **8-KPI agent scorecard.** The wedge trio (FVT / TEU / SAC) gained five agent-workflow KPIs in v0.19–v0.21, all graded A–F against tuneable thresholds:
  - **CHR** — Cache Hit Ratio (≥90/70/50)
  - **CGR** — Confirmation Gate Rate (≤10/20/30)
  - **RGR** — Regenerate Rate (≤5/10/20)
  - **TCS** — Tool Success Rate (≥95/85/70)
  - **DAR** — Destructive Action Rate (≤0.5/2/5)
- **Honest grading (v0.21.1).** TEU stays N/A (default 15%) when the optimiser never ran — no F for being on JSONL-only mode. SAC syncs payload attribution from indexed columns so column-side backfills propagate. CGR strips autonomous-loop sentinels (`continue` ×100+ from `/loop` mode isn't a human ack). Overall grade F → B on real 30d data.
- **`tokenops task start|done|list`** (v0.20–v0.21). Operator-marked task boundaries persist to `~/.tokenops/tasks.jsonl`. `task list --metrics` enriches every task with the events-store rollup: turns, input/output tokens, cache-aware cost, TTFUO, cost-per-turn.
- **`tokenops coach prompts`** ranked recommendations (v0.18) project tangible savings (tokens / dollars / hours) per win, grounded in the operator's own turn averages. JSON output ships `{ findings, turn_stats }` for MCP-host UIs.
- **Coach replies + Anthropic admin-API attribution (v0.17).** `tokenops coach replies` detects output-compression patterns (caveman-skill verdict, article density, filler density). Anthropic admin-API events now stamp synthetic `agent_id` / `workflow_id` so they don't drag SAC down.

## v0.16

- **Per-project rollups.** `claudecodejsonl` poller stamps `agent_id = "claude-code:<project>"` and `workflow_id = "claude-code:<project>:<session>"`. `group=agent` answers "which project burns the most".
- **Cache hit-rate tile + `/api/spend/cache_stats`.** The dashboard `Cache hit: XX.X%` tile; for agent workloads the ratio routinely sits >95%.
- **Waste-detector profiles** for `claude-code:` and `codex:` workflows (900k / 250k peak respectively) — stops short-workflow defaults firing on every session.
- **Codex parity** for the v0.14.x JSONL improvements: `SessionID`, `AgentID="codex"`, `WorkflowID="codex:<session>"`, and `CachedInputTokens` on every PromptEvent.
- **`tokenops coach prompts` auto-discovers Codex.** Scans both `~/.claude/projects` AND `~/.codex/sessions`.

## v0.14.x – v0.15.0

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

## v0.10.x – v0.11.0

- **`tokenops.local` via mDNS (v0.10.1)** — The daemon advertises itself over zeroconf on Start, so the dashboard URL becomes `http://tokenops.local:7878/dashboard` instead of a bare loopback address. The `tokenops_dashboard` MCP tool prefers it; falls back to `127.0.0.1` when `.local` resolution isn't available.
- **Vendor /usage ingestion (v0.10.2)** — Two new signal sources upgrade Anthropic confidence beyond the heuristic default. The **Claude Code stats cache reader** parses `~/.claude/stats-cache.json` and emits per-(date, model) deltas (signal_quality → medium). The **Anthropic Admin API poller** calls `/v1/organizations/usage_report/messages` every 5min with an admin key (signal_quality → high). Both wired through `config.vendor_usage.*`; both honest about the Claude Max 5h-window blind spot.
- **Dashboard auth (v0.10.3)** — `/dashboard` + `/api/*` now require a shared-secret token (`/healthz`, `/readyz`, `/version` stay public). Daemon mints + persists the token automatically at `~/.tokenops/dashboard.token`; the MCP tool returns a clickable URL with the token pre-attached so the operator gets a one-click authenticated visit. Browser-style auth mints a session cookie and 303s to a clean URL so the token never lingers in history.
- **Auto-detect on init (v0.10.0)** — `tokenops init --detect` reads your installed AI clients (Claude Code/Desktop, Cursor, ChatGPT Desktop, env-var API keys) and prints the exact plan-set commands. Run it once, paste what fits.
- **Interactive dashboard (v0.10.0)** — A Vue + D3 dashboard ships with the daemon at `/dashboard`. Hourly cost line, tokens-per-bucket stacked bar, KPI tiles, 15s auto-refresh. Driven by the same `/api/spend/*` endpoints the CLI uses.
- **Inline charts in MCP responses (v0.10.0)** — `tokenops_session_budget` leads with a coloured headroom gauge (green / amber / red by overage band); `tokenops_burn_rate` ships a sparkline. Rendered inline in markdown so every MCP client shows them today.
- **Dynamic-cheapest coaching router (v0.10.0)** — The coaching pipeline picks the lowest blended-rate model per provider from the pricing table at runtime. No hardcoded model names; pricing updates flow through automatically.
