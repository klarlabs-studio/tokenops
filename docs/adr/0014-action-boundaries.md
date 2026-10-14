# ADR 0014 — Action boundaries: who may authorize what, decided before the agent acts

- **Status:** Accepted 2026-10-12
- **Date:** 2026-10-12
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (one authority concept; explain before automating), ADR 0007 (one model policy; not a hard control), ADR 0012 (team plane), ADR 0013 (grants for other apps' sign-ins)

## Context

Agents now run continuously, and the moment where a person asks "should
this continue?" is gone. TokenOps already decides *how much* it may do
about that, but not *who* may allow an agent to go further:

- `internal/contexts/policy` has one authority scale (`ObserveOnly`,
  `Recommend`, `RequireApproval`, `Automatic`), and `Effective` takes the
  lesser of the daemon's and a subsystem's.
- Decisions are recorded as `DecisionEvent`s that move from shadow to
  proposed to applied, and `explain_decision` rebuilds why. Only
  `model_route` uses this today, and its approval (`routing_decide`)
  applies to future requests, not the one in flight.
- Few points can stop an action: read-guard (Read, in Claude Code and
  opencode) and route-guard's subagent path (Agent, Claude Code), which
  denies or rewrites a model the policy forbids. Everything else
  nudges: coach-hook, `session_budget`'s `recommended_action`, budget
  alerts. The proxy rewrites but never fails a request (ADR 0007).
- A decision records no actor (which agent, session or person asked) and
  no approver.
- The team plane only receives figures. Nothing is sent from it to
  members' machines, and ADR 0007 said organisations ship `config.yaml`
  through device management rather than a company layer.

What is missing is a boundary set *before* work starts: "past $20 in a
session, ask me before the next step", "subagents above Sonnet need an
admin", "no new work once the week's plan is under 5%", and a record of
who said yes.

## Decision

### 1. One rule type, the existing authority scale

A boundary is a rule in `config.yaml`, evaluated by the daemon:

```yaml
boundaries:
  - name: session-spend
    action: continue          # what the agent is about to do
    when: { session_cost_usd: ">= 20" }
    require: ask              # allow | ask | approver:<role> | deny
  - name: big-subagents
    action: subagent_model
    when: { model_tier: "> sonnet" }
    require: approver:admin
  - name: plan-floor
    action: continue
    when: { plan_left_pct: "< 5", window: week }
    require: deny
```

- **Actions are resource actions only**, the ones TokenOps already
  observes: `continue` (the agent's next tool call), `subagent_model`,
  `model_route`, `spend` (a request whose priced cost crosses a budget),
  and `share` (figures leaving the machine: an OTel export, a team
  upload). Authorizing arbitrary tools (deploys, writes, shell commands)
  is out of scope; that belongs to the harness and to CI.
- **Conditions use figures the daemon already holds:** session and window
  cost, plan headroom left, model tier, workflow and agent id, time of
  day. No prompt text is read to evaluate a rule.
- **`require` maps onto `policy.Authority`:** `allow` is `Automatic`,
  `ask` and `approver:<role>` are `RequireApproval` with a named
  approver, and `deny` is refusal. Rules combine like `Effective`: the
  strictest matching rule wins. A boundary can only tighten what the
  coach presets would otherwise do, never widen it.

### 2. Every decision names its actor and its approver

`DecisionEvent` gains `Actor` (harness, session id, agent id, the local
user) and `Approval` (who approved, how, when, under which rule). The
boundary engine records a decision for every rule that matched, using
the existing stages: shadow when it would have acted, proposed when it
asked, applied or rejected when answered. `explain_decision` shows the
rule and the approver. This closes the accountability gap with the same
record the router already writes, not a second log.

### 3. Enforcement only where a refusal is real

A boundary is enforced at a point that can refuse, and is otherwise
reported as advice, labelled as such:

- **The pause point (new):** a PreToolUse hook with matcher `*` (Claude
  Code; opencode's `tool.execute.before`) checks `continue` rules before
  each tool call. When attended it returns `ask`, which is the "should this
  continue?" moment. When unattended
  (`CLAUDE_CODE_SESSION_ATTENDED=0`, where `ask` acts as a denial and a
  retry) it returns `deny` with a reason the agent can act on: what was
  crossed, who can approve, and `tokenops approve <id>`. A broken or
  slow evaluator fails open, as read-guard does, and the failure is
  recorded.
- **Existing points** take boundary rules too: route-guard for
  `subagent_model`, the proxy for `model_route` (rewrite only; ADR 0007
  is unchanged and the proxy still never fails a request), the uploader
  and exporter for `share`.
- **Harnesses without a refusing hook** (Codex, Cursor) get the rule as
  advice through the prompt path and `session_budget`, and
  `tokenops boundaries status` says per harness which rules are enforced
  and which are advice only. Nothing claims enforcement it lacks.

### 4. Approvals: local first, then by role through the team plane

- **Local:** `tokenops approve|deny <id>`, the MCP tool
  `tokenops_boundary_decide`, or the menu bar. An approval can be for one
  action or for the rest of a session.
- **By role (team plane):** for `approver:<role>`, the daemon sends an
  approval request: the rule, the action class, the figures that crossed
  it, the requesting member and the harness. No prompt, path or model
  output, the same line as the upload contract, with a `pkg/teamwire`
  test that fails if free text is added. Approvers answer in the web view
  or with `tokenops team admin approve`, the answer is in the
  organisation's audit log, and the daemon polls for it. Until it is
  answered, the action stays refused.

### 5. Organisation boundaries through the team plane, tighten-only

Owners and admins may author boundaries in the team plane. The daemon
pulls them, as a versioned and signed document, alongside its upload
cycle. Merged with local rules, the strictest wins, so a member can add
boundaries but cannot loosen the organisation's. `tokenops boundaries
status` shows which rules came from the organisation. This **amends ADR
0007's "no separate company layer"** for boundaries only: device
management still works, and the team plane is a second, self-serve way
to deliver the same rules.

The boundary engine, local rules, the pause point and local approvals
are free and live in the open-source core. Distributing rules from an
organisation and approving by role are team-plane features. The
entitlement stays in the team plane (AGENTS.md: entitlements stay
outside the core domains).

### 6. Rollout: shadow first

As ADR 0004 requires explanation before automation, every new boundary
starts in **shadow**: it records "would have asked or refused" for a
week, or until the user promotes it with `tokenops boundaries promote
<name>`. `tokenops boundaries status` shows how often each rule would
have fired, so a rule that would interrupt constantly is seen before it
does.

## Consequences

- The post-hoc story ("you can see the costs") gains a before-the-fact
  boundary and a named approver, using machinery that exists: the
  authority scale, the decision record, and the hooks.
- It is still **not a hard control** (ADR 0007): someone who removes the
  hook removes the boundary. The team plane can show that a member's
  machine has not reported its hook installed. That is a derived,
  member-visible signal, never a hidden one. Hard guarantees still belong
  at the vendor's org controls or a gateway, and the docs say so.
- The PreToolUse hook on every tool call is a latency budget. The
  evaluator must answer from memory in under 5 ms, with no store reads
  on the hot path. It gets a benchmark gate like the glance API's.
- New domain `internal/contexts/boundaries`, registered in archlint; new
  config section `boundaries`; new `teamwire` messages for approval
  requests and organisation rules, both with no-free-text tests.

## Not doing

- Authorizing arbitrary tool actions or limiting what an agent may change
  in a codebase. That is the harness's and CI's job, and TokenOps does
  not become an agent framework.
- Identity beyond what exists: the local user, and team-plane accounts
  with their roles. There is no separate RBAC system.
- Blocking at the proxy. A request in flight is rewritten or allowed,
  never failed (ADR 0007).

## Phases

1. Boundary engine, `continue` and `subagent_model` rules, the pause
   point in Claude Code and opencode, local approvals, actor and approval
   on decisions, shadow rollout.
2. `spend`, `model_route` and `share` rules; status per harness;
   approvals from the menu bar.
3. Team plane: organisation rules (tighten-only), approval by role, audit.

## Phase 1 as built (amended 2026-10-12)

Where the code met the decision, phase 1 took the closest faithful
option:

- **Figures on the hot path.** The pause point reads no store. Session
  cost is the coach hook's running total (updated at each turn's end, so
  it lags the turn in flight, and needs the coach hook); plan headroom is
  the window reading Claude Code hands TokenOps' status line, used only
  while fresher than 30 minutes. opencode has neither cheaply, so
  `plan_left_pct` never matches there yet. A figure that cannot be read
  never satisfies a condition.
- **Promotion is explicit.** A rule stays in shadow until `tokenops
  boundaries promote`; nothing promotes it after a week. The stage is
  written beside the rule in `config.yaml` rather than kept in state, so
  a config shipped to several machines carries whether its rules bind.
- **Decision ids are per rule and session** (`bnd-…`), so an agent that
  retries is refused under the id the operator was told to approve. A
  shadow match is recorded once per session and counted on every call.
- **Decisions reach the store through a ledger.** The hook appends to
  `~/.tokenops/boundaries/decisions.jsonl` and the daemon ingests it every
  minute; explain reads the ledger too.
- **opencode cannot ask.** Its hook refuses by throwing, so an `ask` rule
  refuses there with the way out. `subagent_model` is advice only in
  opencode.
- **The hook is opt-in** (`hooks install --boundary`): it runs before
  every tool call, and its cost is the operator's to accept.
- **One MCP tool.** `tokenops_boundaries` (`action=status|decide`) joins
  the public tools, `tokenops_boundary_decide` being its inner tool;
  promote and demote have no MCP equivalent, so an agent cannot change the
  limits set on it.
- **Only a person approves.** The MCP tool and `POST /api/boundaries/decisions`
  can only deny, and `tokenops approve` refuses without a terminal: the
  agent a boundary stops can call tools and run commands, so an approval
  through them would be the agent approving itself (a `subagent_model`
  rule leaves its other tools working). An approval comes from a terminal
  or from Claude Code's own permission prompt.


## Phase 2 as built (amended 2026-10-12)

- **Each action takes only the figures known where it is decided.**
  `spend` reads `budget_used_pct` (with `budget: <name>`) and the session's
  cost; `model_route` reads the target's `model_tier` and `plan_left_pct`;
  `share` reads `destination` (team or otel). A condition its decision
  point cannot read is refused when the config loads, rather than never
  matching.
- **`spend` is decided at the pause point**, beside `continue`, before
  every tool call. The proxy still never fails a request (ADR 0007), so
  "a request whose priced cost crosses a budget" is judged on the step
  that would spend, not on the request in flight.
- **The daemon writes the figures the hook cannot compute.** When a rule
  compares plan headroom or a budget, the daemon measures them every
  minute into `~/.tokenops/boundaries/figures.json`; the pause point reads
  it while under five minutes old. This is how opencode gets
  `plan_left_pct`: it does not say which provider a session uses, so it
  reads the tightest plan TokenOps watches. Claude Code still prefers its
  own status-line reading.
- **Routes and destinations are decisions of their own.** A route is one
  decision per provider and model pair, a share one per destination, so an
  approval lets exactly that through: once, or for 24 hours. The router
  asks only when it would apply a route; a held route leaves the request
  on its model. Held OTel events are dropped, not queued.
- **Advice is the glance.** Codex and Cursor read enforced `continue` and
  `spend` rules that hold on plan headroom or a budget from
  `tokenops_glance` (and `/api/glance`). A rule needing the session's cost
  is not judged there.
- **The menu bar cannot approve over the API.** The daemon's token is
  readable by any of the user's processes, the agent included, so the API
  stays deny-only. The menu bar denies through it and, for an approval,
  opens a terminal running `tokenops approve <id> --confirm`, which shows
  what waits and answers only on a typed yes.

## Phase 3 as built (amended 2026-10-13)

- **The document is signed and pinned.** The server signs the exact bytes
  of the organisation's rules with an Ed25519 key derived from its secret.
  A machine pins the key on its first fetch and verifies on every read,
  including the cached copy the pause point reads, so the rules cannot be
  loosened by editing a file. A document under another key is refused
  until the machine joins again. Leaving forgets the rules.
- **Organisation rules keep their own names and stage.** They are shown and
  keyed as `org:<name>`, so they never collide with a local rule, and their
  stage is the organisation's: a member can neither promote nor demote one.
- **Only organisation rules are routed.** A local `approver:<role>` is still
  answered on the machine; only an organisation rule's request leaves it,
  and it carries the rule's name (the organisation's own), the action, the
  client and structured crossings. A budget's name is a member's label and
  is left out.
- **The keyboard does not answer a routed rule.** Whoever is at Claude
  Code's prompt need not hold the role, so a routed rule refuses instead of
  asking, and `tokenops approve` refuses it. Deny still works locally.
- **Nobody approves themselves through a token.** The team plane refuses
  an answer, through an administrator token, from the member who asked: an
  agent on that member's machine could hold the token. The same person
  may answer in the web view, in a session signed in with their account;
  one opened from a sign-in link does not count, since a device token can
  mint those. Leads, who hold no administrator token, answer in the web
  view only. Rotating the server's secret changes the signing key, and
  members then join again.
- **A one-step approval is spent.** The server returns an existing request
  only while it is pending or was answered for the session; after a
  one-step answer the next step is a new request.

## Hard budget windows (amended 2026-10-15)

- **A hard budget window is a boundary, not a second enforcement path.** A
  budget may hold several windows (#661), each soft (the coach notifies) or
  hard. A hard window is read as a `spend` rule named
  `budget-<budget>-<window>`, holding when that window's `budget_used_pct`
  is `>= 100`, requiring `ask` (default) or `deny`. It is evaluated, shown,
  promoted and demoted like any local rule; its stage is written beside the
  window in `config.yaml`, and it starts in shadow.
- **Each window has its own figure.** With several windows the daemon
  writes `budget_used_pct` per window as `<budget>:<window>`, and under the
  budget's name its fullest window, so a rule naming the budget keeps
  meaning "any of its windows". A single-window budget keeps its name as
  its key.
- **Agents cannot loosen a hard window.** The daemon API and the MCP tool
  may make a window hard, but change or remove a budget holding one only
  from a terminal, for the same reason they cannot promote or approve.
