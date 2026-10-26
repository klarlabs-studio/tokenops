# Research: agent cost, caching and routing (October 2026)

This note lists recent papers, engineering posts and products that bear on what TokenOps does. For each, it says what TokenOps could take from it. It records where the October 2026 work came from and what is left. Every arXiv link was checked against its abstract page. The vendor posts are summarised from their own pages where those could be read. A figure quoted only from a secondary write-up is marked *(secondary)*.

Status: **building** means in progress or shipped; **later** means recorded, not started.

## Papers

| Source | Finding | For TokenOps | Status |
|---|---|---|---|
| [Unsupervised Cycle Detection in Agentic Applications](https://arxiv.org/abs/2511.10650) (IBM, 2025) | Structural repetition alone detects agent loops poorly (F1 0.08); adding a progress or semantic signal raises it to 0.72. | Loop breaker: repeated calls *with no file changed*, at the pause point. | building (#746) |
| [EET: Experience-Driven Early Termination](https://arxiv.org/html/2601.05777) (2026) | Stopping when a run exceeds what similar successful runs cost, with no progress, cut cost 19–55% for under 0.2% lost resolution. | The loop breaker's second signal: spend past the p90 of successful tasks of the same kind. Needs the task kind at the pause point. | later |
| [How Do AI Agents Spend Your Money?](https://arxiv.org/abs/2604.22750) (2026) | Runs of one task vary up to 30× in tokens; agents' own cost guesses correlate at most 0.39 with the truth. | `prepare_work` reports p50 and p90 from local history, never the agent's estimate. | building |
| [Budget-Aware Tool Use (BATS)](https://arxiv.org/abs/2511.17006) (2025) | A bigger tool budget helps only when the agent can see what is left. | A short headroom note in the agent's per-turn context. | building |
| [Don't Break the Cache](https://arxiv.org/abs/2601.06007) (2026) | Dynamic content and tool-set changes in the prefix invalidate prompt caching. | Explain each cache rebuild by cause; lint the always-loaded context for volatile content. | building (#747, lint) |
| [SWE-Router](https://arxiv.org/abs/2607.00053) (2026) | Deciding weak versus strong after the cheap model's first turns keeps most of the strong model's results at much lower cost. | Autopilot starts subagents cheap and escalates on signals TokenOps already has (tool errors, re-reads, no edits). Shadow first. | later |
| [Task-to-Model Optimization (T2MO)](https://arxiv.org/abs/2608.08528) (2026) | Minimising token cost fails once retries and escalations count; optimise cost per completed task, with a minimum pass rate before a cheaper model pays off. | Rank autopilot routes by cost per achieved outcome; show the break-even pass rate per task kind. | later |
| [Scrouting](https://arxiv.org/abs/2608.04804) (2026) | A cheap repository "scout" handoff let the cheapest fixer match the best model at about a fifth of the cost per solve. | A coach recommendation for an explore-then-implement split, measured with `tokenops experiment`. | later |
| [Agent Psychometrics](https://arxiv.org/abs/2604.00594) (2026) | An item-response model separates task difficulty from agent ability and predicts per-task success. | Routing advice that states its confidence per task kind. | later |
| [The Complexity Trap](https://arxiv.org/abs/2508.21433) (NeurIPS 2025 DL4C) | Masking old tool outputs halves cost and matches LLM summarisation. | A coach tip when stale tool output dominates a session's context. | later |
| [SWE-Pruner](https://arxiv.org/abs/2601.16746) (2026) | A goal-aware line skimmer cut 23–54% of tokens on SWE-bench Verified. | Let `fmt` keep the lines that match the agent's stated goal. | later |

## Engineering posts

| Source | Finding | For TokenOps | Status |
|---|---|---|---|
| [Anthropic: multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system) | Agents use about 4× the tokens of chat, and multi-agent systems about 15×. | A subagent fan-out multiplier per session, and a boundary on subagent count. | later |
| [Anthropic: effective context engineering](https://anthropic.com/engineering/effective-context-engineering-for-ai-agents) | Context is a finite attention budget; compaction, notes and subagents manage it. | Link coach tips to the technique they apply. | later |
| [Manus: context engineering](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus) | KV-cache hit rate is the key metric; a timestamp at the start of the prompt kills it. | The volatile-prefix lint. | building |
| [OpenAI: Unrolling the Codex agent loop](https://openai.com/index/unrolling-the-codex-agent-loop) | The previous prompt must be an exact prefix of the next; changing MCP tools breaks it *(secondary)*. | Rebuild causes for Codex, once its sessions record cache writes. | later |
| [Cursor: dynamic context discovery](https://cursor.com/blog/dynamic-context-discovery) | Moving MCP tool descriptions to files cut tokens 46.9% on MCP runs; long outputs go to files. | `fmt` writes very long output to a file and returns its tail and the path. | later |
| [Factory: evaluating context compression](https://factory.ai/news/evaluating-compression) | Measure tokens per task, not per request *(scores secondary)*. | Score each compaction by the re-reads that follow it. | later |
| [Amp: rush mode](https://ampcode.com/news/rush-mode) | The cheap mode was 77% cheaper on a small task but 19% cheaper and slower on a complex refactor. | Evidence for pricing rework into routing. | later |
| [GitHub: Copilot auto model selection](https://github.blog/changelog/2025-12-10-auto-model-selection-is-generally-available-in-github-copilot-in-visual-studio-code/) | Auto selection gets a 10% discount on the premium-request multiplier. | The Copilot reader should apply multipliers to headroom. | later |

## Products

| Product | Relevant | For TokenOps |
|---|---|---|
| [OpenRouter Auto Router](https://openrouter.ai/docs/guides/routing/routers/auto-router) | Ranks models per task type from community usage. | Teams could opt in to sharing derived outcome rates per task kind and model as routing priors. |
| [LiteLLM budget routing](https://docs.litellm.ai/docs/proxy/provider_budget_routing) | Provider, model and tag budgets filter which deployments can serve. | Allocation tags as budget scopes. |
| [Portkey budget limits](https://portkey.ai/docs/product/ai-gateway/virtual-keys/budget-limits) | Hard dollar or token caps per key. | Nothing new; boundaries already cover it. |
| [Vantage: Claude Enterprise costs](https://www.vantage.sh/blog/anthropic-analytics) | Daily per-user Claude Enterprise analytics. | Check that the FOCUS export loads into Vantage, and document it. |

## Order of work

Built first, chosen in October 2026: the loop breaker (shadow), cache-rebuild causes with a cold-cache countdown, the volatile-prefix lint, and spread-aware `prepare_work` with the headroom note. Next by value for effort: outcome-priced routing with a break-even pass rate, `fmt` writing long output to a file, the subagent fan-out multiplier, escalation after k turns in shadow, and a compaction quality score.
