# coach-hook — Stop-hook session-budget coaching nudge

`coach-hook` is a Claude Code **Stop** hook. After each turn it sums the full
API-equivalent cost of the new turns into a running **per-session total** and,
as that total crosses fractions of a per-session budget, surfaces a short,
non-blocking, escalating nudge to reclaim context:

> tokenops: 75% of your $50 session budget ($37.60) — consider /compact or a
> fresh session soon; cache-read grows every turn you carry this context.

Cache-read is the dominant recurring cost in a long Claude Code session: every
turn re-bills the whole accumulated context prefix at the cache-read rate, and
that toll repeats on *every* turn until you `/compact` or start fresh. The coach
gives you the signal to pull that lever before the session compounds into real
money.

It works even when your traffic never reaches the tokenops proxy (e.g. Claude
Code on a subscription), because it runs **inside** the client.

## On a flat-rate plan it speaks quota, not dollars

When the session's provider has a plan bound (`plans.anthropic`,
`plans.openai`) and a live window reading exists — the claude.ai usage meter
for Claude Code, the rate limits Codex records on every turn — the coach
replaces the dollar ladder with the plan's own window. On a subscription the
dollar figure is a counterfactual; the window is what stops work.

> tokenops: 76% of your weekly Claude limit used, resets in 2d 4h. At this
> pace it runs out in ~1d 9h, before the reset. Route research and lookups to
> a smaller model and /compact long sessions to stretch it.

- It judges the **most constrained** window the vendor reports (5-hour,
  weekly, or a model-scoped weekly limit such as "weekly Fable").
- It speaks at 50%, 75%, 90%, and 100% of that window, **each once per
  window across all sessions**, because a window outlives any one session.
  A new reset re-arms the tiers.
- It adds a pace line only when the current rate of use would exhaust the
  window before it resets.
- It needs a reading no older than 30 minutes. Without one — no plan bound,
  the meter not connected, or a client with no window meter (Cursor,
  opencode) — the dollar ladder below applies unchanged.
- The reading is a read-only query; the hook never takes the store's write
  lock.

`tokenops coach-hook stats` counts quota nudges by tier beside the dollar
alerts.

## Why a cumulative budget (not a per-turn threshold)

The first version nudged when a **single** turn's cache-read crossed a flat
token threshold. That misses the most expensive real-world shape: long, *flat*
sessions. Observed sessions ran **7,000–9,300 turns at ~600k cache-read
tokens/turn** and quietly accrued **~$2,400** in API-equivalent spend — yet no
single turn was extreme, so a per-turn threshold never fired. The cost is made
by **accumulation**, not by spikes.

So the coach now tracks **cumulative per-session spend** against a budget
(default **$50**) and alerts at budget fractions. Because the metric is dollars,
it is **model-agnostic**: a cheaper model accrues more slowly per token but
still trips the same fractions.

## Install

The simplest path is the scaffolder, which merges the hook into
`~/.claude/settings.json` for you (idempotent, backs up first):

```sh
tokenops hooks install --coach
```

Install both in-client hooks (coaching nudge + read dedup guard) at once:

```sh
tokenops hooks install --coach --read-guard
```

Preview without writing:

```sh
tokenops hooks install --coach --dry-run
```

Inspect / remove:

```sh
tokenops hooks status
tokenops hooks uninstall --coach
```

`hooks install`:

- targets `~/.claude/settings.json` (override with `--settings <path>`),
- **merges** the tokenops entries into `.hooks` without clobbering unrelated
  hooks you already have,
- is **idempotent** — re-running never duplicates an entry; it updates in place,
- **backs up** the prior file to `settings.json.bak` and writes atomically
  (temp + rename),
- prints the binary path + version the hook will call, so you can see exactly
  which build is wired in and get a warning if a different tokenops was already
  referenced.

If you prefer to wire it by hand, `tokenops coach-hook hook` prints the raw
settings.json block.

## Budget and alert tiers

Flags on `hooks install` (and on the bare `coach-hook` command):

| Flag       | Default | Meaning                                                          |
| ---------- | ------- | ---------------------------------------------------------------- |
| `--budget` | `50`    | Per-session API-equivalent USD budget the alert fractions measure against. |

The coach fires **once** at each of **50%**, **75%**, and **100%** of the
budget, then re-alerts every additional budget over — **200%**, **300%**, and so
on. Each boundary is **latched**: once an alert fires it never repeats, so the
coach never nags every turn. A Stop that jumps across several fractions at once
(e.g. 40% → 120%) fires only the **single highest** boundary reached (here,
100%), not a burst of every crossed tier.

Set `--budget` lower to be nudged earlier, higher if your normal sessions
legitimately run large.

### Follow-through

Each tip is recorded in `~/.tokenops/coach/followthrough.jsonl` and settled
over the next three turns: **followed** when the context shrinks to half or
less (a `/compact` or a fresh start), the model changes, or the session
ends; **ignored** when none of that happens. An early tier (50% or 75%)
ignored five times in a row within 14 days is held back from then on
(`coach-hook stats` counts it as "held back because earlier tips like them
were ignored"), until that evidence ages out. `verbose` still gives it, and
tips at 90% of a window or past the budget are never held back.
`tokenops coach` shows the record per tier.

## Stats

See how much your sessions have spent and which alerts fired:

```sh
tokenops coach-hook stats
tokenops coach-hook stats --json
```

It reports Stop events observed, distinct sessions, the budget alerts fired
broken down by tier (50% / 75% / 100% / 200% …), the largest single-session
API-equivalent spend, and the total estimated spend across sessions.

## Cost estimate

Cost is an **API-equivalent** figure: what each turn's tokens would cost at
public list prices. The rate comes from tokenops' own pricing catalog (`spend`
engine); each turn is priced across **all** token types — input, output,
cache-write, and cache-read. A model the catalog can't price contributes $0 (the
turn still counts toward dedup, it just adds nothing), so the total never
over-states what we can defend. On a subscription the money is not billed per
turn — the figure is there to make the size of the drag legible.

## Privacy

The hook reads only the **tail** (~256 KiB) of your local transcript jsonl to
find the turns added since the last Stop. It never reads the whole file and
never sends anything off your machine. State is a tiny per-session counter
(cumulative $, the highest alert fired, and a dedup timestamp) plus an
append-only ledger under `~/.tokenops/coach-hook/`.

## Fail-open guarantee

A coach must never disrupt your session. On any error — unreadable transcript,
malformed payload, missing usage data — the hook exits 0 with no output and the
turn proceeds untouched. It uses `systemMessage` (a user-facing, non-blocking
channel), never `decision:"block"`, so it can surface a nudge without forcing the
agent to keep going.
