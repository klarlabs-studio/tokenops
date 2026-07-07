# Pricing research & snapshots

TokenOps prices model usage from a rate card. Historically that card was a
single hand-maintained file (`internal/contexts/spend/spend/pricing.yaml`) with
no source and no time dimension — which is how the Opus 4.x rows silently sat at
⅓ of real pricing for an unknown period (`$5/$25/$0.50` instead of
`$15/$75/$1.50`). Because the wrong input was *internally consistent* with the
cache-read line, nothing flagged it. **Consistency is not correctness.**

The `tokenops pricing` command implements **Phase 1** of
[ADR 0002](adr/0002-pricing-research-snapshots.md): a researched, sourced,
timestamped, drift-visible pricing framework. It does **not** yet change how
costs are computed — the cost engine still uses the embedded catalog. Phase 1
makes rates *sourced and diffable* so drift is loud instead of silent; Phase 2
wires effective-dated snapshots into the cost path.

## The model

- **Snapshot** — a point-in-time rate card with provenance: `source`,
  `source_url`, `fetched_at`, and `rates` keyed by tokenops model key
  (`claude-opus-4-8`, `claude-3-5-sonnet`, …). Snapshots are Anthropic-scoped in
  Phase 1: the consistency heuristics are a per-family invariant and the drift
  the ADR targets was an Anthropic row.
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
Fetched 7 model rates (as of 2026-07-08T09:00:00Z).

Changes vs baseline (embedded-baseline):
  ~ claude-opus-4-8 cache_read 0.5 → 1.5 (+200%)
  + claude-3-opus (added)

Snapshot written: ~/.tokenops/pricing/snapshots/2026-07-08T09-00-00Z.json
```

That first line is the entire point: the Opus error would have **shouted**
`claude-opus-4-8 cache_read 0.5 → 1.5 (+200%)` instead of hiding.

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

The guard encodes the Anthropic family invariant that would have caught Opus:

- cache-read ≈ **10%** of input (±50% tolerance), and
- output ≈ **5×** input (±40% tolerance).

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

## Scope (Phase 1)

Phase 1 is the framework only. The cost engine still uses
`spend.DefaultTable()`; snapshots are written and inspectable but **not yet
consulted** on the hot path. Effective-dated snapshot selection (pricing a June
event at June's rate) is Phase 2.
