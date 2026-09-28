# ADR 0006 — One coach: who decides, and how much it says

- **Status:** Proposed
- **Date:** 2026-09-28
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0001 (coaching hooks), ADR 0004 (authority ladder,
  Phase 5), ADR 0005 (earned digest), PRs #443 (quota nudges), #444
  (read-guard authority report)

## Context

An operator wants one coach they can configure along two questions they
actually ask: **who decides** when the coach sees something worth changing,
and **how much it tells them**. Measured against that on the maintainer's
machine on 2026-09-27:

**The coach exists as three features behind six settings.**

| What the operator wants | Where it lives | Setting |
|---|---|---|
| Tips in the session | `coach-hook` (Stop) | `coaching.delivery` |
| Stop waste: refuse a redundant re-read | `read-guard` (PreToolUse) | `coaching.delivery` (`intervene`) |
| Advise or delegate a cheaper model per turn | `route-guard` (UserPromptSubmit) | `optimizer.smart_routing.{enabled,intervention,auto_kinds,models}` |
| Rewrite the model on live requests | the proxy | `mode`, `optimizer.mode`, `optimizer.routing_rules`, `preferred_models`, routing approval |

The settings mix the two questions. `coaching.delivery` decides both
whether the coach speaks and whether it blocks; `coaching.quiet` rate-limits
speech; `smart_routing.intervention` decides delegation. The ADR 0004
authority report describes them in one vocabulary but misreported the one
subsystem actually intervening until #444.

**Two documents disagree about the kill switch.** ADR 0004 says the
daemon's `mode` caps every subsystem; the configuration guide says `mode`
governs traffic and `coaching.delivery` the session, "deliberately
separate". The hooks follow the guide and never read `mode`.

**What the clients let a hook do** (Claude Code hooks reference,
code.claude.com/docs/en/hooks.md, checked 2026-09-28):

- `PreToolUse` may return `permissionDecision: "ask"` besides `allow` and
  `deny`: an approval point exists, but only at a tool call. The prompt's
  exact presentation, whether the reason is shown to the user, and headless
  behaviour are not documented.
- `PreToolUse` can match the subagent tool (`Agent`) and rewrite a tool's
  input (`updatedInput`). Whether the subagent's model is an input a hook
  can read or change is not documented.
- `UserPromptSubmit` can block or rewrite a prompt; it has no documented
  approval prompt. No hook interface TokenOps uses can change the session's
  own model. A `PreModelSwitch` event is listed as able to block a switch;
  its inputs and outputs are not documented.
- Only the proxy rewrites the model of a request, and only for traffic
  routed through it; the maintainer routes none, by choice.

**Evidence of effect is uneven.** Read-guard refused 255 re-reads (about
1.2M tokens). The route guard argued for a cheaper model in 13 of 53
sessions; nothing records whether anyone followed. Before #443 the coach
sent 556 dollar alerts across 80 sessions on a flat plan.

## Decision

The coach is one capability configured by **two independent dials**:
`autonomy` (who decides) and `verbosity` (how much it says).

```yaml
coach:
  autonomy: advise     # off | advise | ask | autonomous
  verbosity: normal    # quiet | normal | verbose
  # optional per-power autonomy overrides:
  # powers: { waste: autonomous, models: ask }
```

### 1. Autonomy: who decides

Each rung is a rung of the ADR 0004 ladder.

| `autonomy` | Ladder | The coach… |
|---|---|---|
| `off` | observe only | records what it would say or do, and does neither |
| `advise` | recommend | tells the operator what they could do better; changes nothing |
| `ask` | require approval | steps in with a concrete change and waits for the operator's approval |
| `autonomous` | automatic | makes the change itself |

It applies to three powers, with one default and optional per-power
overrides (`coach.powers.<power>`):

| Power | advise | ask | autonomous |
|---|---|---|---|
| **inform**: quota window, context fullness (#443) | the tip | the tip, proposing the next action | the tip; any action goes through the other powers |
| **waste**: redundant re-reads | "this re-read costs ~Nk tokens" | `PreToolUse: ask`: the operator approves the re-read | `PreToolUse: deny` with the reason to the agent |
| **models**: better-fitting model for the work | "this is research; Sonnet fits" | the agent is told the work may move; launching a subagent on the cheaper model goes through `PreToolUse: ask` on `Agent`; on the proxy, a routing proposal awaits `tokenops routing decide` | the agent is told to delegate; where the hook can set the subagent's model it does; on the proxy the route applies, outcome-gated |

Invariants at every rung: never move work to a pricier model than the
operator chose (a spending decision nobody delegated), and proxy switching
stays outcome-gated per ADR 0004.

**Effective, not just configured.** Each power reports the rung it can
actually reach here and why when that is lower. For example:
`models: autonomous → ask, because this client cannot set a subagent's
model`, or `models: autonomous → advise, because no traffic reaches the
proxy and the client has no subagent hook`. The judgment comes from
evidence (recent proxy-sourced events, the client's hooks, verified client
capabilities), not config alone.

### 2. Verbosity: how much the coach says

| | `quiet` | `normal` (default) | `verbose` |
|---|---|---|---|
| Quota / context | only when work is about to stop (90–100% of a window, or the pace runs out before the reset) | each tier once per window, with reset time and one tip | every tier, pace, context fullness, reasoning |
| Actions taken (`autonomous`) | silent; recorded, shown by `tokenops coach` | one line: what moved | what, why, and how to undo |
| Model advice (`advise`) | only large mismatches | once per kind of work per session | every time, with the tier reasoning |
| Approval requests (`ask`) | **always shown** | always shown | always shown, with reasoning |

Approval requests are exempt from `quiet`: asking is what `ask` is for, and
a silent ask is a silent "never". `coaching.quiet` (minimum interval,
per-session cap) becomes part of `normal`; explicit values still override.

### 3. The kill switch

`mode` governs traffic (proxy routing, the budget watcher), as the
configuration guide says and the hooks already behave. The coach's kill
switch is `coach.autonomy: off` (`tokenops coach off`), which every hook
reads on each invocation. The authority report stops capping hook-driven
powers by the daemon's mode; ADR 0004's cap remains for daemon-side
interventions.

### 4. Compatibility

With no `coach` block, the old keys map in:

- `coaching.delivery`: observe maps to `off`, advise to `advise`, intervene
  to `autonomous` for waste and `advise` for inform.
- `optimizer.smart_routing`: off maps to `off`, advise to `advise`,
  delegate to `ask`, auto to `autonomous`, for models.
- `coaching.quiet` maps into the `normal` rate limits.

A `coach` key wins when present. `tokenops coach` shows each power's source
key, and `tokenops coach migrate` writes the equivalent block. Nothing
changes for an existing config until the operator says so.

### 5. One surface

`tokenops coach` and the `tokenops_coach` MCP tool show both dials, each
power's configured and effective rung with the reason, its source key, and
what it did recently: tips spoken, approvals asked and answered, reads
refused, work delegated or switched.

- `tokenops coach autonomy <rung>` sets the default autonomy.
- `tokenops coach verbosity <level>` sets how much it says.
- `tokenops coach set <power> <rung>` overrides one power.

### 6. Follow-through

Every intervention records whether it was followed:

- a tip: did the session compact, end, or change model within the next few
  turns;
- advice: did the following turns run on the suggested tier;
- an approval request: was it approved or declined;
- an autonomous action: did it stand or was it undone.

This is ADR 0005's action rate applied to the live coach. Advice
consistently ignored for a kind goes quiet for that kind, on thresholds set
from the recorded evidence.

### 7. Verify before building

Two capabilities the design leans on are documented only in part. A spike
confirms each in a real Claude Code session before its rung ships:

1. What `PreToolUse: ask` shows the operator (the reason, the tool input),
   and what happens headless.
2. Whether the `Agent` tool's input carries a model a hook can read and set
   via `updatedInput`.

Until (2) is confirmed, `models: autonomous` on the hook path reports
itself as `ask`. Until (1) is confirmed, `ask` on the hook path is
implemented as advice that asks the agent to seek approval, and reports
itself that way.

## Alternatives considered

- **One dial for both.** Autonomy and talkativeness are independent: "act
  on your own and tell me what you did" and "only advise, and only when it
  matters" are both reasonable. One dial forces a pairing.
- **Keep the six settings; document them better.** The guide already
  documents them well. The operator still has to learn that three features
  and six keys are one coach.
- **Let `mode: passive` silence the hooks, as ADR 0004 intended.** It
  couples every hook to daemon configuration and makes "passive traffic,
  active coach" inexpressible. `coach.autonomy: off` is the kill switch the
  hooks already know how to honour.
- **Automatic switching of the session's own model.** No hook interface
  TokenOps uses can do it. The strongest moves are delegation (hook path)
  and request rewriting (proxy), and the report says which applies.

## Consequences

- A `coach` config block (`autonomy`, `verbosity`, `powers`), validated at
  load and covered by the config matrix.
- `capability/authority` reports the coach's powers as the product presents
  them, and daemon-side interventions separately.
- The three hooks read the `coach` block, falling back to the old keys;
  read-guard gains the `ask` rung; the route guard gains an `Agent`-tool
  approval path once the spike confirms it.
- A follow-through ledger that ADR 0005's promotion bar and the live
  coach's quieting both read.
- The configuration guide and ADR 0004's execution notes are updated to one
  account of the kill switch.
