# Action boundaries

A boundary is a limit you set **before** work starts, and a record of who
let the agent go further. Agents run for long stretches now, and the
moment where you would have asked "should this continue?" is gone. A
boundary puts it back, at the points that can actually stop an agent.

```yaml
# ~/.tokenops/config.yaml
boundaries:
  - name: session-spend
    action: continue          # the agent's next tool call
    when: { session_cost_usd: ">= 20" }
    require: ask              # allow | ask | approver:<role> | deny
  - name: big-subagents
    action: subagent_model    # the model a subagent is about to run on
    when: { model_tier: "> sonnet" }
    require: approver:admin
  - name: plan-floor
    action: continue
    when: { plan_left_pct: "< 5", window: week }
    require: deny
```

Then wire the pause point and watch what the rules would do:

```bash
tokenops hooks install --boundary            # Claude Code
tokenops hooks install --boundary --client opencode
tokenops boundaries status
```

## Rules

| Key | Takes |
|---|---|
| `name` | lower-case letters, digits, `-` and `_`; unique |
| `action` | `continue` or `subagent_model` |
| `when` | conditions, all of which must hold (below) |
| `require` | `allow`, `ask`, `approver:<role>` or `deny` |
| `stage` | `shadow` (the default) or `enforce` |

Conditions read only figures TokenOps already holds; no prompt text is read
to evaluate a rule. Each needs a comparator: `<`, `<=`, `>`, `>=` or `==`.

| Condition | Compares | Read from |
|---|---|---|
| `session_cost_usd` | the session's API-equivalent cost so far | the coach hook's running total, updated at each turn's end |
| `plan_left_pct` with `window: 5h \| week` | the share of the plan window left; with no window, the tightest | the plan windows Claude Code reports to TokenOps' status line, if fresher than 30 minutes |
| `model_tier` | haiku < sonnet < opus (`subagent_model` only) | the subagent's model, or the session's when it inherits |
| `workflow_id`, `agent_id` | `==` with `*` wildcards, e.g. `"== claude-code:*my-repo*"` | the session's attribution |

A figure TokenOps cannot read never satisfies a condition: a boundary that
cannot see its figure lets the action through. In particular,
`session_cost_usd` needs the coach hook (`tokenops hooks install --coach`),
and `plan_left_pct` needs TokenOps' status line in Claude Code; neither
figure is available to opencode yet.

A config with an unknown action, condition or requirement is refused when
it loads, with the rule named.

## What a rule requires

`require` sits on the same ladder as every other TokenOps authority:
`allow` is automatic, `ask` and `approver:<role>` need approval, `deny`
refuses. When several enforced rules match, the strictest wins — deny, then
a named approver, then ask, then allow — so a boundary can only tighten.

| Rule | Someone at the keyboard (Claude Code) | Nobody at the keyboard, or opencode |
|---|---|---|
| `ask` | Claude Code asks; yes holds for the rest of the session | refused, with the rule, what was crossed and `tokenops approve <id>` |
| `approver:<role>` | as `ask` | as `ask`, naming the role |
| `deny` | refused | refused |

The refusal is written for the agent to act on: it says what was crossed,
tells it to stop rather than retry, and names the command you run to let
it go on.

**Phase 1:** `approver:<role>` is stored and shown, but answered on this
machine like `ask`. Routing the request to someone holding the role needs
the team plane, which comes later; `boundaries status` says so on the rule.

## Shadow first

Every rule starts in shadow. A shadow rule is evaluated on every call,
counted, and recorded once per session as a decision that "would have
asked" or "would have refused" — and changes nothing. Watch it for a while:

```bash
tokenops boundaries status
```

```
session-spend  continue → ask  [shadow]
  would have fired 41 times in 3 sessions, last 2026-10-12 16:02
  claude-code  shadow — records what it would have done; promote to enforce
  opencode     hook not installed — tokenops hooks install --boundary --client opencode
  codex        advice only — its hooks cannot refuse a call, so the rule is not enforced there
  cursor       advice only — its hooks cannot refuse a call, so the rule is not enforced there
  note: in shadow; `tokenops boundaries promote session-spend` makes it bind
```

A rule that would interrupt constantly shows up here before it does.
When it looks right, promote it:

```bash
tokenops boundaries promote session-spend
```

Promotion writes `stage: enforce` beside the rule in `config.yaml`, so a
config shipped to several machines carries whether its rules bind.
Nothing promotes a rule for you.

## Answering

```bash
tokenops approve bnd-1a2b3c4d5e6f            # the next step
tokenops approve bnd-1a2b3c4d5e6f --session  # the rest of the session
tokenops deny bnd-1a2b3c4d5e6f
```

Only a person can approve: `tokenops approve` refuses to run without a
terminal, and the MCP tool and the daemon API can only deny. The agent a
boundary stopped can call tools and run commands, so an approval through
any of those would be the agent approving itself.

An answer applies to the session it was asked in, and is honoured for at
most a day. Answering at Claude Code's prompt counts too: TokenOps reads
the answer back and records it.

Every decision names its **actor** — the client, session, agent and local
user — and, once answered, its **approval**: who, how (`cli`, `mcp`,
`api` or `prompt`), when, and under which rule.

```bash
tokenops explain bnd-1a2b3c4d5e6f
```

## Where a boundary is enforced

| Client | `continue` | `subagent_model` |
|---|---|---|
| Claude Code | enforced (`--boundary` hook, every tool) | enforced (route-guard's Agent hook) |
| opencode | enforced (plugin, `tool.execute.before`) | advice only |
| Codex, Cursor | advice only | advice only |

`boundaries status` reports this per rule, so nothing claims enforcement
it lacks.

## What a boundary is not

- **Not a hard control.** Removing the hook removes the boundary. A hard
  guarantee belongs at the vendor's organisation controls or a gateway.
- **Not a tool permission system.** Boundaries cover resource actions —
  continuing, which model a subagent runs on — not which commands an agent
  may run. That is the harness's and CI's job.
- **Never in the agent's way when broken.** An evaluation that fails or
  takes over two seconds lets the call through, and the failure is
  recorded as a decision you can see.

The pause point answers from two small files, never the event store: about
0.06 ms per call when nothing is crossed and 0.4 ms when a rule is, against
a 5 ms budget the test suite enforces. Its decisions are written to
`~/.tokenops/boundaries/decisions.jsonl`, which the daemon ingests into
the event store every minute.

See [ADR 0014](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0014-action-boundaries.md)
for the design.
