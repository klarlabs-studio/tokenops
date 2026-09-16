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
| Cursor | ⚠️ quota only | ✅ | ❌ *(agent traffic goes through Cursor's backend)* | the usage cookie gives plan consumption, not per-turn tokens |
| Gemini CLI | ❌ *(no token log)* | ✅ | ✅ base-URL override | its `logs.json` records prompts only — no token data |
| Desktop apps | ❌ | ✅ *(if MCP host)* | ❌ *(no base-URL override)* | MCP tools only; Anthropic cookie for Max % |
| GitHub-hosted (Copilot agent) | ⚠️ quota only | ❌ | ❌ | the Copilot quota endpoint gives bucket %, nothing per-turn |
| Jules / hosted | ❌ | ❌ | ❌ | out of reach — see Boundaries |

## What reaches which client

The plane table above is about where the *data* comes from. This one is
about what you actually get, which is the question you have when choosing
a client — and the honest answer is that parity does not exist.

Published rather than papered over. A matrix with gaps in it is worth
more than a promise of everything: you can plan around a gap, and the
gaps here are consequences of where a client keeps its state, not of what
we got round to.

| Capability | Needs | Claude Code | Codex CLI | opencode | Cursor | Gemini CLI | Desktop | GitHub-hosted |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| Spend + token accounting | a local token log | ✅ | ✅ | ✅ | ⚠️ | ❌ | ❌ | ⚠️ |
| Ground truth + routing *enforcement* | a base-URL override | ✅ | ✅ | ✅ | ❌ | ✅ | ❌ | ❌ |
| Routing *advice* (`tokenops_routing_advise`) | an MCP host | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ |
| Prompt + reply coaching | prompt text on disk | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| Agent DX metrics (`dx`) | a transcript reader | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| Work storytelling (`story`) | a reader **and** prompt text | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| Proactive coaching (nudges) | an end-of-turn hook | ✅ | ✅ | ⬜ | ✅ | ❌ | ❌ | ❌ |
| `read-guard` (intervene) | a **blockable** file-read hook | ✅ | 🚫 | ⬜ | 🚫 | ❌ | ❌ | ❌ |
| `tokenops fmt` compression | the agent runs shell commands | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| MCP tools (ask anything) | an MCP host | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ |

⬜ means possible and not built yet. 🚫 means the client cannot support it
— not a backlog item. ⚠️ means partial, and each one means something
specific:

- **Cursor and GitHub-hosted spend** is *quota*, not tokens. Both expose a
  consumption endpoint the vendor's own UI reads — how much of the plan
  is gone — and neither exposes per-turn token counts. Useful for
  headroom; useless for "which project burns the most".

### Coaching is pull-only on Desktop and GitHub clients

Claude Desktop, Codex Desktop and the GitHub clients have **no local
token log and no base-URL override**. There are no transcripts to read,
which means no storytelling, no proactive nudges, and no routing
*enforcement* — nothing TokenOps does *to* a session is reachable,
because nothing of the session is reachable.

What is left is the MCP surface, and on those clients it is the whole
product. So it is the one that has to be genuinely good there: every
question — spend, headroom, burn rate, forecast, the work account, the DX
metrics, which model this turn should run on — is a tool call, and
`tokenops_help` lists what the running server exposes. That is a real answer, just a smaller one than a client
whose session we can sit inside.

Parity is not available on those clients and will not become available;
saying so is the differentiator against a matrix that promises everything.

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
- **Desktop and GitHub clients get the MCP surface and nothing else.** No local
  token log and no base-URL override means no transcripts, so coaching there is
  pull-only. See the capability matrix above.
- **`read-guard` is permanently unavailable on Codex and Cursor**, for two
  different reasons and neither of them ours. Codex has no file-read tool
  at all: across 40 real rollouts every tool call was `exec_command`, MCP,
  `wait`, `write_stdin` or `request_user_input`, so reading a file is a
  shell command and there is no read to intervene in. Cursor has
  `beforeReadFile`, but only `beforeShellExecution` and
  `beforeMCPExecution` honour a permission decision — its own docs call
  `beforeReadFile` a tripwire, not a lock. `tokenops hooks install
  --read-guard --client codex` refuses and says so rather than writing a
  hook that never fires.
- **Cursor's reader is the least exercised.** It reads the same structure as
  the others and carries prompt text, but no Cursor store was available to
  verify it against real history the way Claude Code, Codex and opencode were.
  Treat its numbers as unconfirmed until you have checked them against your
  own sessions.
