# ADR 0001 — Opt-in usage-coaching hooks

- **Status:** Accepted (Phase 1.1 implemented — cumulative-budget Stop-hook + `tokenops hooks install`)
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
| **Stop** (per turn) | ⭐ Cumulative session-budget nudge (graduated cache-read alerts) | off | jsonl tail |
| **UserPromptSubmit** | Session budget guardrail — warn (or block) past a $ ceiling | off | `budget_set` |
| **PreCompact / SessionEnd** | Wrap-up: "session ≈ $X · N turns · M% cache-read" | off | jsonl |
| Weekly (throttled via SessionStart, or cron) | `scorecard` / `coach` digest | off | `coach` |

### The star: the Stop-hook cumulative-budget nudge

The highest-value control, and the reference implementation for the layer:

- **Trigger:** on `Stop`, read the tail of the active session jsonl and sum the
  **full API-equivalent cost** of the new turns since the last Stop (input +
  output + cache-write + cache-read, each at the model's per-million rate),
  accumulating into a **per-session cumulative $**.
- **Alerts:** graduated, GitHub-Actions-style and **latched**. As the session's
  cumulative spend crosses fractions of a per-session budget (default **$50**),
  fire once at each of **50% / 75% / 100%**, then re-alert every additional
  budget over (**200% / 300% / …**). A Stop that jumps across several fractions
  fires only the single highest boundary reached; each boundary fires once.
- **Message:** escalating tone that always names the lever, e.g. *"tokenops:
  75% of your $50 session budget ($37.60) — consider `/compact` or a fresh
  session soon; cache-read grows every turn you carry this context."*
- **Config:** `budget` (USD), the alert `tiers`, and an `over-budget step`.

This directly targets the 79%/$40k lever and is O(tail) cheap.

#### Phase 1.1 — why cumulative budget replaced the flat per-turn threshold

Phase 1 shipped a flat bound: nudge when a **single** turn's
`cache_read_input_tokens` crossed ~1M, with a turn cooldown. That model is blind
to the dominant real-world failure mode. Observed sessions ran **7,000–9,300
turns at ~600k cache-read tokens/turn** and accrued **~$2,400** in
API-equivalent spend — yet *no single turn* was extreme, so a per-turn threshold
never fired even as the session quietly compounded into thousands of dollars.
The damage is done by **accumulation across a long, flat session**, not by any
one spike.

Phase 1.1 therefore tracks **cumulative per-session cost** against a budget and
prices the **whole** turn (not just cache-read), so the signal reflects real
spend and catches the long-flat shape the threshold missed. Latched
budget-fraction alerts replace the turn cooldown: hysteresis comes from each
tier firing once rather than from a fixed quiet window. The metric is
**$-normalized**, so it is model-agnostic — a cheaper model accrues more slowly
per token but still trips the same budget fractions.

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
  - **Phase 1.1** — replace the flat per-turn threshold with a **cumulative
    per-session budget + graduated latched alerts** (50/75/100% + over-budget
    escalation), pricing the full turn. Rationale above: the per-turn threshold
    missed long-flat sessions (~$2,400 over ~9k turns, no single spike).
- **Phase 2** — SessionStart spend brief.
- **Phase 3** — UserPromptSubmit budget guardrail (`budget_set`).
- **Phase 4** — PreCompact/SessionEnd wrap-up + weekly `scorecard`/`coach` digest.
- **Phase 5** — Codex/Cursor parity for the Stop-hook signal.

Each phase is independently opt-in and shippable.
