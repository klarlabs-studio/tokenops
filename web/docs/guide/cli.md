# CLI reference

The `tokenops` binary wraps the daemon's local event store. Every
subcommand has a matching MCP tool (`tokenops_<name>`).

## Setup

### `tokenops init`

Scaffolds the config (sqlite + rules on, idempotent) **and wires this
machine**: it registers the MCP server with every installed host and
installs the Claude Code hooks. `--detect` adds a report of the clients it
found; it does not make the command read-only. Use `tokenops detect` to
only look, or `--print-only` / `--no-wire` for a dry run.

The rules scan root is the repository `init` was run inside — it walks up
for a `.git` — not the directory your shell happened to be in. Running it
from your home directory is refused rather than binding the scanner to
every project you own.

```bash
tokenops init --detect          # write config + suggest plan-set commands
tokenops init --print-only      # render YAML to stdout, don't write
tokenops init --force           # overwrite existing config
```

### `tokenops detect`

Reports which AI clients are installed and changes nothing. Filesystem and
environment only: no network calls, no credential reads, nothing written.

```bash
tokenops detect
```

A bare API key is reported as metered usage with no plan to bind — binding
a subscription against metered traffic would make every headroom figure
wrong in a way that looks authoritative.

### `tokenops plan {list|set|unset|catalog|headroom}`

```bash
tokenops plan list              # configured bindings + headroom
tokenops plan catalog           # all 13 plans in the catalog
tokenops plan set anthropic claude-max-20x
tokenops plan unset cursor
tokenops plan headroom          # live consumption + overage risk
```

#### Plans billed at API rates

`claude-enterprise` has no rate-limit window: usage-based Enterprise is
billed at API rates from the first token, so there is no cap to be under.
Headroom is measured against your monthly spend limit.

The best source is Anthropic itself. With the
[Claude subscription telemetry](#tokenops-vendor-usage-setup-claude-subscription) set up,
claude.ai reports what you have spent this month and the limit you are
spending against, and headroom uses those figures — no admin key needed,
and nothing to type in:

```bash
tokenops vendor-usage setup claude-subscription
tokenops plan set anthropic claude-enterprise
```

Without subscription telemetry, give the limit yourself; the binding is refused with
neither, rather than measured against a number nobody chose:

```bash
tokenops plan set anthropic claude-enterprise \
  --spend-limit 5000 --limit-window monthly
```

`plan headroom` says which you are looking at: spend *reported by the
vendor*, or *estimated from token counts*.

From an agent, the same two steps are `tokenops_vendor_usage_setup` and
`tokenops_plan_set`. The setup tool never takes the session key as an
argument — it is a claude.ai login, and a tool argument lands in the
agent's transcript. It reads the key from
`TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY` or existing config; the terminal
`setup` reads it without echoing it.

```yaml
plan_limits:
  anthropic:
    spend_limit_usd: 5000
    window: monthly       # or weekly / daily, matching the console
    rate_factor: 0.8      # optional: 20% off list, for a negotiated contract
```

`rate_factor` matters on a discounted contract. TokenOps costs from the
public rate card, so without it your console limit is compared against an
overstatement and nothing says so.

Claude Team is two plans, five times apart: `claude-team-standard` (1.25x
Pro) and `claude-team-premium` (6.25x Pro). Seat-based Enterprise is an
allowance plus metered overflow — two denominators at once — and is not
modelled; naming it says so.

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

### `tokenops daemon {install|uninstall|status}`

Supervises `tokenops start` so ingestion survives reboot. macOS writes
a LaunchAgent; Linux writes a systemd user unit. Placeholders in
`deploy/launchd` / `deploy/systemd` are filled with this binary's path
and `$HOME`.

```bash
tokenops daemon install            # write + load
tokenops daemon install --no-load  # write only
tokenops daemon status
tokenops daemon uninstall
```

`tokenops serve` is the MCP server and does not ingest. A reboot that
kills an unsupervised `start` while the client respawns `serve` is how
a 27-day outage stayed invisible.

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
tokenops spend --include-source=mcp-session     # include MCP activity pings
tokenops spend --json
```

### `tokenops scorecard`

Operator wedge KPI scorecard. Three classic KPIs (FVT, TEU, SAC)
plus five agent-workflow KPIs added in v0.19–v0.21:

  - **CHR** — Cache Hit Ratio (≥90/70/50)
  - **CGR** — Confirmation Gate Rate (≤10/20/30)
  - **RGR** — Regenerate Rate (≤5/10/20)
  - **TCS** — Tool Success Rate (≥95/85/70)
  - **DAR** — Destructive Action Rate (≤0.5/2/5)

All grade A–F against tuneable thresholds. CHR is computed from
events.db; CGR + RGR from JSONL prompt extracts; TCS + DAR from
JSONL `tool_use` + `tool_result` blocks. Returns `warming_up`
with a 3-step activation checklist when no real data backs the KPIs.

```bash
tokenops scorecard --since-days 30        # text table
tokenops scorecard --since-days 30 --json # structured output
```

### `tokenops task {start|done|list}`

Operator-marked task boundaries persisted to `~/.tokenops/tasks.jsonl`.
`task list --metrics` enriches every task with the events-store
rollup over its `[StartedAt, CompletedAt]` window: turns,
input/output tokens, cache-aware cost, TTFUO, cost-per-turn.

```bash
tokenops task start "fix auth middleware"
# ...do the work...
tokenops task done
tokenops task list --since 7d --metrics
tokenops task list --since 30d --json   # MCP-host friendly
```

## Vendor-side usage

### `tokenops vendor-usage setup claude-subscription`

Connects claude.ai's subscription telemetry — what the app itself shows: the
5-hour and 7-day utilisation on Pro and Max, or on Claude Enterprise, what
you have spent this month against your spend limit. It is the only
authoritative reading TokenOps can get for a Claude subscription;
everything else is estimated.

The canonical setup name is `claude-subscription`. It is intentionally not
`claude-code`: `claude-code-jsonl` is the separate per-turn coding-activity
source, while subscription telemetry can cover Claude web, Claude Code,
Pro, Max, Team/Business, and Enterprise. The former
`claude-usage-meter` command spelling remains an accepted compatibility alias.

Only what Anthropic reports is recorded. A window your plan does not have
is left out, never shown as 0%, and a reply in a shape this version cannot
read is logged and skipped rather than stored as zeros.

```bash
tokenops vendor-usage setup claude-subscription
```

By default it reads the session from the browser you are already signed in with —
Chrome, Arc, Brave, Edge, Chromium or Firefox — so there is nothing to copy
or paste. On macOS the system asks you to allow reading the browser's
keychain entry; that prompt is the permission step. TokenOps reads only the
Claude session and Cloudflare clearance cookies, from a copy of the store, and never writes to your browser
profile.

```bash
tokenops vendor-usage setup claude-subscription --browser Chrome  # pick one
tokenops vendor-usage setup claude-subscription --paste           # type it instead
tokenops vendor-usage setup claude-subscription --paste-request   # import a usage request
```

Some operating systems protect browser cookie databases even after the
keychain is unlocked. `--paste-request` is the portable fallback: open
`claude.ai/settings/usage`, select the content-free
`/api/organizations/.../usage` GET in Developer Tools → Network, choose
**Copy as cURL**, and paste it at the hidden prompt. TokenOps rejects other
endpoints and extracts only `sessionKey`, `cf_clearance`, `User-Agent`, and
the organization ID; it does not store the copied command or any response.
The clearance is short-lived, so repeat setup if source health later reports
that Claude's bot check refused it.

With no browser session found it falls back to explaining where the cookie
is and reading it without echoing it into your scrollback. Either way it
**verifies the key against Anthropic**, prints your real percentages, and
only then writes config. A mistyped or expired key fails here rather than
sitting in config producing nothing — session cookies rotate, so a stale
copy is the usual cause.

From an agent, `tokenops_vendor_usage_setup` does the same: it reads the
browser session (you allow the keychain prompt) and never asks for the key
in the conversation.

Prefer this over `vendor-usage enable claude-subscription --session-key`,
which writes whatever you give it without checking.

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
file so operators don't hand-edit YAML. Seven sources covered:
`claude-subscription`, `cursor`, `github-copilot`, `codex-jsonl`,
`claude-code-jsonl`, `opencode`, `anthropic-admin`. Secrets accept env-var
fallback so they don't leak through shell history.

```bash
# Auto-discovers OAuth token from ~/.config/github-copilot
tokenops vendor-usage enable github-copilot

# Reader, no secret
tokenops vendor-usage enable claude-code-jsonl
tokenops vendor-usage enable codex-jsonl

# Secret via env to keep it out of shell history
TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY=sk-… \
  tokenops vendor-usage enable claude-subscription

# Flip a source off without clearing the persisted secret
tokenops vendor-usage enable claude-subscription --disable
```

Available env vars:
`TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY`,
`TOKENOPS_CURSOR_COOKIE`,
`TOKENOPS_COPILOT_OAUTH_TOKEN`,
`TOKENOPS_ANTHROPIC_ADMIN_KEY`.

## Coach

### `tokenops coach delivery`

Shows or sets how far coaching goes: `observe` (answers when asked),
`advise` (also nudges, never blocks — the default), or `intervene` (also
lets `read-guard` refuse redundant re-reads).

```bash
tokenops coach delivery              # print the current level
tokenops coach delivery intervene    # let the guard start blocking
```

One key for every coaching channel, applied at call time — no need to
re-run `tokenops hooks install`. See
[Configuration](/guide/configuration) for how it differs from `mode`.

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

Output leads with a **BIGGEST WIN** panel — the highest-impact
recommendation with evidence quotes pulled from your data, plus
projected savings (turns / tokens / dollars / hours) computed
from your per-turn averages.

### `tokenops coach replies`

Output-side sibling of `coach prompts`. Walks the same JSONLs but
extracts assistant replies and scores per session: article density,
filler density, average word length, code-block ratio, and a
"caveman-likely" verdict + rough estimated token savings. Useful to
detect when an output-compression skill is engaged and how many
tokens it suppressed.

```bash
tokenops coach replies --since 7d
tokenops coach replies --json
```

## Story

### `tokenops story`

Reconstructs your work as an account of it, one task at a time: the
instruction you typed, everything the agent did before the next one, what
it cost, and the specific moments it went wrong.

```bash
tokenops story                      # last 7d, 10 most recent tasks
tokenops story --days 1 --limit 3
tokenops story --json               # same account, for an agent to read back
tokenops story --idle-gap 30m       # coarser task boundaries
tokenops story --for report         # for someone you bill or report to
tokenops story --for handoff        # for a teammate picking the work up
```

```
Your work — last 2d
  3 tasks

─ fix the parser panic on empty input
  Tue 09:24 → 09:28 (3m)  · split by idle gap
  4 instructions · 73 turns · 38 tool calls · context peaked at 681k
  touched internal/parse/lexer.go, internal/parse/parse_test.go
  where it went sideways:
    · you told it the 1st answer was wrong
    · the 2nd instruction edited the same file twice
```

**Sessions run in throwaway directories are excluded.** A benchmark
harness, a temporary clone, anything under `/tmp` or the system temp
root: `story`, `dx` and `coach prompts` all claim to describe how *you*
work, and a simulated session has no operator. Pass `--include-scratch`
to read them anyway.

This is not a rounding error. On one real machine a simulation harness
was **94% of a 7-day window**, and `dx` graded it: a median of 2.0 turns
per instruction against the operator's actual 17.0, zero tool calls
against 8.0, and straight A's over a corpus that contained almost none
of their work.

Spend is deliberately *not* filtered this way. A simulated session still
spent real tokens against a real plan, so the ledger and the headroom
math are right to count it. What it never did was say anything about how
its operator works.

**A task is inferred, not declared.** A new session always starts one, and
a pause longer than `--idle-gap` (default 10m) splits one. Every task says
which of those split it, because a boundary you can see is one you can
argue with. The gap is measured from when the agent *stopped working*, not
from when you typed — a unit that ran for an hour did not leave you idle
for an hour. `tokenops task start|done` still marks a task exactly when you
want to be precise.

Units are grouped per session before any boundary is inferred. Concurrent
sessions are ordinary — two terminals, two projects — and their
transcripts interleave in wall-clock time, so a global time order would
see the session change at nearly every instruction.

**`context peaked at` is not a cost.** It is the largest context a single
turn carried — how heavy the work got, counting the context once. The
summed figure (`context_carried_tokens` in JSON) double-counts by
construction, because every turn re-sends the accumulated context; that is
the number that reads as $94k when cache-aware pricing says $10k. It is
there for comparing tasks against each other, never as spend.

#### `--for` — one account, four readings

The structure is one task. The renderings differ because the audiences
do: four people want the same work described and none of them wants the
same document.

| `--for` | Reader | Optimised for |
|---|---|---|
| `me` *(default)* | you | candour — what happened, including where it went sideways |
| `agent` (`--json`) | a machine reading its own history back | fields, enumerated |
| `report` | someone you bill or report to | **defensibility** — evidence readable by someone who was not there |
| `handoff` | a teammate picking the work up | **state of the world** — what to open first |

`--for report` is a record, not a confession. It carries what the
transcript actually holds — when the work happened, for how long, at what
volume, against which files — grouped by day, and it omits the friction
narrative on purpose: "you told it the third answer was wrong" is candour
aimed at you, and in front of a client it turns an account of work into an
apology for it. The footer says what the figures are *not*: wall-clock
from local transcripts is not a timesheet and not a judgement about what
is billable.

`--for handoff` is ordered by what needs attention rather than by time —
work that ended on friction first, then what ran clean, then the files the
work kept coming back to. It never says a task "landed": a transcript
records what was attempted, and whether the code works is a question only
the tests answer.

Both replace your home directory with `~` and elide long paths from the
left. A document meant for someone else should not carry your username.

Titles are your own instructions in both, quoted rather than
paraphrased — a summariser can be wrong and a quote cannot. An unknown
`--for` is an error rather than a silent fall back to the default, because
that fallback is how someone emails a client the candid rendering.

Prompt text is read at scan time and **never persisted**. The account is
rebuilt from transcripts on every run.

### `tokenops daemon restart`

Restarts the supervised unit so it re-reads config. The daemon loads its
configuration once, at boot, so a config write that is not followed by a
restart has not taken effect yet.

```bash
tokenops daemon restart
```

The commands that write config — `plan set/unset`, `provider set/unset`,
`vendor-usage enable` — do this for you. Pass
`--no-restart` when writing several keys in a row, then restart once.

Unsupervised (no unit installed), there is nothing to bounce: those
commands tell you to Ctrl-C and re-run `tokenops start` instead of
claiming a restart that did not happen.

## Hooks

TokenOps wires three hooks into a client, each independent:

| Hook | Fires | What it does |
|---|---|---|
| `--coach` | end of turn | the coaching nudge, against a per-session budget |
| `--read-guard` | before a file read | refuses a redundant re-read of an unchanged file |
| `--route-guard` | when you submit a prompt | states the case for a cheaper model when the one in use is over-spec |

`--client` selects which client to act on, and **install, status and
uninstall all take it**:

| `--client` | Where the hooks live |
|---|---|
| `claude-code` *(default)* | `~/.claude/settings.json` |
| `codex` | `~/.codex/hooks.json` |
| `cursor` | `~/.cursor/hooks.json` |
| `opencode` | a generated plugin in `~/.config/opencode/plugins` |

```bash
tokenops hooks install --route-guard        # wire the model-fit guard
tokenops hooks status                       # what's wired for Claude Code
tokenops hooks status --client cursor       # ... and for Cursor
tokenops hooks uninstall --route-guard      # remove one hook
tokenops hooks uninstall                    # remove every tokenops hook
```

`uninstall` with no hook named removes all three. Naming one removes only
that one, on every client — including opencode, where the generated plugin
is rewritten with the remaining halves rather than deleted, and deleted
only once nothing is left in it.

`status` reports the hook, its event, and the binary each entry calls, and
flags an entry pointing at a different binary than the one you ran — which
is what a stale `hooks install` from an older install looks like.

### `tokenops hooks install --client codex`

Codex keeps hooks in `~/.codex/hooks.json`, in the same nested shape
Claude Code uses inside `settings.json`, and sends the same `Stop` payload
under the same field names. So the coaching nudge is one handler for both
clients:

```bash
tokenops hooks install --coach --client codex
```

Two things differ and both are handled explicitly.

**The entry is a single command string**, not `command` plus an `args`
array. Codex documents it that way; assuming otherwise would run the bare
binary with no subcommand — a hook that installs, reports success, and
does nothing.

**Codex will not run it until you trust it.** A non-managed hook is
skipped, silently, until its exact definition has been reviewed via
`/hooks` in Codex. The installer says so rather than printing "Wrote …"
and stopping:

```
Not armed yet. Codex skips a hook until you trust it:
  run `/hooks` in Codex, review the tokenops entry, and trust it.
Until then the hook is written but silently not run.
```

`--read-guard --client codex` is **refused**, with the reason: Codex has
no file-read tool, so there is nothing to intervene in.

### `tokenops hooks install --client cursor`

Cursor keeps hooks in `~/.cursor/hooks.json` and fires `stop` when the
agent loop ends:

```bash
tokenops hooks install --coach --client cursor
```

Cursor is the cheapest of the three to support and the easiest to get
wrong.

**Cheapest,** because its `stop` payload carries the turn's token counts
inline — `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`. There is no transcript to find or parse.

**Easiest to get wrong,** because its cache accounting is a third
distinct convention:

| client | relationship |
|---|---|
| Claude Code | `input_tokens` and the cache figures are **disjoint** |
| Codex | `cached_input_tokens` sits **inside** `input_tokens` |
| Cursor | **both** cache figures sit inside `input_tokens` |

Cursor's own team put it plainly: *"input_tokens is inclusive of
cache_read_tokens and cache_write_tokens… only 14 of the 1.18M input
tokens were genuinely uncached."* Pricing that the Claude Code way bills
1.18M tokens at the full input rate instead of 14.

Two more things the implementation accounts for. Cursor's numbers are
**cumulative per turn** and it sends identical values on `stop` and
`afterAgentResponse` for the same `generation_id` — so turns are
deduplicated on that id rather than summed. And the token fields are
**optional**: absent means "not reported", never zero-cost.

Installing it is also what gives Cursor **per-turn spend**. Cursor keeps
no per-turn record on disk and its usage endpoint reports only a
percentage of plan consumed — the hook payload is the only place those
token counts ever exist. `coach-hook` records each one to
`~/.tokenops/cursor-turns/`, and the daemon ingests it, so Cursor joins
every other client in `spend`, burn rate, forecast and top consumers.
Without the hook, Cursor stays quota-only.

The schema is **flat**, unlike Claude Code's and Codex's — an event maps
straight to entries carrying `command`, with a top-level `"version": 1`
and a lower-camel event name. `--read-guard --client cursor` is
**refused**: `beforeReadFile` is observe-only, so a read cannot be
declined there.

### `tokenops hooks install --read-guard --client opencode`

opencode is the **only client besides Claude Code where read-guard can
actually refuse a read**. Codex has no file-read tool at all; Cursor's
`beforeReadFile` is observe-only. opencode has a `read` tool and a
`tool.execute.before` hook that blocks when it throws.

What it does not have is a config file to merge into — its extension
point is a JavaScript module. So tokenops **generates** one at
`~/.config/opencode/plugins/tokenops-read-guard.ts`:

```bash
tokenops hooks install --read-guard --client opencode
# restart opencode to load it
```

Generated rather than shipped, deliberately. A published package would
mean a second artifact, a second release path, and version skew between
the plugin and the binary it calls. The generated file has none of that:
it is written by the binary, names the version that wrote it, hardcodes
the absolute path to that same binary, and is rewritten in place by the
next install. It is a config file that happens to be JavaScript — the
other three clients get one too, theirs just happen to be JSON.

Delete the file to remove it.

**It fails open.** A guard sitting in front of every file read must never
be the reason a read cannot happen. A missing binary, a spawn failure,
unparseable output — all allow the read. Only an explicit deny throws,
and the throw happens outside the `try` so a bug in the error handling
cannot swallow a real refusal.

**It respects `coaching.delivery`** like every other read-guard surface:
`intervene` refuses, anything below records what it would have refused.

`--coach --client opencode` works too, and takes a different route from
every other client. opencode has no end-of-turn hook that accepts a
return value, so the nudge is delivered as a **TUI toast** — the one
channel a plugin has to reach you. The trigger is `session.idle`.

It is also the only client that already knows what its turns cost:
opencode records a per-message `cost`, and the coach uses that in
preference to inferring one from a rate card. That is how a session on a
model no rate card carries still produces a number.

A zero cost is usually *correct* — a Copilot turn or one through
opencode's own gateway is included in a subscription. But the budget is
denominated in **API-equivalent** spend, the counterfactual, so a zero
falls back to the rate card exactly as it does for Claude Code on a
subscription. When neither can answer, the model is reported as unpriced
rather than counted as free.

The whole session is recomputed on each idle rather than accumulated.
`session.idle` fires every time you stop typing, and a hook that added on
every fire would inflate a session without bound.

#### Codex sessions may report $0

The shipped rate card has no entry for `gpt-5.5` or `gpt-5.6-luna`, which
is what Codex runs today. Those turns cannot be priced, and a session
reported as free when nobody could cost it is exactly the failure this
tool exists to find — so `coach-hook stats` names them instead of staying
quiet:

```
  not priced — no rate card for these models:
    gpt-5.5                14 turn(s)
    their spend reads as $0 above. Add rates via `pricing.path` to count them.
```

and it will not tell you your spend is lean on the strength of numbers it
could not compute.

## Replay

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

## Command-output compression (`fmt`)

Deterministic per-command output compression — shrink a command's stdout
before it enters an agent's context, keeping every critical line (errors,
failures, changed state) and dropping noise. 46 commands ship built in;
loss level and extra formatters are configured under
[`optimizer.command_fmt`](./configuration.md#command-output-compression-command_fmt).

### `tokenops fmt -- <command> [args...]`

Runs the wrapped command, compresses its stdout, passes stderr through,
and **propagates the exit code** so agents still see failures. The full
raw output is written to `~/.tokenops/recovery/` so nothing is lost.

```bash
tokenops fmt -- git status
tokenops fmt --level aggressive -- npm install
tokenops fmt --quiet -- go test ./...
```

Flags: `--level` (override loss level for this run), `--raw-on-error`
(forward raw stdout on non-zero exit, default on), `--emit` (record an
OptimizationEvent), `--no-recover`, `--quiet`.

### `tokenops fmt bench --corpus <dir>`

Measures per-file and aggregate byte / estimated-token savings at each
loss level over a directory of captured command outputs
(`<command>.<label>.txt`). Use it to quote real numbers for your own
command mix.

### `tokenops fmt hook [--shell zsh|bash]`

Emits shell wrapper functions for the formatter-backed commands, gated on
`TOKENOPS_FMT=1` so compression only activates where you want it (e.g. an
agent session) and interactive use is untouched.

```bash
eval "$(tokenops fmt hook)"      # add to ~/.zshrc
export TOKENOPS_FMT=1            # the agent sets this to activate
```

### `tokenops fmt recover <id>`

Prints the full stored output for a prior run and records the re-access —
the signal `fmt learn` uses to detect over-compression.

### `tokenops fmt learn [--apply]`

Mines the recovery index (compression + re-access records) and reports
which commands need a formatter (falling back to the generic scrub) and
which are over-compressing. `--apply` writes the safe loss-level tuning
back to config locally; new-formatter candidates are printed as a
paste-ready stub. The `tokenops_fmt_learn` MCP tool returns the same
report to agents. The formatters stay deterministic — learning proposes,
it never mutates runtime behaviour.

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
