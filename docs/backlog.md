
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
