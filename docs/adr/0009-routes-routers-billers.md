# ADR 0009 — Endpoints, routers, and billers

- **Status:** Accepted 2026-10-01. Part 1 (§1–4, §6, §7) is implemented. Part 2 (§5): standing down is implemented (2026-10-03); governing the router (policy on route IDs, budget per biller, quality by served model) is open.
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
  - It reads `env.ANTHROPIC_BASE_URL` from managed, `settings.local.json`
    and `settings.json`, at start and every minute, and records changes.
  - A first sighting is dated from evidence when there is some.
    FireConnect snapshots Claude Code's settings
    (`~/.fireconnect/claude/provider-backup.json`) when it first routes
    them through Fireworks, so the file's time dates the switch.
    Without evidence, the first route applies back to the start, as in
    the plan history.
  - Each turn takes the route in force at its timestamp, and records the
    endpoint it went through.
  - A namespaced served-model name (`accounts/fireworks/…`) names its
    biller when no endpoint does.
- **Cursor:** `openAIBaseUrl` and the per-mode model settings. Not yet
  implemented.
- Route detection reads only setting names and base URLs. Keys are read
  only for the account readings in §7, each for the vendor whose
  endpoint it is sent to, and are never stored. The one `apiKeyHelper`
  ever run is FireConnect's.

### 3. A biller is resolved from endpoint, served model, and credential

- A direct endpoint is the biller.
- A gateway that serves its own models (Fireworks serverless, OpenRouter,
  opencode Zen) is the biller for those models.
- A router that runs closed models on the operator's own credential
  (FireRouter's "closed-model credentials") bills those turns to the
  model's vendor. FireConnect passes an API key for this
  (`x-anthropic-api-key` in FireConnect's source), so the vendor bills
  them per token, as API usage.
- A gateway that resells closed models (OpenRouter's pass-through) bills
  them itself.
- Any other non-Anthropic endpoint bills every turn it serves, whatever
  the model is called. DeepSeek's Anthropic API answers `claude-opus`
  requests with its own model.
- Each endpoint declares which case applies in a small catalog keyed by
  host and, where one host bills two ways, by path. z.ai serves its
  coding plan under `/api/anthropic` and `/api/coding`, and its
  pay-as-you-go API under `/api/paas`, which is named `zai-api`. An
  unknown endpoint is reported as such, never guessed.

### 4. Plan coverage follows the plan's kind

- A rate-limited subscription (Max, Pro, Plus) covers its biller's usage,
  which is recorded at $0 as today.
- A spend-denominated plan (usage-based Enterprise) covers nothing. Usage
  is billed at API rates and counts against the spend limit.
- A plan covers only turns through its vendor's own endpoint, with the
  plan's login. A Claude turn through FireRouter runs on an API key, so
  Max does not cover it and it does not count against a claude.ai
  Enterprise limit.
- `pay-as-you-go` binds any provider billed per token to a spend limit
  the operator sets, such as a Fireworks account cap. Spend limits are
  dated in the plan history, so each month keeps its own.
- Plans billed at API rates have no flat fee, and plan cost leaves them
  out.
- Usage recorded under the earlier rules is corrected by the daemon at
  start, without asking, because the mistake was TokenOps': stretch by
  stretch from the route and plan histories. Each correction is in the
  audit log as `cost_correction`, and a second pass changes nothing.

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

**Standing down (implemented 2026-10-03).** A router is recognised from
the model ID a harness asks for: `firerouter…` and `openrouter/auto`,
read from Claude Code's settings (`model`, `env.ANTHROPIC_MODEL` and the
per-class defaults), Codex's `config.toml` and opencode's config. The
route guard and the subagent guard then leave that harness's turns
alone, `tokenops_routing_advise` answers `stay` with `decided_by`, and
the coach report lists the router and says the models power stands down
there. The model policy still holds for a model the agent names itself.

### 6. Prices come from the biller's own rate card

- Model vendors' prices come from LiteLLM's feed. Gateways' and
  coding-plan vendors' prices come from models.dev, which publishes
  each provider's own rates. Rows checked against a vendor's page are
  pinned, and no feed overrides them.
- models.dev lists coding-plan providers (`zai-coding-plan`, …) at $0.
  That is true of the plan and useless as a price, so plan turns are
  valued at the vendor's pay-as-you-go rate.
- opencode Zen turns keep the cost opencode stamps on each message,
  which is what Zen charged.
- The served-model name is the key. A namespaced name is normalized to
  the biller's catalog name.
- A served model without a price is reported as unpriced, as today. It is
  never priced at the closed vendor's list rate.

### 7. Evidence comes autonomously, for every vendor

TokenOps uses the best evidence it can reach without asking, in this
order, and the operator corrects it afterwards:

1. What the harness writes locally: Codex's plan type and windows, the
   endpoint in each harness's settings, opencode's provider per message.
   The local transcript readers are turned on by `init` for every client
   whose sessions are on the machine.
2. The vendor's own reading, through a login already on the machine
   (Copilot, Cursor, claude.ai with OS consent, OpenRouter's key
   endpoint). These add outbound calls, so the setup wizard names each
   one before it is turned on. The exception is a reading that goes only
   to the vendor the operator already sends their work to, with the
   credential their harness already uses for it. The Fireworks reader is
   the first: it is on by default, idle without a Fireworks key, named
   in the docs and switchable. It runs FireConnect's `key export` helper,
   and no other `apiKeyHelper`. The vendor account readers (OpenRouter,
   DeepSeek, Moonshot) follow the same rule: on by default, each called
   only with a key a harness already sends to that vendor's endpoint
   (Claude Code's settings, Codex's providers, opencode's auth.json and
   config), never stored.
3. The operator's entries, only for what nothing can see: past months,
   negotiated prices, a bill in their currency.

Where the operator's entry and the vendor disagree, the vendor wins and
the mismatch is shown.

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
