# Coverage

TokenOps instruments AI usage on **three planes**. Which ones a given client
supports is the whole integration story — there is no single "install," just
these three surfaces, in order of decreasing passivity.

| Plane | What it needs | What you get |
|---|---|---|
| **Passive read** | TokenOps reads logs/DB the client already writes | Per-turn token attribution, zero wiring |
| **MCP** (`tokenops serve`) | The client is an MCP host | The agent *calls* TokenOps (budget, headroom, coach, status) |
| **Proxy** (base-URL override) | The client lets you set its API base URL | Ground-truth token/cost accounting + live optimization |

The atomic unit is a **turn** (one request→response), stamped
`agent_id = <client>:<project>` and `workflow_id = <client>:<project>:<session>`,
so everything rolls up **turn → session → project**.

## Clients

| Client | Passive read | MCP | Proxy | Notes |
|---|:--:|:--:|:--:|---|
| Claude Code | ✅ `~/.claude/projects` | ✅ | ✅ `ANTHROPIC_BASE_URL` | reference integration; also the `read-guard` hook |
| Codex CLI | ✅ `~/.codex/sessions` | ✅ | ✅ `OPENAI_BASE_URL` | reader surfaces OpenAI's official rate-limit % |
| opencode | ✅ SQLite store | ✅ | ✅ per-provider baseURL | reader is multi-provider |
| Gemini CLI | ❌ *(no token log)* | ✅ | ✅ base-URL override | its `logs.json` records prompts only — no token data |
| Desktop apps | ❌ | ✅ *(if MCP host)* | ❌ *(no base-URL override)* | MCP tools only; Anthropic cookie for Max % |
| Jules / hosted | ❌ | ❌ | ❌ | out of reach — see Boundaries |

## Providers (proxy plane)

Every provider below is routable through the proxy. Bind one with
`tokenops provider set <name>` (omit the URL to use the built-in preset);
`tokenops provider list` shows them all.

| Provider | Preset base URL |
|---|---|
| openai | `https://api.openai.com` |
| anthropic | `https://api.anthropic.com` |
| gemini | `https://generativelanguage.googleapis.com` |
| mistral | `https://api.mistral.ai` |
| groq | `https://api.groq.com/openai` |
| deepseek | `https://api.deepseek.com` |
| xai | `https://api.x.ai` |
| perplexity | `https://api.perplexity.ai` |
| fireworks | `https://api.fireworks.ai/inference` |
| cerebras | `https://api.cerebras.ai` |
| together | `https://api.together.xyz` |
| openrouter | `https://openrouter.ai/api` |
| cohere | `https://api.cohere.com` |
| ollama | `http://localhost:11434` *(local, no key)* |
| lmstudio | `http://localhost:1234` *(local, no key)* |
| litellm | `http://localhost:4000` *(self-hosted gateway)* |
| vercel | `https://ai-gateway.vercel.sh` |

**OpenRouter is the universal fallback for ground truth.** Any client with no
local reader but a base-URL override can route through OpenRouter-via-TokenOps
and every turn becomes visible.

Cohere is not OpenAI-wire-format — it has a dedicated normalizer for its v2
(`/v2/chat`) and v1 (`/v1/chat`) shapes — but auth is a `Bearer` header so the
passthrough proxy handles it.

Pricing ships for the single-model-family providers
(groq/deepseek/xai/perplexity/cerebras/cohere). Fireworks, Together, and OpenRouter
multiplex arbitrary third-party models under namespaced names, so a static
rate card can't price them accurately — token counts are still metered; attach
`$` cost via a pricing override (below).

### Pricing overrides & drift

The built-in rate card is **public list prices captured at a point in time** —
they drift, and negotiated/enterprise rates differ. Layer your own rates over
the defaults without touching the binary:

```yaml
# ~/.config/tokenops/config.yaml
pricing:
  path: ~/.config/tokenops/pricing-override.yaml   # or env TOKENOPS_PRICING_PATH
```

The override file uses the **same schema** as the built-in catalog (USD per
million tokens; `*` = prefix match, longest wins) and is *merged* over the
defaults — a matching provider+model row replaces that rate, a new row adds one.
This is also how you price the multiplexers: copy the model string TokenOps
records (`RequestModel`) verbatim and give it the underlying model's rate. A
ready-to-copy example lives at
[`docs/examples/pricing-override.yaml`](https://github.com/klarlabs-studio/tokenops/blob/main/docs/examples/pricing-override.yaml).

## How an agent bootstraps setup

The MCP surface is self-describing. An agent calls `tokenops_status` and gets
back `signal_quality.level` plus `blockers[]` and `next_actions[]` — the exact
commands to upgrade — and `tokenops_data_sources` reports which planes are live.
So an agent on a fresh install can tell you what to run to give it better data.

## Boundaries

These are honest limits of a local-first, no-telemetry tool — not gaps to fill:

- **Gemini CLI cannot be metered passively.** Its `logs.json` is a prompt log
  with no token usage. Use the proxy plane instead.
- **AWS Bedrock is not proxy-metered.** It requires SigV4 request signing; the
  proxy is pure passthrough with no per-provider auth hook.
- **Jules and fully-hosted agents are out of reach.** No local logs, no MCP host
  you control, no proxy you can insert. TokenOps can only instrument agents that
  run where you can read their logs, mount an MCP server, or sit in front of
  their API.
