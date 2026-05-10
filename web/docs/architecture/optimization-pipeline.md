# Optimization pipeline

The optimizer is a pluggable, ordered list of `Optimizer`
implementations. Each one inspects a `Request` and returns
`Recommendation` values; the pipeline decides whether to apply them
based on the request's mode.

## Modes

- **passive** — observe-only. Recommendations are emitted; the
  request body is unchanged. Default for most production paths.
- **interactive** — the pipeline calls a `Decider` for each
  recommendation; accepted ones are applied to the request body
  before the proxy forwards upstream.
- **replay** — runs against historical envelopes via the replay
  engine. The original is preserved; the result is used purely for
  reporting / coaching.

A latency budget caps total wall-clock spent; optimizers that would
push past the cap are skipped with an attributed event so dashboards
can surface lost opportunities.

## Optimizers shipped

| Kind                | Package                                     | What it looks for                                            |
|---------------------|---------------------------------------------|--------------------------------------------------------------|
| `prompt_compress`   | `internal/optimizer/promptcompress`         | Whitespace, smart-quote, comment, dedupe-line transforms     |
| `semantic_dedupe`   | `internal/optimizer/dedupe`                 | Near-duplicate messages by Jaccard shingle similarity        |
| `retrieval_prune`   | `internal/optimizer/retrievalprune`         | Oversized retrieval blocks split by `---` / numbered chunks  |
| `context_trim`      | `internal/optimizer/contexttrim`            | Configurable retention (system always, last N turns, …)      |
| `model_router`      | `internal/optimizer/router`                 | Cost / latency / quality-aware model swap                    |

A `quality_gate` wrapper (`internal/optimizer/qualitygate`) refuses
applies that fall below a quality threshold.

## Adding an optimizer

1. Implement `optimizer.Optimizer` (`Kind()`, `Run(ctx, req)`).
2. Return zero or more `Recommendation` values. Set `ApplyBody` only
   when interactive mode is meaningful for your transform.
3. Add the package to the pipeline at construction time
   (`optimizer.NewPipeline(opt1, opt2, ...)`).
4. Write tests against `optimizer.Pipeline` in passive + interactive
   modes — they exercise the decision plumbing for free.
