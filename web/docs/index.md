---
layout: home
hero:
  name: TokenOps
  text: Predict Claude Max rate-limit cutoffs from inside Claude Code
  tagline: Local MCP server + CLI that watches your Claude Max / ChatGPT Plus / Copilot / Cursor plan window and tells the agent — before you hit the cap — to slow down, switch model, or wait for reset. Brew install. Zero data leaves your machine.
  actions:
    - theme: brand
      text: 90-second quickstart
      link: /guide/quickstart
    - theme: alt
      text: Star on GitHub
      link: https://github.com/felixgeelhaar/tokenops
features:
  - title: Built for Claude Code, Cursor, aider
    details: 22 MCP tools agents can call directly. `tokenops_session_budget` returns `recommended_action ∈ continue|slow_down|switch_model|wait_for_reset` with a confidence band. No SDK changes, no proxy required to start.
  - title: Honest signal quality
    details: Every response carries `signal_quality.level`. The MCP-ping heuristic is labelled `low` by default; wire the proxy for `high`. We never pretend a guess is a guarantee.
  - title: Three commands, ninety seconds
    details: '`brew install felixgeelhaar/tap/tokenops` → `tokenops init` → `tokenops plan set anthropic claude-max-20x` → ask your agent for `tokenops_session_budget`.'
  - title: Plan catalog out of the box
    details: 'Claude Max 5x / 20x, Claude Pro, Claude Code (Max+Pro plans), ChatGPT Plus / Pro / Team, GitHub Copilot Individual / Business, Cursor Pro / Business. Dated vendor source URLs pinned in code.'
  - title: Local-first, open source
    details: SQLite database. No cloud account. No telemetry. Your prompts, your machine. Apache 2.0.
  - title: Demo data isolation
    details: 'Synthetic events stay out of every default rollup. `tokenops demo --reset-only` purges them. `tokenops_data_sources` shows the real-vs-seeded ratio at a glance.'
---

## Why TokenOps exists

Anthropic, OpenAI, GitHub, and Cursor publish *rate-limit windows*, not monthly token caps, for their flat-rate plans. The vendor dashboard tells you you've hit the cap **after** the cap hits. There's no early signal in the agent context where you're actually working.

TokenOps closes that gap. It runs as an MCP server inside Claude Code (or Cursor, or aider, or any MCP host), observes activity, and surfaces a single tool — `tokenops_session_budget` — that the agent can call mid-conversation to decide whether to keep going.

## Honest about what it sees today

The default install observes **MCP tool invocations**, not your raw Claude conversation turns. The product tells you so in every response (`signal_quality.level: low`). Wire the local proxy and it sees every Claude request directly (`signal_quality.level: high`). Vendor /usage API ingestion is on the roadmap — when it lands, you get the highest-quality signal with zero proxy config.

## Who this is for

- Solo founders running Claude Code or Cursor 6+ hours a day on client work
- Staff engineers on Claude Max 20x billing time against AI sessions
- Anyone who's lost focus to a mid-refactor rate-limit cutoff

If you don't recognise the struggling moment, this isn't the product for you yet.

## Looking for early users

If you'd trade a 15-minute call for hands-on help wiring TokenOps to your workflow, [open an issue on GitHub](https://github.com/felixgeelhaar/tokenops/issues/new) or DM `@felixgeelhaar`. The current focus is the first ten real users, not the first thousand stars.
