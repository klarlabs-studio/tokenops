# ADR 0002 — Researched, effective-dated pricing snapshots

- **Status:** Accepted; amended 2026-10-29: a model the card in force does not list takes the nearest card that does, see *Amendment: the nearest card that lists the model*; amended 2026-11-01: a local overlay of observed plan limits, see *Amendment: observed limits stay local*
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

#### Amendment: the nearest card that lists the model (2026-10-29)

The card in force at an event's timestamp is authoritative for every
model it lists. A model it does not list was first priced as unknown,
even when an earlier or later card listed it: a source can add a model
after its launch, or drop it while it is still used. On one machine that
left about 20,000 recorded requests unpriced (older Codex models, opencode
models) that the cards did price.

Such a model now takes the nearest card that lists it: the latest earlier
card, whose rate was in force when the model was last listed, else the
earliest later card, which first listed it. A recorded turn used a model
that existed, so the card nearest in time is better evidence of its price
than none. A model no card lists stays unknown.

#### Amendment: observed limits stay local (2026-11-01)

The same rule covers the plan catalog: its tiers' allowances change by
pull request, with a source, never at runtime. The catalog-drift finding
(`tokenops checkup`, #838) measures when an operator's own windows put
two tiers at a different ratio than the catalog states, and tells them to
open an issue.

Waiting for that pull request left plan advice on the operator's machine
replaying against a figure their own readings contradict. So a drifted
comparison, under exactly the finding's evidence thresholds, is now also
kept in a local overlay (`~/.tokenops/observed-limits.json`), and plan
advice on that machine replays with the observed figure in its place:

- **Local only.** The overlay is never written into the shipped catalog,
  never uploaded, and never used for anything but this machine's tier
  projections (plan advice). Live window shares and paces come from the
  vendor and do not use it.
- **Labelled.** Every use names both figures, observed and catalog, in
  the advice's `observed` list and a caveat.
- **Conservative.** Readings measure a ratio between two tiers, not either
  allowance, so the lower tier keeps its catalog figure and only the
  higher one is scaled. A limit drops out on its own once the evidence no
  longer drifts, or once the catalog changes the figure it was observed
  against: the pull request supersedes it.
- **Reversible.** `tokenops plan observed reset` empties the overlay and
  keeps it off until `tokenops plan observed on`.

The finding still asks for the issue: the overlay helps one machine, the
pull request fixes the catalog for everyone.

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
