# Pricing research & snapshots

TokenOps prices model usage from a rate card. Historically that card was a
single hand-maintained file (`internal/contexts/spend/spend/pricing.yaml`) with
no source and no time dimension — which is how the Opus 4.x rows silently sat at
⅓ of real pricing for an unknown period (`$5/$25/$0.50` instead of
`$15/$75/$1.50`). Because the wrong input was *internally consistent* with the
cache-read line, nothing flagged it. **Consistency is not correctness.**

The `tokenops pricing` command implements [ADR 0002](adr/0002-pricing-research-snapshots.md):
a researched, sourced, timestamped, drift-visible pricing framework.
**Phase 1** made rates *sourced and diffable* so drift is loud instead of
silent. **Phase 2** (this section's *Effective dating*) wires those snapshots
into the cost path: each event is priced at the rate card that was in effect at
the event's own timestamp. **Phase 3** broadened snapshots from Anthropic-only
to **every provider the catalog prices** (OpenAI, Anthropic, Mistral, Gemini,
Cohere, Groq, DeepSeek, xAI, Perplexity, Cerebras), so `refresh` now surfaces
drift in *any* vendor's rows — not just Anthropic's.

## The model

- **Snapshot** — a point-in-time rate card with provenance: `source`,
  `source_url`, `fetched_at`, and `rates` keyed by `"<provider>/<model>"`
  (`anthropic/claude-opus-4-8`, `openai/gpt-4o`, `mistral/mistral-large`, …).
  The provider prefix is what makes a snapshot span every provider while
  keeping clean string keys in JSON, and it matches the multi-provider engine
  table (`spend.Key{Provider, Model}`) so a fetched rate overrides the correct
  vendor's baseline row. The ratio guard (below) stays Anthropic-family-scoped.
- **Source** — a pluggable fetcher (`Source` interface). The default is
  **LiteLLM** (`BerriAI/litellm/model_prices_and_context_window.json`): vendor
  *list* prices, a stable raw URL, no API key. OpenRouter, a vendor-page
  scraper, or a curated override can be added later without touching the engine.
- **Baseline** — the embedded `pricing.yaml`, wrapped as an always-present
  fallback snapshot (`source: embedded-baseline`) with a fixed, committed
  `fetched_at`. Everything works offline against the baseline.

## Storage

Snapshots are append-only, timestamped JSON files under
`~/.tokenops/pricing/snapshots/<RFC3339>.json` (colons sanitized to `-` for
portability). Writes are atomic (temp + rename). The directory listing *is* the
index — files sort lexically by their RFC3339 name, which is also chronological.
The baseline is never persisted; it lives in the binary.

```
~/.tokenops/pricing/
└── snapshots/
    ├── 2026-07-08T09-00-00Z.json
    └── 2026-07-09T09-00-00Z.json
```

Override the directory with `--dir` (used by tests and CI).

## Commands

### `tokenops pricing refresh`

Fetch the current rates from the source, run the consistency guard (anomalies
print as warnings), **diff against the latest existing snapshot** (or the
baseline), print the changes, and write the new snapshot.

```
$ tokenops pricing refresh
Fetching rates from litellm…
Fetched 42 model rates (as of 2026-07-08T09:00:00Z).

Changes vs baseline (embedded-baseline):
  ~ anthropic/claude-opus-4-8 cache_read 0.5 → 1.5 (+200%)
  ~ mistral/mistral-large input 2 → 3 (+50%), output 6 → 9 (+50%)
  + anthropic/claude-3-opus (added)

Snapshot written: ~/.tokenops/pricing/snapshots/2026-07-08T09-00-00Z.json
```

That first line is the entire point: the Opus error would have **shouted**
`anthropic/claude-opus-4-8 cache_read 0.5 → 1.5 (+200%)` instead of hiding.
Because snapshots now cover every provider, the same refresh also surfaces
drift in a Mistral or DeepSeek row that a hand-maintained baseline had let go
stale — keys sort `"<provider>/<model>"`, so `show` and `diff` group by vendor.

Flags: `--source litellm` (source), `--url` (override the endpoint), `--dir`
(state dir), `--dry-run` (fetch, lint, and diff but do not write).

On any fetch error, `refresh` prints a clear message and **exits non-zero
without writing** — offline callers keep working on the baseline.

### `tokenops pricing show [--snapshot latest|baseline|<ts>] [--json]`

List the rates in a snapshot (default: latest, falling back to baseline).

### `tokenops pricing diff [--from <ts|baseline>] [--to <ts|latest>]`

Diff two snapshots. Default: `baseline → latest`.

### `tokenops pricing lint [--snapshot ...]`

Run the consistency guard over a snapshot and report anomalies. **Exits
non-zero when any are found**, so it can gate CI.

## The consistency guard

The guard's headline check encodes the **Anthropic family invariant** that
would have caught Opus:

- cache-read ≈ **10%** of input (±50% tolerance), and
- output ≈ **5×** input (±40% tolerance).

**This ratio check is Anthropic-family-specific and runs only on `anthropic/*`
rows.** Other providers price on different curves — Gemini Flash output is ~8×
input, cache discounts vary — so applying the Anthropic ratios to them would
false-flag legitimate rates. Now that snapshots span every provider, that
scoping matters: the guard must not cry wolf on a correct OpenAI or Mistral row.

Every row — regardless of provider — additionally gets a **conservative generic
sanity check** that flags only genuine impossibilities (today: a cache-read
priced *above* fresh input, which is never a discount). It is deliberately
narrow so it produces no false positives across the multi-provider catalog.

Rows with a zero input rate, or a zero value in the field being checked, are
skipped — a missing number is not a wrong number. The guard defends against the
*source itself* being wrong and against a future hand-edit re-introducing a
⅓-style error. It runs automatically on `refresh` (as warnings) and on demand
via `lint` (as a CI gate).

## Sandbox / network note

The `refresh` fetch reaches the network. The development sandbox blocks outbound
calls (the same limit as provider live-verify), so **the operator or CI runs the
actual fetch** — `refresh` is built and tested here against fixtures
(`httptest`), never the live feed. Run it where the source is reachable; commit
or ship the resulting snapshot as needed.

## Effective dating (Phase 2)

Phase 2 makes the cost engine *time-aware*. Instead of pricing every event
against one flat table, the engine holds a series of **effective-dated tables**
— one per snapshot, keyed by the snapshot's `fetched_at` — and prices each event
at the table that was **in effect at the event's own timestamp**.

- **Selection rule.** For an event at time `T`, the engine uses the table with
  the greatest `EffectiveFrom ≤ T`. An event that predates every snapshot (or
  carries no timestamp) prices at the **baseline** — the earliest table — so
  costing never fails for lack of a dated table.
- **Baseline is the floor.** The embedded baseline snapshot carries a fixed,
  committed `fetched_at` and always sorts first. Anything before the first real
  refresh prices on the baseline, i.e. on the committed list prices.
- **Each dated table is complete and authoritative.** A snapshot now spans
  every provider the catalog prices, but a source can still omit models, so each
  dated table layers the snapshot's rates onto the full embedded catalog via
  `MergeOverrides` — keyed by `Key{Provider, Model}`, so a fetched Mistral rate
  overrides the Mistral baseline (not just Anthropic) and any provider absent
  from the snapshot keeps its catalog rate. The table selected for an instant is
  authoritative — a model missing from it is a miss (the usual
  `ErrUnknownModel`), *not* a fall-through to a differently-dated table.
- **Overrides still apply.** A negotiated-rate override file (`pricing.path`) is
  layered onto *every* dated table, so your rates hold across all periods.

Concretely: refresh on 2026-08-15 and Anthropic's Opus input rate changes. An
event from 2026-07-20 is priced at the pre-refresh (baseline) rate; an event
from 2026-09-01 is priced at the 08-15 snapshot's rate — from the same engine,
in the same query.

Where it takes effect:

- **Ingest (live proxy).** Costs are stamped at ingest from `Envelope.Timestamp`
  (`Engine.ApplyToEnvelope`), so a request is priced at the rate in effect when
  it happened.
- **Historical recompute.** The analytics aggregator reprices zero-cost events
  (e.g. vendor-usage JSONL that ships tokens but no price) at the effective rate
  for their time bucket.

A baseline-only engine — no refreshes yet — is byte-for-byte identical to the
pre-Phase-2 flat engine (`spend.NewEngine(spend.DefaultTable())`).

### Where it's wired

The effective-dated engine is constructed in the composition root
(`internal/bootstrap`) and in the standalone CLI `spend` / `replay` paths via
`pricing.EffectiveEngine` / `EffectiveEngineWithOverrides`. Construction is
fail-soft: any error building the dated engine degrades to the flat
baseline+override engine, so a bad snapshot dir never breaks costing.
