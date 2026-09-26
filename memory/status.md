---
updated: 2026-09-26
---
## Current State

TokenOps is a local-first adaptive control plane for AI-assisted work. Current
source `main` includes PR #398. The CLI/MCP/API surfaces are the product;
there is no bundled browser dashboard or demo-data workflow (PR #387).

ADR 0004 Phases 0–9 are implemented. Phase 5 now includes bounded live
validation. PRs #394–#396 improved execution attribution and correctly rejected
two invalid earlier trials. A later five-pair metered OpenAI trial through the
current-checkout supervised daemon supplied complete randomized assignment,
route, usage, pricing, latency, and verifier-outcome evidence.

Codex CLI has a global stdio MCP registration pointing at the current
checkout's `bin/tokenops serve`. On 2026-09-26, a fresh interactive Codex CLI
session passed approval review and successfully called the read-only
`tokenops_version` and `tokenops_status` tools. The MCP server reported
`v0.70.0-52-gd7355e4` from commit `d7355e4`; the installed daemon reported
v0.70.0 and ready with no blockers. MCP registration still does not route
model traffic; this validation did not configure an OpenAI API provider or
metered route.

Live validation on 2026-09-26 first restarted installed v0.70.0 and confirmed
CLI health/readiness plus MCP initialize, tools/list, and `tokenops_status`
against the real local store. The current checkout was subsequently built and
installed as the supervised daemon for the final trial. No demo/seed command
was run. A tagged release containing the post-v0.70.0 cleanup and trial fixes
still needs an operator-controlled release check.

The daemon was not answering initially. A supervised restart succeeded from
the host context, and health/readiness subsequently returned 200. The local
MCP stdio validation also returned ready status on v0.70.0.

The prior variant HTTP 400 is narrowed but not conclusively diagnosed. Safe
event metadata shows both failed variants routed `claude-opus-5-5` to
`claude-sonnet-5`; their matched baselines returned 200. Local proxy/router
tests confirm that a trial route changes only `model` and preserves other
top-level request fields. Anthropic's Sonnet 5 migration documentation says it
returns 400 for manual extended thinking and non-default sampling parameters,
so a preserved
model-specific field is the leading hypothesis. TokenOps retained neither the
request body nor the upstream error body, so the exact rejected field remains
unknown and no prompt or credential-bearing body should be added to telemetry.

The final Phase 5 trial enrolled five randomized `gpt-6-sol`/`gpt-6-luna`
pairs. All ten calls returned HTTP 200 and the exact `TOKENOPS_OK` value; the
local JSON verifier recorded ten independent verification outcomes without
retaining response content. Both arms achieved 5/5 outcomes and 21 measured
tokens per execution. Verified standard pricing measured $0.000090 per Sol
execution and $0.0000045 per Luna execution. The experiment ledger reports
supported evidence, five improved pairs, 95% median cost reduction, and all
quality/latency guardrails passing. Randomized verification accepted all five
pairs without observational fallback. This proves only the bounded exact-output
cohort, not general quality equivalence.

The live trial exposed and fixed three genuine gaps: non-streaming provider
usage/model metadata was not preferred over response-envelope tokenization;
synthetic proxy executions started just after their prompt observation; and
there was no content-safe local JSON outcome verifier. Exact verified GPT-6 Sol
and Luna pricing was also added. The current checkout remains installed as the
supervised daemon and reports healthy/ready. The temporary API provider and
route were removed and the original OpenAI subscription was restored. The
deprecated `codex-plus` spelling now resolves to canonical `gpt-plus`.

A current-checkout build passed CLI health checks and MCP stdio
initialize/tool-list/status/resource-glance calls. The resource glance read
actual Claude Code and Codex local session records, reported current pressure
as clear, and recommended continuing. That build is now the supervised daemon
used for the final paired validation.

The formatter-learning evidence guard was merged in PR #390. It separates
projected savings from observed estimates and avoids recommending stronger
compression without wrapped recovery evidence. Full `make verify` passed its
Go, lint, race, eval, and security gates at that time; local proto verification
may still require the repository's pinned compiler version.

## Next

1. Resolve the remaining historical Anthropic HTTP 400 ambiguity with a sanitized provider error
   classification or a local reproduction of the original request shape; do
   not log prompt or credential-bearing bodies.
2. Before another paid paired attempt, verify full-execution arm consistency
   and obtain explicit authorization for the new model calls.
3. Record a human or recognized-verifier outcome only when actually observed;
   do not seed examples or claim causal uplift from task completion alone.
4. Keep background coaching off until its opt-in, scope, and cost policy are
   explicit; on-demand coaching remains the safe path.
5. The formatter provenance guard was merged in PR #390; tune `fmt learn`
   thresholds only after sufficient genuine wrapped-run/recovery evidence.

## Recently Resolved

- PR #396: randomized verification rejects assigned executions with upstream
  HTTP failures.
- PR #395: randomized verification rejects partially applied model-route arms.
- PR #394: randomized verification attributes proxy executions from durable
  assignment IDs.
- PR #390: formatter-learning projections are distinct from observed evidence.
- PR #387: removed demo command/data and browser dashboard surfaces.
- PR #388: deduplicated repeated provenance caveats in aggregates.
- PR #389: distinguished incomplete API-equivalent estimates from actual
  plan-covered marginal cost.
