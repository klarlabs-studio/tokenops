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
Snapshot as of 2026-09:

| Catalog name | Display | Provider | Window cap |
|---|---|---|---|
| `claude-max-5x` | Claude Max 5x | anthropic | ~50 msgs / 5h |
| `claude-max-20x` | Claude Max 20x | anthropic | ~200 msgs / 5h |
| `claude-pro` | Claude Pro | anthropic | ~45 msgs / 5h |
| `gpt-plus` | ChatGPT Plus | openai | model-dependent / 5h |
| `gpt-pro` | ChatGPT Pro (tier unspecified) | openai | model-dependent / 5h |
| `gpt-pro-5x` | ChatGPT Pro Standard ($100) | openai | 5x Plus / 5h |
| `gpt-pro-20x` | ChatGPT Pro More usage ($200) | openai | 20x Plus, 10x from 2026-10-30 / 5h |
| `gpt-pro-500` | ChatGPT Pro Max usage ($500) | openai | 25x Plus / 5h |
| `gpt-business` | ChatGPT Business | openai | model-dependent / 5h |
| `copilot-individual` | GitHub Copilot Individual | github | no published cap |
| `copilot-business` | GitHub Copilot Business | github | no published cap |
| `cursor-pro` | Cursor Pro | cursor | 500 requests / month |
| `cursor-business` | Cursor Business | cursor | 500 requests / month |
| `zai-glm-coding-lite` / `-pro` / `-max` | z.ai GLM Coding | zai | 2,000 / 12,000 / 28,000 credits / 5h |
| `opencode-go` / `-plus` | opencode Go | opencode-go | dollar cap per model; 5h = 20% of it |
| `alibaba-coding-pro` | Alibaba Cloud Coding Plan Pro | alibaba | 6,000 requests / 5h |
| `minimax-token-plus` / `-max` / `-ultra` | MiniMax Token Plan | minimax | 5h, no published count |
| `cerebras-code-pro` / `-max` | Cerebras Code | cerebras | 24M / 120M tokens / day |
| `synthetic-pack` | Synthetic | synthetic | 500 requests / 5h per pack |

A coding plan covers only turns through its own endpoint: z.ai's
`/api/anthropic` and `/api/coding`, not its pay-as-you-go `/api/paas`.
opencode records which one it used per message (`zai-coding-plan` versus
`zai`); for Claude Code and Codex the endpoint comes from their settings
(ADR 0009). z.ai's Pro and Max tiers carry no list price: their prices are only on
a storefront TokenOps could not read, so plan cost
uses what you record with `--price`.

Window caps reflect the vendor's published rate-limit window. When a vendor
publishes model-dependent ranges rather than one plan-wide cap, TokenOps keeps
the window but does not invent a single message denominator; authoritative
usage-meter percentages drive headroom instead.

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

- `consumed_tokens`, `consumed_pct` — monthly usage when the plan
  publishes a token cap.
- `headroom_days` — extrapolated from rolling 7-day burn rate.
- `window_consumed`, `window_cap`, `window_unit`, `window_pct`,
  `window_duration`, `window_resets_in` — rolling rate-limit window usage.
  When the vendor meter reports the duration, that value overrides the
  catalog fallback; Codex plan variants do not always expose the same primary
  and secondary windows.
- `vendor_plan_type` — the provider's own plan identifier when present.
  This is distinct from `plan_name`, which is the operator's canonical
  TokenOps configuration such as `gpt-plus` or `gpt-pro-5x`.
- `vendor_plan` — set only when `vendor_plan_type` names a different
  catalog plan than the one configured. A catalog plan lists the vendor
  identifiers observed for it (`plus` → `gpt-plus`, `prolite` →
  `gpt-pro-5x`, `pro` → `gpt-pro-20x`). OpenAI does not document these
  values; `prolite` was confirmed on 2026-10-01 against an account whose
  ChatGPT pricing screen showed Pro Standard as its current plan. The
  mismatch is reported with the `plan set` command that fixes it, never
  applied: the binding stays the operator's.
- `overage_risk` — `low`, `medium`, `high`, or `unknown`. The headline
  takes the worse of the monthly and window signals.
- `note` — populated when math falls through (e.g. plan publishes no
  cap at all, or there isn't yet ≥7d of plan-included traffic).

Example output for Claude Max 20x:

```
Claude Max 20x (claude-max-20x) — risk low
  tokens this month: 478732 (no monthly cap)
  window:  27 / 200 messages per 5h (13.5%) — resets in 5h0m0s
```

The same report is available via the `tokenops_glance (view=headroom)` MCP tool.

Claude Code JSONL supplies genuine per-turn token and cache usage but not an
authoritative subscription quota percentage. Until the Claude usage meter is
connected, its message-window percentage and reset remain estimates and are
marked accordingly. Codex JSONL carries vendor-reported window duration,
utilization, reset time, and plan type, so those fields are authoritative even
when they differ from the configured catalog fallback.

A browser-visible usage page can confirm the operator's current plan and quota
dimensions, but it is not an ingestion source. Do not transcribe those values
into the event store or report the meter as connected. The daemon requires the
session-authenticated usage response so every recorded percentage retains its
vendor timestamp and reset provenance.

Claude's current public web client reads a unified `limits` array whose entries
carry a vendor `kind`, percentage, reset timestamp, and optional model or
surface scope. TokenOps accepts that contract and the historical top-level
window blocks. Aggregate session and weekly entries retain the compatibility
attributes used by headroom; other entries retain their vendor kind and scope
instead of relying on a fixed model-name allowlist.

## Adding a custom plan

1. Add an entry to the `catalog` map in
   `internal/contexts/spend/plans/plans.go` with a dated `SourceURL`.
2. Run `go test ./internal/contexts/spend/plans/...` to confirm the
   catalog validation tests still pass.
3. Update the table above.
