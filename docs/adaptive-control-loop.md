# Adaptive Control Loop

TokenOps keeps the harness responsible for task planning and execution. The
control plane observes enough of that work to allocate AI resources, apply
policy, and learn from outcomes without becoming an agent framework.

## First Supported Loop

The first closed loop covers coding-agent model allocation:

1. Smart routing considers the current provider, model, task class, capacity,
   pricing, preferences, and adapter capability.
2. A durable decision records the alternatives, evidence, policy, effective
   authority, uncertainty, and rationale.
3. MCP surfaces advice on every supported harness. Only proxy traffic is
   executable; other adapters remain advisory.
4. Outcomes come from explicit human assessment or a recognized verifier run
   after the final edit. Completion or self-report alone is not proof.
5. Learning projects matched outcomes into retractable evidence tiers.

## Evidence Tiers

| Tier | Meaning | Allowed influence |
|---|---|---|
| `unknown` | No current strong matched evidence | History only |
| `observed` | Some current evidence, below promotion gates | Explain and continue shadowing |
| `supported` | At least five matched pairs, 60% strong coverage, quality non-inferiority, and meaningful resource improvement | Recommend |
| `trusted` | At least 20 pairs and 90% strong coverage | Eligible for separately authorized automation |

Evidence is scoped by a route fingerprint and expires after 90 days. Changes
to provider, models, policy surface, or adapter invalidate rather than silently
reuse an old belief.

The proxy applies a learned route automatically only when both conditions are
true: policy grants automatic authority and the exact route fingerprint has a
current `trusted` belief. If evidence is missing, stale, or falls below the
gate, the route regresses to a recorded recommendation and the baseline model
continues. Supported evidence can recommend but cannot grant itself authority.

## Bounded Experiments

Experiments require an explicit `start`, stay within one provider, randomize
the order of baseline and variant inside each pair, and are capped at 10 pairs
or 14 days. Evidence from multiple fresh trials with the same fingerprint is
combined, allowing the 20-pair trusted threshold without weakening the bound
on any individual trial. Assignments and termination are append-only events.
Use:

```bash
tokenops experiment start anthropic claude-opus-4-1 claude-sonnet-4-5
tokenops experiment status <experiment-id>
tokenops experiment stop <experiment-id> --reason "operator stopped"
```

`status` reports both persisted state and the current evidence-derived belief.
Raw prompts, command output, and private work content are not stored in control
events.

## Outcome Verification

`tokenops verify` and `tokenops_verify` join outcome events to executions by
execution ID, including outcomes recorded after the work ended. Both surfaces
show assessed success rates for optimized and baseline cohorts. A drop in
success can flag harm even when measured token use falls; absent assessments
remain unknown. They also report mean proxy-observed request latency for each
cohort; missing requests remain unknown rather than zero. The current cohort
split is observational, so these measures can identify a warning but cannot
claim the intervention caused the difference. Plan-included token use is
reported separately by provider as quota consumption, never converted to
synthetic dollar savings. Metered spend remains unreported by this comparison
until its event-time pricing provenance can be established.
