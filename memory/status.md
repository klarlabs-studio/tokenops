---
updated: 2026-09-26
---
## Current State

TokenOps is a local-first adaptive control plane for AI-assisted work. Current
source `main` includes PR #405; v0.72.0 contains the work through PR #404. The
CLI/MCP/API surfaces are the product; there is no bundled browser dashboard or
demo-data workflow (PR #387).

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
was run. v0.71.0 then published the post-v0.70.0 cleanup and trial fixes via
GoReleaser, including four platform archives, checksums, and the Homebrew cask.
The Homebrew upgrade restarted the supervised daemon; CLI and daemon both
reported v0.71.0 at commit `844c9be`, with health and readiness returning 200
and no blockers.

The daemon was not answering initially. A supervised restart succeeded from
the host context, and health/readiness subsequently returned 200. The local
MCP stdio validation also returned ready status on v0.70.0.

The prior variant HTTP 400 is narrowed but cannot be conclusively diagnosed
retroactively. Safe event metadata shows both failed variants routed `claude-opus-5-5` to
`claude-sonnet-5`; their matched baselines returned 200. Local proxy/router
tests confirm that a trial route changes only `model` and preserves other
top-level request fields. Anthropic's Sonnet 5 migration documentation says it
returns 400 for manual extended thinking and non-default sampling parameters,
so a preserved
model-specific field is the leading hypothesis. TokenOps retained neither the
request body nor the upstream error body, so the exact rejected field remains
unknown and no prompt or credential-bearing body should be added to telemetry.
PR #405 closes the prospective observability gap: Anthropic error envelopes
are reduced to bounded classifications for unsupported sampling parameters,
incompatible thinking configuration, assistant prefill, known provider error
types, or provider-plus-HTTP-status fallback. Raw error messages and bodies are
discarded, and the sanitized classification is exported as OTLP `error.type`.

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

The exact-output live trial exposed and fixed three genuine gaps: non-streaming provider
usage/model metadata was not preferred over response-envelope tokenization;
synthetic proxy executions started just after their prompt observation; and
there was no content-safe local JSON outcome verifier. Exact verified GPT-6 Sol
and Luna pricing was also added. The released v0.71.0 binary is installed as
the supervised daemon and reports healthy/ready. The temporary API provider
and route were removed and the original OpenAI subscription was restored. The
deprecated `codex-plus` spelling now resolves to canonical `gpt-plus`.

A bounded coding-agent extension then ran a disposable Go repair task through
Codex CLI and the OpenAI Responses API. The clean confirmation experiment
`experiment:f9cb9517-6be3-4391-9d76-bb7c56b679dc` assigned one full execution
to each arm. The baseline made five streamed `gpt-6-sol` calls; the variant
made six streamed calls that all returned `gpt-6-luna`. Every call retained
the execution/decision/experiment link, reported vendor usage and latency, and
the two executions recorded four and five content-safe tool calls. Independent
`go test ./...` runs passed for both fixtures and were recorded as scoped human
outcomes. Measured cost was $0.044583 baseline and $0.002401 variant; mean
request latency was 3128ms and 2300ms. The ledger reports one complete pair,
100% strong outcome coverage, and observed evidence only. This proves the
coding-agent evidence path, not comparative quality or causal uplift; at least
five matched pairs remain necessary for a supported belief.

The first coding-agent pair exposed two confounders before that clean run:
`--approve-for-me` added separate `codex-auto-review` traffic, and once a
one-pair trial filled, later requests reused the model arm but lost experiment
correlation. The confirmation run used the ordinary workspace sandbox, and the
multi-call evidence fix keeps a durable execution assignment reusable after
enrollment closes while learning counts and aggregates unique executions
rather than per-request decisions.

Relicta 4.2.0 planned, approved, and published v0.71.0 and v0.72.0, but two
configuration settings did not behave as declared: `gitsign: true` still
produced an unsigned annotated tag, and `autocommitchangelog: false` still
appended generated notes to the local changelog after pushing the tag. The
append was removed locally and never reached either tag or `main`. The defects
reproduced during v0.72.0; resolve or guard both behaviors before the next
release.

A current-checkout build passed CLI health checks and MCP stdio
initialize/tool-list/status/resource-glance calls. The resource glance read
actual Claude Code and Codex local session records, reported current pressure
as clear, and recommended continuing. Current-checkout builds were used only
for the paired validations; the supervised daemon was restored to the released
Homebrew binary afterward. v0.72.0 subsequently published the streaming and
multi-call evidence fixes in four platform archives plus checksums and a
Homebrew cask. The installed CLI and supervised daemon report v0.72.0 at
commit `5bbdaf4`; host-local health and readiness both return 200 with no
blockers.

The formatter-learning evidence guard was merged in PR #390. It separates
projected savings from observed estimates and avoids recommending stronger
compression without wrapped recovery evidence. Full `make verify` passed its
Go, lint, race, eval, and security gates at that time; local proto verification
may still require the repository's pinned compiler version.

## Next

1. Before another paid paired attempt, obtain explicit authorization for the
   new model calls and use at least five matched pairs before interpreting an
   arm difference as supported evidence.
2. Run representative Codex and Claude Code coding cohorts with independent
   verifiers; a future Anthropic reproduction can now distinguish the leading
   incompatibility classes without retaining response content.
3. Resolve or guard Relicta's ignored `gitsign` and `autocommitchangelog`
   settings before the next release.
4. Record a human or recognized-verifier outcome only when actually observed;
   do not seed examples or claim causal uplift from task completion alone.
5. Keep background coaching off until its opt-in, scope, and cost policy are
   explicit; on-demand coaching remains the safe path.
6. The formatter provenance guard was merged in PR #390; tune `fmt learn`
   thresholds only after sufficient genuine wrapped-run/recovery evidence.

## Recently Resolved

- PR #405: adds content-safe Anthropic upstream error classification and OTLP
  export; the original 400 remains historically unknowable because its body
  was not retained.
- v0.72.0: released PRs #402–#404, installed through Homebrew, and verified the
  supervised daemon's version, health, and readiness.
- PR #403: completed the coding-agent evidence path and preserves execution
  correlation and coverage across multi-call trials after enrollment closes.
- PR #402: streamed Responses/Messages usage, latency, model, and content-safe
  tool-call counts are recorded for coding-agent traffic.
- v0.71.0 / PR #400: published the Phase 5 proof and post-v0.70.0 work;
  Homebrew installation and released-daemon version/commit/health were verified.
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
