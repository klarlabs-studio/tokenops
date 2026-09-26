---
updated: 2026-09-26
---
## Current State

TokenOps is a local-first adaptive control plane for AI-assisted work. Current
source `main` includes PR #396. The CLI/MCP/API surfaces are the product;
there is no bundled browser dashboard or demo-data workflow (PR #387).

ADR 0004 Phases 0–4 and 6–9 are implemented. Phase 5 implementation is shipped,
but live validation remains open. PRs #394–#396 improved execution attribution
and reject partial or failed randomized routes. Two real paired trial attempts
were rejected by those checks (partial route application; upstream HTTP 400
with fallback), so neither is valid comparison or outcome evidence. Verification
must remain observational. The plan state is tracked in `.roady/`; current
intent and phase truth are in `docs/adr/0004-ai-work-intelligence-and-control.md`.

Live validation on 2026-09-26 restarted the installed supervised daemon and
confirmed CLI health/readiness plus an MCP initialize, tools/list, and
`tokenops_status` call against the real local store. No demo/seed command was
run. Installed CLI/daemon is v0.70.0, but it still exposes the retired
`demo` command and `tokenops_dashboard` MCP tool; it predates source cleanup in
PR #387 and is not yet the current product surface. `tokenops events` used its
JSONL fallback and reported 274 `budget.exceeded` events; this is not a matched
intervention/outcome cohort and cannot establish quality or causal uplift.

The daemon was not answering initially. A supervised restart succeeded from
the host context, and health/readiness subsequently returned 200. The local
MCP stdio validation also returned ready status on v0.70.0.

A temporary build of current checkout (not installed) also passed CLI health
checks and MCP stdio initialize/tool-list/status/resource-glance calls against
that daemon. The resource glance read actual Claude Code and Codex local
session records, reported current pressure as clear, and recommended
continuing. This validates the surface and data path only; it does not supply
an intervention/outcome pair or verify work quality.

The formatter-learning evidence guard was merged in PR #390. It separates
projected savings from observed estimates and avoids recommending stronger
compression without wrapped recovery evidence. Full `make verify` passed its
Go, lint, race, eval, and security gates at that time; local proto verification
may still require the repository's pinned compiler version.

## Next

1. Diagnose the variant request's HTTP 400 using non-sensitive request
   metadata and a mocked/local compatibility test; do not log prompt or
   credential-bearing bodies.
2. Before another paid paired attempt, verify full-execution arm consistency
   and obtain explicit authorization for the new model calls.
3. Record a human or recognized-verifier outcome only when actually observed;
   do not seed examples or claim causal uplift from task completion alone.
4. Keep background coaching off until its opt-in, scope, and cost policy are
   explicit; on-demand coaching remains the safe path.
5. Merge the formatter provenance guard before relying on `fmt learn`; tune
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
