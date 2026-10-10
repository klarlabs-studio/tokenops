---
layout: home
hero:
  name: TokenOps
  text: Analytics tells you about last week's retry loop. TokenOps ends this one.
  tagline: Your coding agents already write down everything they do. TokenOps reads those files on your machine — Claude Code, Codex, opencode, Cursor, Gemini CLI — watches every plan's limits, and hands the agent back something it can act on mid-task. Your prompts and your code never leave this machine. No account. Apache 2.0.
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
  - title: Every plan's limits, from more than one source
    details: 'Claude Max, ChatGPT Pro, Copilot, Cursor and pay-as-you-go accounts in one view: what is left in each window, the pace, when it resets. Each window is read several ways, and the newest wins. A reading from a source that stopped is shown with its age, never as current. In the terminal, the status line and the macOS menu bar, with alerts at 20% left, 5% and used up.'
  - title: It reads what is already there
    details: 'Passive readers for every client that keeps a local record — Claude Code, Codex CLI, opencode, Cursor, Gemini CLI. What each of your commits cost. An optional proxy for ground truth. No change to how you or your agents work.'
  - title: Your prompts and your code never leave this machine
    details: 'Prompt text and file contents are read at scan time and never persisted — only derived numbers are. Local SQLite, no cloud account, no telemetry. Every outbound call is named below, and the one that can send figures (an OpenTelemetry export to your own collector) is off until you turn it on.'
  - title: Honest about what it cannot see
    details: 'Every prediction carries signal_quality (low / medium / high) plus a one-line caveat and an upgrade path. The capability matrix marks what a client can never support — not "coming soon" — with the reason. Time-to-first-token is reported only under the proxy, because no transcript records it.'
---

## Analytics stops at a report. This doesn't.

Reading your agents' session files is the easy half. Every finding below is
one TokenOps computes *and* acts on, inside the session it found it in — not
in a report you open the following week.

| TokenOps finds | TokenOps does, in the same session |
|---|---|
| The same file read three times in one turn | `read-guard` refuses the third read before it costs a token — Claude Code and opencode, the two clients whose hooks can decline one |
| A 40k-token command output heading for the context window | `tokenops fmt` compresses it to the part the agent needs |
| The plan window minutes from a cutoff | `tokenops_glance (view=session_budget)` returns `wait_for_reset`; the agent parks the work |
| A frontier model closing work a cheap one would close | `tokenops_routing (action=advise)` decides per turn from task class and window pressure — no rules table, and it recommends rather than rewrites |
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

## Five clients, and a matrix that admits the gaps

Claude Code, Codex, Cursor, opencode and Gemini CLI all keep a local
record. TokenOps reads every one of them, and arms what each one can
actually carry:

| | reads sessions | coaching nudge | refuses a redundant read |
|---|:--:|:--:|:--:|
| Claude Code | ✅ | ✅ | ✅ |
| Codex CLI | ✅ | ✅ | 🚫 |
| Cursor | ✅ | ✅ | 🚫 |
| opencode | ✅ | ✅ | ✅ |
| Gemini CLI | ✅ | ⬜ | ⬜ |

⬜ is not built yet: for Gemini CLI, TokenOps reads sessions only, so far.
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

Want to see what a week of your agents' work cost and where it leaked
first? One command, nothing to install first:

```bash
npx @klarlabs-studio/tokenops checkup
```

It reads the week straight from the transcripts, with no setup and nothing
sent anywhere. Then:

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
  "tool": "tokenops_glance (view=session_budget)",
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

There is nowhere for them to go: the event store is a SQLite file under
`~/.tokenops`, there is no cloud account to create, and no telemetry to opt out
of. The daemon API is local by default and protected by a shared secret.

The guarantee is about the content, not the address. Everything TokenOps
derives — a rate, a grade, a token count, a headroom percentage — is computed
here, from material that stays here. The words you typed and the files you
opened are not in that set and cannot be moved into it by configuration.

That is a stronger guarantee than redaction-then-upload. Redaction is a filter
over unbounded input, and a filter can miss: a prompt can contain anything, and
whoever wrote the patterns had to guess what. Here there is no sensitive
payload to redact, because the sensitive part never enters the pipeline.

**Every outbound call, named rather than discovered.** A tool that starts
talking to the network without saying so has spent trust it cannot buy back.

- **Rate cards and currency.** Once a day the daemon fetches a public rate
  card (LiteLLM's, plus models.dev's for gateways such as Fireworks), so a
  model released after your binary does not silently price at zero. When
  your currency is not the US dollar, it also fetches the ECB's daily euro
  rate. Both *download*. They send no prompt, no file, no identifier and no
  usage figure, and each is one line to switch off
  (`pricing.refresh.disabled: true`, `money.fetch_rate: false`; see
  [configuration](/guide/configuration#automatic-rate-card-refresh)).
- **Your own plan's limits, from the vendor.** The readers you turn on ask
  each vendor how much of *your* plan is used: the claude.ai usage meter,
  Claude Code's own sign-in (opt-in), `codex app-server` (Codex signs its
  own request), and the account endpoints of the keys your harnesses already
  hold. Each credential goes only to the vendor that issued it, and the
  answer is a percentage and a reset time. The full list, and what each
  needs, is on the [coverage page](/integrations/coverage#where-plan-windows-come-from).
- **Figures to your own collector, if you ask.** `otel.enabled` pushes
  derived metrics to an OpenTelemetry collector you name: window use and
  pace, usage and its value, session grades, the coach's findings, cost per
  commit. Figures only, never prompts, code or commit subjects, and
  `tokenops otel` prints exactly what would leave. It is off until you turn
  it on.

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

## Honest about what it sees, and how fresh it is

A plan's windows come from the vendor's own figures, read several ways and
least invasive first. For Claude: Claude Code's status line on every turn,
the claude.ai usage meter between sessions, and, if you opt in, Claude
Code's own sign-in. For Codex: its app server every 15 minutes, and the
rate limits in every rollout. The newest reading of each window wins.

Every window says which source read it and when. When that source has
stopped, the window shows its age in place of its pace, and raises no pace
warning. If every window of a plan is stale, the coach says so with the fix,
and the menu bar alerts once. On 5 October an expired claude.ai session left
one install showing a 21-hour-old week as current, 15 points off. That
cannot happen quietly any more.

Every other prediction carries `signal_quality` (low / medium / high) with a
one-line caveat and an upgrade path. Where no vendor figure exists, the
estimate says it is one.

## Why TokenOps exists

Every major vendor publishes *rate-limit windows*, not monthly token caps, for
flat-rate plans. The vendor dashboard tells you you've hit the cap **after**
the cap hits, and nothing tells the agent anything at all. When Claude returns
"limit reached, resets in 4h" mid-refactor, your options are another browser
tab or eating the wait.

TokenOps puts the headroom check inside the agent's loop, for every provider it
tracks, and in front of you before the cutoff: the status line, the menu bar
and an alert at 20% left. `tokenops init` binds your plans from what your
clients report about themselves, and asks only for what it cannot tell:

```
PLAN                       WINDOW            LEFT  RESETS    PACE
Codex Pro Standard ($100)  Weekly              0%  3d 10h    used up
Claude Max 20x             Session            75%  1h 57m    -36% · lasts
                           Weekly             76%  3d 5h     -30% · lasts
```

That is `tokenops glance --brief` on this machine, the day it shipped.

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

Shipping now: **v0.102.0**. See [release highlights](/changelog) for what
changed and why, or the
[full changelog](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md)
for every commit.
