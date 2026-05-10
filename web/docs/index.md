---
layout: home
hero:
  name: TokenOps
  text: Local-first LLM observability + optimization
  tagline: A drop-in proxy for OpenAI, Anthropic, and Gemini that observes every prompt, replays sessions through optimizers, and forecasts spend — without leaking data off your machine.
  actions:
    - theme: brand
      text: Quickstart
      link: /guide/quickstart
    - theme: alt
      text: GitHub
      link: https://github.com/felixgeelhaar/tokenops
features:
  - title: Drop-in proxy
    details: One env var (OPENAI_BASE_URL, ANTHROPIC_BASE_URL, …) sends every request through the local daemon. No SDK fork, no patching.
  - title: Local-first storage
    details: SQLite by default. Your prompts never leave the machine unless you explicitly enable the OTLP exporter.
  - title: Optimizer pipeline
    details: Prompt compression, semantic dedupe, retrieval pruning, context trimming, model routing — all observed, optionally applied.
  - title: Replay + coaching
    details: Run last week's sessions through the optimizer offline. See exactly what you would have saved.
  - title: <50ms p99 overhead
    details: Bench-gated in CI. The proxy is on the request hot path; we treat latency as a feature.
  - title: MCP-native
    details: An MCP server ships with the daemon so your favorite LLM client can query spend, forecasts, and traces directly.
---
