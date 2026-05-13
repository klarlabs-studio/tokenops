---
layout: home
hero:
  name: TokenOps
  text: Predict rate-limit cutoffs inside your AI agent
  tagline: Local MCP server + CLI that watches your flat-rate AI subscription window — Claude Max, ChatGPT Plus / Pro / Team, GitHub Copilot, Cursor — and tells the agent, before you hit the cap, to continue, slow down, switch model, or wait for reset.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
    - theme: alt
      text: Star on GitHub
      link: https://github.com/felixgeelhaar/tokenops
features:
  - title: Eleven plans in the catalog
    details: 'Claude Max 5x / 20x, Claude Pro, Claude Code (Max + Pro), ChatGPT Plus / Pro / Team, GitHub Copilot Individual / Business, Cursor Pro / Business. Each with a dated vendor source URL pinned in code.'
  - title: Provider-agnostic by design
    details: 'OpenAI, Anthropic, Google Gemini all flow through the same proxy and event schema. Spend, optimizer, scorecard, MCP tools — every surface treats them as peers, not special cases.'
  - title: Honest signal quality
    details: Every prediction carries `signal_quality.level` (low / medium / high) plus a one-line caveat. Heuristic mode is labelled; proxied mode is labelled; the product never pretends a guess is a guarantee.
  - title: MCP-first
    details: '22 MCP tools agents can call directly. `tokenops_session_budget` returns `recommended_action ∈ continue|slow_down|switch_model|wait_for_reset`. Works with Claude Code, Cursor, aider, Codex, and every other MCP host.'
  - title: Three commands, ninety seconds
    details: '`brew install felixgeelhaar/tap/tokenops` → `tokenops init` → `tokenops plan set <provider> <plan>` → ask your agent for `tokenops_session_budget`.'
  - title: Local-first, open source
    details: SQLite database. No cloud account. No telemetry. Apache 2.0. Demo-data isolation by default so synthetic seeds never contaminate the real signal.
---

## Why TokenOps exists

Every major vendor — Anthropic, OpenAI, GitHub, Cursor, Google — publishes *rate-limit windows*, not monthly token caps, for their flat-rate plans. The vendor dashboard tells you you've hit the cap **after** the cap hits. There's no early signal in the agent context where you're actually working.

TokenOps closes that gap for every provider it tracks. One CLI, one MCP server, one event schema. Configure as many plans as you use:

```bash
tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-plus
tokenops plan set github copilot-business
tokenops plan set cursor cursor-pro
```

Every plan you bind contributes to a unified headroom view your agent can query mid-conversation.

## Honest about what it sees today

The default install observes **MCP tool invocations** as an activity proxy — every response says so. Wire the local proxy and TokenOps sees every request flowing through it (`OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `GEMINI_BASE_URL`). Vendor `/usage` API ingestion is on the roadmap — it lands the highest-quality signal with zero proxy config.

## Who this is for

- Solo founders running coding agents 6+ hours a day across multiple AI subscriptions
- Staff engineers billing client time against AI sessions, juggling Claude + GPT + Copilot stacks
- Anyone who's lost focus to a mid-task rate-limit cutoff on any provider

If you don't recognise the struggling moment, this isn't the product for you yet.

## Looking for early users

If you'd trade a 15-minute call for hands-on help wiring TokenOps to your workflow, [open an issue on GitHub](https://github.com/felixgeelhaar/tokenops/issues/new) or DM `@felixgeelhaar`. Current focus is the first ten real users — across any provider mix.
