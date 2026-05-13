
## Operator Golden Path + Wedge KPI

Add a clear operator-first onboarding path that proves value quickly: a 5-minute golden path from install to first measurable token insight, with explicit expected output and a wedge KPI definition in docs.

---

## Branch Protection Compliance Guardrail

Define and enforce a strict PR-only workflow for protected branches. Add repository guardrails and team runbook so AI/CLI automation never pushes directly to main. Include branch-protection compliance checks in contribution workflow.

---

## Security Finding Signal Governance

Harden nox signal quality by formalizing a false-positive governance policy: classify scanner findings (real issue vs acceptable pattern), keep scoped excludes minimal, and require documented rationale + periodic review for every suppression path.

---

## Optimization Quality Evals Framework

Implement AI optimization evaluation harness with offline replay datasets and quality regression gates. Track task-success, regenerate rate, estimated token savings, and quality drift before enabling broader optimization defaults.

---

## Operator Wedge KPI Scorecard

Define a product wedge scorecard tied to operator outcomes: first-value time, token efficiency uplift, and spend attribution completeness. Publish metric definitions, baseline capture workflow, and rollout thresholds in docs + CLI output.

---

## Telemetry Contract and Lineage Control

Add explicit data-contract and lineage docs for key event fields used across proxy, storage, OTLP, and dashboard. Include schema change policy, compatibility tests, and owner map for each telemetry contract.

---

## Risk-Based Test Coverage Uplift

Introduce repository-level quality debt dashboard for low-coverage/high-risk packages (e.g., daemon entrypoints and integration surfaces). Add targeted tests and risk-ranked coverage goals rather than blanket percentage targets.

---

## Rule Intelligence

Treat operational rule artifacts (CLAUDE.md, AGENTS.md, repo instructions, MCP policies, Cursor rules, coding conventions) as first-class telemetry. Provide a Rule Engine that ingests rule sources, analyzes ROI (tokens saved, retries avoided, context reduction, latency impact, quality drift), detects conflicts and redundancy, compresses rule corpora into distilled behavioral representations, and dynamically injects only the relevant subset per request/workflow. Includes benchmarking harness for coding/workflow rule systems. Surfaces via CLI (tokenops rules analyze|conflicts|compress|inject|bench), proxy hooks, MCP server, and dashboard. Local-first, respects redaction, OTLP-emittable. Issue #12.

---

## Fix rulesfs walker: tolerate permission-denied + skip unreadable subtrees

## Problem

`internal/infra/rulesfs/source.go` `Discover()` walks the filesystem under the supplied root and bails on the first error any `filepath.WalkDir` callback returns. When the root is anywhere with read-restricted siblings (notably anywhere under `$HOME` on macOS — `~/Library/Saved Application State/...`, `~/Library/Containers/...`, etc.), the walk aborts before the loader ever sees the rule artifacts.

Observed cases:

- `tokenops rules analyze --root ~/.claude` → returns 0 documents even though `~/.claude/CLAUDE.md` exists. The walker enters one of the hidden sibling subtrees, hits an error, and halts.
- `tokenops rules analyze --root ~` → exits non-zero with `permission denied: open ~/Library/Saved Application State/com.shure.motivmix.savedState`.

## Repro

```bash
echo "# rules" > ~/.claude/CLAUDE.md
tokenops rules analyze --root ~/.claude --repo-id home
# expected: 1 document discovered
# actual:   no rule artifacts found
```

## Root cause

`Discover()` propagates every error from `filepath.WalkDir`'s callback. A single `EACCES` or `EPERM` from `os.Open` on a sibling subtree kills the whole walk. The existing hidden-dir skip (`base != ".cursor"` line in source.go) doesn't cover Library/Containers-style dirs without a leading dot.

## Fix

In the `filepath.WalkDir` callback (and the `fs.WalkDir` branch above it):

1. When the WalkDir-supplied `err` is non-nil and `errors.Is(err, fs.ErrPermission)`, log + skip:
   - If `d != nil && d.IsDir()` → return `fs.SkipDir` to skip the whole subtree.
   - Otherwise → return `nil` to skip the single file and continue.
2. For non-permission errors, keep current behaviour (propagate) so genuine I/O failures still surface.
3. Optionally: add a `Patterns`-aware short-circuit so a walker that's only looking for `CLAUDE.md` / `AGENTS.md` / `.cursor/...` doesn't bother descending into `Library/`, `node_modules/`, `vendor/` etc. — already half-implemented for the latter two; extend the skip list.

## Acceptance

- `tokenops rules analyze --root ~/.claude` discovers `CLAUDE.md` and produces a non-empty document set.
- `tokenops rules analyze --root ~` completes without exiting non-zero on permission errors; logs the skipped paths at debug level.
- New unit test in `internal/infra/rulesfs/source_test.go` injects a synthetic `fs.FS` that returns `fs.ErrPermission` on a sibling dir; assert that the legitimate `CLAUDE.md` is still discovered.

## Surfaces affected

- `tokenops rules analyze|conflicts|compress|inject|bench` CLI subcommands.
- `tokenops_rules_*` MCP tools (same loader path).
- `/api/rules/*` HTTP endpoints.

## References

- Discovered while testing tokenops MCP integration in Claude Code / Desktop after `brew install felixgeelhaar/tap/tokenops` (v0.2.0).
- Related: `internal/infra/rulesfs/source.go` lines ~57-104 (`Discover()`).


---

## First-Run Activation Flow

Activate new operators in under 5 minutes. Ship `tokenops init` wizard (idempotent: enables sqlite storage at $XDG_DATA_HOME or ~/.tokenops/db, default RBAC, audit on, rules root=$PWD, writes config), `tokenops demo` (seeds 7 days synthetic events so spend/burn/forecast/scorecard/top return populated data), structured `blockers[]` + `next_actions[]` fields in /healthz, /readyz, /version, and MCP status, and a disabled-subsystem error contract (`{error,hint}` instead of empty success when Storage/Rules/Providers disabled). Closes the time-to-value gap surfaced in v0.2.0 first-run review.

---

## Plan-Based Cost Model

Support flat-rate subscription plans (Claude Max, Claude Code Pro, ChatGPT Plus/Pro, Cursor, GitHub Copilot, Cody) where per-token cost is zero but quota matters. Design: PromptEvent gains `CostSource` enum (metered|plan_included|trial), config gets `plans: {provider: plan_name}` map. Spend engine: when CostSource=plan_included, CostUSD=0 and event counts toward quota tracker instead. New metrics: plan_quota_consumed_pct, plan_headroom_days, plan_overage_risk. Dashboard + CLI surface plan headroom alongside metered cost. Backward compat: events without CostSource default to "metered". Requires per-plan quota config (input/output tokens/month, rate-limit windows). Initial plan catalog: claude-max, claude-pro, claude-code-max, gpt-plus, gpt-pro, gpt-team, copilot-individual, copilot-business, cursor-pro, cursor-business.

---

## Rate-Limit Window Headroom

Most LLM subscriptions (Claude Max 5x/20x, Claude Pro, ChatGPT Plus/Team) publish rolling-window rate limits, not monthly token caps. Extend the plan model to track messages/requests per window (5h for Claude, 3h for ChatGPT, etc.) and emit a window-based headroom report alongside the existing monthly view. Split catalog: claude-max-5x + claude-max-20x replace generic claude-max; ChatGPT Plus + Team get message-per-window caps. HeadroomReport gains window_consumed, window_cap, window_pct, window_resets_at fields. CLI + MCP surface both metrics so operators see "67% of 5h window, resets in 1h42m" instead of "no monthly cap published". Sources: Anthropic Max plan docs, OpenAI Plus/Team rate-limit FAQ, dated 2026-05 snapshot URLs.

---

## MCP-First Wedge: Session Budget + Config-as-Code

Three-skill review converged: TokenOps targets wrong consumption surface (proxy traffic, but plan-based users go through Claude Code / Cursor MCP) and forces JSON-editing for config (Plans, Providers, Rules, OTel). The wedge bet is MCP-resident session observability + a config-as-code CLI primitive that replaces every text-edit-then-restart ritual. Ships: tokenops <subsystem> set verb pattern, MCP-side session traffic observer that counts Claude Code / Cursor MCP calls against plan windows, tokenops_session_budget MCP tool predicting next-2h headroom, structured error hints carrying exact next commands, customer-research scaffolding (interview script + tracker doc). Skips proxy work, optimizer expansion, dashboard polish until the wedge validates with 5 real users.

---

## Data Isolation + Surface Polish

Five gaps surfaced from the full v0.7.1 demo. (1) Rules walker bails on permission-denied siblings under $HOME so analyze/conflicts/inject return null even when CLAUDE.md exists — pre-existing in-progress task. (2) Synthetic demo events mix with real proxy/MCP-session events in every analytics query; need source-tagging filter + a `--include-demo` opt-in. (3) Only plan_headroom + session_budget self-record session pings; the other 20 MCP tools don't, biasing the window count. Auto-wrap at registration so every tool call records. (4) 22 MCP tools surface flat with no curation; add tokenops_help that returns a "start here" subset. (5) Status/scorecard should expose real_vs_seeded counts so operators see signal vs synthetic ratio. Skips vendor /usage ingestion (waits for customer-discovery interview data).

---
