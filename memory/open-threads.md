---
updated: 2026-09-27
---
## Open

- Subscription-plan telemetry: validate canonical GPT Plus/Pro and Claude
  Pro/Max/Business/Team/Enterprise semantics with genuine plan-meter records.
  The supported API-backed cohorts prove routing and metered cost only; API
  billing is separate from subscription quota accounting. Current Codex data
  reports opaque plan type `prolite` with one weekly window. Codex's documented
  `account/read` method independently returned the same value, while official
  OpenAI documentation publishes no mapping for it; preserve it as vendor
  identity rather than mapping it to a catalog plan. Claude JSONL lacks an
  authoritative quota percentage. The decoder preserves shape-valid vendor window labels and
  supports the current public web client's unified limits contract. A
  content-safe copied usage request has now enabled a current-checkout
  interactive daemon to store one genuine
  `claude-usage-meter` event and `plan headroom` consumed its authoritative
  window. Durable supervised polling now succeeds under launchd after the
  content-safe request importer began preserving strict allowlists of browser
  client hints and Cloudflare bot-management cookies; every imported cookie is
  centrally redacted. Do not generalize this Max-plan proof to Pro, Team,
  Business, or Enterprise. Additional plan validation requires genuine records
  from accounts carrying those plans.
- Relicta release governance: the repository guard in
  `scripts/relicta-publish-signed.sh` never invokes Relicta's unsafe publish
  action. Relicta plans, versions, writes notes, and approves; Git verifies and
  pushes the signed tag, then the guard cancels the Relicta run so planning can
  resume. v0.72.2 proved CLI skip flags are ignored and v0.72.3 proved config
  disables are ignored too. Retain those upstream defects as known limitations.
- fmt learning threshold tuning: the merged provenance guard distinguishes
  actual wrapped runs from offline projections and prevents up-tuning without
  recovery reads. The 2026-09-27 audit found 14 genuine wrapped runs, 33,778
  projected runs, and zero recovery reads; that is still insufficient outcome
  evidence for a threshold experiment.

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

## Resolved 2026-09-26

- Installed v0.72.1 health/readiness and release identity passed after the
  Homebrew upgrade.
- Five-pair OpenAI and Anthropic API-backed coding-agent cohorts reached
  supported evidence with independent verifiers and stable multi-call arms.

## Resolved 2026-09-27

- A fresh read-only Codex session completed the released MCP
  `tokenops_prepare_work` to `tokenops_review_work` handoff. The installed MCP
  server and daemon both reported v0.73.1, the exact workflow identifier was
  preserved, and an intentionally unexecuted task returned `no_evidence`
  without presenting placeholder zeroes as measurements. The global MCP
  registration now targets the installed Homebrew binary instead of a mutable
  checkout build.
