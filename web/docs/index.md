---
layout: home
hero:
  text: Know what's left. Set what's allowed. Let your agents work.
  image:
    light: /mark.svg
    dark: /mark-dark.svg
    alt: ""
  tagline: Guardrails for coding agents. TokenOps watches every AI plan and account your agents draw on (Claude, Codex, Cursor, Copilot and about 90 more) and steps in during the session, before a limit, a budget or a rule you set is crossed. On your machine; your prompts and your code never leave it. Free and open source; teams add shared rules and approvals.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
    - theme: alt
      text: Set a boundary
      link: /guide/boundaries
features:
  - title: Know what's left, on every plan
    details: 'Claude Max, ChatGPT Pro, Copilot, Cursor, z.ai, Kimi, API keys and routers: about 90 providers in one view of what is left in each window, the pace and when it resets. Each window is read several ways and the newest wins; a reading that stopped is shown with its age, never as current. In the terminal, the status line and the macOS menu bar.'
  - title: Steer the session it's in
    details: 'read-guard refuses the third redundant read of a file. fmt compresses a 40k-token command output before it reaches the context window. The coach nudges as session cost crosses a budget fraction. session_budget hands the agent a closed action it branches on: continue, slow_down, switch_model, wait_for_reset.'
  - title: Set what's allowed, before it starts
    details: 'Action boundaries put limits on what an agent may do without asking: keep going past a session cost, spend into the last of a window or a budget, start a subagent on a bigger model, send figures off the machine. Every rule starts in shadow, recording what it would have done, until you enforce it. Only a person at a terminal can approve; the agent that was stopped cannot approve itself.'
  - title: For teams, the same guardrails, shared
    details: 'Owners set rules every member gets as a signed document they can tighten but never loosen. A rule that needs a role asks the people who hold it, who answer in the web view or the CLI, and every answer records who gave it. The team view rolls up derived figures only, by team, repository and kind of work, never prompts, files or paths.'
---

## What you use today, and what it leaves out

| | Each vendor's own meter | Usage meters such as CodexBar | Gateways and admin caps | TokenOps |
|---|:--:|:--:|:--:|:--:|
| Every plan and account in one view | one vendor | ✅ | API keys only | ✅ |
| Subscription windows (Max, Pro, Copilot, Cursor) | its own | ✅ | 🚫 | ✅ |
| Acts inside the session, before the limit | 🚫 | 🚫 | fails the request at the cap | ✅ |
| Limits you set, with approval by role | 🚫 | 🚫 | dollar caps | ✅ |
| Which account paid, through routers | 🚫 | 🚫 | its own traffic | ✅ |
| Prompts stay on your machine | ✅ | ✅ | 🚫 they pass through it | ✅ |

The meters tell you where you stand. A gateway stops a request when the
money runs out. TokenOps does what neither does: it tells the agent, while
it is working, and holds it at the line you drew.

## Know what's left

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

## Steer the session it's in

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

## Set what's allowed

Agents run for long stretches now, and the moment where you would have
asked "should this continue?" is gone. An action boundary puts it back:
a limit you set before work starts, at the points that can actually stop
an agent, and a record of who let it go further.

```yaml
# ~/.tokenops/config.yaml
boundaries:
  - name: session-spend
    action: continue          # the agent's next tool call
    when: { session_cost_usd: ">= 20" }
    require: ask              # allow | ask | approver:<role> | deny
  - name: plan-floor
    action: continue
    when: { plan_left_pct: "< 5", window: week }
    require: deny
  - name: big-subagents
    action: subagent_model    # the model a subagent is about to run on
    when: { model_tier: "> sonnet" }
    require: approver:admin
```

Every rule starts in shadow and only records what it would have done;
`tokenops boundaries status` shows how often, and `tokenops boundaries
promote <name>` enforces it. In Claude Code and opencode the check runs
before each tool call in under half a millisecond, and fails open. An
enforced rule asks at the prompt when you are there; when you are not, it
stops the agent with the one command that lets it go on,
`tokenops approve <id>`, which only a person at a terminal can run. The
menu bar lists what waits on you. Codex and Cursor get the same rules as
advice the agent reads on the glance.

Boundaries also hold the router (a held route leaves the request on its
model, it never fails it) and anything that would send figures off the
machine. [The guide](/guide/boundaries) has every action and condition.

## For teams

The same guardrails, set once for everyone. Owners and admins write rules
in the team plane's web view or with `tokenops team admin boundaries`;
every machine fetches them as a document the server signs and the machine
verifies, so a member can tighten them but never loosen them. A rule that
needs a role sends the people who hold it a request carrying the rule, the
action and the figures that crossed it, nothing else, and they answer in
the web view or with `tokenops team admin approve`.

The team view rolls up what each machine measures, by team, repository and
kind of work: counts, time waiting on the agent, tokens and cost. Never
prompts, files, paths or commit messages; the upload format has no field
that could hold them, and `tokenops team preview` prints an upload byte for
byte. Groups of fewer than three people are withheld, and individual
figures need a grant the member sees. klarlabs hosts it in the EU, with a
14-day trial; see [the team plane](/guide/team).

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
`~/.tokenops`, there is no cloud account to create (unless you join a team),
and no telemetry to opt out
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

## Who this is for

- Developers running coding agents for hours a day across more than one
  plan (Claude Max and Codex, Cursor and an API key) who lose work to a
  mid-task cutoff or find out about a bill after the fact
- Engineering leads on usage-based contracts (Claude Enterprise, API
  accounts) who want limits and approvals for agents without putting a
  gateway in front of every prompt
- Anyone who lets an agent run unattended and wants to decide in advance
  how far it may go

If you don't recognise any of those, this isn't for you — and that is a
fine outcome. It was built to solve them, not to be adopted.

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

Shipping now: **v0.105.0**. See [release highlights](/changelog) for what
changed and why, or the
[full changelog](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md)
for every commit.
