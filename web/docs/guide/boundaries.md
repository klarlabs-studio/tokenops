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
  - name: budget-floor
    action: spend             # the next tool call, judged by money
    when: { budget_used_pct: ">= 90", budget: monthly }
    require: ask
  - name: no-opus-routes
    action: model_route       # the router moving a request to another model
    when: { model_tier: ">= opus" }
    require: ask
  - name: team-upload
    action: share             # figures leaving the machine
    when: { destination: "== team" }
    require: ask
```

Then wire the pause point and watch what the rules would do:

```bash
tokenops hooks install --boundary            # Claude Code
tokenops hooks install --boundary --client opencode
tokenops boundaries status
```

## The headroom pause

`tokenops init` writes one boundary on every new config, so the pause
before a plan runs out is there from the first session:

```yaml
boundaries:
  - name: headroom-pause
    action: continue                     # the agent's next tool call
    when: { plan_left_pct: "<= 10" }     # the tightest plan window, 90% used
    require: ask
    stage: shadow
```

With no `window`, `plan_left_pct` reads the tightest window TokenOps knows
— the 5-hour or the weekly one, whichever has less left. 90% leaves the
agent room to finish its step, commit and hand over. In shadow it asks
nothing: it records each session it would have paused, and the glance's
findings (`tokenops glance`, `GET /api/findings`, the menu bar) say
"The headroom pause would have paused 3 sessions this week". When that
looks right:

```bash
tokenops boundaries promote headroom-pause
```

From then on it asks at Claude Code's prompt when someone is there, and
otherwise refuses with the id `tokenops approve` takes; Codex and Cursor
are told through the glance. Init also installs the pause point (the
boundary hook) in Claude Code and opencode, because a rule without one
records nothing, and the findings say so if it is missing.

A config written before init did this is not changed behind your back.
The findings suggest the pause when a plan is watched and none is set;
`tokenops boundaries headroom-pause` adds it in shadow and installs the
pause point. A `continue` rule of your own on `plan_left_pct`, whatever
its name, counts as a headroom pause and is left alone. Change the
threshold by editing `plan_left_pct` in `config.yaml`.

## Rules

| Key | Takes |
|---|---|
| `name` | lower-case letters, digits, `-` and `_`; unique |
| `action` | `continue`, `spend`, `subagent_model`, `model_route` or `share` (below) |
| `when` | conditions, all of which must hold (below) |
| `require` | `allow`, `ask`, `approver:<role>` or `deny` |
| `stage` | `shadow` (the default) or `enforce` |

| Action | What it is about to do | Decided by |
|---|---|---|
| `continue` | the agent's next tool call | the pause point |
| `spend` | the agent's next tool call, judged by money | the pause point |
| `subagent_model` | the model a subagent is about to run on | route-guard's Agent hook |
| `model_route` | the router moving a request to another model | the router, in the proxy |
| `share` | figures leaving the machine: a team upload, an OTel export | the uploader and the exporter |

Conditions read only figures TokenOps already holds; no prompt text is read
to evaluate a rule. Each needs a comparator: `<`, `<=`, `>`, `>=` or `==`.
An action takes only the conditions it can be judged by where it is
decided:

| Condition | Compares | Actions | Read from |
|---|---|---|---|
| `session_cost_usd` | the session's API-equivalent cost so far | `continue`, `spend`, `subagent_model` | the coach hook's running total, updated at each turn's end |
| `plan_left_pct` with `window: 5h \| week` | the share of the plan window left; with no window, the tightest | `continue`, `subagent_model`, `model_route` | in Claude Code, the windows it reports to TokenOps' status line (if fresher than 30 minutes); otherwise the daemon's figures |
| `budget_used_pct` with `budget: <name>` | the share of a configured budget used in its window; with no name, the fullest | `spend` | the daemon's figures |
| `model_tier` | haiku < sonnet < opus | `subagent_model`, `model_route` | the subagent's model (or the session's when it inherits); the model the router would move to |
| `destination` | `== team` or `== otel` | `share` | where the figures are going |
| `workflow_id`, `agent_id` | `==` with `*` wildcards, e.g. `"== claude-code:*my-repo*"` | all but `share` | the session's attribution, or the request's |

A figure TokenOps cannot read never satisfies a condition: a boundary that
cannot see its figure lets the action through. In particular,
`session_cost_usd` needs the coach hook (`tokenops hooks install --coach`).

**The daemon's figures.** Plan headroom outside Claude Code and budget use
need the event store, which the pause point never opens. So when a rule
compares one of them, the daemon measures them every minute into
`~/.tokenops/boundaries/figures.json`, and the pause point reads that one
file while it is under five minutes old. With the daemon stopped, those
rules see no figure. In opencode, which does not say which provider a
session uses, `plan_left_pct` reads the tightest plan TokenOps watches.

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

`approver:<role>` in your own config is answered on this machine, like
`ask`. In your organisation's rules it goes to that role: see
[Organisation boundaries](#organisation-boundaries). Routing the request to someone holding the role needs
the team plane, which comes later; `boundaries status` says so on the rule.

### Inside TokenOps: routes and figures

`model_route` and `share` are decided inside TokenOps, so no agent reads
their refusal, and nobody is at a prompt. `ask` holds the action until you
approve it; `deny` holds it until the rule changes.

- **A held route** leaves the request on the model it asked for. The
  request itself is never failed or delayed. Each route (provider, from
  and to model) is its own decision, so an approval lets that route
  through: once, or for 24 hours with `--session`. The router asks only
  when it would actually apply a route, never while observing.
- **Held figures** are not sent: the hourly team upload (and `tokenops
  team sync`), the OTel metric push, and the OTel event stream, whose
  events are dropped while held, not queued; the event store keeps every
  one. Each destination is its own decision. Uploads resend their last
  days whole, so an upload held for a while fills the gap once it goes.

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
tokenops approve bnd-1a2b3c4d5e6f --confirm  # show what waits, then ask y/N
tokenops deny bnd-1a2b3c4d5e6f
```

The menu bar lists what waits under *Waiting on you*. **Deny** answers
there. **Approve…** opens a terminal running `tokenops approve <id>
--confirm`, so the approval is still you, at a terminal, typing yes.

Only a person should approve: `tokenops approve` refuses to run without a
terminal, and the MCP tool and the daemon API can only deny. The agent a
boundary stopped can call tools and run commands, so an approval through
any of those would be the agent approving itself. How far that holds is
below.

An answer applies to the session it was asked in, and is honoured for at
most a day. Answering at Claude Code's prompt counts too: TokenOps reads
the answer back and records it.

Every decision names its **actor** — the client, session, agent and local
user — and, once answered, its **approval**: who, how (`cli`, `mcp`,
`api` or `prompt`), when, and under which rule.

```bash
tokenops explain bnd-1a2b3c4d5e6f
```

### What approval guarantees

Approval is a guardrail against an agent that is wrong or misled, not a
security boundary against a process that wants to get through. It holds
against the agent's own tools; it does not hold against everything that
runs as your user.

**It guarantees:**

- An agent cannot approve through TokenOps' MCP tools: `tokenops_boundaries`
  can only deny, and the settings tools (`tokenops_configure`,
  `tokenops_routing`, `tokenops_coach`, `tokenops_experiment`) apply a
  change only after you confirm it at your client
  ([MCP server](/integrations/mcp-server#changing-settings-asks-you)).
- The daemon's decision endpoint (`POST /api/boundaries/decisions`) can
  only deny.
- `tokenops approve` refuses to run when its standard input is not a
  terminal, so an agent's plain shell call fails.
- Every answer is recorded with who gave it and how (`cli`, `mcp`, `api`,
  `prompt` or `team`), and `tokenops explain <id>` shows it.

**It does not guarantee** that the approval came from you. Anything running
as your user can act as you:

- The terminal check is that standard input is a terminal, nothing more.
  A command can get one for itself:
  `script -q /dev/null tokenops approve <id>` passes it, and so would
  `expect`, `tmux` or any pseudo-terminal.
- The daemon's API token is a file in `~/.tokenops` that your user can
  read. With it, a process can call the settings endpoints directly, such
  as `POST /api/mode` or `POST /api/routing/rules`.
- The boundary ledger and `config.yaml` are files your user can write.

TokenOps does not try to tell those apart from you. A check that looks for
a "real" terminal (a controlling terminal, its session, the parent
processes) cannot separate a pseudo-terminal an agent opened from the one
you typed into, and a check that seems to would promise more than it
delivers.

**Recommended setup:**

- Do not give agents an unrestricted shell for TokenOps. In Claude Code,
  add permission rules that ask before `Bash(tokenops approve:*)`,
  `Bash(tokenops mode:*)`, `Bash(tokenops routing:*)`,
  `Bash(tokenops boundaries:*)` and `Bash(script:*)`, and before any
  `curl` to the daemon's address; other harnesses have equivalents.
- Answer boundaries yourself: at Claude Code's prompt, in the menu bar, or
  with `tokenops approve <id> --confirm` in your own terminal.
- For a guarantee that holds against a determined process, use the
  vendor's organisation controls or a gateway (see below).

## Where a boundary is enforced

| Client | `continue`, `spend` | `subagent_model` |
|---|---|---|
| Claude Code | enforced (`--boundary` hook, every tool) | enforced (route-guard's Agent hook) |
| opencode | enforced (plugin, `tool.execute.before`) | advice only |
| Codex, Cursor | advice, through the glance | advice only |

`model_route` is enforced by the router for every request through the
TokenOps proxy, and `share` by the uploader and the exporter; each is idle
where it is not in use (no routing configured, no team joined, no
collector). `boundaries status` reports all of this per rule, so nothing
claims enforcement it lacks.

**Advice.** Codex's and Cursor's hooks cannot refuse a call. Instead, an
enforced `continue` or `spend` rule that holds on plan headroom or a
budget is listed on the glance — `tokenops_glance` and `GET /api/glance`,
under `boundaries` — with what the agent should do: stop and tell you, or
stop and ask. A rule that needs a session's cost, or a workflow or agent,
cannot be judged there and is left out rather than guessed.

## Organisation boundaries

When this machine has [joined a team](/guide/team), its owners and admins
can set boundaries for every member, in the web view (*Boundaries*) or with
`tokenops team admin boundaries --set org.yaml`, written exactly as
`config.yaml`'s section is. The daemon fetches them every five minutes.

- **They only tighten.** Organisation rules sit beside yours, named
  `org:<name>`, and the strictest matching rule wins. You can add
  boundaries but not loosen theirs; their stage (shadow or enforce) is the
  organisation's to set, so `boundaries promote` and `demote` refuse them.
- **They are signed.** The server signs the document; this machine pins the
  key on the first fetch and checks the signature every time it reads the
  rules, the copy on disk included. Rules edited on disk are not rules, and a
  document under another key is refused (`tokenops team status` says so).
  Leaving the team forgets them.
- **`approver:<role>` goes to the role.** When an organisation rule naming
  an approver holds an action, the agent is refused (even with someone at
  the keyboard, who may not hold the role) and the daemon sends an approval
  request to the team plane: the rule, the action, the client and the
  figures that crossed it — numbers and names from fixed lists, never a
  prompt, a path or a name you typed (a test fails the build if one could
  be added). Everyone in the organisation with that role or above is
  e-mailed and answers in the web view (*Approvals*), or, as an owner or
  admin, with `tokenops team admin approve <id>`; the answer reaches this machine within a minute and
  is recorded with who gave it. Until then the action stays refused. You can
  deny it here; you cannot approve it here.
- **Nobody approves themselves through a token.** `team admin approve`
  refuses a request you made: an agent on your machine could hold your
  administrator credential. Another member with the role answers it, or
  you do, in the web view signed in with your account (a session opened
  from a sign-in link does not count: a machine's token can make one).

Local rules naming an approver are not sent anywhere: only your
organisation's rules leave this machine as requests.

## What a boundary is not

- **Not a hard control.** Removing the hook removes the boundary. A hard
  guarantee belongs at the vendor's organisation controls or a gateway.
- **Not a tool permission system.** Boundaries cover resource actions —
  continuing, spending, which model runs, whether figures leave — not
  which commands an agent may run. That is the harness's and CI's job.
- **Never in the agent's way when broken.** An evaluation that fails or
  takes over two seconds lets the call through, and the failure is
  recorded as a decision you can see.

The pause point answers from a few small files, never the event store:
about 0.1 ms per call when nothing is crossed and 0.7 ms when a rule is, against
a 5 ms budget the test suite enforces. Its decisions are written to
`~/.tokenops/boundaries/decisions.jsonl`, which the daemon ingests into
the event store every minute.

See [ADR 0014](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0014-action-boundaries.md)
for the design.
