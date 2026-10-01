# Configuration

The daemon reads `config.yaml` (path via `--config`) merged with
`TOKENOPS_*` environment variables; CLI flags win last.

## Operating modes

`mode` decides how TokenOps helps you optimize:

| Mode | What runs |
|---|---|
| `passive` (default) | Collect + analyze on demand. Proxy observes traffic and stamps costs; pollers ingest vendor usage; `spend` / `replay` / `coach` answer when asked. Nothing is altered, nothing fires on its own. |
| `active` | Everything in passive, plus interventions: the proxy **applies `optimizer.routing_rules` to live traffic** (requests for `from_model` are rewritten to `to_model` before reaching the upstream, recorded as applied optimization events), and the daemon runs a **background spend watcher** that evaluates `budgets` every `watch.interval` and warns about budget threshold/forecast breaches and unpriced models. |

Recommended path: run passive, use `tokenops replay` to validate what a
routing rule would have saved on real history, then flip `mode: active`
to enforce it.

### The coach: who decides, and how much it says

**Start with a preset.** One choice sets every power and how much the coach
says, installs the coach's hooks on every agent installed here (Claude
Code, Codex, Cursor, opencode, as far as each supports them), and sets or
restores where agents compact:

```bash
tokenops coach preset            # list them; * marks yours
tokenops coach preset guided     # set one
tokenops init --preset autopilot # on a new machine (a new config gets advise)
```

| Preset | inform | waste | models | context | verbosity | In short |
|---|---|---|---|---|---|---|
| `observe` | off | off | off | off | quiet | records what it would say or do; says and does nothing |
| `advise` | advise | advise | advise | advise | normal | tells you what you could do better; changes nothing |
| `guided` | advise | autonomous | ask | advise | normal | refuses redundant re-reads, asks before moving subagents |
| `autopilot` | advise | autonomous | autonomous | autonomous | quiet | acts on its own and stays quiet, including where agents compact |

Asking an agent works the same way: the `tokenops_coach` tool takes
`preset` and runs the same command. Tuning one power afterwards is fine;
`tokenops coach` then shows the coach as *tuned*.

The coach has two dials ([ADR 0006](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0006-one-coach.md)):

```yaml
coach:
  autonomy: advise     # off | advise | ask | autonomous   (who decides)
  verbosity: normal    # quiet | normal | verbose          (how much it says)
  powers:              # optional: override autonomy for one power
    waste: autonomous
```

`autonomy` applies to four powers: **inform** (tips on your plan's quota
window), **waste** (redundant re-reads), **models** (moving work to a
cheaper model that fits it), and **context** (when the agent compacts).

| `autonomy` | The coach… |
|---|---|
| `off` | records what it would say or do, and does neither |
| `advise` | tells you what you could do better; changes nothing |
| `ask` | steps in with a concrete change and waits for your approval |
| `autonomous` | makes the change itself |

`verbosity` is independent of who decides:

| | `quiet` | `normal` (default) | `verbose` |
|---|---|---|---|
| Quota tips | at 90–100% of a window, or earlier when the pace runs out before the reset | each tier (50/75/90/100%) once per window | each tier, plus the pace line always and every window listed |
| Dollar tips (pay-per-token) | only past the budget | each tier once per session | each tier once per session |
| The read-guard case | never | once per session | once per session |
| Model advice | only when the model is two tiers or more above the work | once per kind of work per session | every applicable turn |
| Compact tip (20 turns above the compaction line) | never | once until the next compaction | once until the next compaction, with what compacting would keep context near |

For **context**, `advise` is the compact tip (20 turns above the
compaction line without compacting). `autonomous` sets where each agent
compacts on its own, at the same line (600k on Claude Code, 150k on Codex,
60% of each opencode model's window; `compact_at_tokens` moves it):

| Agent | What tokenops sets |
|---|---|
| Claude Code | `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW` in `~/.claude/settings.json` (the line + 33k: Claude Code compacts ~33k below it) |
| Codex | `model_auto_compact_token_limit` in `~/.codex/config.toml` |
| opencode | `provider.<id>.models.<id>.limit.input` in `opencode.json`, keeping the model's own context and output limits, for the models you ran in the last 30 days |
| Cursor | nothing: Cursor has no setting for when it summarises, so it stays advised |

A value you set yourself is never overwritten, and one you change after
tokenops set it is left as you have it. Lowering `context` restores every
file exactly as it was, byte for byte when nothing else has touched it
since. `tokenops coach` shows each agent's setting. `ask` reports as
`advise`: no agent offers an approval point for compacting. Setting
`context` to `autonomous` in `config.yaml` by hand does not edit the
agents' files; `tokenops coach set context autonomous` (or the
`tokenops_coach` tool) does.

For **models**, `ask` puts the same move to you first, in interactive
Claude Code sessions: the permission prompt for the subagent says which
model the coach proposes instead. **Yes** runs it there. **No** pauses the
agent. Tell it to continue and the subagent runs as planned, without being
asked about again. When nobody is attending (a headless run), `ask` stays
silent, because an unanswered question would stall the agent. Declines
count in the follow-through record, so a kind of move you keep declining
stops being proposed. `inform` and `waste` have nothing to approve and
report `ask` as `advise`.

For **models**, `autonomous` moves a subagent the agent launches to a
cheaper model that fits its work (Claude Code's Agent tool; the subagent's
short description is classified first). It only ever moves down, and the
session's own model is advised, never changed. It needs the route guard's
subagent hook: `tokenops hooks install --route-guard` adds it, and
`tokenops coach` says when it is missing.

The coach keeps a **follow-through record** in
`~/.tokenops/coach/followthrough.jsonl`: whether each piece of model advice
was followed (a later turn, or a subagent the agent launched, ran below the
tier it was given on) or ignored (four turns later, still on the same
tier), whether each quota or budget tip was followed (within three turns
the context shrank to half or less, the model changed, or the session
ended) or ignored, and whether each subagent move stood or was undone (you
lowered `models` within 24 hours). Advice for a kind of work, or an early
tip (50% or 75% of a quota window or the session budget), that you ignore
five times in a row within 14 days goes quiet for that kind; it is offered
again once that evidence ages out, and `verbose` always shows it. Tips at
90% of a window or past the budget never go quiet. `tokenops coach` lists
the record per kind.

A rung the coach cannot deliver yet is shown one rung lower, with the
reason. `tokenops coach` (or the `tokenops_coach` MCP tool) shows each
power's configured and effective rung and the setting it came from:

```bash
tokenops coach                         # the whole picture
tokenops coach autonomy autonomous     # every power's default
tokenops coach set models ask          # one power
tokenops coach verbosity quiet         # how much it says
tokenops coach off                     # records, says and does nothing
tokenops coach migrate                 # write a coach block from the keys below
```

Without a `coach` block, the older keys keep meaning what they meant:
`coaching.delivery` sets inform and waste, and
`optimizer.smart_routing` sets models. A `coach` key wins where present.
The coach's hooks never read `mode`; `coach off` is the coach's off switch.

### `mode` is about traffic; `coaching.delivery` is about your session

These are two axes, and they are deliberately separate. `mode` decides
whether TokenOps rewrites **traffic** — routing rules on the proxy, the
spend watcher. `coaching.delivery` decides how much of your **session**
the coach may take. Wanting live routing without a coach that interrupts
is a reasonable position, and so is the reverse.

`coaching.delivery` is a ladder graded by interference, each rung adding
a channel to the one below:

| Level | What the coach does |
|---|---|
| `observe` | Records everything, answers when asked — `tokenops coach prompts`, `coach replies`, `dx`, and the MCP coaching tools. The hooks stay installed but say nothing, while still keeping their ledgers: `coach-hook stats` and `read-guard stats` show what you are missing before you let them speak. |
| `advise` (default) | Observe, plus the coach speaks unprompted but never blocks — `coach-hook` nudges as session cost crosses a budget fraction. Advice you can ignore. |
| `intervene` | Advise, plus the coach acts — `read-guard` refuses a redundant re-read before it costs a token. |

The rungs are graded by interference rather than by who started the
exchange, because that is the question you actually have. "Does it speak
without being asked" would put a non-blocking nudge and a refused tool
call on the same rung, and those are not remotely the same imposition.

```bash
tokenops coach delivery              # print the current level
tokenops coach delivery intervene    # let the guard start blocking
tokenops coach delivery advise       # back it out
```

It takes effect immediately. The hooks resolve this on every invocation,
so there is nothing to re-install. `--mode` on `read-guard` still pins a
behaviour against the config when you want one hook to differ.

`advise` is the default because it is what the hooks already did before
this setting existed — upgrading changes nothing until you say so.

#### The coach argues for `intervene`; it never takes it

`read-guard` keeps a ledger of the redundant re-reads it *would* have
blocked. Once that evidence crosses the bar, the coach makes the case
during an ordinary session — with your own numbers and the one command
that acts on them:

```
tokenops: read-guard has watched ~397k tokens of redundant re-reads go by
across 22 sessions — 225 reads it would have refused, and did not.
`tokenops coach delivery intervene` lets it refuse them before they cost
anything.
```

Who pulls the trigger follows `coaching.delivery` itself rather than a
second knob, because it is the same question that ladder already answers:

| Level | What happens with the evidence |
|---|---|
| `observe` | Recorded. Nothing is said. |
| `advise` | The case is made, once per session. The decision stays yours. |
| `intervene` | Already there — the guard is refusing re-reads, so there is nothing to ask for. |

It never promotes itself. Flipping a hook from observing to refusing the
agent's reads is exactly what `intervene` exists to gate, and a tool that
crosses that line on its own has decided the question the gate was there
to ask. A guard that has already blocked something is never argued with
again, whether it got there by `coach delivery intervene` or a pinned
`--mode=active`.

#### `coaching.quiet` — how often the coach may speak

Every finding the coach can raise already latches: a budget tier fires
once per session and then stays quiet, and so does the read-guard case
above. `coaching.quiet` exists for the case a latch cannot see — two
*different* findings landing back to back, which reads as nagging however
reasonable each one was on its own.

The coach also says at most one thing per Stop, and the budget tier goes
first: it is about the session in flight, while the read-guard case will
be just as true next turn.

| Key | Meaning |
|---|---|
| `min_interval` | A floor between two proactive nudges in one session. A nudge held back by the floor is **deferred, not dropped**: its finding stays unlatched and speaks at the next opportunity past the floor. |
| `max_per_session` | A cap on how many proactive nudges one session may carry. Unlike the floor this **drops** — a cap that queues what it refused is not a cap. Zero means no cap. |

Both default to zero, which means the per-finding latches are the whole
policy — exactly what the hooks did before the key existed. It only bites
at `delivery: advise` and `intervene`, where something speaks at all;
neither knob touches anything you ask for, because a direct question is
never an interruption.

`coach-hook stats` counts what the policy held back, by rule:

```
  budget alerts fired: 2
  held back by coaching.quiet: 1
    min_interval    1
```

That line is the point. A rate limit you cannot see working is
indistinguishable from one that does nothing.

Mode, budgets, and routing rules are also editable through the MCP
server — `tokenops_mode`, `tokenops_budget_set`, and
`tokenops_routing_rule_set` write the same `config.yaml` the CLI verbs
manage (validated before every write). The daemon picks the changes up
on its next restart.

Setting `mode: active` via `tokenops_mode` also **ensures a daemon is
running** — active mode's interventions (live routing, spend watcher)
live in the daemon, so activating without one would be a silent no-op.
If no daemon answers on its advertised URL, one is started detached
with the active config (logs land next to `events.db` in
`daemon.log`); if one is already running, the response reminds you to
restart it so it picks up the new mode.

## Reference

```yaml
mode: passive                 # passive | active (see Operating modes)

listen: 127.0.0.1:7878        # bind address
log:
  level: info                 # debug | info | warn | error
  format: text                # text | json
shutdown:
  timeout: 15s                # graceful shutdown grace period

storage:
  enabled: true               # open the local sqlite store
  path: ~/.tokenops/events.db

retention:                    # opt-in; empty keep deletes nothing
  interval: 1h
  keep:                       # by event type
    prompt: 30d
    workflow: 90d
  keep_by_source:             # by reader; overrides the type window
    opencode: forever         # never prune this source
    codex-jsonl: forever
    read-guard: 7d            # may also be shorter than the type window

tls:
  enabled: false              # serve HTTPS with auto-minted cert
  cert_dir: ~/.tokenops/certs
  hostnames: []               # extra SANs

providers:                    # upstream URL overrides
  openai: https://api.openai.com
  anthropic: https://api.anthropic.com
  gemini: https://generativelanguage.googleapis.com

otel:
  enabled: false              # ship envelopes to an OTLP collector
  endpoint: http://localhost:4318
  headers:
    x-honeycomb-team: ...
  service_name: tokenops
  redact: true

pricing:
  path: ~/.tokenops/pricing.yaml  # optional rate overrides (see below)

model_policy:                 # models no route may land on (see below)
  allow: ["anthropic/*"]      # optional; when set, only these
  deny: ["*opus*"]            # always wins over allow

optimizer:
  routing_min_quality: 0.7    # skip rules below this quality floor
  smart_routing:              # decide per turn, with no rules (see below)
    enabled: false
    window_pct_above: 70      # conserve only once the window is this full
    quality: 0.75             # confidence a mechanical turn survives it
    models:                   # the models on offer; routing picks among these
      anthropic: [claude-opus-5-5, claude-sonnet-5, claude-haiku-4-5]
  routing_rules:              # model-routing optimizer (see below)
    - provider: anthropic
      from_model: "claude-fable-5*"
      to_model: claude-opus-4-8
      quality: 0.9
      fallbacks: [claude-sonnet-4-6]
  command_fmt:                # deterministic command-output compression (see below)
    default: balanced         # conservative | balanced | aggressive
    overrides:                # per-command loss level
      kubectl: aggressive
    emit_events: false        # append command_fmt OptimizationEvents to the store
    formatters:               # user-defined formatters (no recompile)
      - command: mytool
        critical: ["(?i)error", "FAILED"]
        drop:
          balanced: ["^DEBUG ", "^TRACE "]
          aggressive: ["^INFO "]

coaching:
  delivery: advise            # observe | advise | intervene
  quiet:                      # rate-limits the *proactive* channel only
    min_interval: 30m         # floor between two nudges in one session
    max_per_session: 3        # cap; 0 defers to the per-finding latches
  context_limits:             # waste-detector threshold overrides
    - workflow_prefix: "claude-code:"
      max_context_tokens: 500000
      context_growth_limit_tokens: 1000000

budgets:                      # evaluated by the active-mode watcher
  - name: weekly-all
    window: weekly            # daily | weekly | monthly (calendar, UTC)
    limit_usd: 50
    warn_at: 0.75             # optional; fraction of limit_usd
    crit_at: 0.95             # optional
    basis: spend              # spend (real billed cost, default) |
                              # equivalent (API list-price value incl.
                              # plan-covered usage — use on flat plans)
    # workflow_id / agent_id optionally scope the limit

watch:
  interval: 15m               # watcher cadence (default 15m, min 1m)
```

## Model routing rules

`optimizer.routing_rules` feeds the model-routing optimizer used by
`tokenops replay` and the `tokenops_replay` MCP tool. Each rule says
"traffic asking for `from_model` could run on `to_model`"; `quality` is
your confidence (0–1] that the cheaper model preserves task quality.
Replay then reports what each rule would have saved on real history:

```
Model routing opportunities:
  - route claude-fable-5 -> claude-opus-4-8: 124 requests, would save $9.43
```

Replay never resends traffic upstream — routing rules are evaluated
offline against the local event store, so you can validate a rule's
savings before changing any client configuration.

With `mode: active`, the same rules are **enforced on live proxied
traffic**: the proxy rewrites the request's model before forwarding,
logs the intervention, and records an applied optimization event
(visible in MCP/CLI results and `tokenops replay`). The observation keeps
the originally requested model, so you can always audit what clients
asked for versus what was served. Routing never breaks a request — any
parse failure forwards the original body untouched.

## Smart routing (`optimizer.smart_routing`)

A routing rule is a decision you made once, in advance, about a pair of
model names. That is a reasonable thing to be *able* to write and a poor
thing to *have* to write: the rule cannot see the turn it is deciding
about, so it is either too broad to be safe or too narrow to fire, and it
goes stale the moment a model is renamed.

`smart_routing` is the same decision made per turn, from signal that is
actually measured:

| Signal | Where it comes from |
|---|---|
| What kind of work this turn is | the task classifier — a terse instruction over mostly tool traffic reads as mechanical; a long specifying one, or one that rejects the last answer, reads as reasoning |
| How full the plan's rate-limit window is | the vendor's own meter where one exists, the plan-consumption heuristic otherwise |
| What is cheapest right now | the live pricing table, not a model name written into config |

Nobody writes a `from`/`to` pair, so nobody has to maintain one. A
configured rule always wins where one matches; the policy only decides
turns no rule covers.

**It is deliberately narrow, and every narrowing is deliberate:**

- **Downwards only.** The preferred-model ceiling outranks it, so it can
  never raise a bill.
- **Mechanical work only.** Routing a reasoning turn down is the quality
  trade you did not ask for. When the classifier abstains, so does the
  policy.
- **Only while the window is tight.** There is no "conserve always"
  setting. On a flat-rate plan a request costs nothing at the margin, so
  routing down while you have headroom trades quality for a saving that
  does not exist.
- **An unmeasured window is not a full one.** If the meter is not
  reporting, the policy stays put and says so. This meter read 0/200 for
  months on a real machine; acting on that would have degraded quality to
  relieve a shortage that was not happening.

Cheapest is not the same as best, and that is the honest limit of a
policy with no rules in it. List the models actually on offer under
`smart_routing.models` and it picks only among them; otherwise it ranks
the whole rate card, retired models included. Rule out anything you would
never want with `model_policy`, and if you want a specific target, write
a rule.

### Where routing bites, and the way round it

Enforcement means rewriting the model in the request, which needs the
proxy: `mode: active` plus a base-URL override. Most operators never wire
that, and on those machines the optimizer holds an opinion nobody can
hear.

So the same policy is also reachable as **advice**, through the MCP tool
`tokenops_routing_advise`. An agent hands it the instruction it is about
to act on and the model it would otherwise use, and gets back `stay` or
`switch` with the reason, the class, and the window reading. It
recommends and never applies — the model stays the caller's choice, and
ultimately yours.

That path needs no proxy and no base-URL override, which makes it the one
routing surface available on every client that speaks MCP. It runs the
same policy the request path runs, deliberately: advice that disagreed
with enforcement would be worse than no advice, because you would be
surprised twice.

## Model policy (`model_policy`)

A person or a company may rule models out: a vendor with no agreement
in place, a model too expensive for the team, one that has not passed
review. `model_policy` is two lists of patterns that every routing
decision honours — routing rules and smart routing on the proxy,
`tokenops_routing_advise`, the coach's advice, and its subagent moves:

```yaml
model_policy:
  allow: ["anthropic/*", "openai/gpt-5*"]
  deny: ["*opus*"]
```

- **Deny always wins.** A model matching any deny pattern is never a
  target.
- **An allow list is exclusive.** When it is set, only the models it
  names are targets; when it is empty, everything not denied is.
- **Patterns** match the bare model id (`claude-opus-5`) and the
  provider-qualified one (`anthropic/claude-opus-5`), ignoring case.
  `*` matches any run of characters, `/` included; `?` matches one. So
  `openai/*` rules out a vendor and `*opus*` a family wherever it is
  served.

A rule whose target is ruled out falls through to its first permitted
fallback, and routes nowhere when there is none. `tokenops coach status`
shows the policy in force.

A request that *asks* for a forbidden model is moved off it. This holds
whatever the coach's dials say, because it is your rule, not the coach's
advice:

- **Claude Code subagents** (needs `tokenops hooks install --route-guard`)
  run on the permitted model closest in tier to the one requested: the
  same tier first, then the nearest below, then the nearest above. When
  the Agent tool can name none, the call is refused with the permitted
  models listed, so the agent can choose again.
- **Requests through the proxy** go to the permitted model in
  `smart_routing.models` closest in price. `optimizer.mode` still decides
  whether that is observed, proposed, or applied. Without
  `smart_routing.models` there is nothing safe to move to, and the
  request is reported, not guessed at. The proxy never fails a request
  over the policy.

A hook cannot change a session's own model, so a forbidden session model
is only enforced on its subagents and on proxied requests. See
[ADR 0007](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0007-model-policy.md).

A company that wants the same policy on every machine ships it in the
configuration file through its device management, like any other
setting.

## Command-output compression (`command_fmt`)

`optimizer.command_fmt` configures `tokenops fmt` — the deterministic
compressor that shrinks a shell command's stdout **before** it enters an
agent's context, keeping every line the formatter classifies as critical
(errors, failures, changed state) and dropping noise. 46 commands ship
built in (git, go/pytest/jest/vitest/rspec/playwright, npm/pip/uv/…,
mvn/gradle/bazel/dotnet/cmake/…, docker/kubectl/helm, terraform/pulumi/
ansible, aws/gcloud/az, and more).

- `default` — loss level for any command without an override:
  - `conservative` — strip only unambiguous noise (ANSI, blank runs,
    duplicate lines). Nothing semantic dropped.
  - `balanced` (recommended) — also drop command-specific noise
    (progress, banners, up-to-date chatter).
  - `aggressive` — additionally collapse repetitive state (e.g.
    "+142 unchanged files") into a summary.
- `overrides` — per-command loss level (a noisy command can be dialed
  up without loosening the global default).
- `emit_events` — append a `command_fmt` OptimizationEvent per compressed
  run so telemetry and scorecard count the savings.

Two invariants hold at **every** level, for built-in and user formatters
alike: **determinism** (pure function of input + level) and
**critical-line survival** (a formatter that would drop a critical line
falls back to the raw output instead). The full output is always kept in
`~/.tokenops/recovery/` for retrieval.

### User-defined formatters

`command_fmt.formatters` extends or overrides the catalog for any command
**without recompiling**. Declare the regexes that mark critical lines
(always preserved) and the noise regexes to drop per loss level:

```yaml
optimizer:
  command_fmt:
    formatters:
      - command: mytool
        aliases: [mt]
        critical: ["(?i)error", "FAILED", "^\\s*modified:"]
        drop:
          balanced:   ["^DEBUG ", "^\\s*at "]   # dropped at balanced + aggressive
          aggressive: ["^INFO "]                 # dropped at aggressive only
```

User rules run through the same critical-line guard as built-ins, so an
overly broad drop rule can never remove a line you marked critical — it is
preserved and the formatter falls back safely.

`tokenops fmt learn` mines usage telemetry to suggest which commands need a
formatter and which are over-compressing; `tokenops fmt learn --apply`
writes the safe loss-level tuning back to this config locally. The
`tokenops_fmt_learn` MCP tool exposes the same report to agents. See the
[CLI reference](./cli.md#command-output-compression-fmt).

## Budgets and the spend watcher

`budgets` define calendar-window (UTC) spend limits. In `mode: active`
the daemon evaluates them every `watch.interval` against actual spend
plus a Holt forecast for the remainder of the window, logging
`threshold_reached` and `forecast_breach` alerts (deduplicated per
window) and publishing `budget.exceeded` domain events. The watcher
also flags models missing from the pricing table. In passive mode
budgets are inert — define them ahead of time and flip the mode when
you want enforcement-grade visibility.

On flat-rate plans (Claude Max, ChatGPT Plus) real spend is ~$0 — a
`basis: spend` budget guards nothing. Use `basis: equivalent` to watch
the **API-equivalent value** instead: what the window's usage would
have billed at list prices, including plan-covered traffic. The same
figure appears as `api equivalent` in `tokenops spend` and
`api_equivalent_usd` in the `tokenops_spend_summary` MCP tool.
Equivalent-basis budgets get threshold alerts only (no forecast).

Plan-covered traffic is identified centrally: every event written to
the store for a provider with a plan bound in `plans:` is stamped
`plan_included` at the storage sink, regardless of which poller or
proxy produced it.

## Waste-detector context limits

The workflow waste detector (`tokenops replay --workflow`, the
`tokenops_workflow_trace` MCP tool)
ships built-in thresholds per workflow type: `claude-code:` sessions
flag context above 900k tokens, `codex:` above 250k, everything else
above 32k. `coaching.context_limits` overrides them per workflow-ID
prefix — a matching entry replaces the built-in profile, and fields you
omit keep the detector defaults:

```yaml
coaching:
  context_limits:
    - workflow_prefix: "claude-code:"
      max_context_tokens: 500000          # flag earlier than the 900k default
      context_growth_per_step_tokens: 3000  # mean growth per step (default 5k)
      max_consecutive_agent_loops: 4
      system_redundancy_min: 3
      compact_at_tokens: 400000           # compact_earlier line (default 600k)
```

**Compacting late** (`compact_earlier`) reports a session that ran 20 or
more steps in a row above `compact_at_tokens` without compacting: 600k for
`claude-code:`, 150k for `codex:`, off elsewhere. It prices the stretch
against compacting at that line, where context grows back and is compacted
again and so averages halfway between the session's post-compaction size
and the line. The excess is each step's context above that average, at API
rates; on a flat plan it is the share of the plan's allowance those steps
used. An entry that omits `compact_at_tokens` keeps the built-in line.

Context growth is judged **per step** by default: 5k tokens per step for
`claude-code:`, 15k for `codex:`, and 8k elsewhere, over sessions of at
least 10 steps. A long agent session grows a steady amount per turn and
compacts now and then, so a limit on its total growth flags every session
past a certain length. `context_growth_limit_tokens` still sets a total
limit for a prefix that wants one; setting it switches that prefix to the
total rule.

The repeated-agent-loop check (`max_consecutive_agent_loops`) applies only
to workflows with more than one agent. In a single-agent session every turn
belongs to the same agent, so a consecutive-run count is the session
length; repetition there is caught by the identical-prompt check instead.

## Automatic rate-card refresh

A rate card goes stale on its own. A model released after your binary was
built prices at **zero**, so a session that cost real money reports as
free — the exact failure this tool exists to find. So the daemon refreshes
the card itself rather than waiting for someone to remember
`tokenops pricing refresh`.

**This is one of two outbound calls tokenops makes on its own** (the other
is the [exchange rate](#switching-plans), only when your currency is not
USD). It fetches a
public rate card (LiteLLM's `model_prices_and_context_window.json`) and
sends nothing: no prompt, no file, no identifier, no usage figure. The
privacy claim is about content and no content is involved — but a tool
that starts talking to the network without saying so has spent trust it
cannot buy back, which is why it is documented here and switchable:

```yaml
pricing:
  refresh:
    disabled: true      # off
    interval: 24h       # default 24h, floor 1h
```

Three things about how it behaves:

- **It applies what it fetches.** The spend engine is built once at
  startup, so a refresh hands the new cards to the *running* engine rather
  than leaving them on disk for the next restart. A refresh that writes a
  snapshot the live process never reads is a no-op wearing the clothes of
  an update.
- **It does not write when nothing changed.** Rate cards move on the order
  of weeks; a daily unconditional write would leave a year of
  near-identical snapshots and make `pricing diff` useless. A separate
  marker records that a check happened, so a no-change check still counts
  and a restart does not re-fetch.
- **It fails quiet and soft.** No network, a bad payload, a source outage:
  it logs and keeps the card already in force. Pricing is never the reason
  ingestion stops.

Your `verified: true` rows and your `pricing.path` overrides both outrank
anything fetched, so a refresh cannot regress a rate you hand-checked or
one you negotiated.

## Pricing overrides

TokenOps ships an embedded list-price catalog (USD per million tokens)
used to cost every request. When a vendor releases a model the catalog
doesn't know yet, `tokenops spend` and the `tokenops_spend_summary` MCP
tool flag it:

```
⚠ no pricing for 1 model(s) — total spend is underestimated:
    anthropic/claude-fable-5[1m] (213 requests)
```

Add a rate without waiting for a TokenOps release — or apply negotiated
rates — via `pricing.path`. The file layers on top of the built-in
catalog; fields you omit inherit from the matching built-in row:

```yaml
# ~/.tokenops/pricing.yaml
currency: USD
rates:
  anthropic:
    "claude-fable-5*":            # trailing * = prefix match, covers
      input_per_million: 10.00    #   suffixed variants like [1m]
      output_per_million: 50.00
      cached_input_per_million: 1.00
```

## mDNS (`mdns`)

The daemon can advertise itself as `tokenops.local` so local API clients have a
memorable URL. By default it advertises **only when the LAN can actually
reach it** — on the default loopback bind the record would point at
`127.0.0.1`, which no peer can use, while the machine's hostname still went
out on every interface.

```yaml
mdns:
  enabled: true              # force on or off; omit to follow the bind
  instance_name: workstation # replaces the hostname in the advertised name
```

`instance_name` is for operators who want `tokenops.local` without
publishing what their laptop is called.

## Plan limits (`plan_limits`)

Figures only you can supply, for plans whose limit this tool cannot know.
A spend-denominated plan — usage-based Enterprise — is billed at API rates
from the first token, so headroom is measured against the org spend limit
your admins set in the vendor console.

```yaml
plans:
  anthropic: claude-enterprise
plan_limits:
  anthropic:
    spend_limit_usd: 5000
    window: monthly     # monthly (default), weekly or daily
    rate_factor: 0.8    # optional: scale to a negotiated rate
```

These figures are the fallback. When Claude subscription telemetry is set up
(`tokenops vendor-usage setup claude-subscription`), Anthropic reports both
the month's spend and the limit, and headroom uses those instead — what
the vendor bills outranks a recomputation from a rate card, so neither
`spend_limit_usd` nor `rate_factor` applies to them.

Binding such a plan with neither the meter nor `spend_limit_usd` is refused
rather than defaulted: a percentage measured against a number nobody chose
reads exactly as authoritative as a real one.

`rate_factor` exists because enterprise rates are frequently negotiated
off list while TokenOps costs from the public rate card. Without it your
console limit is compared against an overstatement, and the error is
invisible.

Unlike a subscription, a spend-denominated plan covers nothing: its usage
is recorded as billed and priced at API rates, so `spend` shows what it
costs and headroom counts it against the limit. Before v0.86.1 such usage
was recorded as covered, at $0, which read the limit as 0% used. To
correct usage recorded that way, re-bind the plan from the date it took
effect:

```bash
tokenops plan set anthropic claude-enterprise --spend-limit 500 --since 2026-09-01
```

## Switching plans

`plans:` holds the plan you are on now. TokenOps also keeps a history of
switches in `~/.tokenops/plan-history.jsonl`, so reports over a past
period use the plan that was in force then.

```bash
tokenops plan set openai gpt-pro-5x                     # from now on
tokenops plan set openai gpt-pro-5x --since 2026-09-01  # you were on it since then
tokenops plan history                                   # list the switches
tokenops plan catalog                                   # plans and their list prices
```

TokenOps decides whether a call was covered by a plan when it records the
call. Usage recorded while no plan was set counts as billed per token,
and setting a plan later does not change that. `--since` is how you
correct it: it re-marks that provider's billed usage since the date as
plan-covered and tells you how many calls it changed. Trial usage and
other providers are left alone, and the change is written to the audit
log. `plan unset` is never backdated, because that would turn covered
usage into charges the vendor may never have made.

`tokenops spend` prices the plans in force over its window, prorated by
day across switches, and shows the list-price value of your usage per
unit of plan cost. It uses what you told it you pay, and the catalog's
US list price (monthly billing, before tax) where you gave nothing.
Per-seat plans are priced for one seat, and a plan with no flat price,
such as Enterprise, is left out and flagged.

Record what your bill says, in your currency, with `--price`:

```bash
tokenops plan set anthropic claude-max-20x --price 214.60 --currency EUR
```

```yaml
money:
  currency: EUR      # set by `tokenops init` from your system region
  # per_usd: 0.88    # optional: pin a rate instead of the ECB's
  # fetch_rate: false  # optional: never fetch the ECB rate
```

Every total `tokenops spend` prints is in your currency, with the dollar
figure beside the summary lines and the rate named underneath:

```
  api equivalent:  9397.12 EUR (10670.04 USD) (plan-covered usage at list price)
  plans:           242.96 EUR (your prices, prorated)
  value per plan EUR: 38.7x (api equivalent / plans)
  rate:            1 EUR = 1.1355 USD (ECB reference rate, 2026-09-30); converted amounts move with it
```

Usage is priced in US dollars, because vendors publish rate cards in
dollars, so a euro figure moves with the exchange rate even when your usage
does not. The rate is the European Central Bank's daily reference rate,
fetched at most once a day and cached in `~/.tokenops/fx-ecb.json`. The fetch
downloads a public file and sends nothing. If it fails, the last cached rate
is used and its date says how old it is. Pin `per_usd` to use a rate of your
own, or set `fetch_rate: false` to keep this off the network.

`tokenops init` records your currency once, from `--currency` or your
system region. On macOS it reads the region setting, so an English-language
system in Germany is billed in euros. See
[ADR 0008](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0008-plan-history.md).

## Environment variables

| Variable                          | Maps to                       |
|-----------------------------------|-------------------------------|
| `TOKENOPS_LISTEN`                 | `listen`                      |
| `TOKENOPS_LOG_LEVEL`              | `log.level`                   |
| `TOKENOPS_LOG_FORMAT`             | `log.format`                  |
| `TOKENOPS_SHUTDOWN_TIMEOUT`       | `shutdown.timeout`            |
| `TOKENOPS_TLS_ENABLED`            | `tls.enabled`                 |
| `TOKENOPS_TLS_CERT_DIR`           | `tls.cert_dir`                |
| `TOKENOPS_STORAGE_ENABLED`        | `storage.enabled`             |
| `TOKENOPS_STORAGE_PATH`           | `storage.path`                |
| `TOKENOPS_OTEL_ENABLED`           | `otel.enabled`                |
| `TOKENOPS_OTEL_ENDPOINT`          | `otel.endpoint`               |
| `TOKENOPS_OTEL_SERVICE_NAME`      | `otel.service_name`           |
| `TOKENOPS_PROVIDER_OPENAI_URL`    | `providers.openai`            |
| `TOKENOPS_PROVIDER_ANTHROPIC_URL` | `providers.anthropic`         |
| `TOKENOPS_PROVIDER_GEMINI_URL`    | `providers.gemini`            |
| `TOKENOPS_PRICING_PATH`           | `pricing.path`                |
