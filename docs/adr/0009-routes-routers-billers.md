# ADR 0009 — Endpoints, routers, and billers

- **Status:** Proposed 2026-10-01
- **Date:** 2026-10-01
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0003 (authoritative cost), ADR 0006 (one coach), ADR 0007 (model policy), ADR 0008 (plan history)

## Context

TokenOps records one `provider` per request. For Claude Code it is
hardcoded to `anthropic`, because Claude Code only speaks Anthropic's API.
That stopped being true once gateways and routers entered the path.

On the maintainer's work machine, FireConnect points Claude Code at
Fireworks (`ANTHROPIC_BASE_URL=https://api.fireworks.ai/inference`) with a
`firerouter` model ID. FireRouter picks a model for each user turn. Easy
turns go to a Fireworks open model (`kimi-k3`, `glm-5p3-flash`), billed by
Fireworks. Hard turns go to Claude, run on the operator's own Anthropic
credential and billed by Anthropic. The response `model` field names the
model that served the turn. One Claude Code session therefore has two
billers and a router TokenOps does not control.

TokenOps reported this as:

- `kimi-k3` and `glm-5p3-flash` under `anthropic`, with no price.
- An Enterprise spend limit compared against usage stamped as covered by
  a plan, so real cost read $0 and headroom read 0%. Usage-based
  Enterprise is billed at API rates from the first token.

The same shape exists elsewhere:

- opencode routes through its own gateways (`opencode`, `opencode-go`)
  besides direct providers. It records `providerID` on every message.
- Codex records `model_provider` in each session's `session_meta`.
- OpenRouter's `openrouter/auto`, LiteLLM and company gateways route on
  the operator's behalf.

At the same time, TokenOps has its own routing: the smart-routing proxy
(ADR 0007 enforcement) and the coach's models power, which moves
subagents to a cheaper model. Two routers deciding the same turn would
produce choices nobody can explain.

## Decision

### 1. Five facts per request, kept separate

| Fact | Meaning | Example |
|---|---|---|
| **Harness** | the client that ran the work | Claude Code |
| **Endpoint** | where the harness sends requests | Fireworks |
| **Router** | who chose the model for this turn | FireRouter, OpenRouter auto, TokenOps, or none |
| **Served model** | the model that answered | `claude-sonnet-5`, `kimi-k3` |
| **Biller** | whose bill, plan or limit the request counts against | Anthropic (Enterprise org), Fireworks |

- `provider` keeps its meaning for compatibility and becomes the biller.
- Spend, plan headroom, spend limits and plan coverage all key on the
  biller, never on the endpoint.
- A FireRouter turn served by Claude on the operator's Anthropic
  credential counts against the Anthropic plan or limit. A turn served by
  a Fireworks open model counts against Fireworks.

### 2. Each endpoint is read from the best source the harness offers

- **opencode:** `providerID` per message. This is exact.
- **Codex:** `model_provider` per session, resolved through
  `[model_providers.*]` in `config.toml`. This is exact per session.
- **Claude Code:** transcripts do not record the endpoint. The daemon
  keeps a dated **route history** (`~/.tokenops/route-history.jsonl`,
  0600), like the plan history.
  - It snapshots the effective endpoint settings from user, project,
    local and managed settings and the launching environment:
    `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`,
    `ANTHROPIC_DEFAULT_*_MODEL`, `CLAUDE_CODE_SUBAGENT_MODEL`,
    `CLAUDE_CODE_USE_BEDROCK` and `CLAUDE_CODE_USE_VERTEX`.
  - Each turn takes the route in force at its timestamp.
  - A namespaced served-model name (`accounts/fireworks/…`, `vendor/model`)
    is a second signal. When it disagrees with the history, the model
    name wins.
- **Cursor:** `openAIBaseUrl` and the per-mode model settings.
- Only setting names and base URLs are read. Credentials and
  `apiKeyHelper` output are never read or stored.

### 3. A biller is resolved from endpoint, served model, and credential

- A direct endpoint is the biller.
- A gateway that serves its own models (Fireworks serverless, OpenRouter,
  opencode Zen) is the biller for those models.
- A router that runs closed models on the operator's own credential
  (FireRouter's "closed-model credentials") bills those turns to the
  model's vendor. A gateway that resells closed models (OpenRouter's
  pass-through) bills them itself.
- Each endpoint declares which case applies in a small catalog. An
  unknown endpoint is reported as such, never guessed.

### 4. Plan coverage follows the plan's kind

- A rate-limited subscription (Max, Pro, Plus) covers its biller's usage,
  which is recorded at $0 as today.
- A spend-denominated plan (usage-based Enterprise) covers nothing. Usage
  is billed at API rates and counts against the spend limit.
- Usage already stored as covered under a spend-denominated plan is
  corrected through the same audited path as `plan set --since`
  (ADR 0008).

### 5. One router decides each turn

- When an external router is in the path (`firerouter…`, `auto`,
  `openrouter/auto`, or a configured gateway marked as routing), TokenOps
  does not choose the model for that turn. The proxy and the coach's
  models power stand down for that harness, and status says why.
- TokenOps governs the router instead, on the autonomy ladder of ADR 0006:
  1. **Policy.** A route ID is a list of models. Each one is checked
     against `model_policy`. A forbidden one is reported at advise, and at
     autonomous the route ID is rewritten to the closest permitted route,
     as proxy enforcement does today.
  2. **Budget per biller.** When one biller nears its limit or window,
     TokenOps proposes moving weight to another. For example, a route
     with an open model, or the router's own preference header. The
     reverse also applies: a subscription with spare window can take turns
     a metered endpoint would bill.
  3. **Quality.** `dx` splits outcomes by served model, so the cheap
     turns a router picks are judged by rework and first-try rate, not
     by price alone.
  4. **Explain.** Every routing record names the router that decided.
- With no external router in the path, TokenOps routes as before.

### 6. Prices come from the biller's own rate card

- Fireworks, OpenRouter and opencode Zen prices are read from their own
  pricing pages or feeds.
- The served-model name is the key. A namespaced name is normalized to
  the biller's catalog name.
- A served model without a price is reported as unpriced, as today. It is
  never priced at the closed vendor's list rate.

## Consequences

- Spend and headroom become correct for gateway and router setups, and
  for usage-based Enterprise.
- The Claude Code route history is a new file the daemon maintains. A
  wrong snapshot misattributes turns, so changes are shown in
  `tokenops status`, and the setup wizard confirms them.
- Proxy routing and the models power do less when an external router is
  present. That is deliberate: two routers cannot be explained.
- Rewriting a router's model ID changes the operator's harness settings.
  It runs only at `autonomous`, with a backup, like every other write
  `init` and `coach preset` make.
- The endpoint catalog is one more list that drifts. Each entry pins its
  source page and date, like the plan catalog.

## Alternatives considered

- **Keep one `provider` and infer the rest from model names.** This breaks
  on routers that return a bare model name, such as FireRouter's
  `claude-opus-5-5`. A Claude turn would be billed to Anthropic correctly,
  but the router that chose it, and the endpoint, would be invisible.
- **Proxy every harness through TokenOps to observe the endpoint.**
  Operators bypass the proxy by choice, and putting TokenOps in front of
  another router adds a hop and a second decision maker.
- **Let TokenOps override external routers.** That means two routers per
  turn, and no explanation that holds.
