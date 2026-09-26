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
| `supported` | At least five matched pairs and 60% strong outcome coverage; every declared guardrail is measured and passes, and the declared objective meets its improvement gate | Recommend |
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
tokenops experiment start anthropic claude-opus-4-1 claude-sonnet-4-5 \
  --objective tokens --min-improvement-pct 10 \
  --guardrail quality --guardrail latency_ms:10
tokenops experiment status <experiment-id>
tokenops experiment stop <experiment-id> --reason "operator stopped"
```

`status` reports both persisted state and the current evidence-derived belief.
Raw prompts, command output, and private work content are not stored in control
events. The objective and guardrail policy is persisted with the trial. The
objective improvement percentage and numeric guardrail tolerances are explicit
operator choices, not universal weights. A strict `quality` non-inferiority
guardrail is required. Missing objective or guardrail measurements, or any
guardrail regression, keeps the belief observational. Trials with different
utility policies are not pooled.

## Outcome Verification

`tokenops verify` and `tokenops_verify` join outcome events to executions by
execution ID, including outcomes recorded after the work ended. When a decision
is supplied, CLI and MCP outcome recording copy its intervention and experiment
IDs onto the outcome event, making the decision-to-intervention-to-outcome
lifecycle directly queryable in the local ledger. Both surfaces show assessed
success rates for the compared cohorts. A drop in success can
flag harm even when measured token use falls; absent assessments remain
unknown. They also report mean proxy-observed request latency for each cohort;
missing requests remain unknown rather than zero. Applied-versus-not-applied
cohorts remain observational. When execution-linked randomized assignments
exist, verification can compare one selected trial's complete pairs; every
assigned execution must be reconstructed and have measured tokens and an
explicit outcome. Incomplete or mixed evidence falls back to observational
reporting. Use `tokenops verify --experiment-id <id>` when multiple trials are
present. The randomized result applies to that trial's executions, not to all
work or future routing. Plan-included token use is
reported separately by provider as quota consumption, never converted to
synthetic dollar savings. Metered spend remains unreported by this comparison
unless a prompt event carries `cost_measured`, which the proxy sets only after
the effective-dated rate card priced the event successfully. Legacy events,
missing-rate events, and subscription/trial usage remain unknown for metered
spend comparisons. Human attention is reported only when an operator explicitly
provides active effort (for example, `tokenops outcome record <execution-id>
--result achieved --attention-minutes 3.5`); TokenOps does not infer it from
transcripts or agent estimates. Cohort means remain unknown if any execution
lacks an operator report.

Proxy routing optimization events carry the session association used by work
reconstruction, so applied routes enter the intervention cohort instead of
being silently counted as baseline attempts. Verification also accepts other
durable decision kinds only when their lifecycle reached `applied`; proposals,
shadow decisions, rejections, and failures do not enter the intervention
cohort. Paired routing decision/optimization records count as one kind.
