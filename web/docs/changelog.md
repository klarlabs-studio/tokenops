# Release highlights

The curated arc of what changed and why. For every commit, see the
[full CHANGELOG](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md);
for binaries, the [releases page](https://github.com/klarlabs-studio/tokenops/releases).

Current release: **v0.68.1**.

## v0.68.0 — nothing to paste

Connecting claude.ai's own usage meter used to mean opening developer
tools, finding a cookie called `sessionKey`, copying a value you must not
let anyone see, and pasting it into a terminal. TokenOps had a command
whose main job was explaining those four clicks, which is a strange thing
for a tool to be proud of.

Now it reads the session from the browser you are already signed in with.
macOS asks you once whether to allow it; you say yes, and that is the
entire setup. Your agent can do it too, and the login never passes through
the conversation.

The reader takes exactly one cookie, for claude.ai, from a copy of the
store — your browser can stay open, and its profile is never written to.
If you decline the prompt, it says you declined, rather than telling you
no session exists.

## v0.67.1 — three answers that sounded certain

Testing 0.67.0 against a real machine, rather than the fixtures a test
suite agrees with, turned up three answers that were stated with more
confidence than they had earned.

The rate-limit window "resets in 5h0m0s" — which was simply the window's
length, counted from the moment you asked, whether the window opened four
hours ago or a minute ago. A Codex plan's headroom came with Claude Code's
transcripts as its evidence, which say nothing about Codex. And the count
of background events carried no date, so 274 budget alerts from a budget
deleted in June looked like 274 alerts happening now.

None of these was a crash or a wrong total. Each was a number that looked
measured and was not, which is the harder kind to notice — and the kind
this tool exists to stop other people from shipping.

## v0.67.0 — what your agent is told

Most of TokenOps is read by you. More and more of it is read by your coding
agent, through the MCP server, and this release is about what the agent was
being told. A full review of the 41 tools found places where it was told
something wrong, told to do something harmful, or allowed to undo something
you had set.

Some of it was quietly dangerous. An agent adjusting a token budget's
warning level would have erased the limit itself. A healthy daemon was
reported as missing — because TokenOps' own tests kept deleting the file
it is found by — and the advice for a missing daemon would have started a
second one. And every careful refusal, such as "a budget needs a ceiling",
reached the agent as "internal error", which it cannot act on.

Some of it was just wrong. On a subscription, the spend tools reported
$0.00 for billions of tokens. The help index listed half the tools. An
agent could not tell it was talking to an MCP server from months ago —
which, on the machine where this was found, it was.

All of it is fixed. Agents can now also bind your plan and connect the
Claude usage meter themselves, without the session key ever passing through
the chat. One thing only you can do: restart your MCP clients once, so they
pick up this server. From then on, an outdated one says so.

## v0.66.0 — the meter reads the real thing

TokenOps can read claude.ai's own usage meter: the percentages Anthropic
shows you, rather than an estimate built from counting messages. It turned
out it had never once read them correctly. The code was written for a reply
nobody had actually seen, and its tests were written from the same guess —
so they could only ever agree with it.

Checking it against real replies, from an Enterprise account, a Max account
and a personal one, found four ways it went wrong. It looked for spend under
names that do not exist. Where a plan has no 5-hour window, it wrote down
"0% used" and would have passed that off as Anthropic's own figure. It kept
only the first reading after each reset, so it showed usage as it stood
minutes in. And the terminal and your agent assembled headroom separately,
each missing something the other had.

All four are fixed, and the real replies are now the tests. The part that is
new: on Claude Enterprise, Anthropic tells you what you have spent this
month and what your limit is. TokenOps now uses exactly that, with no admin
key and nothing to type in — which matters, because the person using
Enterprise at a company is almost never the one who holds the admin key.

## v0.65.4 — upgrading is one command, not three

Upgrading TokenOps used to end with an instruction: run
`tokenops daemon install`. On a machine where the daemon was already
running, that instruction could stop it, fail to start the new one, and
then print two lines of `launchctl` for you to type — while reporting that
all was well.

The cause was a race. macOS keeps hold of a background job for a moment
after it is told to let go, and starting its replacement in that moment
fails. TokenOps now restarts a running daemon in place, waits properly when
it has to replace one, and — the part that matters most — does it for you:
`brew upgrade` restarts an installed daemon on the new version by itself.

If it ever cannot start the daemon, it now says so, and tells you the one
TokenOps command to run. No supervisor syntax.

## v0.65.3 — tidying without stopping the room

0.65.2 moved TokenOps' tidy-up away from startup, so it no longer collided
with the history being loaded in. The next tidy-up showed why that was only
half a fix: anything written while it ran still had to wait, and waited
long enough to come close to being lost.

The tidy-up rewrote the whole store to give back a few hundred kilobytes,
and nothing else could write until it finished — half a minute on a
well-used machine, every time it had anything to remove. It now gives the
space back in small pieces, each over in a fraction of a second, so nothing
waits behind it.

Existing stores pay the old cost once more, on the first tidy-up after
upgrading, which converts them. New ones start out converted.

## v0.65.2 — two jobs, one lock

Fixing the CPU problem in 0.65.1 made a second problem visible, which had
been there all along.

When TokenOps starts, it re-reads your agents' history into its store. It
also tidied the store at startup — and tidying locks it. On a
well-used machine the lock lasted half a minute, and the history being
written in waited, retried, and got through on its very last attempt. One
more failure and some of it would have been counted as lost.

The tidy now waits a few minutes after startup, so the two no longer run at
the same time.

## v0.65.1 — the watcher stops costing more than it watches

TokenOps runs a small background process that reads what your coding agents
write to disk. It was meant to be invisible. On a well-used machine it was
holding most of a CPU core, all day, every day.

The cause was simple once measured. Every thirty seconds it re-read every
Claude Code transcript, every Codex session and the whole opencode database
from the start — on the machine that found it, close to three gigabytes,
almost none of which had changed since the last look. The more you used your
agents, the more the tool watching them cost you, which is exactly backwards
for a tool whose job is to make agent work cheaper.

It now remembers how far it has read in each file and picks up from there,
and leaves alone anything that has not changed. On the same machine and the
same data, its steady-state CPU went from about a third of a core to about
one percent.

One quieter fix came with it. A single very long line in a transcript —
a large tool result — used to stop the reader for the rest of that file, so
everything the agent did afterwards in that session went uncounted. That
line is now skipped on its own.

## v0.65.0 — a name that says what it does

One source in TokenOps reports Anthropic's own measure of how much of your
Claude subscription is gone, rather than an estimate built from counting
messages. It was called `anthropic-cookie`, which told you how it worked and
nothing about why you would want it. It is now `claude-usage-meter`.

This release breaks one thing on purpose. A config file still using the old
name is refused when TokenOps starts, with a message naming the new key. That
is deliberate: a renamed setting is otherwise ignored without a word, and the
meter would quietly stop reporting. A startup error you can read is better
than a feature that disappears. If you never set up the meter, nothing
changes for you.

Alongside it is a design decision worth reading if you run TokenOps for a
team. Both Anthropic and OpenAI will tell you exactly what they are about to
bill, and TokenOps works it out from a public price list instead — which is
approximately right, and wrong in the ways that matter most, such as a
negotiated discount. The fix is to ask the vendor. But asking needs an admin
key, and admin keys belong to whoever runs the organization, not to the
developer at the laptop. So that work is written down for the team edition,
where the person installing it is the one who holds the key.

## v0.64.0 — the same answer from both doors

TokenOps has two front doors. You type at one and your agent calls the
other, and for a long time they did not know the same things.

Nobody set out to build it that way. A command gets added where it is needed
and the other surface is a separate file, so the drift is invisible until
somebody asks a direct question — which is what happened here: *are we at
parity?* The honest answer was no, in both directions, and nothing had ever
checked.

The uncomfortable half is which way it ran. Four settings — the operating
mode, the model ceiling, spend budgets, routing rules — could be changed by
an agent through the MCP server and not by the person at the terminal, where
`tokenops config` could only print. If you wanted your own model ceiling
raised you edited YAML by hand, or you asked the agent to do it. A tool
easier to drive by asking something else to use it has a design problem, and
it went unnoticed for as long as it did because each half worked perfectly
well on its own.

Going the other way, an agent could not ask what a model costs, nor whether
the numbers it was about to quote were still being collected. It had event
counts, which tell you a source is quiet without telling you whether it was
ever switched on. Those are different problems and only one of them has a
fix.

Both directions are closed now, and the parity check that found them has
teeth: it fails when either surface grows without the other, and carries a
written reason for each asymmetry that is genuinely deliberate — installing
hooks on this machine is a local act and reasonably stays one.

That check also had a hole of its own, found the moment it was used in
anger. It built its own view of the tool surface from a list that could fall
out of date, so the first two tools added after it shipped were invisible to
it and it passed them without comment. It now reads its own source and
refuses to run incomplete. A check you have not seen fail is not yet a check.

## v0.63.0 — the instrument catches its own

Four releases ago TokenOps started reporting the telemetry it had failed to
write. The counter had always existed; it was only ever spoken aloud in a log
line at shutdown, which is how thirty thousand rows once went missing without
a word anyone saw.

Its first catch in the wild was TokenOps. Upgrading a machine to the release
that surfaced it produced, thirteen seconds after boot, a warning that 222
events had been dropped — exactly the size of that machine's read-guard
history.

The cause was one ingestion path that had been missed when the others were
fixed. Every vendor-usage poller waits for room in the queue rather than
discarding what does not fit; the read-guard replay did not, and it is the
worst candidate for that, because it republishes an entire ledger at boot and
again every two minutes. Nothing was permanently lost — that replay recovers
anything dropped on its next pass — but the counter read non-zero on every
single start, and a number that is always non-zero is one you stop reading.
That is the failure it was surfaced to prevent, so it was worth fixing for
the counter's sake even though no data was at risk.

The website got the same treatment, last and worst. Every command added
across this cycle had shipped without reaching the documentation, and the
front page had been advertising v0.56.0 while six further versions went out.
The deploy was never the problem: the claim was written by hand once and
nothing ever compared it to reality. It does now.

## v0.62.0 — asking Anthropic instead of estimating

Everything TokenOps says about a Claude subscription's remaining capacity is
an estimate, derived from counting messages against a published cap. There is
one exception, and it has been sitting behind four clicks in browser devtools:
claude.ai reports its own utilisation percentages, and your browser already
holds the cookie that reads them.

Turning that on was possible before and easy to get wrong. The flag wrote
whatever you gave it and said it had worked, without ever asking Anthropic
whether the key was any good. A cookie copied a week ago — they rotate — would
be accepted, stored, and then fail silently on every poll into a log file. One
machine collected three thousand of those failures without a single message
anyone saw.

`tokenops vendor-usage setup claude-usage-meter` asks Anthropic first. It tells
you where the cookie is, takes it without printing it to your terminal,
resolves which organisation to meter, fetches a real reading, and shows you
the percentages before it writes anything. If the key is stale it says so, and
says that rotation is the likely reason, because "invalid" on its own sends
you checking the wrong thing.

The other change is a guard rather than a feature. Three commands shipped on
the CLI with no equivalent tool in one day, and nothing noticed, because the
test meant to protect that only checked four tools by name. It now diffs both
surfaces and fails when one grows without the other — while carrying, for each
deliberate asymmetry, the reason it is deliberate. Installing hooks on this
machine is reasonably a local act. An agent being unable to ask what a model
costs is not, and that one is now written down as a gap rather than left to be
rediscovered.

## v0.61.0 — a denominator you already have

The previous release gave Enterprise no plan entry at all, and said so on
purpose: usage-based Enterprise is billed at API rates from the first token,
so there is no cap to be under, no percentage to report, and nothing for a
headroom calculation to divide by. Inventing a window for it would have
produced maths that looked authoritative and was fiction.

That reasoning was right about the vendor and wrong about the operator. There
is a denominator — it is just not Anthropic's. Admins set org, seat-tier and
per-user spend limits in the console, and the person running this tool knows
that figure perfectly well.

So Enterprise is a plan again, of a different kind: one whose limit is
supplied rather than published. Binding it without the number is refused,
because a percentage measured against a default nobody chose reads exactly as
confident as a real one. Give it the limit and the report is spend against
that limit, in the same shape a window would have taken.

The half that already worked is the interesting half. Enterprise traffic is
genuinely metered, so the cost recorded against each event is what the vendor
actually charges — unlike a subscription's traffic, which is correctly zero
because it costs nothing at the margin. The numerator has been right all
along; only the thing to compare it against was missing.

One detail worth knowing if you are on a negotiated contract: TokenOps costs
from the public rate card, and enterprise rates are frequently discounted off
it. Left alone, that compares your console limit against an overstatement and
never mentions it. `rate_factor` scales measured spend to what you actually
pay.

Seat-based Enterprise is still not modelled. It is an included allowance and
then metered overflow — two denominators at the same time — and asking for it
says that, rather than quietly handing you the usage-based plan and a number
that does not describe your contract.

## v0.60.0 — one number, in one place

A subscription tier is not a number any more. Anthropic documents Max and
Team as multiples of Pro — "five times the Pro plan's per-session usage
allowance" — and has stopped publishing an absolute for any tier, Pro
included.

TokenOps held three absolutes instead, written down separately, and they had
quietly come apart from the relationship they were meant to express: Max 5x
sat at 1.1x Pro where the vendor says 5x, Max 20x at 4.4x where it says 20x,
and the support page both of them cited had become a 404. Nobody noticed,
because a wrong denominator does not look wrong — it just makes a percentage
that is always comfortable.

So the tiers now derive. One pinned Pro baseline, a multiplier per tier, and
a test that forbids a derived entry from carrying an absolute of its own,
because holding the same fact in two places is how they drifted the first
time. Correcting Pro corrects everything below it.

**Your headroom percentage will move.** Max 5x goes from 50 to 225 messages
per window and Max 20x from 200 to 900. You are not using less than you were;
the number you were being measured against was too small. Two independent
trackers recorded 45 / 225 / 900 from Anthropic's own documentation a
fortnight ago, which is exactly what the derivation produces.

Honesty about what is left: the Pro baseline cannot currently be checked
against any vendor page, so it sits in one place with a label saying so.
Settings → Usage in the Claude app is the only figure that is certainly
yours.

Team plans also work now — Standard and Premium seats, as two separate
entries, because they differ by five times. Enterprise still has none, on
purpose: usage-based Enterprise bills at API rates from the first token and
seat-based Enterprise is an allowance with metered overflow, so neither is a
window to have headroom in. Asking for it now says that, rather than showing
you a list your plan is missing from.

## v0.59.0 — what the tools were not saying

This release came out of two things: an audit of what was actually wired to
what, and someone installing TokenOps for the first time and writing down
everything that surprised them.

Both found the same shape of defect twice over.

The first is a command that reports success and does nothing. `hooks status`
listed two hooks on a machine running three, and no flag could remove the
third — it had shipped wired to the installer and invisible to the two
commands that inspect and remove. `plan set` wrote the key and then told the
operator to restart the daemon, which is another way of saying the setting had
not taken effect. A retention rule pinned a source tag that did not exist, so
history its author believed was kept forever was quietly on a 120-day window.
In each case the tool had said it was done.

The second is a number that reads zero because nobody asked what zero means.
A subscription's traffic costs nothing at the margin, so on a flat-rate plan
every money column read $0.0000 while the headline reported thousands —
including the column that "top consumers" was ranked by, which made the
ranking meaningless. Rows the daemon failed to persist were counted and then
spoken aloud only in a log line at shutdown, so thirty thousand of them went
missing across five weeks with nothing an operator could see. And an ingestion
check that could only count its own events reported two sources as critical
failures while both readers were, provably, level with their source to the
second — an alarm that is always on, which is how the outage it was built for
happened in the first place.

Underneath the individual fixes there is one rule: a surface should say what
is true, and when it cannot know, say that instead. A daemon that predates a
field reports nothing rather than zero. An origin that cannot be read is an
unknown, not a clean bill of health. A tier whose baseline went missing stops
claiming a denominator rather than inventing one. Guessing is the failure the
whole release is about.

Two things also stopped being secrets. `tokenops detect` will tell you what
clients are on the machine without writing to any of them, and `tokenops
daemon restart` exists — six commands had been ending with "restart the
daemon" and none of them could name a command, because there was not one.

## v0.58.0 — routing that asks again

Routing had been configurable since it shipped and had never once rewritten
a request. It enforced through a proxy most people bypass, and what it could
say was a rule naming two models of one vendor, gated on how full the plan
window was — so a model that did not suit the work was corrected only when
capacity ran short.

That framing was wrong. The waste is not scarcity, it is inertia: a model is
chosen at the start of a session and stays chosen, and every turn afterwards
runs on it, including the lookups and the reading-around that something
cheaper and faster would do just as well. Nothing asks the question a second
time.

Now something does, once per turn, on every client that has a prompt-time
hook. Retrieval and research drop a tier; the cross-cutting refactors and the
hard debugging are left alone. It only ever routes down — suggesting a pricier
model is a spending decision nobody delegated.

Underneath it, tiers are derived from the rate card rather than written into
the source, because a hardcoded list of model names is wrong within a release.
That also made three things visible that had been quietly broken: free and
local models were skipped by the very component meant to find something
cheaper, because their price is zero; refreshed prices were being missed
because the card files them as patterns; and models retired years ago were
still setting the boundaries between tiers, pushing the current generation
into the middle of its own catalogue.

## v0.57.0 — the events that never arrived

Last release found four readers that measured nothing. This one follows the
same method past the reader and into the store, and finds the events being
counted correctly and then thrown away.

Enabling every reader on one real machine and counting the source stores
independently: opencode held 48,540 assistant messages with token usage and
the store had 33,316. Codex had 3,597 turns and the store had 845. Three
separate paths were discarding events while reporting success.

The event bus dropped envelopes whenever its queue filled. That is the right
trade for the proxy, which must not block on storage, and the wrong one for a
backfill: a poller advances its watermark before publishing, so a dropped
envelope is never revisited. The loss was permanent, surfaced only as one line
at shutdown, and reproduced identically on every restart. It was not even a
clean truncation — one month lost 63% of its rows, the next none — so every
period was understated by a different amount, which is harder to notice than
losing everything.

Underneath it, a write-ahead log that had grown to 372MB beside a 396MB
database, because SQLite can only checkpoint up to the oldest snapshot a
reader still holds and this store is read continuously. Inserts stopped
fitting in their deadline, and a rejected batch was discarded whole rather
than retried.

And retention, which could only be configured per event type — while every
vendor-usage reader writes the same type. One window governed Claude Code,
Codex, opencode and Cursor together with the live stream, so importing
history meant watching it be deleted four minutes later. `keep_by_source`
now sets a window per reader: keep what you imported, still trim what
streams in.

## v0.56.0 — every client, and four silent zeros

The arc of this release is one defect wearing six different outfits: a path
that reports success while measuring nothing. Four were found by checking each
reader against the real store on disk instead of against its own fixtures, and
comparing to a count taken independently.

- **`dx --source codex` reported "0 instructions across 37 sessions"** on 343
  real instructions. The reader keyed on an event current Codex stopped
  emitting, and skipped the record that replaced it to avoid double-counting.
  Zero-of-many is the worst version of this: an idle machine looks identical.
- **Codex spend had no model at all.** `Turn.Model` was declared and never
  assigned, so every Codex turn entered the store unpriceable and the whole
  client reported `$0`. 3597 turns, none of them costed.
- **The coach priced from the wrong card.** It used the catalog embedded in the
  binary, not the effective-dated one the daemon builds from your snapshots —
  so a machine whose snapshot knew `gpt-5.5` still had its budget measured
  against a card that did not. On one real rollout that was the difference
  between `$0.00` and `$0.68`, and the nudge that never fired.
- **`dx` was grading a benchmark harness.** Sessions run in throwaway
  directories were 94% of a 7-day window here, and set every grade: a median of
  2 turns per instruction against the operator's real 17, and straight `A`s.
- **Prompt text reached only one reader**, so `story` titled 1258 of 1258
  opencode tasks "(no instruction text)" — and every instruction opened its own
  task, because the continuation lexicon cannot judge words it does not have.

### Every client, and an honest matrix

Claude Code, Codex, Cursor and opencode now all read, all carry a coaching
nudge, and the two whose hooks can decline a read now refuse one. Codex's
payload matches Claude Code's field for field; Cursor carries its tokens
inline; opencode needed a generated plugin and delivers through a TUI toast.

Three cache conventions had to be told apart to price them — Claude Code keeps
input and cache disjoint, Codex nests cache inside input, Cursor nests both —
and getting that wrong overstates a Cursor turn by five orders of magnitude on
the uncached line.

Where a client *cannot* support something, the matrix says so with the reason
rather than calling it pending. Codex has no file-read tool; Cursor's
`beforeReadFile` cannot decline. `hooks install` refuses both.

### Also

- **Smart routing** decides per turn from task class, plan-window pressure and
  the live rate card, with no rules table — and `tokenops_routing_advise`
  reaches every MCP client, including the ones with no proxy.
- **The rate card refreshes itself** daily and applies to the running daemon,
  rather than writing a snapshot nobody reads until a restart.
- **GPT-6 Astra** priced and pinned, with the two published rules this schema
  cannot express written down rather than left to be discovered from a number
  that looks low.
- **`story` gained two more audiences** — evidence for someone you bill, state
  of the world for a teammate — plus an MCP tool so the agent reads its own
  history back.
- **`coaching.quiet`** rate-limits the proactive channel, and the coach now
  argues for `intervene` from your own ledger instead of waiting to be asked.

## v0.45.0 – v0.54.3 — agent experience, and honest units

The arc since v0.44: the product stopped accounting only in dollars — which
are always `$0.00` on a flat-rate subscription — and gained a second
dimension, what sessions are like to work with.

- **`tokenops dx`** (v0.46–v0.49). Agent developer-experience metrics derived
  from transcripts every client already writes: turns per instruction,
  first-try rate, rework, interrupts, escalation to subagents. Graded A–F on
  the worst dimension rather than the average. Reads Claude Code, Codex,
  opencode, and Cursor, and reports per upstream provider.
- **Quality vs context** (v0.53.0). Answers "does quality degrade as context
  grows" from the operator's own transcripts — rejection rate and repeated
  tool calls per context band — and says plainly when the corpus is too noisy
  to call it.
- **Coaching completes across clients** (v0.50–v0.51). Prompt coach and reply
  coach both read opencode, closing coverage over every client that keeps a
  local record.
- **Tokens and the rate-limit window became first-class** (v0.45.0). Two KPIs
  stopped grading data that was never measured, and `init` became the single
  command that wires the machine.
- **Counterfactual cost is labelled as such** (v0.51.1). On a subscription,
  "cost" is what the session would have cost at list price. Every tier now
  says so, and "your budget" is reserved for one the operator actually set.
- **The MCP server stopped counting itself** (v0.54.0). `mcp-session` pings
  carry no tokens and no cost but were landing in request totals.
  `--include-source` re-admits excluded sources by name.
- **Security and release-path fixes** (v0.54.1–v0.54.3). A reachable
  `golang.org/x/text` infinite loop (GO-2026-5970) closed, and three attempts
  to get that fix in front of Homebrew users — the first two failing in the
  release workflow's credentials, not the product.

## v0.44.0

- **Supervise ingestion.** `tokenops daemon install` writes a launchd LaunchAgent (macOS) or systemd user unit (Linux) that keeps `tokenops start` alive across reboot. `tokenops serve` is the MCP server and does not ingest.
- **Dashboard token file is `0600`.** `~/.tokenops/daemon.url` carries the auth token; it is no longer world-readable.
- **Gemini 2.5 rates pinned** against Google's pricing page (cache-read is 10% of input: Pro `$0.125`, Flash `$0.03`, Flash-Lite `$0.01`).

See the [changelog](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md) for the full 0.22–0.44 arc (pricing research, fmt, coaching hooks, Homebrew cask, the 27-day ingestion outage).

## v0.43.0

- **Dead ingestion is visible.** `tokenops status` names how long a source has been silent and tells you to run `tokenops start` (not `serve`). Spend tools add `measurement.trusted: false` when the pipeline is dead. `tokenops_status` reports when no ingestion daemon is reachable.

## v0.21.1

- **8-KPI agent scorecard.** The wedge trio (FVT / TEU / SAC) gained five agent-workflow KPIs in v0.19–v0.21, all graded A–F against tuneable thresholds:
  - **CHR** — Cache Hit Ratio (≥90/70/50)
  - **CGR** — Confirmation Gate Rate (≤10/20/30)
  - **RGR** — Regenerate Rate (≤5/10/20)
  - **TCS** — Tool Success Rate (≥95/85/70)
  - **DAR** — Destructive Action Rate (≤0.5/2/5)
- **Honest grading (v0.21.1).** TEU stays N/A (default 15%) when the optimiser never ran — no F for being on JSONL-only mode. SAC syncs payload attribution from indexed columns so column-side backfills propagate. CGR strips autonomous-loop sentinels (`continue` ×100+ from `/loop` mode isn't a human ack). Overall grade F → B on real 30d data.
- **`tokenops task start|done|list`** (v0.20–v0.21). Operator-marked task boundaries persist to `~/.tokenops/tasks.jsonl`. `task list --metrics` enriches every task with the events-store rollup: turns, input/output tokens, cache-aware cost, TTFUO, cost-per-turn.
- **`tokenops coach prompts`** ranked recommendations (v0.18) project tangible savings (tokens / dollars / hours) per win, grounded in the operator's own turn averages. JSON output ships `{ findings, turn_stats }` for MCP-host UIs.
- **Coach replies + Anthropic admin-API attribution (v0.17).** `tokenops coach replies` detects output-compression patterns (caveman-skill verdict, article density, filler density). Anthropic admin-API events now stamp synthetic `agent_id` / `workflow_id` so they don't drag SAC down.

## v0.16

- **Per-project rollups.** `claudecodejsonl` poller stamps `agent_id = "claude-code:<project>"` and `workflow_id = "claude-code:<project>:<session>"`. `group=agent` answers "which project burns the most".
- **Cache hit-rate tile + `/api/spend/cache_stats`.** The dashboard `Cache hit: XX.X%` tile; for agent workloads the ratio routinely sits >95%.
- **Waste-detector profiles** for `claude-code:` and `codex:` workflows (900k / 250k peak respectively) — stops short-workflow defaults firing on every session.
- **Codex parity** for the v0.14.x JSONL improvements: `SessionID`, `AgentID="codex"`, `WorkflowID="codex:<session>"`, and `CachedInputTokens` on every PromptEvent.
- **`tokenops coach prompts` auto-discovers Codex.** Scans both `~/.claude/projects` AND `~/.codex/sessions`.

## v0.14.x – v0.15.0

- **`tokenops coach prompts` (v0.15.0).** Heuristic prompt-quality feedback for Claude Code users. Walks `~/.claude/projects/**/*.jsonl`, extracts human-typed turns, reports length distribution, vague/ack/repeat counts, and concrete recommendations. Prompt text is read at scan time and is **never persisted** to the event store. MCP tool `tokenops_coach_prompts` exposes the same surface to agent hosts.
- **Coach wiring for Claude Code (v0.14.3).** JSONL events now carry `session_id` + `workflow_id` on the indexed columns (not just attributes), so `tokenops replay` + the waste detector resolve Claude Code sessions. Coach surface was dark for JSONL data; now it surfaces oversized-context + runaway-growth findings per session.
- **Cache-aware pricing (v0.14.2).** Dashboard cost over-estimated by ~9x because cache reads billed at the new-input rate ($15/M for claude-opus-4-7) instead of the cache rate ($1.50/M). Poller now writes the cache split; aggregator reads it back via `json_extract`. On real 7-day data: **$94k → $10k**.
- **`Summarize` cost recompute (v0.14.1).** Dashboard TOTAL COST showed `$0.00` for any data from vendor-usage-jsonl sources (those readers ship token counts but no prices). Fixed by recomputing via `spend.Engine` in the same path `AggregateBy` already used.
- **`tokenops vendor-usage enable <source>` (v0.14.0).** Writes a vendor-usage source's config block so operators don't hand-edit YAML to flip the v0.13.0 pollers on. Six sources: `claude-usage-meter`, `cursor`, `github-copilot`, `codex-jsonl`, `claude-code-jsonl`, `anthropic-admin`. Secrets accept env-var fallback (`TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY`, etc.).
- **Four new vendor-usage sources (v0.13.0).**
  - **Codex CLI JSONL reader** — parses `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`, surfaces OpenAI's authoritative `rate_limits` block (5h primary + weekly secondary used_percent + resets_at).
  - **GitHub Copilot quota poller** — calls `api.github.com/copilot_internal/user` with the OAuth token Copilot IDE plugins already manage. Auto-discovers from `~/.config/github-copilot`.
  - **Cursor `/api/usage` poller** — cookie-based scrape of cursor.com.
  - **Claude usage meter scraper** — polls `claude.ai/api/organizations/{org_id}/usage` with the operator's browser `sessionKey`. **The only source that surfaces the official Claude Max weekly utilization %.**
- **Claude Code JSONL reader (v0.12.0).** Parses `~/.claude/projects/<project>/<session>.jsonl` — Claude Code's live per-turn conversation record — and emits one PromptEvent per assistant turn with the full `message.usage` block. The v0.10.2 stats-cache reader was lagging by days; deprecated in favour of this.

## v0.10.x – v0.11.0

- **`tokenops.local` via mDNS (v0.10.1)** — The daemon advertises itself over zeroconf on Start, so the dashboard URL becomes `http://tokenops.local:7878/dashboard` instead of a bare loopback address. The `tokenops_dashboard` MCP tool prefers it; falls back to `127.0.0.1` when `.local` resolution isn't available.
- **Vendor /usage ingestion (v0.10.2)** — Two new signal sources upgrade Anthropic confidence beyond the heuristic default. The **Claude Code stats cache reader** parses `~/.claude/stats-cache.json` and emits per-(date, model) deltas (signal_quality → medium). The **Anthropic Admin API poller** calls `/v1/organizations/usage_report/messages` every 5min with an admin key (signal_quality → high). Both wired through `config.vendor_usage.*`; both honest about the Claude Max 5h-window blind spot.
- **Dashboard auth (v0.10.3)** — `/dashboard` + `/api/*` now require a shared-secret token (`/healthz`, `/readyz`, `/version` stay public). Daemon mints + persists the token automatically at `~/.tokenops/dashboard.token`; the MCP tool returns a clickable URL with the token pre-attached so the operator gets a one-click authenticated visit. Browser-style auth mints a session cookie and 303s to a clean URL so the token never lingers in history.
- **Auto-detect on init (v0.10.0)** — `tokenops init --detect` reads your installed AI clients (Claude Code/Desktop, Cursor, ChatGPT Desktop, env-var API keys) and prints the exact plan-set commands. Run it once, paste what fits.
- **Interactive dashboard (v0.10.0)** — A Vue + D3 dashboard ships with the daemon at `/dashboard`. Hourly cost line, tokens-per-bucket stacked bar, KPI tiles, 15s auto-refresh. Driven by the same `/api/spend/*` endpoints the CLI uses.
- **Inline charts in MCP responses (v0.10.0)** — `tokenops_session_budget` leads with a coloured headroom gauge (green / amber / red by overage band); `tokenops_burn_rate` ships a sparkline. Rendered inline in markdown so every MCP client shows them today.
- **Dynamic-cheapest coaching router (v0.10.0)** — The coaching pipeline picks the lowest blended-rate model per provider from the pricing table at runtime. No hardcoded model names; pricing updates flow through automatically.
