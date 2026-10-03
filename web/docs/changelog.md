# Release highlights

The curated arc of what changed and why. For every commit, see the
[full CHANGELOG](https://github.com/klarlabs-studio/tokenops/blob/main/CHANGELOG.md);
for binaries, the [releases page](https://github.com/klarlabs-studio/tokenops/releases).

Current release: **v0.94.0**.

## v0.94.0 — Setup that asks less, and routers left to route

`tokenops init` now binds your plans from what your clients already
record: Claude Code keeps the plan you are signed in with, Codex writes
its plan type into every session. Each binding says which client
reported it, and a plan you bound yourself is never changed. In a
terminal, init then asks only what nothing on the machine could answer,
each question with a default; without one, or with `--yes`, it asks
nothing.

Where an external router chooses the model for each turn, FireRouter in
front of Claude Code or OpenRouter Auto in opencode, TokenOps no longer
chooses a second time. Its model advice and subagent moves stand down for
that harness, and the coach says which router decides.

## v0.93.0 — Coding plans and gateways

`plan headroom` now shows the coding plans you use with the windows the
vendor itself reports: z.ai's GLM Coding Plan, Kimi Code, MiniMax's
Token Plan, Synthetic and Chutes. TokenOps reads them with the key your
harness already sends that vendor, at that vendor's endpoint only. A
plan TokenOps has no catalog entry for shows as `subscription`, with the
vendor's windows. DeepInfra and Vercel AI Gateway show their balance and
spend.

A harness pointed at a gateway you run or subscribe to is now read too.
TokenOps asks the gateway's health route what it is, without your key;
only a recognised LiteLLM, Bifrost or ClawRouter is then sent the key,
at the same address, to read that key's own budget.

These readers follow each vendor's published endpoint or its own
client's code. They have not yet met a live account of every kind, so
the first readings are the real test.

## v0.92.0 — One API for every surface

The daemon's local API now answers everything a menu bar, a script or a
dashboard needs, from the same code as the MCP tools and the CLI, so all
three give the same figures. It reads plan headroom and every vendor
window, the session budget, status, mode, the coach, data sources,
agent DX, the work account, spend and pricing; and it changes the mode,
budgets, routing rules, plan bindings, the preferred model, routing
answers, outcomes and the coach. Writes need the API token, take only
JSON and are written to the audit log. It serves derived figures only:
the operator's instructions are withheld. The contract is a generated
OpenAPI 3.1 document; see [Daemon API](/guide/api).

Claude's windows now also come from Claude Code's own status line, so
headroom shows them without a claude.ai login, and behind a Claude apps
gateway it shows the spend limit too.

## v0.91.0 — Every window, as the vendor reports it

`plan headroom` now shows every usage window the vendor reports, as the
share used and when it resets: Claude's 5-hour, weekly and model-scoped
weekly windows from the Claude usage meter, and Codex's windows, which
were not shown before because Codex reports a percentage and no message
cap. The busiest window sets the risk, so a weekly limit near its end
counts even when the 5-hour window is empty.

Claude's 5-hour share was shown as a message count against the plan's
published cap, a number Anthropic never reports. Headroom and the
session budget now show the vendor's percentages only.

## v0.90.0 — Every provider you pay per token

`plan headroom` now shows every provider you used and pay per token for,
without setup: OpenRouter, z.ai, Kimi, DeepSeek and the rest appear as
pay-as-you-go with their spend this month, each row named by provider.
Pay-as-you-go now counts every turn an API key was billed for, including
Claude through a router on your Anthropic key.

Where a vendor documents an account endpoint, TokenOps reads its own
figures with the key your harness already sends it: OpenRouter's key
spend and cap, and DeepSeek's and Moonshot's prepaid balance. Keys come
from Claude Code's settings, Codex's providers, opencode's auth.json and
config, or the environment; each goes only to its own vendor and is
never stored.

## v0.89.0 — Fireworks, and what a figure means

When a Fireworks key is on the machine, TokenOps reads the account's own
figures from Fireworks' API: on a company account your spend against your
per-user cap, otherwise the month's spend against the account's limit.
The key is FIREWORKS_API_KEY or FireConnect's own, fetched for each
reading and never stored. `plan headroom` shows Fireworks as
pay-as-you-go without anything to set up.

`tokenops explain wall-clock` says what a figure measures, how it is
worked out and how to read it, with its grade bands; `tokenops explain`
lists all thirty. Agents get the same answers from `tokenops_explain`.

Headroom read at most 100,000 events, oldest first, so a busy month
lost its newest readings; it now reads the whole range. Installing the
status line says what happened in plain words, and Cursor is no longer
called unreadable when you have not used it lately.

## v0.88.1 — Cursor's timestamps

Current Cursor builds write a message's time as a string. The Cursor
reader accepted only a number, so on a real machine it read all 269,031
messages as an unknown schema and `dx` reported Cursor as unreadable. It
now accepts a number, a numeric string or an ISO time.

## v0.88.0 — opencode 2, and a line under the prompt

opencode 2 keeps its sessions in new tables of the same database, copies
1.x sessions across a few at a time, and does not run 1.x plugins. An
upgraded opencode would have gone dark in TokenOps. Every opencode reader
now reads both versions' tables, counting each message once, and
`hooks install --client opencode` writes a plugin for each version
installed: read-guard and route-guard work in opencode 2, verified in a
real 2.x session.

TokenOps also has a line under Claude Code's prompt, in Klarlabs colours:
the quota windows Claude Code reports, the context against where the
session compacts, the cache, the session's cost in your currency, and the
coach's open tip. It reads only what Claude Code hands it and files
TokenOps already keeps, and takes about 12 ms. `init` installs it and
keeps a status line you already have under it; uninstalling it is
remembered.

## v0.87.0 — who bills what

A coding agent no longer talks to one vendor. Claude Code pointed at
Fireworks through FireConnect sends easy turns to Fireworks' open models
and hard ones, through FireRouter, to Claude on an Anthropic API key.
TokenOps recorded all of it as Anthropic's: Fireworks turns sat on the
Anthropic plan or spend limit with no price, and Claude turns through the
gateway counted as covered by Max.

Every request now records who bills it (ADR 0009). TokenOps works it out
on its own: from Claude Code's endpoint, kept as a dated route history
and dated by FireConnect's own settings backup; from each Codex
session's provider; from the provider opencode records per message. A
gateway's models are billed by the gateway at its own rates, and a plan
covers only turns through its vendor's own endpoint. Usage recorded
under the old rules is corrected at start, and the audit log says so.

The catalog grows past Claude and OpenAI: z.ai's GLM Coding Plan, opencode
Go and Zen, Kimi Code, MiniMax, Alibaba's coding plan, DeepSeek, Cerebras
Code, Synthetic and Chutes, with endpoints that tell a coding plan from
the same vendor's pay-as-you-go API, and prices from models.dev where
the vendor publishes them. Spend limits are dated, so last month keeps
last month's limit, and any provider billed per token can be measured
against its own cap. `init` turns on the transcript readers for the
clients on your machine, and the MCP spend summary speaks your currency.

## v0.86.2 — fixing our own mistake without asking

v0.86.1 stopped recording usage-based Enterprise as covered by its plan,
but usage already recorded that way still read $0 until the operator ran a
command to correct it. That asked them to fix a TokenOps bug. The daemon
now does it on its own at start: the plan history says which stretches
were on a plan billed at API rates, only those are re-marked, and each
correction is recorded in the audit log.

## v0.86.1 — what Enterprise actually costs

Usage-based Claude Enterprise is billed at API rates from the first token,
but TokenOps treated it like a subscription and recorded its usage as
covered by the plan, at $0. Real cost read zero and the spend limit read
0% used. Coverage now follows the kind of plan: a spend-limited plan
covers nothing, its usage is priced at API rates, and headroom counts it
against the limit, graded by the sources that fed it. Usage already
recorded as covered is corrected by re-binding the plan with `--since`.

ChatGPT Pro now comes in three tiers, $100, $200 and $500, keyed on price
because OpenAI moves the multipliers. Headroom names a binding that
contradicts the plan type Codex itself reports. `dx` and `story` no longer
fail over MCP when one client's store cannot be read: they report the
others and name the one that failed. ADR 0009 sets out how TokenOps will
attribute work that runs through gateways and routers such as Fireworks
and OpenRouter.

## v0.86.0 — one currency, with the rate it used

`spend` used to set a plan bill in euros beside usage valued in dollars,
which invites comparing €243 with $11,300 as if they were one unit. Every
total is now shown in your currency, chosen once by `tokenops init` from
your system region (or `--currency`), with the dollar figure beside it and
a line naming the rate: the ECB's daily reference rate, fetched at most
once a day and cached. A converted amount moves with the exchange rate
even when usage does not, and the report says so. This is the second
outbound call TokenOps makes on its own; it downloads a public rate,
sends nothing, and `money.fetch_rate: false` turns it off.

## v0.85.0 — the plan you were on then

The config holds the plan in force now, and every report over a past
period used it, so switching from Plus to Pro rewrote last month's cost
and limits. Plan switches are now recorded with the date they took effect
(ADR 0008). `plan set --since` backdates a switch and re-marks usage that
was recorded as billed per token in between as covered by the plan, in
the audit log. `--price` and `--currency` record what your bill says,
regional price and tax included; without them plan cost uses the catalog's
US list price, which now covers fifteen plans and was checked against
each vendor's own pricing page. `plan history` lists every switch.

`dx` now splits instructions by model and reasoning effort, read from
Claude Code, Codex and opencode transcripts, so an effort setting can be
judged by what it did to your work rather than by its name.

## v0.84.0 — models nobody may route to

A person or a company can now rule models out with `model_policy`: allow
and deny globs over `model` or `provider/model`, deny winning, a non-empty
allow list being exclusive (ADR 0007). The file is plain YAML, so device
management can ship it. Every route respects it — proxy routing, smart
routing, subagent moves and routing advice — independently of how the
coach is set. A request for a ruled-out model is moved to the
closest-priced permitted one rather than failed.

## v0.77.0 – v0.83.0 — one coach

Coaching settings had grown into separate keys that each decided part of
what the coach said and did. They are now one coach with two dials
(ADR 0006): autonomy — off, advise, ask, autonomous — decides who acts,
and verbosity decides how much it says. Four powers sit under it: inform,
waste (redundant re-reads, compacting late), models (moving subagents to a
cheaper model, or asking first) and context (where each agent compacts).
`tokenops coach preset` sets all of it in one choice and wires the hooks
on every installed agent. The coach records whether its advice was
followed and quiets advice you keep ignoring, and `tokenops coach` shows
the live quota window and the next tip. `coach migrate` writes the
equivalent of your older settings, so behaviour does not change.

## v0.72.0 – v0.76.0 — counting what was already there

The proxy now measures streamed agent traffic and compressed responses.
Claude subscription polling decodes the vendor's unified limits and keeps
the quota windows the vendor reports instead of assuming them. The event
store stopped losing rows to write-lock contention, and pollers that
re-read history no longer store events twice. On flat-rate plans the
coach speaks in quota rather than dollars, and waste findings that only
measured how long a session was are gone.

## v0.71.0 — evidence before optimization

TokenOps can now connect work, actors, executions, interventions, and outcomes
through one canonical event history. Its CLI and MCP guidance use that shared
evidence to review work, prepare a task, inspect resource pressure, and explain
what the control loop may do next.

Randomized routing is deliberately stricter: an experiment counts only full,
durably attributed executions with successful upstream responses and explicit
human or verifier outcomes. A bounded five-pair OpenAI trial exercised those
rules end to end and measured a 95% cost reduction for its exact-output cohort;
that result is evidence for that cohort, not a claim of general model-quality
equivalence.

The release also adds verified GPT-6 Sol and Luna pricing, authoritative usage
parsing from provider responses, content-safe JSON outcome verification, and
canonical subscription plan names for OpenAI and Anthropic products. The
daemon's background workers now expose supervised component health instead of
hiding behind one process-level status.

## v0.70.0 — numbers that say where they came from

Four of this release's five changes close the same kind of defect: a
figure, or a clean bill of health, that TokenOps had no basis for and
reported anyway.

An unpriced model used to contribute `0` to a cost total. The gap was
reported in one rollup and not in the other — and the silent one is what
feeds burn rate, forecast, top consumers and the dashboard, so the total
an operator acts on presented itself as complete. A proxy with no
tokenizer shipped events with zero token counts by design, which
downstream is indistinguishable from a request that consumed nothing: a
misconfigured install reported no usage as confidently as a working one
reported real usage. An optimizer that could not measure its own effect
returned the same `0` as one that had measured no effect. And a poller
being refused every minute produced exactly the same silence as a vendor
nobody uses — all four pollers tracked their last error behind a method
nothing called, and none recorded a success, which is the fact that
separates the two.

Numbers now carry whether they were observed, derived or estimated, and
how much they account for. Ingestion health is data rather than a warning
sentence: `GET /api/sources` and the `tokenops_data_sources` tool report
when each source was last seen, when its reader last succeeded, and what
it last failed with.

The `tokenops fmt` recovery store — the full raw output of every command
it wraps, which is the broadest capture surface in the product — was
world-readable, unpruned and unredacted. So was the learning index beside
it, and the domain-event log. All are now owner-only, and every write
repairs the permissions it finds rather than only the files it creates,
because those files already exist on every installed machine.
`SECURITY.md` had also promised for sixty minor versions that vendor
credentials could be supplied by the environment. Nothing implemented it.
Now five can be, and the policy records the correction.

Underneath, TokenOps gained the four primitives it never had — Work,
Actor, Execution and Outcome. Outcome had no non-test match anywhere in
the tree, which is why TokenOps could only ever optimize what work
consumes: it could make work cheaper while making it worse and have no
way to notice.

## v0.69.0 — the routes that were never behind the door

TokenOps' security policy said `/dashboard` and `/api/*` had required a
shared token since v0.10.3. That was true of most of `/api/*`. Six routes
were not covered: the audit log, the four rule-intelligence endpoints, and
the domain-event counts. They had been mounted beside the guarded routes
rather than inside them, and Go's router prefers an exact path over the
pattern the guard wrapped — so the guard never saw them.

The daemon binds to loopback by default, which is what kept this a local
matter for most people. A daemon bound to a LAN address, which TokenOps
supports and documents, served its own audit log to the network.

Every `/api` route now registers on the guarded mux, so a route cannot
opt out of authentication by being added in the wrong place. Health
probes stay open, as probes must. The daemon's own test had asserted the
unauthenticated answer was correct, which is how this survived as long as
it did; it now asserts the opposite.

If you call `/api/domain-events` from a script, it now needs the token.
`tokenops events` and the MCP tool read it from the daemon themselves.

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
