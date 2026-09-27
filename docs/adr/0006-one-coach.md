# ADR 0006 — The coach is one capability with three powers

- **Status:** Proposed
- **Date:** 2026-09-27
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0001 (coaching hooks), ADR 0004 (authority ladder,
  Phase 5), ADR 0005 (earned digest), PRs #443 (quota nudges), #444
  (read-guard authority report)

## Context

What an operator wants from TokenOps's coach is one thing they can
configure: it tells them what helps them work better, it stops waste, and
it can move work to a better-fitting model. Measured against that on the
maintainer's machine on 2026-09-27:

**The coach exists as three features behind six settings.**

| Power the operator wants | Where it lives | Setting |
|---|---|---|
| Inform: nudges in the session | `coach-hook` (Stop) | `coaching.delivery` |
| Stop waste: refuse a redundant re-read | `read-guard` (PreToolUse) | `coaching.delivery` (`intervene`) |
| Advise or delegate a cheaper model per turn | `route-guard` (UserPromptSubmit) | `optimizer.smart_routing.{enabled,intervention,auto_kinds,models}` |
| Rewrite the model on live requests | the proxy | `mode`, `optimizer.mode`, `optimizer.routing_rules`, `preferred_models`, routing approval |

The ADR 0004 authority report describes these in one vocabulary, but it
describes rather than simplifies. Until #444 it misreported the one
subsystem actually intervening (read-guard: 255 re-reads refused while
reported observe-only).

**Two documents disagree about the kill switch.** ADR 0004 says the
daemon's `mode` caps every subsystem, so `mode: passive` silences the coach.
The configuration guide says `mode` governs traffic and `coaching.delivery`
governs the session, "deliberately separate". The hooks follow the guide:
none of them reads `mode`. The report follows ADR 0004. On a passive
machine the report says the coach is held back while it nudges and blocks.

**What can switch a model depends on the path, not the setting.**
None of the hook interfaces TokenOps uses can change the session's model.
On the hook path the strongest move is delegation: marking work the agent
may hand to a subagent on a cheaper model, where the client supports
subagents on a chosen model (Claude Code does). Only the
proxy rewrites the model of a request, and only for traffic routed through
it; the maintainer routes none, by choice.

**Evidence of effect is uneven.** Read-guard's effect is measured (about
1.2M tokens refused). The route guard argued for a cheaper model in 13 of 53
sessions; nothing records whether anyone followed it. Before #443 the
coach-hook's main output was 556 dollar alerts across 80 sessions on a flat
plan, about money that was never charged.

## Decision

The coach is **one capability with three powers**, each a rung on the ADR
0004 ladder, configured in one place and reported with what it can actually
do on this machine.

1. **One `coach` block, three powers.**
   - `coach.inform`: `observe | advise`. Speaks about the plan's quota
     window and context fullness in the session (#443), and later the
     earned digest (ADR 0005).
   - `coach.waste`: `observe | advise | act`. `act` lets read-guard refuse
     a redundant re-read; future waste interventions join this power.
   - `coach.models`: `observe | advise | delegate | switch`.
     - `advise` states the case.
     - `delegate` marks work the agent may hand to a cheaper subagent.
     - `switch` rewrites the model on requests through the proxy.

     Existing invariants hold at every rung: never route to a pricier model
     than the operator chose (a spending decision nobody delegated), and
     `switch` stays outcome-gated per ADR 0004.
2. **One dial over them.** `coach.level: quiet | advise | act` is a preset:
   - `quiet`: every power `observe`.
   - `advise`: every power `advise`.
   - `act`: `inform: advise`, `waste: act`, `models: delegate`.

   `switch` is never set by a preset. It needs the proxy and
   outcome evidence, so it is named explicitly. A power set on its own
   overrides the preset.
3. **Configured, effective, and why.** Each power reports what was
   configured, what it can do here, and the reason when those differ. For
   example: `models: switch → delegate, because no traffic reaches the
   proxy`, judged from whether proxy-sourced events arrived recently, not
   from config alone; or `models: delegate → advise, because this client
   cannot run a subagent on a chosen model`.
4. **The daemon's `mode` stops being a coaching control.** `mode` governs
   traffic (proxy routing and the budget watcher), as the configuration
   guide says and as the hooks already behave. The coach's kill switch is
   `coach.level: quiet` (`tokenops coach quiet`), which the hooks read on
   every invocation. The authority report stops capping hook-driven powers
   by the daemon's mode, so it describes what actually happens. ADR 0004's
   cap remains for daemon-side interventions (proxy routing, the watcher).
5. **Old settings keep working.** With no `coach` block, `coaching.delivery`
   maps to `inform` and `waste` (as today), and
   `optimizer.smart_routing.{enabled,intervention}` maps to `models`. A
   `coach` key, when present, wins. `tokenops coach` shows which key each
   power came from, and `tokenops coach migrate` writes the equivalent
   `coach` block. Nothing changes for an existing config until the operator
   says so.
6. **One surface.** `tokenops coach` and the `tokenops_coach` MCP tool show
   the three powers (configured, effective, reason, source key) and what each
   did recently: nudges spoken, reads refused, work delegated or switched.
   `tokenops coach set <power> <rung>` changes one power, and
   `tokenops coach level <preset>` changes the dial.
7. **Every intervention records whether it was followed.** The effect of a
   refused read is self-evident; advice is not. The coach records:
   - after a context or quota nudge, whether the session compacted, ended,
     or changed model within the next few turns;
   - after model advice, whether the following turns ran on the suggested
     tier or were delegated.

   This is the action rate ADR 0005 requires before a finding earns the
   digest, applied to the live coach. Advice a kind of operator
   consistently ignores goes quiet for that kind. The threshold is set from
   the recorded evidence, not in advance.

## Alternatives considered

- **Keep the settings; document them better.** The configuration guide
  already documents them well, and the report already unifies their
  vocabulary. The operator still has to know that three features and six
  keys make one coach.
- **Let `mode: passive` silence the hooks, as ADR 0004 intended.** A
  coherent choice, but it couples every hook to daemon configuration, and it
  makes "passive traffic, active coach", a position the guide calls
  reasonable, impossible to express. A kill switch that the hooks honour
  directly (`coach.level: quiet`) is simpler and already how they work.
- **Automatic model switching on the hook path.** Not possible through the
  hook interfaces TokenOps uses: none can change the session's model. Delegation is the strongest honest
  rung there, and the report says so rather than implying a switch.
- **A single `coach.level` with no per-power settings.** Simpler, but
  "stop waste, but only advise on models" is a common and reasonable
  position that a single dial cannot express.

## Consequences

- A `coach` config block, validated at load and covered by the config
  matrix, with the preset and three powers.
- `capability/authority` reports the coach's powers as the product
  presents them, and daemon-side interventions separately.
- The hooks read the `coach` block, falling back to the old keys.
- A follow-through ledger per nudge and per piece of model advice, which
  ADR 0005's promotion bar and the live coach's quieting both read.
- The configuration guide and ADR 0004's execution notes are updated to
  one account of the kill switch.

## Open questions for acceptance

- Should `coach.level: act` include `models: delegate` by default, or stop at
  `advise` until follow-through evidence shows delegation is used well?
- Should `inform` gain an `act` rung later (for example, pausing a session
  at an exhausted window), or is interrupting always a hook's advice and
  never the coach's action?
