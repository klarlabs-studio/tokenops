# ADR 0002 — Researched, effective-dated pricing snapshots

- **Status:** Accepted
- **Date:** 2026-07-07
- **Deciders:** TokenOps maintainers
- **Related:** `internal/contexts/spend/spend` (pricing catalog + engine), ADR 0001, the "800:1 / The tool was guessing" honesty ethos

## Context

Model rates live in a hand-maintained embedded catalog
(`internal/contexts/spend/spend/pricing.yaml`). It has two structural problems:

1. **It drifts silently.** The Opus 4.x rows were entered at exactly ⅓ of real
   pricing (`$5/$25/$0.50` vs `$15/$75/$1.50`). Because cache-read ($0.50) was
   internally consistent with the *wrong* input ($5), nothing flagged it — every
   Opus API-equivalent cost was understated 3× for an unknown period (the
   coach-hook budget, spend summaries, the blog ratios). It was only caught by a
   human noticing the cache-read line looked low. **Consistency is not
   correctness.**
2. **It has no provenance and no time dimension.** A rate is just a number — no
   source, no "as of when." When a vendor changes prices, historical spend should
   still be computed at the rate that was **in effect at the time of the event**,
   not today's rate. A flat table can't express that.

This is squarely against the product's ethos: TokenOps exists to replace *guessing*
with *sourced measurement*. Its own cost basis being a silently-drifting,
unsourced, timeless table is the exact anti-pattern it sells against.

## Decision

Make pricing **researched, sourced, effective-dated, and drift-visible.**

### 1. A structured source (default: LiteLLM), behind a pluggable interface
Fetch rates from a machine-readable feed instead of transcribing by hand. Default:
**BerriAI/litellm `model_prices_and_context_window.json`** — vendor *list* prices
with `input_cost_per_token`, `output_cost_per_token`,
`cache_read_input_token_cost`, `cache_creation_input_token_cost` for
Anthropic/OpenAI/etc.; stable raw URL; no key. Wrap it in a `PricingSource`
interface so OpenRouter's `/models` API, a vendor-page scraper, or a curated
override can be added later without touching the engine.

### 2. Effective-dated snapshots (the "at the time" part)
Pricing stops being one flat table and becomes a series of **timestamped
snapshots**, each carrying `source`, `source_url`, and `fetched_at`. The cost
engine selects the snapshot **in effect at the event's timestamp** — so a June
event is priced at June's rate. The embedded `pricing.yaml` remains the offline
**baseline snapshot** (dated at its commit) and the guaranteed fallback.

### 3. A `pricing refresh` flow that DIFFS
`tokenops pricing refresh` fetches the source → normalizes to the internal model →
writes a new snapshot → **prints a diff against the current one**. The Opus error
would have shouted `claude-opus-4-8 cache_read 0.50 → 1.50 (+200%)` instead of
hiding. Plus `pricing show` / `pricing diff` to inspect. Refresh is manual/opt-in
(or, later, a vendor-usage-style poller); offline always works on the baseline.

### 4. A consistency guard as a lint on every snapshot
Run the exact check that caught Opus — cache-read ≈ 10% of input, output ≈ 5× input
(per family) — over any fetched snapshot and **warn on anomalies** before adopting
it. This defends against the *source itself* being wrong, and against a future
hand-edit re-introducing a ⅓-style error.

## Alternatives considered
- **Status quo (hand-maintained YAML).** Rejected — it just drifted, unsourced.
- **OpenRouter `/models` as default.** Its prices carry OpenRouter's margin and its
  IDs don't always map to vendor list prices — good for proxy-routed spend, wrong
  as the list-price basis. Keep as a *pluggable alternate*.
- **Vendor pricing-page scrape as default.** Most authoritative, most fragile
  (HTML churn). Keep as a *pluggable alternate* (TokenOps already scrapes).
- **Live per-request pricing fetch.** Rejected — latency + a hard network coupling
  on the hot path; a cached snapshot is the right unit.

## Consequences
**Positive**
- Cost basis becomes accurate, **sourced**, and **historically correct**.
- Drift is *loud* (diff on refresh) instead of silent.
- Provenance for every number — matches the "every claim is sourced" ethos.

**Negative / risks**
- **Third-party feed accuracy.** LiteLLM can itself be stale/wrong → mitigated by
  the consistency guard, the diff (human sees changes), and the embedded baseline.
- **Mapping maintenance.** LiteLLM model IDs → TokenOps keys (incl. cache fields);
  models absent from the source fall back to the baseline.
- **Engine change.** Effective-dating touches the cost path (snapshot selection by
  event timestamp) — the most invasive piece; phase it.

## Open questions
1. **Snapshot storage.** A `pricing/snapshots/<fetched_at>.json` dir, or one file
   with effective-date ranges per rate? (Lean: append-only timestamped files +
   an index; simplest to reason about and diff.)
2. **Effective-dating granularity.** Per-fetch snapshot vs per-observed-rate-change
   (dedup snapshots that didn't change). (Lean: store per-fetch, collapse in
   selection.)
3. **Refresh cadence.** Manual only, or an opt-in poller (like the vendor-usage
   sources)? Default manual; poller later.
4. **The fetch itself.** The dev sandbox blocks outbound calls (same limit as the
   provider live-verify) — the `refresh` command is built here; the operator (or
   CI) runs the actual fetch, or a hand-off script does.

## Rollout
- **Phase 1** — `PricingSource` interface + LiteLLM adapter + `pricing refresh`
  (fetch → snapshot → **diff**) + the consistency-guard lint. Engine still uses the
  *latest* snapshot. Embedded baseline unchanged. (No hot-path change yet.)
- **Phase 2** — effective-dating: the cost engine selects the snapshot in effect at
  each event's timestamp.
- **Phase 3** — additional pluggable sources (OpenRouter, scrape, curated) + an
  optional auto-refresh poller.

Each phase is independently shippable; Phase 1 already ends the silent-drift
problem for new data.
