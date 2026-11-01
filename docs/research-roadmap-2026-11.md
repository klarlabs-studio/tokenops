# Research roadmap, November 2026

What the November 2026 review of the research and the field changes in
TokenOps, in the order it is built. Each step is one pull request. The
evidence behind a step is named with it; where it is a preprint or one
vendor's account, it says so.

Two findings set the order. First, a correctness gap: Claude Code writes
each subagent's turns to `<session>/subagents/agent-<id>.jsonl`, and
TokenOps reads only `<project>/*.jsonl`. On one operator's machine that
leaves out about 27% of a week's tokens and 35% of its output, so spend,
waste and units undercount wherever subagents run. Second, a replay study
of SWE-bench runs (arXiv 2608.08239) found that a shadow log cannot say
what a different model would have done: autopilot's promotion needs a
randomized comparison, not a longer shadow period.

## Steps

1. **Read subagent transcripts.** Ingest `<session>/subagents/agent-*.jsonl` alongside the session's own file, with the subagent's id, type and description from its `.meta.json`, and the tool call that started it. Turns already read are not counted twice. Acceptance: on a fixture with a session and two subagents, `tokenops spend` counts every subagent turn once; the waste report and units include them; a re-poll adds nothing.
2. **The read guard forgets what a compaction dropped.** After `/compact` or `/clear` the agent no longer holds the files it read, so a re-read must not be refused. Register the compaction hook and clear the session's read state on it. Acceptance: a read refused before a compaction is allowed after it; refusals before it are unchanged.
3. **What ate my week.** `tokenops spend --by session|subagent|role` and a breakdown in `tokenops spend waste` that names background calls (a subagent run in the background, Claude Code's own summary calls), subagent spawns and cache rebuilds, with each one's share of the week. `tokenops explain` names the share that tripped a window. Evidence: practitioner reports of hidden background spend (anecdotal); TokenOps' own subagent gap above.
4. **Reverts count against the work they undo.** Detect `git revert` commits (`This reverts commit <sha>`) within 14 days of the commit they undo, mark that unit of work reverted, and report the share of spend on work not achieved or reverted ("expensive failures") in `tokenops spend units`. Evidence: SWE-Effi (arXiv 2509.09853); revert rates vary by vendor and window, so the window is recorded with the figure.
5. **Autopilot earns its moves against a holdout.** A small share of eligible subagent moves (default 10%) is held on the default model, chosen at random and logged with its probability; each move and each holdout is a decision that `tokenops_outcome` and reverts can settle. When the moved runs do worse than the holdout by more than the noise, autopilot steps itself down from acting to asking, and `tokenops explain` shows the comparison. Evidence: the replay study above; LLMRouterBench (arXiv 2601.07206) on routers that do not beat simple baselines; AgentOpt (arXiv 2604.06296) on per-role coupling.
6. **Routing reads the pace, per turn.** The per-turn model advice reads plan pressure the way subagent routing already does, and the headroom note given to the agent says when the binding window runs out at this pace, not only what is left. Evidence: Budget Tracker (arXiv 2511.17006, web agents) on telling an agent its budget; no published work models rolling plan windows.
7. **The read guard covers repeat commands.** A read-only command (`git status`, `git diff`, `git log`, `ls`, `cat`, `grep`, `rg`, `find`) repeated in the same session on an unchanged working tree is answered by pointing to the earlier result instead of running it again, in observe mode first like the read guard. Evidence: Ledger (arXiv 2608.00808, preprint); CORVUS (arXiv 2607.22711) on duplicate reads.
8. **The coach speaks on loops and churn.** New tips for a search loop (the same search repeated without progress) and for re-read churn (the same file read three times in ten turns), naming the file or the search, given at the next tool call rather than at the end of the turn. Evidence: Nudge (TOSEM; 60% shorter PR lifetimes from object-specific nudges); TraceProbe (arXiv 2607.06184) on search loops as the stable failure pattern; session length is not a failure signal (arXiv 2604.02547).
9. **Cache-read share, and why a cache was rebuilt.** Show each session's and project's share of input read from the cache in `tokenops spend`, and join the context lint's findings to the cache rebuilds they explain (`prefix_changed`). Evidence: Manus' context-engineering notes; "Don't Break the Cache" (arXiv 2601.06007, preprint).
10. **What the starting context costs.** A checkup finding that estimates the tokens every session starts with (system prompt, tools of the configured MCP servers, skills, context files) from the first turn's cache writes, and names the largest parts. Evidence: context files raise cost by more than 20% with no gain in success (ICLR 2026).
11. **Room elsewhere, and headroom about to lapse.** The glance and the status line say which other plan has room when the binding one is tight, and warn when a weekly window will reset with much of it unused. Evidence: Peras, limitcast and CodexBar ship these for one harness each.
12. **Boundaries by folder and hour.** Boundary rules can match a project folder and a time of day. Evidence: Peras' per-folder and per-hour rules.
13. **Catalog drift.** A checkup finding when the operator's own readings disagree with the catalog's ratio between tiers, so a vendor's silent limit change is noticed before the catalog is updated by a pull request (ADR 0002).
14. **fmt is measured on outcomes.** An experiment that runs sessions with fmt's output and its raw output at random, and compares outcomes, not only bytes saved. Evidence: Squeez (arXiv 2604.04979) on evaluating compression by what the agent still finds.
15. **The competitive landscape, refreshed.** Add the token-saving tools (RTK, Caveman, Headroom), the pause guards (Peras, limitcast), Quesma's collector and the SaaS seat managers; correct Helicone (no longer part of Mintlify) and note ClickHouse's acquisition of Langfuse.
