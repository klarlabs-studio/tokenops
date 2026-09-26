---
updated: 2026-09-26
---
## Open

- Phase 5 real-world validation: no matched intervention/outcome cohort has
  been observed. Continue with genuine work only; keep claims observational
  until assignment, measurement, and explicit outcome evidence join.
- Installed product verification: v0.70.0 daemon health/readiness and MCP
  status passed on 2026-09-26, but the binary still exposes demo/dashboard
  surfaces retired in PR #387. Upgrade and retest a release containing #387
  only as an operator-controlled deployment.
- fmt learning thresholds: requires more real command-run telemetry.

## Deferred by policy

- Background coaching pipeline: retain on-demand coaching until opt-in,
  replay scope, and cost policy are explicit.
- Coach-hook Phase 2+, extra pricing sources, OpenAI-compatible live provider
  validation, and further CI-minute reductions remain optional follow-ups;
  they are not current roadmap gates.

## Resolved 2026-09-25

- #387 removed demo and browser dashboard surfaces.
- #388 deduplicated repeated measurement caveats.
- #389 corrected unpriced-plan spend wording.
