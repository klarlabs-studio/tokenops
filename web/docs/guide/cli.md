# CLI reference

The `tokenops` binary wraps the daemon's local event store. Every
subcommand has a matching MCP tool (`tokenops_<name>`).

## Setup

### `tokenops init`

Scaffolds the config (sqlite + rules on, idempotent). `--detect`
sniffs installed AI clients and prints the exact `tokenops plan set`
commands for what it found.

```bash
tokenops init --detect          # write config + suggest plan-set commands
tokenops init --print-only      # render YAML to stdout, don't write
tokenops init --force           # overwrite existing config
```

### `tokenops plan {list|set|unset|catalog|headroom}`

```bash
tokenops plan list              # configured bindings + headroom
tokenops plan catalog           # all 13 plans in the catalog
tokenops plan set anthropic claude-max-20x
tokenops plan unset cursor
tokenops plan headroom          # live consumption + overage risk
```

### `tokenops provider {list|set|unset}`

```bash
tokenops provider set anthropic https://api.anthropic.com
tokenops provider list
```

## Running the daemon

### `tokenops start`

Starts the daemon in the foreground. Listens on `127.0.0.1:7878` by
default, also advertises `tokenops.local` over mDNS. Stop with
SIGINT / SIGTERM (Ctrl-C).

### `tokenops serve`

MCP server over stdio. Wire into Claude Desktop / Code / Cursor /
aider via your client's `mcpServers` config.

### `tokenops status`

Daemon health + `blockers[]` / `next_actions[]`. Falls back to a
self-report when the daemon isn't running so MCP-only deployments
don't hit a dead end.

## Spend + plan headroom

### `tokenops spend`

```bash
tokenops spend                                   # last 7 days, top 5 by model
tokenops spend --by provider --top 3 --since 24h
tokenops spend --forecast --forecast-days 14
tokenops spend --include-demo                    # include seeded events
tokenops spend --json
```

### `tokenops scorecard`

Operator wedge KPI scorecard (FVT, TEU, SAC). Returns `warming_up`
with a 3-step activation checklist when no real data backs the KPIs.

## Vendor-side usage

### `tokenops vendor-usage status`

Reads config + counts source-tagged envelopes per source over a
configurable window. Surfaces a hint per source pointing at the
missing config knob when a source is dark.

```bash
tokenops vendor-usage status                 # 24h window
tokenops vendor-usage status --window 7d --json
```

### `tokenops vendor-usage backfill`

One-shot pull of historical Anthropic Admin API usage into the
local store. Deterministic envelope IDs — re-running or running
alongside the live poller never double-counts.

```bash
tokenops vendor-usage backfill --hours 168   # full week (Admin API cap)
tokenops vendor-usage backfill --hours 24 --dry-run
```

### `tokenops vendor-usage enable <source>`

Writes a vendor-usage source's config block to the active config
file so operators don't hand-edit YAML. Six sources covered:
`anthropic-cookie`, `cursor`, `github-copilot`, `codex-jsonl`,
`claude-code-jsonl`, `anthropic-admin`. Secrets accept env-var
fallback so they don't leak through shell history.

```bash
# Auto-discovers OAuth token from ~/.config/github-copilot
tokenops vendor-usage enable github-copilot

# Reader, no secret
tokenops vendor-usage enable claude-code-jsonl
tokenops vendor-usage enable codex-jsonl

# Secret via env to keep it out of shell history
TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY=sk-… \
  tokenops vendor-usage enable anthropic-cookie

# Flip a source off without clearing the persisted secret
tokenops vendor-usage enable anthropic-cookie --disable
```

Available env vars:
`TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY`,
`TOKENOPS_CURSOR_COOKIE`,
`TOKENOPS_COPILOT_OAUTH_TOKEN`,
`TOKENOPS_ANTHROPIC_ADMIN_KEY`.

## Coach

### `tokenops coach prompts`

Heuristic prompt-quality feedback. Walks
`~/.claude/projects/**/*.jsonl` AND `~/.codex/sessions/**/*.jsonl`,
extracts human-typed turns, reports length distribution
(under-5-word, 5-15, 15-50, 50-200, >200), vague-short count
(<15 chars / ≤3 words), pure acknowledgements (yes/no/ok/continue),
short questions, repeated prompts (verbatim 3+ times), and
concrete recommendations tuned to your pattern. **Prompt text is
read at scan time and is never persisted to the event store.**

```bash
tokenops coach prompts --since 7d            # both Claude Code + Codex
tokenops coach prompts --since 30d --json    # JSON for agent hosts
tokenops coach prompts --session <id>        # restrict to one session
```

## Dashboard

### `tokenops dashboard rotate-token`

Mints a fresh 32-byte URL-safe secret and atomic-writes it to
`~/.tokenops/dashboard.token`. Restart the daemon for the new value
to take effect; old URLs return 401 after restart.

```bash
tokenops dashboard rotate-token              # rotate + print path
tokenops dashboard rotate-token --json       # emit token + path as JSON
```

## Demo + replay

### `tokenops demo`

Seeds synthetic events (default 7 days) tagged `source=demo`. Every
default rollup filters them out — pass `--include-demo` (CLI) or
`include_demo: true` (MCP) to opt in. `--reset-only` purges without
reseeding.

### `tokenops replay [SESSION_ID]`

Replays past prompts through the optimizer pipeline.

```bash
tokenops replay sess-abc123 --json
tokenops replay --workflow research-summariser --since 24h
tokenops replay --agent planner --since 7d --limit 200
```

Add `--workflow ID` to also run the waste detector against the
reconstructed workflow trace.

## Rules + governance

### `tokenops rules {analyze|conflicts|compress|inject|bench}`

Rule Intelligence subsystem — analyses ClaudeMD / agent-instruction
files, detects conflicts, compresses, injects, benchmarks.

### `tokenops eval`

Optimizer eval harness — gates new model/prompt configurations
against a baseline.

### `tokenops coverage-debt`

Risk-weighted coverage debt report.

## Inspection

### `tokenops config show`

Dumps the resolved configuration as YAML (or JSON with `--json`).

### `tokenops audit`

Queries the audit log (`~/.tokenops/events.db`).

### `tokenops events`

Per-kind domain-event counts (workflow.started, optimization.applied,
rule_corpus.reloaded, etc.).

### `tokenops version`

Prints the binary version + commit + build date.
