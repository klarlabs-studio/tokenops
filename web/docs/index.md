---
layout: home
hero:
  name: TokenOps
  text: Analytics tells you about last week's retry loop. TokenOps ends this one.
  tagline: Your coding agents already write down everything they do. TokenOps reads those files on your machine — Claude Code, Codex, opencode, Cursor — and hands the agent back something it can act on mid-task, instead of a dashboard you read on Friday. Nothing uploads. No account. Apache 2.0.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
    - theme: alt
      text: What it can read
      link: /integrations/coverage
features:
  - title: It acts, it doesn't just report
    details: 'read-guard refuses the third redundant read of the same file. fmt compresses a 40k-token command output before it reaches the context window. coach-hook nudges as session cost crosses a budget fraction — on Claude Code, Codex, Cursor and opencode. session_budget returns a closed action enum the agent branches on: continue, slow_down, switch_model, wait_for_reset.'
  - title: It reads what is already there
    details: 'Passive readers for every client that keeps a local record — Claude Code, Codex CLI, opencode, Cursor. Six vendor-usage pollers. An optional proxy for ground truth. No change to how you or your agents work.'
  - title: Your prompts never leave this machine
    details: 'Prompt text and file contents are read at scan time and never persisted — only derived numbers are. Local SQLite, no cloud account, no telemetry. The dashboard is a localhost daemon behind a shared secret you can rotate.'
  - title: Honest about what it cannot see
    details: 'Every prediction carries signal_quality (low / medium / high) plus a one-line caveat and an upgrade path. The capability matrix marks what a client can never support — not "coming soon" — with the reason. Time-to-first-token is reported only under the proxy, because no transcript records it.'
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

## Analytics stops at a dashboard. This doesn't.

Reading your agents' session files is the easy half. Every finding below is
one TokenOps computes *and* acts on, inside the session it found it in — not
in a report you open the following week.

| TokenOps finds | TokenOps does, in the same session |
|---|---|
| The same file read three times in one turn | `read-guard` refuses the third read before it costs a token — Claude Code and opencode, the two clients whose hooks can decline one |
| A 40k-token command output heading for the context window | `tokenops fmt` compresses it to the part the agent needs |
| The plan window minutes from a cutoff | `tokenops_session_budget` returns `wait_for_reset`; the agent parks the work |
| A frontier model closing work a cheap one would close | `tokenops_routing_advise` decides per turn from task class and window pressure — no rules table, and it recommends rather than rewrites |
| Cumulative session cost crossing a budget fraction | `coach-hook` nudges mid-session — a Stop hook on Claude Code, Codex and Cursor, a TUI toast on opencode |

## What your sessions actually look like

`tokenops dx` groups work by operator instruction — a prompt you typed, and
everything the agent did before the next one — straight from transcripts the
client already writes. No proxy, no extra instrumentation:

```
Agent DX — last 7d
  478 instructions across 14 sessions

EFFORT PER INSTRUCTION
  turns (median):        17.0       [C]
  turns (p90):           105.6        ← heavy tail: a minority of instructions cost far more than typical
  wall-clock (median):   1.6m       [A]
  tool calls (median):   8.0
  context growth/turn:   2445       [A]

FRICTION
  first-try rate:        86.6%      [A]  (no rework, no interrupt, no delegation)
  rework rate:           20.7%      [C]  (edits revisiting a file within one instruction)
  interrupt rate:        0.2%       [A]  (instructions you had to stop)
  escalation rate:       1.9%       [A]  (instructions delegated to a subagent)
  compactions/session:   1.0        [B]

Overall: C  (the worst grade, not the average — an experience is
         only as good as its sharpest friction)
```

That is this project's own last seven days, printed unedited. A `C` on the
maintainer's machine is the point: a tool that grades your sessions and always
returns `A` is not measuring anything.


It also answers the question everyone assumes they know: *does quality degrade
as context grows?* The table pairs prompt count, rejection rate, and repeated
tool calls per context band — and says plainly when the corpus is too noisy to
call it. Grading the worst dimension rather than the average is deliberate: an
average hides the one thing making the session unpleasant.

## Four clients, and a matrix that admits the gaps

Claude Code, Codex, Cursor and opencode all keep a local record and all
have a hook surface. TokenOps reads every one of them, and arms what each
one can actually carry:

| | reads sessions | coaching nudge | refuses a redundant read |
|---|:--:|:--:|:--:|
| Claude Code | ✅ | ✅ | ✅ |
| Codex CLI | ✅ | ✅ | 🚫 |
| Cursor | ✅ | ✅ | 🚫 |
| opencode | ✅ | ✅ | ✅ |

The two 🚫 are not a roadmap. **Codex has no file-read tool at all** —
across 40 real rollouts every call was a shell command, so there is no
read to intervene in. **Cursor's `beforeReadFile` cannot decline** — only
its shell and MCP hooks honour a permission decision. `tokenops hooks
install --read-guard` refuses on both, with the reason, rather than
writing a hook that never fires.

The [capability matrix](/integrations/coverage#what-reaches-which-client)
carries the same honesty per feature, including for Desktop and
GitHub-hosted clients where coaching is pull-only and always will be.
Parity is not available everywhere. Saying so is the differentiator
against a waitlist that promises it.

## An account of the work, for whoever is asking

`tokenops story` reconstructs what happened one task at a time — the
instruction you typed, what the agent did before the next one, what it
cost, and the specific moments it went sideways. One structure, four
readings, because four people want the same work described and none of
them wants the same document:

```bash
tokenops story                  # candid, for you
tokenops story --json           # enumerated, for the agent
tokenops story --for report     # evidence, for someone you bill
tokenops story --for handoff    # state of the world, for a teammate
```

Titles are your own instructions, quoted rather than paraphrased — a
summariser can be wrong and a quote cannot. The `report` rendering leaves
out the friction narrative on purpose: "you told it the third answer was
wrong" is candour aimed at you, and in front of a client it turns an
account of work into an apology for it. None of the four claims the work
is *correct*; a transcript records what was attempted, and only the tests
know the rest.

## 90 seconds, three commands

```bash
brew trust klarlabs-studio/tap              # first time only
brew install --cask klarlabs-studio/tap/tokenops
tokenops init                               # config, MCP registration, hooks
```

`init` finds the MCP hosts you actually have, registers `tokenops serve` with
each — pinned to the absolute binary path, so a host can never silently run a
stale build — installs the Claude Code hooks, and prints what still needs you:

```
Wiring tokenops into this machine:
  ✓ MCP: Claude Code       registered — restart Claude Code to load the tools
  ✓ hooks                  installed coach-hook + read-guard (Claude Code)
  · plan binding           detected anthropic but not which tier you pay for
```

Re-run it any time to repair drift. It is idempotent, backs up every file it
touches, and refuses to overwrite a host config it cannot parse. Two things
stay yours: picking your plan tier, and pointing a client at the proxy.

## For agents

The MCP surface is a deterministic guardrail, not vibes-in-the-loop. Closed
action enum, calibrated confidence:

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

`recommended_action` is a closed enum so the agent picks the right branch
without parsing prose. `signal_quality.level` lets it decide how much to trust
the call: stay aggressive on `high`, defer to the human on `low`.

## Your code and your prompts never leave this machine

Prompt text is read at scan time and **never written to the store** — the
coaching, DX and story surfaces compute over it in memory and persist only
derived numbers. Your files are read where they already sit and are never
copied anywhere.

Today there is nowhere for them to go: the event store is a SQLite file under
`~/.tokenops`, there is no cloud account to create, and no telemetry to opt out
of. The dashboard is a local daemon behind a shared secret you mint and rotate
yourself (`tokenops dashboard rotate-token`).

The guarantee is about the content, not the address. Everything TokenOps
derives — a rate, a grade, a token count, a headroom percentage — is computed
here, from material that stays here. The words you typed and the files you
opened are not in that set and cannot be moved into it by configuration.

That is a stronger guarantee than redaction-then-upload. Redaction is a filter
over unbounded input, and a filter can miss: a prompt can contain anything, and
whoever wrote the patterns had to guess what. Here there is no sensitive
payload to redact, because the sensitive part never enters the pipeline.

**One outbound call, named rather than discovered.** The daemon fetches a
public rate card once a day so a model released after your binary does not
silently price at zero. It *downloads*; it sends no prompt, no file, no
identifier and no usage figure, and it is one line to switch off
(`pricing.refresh.disabled: true`). A tool that starts talking to the network
without saying so has spent trust it cannot buy back, so it is documented in
[configuration](/guide/configuration#automatic-rate-card-refresh) and logged
on start.

## Cache-aware, or off by 9×

On agent workloads, cache reads routinely exceed 95% of input tokens — and
they bill at roughly a tenth of the new-input rate. Price them as fresh input
and the number is not slightly wrong, it is an order of magnitude wrong. On
one real seven-day window, correcting the cache split moved the reported
figure from **$94k to $10k**.

Rates are pinned per model with dated vendor source URLs, and
`tokenops pricing` can research, snapshot, diff, and lint them — now on a
daily timer rather than only when someone remembers. Hand-checked rows are
marked `verified` and a fetched snapshot cannot regress them; negotiated rates
layer over everything through a pricing override file.

A model nobody can price is reported as unpriced, not costed at zero. The
difference between "this session was free" and "we could not measure it" is
the whole product, and a rate card goes stale on its own.

## Honest about what it sees today

TokenOps reports its own signal quality on every prediction. Four sources,
ranked by faithfulness:

- **`mcp_tool_pings` (low)** — Default. Counts MCP invocations as an activity
  proxy. Useful as an "is the agent talking to me?" signal, not a quota meter.
- **`claude_code_stats_cache` (medium)** — Per-model daily totals; can't
  resolve the 5h rolling window but gives real attribution. The schema is
  undocumented, so every response carries a caveat.
- **`proxy_traffic` (high)** — Route your SDK's base URL through the local
  proxy. Captures every request per-event.
- **`vendor_usage_api` (high)** — Anthropic Admin API poller. Covers metered
  API usage; Claude Max plan-window state has no documented endpoint and
  stays heuristic.

## Why TokenOps exists

Every major vendor publishes *rate-limit windows*, not monthly token caps, for
flat-rate plans. The vendor dashboard tells you you've hit the cap **after**
the cap hits, and nothing tells the agent anything at all. When Claude returns
"limit reached, resets in 4h" mid-refactor, your options are another browser
tab or eating the wait.

TokenOps puts the headroom check inside the agent's loop, for every provider it
tracks. One CLI, one MCP server, one event schema:

```bash
tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-plus
tokenops plan set github copilot-business
tokenops plan set cursor cursor-pro
```

Every plan you bind contributes to a unified headroom view your agent can
query mid-conversation.

## Who this is for

- Solo founders and small teams running coding agents 6+ hours a day across
  multiple AI subscriptions
- Staff engineers billing client time against AI sessions, juggling Claude +
  GPT + Copilot stacks
- Anyone who has lost focus to a mid-task rate-limit cutoff on any provider

If you don't recognise that moment, this isn't for you — and that is a fine
outcome. It was built to solve it, not to be adopted.

## Contributing

Apache 2.0, and built for its maintainer's own daily use: every number on this
page came off this machine, including the `C`.

Issues and pull requests are the front door. Two kinds of contribution are
worth more than the rest:

- **A reader that gets your client wrong.** Every silent zero this project has
  found was found the same way — counting a real store by hand and comparing
  it to what TokenOps reported. If those two numbers disagree on your machine,
  that comparison *is* the bug report, and it is the most useful thing you can
  send.
- **A rate correction with the vendor page attached.** Rates are pinned per
  model with dated sources, and a `verified` row is one somebody hand-checked.
  This repo has twice paid for acting on a rate it had not.

[Open an issue](https://github.com/klarlabs-studio/tokenops/issues/new) when
either applies; [CONTRIBUTING.md](https://github.com/klarlabs-studio/tokenops/blob/main/CONTRIBUTING.md)
covers the rest.

---

Shipping now: **v0.63.0**. See [release highlights](/changelog) for what
changed and why, or the
[full changelog](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md)
for every commit.
