# MCP server

`tokenops serve` starts a Model Context Protocol server that exposes
TokenOps queries as tools. Register it in any MCP client (Claude
Desktop, Cursor, opencode, your own agent) to let an LLM ask "how much
did we spend last week?" or "show me the top wasteful workflows" —
answered from the local event store.

## Tools

Sixteen tools, each answering one question. Most pick their slice with a
`view` (or an `action` or `setting` where they change something), so an
agent chooses between sixteen clear options instead of fifty near
neighbours, and reads far fewer tokens of tool definitions before its
first call. Every tool has a title, annotations a client can base approval
on (`readOnlyHint`, `destructiveHint`, `idempotentHint`), described
parameters, and an output schema; a parameter that does not apply to the
chosen view is refused with a message saying which ones do.

| Tool | Answers | Views, actions or settings |
|---|---|---|
| `tokenops_glance` | Every plan's windows with pace, spend against limits, the session budget, the coach's findings | `all`, `headroom`, `session_budget`, `findings` |
| `tokenops_spend` | What was spent and where | `summary`, `top`, `burn`, `forecast`, `commits`, `units`, `waste`, `tags` |
| `tokenops_sessions` | How agent sessions go, by source, model and role | `dx`, `story`, `runtime`, `prompts` |
| `tokenops_status` | Whether TokenOps is working and how it is set up | `status`, `mode`, `config`, `data_sources`, `vendor_usage`, `version` |
| `tokenops_explain` | What a figure means, or why a decision was made | `term` or `decision_id` |
| `tokenops_records` | What TokenOps recorded | `optimizations`, `audit`, `events`, `workflow`, `scorecard`, `verify` |
| `tokenops_rules` | The repository's agent rules files | `analyze`, `conflicts`, `compress`, `inject` |
| `tokenops_fmt` | Command-output compression | `learn`, `analyze` |
| `tokenops_pricing` | Model prices from the rate card | — |
| `tokenops_prepare_work` | Headroom, a model recommendation and the task's expected share of the plan window before a task | — |
| `tokenops_review_work` | One workflow reviewed after a task, with the window share it took against the estimate | — |
| `tokenops_outcome` | How a piece of work turned out | `record`, `detect` |
| `tokenops_routing` | Which model a turn should run on, and routing governance | `advise`, `proposals`, `decide`, `set_rule` |
| `tokenops_configure` | Change a setting | `plan`, `budget`, `mode`, `preferred_model`, `usage_meter` |
| `tokenops_coach` | How much the coach says and does | — |
| `tokenops_experiment` | A bounded routing trial | `start`, `status`, `stop` (as `action`) |

The read tools are marked read-only; `tokenops_prepare_work`,
`tokenops_review_work` and `tokenops_outcome` record evidence without
changing settings;
`tokenops_routing`, `tokenops_configure`, `tokenops_coach` and
`tokenops_experiment` change settings and are marked destructive. The
developer tools (`eval`, `replay`, `coverage-debt`, `rules bench`) are CLI
commands only.

### Changing settings asks you

A client's own permission prompt is not enough to guard a setting: if you
allowlisted `mcp__tokenops__*`, nothing stood between text the agent read
(a web page, a file, a quoted transcript) and a new operating mode, a
routing rule or an approved model upgrade. So TokenOps asks you itself.
Every call that changes a setting sends an MCP elicitation, a question your
client shows you outside the agent's turn, with the exact change: the
values the agent passed, quoted, and the `tokenops` command that does the
same from a terminal. The change applies only if you tick **Apply this
change** and accept. Declining, dismissing or accepting without the tick
changes nothing, and the agent is told not to retry.

| Tool | Asks before | Answers without asking |
|---|---|---|
| `tokenops_routing` | `decide`, `set_rule` | `advise`, `proposals` |
| `tokenops_configure` | every setting that writes (`plan` and `preferred_model` with a provider, `mode` with a mode, `budget`, `usage_meter`) | listing plans or ceilings, reading the mode |
| `tokenops_coach` | a preset, an autonomy, a verbosity, the holdout, a tip answer | the report (no arguments) |
| `tokenops_experiment` | `start`, `stop` | `status` |

A client that cannot ask is refused, and the refusal names the command to
run yourself, such as `tokenops mode active`, `tokenops routing rule set
<provider> <from> <to>` or `tokenops routing decide <key> approve`. That
includes clients that offer elicitation but reach `tokenops serve` over a
connection that cannot carry a request from the server: over stdio that
works for clients on the stateless `2026-07-28` protocol revision, and
older ones are refused for now. There is no setting that turns the
question off: a switch an agent could reach would reopen the gap, and the
terminal command is always there.

`tokenops_boundaries` asks no one: its `decide` can only deny, which is
the safe direction, and approving a boundary needs `tokenops approve` (see
[Boundaries](/guide/boundaries#what-approval-guarantees)).

### What `tokenops_sessions` returns

By default every view returns derived figures only, the same as the
daemon API: `story` gives each task's instruction count, turns, tool calls,
timing, `files_edited` (a count) and its frictions by kind
(`interrupted`, `rejected`, `rework`, `delegated`), and says
`titles_withheld: true` and `paths_withheld: true`; `prompts` gives
counts and recommendations with no instruction quoted.

Pass `quote: true` on `story` or `prompts` to get the narrative instead:
each task titled with the operator's instruction as typed, the absolute
local paths of the files it edited, each friction as a sentence, and the
prompt samples, repeats and evidence quoted. The parameter's description
says so before the call, so a client can ask the operator first. `dx`
has nothing to quote and refuses it. `tokenops story` on the command line
is unchanged: it is the operator's own local narrative.

### How much of the window a task takes

`tokenops_prepare_work` classifies the instruction (lookup, research,
edit, deep) and estimates the task's share of the plan's rate-limit
window from earlier tasks of the same kind, in the same `repository`
when you pass one and across repositories otherwise:

```text
typically 15–25% of your 5h window; 38% left, resets in 1h10
```

The range is the middle half of past measurements. It is not shown until
five tasks of that kind have been measured; until then the answer says
how many have been (`no estimate yet: 2 of 5 past tasks like this
measured`). When only the low end of the range fits in what is left,
`window_share.suggestion` proposes a lighter model tier (and the model
routing advice picked, if it picked one); when not even the low end fits,
it proposes deferring until the window resets. It never changes the
model.

`tokenops_review_work` with the same `workflow_id` reads the window again,
records the share the task took against the estimate (`within`, `over`
or `under`), and that measurement becomes history for the next estimate.
The share is read from the plan's usage meter at the start and the end,
so other work in the same window counts toward it, and a window that
resets during the task leaves the share unmeasured. Only the task class,
the repository label and percentages are stored, never the instruction.

### Sessions by cohort, and where tasks' time went

`tokenops_sessions` takes `source` (`claude-code`, `codex`, `cursor`,
`opencode`), `model` (a model id, case-insensitive) and `role` (`all`,
`primary`, `auto_review`, `subagent`) on its `dx`, `story` and `runtime`
views. `primary` is what the operator typed; `auto_review` is a review the
harness ran by itself, such as Codex's reviewer. Pooled, those reviews can
outnumber the operator's own instructions many times over and set the
median, so compare them apart. Each answer carries a `cohort` block: what
was selected, and the window's instructions by role before the filter. An
unknown role is an error, not a silent "all". The CLI's `--source`,
`--model` and `--role` select the same instructions and give the same
answer.

```json
{"view": "runtime", "days": 7, "source": "codex", "role": "primary"}
```

The `runtime` view is the coverage report `tokenops story --runtime --json`
exports (see [`tokenops story --runtime`](/guide/cli#runtime-where-the-time-went-and-what-is-not-known)):
per task a status (`completed`, `last_observed`, `open`), the observed
interval, and that interval split into `model_request`, `tool_execution`,
`approval_wait`, `idle` and `unattributed`, each with its `quality`,
`origin`, whether it is `complete`, and its coverage. A category the
harness does not record is `null`, never `0`; the `unmeasured` list names
each such gap. Tool time comes as `critical_path_seconds` and
`resource_seconds`, and the summaries give nearest-rank median and p90
per harness, model and role with their denominators. It carries derived
figures only: no instruction text, paths or session ids. Never add task
durations up as waiting time.

## Claude Desktop setup

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "tokenops": {
      "command": "tokenops",
      "args": ["serve"],
      "env": {
        "TOKENOPS_STORAGE_PATH": "/Users/<you>/.tokenops/events.db"
      }
    }
  }
}
```

Restart Claude Desktop. The TokenOps tools surface in the tools list.

## Cursor / other clients

Any client speaking MCP over stdio works. Run `tokenops serve` as the
command; pass `TOKENOPS_STORAGE_PATH` to point at the events DB.

## Logs

Diagnostic output goes to stderr (stdout is reserved for the JSON-RPC
channel). Tail it via `tokenops serve 2>/tmp/tokenops-serve.log`.
