# ADR 0001 — Opt-in usage-coaching hooks

- **Status:** Proposed
- **Date:** 2026-07-07
- **Deciders:** TokenOps maintainers
- **Related:** `tokenops coach`, `tokenops scorecard`, `tokenops read-guard`, `tokenops fmt`, `session_budget`/`budget_set` MCP tools

## Context

TokenOps can already *observe* agent usage (jsonl ingestion, spend/burn/forecast,
scorecard) and *react* at two touchpoints inside an agent session:

- `tokenops read-guard` — a Claude Code **PreToolUse** hook that blocks redundant
  file re-reads.
- `tokenops fmt` — wraps a command and compresses its output before it reaches
  the agent.

But everything *coaching*-related is **retrospective and pull-based**: you have to
run `tokenops coach` / `scorecard` after the fact to learn what was wasteful. The
operator gets no signal *while the waste is happening*.

A concrete incident motivated this ADR. A 7-day review of one operator's Claude
Code transcripts found ≈ **$50.6k API-equivalent** across ~68.6k requests, of
which **~79% ($40k) was cache-read tokens** — 26.75B of them. The driver was not
output volume (only ~84M input+output tokens) but **large context carried across
many turns of long sessions**: every turn re-reads the whole cached context. The
operator's own reaction: *"I could've been more efficient with shorter sessions."*

The lever is therefore known and specific — **session length / carried context** —
but it is invisible in real time, and the coaching that would surface it exists
only as an after-the-fact report.

## Decision

Introduce an **opt-in, hook-driven, live coaching layer** that turns TokenOps's
existing post-hoc analysis into real-time, threshold-based nudges, installed into
the agent host's hook configuration and off by default.

### Principles (non-negotiable)

1. **Opt-in and quiet.** Nothing installs or fires unless the operator enables it.
   A coach that nags gets disabled; silence is the default output.
2. **Threshold-based, not per-turn chatter.** Fire only when a signal crosses a
   configured bound (carried tokens/turn, session $, turn count), then back off.
3. **Actionable.** Every message names the lever (`/compact`, start a fresh
   session, drop a large file from context, split the task) — never a bare number.
4. **Cheap and local.** Hooks read the *tail* of the current jsonl (or a small
   local counter), never re-scan history, and add no measurable token cost. No
   data leaves the machine.
5. **Reuse, don't reinvent.** Signals come from the existing `spend` / `coach` /
   `scorecard` / `session_budget` engines; hooks are thin adapters.

### Hook taxonomy (Claude Code events; Codex/Cursor parity later)

| Event | Coaching role | Default | Source |
|---|---|---|---|
| **SessionStart** | One-line brief: "yesterday $X · today $Y · pace $Z/wk" | off | `tokenops spend` |
| **Stop** (per turn) | ⭐ Carried-context / cache-read nudge past a threshold | off | jsonl tail |
| **UserPromptSubmit** | Session budget guardrail — warn (or block) past a $ ceiling | off | `budget_set` |
| **PreCompact / SessionEnd** | Wrap-up: "session ≈ $X · N turns · M% cache-read" | off | jsonl |
| Weekly (throttled via SessionStart, or cron) | `scorecard` / `coach` digest | off | `coach` |

### The star: the Stop-hook cache-read nudge

The highest-value control, and the reference implementation for the layer:

- **Trigger:** on `Stop`, read the tail of the active session jsonl, compute the
  most recent turn's `cache_read_input_tokens` (and/or a rolling avg of
  carried-context tokens/turn).
- **Threshold:** when carried context exceeds a bound (default suggestion:
  cache-read > ~1M tokens/turn *sustained over K turns*), emit **one** line, then
  suppress for the next N turns (hysteresis).
- **Message:** e.g. *"This session is carrying ~1.4M tokens/turn in cache reads
  (~$2/turn API-equiv). `/compact` or a fresh session would cut most of it."*
- **Config:** `vendor_usage`/`coach`-style block — enabled, threshold, cooldown,
  message verbosity.

This directly targets the 79%/$40k lever and is O(tail) cheap.

### Distribution: `tokenops hooks install`

A new command scaffolds the selected hooks into the host's settings
(`~/.claude/settings.json` `hooks`, or project `.claude/settings.json`), writes
the coach config block, and is idempotent + reversible (`--uninstall`). It never
enables anything the operator didn't ask for and prints exactly what it changed.

## Alternatives considered

- **MCP-tools-only (status quo, pull).** Rejected as the *primary* path: it can't
  be proactive. The MCP tools remain, as the query surface the hooks summarize.
- **Always-on verbose coaching.** Rejected — violates "quiet"; gets disabled.
- **Daemon-push notifications (OS-level).** Deferred — heavier, and the in-agent
  channel (hook stdout → agent/operator) is where the decision (`/compact`) is
  actually made.
- **Hard blocking by default.** Rejected for coaching; blocking is opt-in and
  reserved for the explicit `UserPromptSubmit` budget guardrail.

## Consequences

**Positive**
- Real-time efficiency signal exactly where the cost is created.
- Ties TokenOps's value into the daily loop instead of a weekly report.
- Reuses existing engines; the hooks are thin.
- A differentiator: "the tool that coaches you *while* you burn tokens, opt-in."

**Negative / risks**
- **Host-coupling.** Hook event names/schemas are Claude Code-specific and can
  change across versions; needs a small compat shim + tests. (We already carry
  this risk with `read-guard`.)
- **Threshold tuning.** Bad defaults either nag or never fire; ship conservative
  defaults + easy override, and measure via `scorecard`.
- **Annoyance surface.** Mitigated by off-by-default, hysteresis, and one-line output.
- **Cross-agent parity.** Codex (`codex-jsonl`) and Cursor exist as sources; the
  Stop-hook concept must generalize (later phase).

## Open questions

1. **"Carried context" metric.** Cheapest proxy = last turn's `cache_read_input_tokens`;
   is a rolling avg over K turns worth the extra tail read? (Lean: start with last-turn.)
2. **Default thresholds.** What cache-read/turn and $/session defaults fire "often
   enough to help, rarely enough to trust"? Seed from real distributions, then tune.
3. **Budget guardrail: warn vs block.** Default warn; block only when the operator
   sets a hard ceiling.
4. **Version delivery.** Hooks call the installed binary — the running MCP/CLI must
   be current (this ADR was written the day a stale 0.25.1 server + a 0.30.1 CLI +
   a 0.35.0 repo were all live at once). `hooks install` should warn on a version
   mismatch.

## Rollout

- **Phase 1** — `tokenops hooks install` + the **Stop-hook cache-read nudge** (the
  reference control). Ship behind a docs page + conservative defaults.
- **Phase 2** — SessionStart spend brief.
- **Phase 3** — UserPromptSubmit budget guardrail (`budget_set`).
- **Phase 4** — PreCompact/SessionEnd wrap-up + weekly `scorecard`/`coach` digest.
- **Phase 5** — Codex/Cursor parity for the Stop-hook signal.

Each phase is independently opt-in and shippable.
