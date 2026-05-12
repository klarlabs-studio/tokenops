# Plan-Based Cost Model

TokenOps tracks two cost regimes side by side:

- **Metered**: per-token billing through the provider's pay-as-you-go
  API. Captured as `cost_usd` on `PromptEvent`.
- **Plan-included**: flat-rate subscriptions (Claude Max, ChatGPT Plus,
  GitHub Copilot, Cursor, etc.). `cost_usd` is zero; the request rolls
  up to a monthly plan quota and rate-limit window.

A request is treated as plan-included when its `PromptEvent.CostSource`
is `plan_included`. `trial` events are likewise zero-cost. Empty (or
explicit `metered`) routes through the normal pricing table.

## Configuring a plan

Two ways to bind a provider to a plan:

```yaml
# ~/.config/tokenops/config.yaml
plans:
  anthropic: claude-max
  openai: gpt-plus
```

Or via env:

```bash
export TOKENOPS_PLAN_ANTHROPIC=claude-max
export TOKENOPS_PLAN_OPENAI=gpt-plus
```

The daemon validates plan names at startup; an unknown plan fails with
the full catalog listed as the suggestion set.

## Supported plans

`tokenops plan catalog` enumerates every plan TokenOps knows about.
Snapshot as of 2026-05:

| Catalog name | Display | Provider |
|---|---|---|
| `claude-max` | Claude Max | anthropic |
| `claude-pro` | Claude Pro | anthropic |
| `claude-code-max` | Claude Code (Max plan) | anthropic |
| `claude-code-pro` | Claude Code (Pro plan) | anthropic |
| `gpt-plus` | ChatGPT Plus | openai |
| `gpt-pro` | ChatGPT Pro | openai |
| `gpt-team` | ChatGPT Team | openai |
| `copilot-individual` | GitHub Copilot Individual | github |
| `copilot-business` | GitHub Copilot Business | github |
| `cursor-pro` | Cursor Pro | cursor |
| `cursor-business` | Cursor Business | cursor |

Each entry in `internal/contexts/spend/plans/plans.go` carries a
dated `SourceURL` pinning the vendor page that documents its limits.
When a vendor updates their plan, refresh both the numbers and the
URL in the same PR so drift surfaces in review.

## Reading headroom

```
tokenops plan headroom
tokenops plan headroom --json
```

Returns a `HeadroomReport` per configured plan with:

- `consumed_tokens`, `consumed_pct`
- `headroom_days` — extrapolated from rolling 7-day burn rate
- `overage_risk` — one of `low`, `medium`, `high`, `unknown`
- `note` — populated when math falls through (e.g. plan publishes only
  rate-limit windows, not a monthly cap)

The same report is available via the `tokenops_plan_headroom` MCP tool.

## Demo mode

`tokenops demo --plan claude-max` stamps every PromptEvent for the
plan's provider with `cost_source=plan_included` and zero `cost_usd`,
so the headroom surface returns non-zero consumption on a fresh
install.

## Adding a custom plan

1. Add an entry to the `catalog` map in
   `internal/contexts/spend/plans/plans.go` with a dated `SourceURL`.
2. Run `go test ./internal/contexts/spend/plans/...` to confirm the
   catalog validation tests still pass.
3. Update the table above.
