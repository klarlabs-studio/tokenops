# ADR 0007 — Model policy: models the owner rules out

- **Status:** Accepted 2026-09-30
- **Date:** 2026-09-30
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (authority ladder), ADR 0006 (one coach),
  PR #463 (policy in every routing decision)

## Context

People and companies rule models out: a vendor with no agreement in
place, a model too expensive for the team, one that has not passed
review. TokenOps picks models in four places: the proxy's routing rules,
proxy smart routing, `tokenops_routing_advise`, and the coach's advice and
subagent moves. None of them could be told "never this model".

- The coach stayed inside `optimizer.smart_routing.models` by accident. That
  list was meant as the menu of models on offer, not a set of permissions.
- Proxy smart routing ranked the whole rate card, including retired models.
- Nothing stopped an agent from asking for a forbidden model itself.

## Decision

1. **One policy, two lists.** `model_policy.allow` and `model_policy.deny`
   hold glob patterns, matched ignoring case against both `model` and
   `provider/model`. Deny always wins. A non-empty allow list is exclusive.
   The domain lives in `governance/modelpolicy`.

2. **Every routing decision honours it.** A route never lands on a
   forbidden model. A rule whose target is forbidden falls through to its
   first permitted fallback. Smart routing picks only among
   `smart_routing.models` when they are declared.

3. **A request for a forbidden model is moved off it.** This is the
   owner's rule, not the coach's advice, so it holds whatever the coach's
   dials say.
   - **Claude Code subagents (PreToolUse on Agent):** the call is rewritten
     to the permitted model the Agent tool can name that is closest in
     tier: the same tier first, then the nearest below, then the nearest
     above. When there is none, the call is refused and the reason lists
     the permitted models, so the agent can choose again.
   - **The proxy:** the request is routed to the permitted model on offer
     closest in price: the priciest one that costs no more, else the
     cheapest one above. The optimizer mode still decides whether that is
     observed, proposed, or applied, and the preferred-model ceiling still
     refers an upgrade. Without declared models on offer there is nothing
     safe to move to, so the request is reported with no target.

4. **No separate company layer.** A company that wants the same policy
   on every machine ships the configuration file through its device
   management, like any other setting. A second, system-wide file would
   add precedence rules for a need that the one file already meets.

## Consequences

- The session's own model cannot be changed from a hook. A forbidden
  session model is enforced only where TokenOps sees a model choice: its
  subagents, and requests through the proxy.
- Blocking a proxied request outright would break the agent mid-task. The
  proxy moves the request, or reports it; it never fails it.
- A policy alone builds the proxy's router, even when no routing rules
  are configured.
- A user can edit their own configuration. The policy is a rule for
  TokenOps to follow, not a control the user cannot get around. A company
  that needs that guarantee enforces it where the model is served: at the
  vendor account or a gateway.
