# Changelog

## Unreleased

### Added

- **copilot:** the Copilot reader finds the Copilot CLI's sign-in, and reads GitHub AI Credits. It looked for a token only in the IDE plugins' `apps.json`/`hosts.json`, so an operator who signed in through the Copilot CLI, which now keeps its OAuth token in the macOS Keychain (service `copilot-cli`, plaintext fallback `~/.copilot/config.json`), was read as not signed in. After the IDE files it now tries the Copilot CLI's own order: `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` (a classic `ghp_` token, which Copilot refuses, is passed over), the Keychain item read quietly (never a prompt; when macOS would ask, health says so and how to sign in instead; nothing with `keychain.disabled`), then `~/.copilot/config.json`. The GitHub CLI's token is another application's sign-in with scopes beyond Copilot, so `gh auth token` is asked only with `vendor_usage.github_copilot.use_gh_cli: true` (`tokenops vendor-usage enable github-copilot --use-gh-cli`). Since 2026-06-01 monthly Copilot plans bill in GitHub AI Credits: the month's allowance is now a vendor window, "month (AI credits)" (premium requests keep their name on annual plans), with its reset, in `plan headroom`, glance and the API, and the credits used are stored with it. The catalog gains `copilot-pro-plus` ($39, 7,000 credits), `copilot-max` ($100, 20,000) and `copilot-enterprise` ($39 a seat, 3,900); `copilot-individual` is shown as Copilot Pro (1,500).
- **brand, menu bar:** one set of design tokens for every TokenOps surface. The menu bar, the docs site and the team app each carried their own copy of the lime-and-ink palette, and the copies had drifted: the dark lime used for links and text was `#3F6B0B` in the menu bar and `#4D7C0F` on the docs site, which is 4.99:1 on white and so just short of the 5:1 the brand promises; the quietest text was `#70757F`, 4.24:1 on the menu bar panel's grey fills. `assets/brand/tokens.json` now holds the colours (light and dark), type scale and font stacks, spacing, radii, shadows and motion, and `go generate ./internal/tools/gen/designtokens` writes them as CSS custom properties (`assets/brand/tokens.css`, `--tok-` prefixed, following the system appearance or pinned with `data-theme`) and as the token table with measured contrast ratios in `assets/brand/README.md`. A test fails when a generated file is stale or a colour falls below the contrast it names. The menu bar panel reads its colours, fonts, radii, shadows and timings from the tokens; other surfaces copy `tokens.css` as it is. What changes on screen: the faintest text is `#686D77` (5.19:1 on white), hairlines are `#E4E6EA` (`#262C3C` on ink), quiet fills `#F7F8F9` (`#1B2030`), and the bar and select border on the selected lime tab are ink instead of white, which the brand never puts on lime. `#4D7C0F` stays the logo's ring on light grounds and is never text.

### Changed

- **docs:** the documentation site moves to https://team.klarlabs.de/, served from the root of the team service. It is no longer built from this repository: `web/docs` and the GitHub Pages workflow (`pages.yml`) are removed, so the site stops deploying from here. GitHub Pages stays on until the new site is live; the maintainer turns it off afterwards, and old `klarlabs-studio.github.io/tokenops/` links need a redirect then. The CLI, configuration and API reference pages stay in the repository under `docs/reference/`, where the generated provider tables and the tests that check the CLI and API docs read them. Homepage links in the README, the issue template, the goreleaser cask, the npm and Python packages, and the systemd unit point at team.klarlabs.de.

### Fixed

- **antigravity:** a versioned Gemini quota keeps its own name. Antigravity 1.4 reports Gemini 4 Argon as a quota group of its own, next to the Gemini and Claude/GPT groups, but any group whose name contained "gemini" was renamed "Gemini", so the Argon limit showed as a second, unexplained "Gemini weekly" window and the Argon limit looked missing. A Gemini group whose name has a version number now keeps it ("week (Gemini 4 Argon)"); the unversioned Gemini groups read as before.
- **fireworks:** your own Fireworks spend is found without `--user-id`, and an account-wide figure says why. Nexus CLI 0.10 keeps no `minted-key.json` and no `ssoAccountId`, so no file named your user: the reader quietly fell back to the whole account's month spend, and nothing said it had. When no file names your user, TokenOps now finds it by email: `--email` (`tokenops vendor-usage enable fireworks --email <you>`, `vendor_usage.fireworks.email`), else whom `nexus status --json` names (a user it names is used as is), else the email in Nexus CLI's files, read only from email fields in `~/.nexus` (then `~/.fireconnect`) — never from key fields. TokenOps never runs `nexus login`. It looks that email up among the account's members (one member, not a service account, or none) and keeps the user in the daemon's memory for a day or until the key changes; your config is not touched, and `--user-id` still wins. When your own figure is still not read (no user found, the lookup or your own read failed with its status, a user you set that the account does not have, an account that is not your key's, or you have no cap), the account's figure says why: the glance card's footer ("account-wide: your user wasn't found — set it with `tokenops vendor-usage enable fireworks --user-id <id>`"), `tokenops vendor-usage status` and `spend_scope_reason` in the API and MCP glance. A member blocked by their cap is shown as blocked: the card says "Blocked until 1 Nov" (local date) and the glance carries `limit_reached` and `blocked_until`. An `exceededUntil` in the past, a null one, or one not enforced no longer reads as blocked, and a null cap is no cap rather than a $0 one.
- **copilot:** an organisation seat no longer reads as unlimited headroom, and one Copilot bucket no longer stands for another. A poll stores one snapshot per bucket at the same moment, and the monthly reading took whichever came first: an unlimited chat bucket at "100% remaining" could stand for a nearly spent premium allowance, and an organisation seat, which reports every bucket unlimited with no personal allowance, showed 0% used and low risk. The monthly reading now takes the busiest bucket with an allowance of its own from the newest poll; a seat with none says the vendor reports no personal allowance, that its limit is the organisation's budget (not reported to members), and the AI credits used this cycle or that none were reported, with the risk unknown. A user record with no buckets, or a capped bucket that reports no share used, is now a failed poll with that reason instead of a successful empty one. The reader also no longer stops for good when no token is found at start: the daemon does not restart readers, so a sign-in made later went unread until a restart, and nothing said why; it now looks again each poll and health names the reason.
- **gemini:** Gemini CLI is described as what it now serves, and a Google AI Pro binding points at Antigravity. Since 2026-06-18 Gemini CLI serves only Gemini Code Assist Standard and Enterprise licences and paid API keys; Google AI Pro, Ultra and the free individual tier moved to Antigravity CLI. TokenOps still presented the Gemini CLI reader as the way to see a Gemini subscription, so an operator on Google AI Pro switched on a reader with nothing new to read, and the plan was still called Google One AI Premium, a name Google retired. The plan is now `gemini-ai-pro`, shown as Google AI Pro; `gemini-ai-premium` keeps working as an alias. With it bound, the Gemini CLI row of `tokenops vendor-usage status` (and the data-sources view over MCP) says Gemini CLI stopped serving Google AI Pro on 2026-06-18 and points at the Antigravity source, with how to connect it or that it is connected. The Gemini CLI source's description and setup text name who it is for. Recordings made before the cutoff are still read.
- **plans:** plan advice no longer says "upgrade" to the plan you already have. On the top tier (Max 20x) with a lockout and nothing larger to move to, the replay recommended the same plan with metered overflow, which costs the same when no window went over its cap, and printed `upgrade` with `$0.00` saved. `tokenops team preview` and the team upload showed it as `upgrade`, recommended `claude-max-20x`. An upgrade now only goes to a larger allowance; with none, the verdict is `stay` and the summary says the plan is the top tier and the lockouts are the price of its allowance. The team wire verdicts are unchanged.
- **daemon:** `tokenops daemon install`, which the Homebrew upgrade runs, restarts the daemon once when it does not answer after starting. On an upgrade to 0.113.1 launchd refused the first start of the new binary (exit reason `OS_REASON_CODESIGNING`, "spawn failed") and the daemon stayed down until restarted by hand, while the next start of the same file worked. The upgrade hook cannot fail the upgrade, so its error went unseen. It now waits five seconds, restarts the unit and checks again, and reports an error only when the retry does not come up either.
- **z.ai, Zhipu AI:** the z.ai reader reads the GLM Coding Plans sold since 2026-07-30. Those plans report their 5-hour and weekly windows as `CREDIT_LIMIT` rows where older plans say `TOKENS_LIMIT`, and the reader kept only `TOKENS_LIMIT`, so on a newer plan `tokenops vendor-usage status` reported z.ai as read with no windows and no headroom was shown. Both row types are now read the same way (a row without a percentage is read from its credits spent and allowance), and an answer with no window TokenOps can read is an error naming the row types it held, never an empty reading. Zhipu AI, z.ai's mainland-China platform, serves the same quota endpoint on `open.bigmodel.cn`, so it has a reader too (`zhipuai-account`), with its own keys.
- **cursor:** the Cursor reader reads the plans Cursor sells now. It read only `/api/usage?user=`, the request counts of the plans Cursor retired, which for every current plan answers with no allowance; `tokenops vendor-usage status` showed Cursor as read while headroom had no real figure (a row without an allowance was stored as a share of -1%), and the catalog offered "Cursor Pro" and "Cursor Business" at 500 requests a month. It now reads `/api/usage-summary` first: the share of the billing cycle's included usage (the plan's own percentage, else its cents, else a team member's cap or the team's pool), resetting when the cycle ends. For the plans sold today, which share a weekly pool across Cursor and Grok and for which usage-summary keeps reporting a stale monthly figure, it reads the pool from `/api/dashboard/get-sand-usage-status` and shows it as a weekly window. The request counts are read only when usage-summary is unavailable, and only an answer with a request allowance counts; any other answer is an error with its reason, never an empty reading. The catalog has Cursor Plus, Premium, Super, Ultra and Teams; `cursor-pro` and `cursor-business` remain, as the legacy plans they are, without the retired request cap.

## 0.113.1 - 2026-10-27

### Changed

- **daemon, menu bar:** an upgrade restarts the daemon and refreshes the menu bar app itself, instead of relying on the Homebrew upgrade hook. Until now the hook was the only thing that moved a running daemon onto a new binary: a daemon started before an upgrade kept running the old code until the hook re-applied its unit, and a daemon installed without the hook kept it until restarted by hand. The daemon now checks every 30 seconds whether its binary has been replaced. A supervised daemon (launchd or systemd) that sees a replacement on two checks in a row stops cleanly and the supervisor starts the installed version, so the switch lands within about a minute of the upgrade. A foreground `tokenops start` logs one warning and keeps running. When the daemon starts, an installed menu bar app that differs from the one shipped with this version is replaced with it, and reopened only if it was open; an app that was never installed is left alone. This prepares moving off Homebrew's deprecated `postflight` cask hook, which Homebrew is replacing with declarative steps that cannot restart services.

## 0.113.0 - 2026-10-27

### Added

- **boundaries:** a loop breaker. An agent stuck in a loop (the same test run again and again, or the same few calls in the same order with nothing changing) went on spending until someone looked, and TokenOps only named it afterwards, as retry-loop waste in the weekly report. A new condition, `loop_calls`, counts the session's latest tool calls that went round a loop with no file changed: the same call (the same tool with the same input) repeated, or the same cycle of two or three calls at least three times. An edit or write not seen before is progress and starts the count again. `tokenops init` writes a `loop-breaker` rule (`loop_calls: ">= 4"`, `require: ask`) on a new config, in shadow: it records each session it would have paused and binds nothing; `tokenops boundaries loop-breaker` adds it to an older config. The findings say "The loop breaker would have paused 2 sessions this week" with the same *Promote* action as the headroom pause; promoted, the next call in a loop asks at Claude Code's prompt or, unattended, is refused with what repeated and a nudge to change approach or ask the user. The pause point now reads each call's tool input, only to hash it: it keeps the tool's name and the hash (the newest 64 calls per session, under `~/.tokenops/boundaries/calls/`, removed once a session has been idle for a day), never the input. It works in Claude Code only, whose hook sends the input; `tokenops boundaries status` shows the rule as not evaluated in opencode, Codex and Cursor. Edits made through a shell are not seen as progress, and polling the same command looks like a loop, which is why it starts in shadow.
- **coach:** `tokenops coach lint-context` finds what in every session's context changes between sessions and so breaks the prompt cache. A provider caches a prompt up to the first byte that differs from an earlier one, so a SessionStart hook that prints the date, or a "Last updated" line near the top of CLAUDE.md, made every session rebuild the cache from that point, and nothing said so. The lint reads Claude Code's CLAUDE.md files and their `@imports` (one level), the SessionStart and UserPromptSubmit hooks in its settings (their command text: hooks are never run), the hook output recent transcripts recorded, and the AGENTS.md files Codex and opencode load. Each finding names its file and line or hook, the matched token only, why it breaks the cache and the fix ("drop it from SessionStart; let the agent run `date` when it needs it"). A hook that puts something new in every session is high; a per-turn hook's output or a line a tool rewrites is medium; a date written in a file is low. Version numbers such as `go 1.26` or a semver are not flagged. A high item from your own files and hooks is also a coach finding in `tokenops glance`, the menu bar and `GET /api/findings`; the report is `tokenops_rules` (`view: lint_context`) over MCP and `GET /api/coach/lint-context` on the daemon. TokenOps' own server instructions and tool descriptions carry nothing volatile, and a test keeps it so.
- **waste, findings:** cache rebuilds say why they happened. The waste report counted "cache-miss rebuilds" as one figure, so it said what a rebuilt prompt cache cost but not what to change. Each rebuild is now classified from token counts, times and model IDs only: a *model switch* (a cache belongs to one model), an *idle gap past the cache lifetime*, measured to when the request went out so a long generation does not count as idle (an hour when the session wrote its cache with the extended lifetime, five minutes otherwise), a *prompt prefix change* after cache hits (a timestamp, the tool set, edited instructions), or *unexplained* when the turn before had no hits. `tokenops spend waste` lists each cause under the class with its tokens, dollars and window share, which add up to the class; `causes` carries them on `--json`, the MCP tools and the API. The week's finding names the top one: "Most cache rebuilds came after idle gaps over 5 minutes." A rebuild on a model switch was left out before and now counts; a compaction's new cache is by design and still does not. A change to the tool or MCP set is not told apart from other prefix changes: Claude Code's transcript records tools it appends after the prefix, not changes to the tool list the prefix starts with. Codex sessions record cached reads but no cache writes, so their misses are not counted
- **status line, glance, coach:** a warning before the prompt cache goes cold. A prompt sent after the cache expired writes the whole context to it again, and nothing said when that would happen. The status line now shows "cache cold in 1m" when under two minutes are left and "cache cold" once the cache has expired, in place of the hit ratio; the rest of the time it is unchanged. It reads the end of the transcript Claude Code hands it for when the last request went out and which lifetime the session's cache has, timestamps and token counts only. `GET /api/glance`, `GET /api/coach` and the glance MCP tool carry `prompt_cache` for the Claude Code session that ran last: when its last request went out, the lifetime, when it expires, the seconds left and `warm`, `expiring` or `cold`, until an hour after it went cold. The menu bar does not show it yet: it refreshes once a minute and has no place for one session's countdown
- **work:** `tokenops_prepare_work` shows how widely tasks of the same kind vary. It gave one range, the middle half of past runs ("typically 15–25% of your 5h window"), and runs of the same task can differ by an order of magnitude, so the range hid the tail a plan has to leave room for. `window_share.spread` now carries the median and the 90th percentile of the same measured runs with their count, and the summary reads "typically 15–25% of your 5h window (p50 20%, p90 28%, n=5)". With fewer than five runs the spread says so (`status: too_few_samples`) and names what the answer falls back to (`fallback: interquartile_estimate`, or `none`). It is read only from measured outcomes, never from an agent's own guess. Existing fields are unchanged.
- **coach, hooks:** the agent sees how much of the plan is left. An agent budgets only what it can see, and nothing put the plan's headroom in front of it. With the route-guard hook installed, the prompt hook now adds one line, "Plan headroom: 5h window 38% left (resets 14:20), week 61% left.", naming only the binding window and another within 25 points of it. It is said at a session's first prompt and again only when a window crosses 50, 25 or 10% left. It travels in `UserPromptSubmit`'s `additionalContext`, after the cached part of the conversation, never in `CLAUDE.md`, `AGENTS.md` or a session-start block, so it never invalidates the prompt cache. On by default; `tokenops coach headroom-note off` (or `coach.headroom_note: false`, or `headroom_note` on the `tokenops_coach` MCP tool) turns it off, as does the inform power being off.

### Fixed

- **hooks:** route-guard's per-session state no longer piles up. `~/.tokenops/route-guard` kept a `session-<id>.json` for every session ever run (and now a `headroom-<id>.json`), and nothing removed them, so the directory held hundreds of files for sessions long over. A file not written for a day is now removed, checked at most once an hour after the guard writes; anything else in the directory is left alone. A session resumed after a day may hear a model case or the headroom note once more.

## 0.112.1 - 2026-10-26

### Fixed

- **fireworks:** the Fireworks reader works with Nexus CLI, the new name of FireConnect. Nexus CLI 0.10.0 moved its files from `~/.fireconnect` to `~/.nexus` and no longer gives Claude Code an `apiKeyHelper`: it puts the Fireworks key in an `X-Fireworks-Api-Key` line of `ANTHROPIC_CUSTOM_HEADERS`. TokenOps looked only for the old helper and the old directory, so on a machine set up with Nexus CLI it found no key from Claude Code and no user, and a company account showed the whole account's spend, or nothing, instead of your own spend against your per-user cap. The key is now read from that header (and `nexus key export` is recognised as the helper), your user and account from `~/.nexus` before `~/.fireconnect`, and Nexus CLI's status line and settings backup are recognised like FireConnect's. Fireworks has no call that names a key's user, so where no file names yours, `tokenops vendor-usage enable fireworks --user-id <id>` sets it (`vendor_usage.fireworks.user_id`).

## 0.112.0 - 2026-10-26

### Changed

- **findings, glance, menu bar:** the week's waste leads with the share of your plan it took, not with API-price dollars. On a flat-rate plan the finding read "Last 7 days: ~$364 avoidable (9.2% of a weekly window), $0.00 realized": dollars nobody paid, beside a zero that read as something broken. It now reads "Last 7 days: ~9.2% of a weekly window avoidable", with each class as its share of the same window ("cache-miss rebuilds 6.4% · over-tiered model 2.2% · …"). Dollars lead only where a provider billed per use (an API key, pay as you go), or follow the plan's share ("…, plus $4.10 billed per use"); with neither reading, they say they are API prices. What the guardrails saved is said in words ("Guardrails already saved ~$41.20 at API prices") and left out while it is nothing. The waste report gains `billed_usd` on the total and on each class: usage stored with a cost, or without one on a provider no plan's windows cover. `tokenops glance`, the MCP tools, `GET /api/findings` and the menu bar share the finding
- **menu bar:** the coach's findings are sorted by where they belong. A warning about a plan window was listed with everything else at the bottom of the panel; it now sits under that window's bar in the plan's tab, and the tab carries a ▲. Findings that need attention or are worth a look stay open and are what the heading counts; information folds behind "N more", closed by default and remembered. Quota findings name their `provider` and `window` on `GET /api/findings` for this
- **plans, menu bar, CLI:** a Codex window that has not started yet reads "starts at your next request" instead of a countdown. Codex reports it at 0% with a reset a full week away from each reading, so the countdown ran forever ("resets in 6d 23h", then 6d 22h). A window is marked not started only when two consecutive readings show 0% with a reset a full window's length from each, and the reset moves between them; the API carries `not_started: true` and no `resets_at` or `resets_in`, and the window has no pace.

### Added

- **coach, menu bar:** what the coach did. `GET /api/coach` (and `tokenops coach --json`) carries `activity` for today and the last 7 days: subagents moved to a cheaper model, new sessions started on one, and re-reads the read guard refused, from the coach's own ledgers, with a one-line summary that is empty when it did nothing. The panel's Coach section opens with it ("Today: moved 3 subagents to a cheaper model · refused 41 re-reads", or the last week's when nothing happened today). The report also lists each preset with what it does, and the panel says that under the preset menu, which offered four names with no explanation
- **plans, glance:** a window that resets early is reported. A vendor can reset a window before the time it had given (a reset on its own side), and the glance went from mostly used to nearly full with no explanation, so it looked like a bug. TokenOps now finds the early reset from the readings it already keeps: a window used by at least 20% whose next reading shows at most 5% and a new reset, with the old reset still more than an hour away when that reading was taken. A normal reset, a usage figure that falls without a reset, a window whose length changed, and a plan switch between the readings (from the plan history, or a change of the plan the vendor reports) are not early resets. Each one is an info finding for a day, *Codex's weekly window reset 5d 12h early*, with the before and after readings and the time the reset fell between. The glance carries `early_resets` on the API and shows the finding in the CLI and the menu bar.

## 0.111.0 - 2026-10-26

### Added

- **menu bar, findings:** the coach's findings can be acted on and hidden from the panel. Every fix a finding suggested was a CLI command in its text (`tokenops boundaries promote headroom-pause`, `tokenops coach tip ack`), which cannot be run from a menu bar, and nothing could be dismissed, so the same list showed at every glance. Each finding now has a stable `key` (its kind and subject, such as `quota:openai:week`, never a figure that changes) and, where the CLI has an operation for it, `actions` from a closed list: `boundary.promote` on the headroom pause in shadow, `tip.ack`, `tip.expected` and `tip.snooze` on unusual spend and on tips that waited. The panel shows them as buttons; promote and expected first open a row inside the finding saying what will change and how to undo it, worded from the rule's own condition rather than an assumed 10%. `POST /api/findings/act` runs an action only while the finding still offers it (409 otherwise), through the same code as the CLI, and audits it; nothing on the API demotes a boundary. *Hide…* hides a finding for a day or a week (`POST /api/findings/dismiss`, kept in `~/.tokenops/findings-dismissed.json`), and it shows again early if it becomes more urgent than when it was hidden; "2 hidden · Show" brings them back. `GET /api/findings` leaves hidden findings out and counts them in `hidden`; `tokenops glance --findings` and the MCP tools still list every finding, and the CLI says how many the menu bar hides.

## 0.110.2 - 2026-10-25

### Changed

- **menu bar:** the panel is plain white in light mode and plain ink in dark mode, without the tinted gradient and the lime glow. Lime stays the accent; text and bars on white keep the dark lime that reads at AA contrast.

## 0.110.1 - 2026-10-25

### Fixed

- **sessions, daemon:** the daemon's session analysis is stored again. The context curve reports a band once a tool call ran in it, even when no instruction started there (one long instruction whose context climbs past a band edge), and that band's rejection rate divided 0 by 0. encoding/json refuses the resulting NaN, so every run since such a week logged `session analysis not stored err="json: unsupported value: NaN"` and kept nothing: the glance said "sessions read 15h ago" and kept saying it, and the coach, `tokenops dx` and move verification read an analysis that only grew older. A band with no instructions now reports a rejection rate of 0 with `prompts: 0` saying why, as the rework rate does with `total_edits`. And one figure can no longer cost the whole analysis: a figure that is not a number is stored as 0 and the daemon warns naming it (`fields=[context_curve[4].reject_rate_pct]`), instead of dropping every other figure beside it.

### Changed

- **brand, menu bar:** the accent is electric lime on ink, in place of cobalt, everywhere: the logo, app icons, favicon and link preview (redrawn by `assets/brand/make.py`), the menu bar panel, the status line and `tokenops` cards, and the docs site. Lime is the accent on dark; on light grounds the ring, links and thin strokes use a dark lime that keeps 5:1 contrast on white, and lime fills carry ink text (white on lime is 1.3:1 and is never used). The amber token and the status colours are unchanged
- **menu bar:** the tray icon is a fuel gauge. It was two rings of the busiest plan's week and session, which read as progress rather than as what is left. It is now the headroom ring with a tank that is full at 100% left and drains in eight steps with the tightest plan window; empty means used up and nothing else (any headroom left shows at least one step), and a stale or unreadable reading draws the ring with a centre dot, never a full or empty tank. Still a template image, so macOS tints it for light and dark menu bars

## 0.110.0 - 2026-10-25

### Added

- **coach, models:** new Claude Code sessions start a tier down while the plan runs short. A hook cannot change a running session's model, so a week running 28 points ahead of pace kept every new session on Opus until it ran out. With `models: autonomous` and the plan pressed (the rule below), the Stop hook now sets the `model` key of `~/.claude/settings.json` one tier down (Opus to Sonnet, never below Sonnet, never to a model the policy rules out), records the move with the pressure as its evidence, and says so with `/model sonnet` for the session already running. It removes the key once a fresh reading finds the plan back on pace, once the pressed window resets, or at the next turn end after `models` is lowered. A model you set yourself, or changed after the coach set it, is never touched; there, and at `ask` or `advise`, the coach advises `/model sonnet` once per pressed window
- **coach, models:** autonomous model moves are verified, and retracted on their own when quality drops. The coach moved subagents to a cheaper model and lowered the default model for new sessions, but nothing checked what that did to the work: a move that made subagents fail or get redone kept being made until you noticed and lowered `models` yourself. The Stop hook now records each subagent's outcome (its call failed, the agent re-ran the same work within 30 minutes, the parent then edited a file the subagent named, the turn was interrupted, or the next instruction rejected it) and each session's agent DX on its first 20 instructions, and compares moved work with like work that was not moved: same task kind, same project, the model it was moved from, within 7 days. With at least 8 runs a side (4 sessions and 20 instructions for the default model), a move kind that does 15 points or more worse at 95% confidence is retracted: the coach stops making it, and stops proposing it at `ask`, for that kind of work only; a default model it set is removed at once and the move is recorded as undone with the reason. You are told once. `tokenops explain moves` shows per move kind the moves made, the outcomes judged, the verdict and why, with the decision and the alternatives it weighed; `tokenops coach` and `tokenops_coach` show it too. A retracted move comes back when work of that kind run on the cheaper model since then holds quality, or with `tokenops coach reinstate <kind|all>`; evidence that ages out (28 days) never restores one. Subagent moves and approvals are now recorded with their Agent call's id so their outcome can be read back. Local files only; the Stop hook notes the session in under a millisecond and reads and judges at most every 10 minutes (about a quarter of a second for a 20 MB transcript)
- **glance:** while a plan runs short, the glance (`tokenops_glance`, `GET /api/glance`) carries a one-line `routing` hint telling the agent why and to pass a cheaper `model` on subagent calls for work that fits, for harnesses where TokenOps cannot rewrite the call. Absent under no pressure

### Changed

- **coach:** the coach no longer restates Claude Code's own limit message, and says how much it says. A used-up Claude window earned a tip that repeated what Claude Code had just told you, and the run-out warning could interrupt on every 5-hour window of a day; many of them were never seen. The used-up tip on Claude Code now stands down unless another bound plan has at least a quarter of its window left, and then it names that plan (the one thing Claude Code does not say). Each stand-down is recorded as `vendor_speaks` with the surface it defers to, as the router stand-down is. The run-out warning interrupts once a day on a plan whose last week of run-out warnings was mostly unseen; the rest wait on the status line. Claude's weekly pace banner was seen only in the desktop app and is not verified in Claude Code, so it stands nothing down, and TokenOps does not record the Claude Code version, so the gate is the plan being Claude's. Codex and other harnesses are unchanged, and `verbosity verbose` turns every stand-down off. `tokenops coach stats` gains *Tip volume*: tips per day by kind over 7 days and the unseen rate. On a replayed week of two run-out windows a day, interruptions went from 28 to 7
- **route guard, models:** subagent routing now reads the plan's pressure. It placed a subagent by its work alone, so implementation and debugging subagents stayed on the Opus-class tier however fast the week was running out: in a real week, with the weekly window 54% left but projected to run out about three and a half days before its reset, autopilot moved one subagent in that time. While the tightest window (5-hour or weekly) is on course to run out at least a tenth of its length before its reset (judged once a tenth of it has gone by), or is down to 25% left before its pace can be judged, edit work moves one tier further down (Opus to Sonnet) and deep work from the deep tier to the default one, for `autonomous` and `ask` alike. Lookups and research never go below what they need, nothing moves up, and the model policy still decides first. The reason, the permission prompt and the verbose message say why ("Claude weekly runs out ~4d 6h before it resets at this pace (54% left); edit work moved from claude-opus-5 to claude-sonnet-5"), and the follow-through record keeps the pressure as the move's evidence. The reading comes from local files only (the status line's limits, else the daemon's figures), so the hook stays fast; with no fresh reading, routing is as before

### Fixed

- **settings, hooks:** TokenOps no longer rewrites your `settings.json` (and `opencode.json`) in alphabetical order with `&&` turned into `\u0026\u0026`. Setting or removing the default model, the compaction window, a hook or the status line decoded the whole file into a map and wrote it back, so every key moved and every `&`, `<` and `>` in a hook command was escaped; Claude Code read it the same, but a person diffing or editing the file could not. The file is now edited in place: your key order, formatting and every value TokenOps did not touch keep their exact bytes, new keys are appended, large integers survive, and the file mode is kept.

## 0.109.1 - 2026-10-25

### Fixed

- **daemon:** a restart answers its health check within moments again, however large the store, and `tokenops daemon restart` no longer calls a normal start a failure. 0.106.1 moved the start-up corrections beside the boot, but the boot still read every stored event id (so the pollers skip history they already recorded) before it opened its port, and that read now competed with the corrections, the glance's warm-up and the other start-up readers for the store's connections. On a 317k-event store on a busy machine the port opened 45 seconds after the start, once 5½ minutes after, while `daemon restart` waits 20 seconds: it reported "Daemon restarted but did not become healthy" for a daemon that was starting normally, and nothing answered in the meantime. The daemon now binds its address first, within milliseconds of reading its config: until it serves everything, `/healthz` answers 200 with `"state": "starting"`, `/readyz` 503 `starting`, `/version` the build, the API 503 `starting` with `Retry-After` instead of waiting, and a provider request waits for the proxy (up to 30 seconds) on the same connection rather than being refused. The stored-id snapshot is read beside the boot over a read-only connection of its own, so it no longer queues behind the store's other readers; pollers that replay history wait for it, so nothing already stored is re-published. Reading the ids also allocates a third less (about 130 ms of CPU for 320k ids). `daemon restart` and `daemon install` say "warming up" when the daemon answered while still starting

- **boundaries:** a plan-headroom reason now says whose plan it read: "Claude plan left 6.0% <= 10% (5h)" instead of "plan left 6.0% <= 10% (5h)". With a Claude plan and a Codex plan side by side, the old wording left the operator to guess which one had run low, and a Codex weekly window that was used up looked like the reason a Claude session would pause. A rule read against the tightest window across every plan keeps the plain wording.

## 0.109.0 - 2026-10-24

### Added

- **menu bar, boundaries:** the menu bar approves a boundary in place, with the presence dialog (ADR 0015 §3). *Approve…* opened a Terminal window running `tokenops approve <id> --confirm`, so approving from the panel meant a second window, a typed yes and then the Touch ID dialog. With a presence key enrolled, **Approve** now shows the dialog straight from the panel and answers with the signed approval; the panel's note says what happened: approved and which key signed, or why nothing was answered (the dialog was cancelled, Touch ID or the password did not match, the screen is locked, nobody could be asked, the key is from another Mac). The daemon builds what is signed, never the app: the new `POST /api/boundaries/statements` (and `POST /api/routing/statements` for a model upgrade) returns the exact statement bytes, the dialog's text naming the rule, action and session, and the unsigned proof, which the app posts back to `POST /api/boundaries/decisions` with only the helper's signature added. Both routes are behind the API token; a machine where no key can sign answers `409 presence_unavailable`. With no key enrolled, a held key, no helper inside `TokenOps.app` or an older daemon, *Approve…* opens the terminal as before. Deny stays one click and never signs. `tokenops menubar --refresh` (the Homebrew upgrade hook) stopped every process under `TokenOps.app/Contents/MacOS/`, which now includes the presence helper: an upgrade during an approval would have killed the dialog. It now stops only the app's own binary
- **boundaries:** presence-checked approval, opt-in (ADR 0015, phase 4). A process running as you could approve a boundary in your place: `script -q /dev/null tokenops approve <id>` gave the command the terminal it checks for, and an answer file written by hand was honoured, because approval rested on the channel an answer arrived on. On a Mac with a Secure Enclave, `tokenops boundaries presence enroll` now creates a key that signs only after Touch ID, a paired Apple Watch or your login password (one dialog to enrol), and `tokenops approve` and `tokenops routing decide <key> approve` show that dialog and sign the approval; a deny never asks. `tokenops boundaries presence enforce` sets `boundary_approval: presence` in config.yaml: an approval then counts only when signed with the key or given at Claude Code's own prompt (labelled harness-attested), and `presence-strict` drops the prompt too, so the pause point refuses there with the `tokenops approve` command instead of asking. With it on, an unsigned approval at the terminal is refused with the reason; a hand-written answer is not honoured, is removed and recorded on the decision as refused (`unsigned_answer_ignored`), and the agent's refusal says why; `POST /api/boundaries/decisions` and `POST /api/routing/decisions` take an approval only with a valid `presence_proof`, the router honours a model upgrade approval only when its signature verifies, and the MCP tools can no longer approve a model upgrade. A key replaced without the old key's signature holds approvals for 15 minutes and is announced; `tokenops boundaries presence rotate` moves to a new key with the old one's signature and holds nothing. With the setting on and no key enrolled, approvals are refused with the enrol command and deny still works. The dialog names the rule, the action and the session, because a paired Watch shows that text and can approve while you reach for Cancel; every signed approval is recorded with the key that signed it (`tokenops explain <id>`: "approved in person: signed with presence key pk-…"), so one you did not intend is visible. Over SSH, from a launchd job, in CI or with the screen locked, the helper refuses at once rather than waiting. Without the setting nothing changes: approvals work as before and, with a key enrolled, are signed and checked in shadow. `tokenops boundaries status`, `GET /api/boundaries`, `tokenops_boundaries` and the glance report the setting, approvals that were not honoured, and enforcement with no key. The helper, `tokenops-presence` (Swift, ad hoc signed), ships in the macOS archives beside `tokenops` and inside `TokenOps.app`; the CLI and daemon stay pure Go, and the Linux archives are unchanged. The menu bar approves inline, below
- **boundaries:** the checking half of presence-checked approval (ADR 0015, phase 3), in shadow. Approval rests on the channel an answer arrives on, and #702 documented that a process running as you can pass that check (`script -q /dev/null tokenops approve <id>`, or writing the answer file). ADR 0015 closes it with an approval signed by a Secure Enclave key that needs Touch ID or your password for every signature. This release adds the pure-Go verifier: a signed statement names the decision, rule, session, scope, a fresh challenge for that raising of the decision, a hash of the crossings you were shown, and when it was signed (`tokenops.approval/v1`, length-prefixed fields); a one-step approval is honoured for 10 minutes and spent once used, a session approval for 24 hours; a key replaced without the old key's signature holds approvals for 15 minutes and is announced, a rotation the old key signed does not. Nothing is refused and nothing enrols a key yet. Each approval the pause point honours is recorded with what the check would have said (`presence`: `signed`, `harness` for Claude Code's prompt, `team`, `unverified` with `would_refuse` and the reason, or `not_enrolled`), the decision's rationale says when enforcement would not have honoured it, and `tokenops boundaries status` (`GET /api/boundaries`, `tokenops_boundaries`) reports the machine's presence state: on every machine today, *presence approval not enrolled*, once rather than on every approval. With a key enrolled, the glance's findings count approvals that would not have been honoured and announce a replaced key. Pending decisions now carry their challenge; `DecisionEvent.approval` gains `presence`, `presence_key` and `would_refuse` (schema 1.6.1). The pause point's 5 ms budget gate now also covers a signed answer (about 0.3 ms)

### Changed

- **plans, coach, budgets:** plan windows and budgets now read one way everywhere: what is left, counting down to "used up" like a fuel gauge. The glance, the status line, the cards and the menu bar already said "% left", but other places counted up, so the same window read 30% in one place and 70% in another: `tokenops plan` ("window: 70% of the week left", was "30% of the week used"), `tokenops coach` ("Claude weekly 55% left", was a bare "45%"), the coach's run-out warning ("(40% left)", was "(60% used)"), the status line's quota tip ("Claude weekly 25% left → /compact", was "75% used"), the session budget tip, provider setup, the usage meter check and the MCP session budget (whose gauge now fills to what is left). Spend budgets lead with money: "budget team (daily): $1.80 of $10.00 left, 82% spent", on the coach's tip and the glance; a boundary on a budget gives what is left beside its own rule ("budget monthly 8.0% left (92.0% spent >= 90%)"), and the menu bar's budget meter says "$8.20 of $10.00 spent". The headroom pause is worded by its rule, `plan_left_pct <= 10`: "once the tightest plan window is down to 10% left". Only the wording changed: config keys, stored figures and the JSON, API and MCP fields (`used_pct` and the rest) are as they were

### Fixed

- **proxy:** a streaming reply to a request with a body is no longer cut after its first event. When the proxy forwarded the body itself (observer, cache and routing off, or a body over the 4 MiB capture limit), it read the body once more after its last byte to confirm the end, from another goroutine, while Go's HTTP server closes the request body as soon as the reply's first bytes go out. When the provider answered before that last read, the read failed and the proxy closed its connection to the provider: the client got one event and then `unexpected EOF`, and the daemon logged `ReverseProxy read error during body copy: ... use of closed network connection`. It happened under load. Once a request body has reached its end, the proxy now answers further reads with the end itself instead of reading the closed body

## 0.108.0 - 2026-10-24

### Added

- **boundaries:** the headroom pause is on by default. A new `tokenops init` writes a `headroom-pause` boundary: once the tightest plan window TokenOps knows is 90% used (`plan_left_pct: "<= 10"`, the 5-hour or weekly window, whichever has less left), the agent's next tool call asks first. It starts in shadow, so it asks nothing and records each session it would have paused, and init installs the pause point (the boundary hook) in Claude Code and opencode so there is something to record; before, a fresh install had no boundary and no pause point, and the pause that shipped in 0.103 did nothing until you wrote a rule yourself. `tokenops boundaries promote headroom-pause` makes it pause
- **boundaries:** the glance's findings (`tokenops glance`, `GET /api/findings`, `tokenops_findings`, the menu bar) say what the headroom pause did: "would have paused 3 sessions this week" in shadow, "paused 2 sessions this week" once promoted, and that it sees no tool calls when no agent has the pause point
- **boundaries:** `tokenops boundaries headroom-pause` adds the pause, in shadow, to a config written before init did, and installs the pause point. An existing config is never changed on its own: re-running init leaves it alone, and the findings suggest the command only when a plan is watched and no `continue` rule on `plan_left_pct` is set

### Changed

- **docs, README:** the landing page's comparison no longer says the vendors' own tools never act and offer no limits, or that only meters show plan windows: Claude Code acts on its own harness, and Claude, OpenAI and GitHub give admins limits on their own seats. The table is dated (checked 2026-10-22) and leads with what no single vendor covers: one set of headroom, budget and session-cost rules across every plan, account and harness, which account paid through a router, agent DX graded across harnesses, and a core that needs no upload at all

### Fixed

- **boundaries:** a call Claude Code stopped itself no longer counts as your approval. When a boundary asked at Claude Code's prompt, TokenOps read the asked call's result back on the next call and took anything but "The user doesn't want to proceed" as a yes. A call stopped by Claude Code's auto mode classifier, a permission rule, a plugin or a PreToolUse hook never reached the prompt, but it still counted: TokenOps recorded you as having approved at the prompt and stopped asking for the rest of the session. The same misreading counted a subagent model move as followed when Claude Code had refused the Agent call. Those stops are now read from Claude Code's own wording (checked against 2.1.307). TokenOps records Claude Code's verdict on the boundary's decision as evidence (`vendor_verdict`: the session and call, when the result was written, what stopped it) and asks at the next call. That is the first time you are asked, not the second. A refused subagent move is dropped unscored, and a person declining at a subagent's prompt now counts as a decline. A hook that refuses with its own free-text reason still reads as a call that ran and failed, because its wording cannot be told apart from one
- **MCP:** the `tokenops` command a settings confirmation shows is now exactly the change the tool applies. `tokenops_configure` (`plan` with a spend limit, window, rate factor, price, currency or `since`), `tokenops_routing set_rule` (with `quality` or `fallbacks`) and a `tokenops_coach` tip answer with `tip_id` or `snooze_minutes` showed the base command followed by "with flags for the other values (see --help)", so you approved, or were asked to run, a command that did not say what would change. `usage_meter` left out `--org`. They now show every flag (`tokenops plan set anthropic claude-enterprise --spend-limit 5000 --limit-window weekly`, `tokenops routing rule set … --quality 0.9 --fallback …`, `tokenops coach tip snooze --id … --for 30m`). A value that starts with a dash, such as a model named `-foo`, would have been read as a flag if you pasted the command; such a command now puts its flags first and `--` before the positional values, and a flag value that starts with a dash is written `--flag=value`. A routing rule the tool would refuse (no target, quality outside (0,1]) is now refused before you are asked. A test pastes every shown command into bash and parses it with tokenops' own command tree, checking that it reaches the same subcommand with the same values, including values with spaces, quotes, `$` and backticks

## 0.107.0 - 2026-10-22

### Added

- **routing:** `tokenops routing decide <key> approve|deny` answers a routing proposal from the terminal, as the menu bar and `POST /api/routing/decisions` do; it is the command a refused `tokenops_routing decide` names

### Security

- **MCP:** the settings tools ask you before they change anything. `tokenops_configure`, `tokenops_routing` (`decide`, `set_rule`), `tokenops_coach` and `tokenops_experiment` carried only a destructive hint, so the client's permission prompt was the only guard, and an operator who allowlisted `mcp__tokenops__*` had none: text the agent read, such as a quoted transcript, a web page or a file, could have it switch TokenOps to active mode, add a routing rule or approve a model upgrade. Every call that changes a setting now sends an MCP elicitation that shows you the exact change, with the agent's values quoted and the equivalent `tokenops` command, and applies it only when you tick *Apply this change* and accept. Declining, dismissing or accepting unticked changes nothing. A client that cannot ask is refused with the command to run yourself (`tokenops mode active`, `tokenops routing rule set …`, `tokenops routing decide <key> approve`). Over stdio a client must speak the stateless `2026-07-28` protocol revision for TokenOps to ask, because the MCP library's stdio transport cannot yet send a request from the server; older clients, which may include yours, are refused for every change until it can. Reading through these tools (listing plans, reading the mode, routing advice, proposals, an experiment's status, the coach's report) asks no one. There is no switch to turn the question off. A route added to a settings tool without a confirmation or a stated reason fails at registration and in a test that enumerates every route
- **proxy:** the TokenOps correlation headers no longer reach the provider. The proxy read `X-Tokenops-Workflow-Id`, `-Agent-Id`, `-Session-Id` and `-User-Id` (which can be an email address) and the `X-Tokenops-Cache` directive, but removed only `X-Tokenops-Execution-Id`, and only while the observer ran; everything else was forwarded to Anthropic, OpenAI and the other providers, and with the observer off the execution id was too. `tokenops anthropic-bridge` said they were stripped. The proxy now removes every `X-Tokenops-*` header from each request it forwards, on every provider route and whatever middleware is on, and a test checks what a real upstream receives with the observer on and off and through the bridge
- **proxy:** the response cache keys each reply to the credential that fetched it. The key was the provider, method, path and body only, so once the cache is switched on a request with someone else's API key, or with none, could have been answered with a reply fetched under another key. The key now also covers the query string and a hash of every credential the providers accept (`Authorization`, `x-api-key`, `x-goog-api-key`, `api-key`, and OpenAI's organization and project headers); the credentials themselves are never stored. The cache is not yet enabled in shipped builds
- **proxy:** a client that opens a request and then sends its body slowly, or not at all, is cut off after 60 seconds, and idle keep-alive connections close after two minutes. The proxy bounded only the time to send the request headers, so a stalled body held a connection and its handler open indefinitely. Streaming replies are unaffected: the limit covers receiving the request, there is still no limit on writing the reply, and a test streams past the limit with and without a request body
- **daemon:** the dashboard token's directory is created owner-only (`0700`) instead of readable by every user on the machine, and an existing token file is set back to `0600` at every start, so a token left readable by an older build or a restore from backup does not stay that way. A persisted token shorter than 32 characters is replaced with a fresh one rather than trusted; clients pick the new one up from the address file the daemon rewrites at start. A `dashboard.admin_token` (or `TOKENOPS_DASHBOARD_ADMIN_TOKEN`) shorter than 32 characters is now refused when the configuration loads, with an error naming the setting and the minimum; leave it unset and the daemon mints one
- **storage:** a new event store is owner-only from the moment it exists. The database, its WAL and its shared-memory file were created with the default permissions (readable by every user on a typical machine) and narrowed to `0600` only after the schema had been written, so other users could open the usage history in between. The file is now created empty at `0600` before SQLite opens it, and an existing store is narrowed before, not after, so the WAL and shared-memory files SQLite creates inherit the restricted mode
- **coach:** `tokenops coach prompts` no longer prints your prompts back unredacted. Its examples (vague and rejecting prompts, repeated prompts, a recommendation's evidence) were your typed text verbatim and uncut, in the text report and in `--json`, so a key or token pasted into a prompt was echoed to the terminal or to whatever read the JSON. Every example now passes through TokenOps' secret redactor (`<redacted:anthropic_api_key>`), is folded onto one line and cut to 120 characters, and that holds for the MCP tool's `quote: true` too
- **vendor usage:** an upstream's error answer no longer carries a credential into TokenOps' errors. The Copilot, Cursor, claude.ai, Anthropic Admin and Claude Code readers quoted up to 4 KB of a failed answer in the error that `GET /api/sources`, the MCP `data_sources` view and the daemon log show, so a vendor echoing the request back (an Authorization header, a cookie) would pass it on. A refusal (401, 403, 407) is now reported by its status alone, and any other answer by at most 200 bytes, redacted and on one line. The gateway readers no longer put a URL's query string into an error, including the transport error that repeats the URL
- **vendor usage:** `tokenops vendor-usage enable` reads a secret from stdin: `--session-key -`, `--cookie -`, `--admin-key -` and `--oauth-token -` prompt without echo on a terminal, or read one line from a pipe (`pbpaste | tokenops vendor-usage enable cursor --cookie - --user-id …`). The env vars work as before

### Changed

- **coach:** `tokenops coach prompts` quotes no prompt text unless you pass `--quote`, the same opt-in the MCP tool has; the counts and recommendations are unchanged, and the report says how to see the examples
- **vendor usage:** a secret typed on the command line (`--cookie <value>` and the other secret flags) still works but prints a deprecation warning: every user on the machine can see it in `ps` output and it stays in shell history. The help's examples use stdin and env vars instead
- **boundaries:** the docs and `tokenops approve --help` say what approval guarantees and what it does not. It guarantees that an agent cannot approve through TokenOps' MCP tools or the daemon's decision endpoint, which can only deny, that `tokenops approve` refuses a stdin that is not a terminal, and that every answer is recorded with how it was given. It does not guarantee the approval came from you: a process running as your user can get a pseudo-terminal (`script -q /dev/null tokenops approve <id>`), read the daemon's token and call its API (`POST /api/mode`, `POST /api/routing/rules`), or write the boundary ledger. The guide recommends harness permission rules that ask before `tokenops approve`, `tokenops mode`, `tokenops routing`, `script` and `curl` to the daemon. TokenOps does not try to detect a "real" terminal: nothing it can read (a controlling terminal, its session, the parent processes) tells a pseudo-terminal an agent opened from yours

### Fixed

- **MCP:** `tokenops_experiment` can start a trial. The public tool listed `provider`, `variant_model`, `objective_metric` and `reason` but did not pass them on, so every `start` was refused ("objective_metric, provider, reason, variant_model does not apply to tokenops_experiment") and a `stop` with a reason was refused the same way. All four now reach the trial, `reason` is recorded on a start as it already was on a stop (`tokenops experiment start` takes `--reason` too), and the confirmation shows the trial's provider, both models, its objective and guardrails, with the exact command that starts the same trial (`tokenops experiment start anthropic claude-opus-4 claude-sonnet-4 --pairs 6 --days 7 --objective plan_quota_tokens --min-improvement-pct 12.5 --guardrail quality --guardrail latency_ms:20`) instead of a placeholder. A stop's command names the reason it records
- **MCP:** `tokenops_coach` takes `holdout` (`off`, `on` or a percent up to 10), which its schema listed but the tool refused; the confirmation names `tokenops coach holdout <value>`
- **MCP:** `tokenops_configure setting=budget` takes `provider`, `budget_mode` (soft or hard), `require`, `add_window` and `remove_window`. The budget tool accepted them, but the public tool refused them, so a budget scoped to one provider's bill or with several windows could be set only from a terminal. The confirmation now shows the exact `tokenops budget set …` or `tokenops budget unset <name> --window …` command rather than "with flags for the other values". A test now compares every public route with the tool behind it and fails when an argument that tool takes is neither passed on nor listed as deliberately left out

## 0.106.1 - 2026-10-17

### Fixed

- **daemon:** a restart answers within a few seconds even on a busy machine. The corrections a start applies to recorded usage (moving gateway and custom-provider turns to the provider that bills them, re-marking plan coverage) ran before the daemon opened its port, and on a large store under load they took most of the restart's wait. They now run beside the start, under the daemon's supervisor, and the log says when they are done; live ingestion records new usage correctly on its own in the meantime, and a correction cut short by shutdown runs again at the next start

## 0.106.0 - 2026-10-17

### Added

- **team:** a team server that accepts them now receives, beside the work figures, the plan and waste figures a team lead asks for: per day and plan, how full the plan's windows got, how much of the usage it covered and how often it ran out; per day, repository and kind of work, the avoidable usage in each of the six waste classes of `tokenops spend waste`; per day, what the guardrails saved beside what stayed avoidable; and per provider, the plan advice's verdict and recommended tier with its monthly difference and confidence. They are the figures `tokenops scorecard`, `spend waste`, `coach stats` and `plan advise` already show, computed by the same code. Every name in them is from TokenOps' own catalogs (providers, plans, waste classes, verdicts); the advice's wording, model names, sessions and your allocation tags stay on the machine. The server says which upload format it reads (`max_schema` on its member page, asked once a day); one that does not say gets exactly the upload it got before, byte for byte. `tokenops team status` says which the server reads, and `tokenops team preview` shows the new sections either way, marked "Withheld" while the server does not accept them yet. ADR 0012 is amended with the new fields (#685)
- **sessions:** `tokenops story --runtime` says where each task's time went, as far as the transcripts record it, and no further. Each task gets a status (completed when the harness recorded its last turn ending, open while the session may still be running, last observed otherwise) and its interval split into model requests, tool execution, approval waiting, idle and unattributed time, each with where the figure comes from and how much of the task it covers. Every instant is counted once, so the parts add up to the interval and overlapping tools are not counted twice; tool time is given both as the critical path the task waited on and as the summed time the tools ran. What a client does not record is unknown, never zero: Codex, Cursor and opencode write no model-request boundaries, Cursor and opencode no tool results, no client records a permission prompt, and time to first token appears only where Codex records it. A task nobody answered, or one still running, is no runtime sample, and every summary states its denominators, with nearest-rank median and p90 per harness, model and role. The friction count is labelled a heuristic and nothing claims a task succeeded. `--runtime --json` is the derived-only export (no instruction text, paths or session ids), the same as `tokenops_sessions` `view: runtime` and `GET /api/story/runtime`. `tokenops dx` and `story` take `--model` and `--role` (`primary`, `auto_review`, `subagent`), and so do the MCP views and the API (`model`, `role`, and now `source`), so your own instructions can be measured apart from the automatic reviews Codex runs, which can be most of a week's instructions and then set its median. `dx` says when the window pooled them (#689)

### Fixed

- **sessions:** concurrent sessions no longer lend each other turns. Instructions were grouped in one time order across every session, so a turn or tool call of one session counted toward whichever session's instruction was typed last; they are now grouped per session, which changes `dx`, `story` and `verify` figures wherever two sessions overlapped (#689)
- **sessions:** Codex tool calls made through its own tools (`apply_patch` and others written as `custom_tool_call`) are counted; reading only `function_call` missed most of a session's calls (#689)

- **boundaries:** the pause point before every tool call takes about a fifth of the time when a rule matches (0.3 ms instead of 1.4 ms, and no longer several milliseconds when the disk is busy). It counted each match by rewriting the session's counter file; it now appends one line to a match log beside it and rewrites the file only when a decision stage is recorded. Parallel tool calls in one session no longer drop each other's counts, a long session's log is folded into its totals past 64 KiB, and counts recorded before are kept (#686)
- **daemon:** a restart answers again in seconds instead of close to a minute. Before opening its port the daemon lists the models Claude Code turns were recorded under, and that lookup read every prompt event's full row; on a store of a few hundred thousand events it took most of a minute, longer than `tokenops daemon restart` waits, so a restart reported the daemon unhealthy although it came up shortly after. The lookup now reads only the usage index (13 s to 0.6 s on such a store), and a test pins its query plan
- **MCP:** `tokenops_sessions` now keeps its word. It said "derived figures only; quoted instructions are withheld", but its `story` view titled each task with the instruction you typed, listed the absolute paths of the files it edited and described each friction in a sentence, and its `prompts` view quoted your instructions. By default both views now answer as the daemon API does: no instruction text, `files_edited` (a count) instead of paths, frictions by kind (`interrupted`, `rejected`, `rework`, `delegated`), and `titles_withheld: true` with `paths_withheld: true`. Pass `quote: true` to get the narrative back; the parameter says before the call that it returns your instruction text and local file paths, and `dx` refuses it. `GET /api/story` no longer names the edited files either. `tokenops story` and `tokenops coach prompts` on the command line are unchanged (#691)
- **status:** `tokenops status` no longer calls a healthy daemon "not running" because this client could not connect to it. Inside a coding agent's sandbox the connection is refused by the OS with "operation not permitted" while the daemon runs fine, and status said `not_running` and advised a restart. It now says why the connection failed (`reachability.class`: `permission_denied`, `refused`, `timeout` or `discovery`, with the failing call and error but no URL or config) and what to do first: for a permission denial, check from an unrestricted shell or allow the client loopback access, restarting only if it is unreachable there too; for a timeout, `tokenops daemon status` and the log; for a missing or wrong address, where the address comes from. Only a refused connection still reports `state: not_running` with the restart advice; every other failure reports `state: unknown` and `daemon_health: unknown`, beside whether a launchd/systemd unit file is installed (`supervisor`, read from the unit file, not the job). `status: daemon_unreachable` and `ready: false` are unchanged, and a reachable daemon's output gains `reachability`. The MCP server's `tokenops_status` says nothing is writing to the event store only after a refused connection; otherwise it says it could not reach the daemon and why, with `daemon_reachability` beside the warnings, and its warnings about quiet readers are unchanged. A daemon that answers `/healthz` with an error status is reported as running but not healthy, with the status (#690)

## 0.105.0 - 2026-10-15

### Added

- **MCP:** `tokenops_prepare_work` now says how much of the plan window a task like this usually takes: "typically 15–25% of your 5h window; 38% left, resets in 1h10". The range comes from earlier tasks of the same kind (lookup, research, edit, deep) in the same repository (pass `repository`), or of the same kind anywhere when that repository has too few, and it stays hidden until five have been measured. When only the low end fits in what is left it suggests a lighter model tier, and when not even that fits it suggests waiting for the reset; it never changes the model. `tokenops_review_work` then records the share the task actually took against the estimate, which feeds the next one. Only the task kind, the repository label and percentages are stored, never the instruction
- **budgets:** a budget can count one provider's bill: `tokenops budget set mine --provider fireworks --limit-usd 50`. It counts what that provider billed, whatever model answered, so a request Claude Code sends through a gateway counts for the gateway. It is for an account you share with others where you want a limit of your own, whether or not the provider sets a cap per member, and it is independent of any such cap. It works with several windows, soft and hard (a hard window is a spend boundary that starts in shadow), and with `--basis spend` (default), `equivalent` or `tokens`. Scopes narrow together: with `--tag client=acme` the budget counts that client's work the provider billed. An unknown provider is refused with the ids it was probably meant to be ("unknown provider "fireworx"; did you mean fireworks?"). `POST /api/budgets` and `tokenops_budget_set` take `provider` (and the API now takes `tag` too); `budget list`, the glance, the coach's budget tips and the menu bar's tab for that provider say whose bill it counts. Existing budgets are unchanged (#682)
- **spend:** `tokenops spend waste` puts one price on waste. It sorts the period's avoidable usage into six classes, each read from something TokenOps already keeps: redundant re-reads (the read guard's ledger), cache-miss rebuilds (a turn that wrote most of its cached context again), over-tiered model (turns left on a model after the coach advised a cheaper one), retry loops and runaway growth, oversized context (both from the waste detector), and abandoned sessions (no commit and no outcome, by the joins of `tokenops spend units`). Each token is counted in at most one class. Every class and the total are stated in API-equivalent dollars and, on a flat-rate plan, as a share of the plan's window ("avoidable 873k tokens $1.38 · 3.8% of the anthropic weekly window"). Beside the total is what the coach's interventions realized over the same period, and the guardrails ranked by their graduation evidence, with the next one to switch on. Nothing is switched on for you. The daemon computes the week once a day, and the glance, the menu bar and `GET /api/findings` show it as one line ("Last 7 days: ~$1.38 avoidable (3.8% of a weekly window), $0.00 realized"). The daemon serves the report at `GET /api/spend/waste`, and agents read it with `tokenops_spend` `view: waste`. Derived figures only (#655)
- **scorecard:** every KPI is set against your own trailing baseline, the 28 days before the period (the window unit costs already use), with whether it moved better or worse given which way the KPI is healthy: "CHR — Cache Hit Ratio (%): 97.5 [A] · baseline 98.5 (-1.0, worse)". The agent KPIs (CGR, RGR, TCS, DAR) are compared from the same transcripts; plan utilization and coverage are compared without a verdict (up or down); unit costs say better when cheaper. A move under a percentage point is unchanged, and a KPI you override or that was not measured in both windows has no comparison. `--json`, `GET /api/scorecard` and `tokenops_scorecard` carry `self_baseline` and, per KPI, `polarity` and `vs_baseline`. It is you against yourself only: there is no team median, since the team plane returns no such figure to the client, and nothing is ranked. The graded scorecard no longer prints "Baseline: (no baseline captured)"; a `--baseline-ref` label shows as "Baseline ref" (#663)
- **spend:** allocation rules file your work under a project, a client and a cost centre. Rules in a new `allocation` config section match a repository (`acme/*`), a directory and everything under it (`~/clients/acme`), a branch prefix or the harness; for each tag the first matching rule that sets it wins, and editing a rule re-files past work. `tokenops spend --by project` (or `client`, `cost_center`) shows the spend per value with the untagged work kept apart; the daemon serves it at `GET /api/spend/tags` and agents read it with `tokenops_spend` `view: tags`. `tokenops budget set acme --tag client=acme` scopes a budget to one tag (threshold alerts, no forecast). The FOCUS export splits each charge by tag and fills the `Tags` column (`{"client":"Acme Corp"}`), and the scorecard shows how much spend the rules tag under SAC. The rules run on this machine and the tags never leave it: the team upload has no field for them (#658)
- **plans:** `tokenops plan advise` says which tier would have fit the last 30 days (`--days 60`, up to 90). It replays your plan's windows against the plan's other tiers (Claude Pro, Max 5x, Max 20x), each tier with its overflow billed at API list price, and no subscription at all, and shows for each how often you would have run out and for how long, the fullest window, and the monthly cost: "Claude Max 5x would have cost $100/mo less than Claude Max 20x, with no lockouts; on Claude Max 20x you peaked at 20% of the weekly window." Readings from before a plan switch are scaled by the plan in force then. Claude's tiers are Anthropic's multiples of Pro's allowance and Pro's own allowance is unverified, so every answer carries a confidence label (at best medium) and says so. It is advice: TokenOps never switches a plan. When a switch would save at least $10 and a tenth of the bill with at most one lockout a month, the glance, the menu bar and `/api/findings` say so once. The daemon serves it at `GET /api/plans/advice`, agents at `tokenops_glance (view=plan_advice)` (#653)
- **spend:** unusual spend is spotted and its likely cause named. Every hour the daemon checks today and yesterday against a forecast from your four weeks before and flags a day whose spend, or usage at API prices, is above the forecast's 95% band by at least $5 and 25%, once there are 10 days of history. The finding ranks what drove the rise by model, biller, billing basis, repository, tool and session, and names a pricier model that took over, usage billed per token while a plan should cover it, or one session carrying the rise like a runaway loop (with what the waste detector found in it): "Spend on Wed 14 Oct was $24.00, $24.00 over the usual $0.00 · Likely cause: Claude usage was billed per token while its plan should cover it, through anthropic-api". It shows on glance, the menu bar and `GET /api/findings`, never in a session, and is answered like a tip: `tokenops coach tip expected --id anomaly-…` stops flagging the same pattern, `ack` and `snooze` work too. Derived figures only (#651)
- **budgets:** a budget can hold several windows at once, say $10 a day and $100 a month: `tokenops budget set team --add-window --window daily --limit-usd 10`. Each window is soft or hard. A soft window (the default) is told to you by the coach as it fills, at its warning and critical thresholds and when it is spent, once per window and through the same tips you can snooze or answer, within the daily interruption budget ("budget team (daily) is 82% used — $8.20 of $10.00, resets in 11h 40m"). A hard window (`--mode hard`, `--require ask|deny`) is enforced by a spend boundary that asks, or refuses, before further steps once the window is spent; like every boundary it starts in shadow and binds after `tokenops boundaries promote budget-team-daily`. `tokenops budget list` and the glance (`spend_budgets` on `/api/glance` and `tokenops_glance`) show each window's share used and mode; `tokenops budget unset team --window daily` removes one. The API and MCP tool take the same fields but change a budget that has a hard window only from a terminal, so an agent cannot loosen the limit set on it. Budgets with one window read and write as before (#661)
- **coach:** graduation criteria. `tokenops coach` shows, for the read guard, the compact tip, the session budget and model routing, which stage each is on (shadow, advise, act), the bar for the next one and where your evidence stands: at least 50 events, at least 90% right or acted on, and a saving above zero (measured by the holdout for tips, from the re-reads it watched for the read guard). When a step is ready, `tokenops glance` says so ("Read guard is ready to enforce") with the command to take it; nothing is promoted automatically. While the coach only observes, the budget tips it would have given are recorded as shadow calls, the evidence for letting it speak. The `tokenops_coach` MCP tool returns the same as `graduation` (#662)
- **coach:** a realized-savings ledger. Every tip shown, re-read refused and model move suggested writes a row with what it was expected to save, the figures it rested on and its decision ID (`~/.tokenops/coach/savings.jsonl`, figures only). To measure what a tip actually saves, the coach now holds the 50% and 75% session-budget tips and the compact tip back from about 10% of sessions, logged as shadow; tips about work about to stop (a plan window running out or used up, the session budget spent) are never held. `tokenops verify` and `tokenops coach stats` compare the two groups on the spend after the tip, compactions and commits that landed, and report the realized saving per tip once each side has 5 sessions ("compact tips: 41 shown, 5 held back · expected ~$0.31 per later turn · realized $3.80 per tip"). `tokenops coach holdout off` opts out (or `on`, or a percent up to 10); a preset keeps your choice (#656)
- **verify:** with several experiments in the window and no `--experiment-id`, `tokenops verify` no longer stops at "multiple experiments are present". It compares the only experiment that can be compared, or the most recent of several, and says which and what else was there; when none can be compared it says why for each
- **spend:** `tokenops spend units` says what finished work cost: per commit, per merged pull request, per recorded and per achieved outcome, and per commit of each change class (feature, bugfix, refactor, test, docs, chore, read from Conventional Commits subjects), each against your own four weeks before ("merged PR $13.50 · baseline $24.00 · -44%"). Merged pull requests come from git alone (squash `(#N)` and `Merge pull request #N` commits on the default branch); no forge is asked. On a flat-rate plan each pull request and outcome is also stated as a share of the plan's weekly window, estimated from your own windows. The work no unit took stays listed by why. `tokenops scorecard` shows the cost per commit, merged PR and achieved outcome under plan use; the daemon serves the report at `GET /api/spend/units` and agents read it with `tokenops_spend` `view: units`. Derived figures only; they are not uploaded to the team plane (#657)
- **plans:** two figures per provider answer "is this seat used?": **plan utilization**, the average peak of the plan's longest window (usually the weekly one) across the windows that reset in the period, and **plan coverage**, the share of that provider's tokens a plan covered rather than an API key, a gateway or overflow billed per token. `tokenops scorecard` lists them under the grades for its window ("anthropic utilization 64% (weekly, 1 window) · coverage 93% of tokens on a plan"). The glance cards, the menu bar and `/api/glance` (`plan_use`) show them for the last 7 days. They are reported, not graded, and a figure that cannot be measured yet is left out rather than shown as 0
- **spend:** `tokenops spend export --format focus` writes your spend and usage as FOCUS 1.2 CSV, the billing format FinOps tools such as Vantage, CloudZero and Cloudability ingest. Plan fees are purchases; usage a plan covered is billed 0 with its API price as the list cost, so the tool sees what each plan was worth; usage billed per token costs what it cost. The daemon serves the same file at `GET /api/spend/export`. Only derived figures go in it: providers, models, plans, counts and money, never session titles, paths or prompts (#659)
- **coach:** you can answer a tip. `tokenops coach tip ack` (seen it), `expected` (normal for me) or `snooze --for 2h` (not now) answers the newest one, and the `tokenops_coach` MCP tool takes the same answer. An acknowledged or expected tip closes its episode, so nothing more of that kind is said in the session. A snooze holds that kind of tip in every session until it ends; a warning that a plan window will run out, or is used up, still speaks. Once most of your answered budget tips are "expected", the session budget is read off your p95 session instead of your p90. `tokenops coach` shows the answers per kind of tip with the share snoozed, and glance flags a kind you snooze most of the time
- **coach:** a daily interruption budget, `coaching.quiet.max_per_day` (default 6, `-1` for none). Past it, tips do not interrupt the session; each waits on the status line as the session's open tip, and glance says how many waited. Tips about work about to stop still speak

### Changed

- **coach:** quota tips on a flat-rate plan now warn about running out, not about how much is used. The coach measures each plan window's pace over the last hour and over the last 15 minutes and warns only when both would use the window up before it resets: "at this pace your 5-hour Claude limit runs out in ~1h 20m, 40m before it resets (60% used)". The tips at 50%, 75% and 90% are gone: 90% used ten minutes before the reset lasts and is not worth a word, while 50% half an hour into a five-hour window is. A burst that has stopped, a short spike in an otherwise slow hour, and a meter reading more than 15 minutes old say nothing. Each window gets one warning, and the used-up note stays. The status line shows it short ("Claude 5-hour out in ~1h20m → smaller model"), and `tokenops coach` says what the forecast is for each plan. Once a window resets, the coach records whether it was forecast to run out and whether it did, and `tokenops coach stats` shows the share of forecasts that came true
- **coach:** past the session budget the coach speaks once. Each further step over it (200%, 300%, …) updates the status line instead of interrupting again
- **coach:** the session budget the coach's tips measure against is now read off your own sessions instead of a flat $50. Unless you set a budget, it is the p90 of what your sessions cost (API-equivalent) over the last 30 days, once at least 20 are known; until then the $50 default stands. It is re-derived weekly, moves at most 25% a week, and stays between $10 and $500, and a session keeps the budget it started with. The tip says where the figure came from ("62% of $38, your p90 session, from 113 sessions"), and `tokenops coach stats` shows the budget, its evidence and any clamp that held it back. On a machine where 44 of 113 sessions passed $50, the tips now mean "unusual for you" rather than firing in four sessions of ten

### Fixed

- **ingestion:** every local transcript reader whose sessions are on your machine is now read, not just the first ones. Before, `tokenops init` turned readers on only when none was on yet, so with one reader on, a second client's sessions (an opencode store, say) stayed unread for good and `vendor-usage status` showed it disabled with 0 events. Now `init` turns on each reader (Claude Code, Codex, opencode, Gemini CLI, Pi) whose data is present and says so once, and the daemon does the same within 15 minutes of a client's sessions appearing, restarting itself so the reader runs. A reader you switch off with `tokenops vendor-usage enable <source> --disable` is remembered as off (`switched_off: true`) and never turned back on; an `enabled: false` an older `init` wrote is not taken as your choice, so if you had switched a reader off before this release, switch it off again. opencode 2 stores, which hold only `session_v2` and `session_message`, are read like 1.x ones. `vendor-usage enable` no longer tells you to restart the daemon in its help: it restarts the supervised daemon itself (#680)
- **fireworks:** the spend Fireworks reports for a whole account (`/billing/summary`, every member of an organisation together, against the account's limit) is labelled as the account's, so it no longer reads as your own extra usage. The glance cards and menu bar call it "Account spend" with "account <id>, all members", `plan headroom` adds the same label, and the API carries `spend_scope` and `spend_scope_label`. The figures are unchanged, and a member's own cap on a company account carries no label (#680)
- **plans:** when you change plans and your client says so, TokenOps now follows it. Before, `init` saw Claude Code report Max 5x against a Max 20x binding and only pointed it out, so every Claude window limit stayed 4× too high until you noticed. Now, whenever Claude Code's account tier or Codex's `plan_type` names a different plan, TokenOps switches to it from when the client first reported it: `init` does, and the daemon checks every 15 minutes, so nobody has to re-run `init`. The switch is recorded in `tokenops plan history` and in `tokenops audit` (actor `claude_code` or `codex`, with the account type and tier it reported), and said once in `init`'s output and the daemon log and for a week on `tokenops glance`, each with the command that undoes it (`tokenops plan set <provider> <plan>`). A plan you set after the client started reporting its plan is kept until the client reports a different one, and a report that fits two plans or none changes nothing
- **audit:** a plan change made now (`tokenops plan set` without `--since`, the `tokenops_configure` tool, the API) is written to the audit log. Only a backdated one was, although the change promised both

## 0.104.0 - 2026-10-14

### Changed

- **docs, README, npm:** TokenOps is described as guardrails for coding agents: "Know what's left. Set what's allowed. Let your agents work." The landing page leads with a comparison against vendor meters, usage meters and gateways, and gives action boundaries and the team plane their own sections
- **brand:** a 1200×630 link preview (`assets/brand/social.png`, drawn by `make.py`) with Open Graph and Twitter card tags on the docs site, and the wordmark on the npm page
- **hooks:** every `tokenops` process starts about 5 ms sooner, which the hooks Claude Code runs on each tool call pay every time. The vendor-account parsers compile their patterns and the coach builds its price table on first use, not at start; package init went from about 13 ms to 6 ms, most of what is left being the SQLite library's
- **daemon:** the session analysis it runs every three hours reads only what changed: it keeps what each Claude Code and Codex transcript yielded between runs (never prompt text), reads a grown Claude Code transcript from where it stopped, and reads a Codex rollout again only when reading it would yield something different. Every scan of the week, CLI included, also skips transcripts last written before the window. On a synthetic week of 400 transcripts a repeat run went from 1.5s and 1.2 GB allocated to 55 ms and 165 MB; on a real machine the run took 55s–3m
- **statusline:** the coach's open tip now ends the status line itself, in a few words ("compact now (/compact)", "Claude weekly 75% used → /compact, smaller model"), until the tip is settled. Before, it was a second row under the line, or only the dim note Claude Code prints after a turn, which is easy to miss and does not show at all when the session is driven remotely. Tips about work about to stop (the session budget spent, a window at 90% or more) are coloured as a warning. Nothing shows when the coach's `inform` power is off, and at `quiet` only those warning tips show. A quota tip disappears once its window resets

### Fixed

- **docs:** on a phone the hero ring overlapped the headline; it now sizes to the screen
- **coach:** a tip you never saw no longer counts as ignored. The follow-through record (`tokenops coach`, the `tokenops_coach` tool) counted every tip the Stop hook printed and you didn't act on as "ignored", although that note may never have been read, and five of those in a row made the coach stop giving that tip. A tip now counts as seen only once the status line has shown it. One that was never shown and not acted on is listed as "unseen": it counts as neither followed nor ignored, and it never makes the coach go quiet. A tip you acted on still counts as followed, wherever it was shown. Older records have nothing showing they were seen, so their ignored tips now read as unseen
- **MCP:** `tokenops_records` with `view=verify` failed in strict clients such as Claude Code ("Structured content does not match the tool's output schema … success_rate must NOT have additional properties"). The success rates now match the declared schema, and a test checks the verify output against it
- **claude.ai usage meter:** once a session has expired and the browser holds no newer one, the daemon reads the browser again only after the browser has written its cookies, where it copied the cookie store on every poll (every five minutes, for days) and logged two warnings each time. The expiry is logged once until a reading succeeds or the cookies change; the stale-reading finding still says how to reconnect
- **storage:** the daemon copies the event store's write-ahead log into the database from a background loop every 10 seconds, instead of inside whichever write crossed SQLite's threshold. That write ran the checkpoint and its disk sync while holding the lock every tokenops process shares, and while long reads were open it retried on every write: the source of "sqlite: slow write lock" warnings and of retried event batches. On a copy-sized test store (451 MB) with three month-long reads running, appends of 64 events over 100 ms fell from 78 to 50 in 2,400 and the slowest from 2.1s to 0.4s. Other tokenops processes keep SQLite's default

## 0.103.0 - 2026-10-13

### Added

- **boundaries:** action boundaries, phase 1 (ADR 0014). A `boundaries:` section in `config.yaml` sets limits before work starts: an action (`continue`, the agent's next tool call; `subagent_model`), conditions on figures TokenOps already holds (`session_cost_usd`, `plan_left_pct` with `window: 5h|week`, `model_tier`, `workflow_id`, `agent_id`) and what is then required (`allow`, `ask`, `approver:<role>`, `deny`). The strictest enforced rule wins; a config with an unknown action or condition is refused with the rule named. Every rule starts in shadow, recording what it would have done; `tokenops boundaries promote <name>` writes `stage: enforce` beside it (`demote` undoes it)
- **boundaries:** the pause point. `tokenops hooks install --boundary` wires a PreToolUse hook on every tool in Claude Code, and the opencode plugin's `tool.execute.before`; route-guard's Agent hook checks `subagent_model` rules. An enforced `ask` asks at Claude Code's prompt when someone is there and otherwise refuses with the rule, what was crossed and `tokenops approve <id>`; `deny` refuses. It reads ids only, answers from two small files in about 0.4 ms (a 5 ms budget, gated in `make bench-gate`), and fails open, recording the failure. Codex and Cursor are reported as advice only
- **boundaries:** local answers. `tokenops approve <id> [--session]` (at a terminal only) and `tokenops deny <id>`; the MCP tool `tokenops_boundaries` (`action=decide`) and `POST /api/boundaries/decisions` can only deny, so the agent a boundary stopped cannot approve itself; a yes at Claude Code's prompt is read back and holds for the session. `tokenops boundaries status [--json]`, `tokenops_boundaries` (`action=status`) and `GET /api/boundaries` show each rule's stage, how often it matched or would have fired, per client whether it is enforced or advice only, and what waits on you. `approver:<role>` is answered locally, as `ask`, until the team plane routes it
- **boundaries:** organisation boundaries, phase 3 (ADR 0014). Owners and admins set rules for every member in the team plane's web view or with `tokenops team admin boundaries --set <file.yaml>`; the daemon fetches them every five minutes as a document the server signs (Ed25519; the key is pinned on the first fetch and every read verifies, so rules edited on disk are not rules). They sit beside local rules as `org:<name>` and the strictest wins, so a member cannot loosen them or change their stage. Leaving a team forgets them; `tokenops team status` says which version is held
- **boundaries:** approval by role. An organisation rule naming `approver:<role>` refuses the action (with or without someone at the keyboard) and the daemon sends an approval request to the team plane — the rule, action, client and the figures that crossed it, nothing else (`teamwire.ApprovalRequest`, held to enumerations and numbers by `TestApprovalHasNoFreeText`). Members with that role or above are e-mailed and answer in the web view or with `tokenops team admin approvals|approve|deny`; the answer reaches the machine within a minute and is recorded with who gave it. A member can deny but not approve an organisation rule locally, and nobody answers their own request through an admin token. The menu bar shows such decisions as waiting on the organisation
- **boundaries:** phase 2 (ADR 0014). Three more actions. `spend` is the agent's next tool call judged by money, with `budget_used_pct` (and `budget: <name>`; without one, the fullest budget). `model_route` is the router moving a request to another model, with `model_tier` (the target's) and `plan_left_pct`: a held route leaves the request on its model and never fails it. `share` is figures leaving the machine, with `destination: == team | otel`: a held team upload, OTel metric push or OTel event stream is not sent (events are dropped while held, not queued). Each action takes only the conditions it can be judged by, and a config naming another is refused. Routes and destinations are decisions of their own, answered with the same `tokenops approve` (`--session` covers 24 hours)
- **boundaries:** the daemon measures plan headroom and budget use every minute into `~/.tokenops/boundaries/figures.json` when a rule compares them, so the pause point can judge `spend` rules and opencode's `plan_left_pct` (the tightest plan TokenOps watches) without opening the event store
- **boundaries:** advice for Codex and Cursor. Enforced `continue` and `spend` rules that hold on plan headroom or a budget are listed on the glance (`tokenops_glance`, `GET /api/glance` under `boundaries`) with what the agent should do. `tokenops boundaries status` shows the router, the team upload and the OTel export as rows of their own, enforced, in shadow or idle
- **boundaries:** `tokenops approve <id> --confirm` shows what waits and answers only on `y`. The menu bar lists waiting decisions under *Waiting on you*: Deny answers through the daemon, Approve opens a terminal running `approve --confirm`, so an approval is still a person at a terminal
- **events:** `DecisionEvent` names its `actor` (harness, session, agent, local user) and its `approval` (approver, method, time, rule, scope); schema 1.6.0. `tokenops explain <bnd-…>` and `tokenops_explain` show the rule and who answered

### Security

- **dependencies:** golang.org/x/net v0.60.0; the release workflow's artifact actions pinned to v7.0.2 and v8.0.2 (#634)

## 0.102.0 - 2026-10-12

### Added

- **team plane:** `tokenops team admin invite --email <address>` has the server e-mail the invite (the join line and a page explaining the install); the printed join line still works. `teamwire.InviteRequest.Email` and `Invite.Emailed`
- **team plane:** self-serve. `tokenops team create --server <url>` opens the server's sign-up page (Sign in with GitHub, which reads the verified primary e-mail and name only, or an e-mail address confirmed by link and a password, with optional two-factor), creates an organisation with a 14-day free trial, and joins this machine through a device link: the CLI shows a short code that is typed into the signed-in page, never put in a URL, and polls until it is approved (RFC 8628 style). The same handshake gives owners and admins an expiring administrator credential (`tokenops team admin login|logout`, kept in `team-admin.json` beside the enrolment, mode 0600; `TOKENOPS_TEAM_ADMIN_TOKEN` with `--server` as an alternative)
- **team plane:** `tokenops team admin teams|create-team|invite|members|remove|role|grants|grant|revoke|billing|audit` on the same API as the web view, each with `--json`: invites with a role, role changes, removal (`--yes`, erases the member's figures), grants with a reason, billing status with `--checkout` (the server's billing page) and `--portal` (Paddle's customer portal; links to any other site are refused), and the audit log
- **team plane:** paused uploads are explained. When an organisation has no active trial or subscription the server refuses uploads with `402` and `code: uploads_paused`; `team sync`, `team status` and the daemon's log say so plainly, that the figures held stay viewable read-only until they are deleted, how an owner subscribes, and that the next upload resends the missed days. `team status` also shows the trial's end and when the first week of totals appears
- **teamwire:** the administration, device-link and billing messages (`DeviceLink*`, `AdminCredential`, `Team`, `Member`, `Invite*`, `RoleRequest`, `Grant*`, `AuditEntry`, `Plan`, `Billing`, `BillingLink`), `Me.Plan`, and `Error.Code`. The upload contract is unchanged; `TestAdminMessagesCarryNoFigures` fails if an administration message reaches an upload's types
- **brand:** a logo. The lowercase wordmark tokenops, whose o is the headroom ring the menu bar draws, on the docs site (with a favicon and its colours), the README, the menu bar app (its first app icon) and the VS Code extension; the files and the script that draws them are in `assets/brand/`
- **vendor-usage:** browser localStorage, at setup only (ADR 0013). For a site that keeps its session in localStorage, `tokenops vendor-usage setup <id>` reads exactly the named keys of one origin from a Chromium-family browser's profile (a private copy of its LevelDB, read with pure-Go goleveldb; no Keychain item, since Chromium does not encrypt localStorage) and stores the session like a pasted one; the daemon never reads a browser, and `--paste` remains. Windsurf's live web figures and Devin (its auth1 session and the organisation last used) now read their session this way, with the typed `ORG:TOKEN` or bundle as the fallback
- **providers:** a generic CLI source in the provider descriptor (`Command`: binary, argument lists, deadline, environment allowlist, signed-out phrases), run with no shell, an empty stdin, its own process group and bounded output that is never logged, and skipped silently when the CLI is not installed. Doubao reads the signed-in `arkcli usage plan --format json` (Coding Plan windows, else the Agent Plan's) and Alibaba Token Plan reads `bl usage token-plan --output json` before the browser session (5-hour, weekly and monthly windows; the international console, then the mainland one), with `bl` given only PATH, HOME, locale, time zone and proxy variables
- **Kiro:** overage credits. Once granted (`setup kiro --use-app-login`), kiro-cli's own sign-in and profile are read from its database (a private copy, two values by one `SELECT`) and CodeWhisperer's `GetUsageLimits` reports the plan's credits this month and the overage credits used against their cap (`kiro-overage`); kiro-cli renews the token, never TokenOps
- **Amazon Bedrock:** AWS SSO and assume-role profiles, behind the same `setup bedrock` opt-in. An SSO profile (`sso_session` or the legacy `sso_start_url`) reads the token `aws sso login` cached in `~/.aws/sso/cache`, read-only, and asks the SSO portal for the role's credentials; an expired sign-in is a stale reading whose hint is `aws sso login --profile <name>`, never refreshed. An assume-role profile asks STS `AssumeRole`, signed with its `source_profile`'s (or, with `credential_source = Environment`, the environment's) keys. The AWS CLI never runs and nothing prompts: profiles that need an MFA code, and `credential_process` (a program TokenOps cannot know will not prompt), are refused with a reason
- **vendor-usage:** other applications' sign-ins, read only by grant (ADR 0013). `tokenops vendor-usage setup <provider> --use-app-login` prints exactly which file, Keychain item or process it would read, which field, and the one host the token is sent to; asks y/N; reads it once against the vendor; and only then records the grant in `vendor_usage.grants.<provider>` (no secret is stored). The daemon re-reads a granted item read-only as it polls, so a token the owning app renews is picked up; it never writes, refreshes or logs it, reads a Keychain item only without a prompt (never with `keychain.disabled`), tries it only after every key, and stops at `--revoke-app-login`. A grant covers exactly what was shown: a release that would read another field or send the token elsewhere is not covered until granted again. `vendor-usage status` lists every grant. Kilo (the kilo CLI's `auth.json`), Codebuff (the codebuff CLI's session, which also reads the weekly rate limit), ClinePass (Cline's sign-in), Nous Portal (Hermes Agent's `auth.json`) Hugging Face (the `hf auth login` token file), Muse Code (its `auth.json` or, without a prompt, its Keychain item), Factory (Droid's `~/.factory/.env`) and Grok (the grok CLI's `~/.grok/auth.json`) gain it as a second way in, beside a typed key
- **vendor-usage:** an optional scope per provider, `vendor_usage.accounts.scopes.<id>`, set with `tokenops vendor-usage setup <id> --scope <value>` (verified with the key before it is stored; `--scope ""` clears it). Kilo reads that organisation instead of the personal account (`X-KILOCODE-ORGANIZATIONID`), v0 that project instead of the default scope (`?scope=`), as CodexBar does; a provider whose reader takes no scope refuses one
- **providers:** Warp: the add-on credits left, the user's and each workspace's grants together, as a credits balance (`balance_credits`, unit `credits`) beside the plan's credit window; read on an unlimited plan too. Poe: the points spent over the last 30 days, from `GET /usage/points_history` followed by its cursor, stored in points as spend in the vendor's own unit (`used_credits`, a new reading field) and never as dollars; a history that cannot be read whole is left out, and the balance is still read
- **providers:** ZenMux: `ZENMUX_MANAGEMENT_API_KEY` is read for the Management API, without setup, as the source's own variable (`Sources[].EnvVars`): a key found there goes only to the Management API reader, and the inference keys harnesses hold (`ZENMUX_API_KEY`) are still never sent there
- **providers:** xAI: the team's spend over the last 30 days, from the Management API's usage history (`POST /v1/billing/teams/{team_id}/usage`), beside the prepaid balance and with the same `TEAM_ID:MANAGEMENT_KEY`. It is stored as spend with its period (`extra_usage_period_min`), as ai&'s 30-day spend now is too, so headroom says it covers the last 30 days rather than calling it this month's; a history xAI cuts short is not stored
- **team plane:** a hosted rollup, run by klarlabs, of the figures TokenOps measures on each member's machine, by team, repository and kind of work, per day or week (#250, ADR 0012). The client is open source and lives here; the server is a klarlabs service and is not published (no source, binaries or image). `tokenops team join <url> <invite>` enrols a machine with the address and single-use code from an invite (there is no default server); the daemon then uploads every hour, per UTC day, repository and kind of work, counts, time waiting on the agent, tokens and cost. Never prompts, files, paths, transcripts, commit messages or model outputs: the upload format (`pkg/teamwire`) has no field that could hold them, a test fails the build if one is added, and the server refuses anything else. `tokenops team preview --json` prints an upload byte for byte; `team status` shows what the server holds, who may see your individual figures, the address single sign-on signs you in by, and every time someone looked; `team sync`, `team web` (a single-use sign-in link) and `team leave` (erases your figures unless `--keep-history`). A new `team` config section and `TOKENOPS_TEAM_*` variables; nothing is sent until you join
- **team plane:** what the service promises about what crosses is documented publicly in ADR 0012: totals withhold any group of fewer than three people and as many others as needed that no difference of what is shown singles anyone out, each week released once and never recomputed; individual figures only under an owner's grant the member sees, with every view recorded; retention of 400 days for figures and 730 for the audit log; hosting in Germany under a GDPR Art. 28 DPA; single sign-on to the web view with any OpenID Connect provider, which signs in only existing members
- **vendor-usage:** `tokenops vendor-usage setup <provider>` connects any vendor account TokenOps reads (OpenRouter, DeepSeek, Moonshot, z.ai, Kimi, MiniMax, Synthetic, Chutes, DeepInfra, Vercel) when no harness or environment variable holds its key: the key is typed without echo, checked with one reading, and only then stored in your config (redacted wherever config is shown) and sent only to that vendor
- **providers:** ClinePass: the subscription's 5-hour, weekly and monthly windows, read with a Cline API key (`CLINE_API_KEY`, `CLINEPASS_API_KEY`, opencode's `cline-pass` key or `vendor-usage setup clinepass`)
- **providers:** Kilo: Kilo Pass credits used this billing period and the prepaid credit left, read with a Kilo API key (`KILO_API_KEY`, opencode's `kilo` key or `vendor-usage setup kilo`)
- **providers:** Codebuff: credits used against the quota until it resets, read with a Codebuff API key (`CODEBUFF_API_KEY` or `vendor-usage setup codebuff`); a provider opencode does not know can now name its own key variables
- **providers:** ZenMux: the rolling 5-hour and 7-day quotas and the pay-as-you-go balance, read with a Management API key that only `vendor-usage setup zenmux` supplies (the inference keys harnesses hold are never sent to the Management API)
- **providers:** xKiro: the plan's 5-hour and 7-day spend windows, today's free-token allowance and the wallet balance, read with an xKiro API key (`XKIRO_API_KEY` or `vendor-usage setup xkiro`)
- **providers:** DevPass (LLM Gateway): the billing cycle's plan credits and the premium weekly window, or on pay-as-you-go the key's spend against its limit, read with a gateway API key (`DEVPASS_API_KEY`, `LLMGATEWAY_API_KEY`, opencode's `llmgateway` key or `vendor-usage setup devpass`)
- **providers:** IBM Bob: Bobcoins used this month against the team budgets, read with an IBM Bob API key or IAM token (`BOBSHELL_API_KEY` or `vendor-usage setup ibmbob`)
- **providers:** v0: the share of the billing cycle's balance and of the request quota used, read with a v0 Platform API key (`V0_API_KEY`, opencode's `v0` key or `vendor-usage setup v0`)
- **providers:** Nous Portal: the monthly credit grant used this period and the top-up credit left, read with a Nous Portal access token from Hermes Agent's sign-in (`NOUS_PORTAL_ACCESS_TOKEN` or `vendor-usage setup nous`); the token expires within the hour and is never refreshed, and an expired one is refused before any request
- **providers:** Warp: credits used since the last refresh against the plan's limit, read with a Warp API key (`WARP_API_KEY`, `WARP_TOKEN` or `vendor-usage setup warp`)
- **providers:** Atlas Cloud: the account's available USD balance (`GET /public/v1/balance`), with an API key from setup or `ATLASCLOUD_API_KEY`
- **providers:** Poe: the point balance left (`GET /usage/current_balance`), kept in points (`balance_credits`), never converted to dollars; API key from setup, `POE_API_KEY` or opencode
- **providers:** Deepgram: the prepaid USD balance left across the key's projects (`GET /v1/projects/{id}/balances`), with an API key from setup or `DEEPGRAM_API_KEY`
- **providers:** Neuralwatt: prepaid USD credit left and, for a subscriber, the period's kWh allowance used as a plan window (`GET /v1/quota`); API key from setup, `NEURALWATT_API_KEY` or opencode
- **providers:** ai&: the organisation's USD spend in the last 30 days, summed exactly from its request logs (`GET /logs`), and out-of-credit (402) as limit reached; API key from setup, `AIAND_API_KEY` or opencode
- **providers:** xAI: the team's posted prepaid USD credit from the Management API (`GET /v1/billing/teams/{team_id}/prepaid/balance`); `tokenops vendor-usage setup xai` asks for `TEAM_ID:MANAGEMENT_KEY`. xAI was in the catalog only
- **providers:** Doubao (Volcengine Ark): the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan), from Volcengine's signed OpenAPI; `tokenops vendor-usage setup doubao` asks for an AccessKey pair as `ACCESS_KEY_ID:SECRET_ACCESS_KEY`
- **providers:** Venice: the USD balance left and, when staking, the share of the DIEM epoch allocation used (`GET /api/v1/billing/balance`); API key from setup, `VENICE_API_KEY` or opencode
- **providers:** ElevenLabs: the subscription's credits used this period as a plan window, with its reset (`GET /v1/user/subscription`); API key from setup, `ELEVENLABS_API_KEY` or `XI_API_KEY`
- **providers:** Hugging Face: Inference Providers charges this month (gross less the included amount), against the spending limit when set (`GET /api/settings/billing/usage-v2`); token from setup, `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN` or opencode
- **providers:** opencode Go's 5-hour, weekly and monthly windows are read from `GET /zen/go/v1/usage` with the opencode API key (`OPENCODE_API_KEY`, opencode's auth.json, or `tokenops vendor-usage setup opencode-go`); it was in the catalog only
- **providers:** OpenAI's API organisation spend this month is read from the Administration API (`GET /v1/organization/costs`) with an organisation admin key from `OPENAI_ADMIN_KEY` or `tokenops vendor-usage setup openai`; the API keys the harnesses use are never sent there
- **providers:** sub2api gateways are read from `GET /v1/usage` with a group API key: a key's quota and 5-hour, daily and 7-day limits, a subscription group's daily, weekly and monthly limits, or the wallet balance; recognised where a harness sends a key to one, or named with `SUB2API_BASE_URL` and `SUB2API_API_KEY`
- **providers:** LLM Proxy (LLM-API-Key-Proxy) gateways are read from `GET /v1/quota-stats`: the tightest quota group left on the credentials it pools and when it resets; recognised by its root route, or named with `LLM_PROXY_BASE_URL` and `LLM_PROXY_API_KEY`
- **providers:** Aixy gateways, hosted or self-hosted, are read from `GET /v1/usage` with a project API key: each budget that applies to the key, spent and reserved against its limit, the binding hard budget as the spend cap; `AIXY_API_KEY` (with `AIXY_BASE_URL` for a self-hosted one) or setup
- **providers:** Amazon Bedrock's spend this month is read from AWS Cost Explorer (`GetCostAndUsage`, signed with SigV4) once `tokenops vendor-usage setup bedrock` opts in with the AWS credentials on the machine (environment or shared credentials file; nothing is stored); Cost Explorer bills each request, so it is asked at most every 8 hours
- **providers:** Azure OpenAI is in the catalog: turns sent to `*.openai.azure.com` (and opencode's `azure` provider) are billed to it and priced at models.dev's Azure rates, rather than probed as an unknown gateway; its account is not read, since a resource key reads no spend or quota
- **vendor-usage:** `tokenops vendor-usage setup <gateway>` connects a self-hosted or hosted gateway (LiteLLM, Bifrost, ClawRouter, sub2api, ...) by its address and a key, checked once there before either is stored; plain HTTP only to a local or private-network host
- **providers:** GitKraken AI: weekly AI credits (and the organization's pool with `GITKRAKEN_ORG_ID`) from api.gitkraken.dev, with the account's bearer token from `GITKRAKEN_API_TOKEN` or `tokenops vendor-usage setup gitkraken`
- **providers:** JetBrains AI: the monthly AI credits the IDE's AI Assistant records in its own quota file and `idea.log`, read locally from the most recently used IDE with no credential
- **providers:** Windsurf: the daily and weekly quota Windsurf caches in its own `state.vscdb`, read locally with no credential, and live from windsurf.com with the web session bundle pasted into `tokenops vendor-usage setup windsurf`
- **providers:** Kiro: monthly plan credits and bonus credits from the operator's signed-in `kiro-cli` (`chat --no-interactive /usage`), which signs its own request; TokenOps reads no credential
- **providers:** Amp: subscription agent usage and orb hours, Amp Free's daily allowance and individual credits, from the signed-in `amp usage` CLI, or with an access token (`AMP_API_KEY` or `tokenops vendor-usage setup amp`)
- **providers:** Augment Code: credits used of the month's allowance, from the signed-in `auggie account status` CLI, or from app.augmentcode.com with the browser session `tokenops vendor-usage setup augment` reads (the daemon re-reads it only quietly)
- **providers:** CodeRabbit: reviews this billing period from the signed-in `coderabbit usage` CLI, stored as a count (`count_<n>_*` attributes): CodeRabbit reports no allowance, so there is no percentage
- **providers:** Zed: edit predictions used of the plan's allowance this billing cycle, from cloud.zed.dev with the Zed editor's own sign-in, which only `tokenops vendor-usage setup zed` reads from the Keychain (opt-in; the daemon never reads the Keychain for it)
- **providers:** Antigravity: 5-hour and weekly quota for Gemini models and for Claude and GPT models, asked of the running Antigravity app's own local language server on 127.0.0.1; nothing leaves the machine
- **providers:** Pi coding agent: per-turn tokens from Pi's and OMP's local session transcripts (`~/.pi/agent/sessions`, `~/.omp/agent/sessions`), under the provider each turn was served by (`vendor_usage.pi`, `tokenops vendor-usage enable pi-sessions`; `tokenops init` turns it on when Pi's sessions are on the machine)
- **providers:** Alibaba Cloud Coding Plan: the 5-hour, weekly and monthly quotas from the Model Studio or Bailian console, read with the Coding Plan API key (`ALIBABA_CODING_PLAN_API_KEY`, `DASHSCOPE_API_KEY`, opencode's `alibaba-coding-plan` key) or the console's browser session (`vendor-usage setup alibaba`, `ALIBABA_CODING_PLAN_COOKIE`)
- **providers:** Alibaba Cloud Token Plan: the Personal/Solo plan's 5-hour, weekly and monthly windows, or a Team plan's credit pool, from the Model Studio or Bailian console with its browser session (`vendor-usage setup alibabatokenplan`)
- **providers:** Qwen Cloud: the Individual Token Plan's 5-hour, weekly and monthly windows from the Qwen Cloud console, read with its browser session (`vendor-usage setup qwencloud` or `QWEN_CLOUD_COOKIE`)
- **providers:** Xiaomi MiMo: the balance (in its own currency when not US dollars) and the Token Plan's monthly credits used, from the MiMo platform console with its browser session (`vendor-usage setup mimo`)
- **providers:** Sakana AI: the 5-hour and weekly quota windows and the pay-as-you-go credit balance from the console's billing page, read with a pasted Cookie header (`vendor-usage setup sakana` or `SAKANA_COOKIE`)
- **providers:** Abacus AI: compute credits used this billing month, resetting at the next billing date, read from apps.abacus.ai with its browser session (`vendor-usage setup abacus`)
- **providers:** Mistral: the subscription's included-API and Vibe allowances used this month (and the credit balance, in its own currency), read from admin.mistral.ai with its browser session (`vendor-usage setup mistral`); the billing spend, priced in euros, is not stored
- **providers:** Command Code: the 5-hour and weekly limits, the monthly credit grant used and the purchased credits, read from api.commandcode.ai with the web session (`vendor-usage setup commandcode`; a pasted bare session token works too)
- **providers:** Qoder: big-model credits used against the plan's (and the team's shared) total until the next reset, from qoder.com or qoder.com.cn with the dashboard's browser session (`vendor-usage setup qoder`)
- **providers:** StepFun: a Coding Plan's 5-hour and weekly windows or a Token Plan's credit pool; `vendor-usage setup stepfun` signs in once with your username and password (typed without echo) and stores only the session token, never the password (`--paste` or `STEPFUN_TOKEN` take a token)
- **vendor-usage:** browser-session providers are read the way the browser sends their cookies (several hosts, prefixed names, the whole host when the names are unknown), with at most one Keychain prompt in setup; a provider can be read with an API key or a session, or signed in to with a password (only the token is stored); a stored session the daemon cannot renew quietly turns its reading stale and the status says to run setup again
- **providers:** Manus: the Pro plan's monthly credits and the daily refresh credits used, as plan windows, and the credit balance in credits (`POST /user.v1.UserService/GetAvailableCredits`), read with the manus.im browser session (`vendor-usage setup manus`, a pasted Cookie header, `MANUS_SESSION_TOKEN` or `MANUS_COOKIE`)
- **providers:** Perplexity: the share of the plan's monthly credit grant used, as a plan window resetting at renewal, and the credit balance in dollars (`GET /rest/billing/credits`), read with the perplexity.ai browser session (`vendor-usage setup perplexity`, a pasted Cookie header, `PERPLEXITY_SESSION_TOKEN` or `PERPLEXITY_COOKIE`); a Perplexity API key is never sent there
- **providers:** T3 Chat: the 4-hour Base window and the monthly Overage budget used, as plan windows (`GET /api/trpc/getCustomerData`), read with the t3.chat browser session's Cookie header pasted at `vendor-usage setup t3chat`. A browser-session provider whose cookies are not known by name now leaves `Cookie.Names` empty: setup asks for the pasted header and no browser is ever read for it
- **providers:** ZoomMate: AI credits used against the budget cap this billing cycle, as a plan window, and the credits left (`GET /ai-computer/api/v1/credits/status`, after exchanging the session for ZoomMate's short-lived token), read with the Cookie header of a request to ai.zoom.us pasted at `vendor-usage setup zoommate`
- **providers:** Notion AI: the AI usage allowance used, the rolling (6-hour) window and the billing period, as plan windows (`POST /api/v3/getCreditRateLimitStatus` on app.notion.com), read with the notion.com browser session's `token_v2` (`vendor-usage setup notion` or a pasted Cookie header); Business and Enterprise workspaces only
- **providers:** LongCat: the active token pack's share used (the legacy usage aggregate without one) and the tokens left, pending fuel packs included, in tokens (`POST /api/pay/quota/metering/token-packs/summary`), read with the longcat.chat session's Cookie header pasted at `vendor-usage setup longcat` or `LONGCAT_MANUAL_COOKIE`
- **providers:** Helmcode: each model's token quota used, as a plan window per model, and the prepaid balance in its own currency (`GET /api/usage/quota` on cloud-api.helmcode.com), read with the Helmcode Cloud dashboard session's Cookie header pasted at `vendor-usage setup helmcode`
- **providers:** TypeSafe: spend this billing cycle and the credit balance left, in dollars (the console billing page's getBillingOverview action, whose ID is found in the page's own scripts and kept for 12 hours), read with the console.typesafe.ai session's Cookie header pasted at `vendor-usage setup typesafe`
- **providers:** Raycast: the month's AI credit allowance used, as a plan window resetting when the next credits arrive, and the credits left (`GET /frontend_api/current_user/ai_credits`), read with the www.raycast.com browser session (`vendor-usage setup raycast` or a pasted Cookie header)
- **providers:** Replicate: spend this month from the current monthly-usage invoice, and the prepaid credit left (`GET /api/{users|organizations}/{name}/invoices` and `/unused-credit`, the account named by the billing page), read with the replicate.com browser session (`vendor-usage setup replicate` or a pasted Cookie header); Replicate's API token is not used
- **providers:** Devin: the organisation's daily and weekly quota used and the extra-usage balance (`GET app.devin.ai/api/<org>/billing/quota/usage`), read with the web app's bearer token given as `ORG:TOKEN` in `vendor-usage setup devin`
- **providers:** Muse Code: the 5-hour and weekly quota used (`POST api.meta.ai/muse-code/key`), read with the `dca:` device token `muse login` issues, given in `vendor-usage setup muse`; the inference key in the answer is never kept
- **providers:** X API: the prepaid credit left on console.x.com, purchased plus free and below zero when overdrawn (`GET /api/me`, `/api/accounts/{id}/credits`), read with the console's browser session (`vendor-usage setup xapi`)
- **providers:** LithosAI: the organisation's prepaid balance and its spend this UTC month (`GET /api/billing`, `/api/billing/spend`), read with the console's browser session (`vendor-usage setup lithosai`)
- **providers:** Langdock: the included 5-hour session and weekly limits used (`usageSettings.getPersonalUsage`), read with app.langdock.com's browser session (`vendor-usage setup langdock`)
- **providers:** Ollama Cloud: the monthly usage (or the older session and weekly windows) from ollama.com/settings, read with the browser session (`vendor-usage setup ollama`)
- **providers:** WorkBuddy (Tencent): the share of the credit packages' cycle used and when it ends, read with www.workbuddy.cn's browser session (`vendor-usage setup workbuddy`)
- **providers:** Muse (muse.ai): the weekly token allowance used, from the page's own server action, read with muse.ai's browser session (`vendor-usage setup museai`)
- **providers:** Charm Hyper: the Hypercredits left (`GET hyper.charm.land/v1/credits`), kept in Hypercredits, read with an API key (`HYPER_API_KEY`) or the browser session (`vendor-usage setup hyper`)
- **providers:** Factory (Droid): 5-hour, weekly and monthly windows (and the Core fallback's), or an older plan's Standard and Premium token allowances, and the extra-usage balance, read with a Factory API key (`FACTORY_API_KEY`) or app.factory.ai's browser session (`vendor-usage setup factory`)
- **providers:** Grok: the SuperGrok credit window used and the prepaid balance, read with the Grok CLI's sign-in token (`GROK_OAUTH_TOKEN` or `vendor-usage setup grok`), or the window from grok.com's billing with its browser session
- **providers:** GroqCloud: the organisation's spend this UTC month from the console's activity call, read with console.groq.com's browser session (`vendor-usage setup groq`)

### Fixed

- **vendor-usage:** z.ai, Kimi, MiniMax, Synthetic, Chutes, DeepInfra, Vercel and the LiteLLM, Bifrost and ClawRouter gateways have a hint in `vendor-usage status`, as do the Cursor hook ledger and the Claude Code status line feed; they showed none
- **menubar, cards:** every provider is named as its users know it in the menu bar and on plan cards (z.ai, Kimi, MiniMax, Vercel, Zhipu AI, Together AI, opencode Go, ...), where several showed a capitalised ID

### Changed

- **Antigravity:** the language server's CSRF token, which is Antigravity's own credential on its command line, is now read only once granted with `tokenops vendor-usage setup antigravity --use-app-login` (ADR 0013); until then nothing of the app's is read. The process is found through the system's process table, not `ps`

- **vendor-usage:** `vendor-usage status` lists sources by provider (alphabetically by provider ID), each provider's sources together

- **menubar:** each plan shows its vendor's own logo (Claude, Codex, Gemini, Copilot, Cursor and the API providers) where it showed two letters
- **menubar:** while the panel fetches your usage it says so, with a spinner and how long it has taken, and keeps what it showed; Refresh spins and reads "Refreshing…" until the new readings are in
- **menubar:** messages say what happened in plain words and what happens next ("Fetching your usage is taking longer than usual. Showing your usage as of 08:50."), where they said "the TokenOps daemon is slow to answer" or showed a raw status line
- **daemon API:** the answers the menu bar polls for every minute come back in a fraction of the time, with the same content. Measured on a copy of a 460 MB store: a month of one provider's spend (`/api/spend/summary?since=720h`) 0.85s → 0.14s, its daily series 1.0s → 0.16s and by model 0.95s → 0.20s; `/api/glance` 0.7s → 0.06s and `/api/findings` 0.75s → 0.08s once the daemon has read the window once; `/api/coach` 2.0s → under 1ms. One full menu-bar refresh went from 3.4–4.8s to 0.45–0.7s. On a busy machine the old figures were several times higher
- **storage:** the first start after upgrading builds an index of the usage the spend rollups read, so they never read the event payloads again (7s on a 460 MB store; other tokenops processes wait for it), and adds a log of changed events that lets the daemon keep the glance's events in memory, reading only new and changed ones between polls. It keeps at most a month of events and lets them go after 10 minutes without a request
- **daemon API:** the first glance after the daemon starts, and the first after events change (a plan restamp, re-attribution at start, a retention pass), no longer re-reads a month of events: the daemon reads them in the background at start and afterwards re-reads only the events that changed. Measured on a copy of a 460 MB store on a busy machine (ranges over runs): the first `/api/glance` after start 0.5–1.2s → 0.14–0.5s (a glance that arrives while the background read is still running waits for it), after an update of 5 events 0.55–0.88s → 0.04–0.07s, after a restamp of 537 events 0.55–0.68s → 0.04–0.10s, after a retention delete of 148,000 events 0.5–0.77s → 0.04–0.09s. A retention delete logs nothing and costs what it did (3.2s for those 148,000 events either way)
- **daemon API:** a spend summary or series reads its window once instead of three times, with byte-identical answers: `/api/spend/summary?since=720h` 0.15–0.21s → 0.08–0.14s and `/api/spend/series?since=720h&bucket=day` 0.19–0.37s → 0.08–0.15s on the same store

## 0.101.2 - 2026-10-08

### Added

- **menubar:** Refresh, in the panel (⌘R) and the right-click menu, asks the daemon's usage readers to poll now instead of re-reading readings up to 15 minutes old, then reads again as the new ones arrive; the panel says what it asked. Behind it, `POST /api/sources/refresh`, accepted once every 30 seconds

### Security

- **keychain:** TokenOps asks for Keychain access only when you start it. The MCP usage-meter setup no longer reads a browser, which opened its "Safe Storage" Keychain item so an agent could raise the macOS prompt unasked; the daemon re-reads the browser only for a claude.ai meter you connected from it, not for a pasted session, which stops when it expires and says how to reconnect. The setup commands name the item before macOS asks, and the CLI guide lists every case
- **keychain:** the daemon never shows a Keychain prompt. Its reads of the browser's "Safe Storage" key and of Claude Code's sign-in are quiet (Security.framework, with interaction forbidden): when macOS would ask, after an upgrade or when Claude Code renews its sign-in, the read is skipped and the stale-reading finding says to run the setup again. Only a setup command you run prompts, and one refusal stops it asking for other browsers. `keychain.disabled` (or `TOKENOPS_KEYCHAIN_DISABLED=1`) turns every Keychain read off
- **proxy:** the daemon refuses requests a web page could send it. A request whose Host is not this machine (DNS rebinding), whose Origin is not local, or that a browser marks cross-site gets 403, so a page can no longer relay through your provider routes or write spend into your history. A reverse-proxy name goes in the new `allowed_hosts`; the daemon warns at start when `listen` is reachable from the network without TLS
- **dependencies:** Go 1.26 and golang.org/x/crypto v0.57.0, which fixes GO-2026-6355 and GO-2026-6354
- **release:** the npm packages are built only after every archive matches the release's `checksums.txt`, and only when all four platforms are present; npm publishes by trusted publishing, with no token
- **security scanning:** nox is the one security gate, now running its reachability, taint-analysis and dependency-confusion plugins (installed plugins did not run before); the separate govulncheck step is gone, and CI scans with nox 1.48.1
- **docs site:** vite, vue and source-map-js upgraded past four high-severity advisories (build-time only)

### Changed

- **claude code sign-in:** `tokenops vendor-usage setup claude-code` reads Claude Code's Keychain item only with `--keychain`; without it, only `~/.claude/.credentials.json`. `--no-keychain` is deprecated: it is now the default
- **spend:** `tokenops spend --forecast` projects the daily totals `tokenops_forecast` projects. It fitted one point per model per day, so its numbers differ, and are right, from this release; with `--until` it projects from that date, and with too little history it says so instead of printing nothing (`forecast_note` in `--json`)
- **queries:** a capped query returns the newest rows, not the oldest: `tokenops optimizations --limit`, the MCP optimizations tool and verify showed the start of the window instead of its end
- **building from source:** needs Go 1.26 (the toolchain directive downloads it)
- **routing:** `tokenops routing proposals` gives the answer `tokenops_routing` gives: the text names what each upgrade adds per million tokens, or why its price could not be checked, and `--json` emits `{pending, note}` with the question to put to you, where it printed the approval log's raw records
- **dx:** `tokenops dx --json` emits the report `tokenops_agent_dx` returns, `{window, metrics, grades, recommendation}`; it carried the metrics alone, which now sit under `metrics`

### Fixed

- **verify:** `tokenops_verify` and `tokenops_records` (view=verify) take `all` to compare every transcript and event, as `tokenops verify --days 0` does; their documented `days: 0` meant the 30-day default, so older work could not be compared
- **story:** `tokenops_story` and `/api/story` give each task `context_carried_tokens`, as `tokenops story --json` does; `limit: -1` returns every task, where the documented `0` meant the default ten
- **plan:** `tokenops plan headroom` answers from what the vendors report when no plan is bound, as the MCP tool does, instead of refusing with "no plans configured"; glance, plan headroom, verify, otel and the plan history read the configured `storage.path` instead of always `~/.tokenops/events.db`
- **anthropic:** `tokenops vendor-usage backfill` stores the whole window it reports; it read one page of the Admin usage report, the first 24 of 168 hourly buckets. The live Admin poller reads every page too, instead of catching up a page per poll
- **task:** `tokenops task list --metrics` reads the store every other command reads; it always opened `~/.tokenops/events.db`, whatever `TOKENOPS_STORAGE_PATH` said
- **scorecard:** the MCP tool and `/api/scorecard` grade the agent KPIs (confirmation-gate, regenerate, tool-success and destructive-action rates) as `tokenops scorecard` always did; they graded the same week on four fewer metrics. `tokenops scorecard` reads the configured `storage.path` instead of always `~/.tokenops/events.db`
- **scorecard, coach:** transcripts last written before the window are no longer read; a seven-day scorecard over 2.1 GB of history went from 14.5s to 7.2s
- **coach:** `coach prompts`' average turn counts only the turns inside the window; it counted every turn in each file written during the window, and none from a session still running after it
- **coach, eval:** `coach replies` and `tokenops eval` print their rows in one order; tied sessions and the per-optimizer and drift rows came out in a different order each run
- **routing:** the route guard tiers models by the card the cost engine prices with, the negotiated rates in `pricing.path` included; it read the latest pricing snapshot alone, so a model the operator pays little for still tiered as a flagship
- **pricing:** `pricing show/diff/lint --snapshot/--from/--to` accept the timestamp `pricing show` prints (`2026-09-15T00:00:00Z`); only a bare date matched before. `pricing refresh --source` names every known source in its error
- **attribution:** the startup correction for Codex moved every turn of a session on a gateway such as Fireworks to that gateway, including the OpenAI models Fireworks runs on your own key, and undid live ingestion's correct attribution at every start. Turns are now decided per model; turns it moved go back to OpenAI, and turns from before endpoints were known gain their endpoint and are billed per token rather than covered by the ChatGPT plan
- **budgets:** an exceeded budget is recorded once per window. The watcher raised `budget.exceeded` on every tick (every 15 minutes) and after every restart, at the 75% warning and on a forecast breach too, so the audit log filled with budgets "exceeded" that were not; a token budget never raised one. It now fires once, at or past the limit, in tokens for a token budget, and not again when the audit log already has it
- **workflows:** reading a workflow trace no longer stores a `workflow.observed` domain event, so the event counters stop counting views
- **read guard:** each prevented re-read is published once, not every two minutes; with OTel event export on, a collector received the whole read-guard history again every tick
- **plans:** the unconfigured-plan hint no longer offers `TOKENOPS_PLAN_<PROVIDER>` as applying without a restart; set in a shell it never reached the daemon
- **config:** a leading `~` in `storage.path`, `pricing.path` and the other configured paths is expanded, as the configuration guide writes them; the daemon opened a directory literally named `~`, and a `~` pricing override failed to load
- **fmt:** the MCP tools `tokenops_fmt_analyze` and `tokenops_fmt_learn` use the formatters your config defines, as the CLI does; a command you had written a formatter for showed as unhandled and came back as the next formatter to write. `tokenops fmt --emit` records into the configured `storage.path`, not always `~/.tokenops/events.db`
- **usage meter:** connecting claude.ai's usage meter through the MCP tool keeps the browser session it reads, with its bot-check clearance, as `vendor-usage setup claude-subscription` does; it verified without the clearance and stored the session as if pasted
- **spend:** prompt-cache writes are priced at their own rate, 1.25× input for a five-minute write and 2× for a one-hour one on Anthropic, where they were priced as plain input; the Admin API poller also counted cache reads at the full input price and left cache writes out of input. Reported Anthropic spend and API-equivalent value go up, and historical Claude Code turns re-price with their writes
- **proxy:** request bodies over 4 MiB are forwarded intact; the proxy forwarded only the first 4 MiB, so the request failed or hung. Anthropic cache reads and writes now count as input, where proxied cache-heavy turns were priced as a few dozen tokens
- **analytics:** plan-covered value and recomputed costs price each event at the rate card in force when it ran; they used the oldest card, so models a pricing refresh added showed as unpriced and repriced models kept their old price
- **daemon:** a shutdown that outlasts its timeout (a long stream) still drains queued events and closes the store, instead of losing them
- **storage:** one malformed event no longer loses the up to 63 good ones in its batch; it is skipped and counted
- **headroom:** a store read that fails is reported, instead of "no plans configured"
- **checkup:** a usage source that exists but cannot be read is named in the warnings instead of looking like no usage, and a read cut short says its totals are low
- **claude code:** the poller's memory of turns it has seen is bounded to seven days and 200,000 entries; it grew for as long as the daemon ran
- **commits:** cost per commit no longer credits other people's commits to you (`bob@x.com` matched `jimbob@x.com`), and commits on `feat/…` and `fix/…` branches show their branch
- **opencode:** session directories honour the time window instead of listing every directory opencode ever used
- **spend:** consumers that tie are ranked by name, not in a different order each run
- **routing:** the gated-routing hint names `tokenops routing proposals` and the `tokenops_routing` tool; it named a command that does not exist
- **windows:** the Windows build compiles again
- **menubar:** on macOS 27 a click on the icon opened the panel in the middle of the screen, like a window; it opens under the icon again
- **menubar:** each coach finding's mark says what it means on hover; the faint dot in front of results such as "The read guard refused 255 re-reads" (level: for your information) is now a ring

## 0.101.1 - 2026-10-06

### Fixed

- **npm:** the launcher is `@klarlabs-studio/tokenops`: npm refused the unscoped name `tokenops` as too similar to an existing `token-ops`, so v0.101.0 published its four platform packages and no launcher. `npx @klarlabs-studio/tokenops checkup` runs the checkup with nothing installed first

## 0.101.0 - 2026-10-06

### Added

- **npm:** `npx tokenops checkup` with nothing installed first. The `tokenops` package launches the binary from one of four platform packages (`@klarlabs-studio/tokenops-{darwin,linux}-{arm64,x64}`), unpacked from the release's own archives, so npm ships what GitHub and Homebrew ship; published with provenance from the release workflow
- **checkup:** `tokenops checkup`, a week of agent work in one read, before any setup: tokens and their value at API prices per harness and model (Claude Code, Codex, Gemini CLI, opencode), the sessions' DX grade, and each leak with the command that fixes it — re-reads of unchanged files, instruction files re-read on every turn, lookups on a flagship model. It needs no config, daemon or store, sends nothing, and says so when a fix is already installed

## 0.100.0 - 2026-10-06

### Added

- **plans:** several sources per provider, tried in order (ADR 0011). Every plan window carries its `source`, `observed_at` and `stale`. Where the claude.ai meter and Claude Code's status line both report a window, the newer reading wins. A window from a polling source silent for more than 30 minutes is stale: the cards and the menu bar show its age in place of its pace, it raises no pace warning or alert, and a plan whose every window is stale raises the finding "Claude's plan reading is 20h old" with how to get it back
- **claude usage meter:** a pasted session falls back to the browser's session when claude.ai refuses it, as a browser-read one already did; `browser: none` turns that off
- **claude code sign-in:** an opt-in source of Claude's plan windows, `tokenops vendor-usage setup claude-code`, the way CodexBar reads them. It reads Claude Code's own OAuth token from `~/.claude/.credentials.json` or, on macOS with your leave, the Keychain; holds it in memory only; sends it only to `api.anthropic.com/api/oauth/usage`; and never refreshes it, since that would sign Claude Code out. It polls every 10 minutes, honours `Retry-After`, does not ask the Keychain again for six hours after a declined prompt, and its readings merge with the status line's and the meter's, newest first
- **codex app server:** a second source of Codex's plan windows, on by default: every 15 minutes TokenOps asks `codex -s read-only -a never app-server` for `account/rateLimits/read`, so the windows stay current between turns. Codex signs its own request; TokenOps reads no credential. The binary is found on PATH or where installers put it (`~/.local/bin`, Homebrew), since launchd's PATH hides them; `vendor_usage.codex_app_server.enabled: false` turns it off
- **menubar:** a plan whose every window has gone stale alerts once that its reading stopped, with the fix, and once when it is back
- **plans:** Claude's `iguana_necktie` window is its cloud credits, shown as "Cloud credits"

- **menubar:** alerts. A window notifies once as it drops to 20% left, to 5% and to used up, with its reset and when it runs out at this pace; one that alerted says so again when it resets. A spend limit alerts the same way. On by default; untick Alerts in the right-click menu to stop them

### Fixed

- **coach:** on a flat-rate plan the coach no longer falls back to dollars when it cannot read the plan's window. It said "$400.99 API-equivalent — 800% of the default $50 session ceiling, and not a charge… /compact" at 19% context; it now says once every six hours, across sessions, how old the plan reading is and how to get it back ("Claude's plan reading is 20h old… sign in to claude.ai in your browser, or run `tokenops vendor-usage setup claude-subscription`")
- **claude usage meter:** claude.ai answers an expired session with a 403 `account_session_invalid`, not a 401. It is now treated as an expired session, so the meter refreshes the session from the browser as it does for a 401, and says so; before, it logged a generic failure every five minutes and never tried the browser
- **sources:** a poller refused on every attempt for 15 minutes is `failing`, however many events the window still holds from before. The claude.ai meter was refused for 20 hours and reported `healthy` on the previous day's readings
- **glance:** a used-up window says "used up" (pace status `used_up`) instead of "out in" an empty duration or "runs out in 0m"; a window claude.ai reports under an internal codename is shown as "other limit", with the vendor's key in `vendor_label`

## 0.99.0 - 2026-10-06

### Added

- **menubar:** the panel reads like CodexBar's, in the Klarlabs palette: the account each plan's client is signed in with in the header, every window as a bar filled to the share left with a tick where an even pace would leave it, "N% in reserve" or "over pace" with "lasts until reset" or "runs out in", four figures (today, the last 30 days' cost and tokens, today's tokens), the last 30 days as a chart, the top model, and the coach. The icon is two rings for the busiest plan, its week outside and its session inside, each filled to the share left. `GET /api/glance` carries `accounts` (Claude Code's signed-in email; Codex keeps its account only inside its sign-in token, which is not read)
- **otel:** the daemon pushes derived metrics to an OTLP/HTTP collector every `otel.interval`: plan window utilization and pace, usage and its value per provider, session grades, the coach's findings and cost per commit. Figures only, checked against a stock OpenTelemetry Collector; `tokenops otel` prints every figure that would be pushed. Forwarding every event is now opt-in (`otel.events: true`); before, `otel.enabled` forwarded events and pushed no metrics
- **commits:** what each of your commits cost. `tokenops spend --by commit`, `GET /api/spend/commits` (subjects withheld) and `tokenops_spend` (view commits) join agent work to commits by place and time: a turn belongs to the repository its session ran in and to the next commit you made there within a day, across local branches, so a squash-merged change carries the work of its branch. Work after your last commit, with no commit within a day, outside a repository, or from a session whose directory is unknown is reported apart rather than dropped. Reads only the clients' own records and `git log`; nothing new is stored

### Changed

- **plans:** windows are shown as the share **left** everywhere a person reads them — the menu bar, `tokenops glance` cards and `--brief`, the statusline, the coach's findings and the MCP text — with bars that shrink as a window fills. The JSON keeps `used_pct`; context stays a used gauge

### Fixed

- **opencode:** opencode 2 showed "Plugin must export a default definition with an id and an effect or setup function" on every start, for the plugin tokenops writes for opencode 1.x. One file now serves both: its default export carries `server` for 1.x and `setup` for opencode 2, checked on opencode 1.18.32 and 2.0.24 with the hooks firing on each. The daemon brings an older generated plugin up to date when it starts, keeping its hooks, binary and budget and folding in the separate `tokenops-v2.js`, so an upgrade repairs it with no reinstall; a plugin tokenops did not write is never touched
- **analytics:** plan-window readings (the claude.ai usage meter, account readers) are stored as prompt events with no model and no tokens, and were counted as requests and listed as an unpriced Claude model — 2,189 of a month's requests here. Usage queries now count only events that carry usage; usage with tokens but no model still counts and still shows as unpriced
- **dx:** `GET /api/dx` and `tokenops_sessions` (view dx) answer the default week from the daemon's background analysis when it is under six hours old, as `tokenops dx` does, instead of reading every transcript per call
- **opencode:** opencode 1.18 creates `session_message` before it moves sessions to `session_v2`, and the reader joined `session_v2` whenever `session_message` existed, so the whole read failed and every opencode session was missing from spend, dx, story and coaching. It now joins whichever session table holds the directory; on this machine that brings back 54,149 messages

## 0.98.1 - 2026-10-05

v0.98.0 was tagged but its release build failed before publishing anything; 0.98.1 is that release.

### Fixed

- **release:** the macOS job built `TokenOps.app` in the wrong directory, so v0.98.0 published no binaries and no cask update (#544)

## 0.98.0 - 2026-10-05 (not published)

### Added

- **menubar:** the menu bar shows the icon alone, its ring filled to the busiest window; a click opens the panel with every plan's details, hovering lists each window's share and reset, and a right click has Refresh, Launch at Login and Quit
- **menubar:** the menu bar app ships in the macOS release and the Homebrew cask; `tokenops menubar` installs it to `~/Applications` and opens it, and upgrades refresh it. It is ad hoc signed, not notarized; the cask clears its quarantine flag as it does the CLI's

### Changed

- **cli:** every visible command has examples in its help, checked by a test that parses each one against the command tree. Identifier flags all end in `-id` (`--workflow-id`, `--agent-id`, `--session-id`, `--decision-id`); `scorecard --since-days` is `--days`; `verify --idle-gap` takes a duration like `story --idle-gap`
- **mcp:** the server sends instructions and a title: which tool to reach for first, how to bracket a task, and which tools change settings and need the operator's say
- **mcp:** sixteen tools instead of fifty, each answering one question and picking its slice with a `view`, `action` or `setting`: `tokenops_glance`, `tokenops_spend`, `tokenops_sessions`, `tokenops_status`, `tokenops_explain`, `tokenops_records`, `tokenops_rules`, `tokenops_fmt`, `tokenops_pricing`, `tokenops_prepare_work`, `tokenops_review_work`, `tokenops_outcome`, `tokenops_routing`, `tokenops_configure`, `tokenops_coach` and `tokenops_experiment`. Every tool now has a title, annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`), an output schema and a description for every parameter; a parameter that does not apply to the chosen view is refused with the ones that do. What an agent reads of the definitions — names, descriptions, input schemas — drops by about 40%. `tokenops_glance` gains the coach's findings as a view. The developer tools (`eval`, `replay`, `coverage_debt`, `rules_bench`) and `tokenops_help` left MCP; they remain CLI commands
- **cli:** `tokenops --help` groups commands by what you are doing (every day, set up, coach and routing, look closer), most used first, with a short start-here guide; every command's one-line description is rewritten in plain words. The hooks other programs call, the MCP server, the daemon's foreground process and TokenOps' own development tools are left out of the list but run as before, and the daemon overrides (`--listen`, `--tls`, …) are named once instead of under every command
- **cli:** `tokenops coach stats` replaces `coach-hook stats` and `read-guard stats`: both ledgers in one view (`--json` gives `budget` and `read_guard`). `tokenops explain` answers a decision's ID as well as a term, and `decision explain` is gone. The hook entry points (`coach-hook hook`, `read-guard hook`, `route-guard hook`) are unchanged, so installed hooks keep working
- **cli:** `tokenops --version`, and `--json` on every command that reads: `mode` (the API's `GET /api/mode`), `plan list` and `plan catalog`, `budget list`, `provider list`, `preferred-model list`, `routing proposals` and `routing rule list`, `daemon status`, `hooks status`, `pricing diff` and `pricing lint`, `optimizations`, `version`. A test holds every new read command to it
- **dx:** `tokenops dx` answers the default week from the daemon's background analysis when it is under six hours old — in under a second instead of minutes; `--fresh` reads the transcripts now

### Fixed

- **cli:** the global `-c/--config` now reaches every command that writes config. Commands that changed config read only their own `--config-path`, so `tokenops -c other.yaml plan set …` wrote to the default config while looking like it worked; `--config-path` and `fmt learn --config` are gone in favour of the one flag
- **mcp:** responses are bounded and structured. `tokenops_fmt` (learn) returned 190 KB and the audit view 139 KB, past what a client accepts from one call; learn now lists the top 20 per list with totals (`limit` to change it) and the audit view defaults to the 50 newest entries. `tokenops_spend` burn and `tokenops_glance` session_budget came back as text; they return their figures
- **menubar:** the icon appears at once instead of after the first daemon read, which on a busy machine could take minutes

## 0.97.0 - 2026-10-04

### Changed

- **cli:** `tokenops glance` cards are laid out like CodexBar's: vendor, source and plan; each window's share, a full-width bar, its reset and its pace; then spend, credit and cost today and over 30 days, with unpriced usage marked. `--brief` gains a pace column and both list the busiest plan first. The glance-wide insight no longer heads every card (#531)

### Added

- **findings:** the coach's findings under the glance cards, in the menu bar panel and at `GET /api/findings`: a window that runs out before its reset and a plan with room for the work, files re-read unchanged, sessions past their budget, the session analysis's one recommended change, unreadable session sources, advice the coach went quiet on, and what an observing coach would have said. The daemon runs the session analysis in the background every three hours. `tokenops glance --findings` lists them all (#532)
- **plans:** every vendor window carries its pace (`pace` in the glance and headroom payloads): on pace, behind and lasting to the reset, or ahead and when it runs out. The CLI and the menu bar show the same answer (#531)

## 0.96.0 - 2026-10-04

### Added

- **menubar:** a modern panel in the Klarlabs palette: a tab per plan, each window with its reset and pace (when it runs out at this rate), extra usage, credit left, and cost today and over 30 days; usage on models without a list price is left out of the money and the panel says how much (#528)

### Fixed

- **pricing:** the models Codex runs on are priced — `gpt-6.1-sol`, `gpt-5.6-sol` (promotional, through at least 2026-11-21), `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`, `gpt-5.5-pro` — from OpenAI's Standard table, verified 2026-10-04. Most Codex requests were unpriced and its cost read near zero; it applies to past usage too

## 0.95.0 - 2026-10-04

### Added

- **cli:** `tokenops glance` draws every plan as terminal cards: a gradient bar for each window the vendor reports with its reset, spend against a limit, credit left, busiest first, laid out to fit the terminal. Bare `tokenops` shows it once set up. `--brief`, `--json`, `--color` (#526)
- **vendorusage:** Gemini CLI's chat recordings are read for each model turn's tokens; init turns the reader on where Gemini sessions exist (#523)
- **menubar:** a menu bar app on Vitra (`apps/menubar`, built from source for now): the busiest window next to the icon and a panel with every plan, from the daemon API (#525)

### Fixed

- **headroom:** one store read per answer instead of nineteen; `GET /api/glance` took up to two minutes in a daemon busy ingesting, and now answers in a fraction of a second (#524)

## 0.94.0 - 2026-10-03

### Added

- **init:** Plans are bound from what the clients report (Claude Code's account tier, Codex's `plan_type`), shown with their evidence and never over a plan you bound. In a terminal, init then asks only what nothing on the machine could answer: an ambiguous plan, what you pay, a spend limit nothing reports, the coach preset, and whether to supervise the daemon. `--yes` asks nothing; running out of input is never consent (#520)
- **routers:** Where an external router chooses the model (FireRouter, OpenRouter Auto), TokenOps stands down: the route and subagent guards leave those turns alone, routing advice answers `stay` with `decided_by`, and the coach names the router (#521)

## 0.93.0 - 2026-10-03

### Added

- **accounts:** Coding plans show the windows the vendor reports, read with the key the harness already sends it: z.ai GLM Coding Plan, Kimi Code, MiniMax Token Plan, Synthetic and Chutes. A provider that reports a plan but has none bound shows as `subscription`. DeepInfra and Vercel AI Gateway report balance and spend (#517)
- **accounts:** Gateways a harness points at are recognised by their health route, without the key, and then read for the key's own budget at that same address: LiteLLM, Bifrost and ClawRouter (#518)

## 0.92.0 - 2026-10-03

### Added

- **headroom:** Claude's 5-hour and weekly windows, and a Claude apps gateway's spend limit, come from Claude Code's own status line, with no claude.ai login; merged per window with the usage meter (#507)
- **api:** The daemon API serves every surface that is not an agent (ADR 0010): glance, plan headroom and session budget (#508); status, mode, coach, config, data sources, vendor usage (#509); agent DX, story and prompt scoring with the operator's words withheld (#510); top consumers, burn rate, scorecard, pricing (#511); routing proposals and decision explanations (#512)
- **api:** Changes through the API: mode, budgets, routing rules, plans (#513); preferred model, routing decisions, outcomes, coach (#514). Token-only, JSON-only, unknown fields refused, audited as `config_change` by `api`
- **api:** An OpenAPI 3.1 contract generated from the handlers (`docs/api/openapi.json`) and a Daemon API guide page, both held to the served routes by tests (#515)

## 0.91.0 - 2026-10-03

### Added

- **headroom:** Every window the vendor reports, as shares used with their reset: Claude's 5-hour, weekly and model-scoped weekly, Codex's windows; the busiest sets the risk (#503)

### Fixed

- **headroom:** Windows reported as a percentage with no message cap (Codex) are shown, with readable reset times (#502)
- **budget:** The session budget advises from the busiest vendor window, and no message count is derived from a percentage (#504)

## 0.90.0 - 2026-10-03

### Added

- **headroom:** Every provider billed per token this month shows as pay-as-you-go without setup, its rows named by provider (#499)
- **spend:** OpenRouter's key spend and cap, and DeepSeek's and Moonshot's prepaid balance, read with the keys the harnesses already use (#500)

### Fixed

- **headroom:** Pay-as-you-go counts every turn an API key was billed for, through any endpoint (#499)

## 0.89.0 - 2026-10-03

### Added

- **explain:** `tokenops explain [term]` and the `tokenops_explain` MCP tool say what each figure means, how it is worked out and how to read it (#495)
- **spend:** The Fireworks account's spend and limit are read without setup, and headroom shows Fireworks as pay-as-you-go (#496)

### Fixed

- **statusline:** Install and uninstall say what happened in plain words, not the wrapped command's paths (#494)
- **dx:** Cursor is no longer reported as unreadable when none of its sessions fall inside the window (#494)
- **storage:** Headroom, the scorecard and the window probe read the whole range, not the oldest 100,000 events (#497)

## 0.88.1 - 2026-10-02

### Fixed

- **dx:** Read Cursor messages whose createdAt is a string, as current Cursor builds write it; every bubble was read as an unknown schema (#493)

## 0.88.0 - 2026-10-02

### Added

- **opencode:** Read opencode 2.x's store alongside 1.x's, each message once (#488)
- **opencode:** A plugin for opencode 2 (read-guard, route-guard), next to the 1.x one (#489)
- **statusline:** TokenOps' line under Claude Code's prompt, in Klarlabs colours, with subagent rows; `init` installs it, wrapping an existing status line (#490)

### Fixed

- **statusline:** An uninstall is remembered, so `init` does not put it back (#491)

## 0.87.0 - 2026-10-01

### Added

- **attribution:** A dated route history decides each Claude Code turn's endpoint and plan; a plan covers only its vendor's own endpoint (#479)
- **attribution:** Codex sessions on a custom model_provider are that provider's (#480)
- **providers:** z.ai, opencode Go and Zen, Kimi, MiniMax, Alibaba, DeepSeek, Cerebras, Synthetic and Chutes: endpoints, coding plans and prices (#481)
- **init:** Turn on the local transcript readers whose sessions are present (#482)
- **plans:** Spend limits are dated, and any provider can have one (`pay-as-you-go`) (#483)
- **mcp:** The spend summary shows the operator's currency and its rate (#484)
- **docs:** ADR 0009 accepted with part 1 as built; gateways in the guides (#486)

### Fixed

- **attribution:** Claude Code turns a gateway served are billed by the gateway (#477)
- **pricing:** Gateway turns are priced at the gateway's own rate, from models.dev and pinned Fireworks rows (#478)
- **init,spend:** `--no-wire` turns readers on; no value-per-plan ratio over cents (#485)

## 0.86.2 - 2026-10-01

### Fixed

- **plans:** The daemon corrects usage recorded as covered at $0 under a plan billed at API rates on its own, at start, and audits each correction as `cost_correction`; no command needed (#476)

## 0.86.1 - 2026-10-01

### Added

- **plans:** ChatGPT Pro is $100, $200 or $500 (`gpt-pro-500` added), and headroom flags a binding the plan type Codex reports contradicts (#470)
- **docs:** ADR 0009, endpoints, routers, and billers (#471)

### Fixed

- **plans:** Usage-based Enterprise is billed at API rates, not covered by its plan: real cost, the spend limit and signal quality were reading $0, 0% and low (#472)
- **dx:** Read every client a store failure does not affect; the MCP dx and story tools return partial results with warnings instead of "internal error" (#473)
- **docs:** README, CLI reference and changelog caught up to v0.86.0 (#469)

## 0.86.0 - 2026-10-01

### Added

- **money:** Show every total in your currency, with the rate it used (#468)

## 0.85.0 - 2026-10-01

### Added

- **plans:** Record what you pay, in your currency (ADR 0008) (#467)
- **plans:** Plan history, backdated switches, and plan prices (ADR 0008) (#466)
- **dx:** Split instructions by model and reasoning effort (#465)

## 0.84.0 - 2026-09-30

### Added

- **routing:** Move requests off models the policy rules out (ADR 0007) (#464)
- **routing:** Model policy rules models out of every route (#463)

## 0.83.0 - 2026-09-30

### Added

- **coach:** Presets set the whole coach and wire every agent (#462)
- **coach:** Context power sets where every agent compacts (ADR 0006) (#461)

## 0.82.0 - 2026-09-29

### Added

- **coach:** Show the live quota window and the next tip in coach status (#459)

## 0.81.0 - 2026-09-29

### Added

- **coach:** Live compact tip after a long stretch near the ceiling (#458)

## 0.80.0 - 2026-09-29

### Added

- **coach:** Models ask proposes subagent moves for approval (ADR 0006) (#455)
- **waste:** Compact_earlier finding names the cost of compacting late (ADR 0005) (#453)

### Fixed

- **route-guard:** Rank the session's own model with the configured list (#456)

## 0.79.0 - 2026-09-28

### Added

- **coach:** Record tip follow-through and quiet ignored early tips (#452)
- **coach:** Record follow-through and quiet ignored advice (ADR 0006 step 5) (#451)

## 0.78.0 - 2026-09-28

### Added

- **coach:** Models autonomous moves subagents to a cheaper model (ADR 0006 step 4) (#449)
- **coach:** Verbosity levels change what the coach says (#448)

## 0.77.0 - 2026-09-28

### Added

- **coach:** One coach with autonomy and verbosity dials (ADR 0006, step 2) (#447)

### Fixed

- **authority:** Report the read guard from coaching.delivery (#444)

## 0.76.0 - 2026-09-27

### Added

- **coach:** Speak quota, not dollars, on flat-rate plans (#443)

### Fixed

- **test:** Sandbox XDG directories in CLI tests and the config matrix (#442)

## 0.75.1 - 2026-09-27

### Fixed

- **mcp,eval:** Readable tool errors, embedded eval suites, MCP tools in the config matrix (#440)
- **coaching:** Stop waste findings that measure session length (#438)

## 0.75.0 - 2026-09-27

### Added

- **storage:** Log slow write-lock waits and holds per process (#436)

## 0.74.3 - 2026-09-27

### Fixed

- **config:** Reject unknown smart-routing interventions (#434)
- **daemon:** Require the API token when storage is disabled (#433)

## 0.74.2 - 2026-09-27

### Fixed

- **daemon:** Skip stored events when pollers re-read history (#432)

## 0.74.1 - 2026-09-27

### Fixed

- **events:** Stop losing rows to store lock contention (#431)

## 0.74.0 - 2026-09-27

### Added

- **workflows:** Propagate prepared IDs through Claude bridge (#427)

### Fixed

- **proxy:** Ignore auxiliary provider requests (#428)

## 0.73.1 - 2026-09-27

### Fixed

- **daemon:** Confirm health after supervised restart (#423)

## 0.73.0 - 2026-09-27

### Added

- **mcp:** Connect work preparation and review (#422)

## 0.72.5 - 2026-09-27

### Fixed

- **plans:** Harden Claude subscription polling (#421)

## 0.72.4 - 2026-09-27

### Fixed

- **telemetry:** Harden Claude request import (#420)
- **release:** Bypass unsafe Relicta publish (#419)

## 0.72.3 - 2026-09-27

### Fixed

- **release:** Disable Relicta native tagging (#418)

## 0.72.2 - 2026-09-27

### Fixed

- **release:** Guard signed Relicta publishing (#417)
- **plans:** Standardize Claude subscription naming (#416)
- **plans:** Add portable Claude meter setup (#415)
- **plans:** Decode Claude unified limits (#414)
- **plans:** Preserve Claude quota window labels (#413)
- **plans:** Honor vendor-reported quota windows (#411)

## 0.72.1 - 2026-09-26

### Fixed

- **proxy:** Preserve incompatible Anthropic routes (#408)
- **proxy:** Observe compressed provider responses (#407)
- **proxy:** Classify sanitized upstream errors (#405)

## 0.72.0 - 2026-09-26

### Added

- **proxy:** Measure streamed agent traffic (#402)

### Fixed

- **experiments:** Preserve multi-call evidence (#403)

## 0.71.0 - 2026-09-26

This release completes the source and live-validation work for ADR 0004. It
adds evidence-bounded guidance across the CLI and MCP surfaces, proves a
bounded randomized routing cohort against metered OpenAI traffic, and tightens
the attribution and verification rules that keep optimization claims honest.

### Added

- **Work-aware CLI and MCP guidance.** Status, review, preparation, resource
  glance, and work-insight surfaces now compose the same evidence-aware control
  loop instead of presenting disconnected counters. (#380–#386)
- **Outcome-gated adaptive routing.** Experiments bind randomized arms to full
  executions, compare only complete pairs, account for reported human
  attention, and require trusted outcomes before autonomous route learning.
  (#352–#357)
- **Canonical work and event correlation.** Native domain envelopes preserve
  actor, work, execution, intervention, and outcome relationships through the
  durable event store. Legacy history can be imported without retaining a
  second event-writing path. (#358–#369)
- **Content-safe JSON outcome verification.** `tokenops outcome check-json`
  records whether a response matches an expected JSON value without retaining
  the response body. (#399)

### Changed

- **The daemon runtime is split into supervised lifecycle components.** Event,
  retention, vendor-usage, pricing, read-guard, spend, and window-probe workers
  now report their own health instead of disappearing behind one process-level
  status. (#370–#378)
- **Subscription names match vendor product names.** OpenAI plans use
  `gpt-plus`, `gpt-pro`, `gpt-pro-5x`, `gpt-pro-20x`, and `gpt-business`;
  Anthropic plans use `claude-pro`, `claude-max-5x`, `claude-max-20x`, Team,
  and Enterprise variants. Existing unambiguous spellings remain deprecated
  aliases. (#399)
- **Background learning is more conservative.** Formatter projections remain
  distinct from observed recovery evidence, and the Phase 5 decision loop
  learns utility only from explicit outcomes. (#390–#391)

### Fixed

- **Claude Opus 5.5 is priced independently from Opus 5.** Its verified
  $4/$20/$0.20 input/output/cache-read rates and 5% cache-read ratio no longer
  inherit the older Opus 5 price.
- **GPT-6 Sol and Luna use verified pricing.** Exact input, cached-input, and
  output rates are available to metered routing comparisons. (#399)
- **Provider-reported response usage wins over envelope estimates.**
  Non-streaming responses now attribute the authoritative model and token
  counts returned by the provider. (#399)
- **Randomized trials reject incomplete or failed evidence.** Verification
  excludes HTTP failures, partially applied route arms, and executions that
  cannot be durably tied to their randomized assignment. (#394–#396)
- **Synthetic proxy execution windows include their initiating prompt.** This
  closes the boundary gap that previously started an execution just after its
  prompt observation. (#399)
- **Plan-cost warnings distinguish unknown cost from zero spend.** Aggregates
  no longer repeat provenance caveats or present unpriced plan usage as a
  complete API-equivalent total. (#388–#389)

## 0.70.0 - 2026-09-20

The first five phases of ADR 0004 — TokenOps as an AI-work intelligence
and control system. Four of them close cases where TokenOps reported a
number, or a clean bill of health, that it had no basis for.

### Security

- **The `tokenops fmt` recovery store is no longer world-readable.** It
  holds the full raw stdout and stderr of every command it wraps, which
  is the broadest capture surface in the product, at `0644` in a `0755`
  directory, unpruned, for as long as the machine lives. A wrapped
  `printenv`, a verbose `curl` or a failing deploy script left its
  credentials there permanently. Now `0600` in a `0700` directory, with
  credential shapes redacted, pruned at 14 days. The learning index
  beside it and `domain-events.jsonl` were `0644` for the same reason
  and are now `0600`. (#339)
- **`config.yaml` gets its permissions repaired on every write.**
  `os.WriteFile` applies its mode only when it creates a file, so a
  config that predates the `0600` default kept its old mode while
  `vendor-usage setup` wrote new vendor credentials into it. All four
  paths now tighten what they find, not only what they create — the
  files already exist on every installed machine. (#339)
- **Credentials can be kept out of `config.yaml`.** `SECURITY.md` has
  said since v0.10.2 to use "environment substitution" for the Anthropic
  admin key. No code implemented it. All five credentials now read from
  the environment at load time, win over the file, and are never written
  back; `SECURITY.md` records the correction. (#339)

### Added

- **Reported numbers carry their provenance.** A new measurement type
  says whether a figure was observed, derived or estimated, and how much
  of the population it accounts for. Unknown no longer reads as zero.
  (#340)
- **Per-source ingestion health, as data.** `GET /api/sources` and the
  `tokenops_data_sources` MCP tool now report, per source, when it was
  last seen, when its reader last polled successfully, and what it last
  failed with. (#341)
- **Work, Actor, Execution and Outcome.** The domain-neutral primitives
  TokenOps had no representation of — Outcome had no non-test match
  anywhere in the tree. Without them TokenOps can only optimize what work
  consumes, with no way to notice it made the work worse. (#342)

### Fixed

- **`plan headroom` and `plan list` no longer reshuffle between runs.**
  The MCP tool sorted the configured providers; the CLI ranged the map,
  and Go randomises map iteration, so "the first plan" was a different
  plan from run to run. Both now call one implementation. (#343)
- **An unknown plan name is reported rather than skipped.** Both surfaces
  dropped it silently, which is how a typo becomes a plan that reports
  nothing and says nothing, forever. (#343)
- **A cost total no longer hides the models it could not price.** An
  unpriced model contributed `0` to the rollups feeding burn rate,
  forecast, top consumers and the dashboard, and the total presented
  itself as complete. `Summarize` reported that gap; `AggregateBy`, which
  those surfaces call, did not. (#340)
- **A proxy with no tokenizer no longer reports zero usage as though it
  counted.** Events now carry a token source, so zeros nobody produced
  are distinguishable from a request that consumed nothing. (#340)
- **An optimizer that cannot measure its own effect says so.** Token
  savings collapsed three outcomes into one integer: a counted saving, a
  counted no-saving, and a failure to count. The third returned `0` —
  indistinguishable from "no effect", with the confidence of a
  measurement. (#340)
- **A refused poller is distinguishable from an unused vendor.** Both
  produce no events. All four pollers tracked their last error behind a
  method with no call sites, and none recorded a success, so nothing
  could tell an operator which situation they were in. (#341)

### Changed

- **Adapters are ratcheted toward a capability layer.** `internal/cli`
  and `internal/mcp` each reached straight into the domain and each
  implemented their own orchestration, which produced divergence rather
  than duplication. An architecture test now records every direct
  adapter → domain import and fails on any new one, so the list can only
  shrink. (#343)

### Notes

- `GET /api/sources` is credentialed like every other `/api` route.
- No schema migration. The new event field is omitted when it carries no
  information, and events written before it existed read back as counted
  — which is what they were.

## 0.69.0 - 2026-09-20

### Security

- **Every `/api` route is behind the dashboard token now.** `/api/audit`,
  `/api/rules/analyze`, `/api/rules/conflicts`, `/api/rules/compress`,
  `/api/rules/inject` and `/api/domain-events` answered without a
  credential even when dashboard auth was configured. They were mounted
  beside the authenticated routes rather than inside them, and Go's
  router prefers an exact path over the `/api/` pattern the middleware
  wrapped, so the middleware never saw them. The daemon binds to
  loopback by default, which is what kept this local; a daemon bound to
  a LAN address served its own audit log to the network. `/healthz`,
  `/readyz` and `/version` stay open, as probes must. (#336)

### Changed

- **`tokenops events` and the `tokenops_domain_events` MCP tool now send
  the dashboard token.** They read it from the daemon's own URL hint, so
  nothing is asked of you. If you call `/api/domain-events` from a script
  of your own, it now needs `Authorization: Bearer <token>` or
  `?token=…`; `tokenops dashboard rotate-token` prints a fresh one. (#336)

### Fixed

- **The usage meter says when Anthropic's bot check refused it.** A
  refusal was reported as "no usage limits for this account", which sent
  you to look at your plan instead of at the request. It now names the
  fix: open claude.ai once in the browser the session came from, which
  renews the `cf_clearance` cookie. That cookie is tied to the browser's
  user-agent, so the two are sent together, and the daemon re-reads both
  from the browser when a request is refused rather than going quiet
  until you notice. (#336)
- **The keychain prompt waits as long as a person needs.** The daemon
  gives up after 15 seconds, because nobody is watching a prompt it
  raises; an interactive setup now waits three minutes. A single timeout
  had been cutting off the prompt the operator was reading. (#336)

### Notes

- The dashboard is unaffected: it authenticates with the session cookie
  it already exchanges the token for on first open.

## 0.68.1 - 2026-09-20

### Fixed

- **Setup no longer asks which organization to meter.** After reading the
  session from the browser, an account with more than one organization —
  a personal one beside a Console or Enterprise one — was asked to pick,
  which is a question the data already answers: only some organizations
  report usage at all. It now connects the one that does, says so when
  there were several, and takes `--org` to force another. A run with no
  terminal (an agent, a script) used to stop at that prompt. (#335)

## 0.68.0 - 2026-09-20

### Added

- **The Claude usage meter connects without a paste.** `tokenops
  vendor-usage setup claude-usage-meter` now reads the claude.ai session
  from the browser you are signed in with — Chrome, Arc, Brave, Edge,
  Chromium or Firefox. On macOS the system asks you to allow reading that
  browser's keychain entry, and allowing it is the whole interaction:
  nothing to find in developer tools, nothing to copy, nothing typed.
  `--browser <name>` picks one; `--paste` keeps the old prompt for a
  machine with no browser to read. (#333)
- **`tokenops_vendor_usage_setup` reads the same session**, so an agent
  can connect the meter without a login passing through the conversation.
  It reports which browser the session came from and never the key
  itself. (#333)

### Notes

- What the reader does: exactly the one cookie it is asked for, for
  claude.ai only. It never lists other cookies, works on a copy of the
  store so your browser can stay open, never writes to the browser
  profile, and copies the write-ahead file so a session from a sign-in
  moments ago is not missed. Declining the macOS prompt is reported as a
  refusal, not as "no session found".
- Safari keeps cookies in its own format behind macOS privacy controls
  and is not supported; use `--paste` there.

## 0.67.1 - 2026-09-20

Three answers that were confidently wrong, found by testing 0.67.0 against
real data rather than fixtures.

### Fixed

- **"Resets in 5h0m0s" was the window length, counted from whenever you
  asked.** A rolling window opens with its first message, so a reset
  nobody reported is now estimated from the first activity inside the
  window and marked as an estimate. An idle window reports no reset at
  all, and a reset Anthropic does report stays exact. (#331)
- **A Codex plan was graded on Claude Code's transcripts.** Headroom for
  `codex-plus` carried "Reads ~/.claude/projects" as its evidence, and its
  confidence came from data that says nothing about Codex. Each provider
  is graded by the sources that report on it; the proxy and MCP activity
  count for all of them. (#331)
- **Domain-event counts had no date.** The daemon replays its stored log
  at startup, so its counts are lifetime totals: 274 budget alerts from a
  budget deleted in June read as 274 happening now.
  `tokenops_domain_events` now reports when each kind first and last
  happened, and says to read that before treating a count as current.
  (#331)

## 0.67.0 - 2026-09-19

A full review of the MCP surface — the 41 tools an agent reaches TokenOps
through — found places where an agent was told something wrong, told to do
something harmful, or could quietly undo what the operator had set. This
release fixes all of them.

### Added

- **An out-of-date MCP server says so.** An MCP client starts its own
  `tokenops serve` and keeps it until the client restarts, so after an
  upgrade an agent can go on talking to the old version — on the machine
  that found this, a server from August, with every release since
  invisible to the agent. `tokenops_status` and `tokenops_version` now
  report when the running server is no longer the installed binary, and
  say to restart the MCP client. They also show the daemon's version
  beside the server's. (#324)
- **`tokenops_plan_set` and `tokenops_vendor_usage_setup`**: bind a plan
  and connect the Claude usage meter from an agent. The meter tool never
  takes the claude.ai session key as an argument — it is a login, and a
  tool argument lands in the agent's transcript. (#323)

### Fixed

- **An agent editing a budget could erase its ceiling.**
  `tokenops_budget_set` had no token limit and rebuilt the whole budget on
  every edit, so changing a token budget's warning threshold removed its
  limit and left a budget that could never fire. Edits now change only the
  fields given, token limits are supported, and a budget without a ceiling
  is refused — the same rule, and the same code, as `tokenops budget set`.
  (#326)
- **A healthy daemon was reported as missing.** The MCP server found the
  daemon only through a file the daemon writes at startup, and TokenOps'
  own test suite deleted that file on every run. The MCP server now also
  checks the configured address; a daemon only removes that file if it
  wrote it; and the tests no longer touch the real home directory. (#325,
  #326)
- **`tokenops_mode` could start a second daemon.** Switching to active
  mode started a new daemon whenever it did not see the running one, and
  when it did see it, told the operator to run `tokenops start` — which
  also starts a second one. It now restarts the supervised daemon, and
  only ever starts one where nothing is supervised and nothing answers.
  Several hints that recommended `tokenops start` now name the command
  that fits the machine. (#326)
- **Agent tools read the config as it was when they started.** After an
  agent changed a plan or connected the meter, `tokenops_config`,
  `tokenops_vendor_usage_status` and parts of `tokenops_status` went on
  reporting the old state. They now read it at the time of the call.
  (#324)
- **`tokenops_domain_events` always answered zero.** The counts live in
  the daemon, not the MCP server. It now asks the daemon, and says so
  plainly when no daemon answers. (#324)
- **Spend tools ignored subscriptions.** On a flat-rate plan,
  `tokenops_burn_rate` reported $0.00 for billions of tokens and
  `tokenops_top_consumers` ranked models by a cost that was zero for all of
  them. Both now lead with tokens and the API-equivalent value, as
  `tokenops spend` already did. (#327)
- **`tokenops_routing_advise` priced with the rate card built into the
  binary** rather than the daily-refreshed one it claimed to use. (#327)
- **`tokenops_help` listed 23 of 41 tools.** It lists all of them, and a
  test fails when a tool is added without an entry. (#327)
- **`tokenops_session_budget`** explains a plan without a rate-limit window
  (Claude Enterprise) instead of returning nothing, and shows the same plan
  every time rather than whichever a map iteration picked. (#327)
- **`days: 0` promised all history and gave seven days** in
  `tokenops_story` and `tokenops_agent_dx`. `all: true` reads all history.
  (#327)
- **Routing-rule errors name the argument**, and blank values are refused.
  (#327)
- **Daily and per-model cost breakdowns were wrong when events were stored
  without a cost.** A day only re-priced its first hour, and in a
  per-model breakdown each unpriced model was charged for every model in
  the bucket — $13.25 against $0.75 of real use in the test. Rows now add
  up to the total for the same window. (#328)
- **Settings an agent changes now apply without restarting its client**:
  which provider activity is attributed to after a plan is bound, waste
  thresholds, the replay pipeline and the coach tool's transcript folder.
  (#328)
- **No advice starts a second daemon.** Warnings that suggested
  `tokenops start` now name `tokenops daemon restart` or `tokenops daemon
  install`; `tokenops status` names the one that fits the machine. (#328)
- **The dashboard link keeps its auth token** when the daemon's address
  file is missing. (#328)
- **Agents can see why a call was refused.** Every refusal the tools
  explain — a budget without a ceiling, the Enterprise spend-limit rule, a
  bad `since` — reached the agent as "internal error", because the MCP
  library masks errors it is not told are the caller's to fix. They now
  arrive as tool errors the agent can read and correct; failures inside
  TokenOps stay masked. A rules tool pointed at a folder that does not
  exist is refused rather than reporting an empty result. (#329)

## 0.66.0 - 2026-09-19

The Claude usage meter reads what claude.ai actually sends. It was written
against a response shape nobody had observed, and its tests were built from
the same guess, so they could only agree with it. Checked against real
responses from a Claude Enterprise, a Claude Max and a personal chat-only
account, it had never read one correctly.

### Added

- **Claude Enterprise spend, from Anthropic.** On Enterprise, claude.ai
  reports what you have spent this month and your spend limit. Headroom now
  uses those figures for a spend-billed plan instead of estimating spend
  from token counts against a limit you typed in — no admin key needed.
  `plan headroom` says which you are looking at: spend *reported by the
  vendor*, or *estimated from token counts*. (#320)
- **`plan set anthropic claude-enterprise` needs no `--spend-limit`** when
  the meter is set up, because Anthropic supplies the limit. Without the
  meter, the typed limit still works as before. (#320)

### Fixed

- **Spend was never read.** The meter looked for field names the response
  does not have; the one reading ever stored on a real install was all
  zeros. (#320)
- **A window the plan does not have is no longer "0% used".** Enterprise
  has no 5-hour or weekly window; the meter turned those into 0% and
  headroom would have trusted it as Anthropic's own figure. Only what
  Anthropic reports is recorded now, and a reply the meter cannot read is
  logged and skipped instead of stored as zeros. (#320)
- **Readings no longer freeze.** Only the first reading after each 5-hour
  reset was ever kept — and on Enterprise, only the first reading ever — so
  headroom showed usage as it stood minutes after the reset. Every change
  is now kept. (#320)
- **The terminal and your agent give the same headroom.** `plan headroom`
  never used the vendor's window percentages, and the
  `tokenops_plan_headroom` tool never read the Enterprise spend limit, so
  an agent was told no limit was configured when one was. Both now build
  their report the same way. (#320)
- **The right organization is metered.** An account can hold several — an
  Enterprise org beside a personal or API one — and the meter took whichever
  came first. It now takes the one that reports usage. Setup shows only what
  Anthropic reported, and refuses an organization with nothing to meter.
  (#320)

### Notes

- The field names are confirmed against real responses on all three account
  types, and those responses are the test fixtures. (#321)
- Enterprise spend is used only in USD and only for the current month; a
  reading from before the month rolled over described a limit that has
  since reset.

## 0.65.4 - 2026-09-19

Reported from a real upgrade. `brew upgrade` told the operator to run
`tokenops daemon install`, which stopped the running daemon, failed to start
the new one with `Bootstrap failed: 5: Input/output error`, printed launchctl
commands to type — and exited 0, with nothing ingesting.

### Fixed

- **Upgrades restart the daemon by themselves.** Where the daemon is
  installed, the Homebrew cask now re-applies it after every upgrade, so it
  comes up on the new version with no command to run. The install notice
  says `tokenops daemon install` is for the first install only. (#318)
- **`tokenops daemon install` works over a running daemon.** launchd keeps
  hold of the old job for a moment after it is told to unload it, and
  starting the new one in that moment is what failed. An unchanged unit is
  now restarted in place, which never unloads at all; a changed one waits
  for launchd to let go, and retries while launchd still refuses. (#318)
- **On Linux, an upgrade runs the new binary.** `systemctl enable --now`
  does not restart a daemon that is already running; install now restarts
  it. (#318)
- **A daemon that cannot be started is reported as a failure.** The command
  exits non-zero and says to run `tokenops daemon install` again, instead of
  printing supervisor commands and exiting 0. (#318)

### Notes

- This release is the last one where upgrading needs a command: the fix
  runs after the upgrade that installs it, so a daemon still stopped from
  an earlier failed install starts during this upgrade.

## 0.65.3 - 2026-09-19

0.65.2 moved the first retention pass off the startup replay. The first
delayed pass showed that was not enough: ordinary writes landing during it
failed three attempts in a row and cleared only once it finished.

### Fixed

- **Retention reclaims space in small chunks instead of rewriting the
  database.** With `reclaim: true`, every pass that deleted anything ran a
  full VACUUM — the whole database rewritten under the write lock, 27–31
  seconds on a 440 MB store, to hand back ~400 KB. Stores now use SQLite's
  incremental auto-vacuum, and reclaim frees pages 256 at a time, each step
  its own short write. On a copy of that store, freeing 443 pages took
  0.295s and 0.004s. (#316)

### Notes

- A store created before this release pays for one more full VACUUM, on the
  first reclaim after upgrading, which converts it. The log shows
  `mode=convert` for that pass and `mode=incremental` after. The 5-minute
  startup delay from 0.65.2 keeps that one conversion off the startup
  replay.
- New stores are created incremental and never need the conversion.

## 0.65.2 - 2026-09-19

Found while verifying 0.65.1 on the machine that reported the CPU problem.

### Fixed

- **The first retention pass no longer runs on top of the startup
  replay.** When the daemon starts, every reader replays its history into
  the store, and retention pruned at the same moment. With `reclaim: true`
  the prune runs a VACUUM that holds the write lock throughout — 31
  seconds on a 440 MB store — and the replay's writes failed four times in
  a row, clearing on their fifth and last attempt. One more and 64 events
  would have been counted as lost. The first pass now waits five minutes;
  later passes keep their interval. (#314)

### Notes

- The same retries appear around every restart in older logs. The CPU load
  fixed in 0.65.1 was hiding them.

## 0.65.1 - 2026-09-19

The daemon held most of a CPU core indefinitely. On one real install: 17
hours of CPU in 14.5 hours of uptime, and event-store writes timing out
behind it.

A profile of the live daemon put all of it in one place. Every 30 seconds,
the Claude Code, Codex and opencode readers re-read everything they ingest
from the beginning — there, 1.4 GB of Claude Code transcripts (16 of 1,769
files had changed in the past hour) and a 1.3 GB opencode database last
written in July. The more you used your agents, the more the tool watching
them cost.

### Fixed

- **Transcripts are read incrementally.** Each file's read position is
  remembered, so a scan parses only what was appended since the last one,
  and a file that has not changed is not opened. On the same machine and
  data, steady-state CPU went from ~32% to ~1.3%. The first scan after the
  daemon starts still reads everything once. (#312)
- **opencode is read only when its database changes.** No change means no
  new rows, so there is nothing to read. (#312)
- **An oversized transcript line no longer hides the rest of the file.** A
  line over 4 MB — a large tool result — ended the read of that transcript,
  so every turn after it was never ingested. It is now skipped on its own.
  (#312)

### Notes

- A line still being written when a scan lands is left for the next scan
  rather than read as a fragment. A file that shrank was replaced, and is
  read again from the start.

## 0.65.0 - 2026-09-19

A breaking rename, released on its own so the notice cannot be missed.

### ⚠️ Breaking

- **`anthropic-cookie` is now `claude-usage-meter`.** The old name described
  how the data was fetched — a browser cookie — rather than what it gives
  you: Anthropic's own 5-hour and 7-day utilisation percentages, the one
  Claude source that is not estimated from transcripts.

  | | before | after |
  |---|---|---|
  | config | `vendor_usage.anthropic_cookie` | `vendor_usage.claude_usage_meter` |
  | source tag | `anthropic-cookie` | `claude-usage-meter` |
  | command | `vendor-usage setup anthropic-cookie` | `vendor-usage setup claude-usage-meter` |
  | env | `TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY` | `TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY` |

  **What you need to do:** if your config contains `anthropic_cookie`,
  rename that key to `claude_usage_meter` — its contents are unchanged. A
  config still using the old key is refused at load with a message saying
  exactly this. It is refused rather than tolerated because a renamed YAML
  key is otherwise silently ignored, and the source would simply stop
  running with nothing to say why.

  Events already stored under the old tag keep it and are no longer counted
  in `vendor-usage status` or matched by retention rules. Headroom is
  unaffected: it reads the utilisation on each event, not the source tag.
  (#308)

### Documentation

- **ADR 0003 — vendor-reported cost outranks recomputed cost.** Both
  Anthropic and OpenAI publish what they bill, behind an admin key, and
  TokenOps recomputes it from a public rate card instead. The ADR applies
  the rule already used for rate limits to money. (#309)

  It is **scoped to the team plane**: an admin key belongs to whoever
  administers the organization, and an individual developer — the operator
  TokenOps is built for today — will not be issued one. When a team plane
  exists, the administrator who deploys it supplies the key. (#310)

## 0.64.0 - 2026-09-19

TokenOps has two surfaces, and asking each of them the same question gave
different answers. A parity check added in 0.62.0 wrote down which: eight
capabilities existed on one side only, some deliberately and some because
nobody had noticed. This closes all eight.

The sharp ones ran the wrong way. `mode`, `preferred_model`, `budget_set`
and `routing_rule_set` were writable through the MCP server and
unreachable from a terminal, where `tokenops config` could only print. An
operator wanting any of them hand-edited YAML — the tool was easier to
drive by asking an agent than by using it.

### Added

- **`tokenops_pricing`** answers what a model costs and when that card was
  fetched. The router in the same process derives its capability tiers
  from exactly this data, while an agent choosing between models had no
  way to ask for it. (#305)
- **`tokenops_vendor_usage_status`** answers whether the numbers are being
  fed. `tokenops_data_sources` reports event counts, which says a source
  is silent but not whether it was ever switched on — "no data" and "no
  data because nothing is configured" are different answers, and only the
  second has a fix. (#305)
- **Six commands for settings the terminal could not reach**: `tokenops
  mode`, `preferred-model {list|set|unset}`, `budget {list|set|unset}`,
  `routing proposals`, `routing rule {list|set|unset}` and
  `optimizations`. Each writes the same config the MCP tools write and
  restarts the daemon afterwards. (#306)

### Notes

- `budget set` refuses a budget with no ceiling and names both ways to
  give it one. On a flat-rate plan real spend is $0 at the margin, so a
  USD limit can never trip, and a budget that cannot fire is worse than no
  budget — `--basis tokens` or `--basis equivalent` are the ones that work
  there.
- `mode active` now says, at the point of setting it, that active mode
  does its work inside the daemon. That was always true and never stated
  where it mattered.
- The per-source config hints moved from the CLI into config so both
  surfaces share one implementation rather than the MCP tool growing a
  second copy that quietly diverges.
- The parity check had a hole and it showed immediately: it built its own
  server from a fixed list, so a tool group it did not know to register
  was invisible to the diff. Two tools were added and it passed unchanged.
  It now reads its own source and fails when a registration is missing.

**GAP entries remaining in the parity allowlist: zero.**

## 0.63.0 - 2026-09-18

The drop counter surfaced in 0.59.0 made its first catch, and it was a
bug in TokenOps itself — found within a minute of upgrading to the
release that surfaced it.

### Fixed

- **The read-guard replay waits for queue room instead of dropping.**
  Every vendor-usage poller publishes through PublishWait; this ingest
  path did not, and it is the burst most likely to overflow — it replays
  the whole ledger at boot and again every two minutes through a Publish
  that discards the overflow silently. The bus's own doc says "Ingestion
  therefore uses PublishWait"; this was the one path that did not.

  On one machine `tokenops status` reported 222 events dropped in the 13
  seconds after boot, exactly the size of that machine's read-guard
  history. Same ledger, same minute, with the fix: 0.

  Nothing was permanently lost — re-scanning recovers a dropped record on
  the next tick, unlike the pollers this path was overlooked by. What was
  lost is the counter's usefulness: a figure that reads non-zero on every
  boot is one an operator learns to skip, which is the failure it was
  surfaced to prevent. (#302)

### Documentation

- **Every command added this cycle is now documented.** `tokenops
  detect`, `daemon restart`, `vendor-usage setup`, the spend-limit flags,
  the Team and Enterprise plans, `plan_limits`, `mdns` and `--no-restart`
  had all shipped without reaching the docs site. (#303)
- **The front page no longer advertises a stale version.** It said
  "Shipping now: v0.56.0" while six further versions shipped: the Pages
  deploy was current every time, the claim was hardcoded, and nothing
  checked it. A test now compares both version claims on the site against
  the newest CHANGELOG heading, which is written while cutting a release
  and is therefore the one place that cannot be forgotten. (#303)

## 0.62.0 - 2026-09-18

The claude.ai cookie carries the only authoritative reading TokenOps can
get for a Claude subscription: Anthropic's own 5-hour and 7-day
utilisation percentages, which replace an estimate built from message
counts against a published cap. It was also the hardest source to turn on
and the easiest to turn on wrongly.

### Added

- **`tokenops vendor-usage setup anthropic-cookie`** — a guided setup that
  verifies the key before writing it.

  `vendor-usage enable anthropic-cookie --session-key ...` writes the key
  and reports success without ever contacting Anthropic, so a mistyped or
  expired cookie produced nothing at all: the poller logged "Usage()
  failed" every tick into a file nobody reads, and one machine accumulated
  3,019 of those. Setup instead says where the cookie lives, reads it
  without echoing it into the scrollback, resolves the organization,
  fetches a real usage snapshot, prints the percentages back, and only
  then writes config. Every refusal ends "nothing was written".

  An account in several organizations is asked which to meter rather than
  having the first one picked, and a rejected key names the likely cause:
  these cookies rotate, so a stale copy is the common failure. (#299)

### Changed

- **CLI and MCP surfaces now fail CI when they drift apart.** The previous
  parity test asserted four named tools existed, which could not notice a
  command shipping on one surface and not the other — three did, in a
  single day. The new one diffs both directions against allowlists that
  carry a reason per entry, so what fails is an asymmetry nobody decided
  on rather than every asymmetry. (#300)

### Notes

- The parity test records the gaps that already exist instead of hiding
  them. `pricing` and `vendor-usage` have no MCP tool, so an agent can
  neither ask what a model costs nor whether ingestion is live; `mode`,
  `preferred_model`, `budget_set`, `optimizations`, `routing_proposals`
  and `routing_rule_set` have no CLI, so an operator must edit YAML for
  settings an agent changes with one call. Each is marked GAP.

## 0.61.0 - 2026-09-18

Usage-based Enterprise is billed at API rates from the first token. There
is no cap to be under, so no window, no percentage, and nothing for the
headroom model to divide by — which is why 0.60.0 gave it no catalog
entry at all, and an operator on Enterprise could bind no plan.

A denominator does exist, though. Anthropic's admins set org, seat-tier
and per-user spend limits in the console. The number is known; it was
simply never known to this tool.

### Added

- **`claude-enterprise`**, a spend-denominated plan. Its limit is supplied
  rather than published, and `plan set` refuses the binding without one:

      plan "claude-enterprise" is billed at API rates and has no usage
      window, so headroom is measured against your org spend limit — set
      it with --spend-limit

      Claude Enterprise (usage-based) (claude-enterprise) — risk low
        spend:   1203.44 / 5000.00 USD (24.1%)

  The numerator already worked: that traffic is genuinely metered, so the
  cost carried on each event is what the vendor charges — unlike a
  plan-covered event, which is correctly zero. (#297)
- **`plan_limits.<provider>`** carries the figures only an operator can
  supply: `spend_limit_usd`, the `window` it covers (monthly, weekly or
  daily, matching the console), and `rate_factor`. (#297)
- **`--spend-limit`, `--limit-window` and `--rate-factor`** on
  `tokenops plan set`. (#297)

### Notes

- **`rate_factor` exists because contracts are discounted.** Enterprise
  rates are frequently negotiated off list while this tool costs from the
  public rate card, so a console limit would otherwise be compared against
  an overstatement — and that error is invisible, which is the kind worth
  refusing to ship.
- **A spend-denominated report fills no window fields.** A plan billed
  from the first token has no window, and rendering an empty one would
  invite the reader to believe there is a cap somewhere they are under.
- **No limit configured** reports the spend and says why there is no
  percentage, rather than dividing by zero or showing a comfortable 0%.
- **Seat-based Enterprise remains unmodelled**, deliberately: it is an
  included per-seat allowance plus metered overflow, two denominators at
  once. Naming it says so rather than silently resolving to the
  usage-based plan.

## 0.60.0 - 2026-09-18

Anthropic documents its plan tiers only as multiples of Pro — "five times
the Pro plan's per-session usage allowance" — and no longer publishes an
absolute for any tier, Pro included. The catalog held three independent
absolutes instead, and they had drifted out of that relationship:
`claude-max-5x` was 1.1x Pro where the vendor says 5x, `claude-max-20x`
was 4.4x where it says 20x, and the support URL both cited now 404s.

That is the drift a pinned source URL exists to surface, and it surfaced.

### Added

- **Claude Team seats**, both verified against the vendor's help centre:
  `claude-team-standard` (1.25x Pro) and `claude-team-premium` (6.25x
  Pro). An operator on Team could previously bind no plan at all. (#290)

### Changed

- **Anthropic tiers derive from one Pro baseline.** Plans carry
  `RelativeTo` + `Multiplier`, resolved on lookup, so a single number can
  go stale instead of five, and correcting it corrects every tier at once.
  A relative entry holds no absolute of its own; a test enforces that,
  because two sources of truth in one record is how these drifted.

  **This changes headroom denominators.** Max 5x moves 50 -> 225 and Max
  20x moves 200 -> 900. Usage is unchanged; the figure it is measured
  against is now the one the vendor documents. Two third-party trackers
  checked in early September independently record 45 / 225 / 900, which is
  what the derivation produces; the replaced absolutes match no source at
  all.

  The Pro baseline itself remains unverifiable at the vendor today, so it
  is pinned in one labelled place. Settings -> Usage in the Claude app is
  the only number that is definitely current. (#290)

### Notes

- **Anthropic Enterprise deliberately has no catalog entry.** Usage-based
  Enterprise is billed at API rates from the first token, and seat-based
  Enterprise is an included allowance plus metered overflow — neither is a
  rate-limit window, and inventing one would produce headroom maths that
  looks authoritative and is fiction. `plan set` now explains that instead
  of leaving an operator to read their tier's absence off a list. (#290)

## 0.59.0 - 2026-09-18

Two kinds of defect, found by auditing what was actually wired and by an
operator installing for the first time.

The first is a command that reports success and does nothing. `hooks
status` listed two hooks on a machine running three, and no flag could
remove the third. `plan set` wrote the key and told the operator to
restart the daemon, which meant the setting had not taken effect yet. A
`keep_by_source` rule pinned a source tag that did not exist. In each
case the tool said it had done the thing.

The second is a number that is zero because nobody asked what zero means.
A subscription's traffic costs nothing at the margin, so every money
column on a flat-rate plan read 0.0000 while the headline reported
thousands — including the column "top consumers" ranked by. Telemetry the
daemon failed to write was counted and then spoken aloud only in a log
line at shutdown; 30,254 rows went missing across five weeks with nothing
an operator could see. An ingestion check that could only count its own
events flagged two sources as critical failures while both readers were
level with their source to the second.

### Added

- **`tokenops detect`** reports which AI clients are installed and changes
  nothing. `init --detect` reads like a dry run and never was: `init`
  wires the MCP server and installs hooks whether or not it is passed.
  (#287)
- **`tokenops daemon restart`** restarts the supervised unit. Six commands
  ended by telling operators to restart the daemon and none could name a
  command, because there was not one. (#283)
- **`--restart` is now the default** on the commands that write config,
  with `--no-restart` for writing several keys at once. The daemon reads
  config at boot, so a write without a restart has not changed anything
  yet. (#291)
- **The overall DX grade names the metric that set it** — `Overall: C
  (turns-per-prompt)`. Worst-grade-wins is deliberate, but a headline that
  will not move and will not say why reads as arbitrary. (#289)
- **`API EQUIV` column and per-group list-price value.** `analytics.Row`
  now carries the shadow value `Summary` has reported since it was added,
  so the table and the headline stop disagreeing. (#284)

### Fixed

- **`hooks status` and `hooks uninstall` see every hook, on every client.**
  route-guard shipped wired to `install` and invisible to both — it ran on
  every turn while status reported it absent, and no flag removed it. Both
  now take `--client`, and marker matching handles Codex's command strings
  and Cursor's flat schema, not just Claude Code's. (#282)
- **Dropped telemetry is visible.** `/healthz` carries `dropped_events`,
  and `tokenops status` and `tokenops_status` warn with the count and
  degrade a ready state. (#283)
- **`init` finds the project.** The rules scan root was the shell's
  working directory, so running `init` from `$HOME` bound the recursive
  scanner to the entire home directory with `repo_id: <username>`. It now
  walks up for a repo root and refuses home and filesystem roots. Running
  from a subdirectory no longer scans a fragment of the project under its
  basename. (#286)
- **Rework means returning to a file**, not editing it twice. Two edits to
  different sections of one file — the ordinary way to make a change —
  counted as waste, and that metric feeds the overall grade. On one real
  14-day corpus the rate falls from 23.9% to 5.2%. (#285)
- **mDNS no longer broadcasts the machine name to publish an address
  nobody can reach.** The daemon advertised unconditionally on every
  interface while binding loopback. It now advertises when the LAN can
  reach it, with `mdns.enabled` and `mdns.instance_name` to override. The
  TXT record also stops claiming `version=v0.10.0`, unchanged for
  forty-eight releases. (#288)
- **Stale ingestion means behind the source, not quiet.** Local readers
  compare against their origin, so an unused vendor is silent and a dead
  reader is not. Remote pollers keep the time-based check, because their
  endpoint always has an answer. (#292)
- **A bare API key no longer suggests a subscription plan.** The wizard
  printed "run: tokenops plan set anthropic claude-max-20x" one line below
  "raw API key — likely metered usage, no plan". `Detect` also
  deduplicates, which its own doc had promised since the package was
  written. (#287)
- **The cursor turn source is registered.** It runs unconditionally and
  appeared in no registry, so its tag could only be learned from source —
  and a `keep_by_source: cursor-turns` rule, named after the ledger
  directory rather than the `cursor-hook` tag, silently pinned nothing.
  Unmatched retention keys are now reported. (#294)
- **`init` stops asking for a daemon unit that is already installed.** The
  prompt printed on every run and sat outside the "steps still need you"
  tally, so it neither counted nor went away. (#283)
- **A plan-covered burn rate reports tokens**, and an all-zero USD
  forecast is replaced by the reason it is empty rather than seven rows of
  $0.0000 with confidence bands. (#284)

## 0.58.0 - 2026-09-18

Routing has been configurable since it shipped and had never rewritten a
single request. It enforced through a proxy most operators bypass, and
what it could express was a rule naming two models of one vendor, gated
on plan-window pressure — so a model that did not fit the work was only
ever corrected when the window was nearly full.

The problem is simpler than scarcity. A model is chosen once at the start
of a session and stays chosen. Every turn afterwards runs on it,
including the retrieval and the reading-around that a cheaper and faster
model would do just as well, because nothing asks the question again.

### Added

- **Capability tiers, for every provider.** A task resolves to a tier and
  each provider resolves the tier to whatever it offers, so routing works
  for openai, gemini, xai, mistral, openrouter and opencode's own
  provider routing rather than one vendor's model names. Tiers come from
  price rank inside a provider because the rate card refreshes daily,
  while a list of model names written into the source rots every release.
  Against the 36 models one operator had actually run: 31 placed, where
  the card alone could price 13. (#280)
- **Task-kind classification.** What an instruction asks for — retrieval,
  research, a routine edit, or the deep work a flagship is for — rather
  than how long it is. The kind belongs to the task, so a continuation
  inherits it: on a real 1316-instruction corpus, 56% of instructions
  were five words or fewer and 27% were a bare "go", and reading each
  prompt alone classified 11% of them against 90.5% with inheritance.
  (#280)
- **`route-guard`**, a prompt-time hook on every client that has one. It
  states the case for a cheaper model when the one in use is over-spec,
  once per kind of work per session. It only ever routes down: suggesting
  something pricier is a spending decision nobody delegated, and Fable is
  capped at half the weekly allowance on Max. Assertiveness is
  configured — advise, delegate, auto, or off — with `auto_kinds` naming
  what may be handed to a subagent without asking. (#280)

### Fixed

- **Free models were invisible to routing.** Candidates were filtered on
  a cost greater than zero, so every `:free` tier and local runtime was
  skipped precisely because it was free — 12 of 36 on one machine, and
  exactly the models worth routing cheap work to. (#280)
- **Refreshed prices were missed entirely.** The card files a snapshot
  rate under a prefix pattern so version-suffixed models resolve to their
  family rate; reading the rate map directly matched only the few exact
  rows and reported the current flagship as an unpriced guess. (#280)
- **Retired models decided the tiers.** The card keeps a long-gone Haiku
  below the current cheap model and a superseded Opus above the current
  flagship, so ranking across the whole catalogue pushed the live
  generation into the middle and the cheapest model an operator could
  actually pick came back "balanced". (#280)
- **Proxy providers got no routing at all.** opencode and Copilot serve
  other vendors' models under their own name and the card has no rows for
  either, so a target search scoped to the requesting provider found
  nothing and gave up — on the clients that most need it. (#280)

## 0.57.0 - 2026-09-17

Three paths were discarding ingested usage events while reporting
success, found the same way as last release's four: by enabling every
reader on a real machine and counting the source stores independently.
opencode held 48,540 assistant messages with token usage and the store
had 33,316; Codex had 3,597 turns and the store had 845.

### Fixed

- **The event bus dropped envelopes whenever its queue filled.** A
  non-blocking send is right for the proxy hot path, which must not
  block on storage, and wrong for ingestion: a poller advances its
  watermark *before* publishing, so a dropped envelope is never
  revisited. The loss was permanent, invisible until one line at
  shutdown (`published=49918 dropped=36072`), and reproduced identically
  on every restart because the queue saturated at the same point each
  pass. It was not even a clean truncation — February lost 63% of its
  rows, March none, April 26% — so every period was understated by a
  different, undetectable amount. Ingestion now waits for room. (#278)
- **A batch the store rejected was discarded whole**, up to 64 rows at a
  time, with no retry. Rejections were routine rather than exceptional:
  the write-ahead log had grown to 372MB beside a 396MB database and an
  ordinary insert no longer fit in its five-second deadline. Batches are
  now retried with backoff, and rows count as lost only once the bus has
  actually given up on them. (#278)
- **The WAL never checkpointed.** SQLite can only checkpoint up to the
  oldest snapshot a reader still holds, and this store is read
  continuously; `VACUUM` does not truncate a WAL, so the existing
  reclaim never touched it. Retention now checkpoints on every pass
  rather than only after one that deleted rows — the log grows with
  writes, so gating it on a deletion left the busiest stores never
  checkpointed. (#278)
- **Retention deleted imported history minutes after it landed.** Every
  vendor-usage reader writes type `prompt`, so one window governed
  Claude Code, Codex, opencode, Cursor and Copilot together with the
  live stream. A backfill is by nature historical: the 120-day window
  removed all 33,316 opencode rows four minutes after ingestion. (#278)

### Added

- **`retention.keep_by_source`** sets a retention window per reader,
  overriding the per-type window in both directions — `forever` for
  imported history, shorter than the type window for something like
  `read-guard`. A type policy now excludes every source holding its own
  policy, so a source rule cannot be silently overruled by the broader
  one. `forever` and `never` are accepted alongside `0`, which in a
  delete path reads like "keep for zero time". (#278)

## 0.56.0 - 2026-09-16

Every client TokenOps reads can now also be coached, and four silent
zeros were found by checking each reader against the real store on disk
rather than against its own fixtures.

### Fixed

- **`dx --source codex` counted nothing.** It keyed on an `event_msg`
  /`user_message` event current Codex no longer emits, and deliberately
  skipped the `response_item` that replaced it to avoid double-counting —
  so it dropped 100%. On 37 real sessions holding 343 instructions it
  reported "0 instructions across 37 sessions". The channel is now
  decided per file. (#264)
- **Codex spend had no model.** `Turn.Model` was declared and never
  assigned, so every Codex turn entered the event store unpriceable and
  the client reported `$0` in spend, burn rate, forecast and top
  consumers. 3597 turns, none costed. Codex states the model on its own
  `turn_context` record; it is now carried forward. (#267)
- **The coach priced from the embedded baseline, not the dated card** the
  daemon builds from persisted snapshots — so a machine whose snapshot
  knew a model still had its budget measured against one that did not. On
  a real rollout, `$0.00` versus `$0.68`, and a nudge that could never
  fire. (#269)
- **`dx` graded a benchmark harness.** Sessions run in throwaway
  directories were 94% of a 7-day window and set every grade: median 2
  turns per instruction against the operator's real 17, and straight As.
  Now excluded, with `--include-scratch` to opt back in. (#260)
- **Prompt text reached only the Claude Code reader**, so `story` titled
  1258 of 1258 opencode tasks "(no instruction text)" and every
  instruction opened its own task. Now carried by every reader. (#264)
- **A failing test names itself.** The gate surfaces the tail of
  `go test`, where the `--- FAIL` line has already scrolled past; two
  pushes were refused with no usable evidence. It also fixed the flake it
  then named: a p99 latency gate that was asserting about the machine.
  (#272)

### Added

- **Coaching on every client.** Codex (`Stop`, same payload shape as
  Claude Code), Cursor (`stop`, tokens carried inline), and opencode
  (`session.idle`, delivered as a TUI toast from a generated plugin).
  Three distinct cache conventions had to be told apart to price them.
  (#266, #271, #274)
- **read-guard on opencode** — the only other client whose hook can
  decline a read. Codex has no file-read tool and Cursor's
  `beforeReadFile` is observe-only; `hooks install` refuses both with the
  reason rather than writing a hook that never fires. (#273)
- **Smart routing** decides per turn from task class, plan-window
  pressure and the live rate card, with no rules table.
  `tokenops_routing_advise` reaches every MCP client, proxy or not.
  (#259)
- **The rate card refreshes itself** daily and applies to the running
  daemon rather than writing a snapshot nobody reads until a restart.
  One outbound call, documented and switchable. (#270)
- **`story --for report` and `--for handoff`**, plus `tokenops_story` so
  the agent reads its own history back. (#257)
- **`coaching.quiet`** rate-limits the proactive channel, and the coach
  argues for `intervene` from the operator's own ledger. (#255, #256)
- **GPT-6 Astra priced** and pinned `verified`, with the two published
  rules the schema cannot express stated in the catalog. (#268)

### Documentation

- **An honest client capability matrix**, marking what a client can never
  support with the reason rather than as pending. (#258)

### Security

- grpc 1.82.1 → 1.83.2 for GO-2026-6348, a called vulnerability surfaced
  by this repo's own pre-push gate. (#261)

## 0.55.0 - 2026-09-15

Coaching gains a single control point, and sessions gain an account of
what happened in them. Both were reachable only by reading code before.

### Added

- **`coaching.delivery` — one axis for every coaching channel.** The two
  hooks that speak first, `coach-hook` and `read-guard`, read no config
  at all: they took flags, and `tokenops init` baked those flags into
  `settings.json`. Configuring the coach meant hand-editing a command
  line in `~/.claude/settings.json`, and changing it meant re-registering
  the hook.

  The rungs are graded by *interference* rather than by who initiated,
  because a non-blocking nudge and a refused tool call are not the same
  imposition:

  - `observe` — records everything, answers when asked. Hooks stay
    installed but say nothing, while still keeping their ledgers.
  - `advise` — plus the coach speaks unprompted but never blocks.
  - `intervene` — plus the coach acts: `read-guard` refuses a redundant
    re-read before it costs a token.

  `advise` is the default because it is exactly what the hooks did
  before this key existed. **Upgrading changes nothing until you say so.**

  `tokenops coach delivery [observe|advise|intervene]` shows or sets it,
  and takes effect immediately — the hooks resolve it on every
  invocation, so there is nothing to re-install.

- **`tokenops story` — what you asked for, what the agent did, and where
  it went sideways.** `dx` answers "what are my sessions like" in
  aggregate; nothing answered "what happened in this one".

  The unit is a task, and it is inferred rather than demanded. A new
  session opens one; a pause longer than `--idle-gap` splits one; an
  instruction that only means something in reference to the work in
  flight ("Go", "proceed", "no, like that") never opens one, however long
  the pause. Every task reports which of those split it.

  `--json` renders the same account for an agent reading its own history
  back. Prompt text is read at scan time and never persisted.

- **`agentdx.Units()`** makes the instruction unit a first-class value
  instead of private state inside `Compute`, which now folds over it — so
  the metrics and the narrative cannot drift into two definitions of
  "rework".

### Changed

- **`hooks install` no longer pins `--mode` on read-guard.** Pinning is
  what made `coaching.delivery` unreachable: the flag always won, so the
  config key changed nothing until the hook was re-registered. An
  explicit `--mode` still wins, which is how one hook gets pinned against
  config deliberately.

- **`init` no longer silently promotes read-guard to blocking** when its
  ledger justifies it. Promoting a hook from observing to refusing the
  agent's reads is precisely what `intervene` exists to gate. The
  evidence is still computed and now reported, with the command that acts
  on it.

- **`make install-hooks` arms the whole local gate.** warden and the
  repository's own protected-branch hook both wanted
  `.git/hooks/pre-push`, and git allows one file there — so installing
  either silently disarmed the other, and whichever ran last decided
  which protection you had. The branch guard is now warden's first
  pre-push step. Without warden on PATH the target still installs the
  standalone guard, so branch protection never becomes conditional.

- **Suppression governance checks liveness, not recency.** The 120-day
  timer failed `go test ./...`, so a governance deadline was
  indistinguishable from a broken build and a one-line date bump was the
  cheapest way out. It was also measuring the wrong thing: of 27
  `scan.exclude` entries, 14 suppressed nothing and 12 pointed at paths
  that had not existed since the DDD refactor. Blocking checks now
  require every entry to resolve to something git tracks unless it
  declares `# Transient:`; review age moved to
  `scripts/suppression-review-due.py` (`make sec-review`), which warns
  without blocking.

- **The privacy claim is scoped to content rather than address.** "There
  is no upload path to secure" was true and would stop being true the day
  a team tier ships. What is guaranteed is that prompts, file contents
  and transcripts never leave the machine, and that everything TokenOps
  derives is a number computed here from material that stays here.

### Fixed

- **Harness-injected entries were counted as operator instructions.**
  `<task-notification>`, `<command-name>`, `<system-reminder>` and the
  slash-command caveat arrive in the transcript with the user role
  because that is the only role available to them. They inflated every
  per-instruction figure, `dx` included. The sample output on the docs
  landing page is regenerated accordingly.

- **Concurrent sessions interleave, and `story` split on it.** Sorting
  every unit into one global time order made the session appear to change
  at nearly every unit, turning each instruction into its own task. Units
  are partitioned by session before any boundary is inferred.

- **Summed per-turn tokens are not a cost.** Every turn re-sends the
  accumulated context, so the sum double-counts — the figure that reads
  about nine times what cache-aware pricing says. `story` reports peak
  context, and the JSON field is named `context_carried_tokens`.

- **Nine GitHub Actions findings were hidden behind a suppression written
  for a different rule.** `.github/workflows/*.yml` was excluded for
  SEC-659/697, which no longer fires there; what it was absorbing was
  IAC-153/306/308/309/314. All ten are now triaged into
  `security/vex.json` with fingerprints minted from the scanner that
  gates CI, not from a local one.

## 0.54.3 - 2026-08-26

Third attempt at delivering v0.54.1's security fix to Homebrew. The
first two failed for two different reasons, neither of them in the
product.

### Fixed

- **The release workflow's tap credential.** v0.54.1's cask step failed
  with `401 Bad credentials`. v0.54.2 was cut to fix it and failed the
  same way, because the "fix" repointed the lookup at a secret that does
  not exist — replacing a rejected credential with an empty one.

  Settled by measuring rather than guessing. A probe printing only
  character counts, never values, reported
  `secrets.HOMEBREW_TAP_TOKEN` at 93 characters — the length of a
  `github_pat_` fine-grained token — and the prefixed variants at 0. The
  name had been right all along; the token behind it had expired.

  A tag-triggered re-run replays the workflow as of that tag, so neither
  v0.54.1 nor v0.54.2 could pick the correction up. Hence a third tag.

  No code change over v0.54.1. `brew` users move from 0.54.0, and off
  the reachable `golang.org/x/text` infinite loop (GO-2026-5970).

## 0.54.2 - 2026-08-26

Delivers v0.54.1's security fix to Homebrew, which never received it.

### Fixed

- **The release workflow read a secret that does not exist.** The org
  secret is named `kl_HOMEBREW_TAP_TOKEN`; `release.yml` asked for
  `secrets.HOMEBREW_TAP_TOKEN`. An undefined secret expands to an empty
  string, so goreleaser presented an empty credential and the cask step
  failed with `401 Bad credentials` — 401 rather than 403 being the
  tell that no credential was presented at all, rather than one lacking
  permission.

  v0.54.1 therefore published its GitHub release and binaries but never
  updated the Homebrew cask, leaving `brew` users on 0.54.0 — the
  version carrying the reachable `golang.org/x/text` infinite loop
  (GO-2026-5970) that v0.54.1 exists to fix.

  This release carries no code change over v0.54.1. It exists to put
  that fix in front of Homebrew users through a release path that
  works.

## 0.54.1 - 2026-08-25

Closes a reachable infinite loop in the coaching backend's HTTP path.

### Fixed

- **`golang.org/x/text` bumped past GO-2026-5970.** govulncheck reported
  it as reachable, not merely present: v0.38.0 loops forever on invalid
  input, and the trace ran from `llm.OpenAICompatBackend.Generate`
  through `http.Client.Do` into `norm.Form`. That is the coaching
  backend's outbound path, so the triggering input is a provider
  response — a hang caused by something tokenops does not control.

  v0.41.0 clears it. `govulncheck` now reports no vulnerabilities.

### Added

- **Dependabot config** (`gomod`, `github-actions`, `npm`), grouped by
  family. The auto-merge workflow had been idling since it was added —
  it acts on Dependabot PRs and nothing was configured to open any — so
  Go modules were never watched, and this one drifted far enough to
  carry a reachable CVE unnoticed.

## 0.54.0 - 2026-08-25

Request counts stop including tokenops talking to itself.

### Changed

- **The MCP server's own activity pings no longer count as traffic you
  made.** `mcp-session` events are the MCP server recording that it was
  called; they carry no tokens and no cost, but they were landing in the
  request total that `tokenops spend`, `burn_rate`, `top_consumers` and
  `forecast` report. An operator reading "42 requests" was reading some
  number of their own calls plus some number of tokenops talking to
  itself, with no way to tell the two apart. They are now excluded by
  default, alongside `demo` — the scorecard already drew the line here.

  Request counts on installs that use the MCP server will drop. Nothing
  about spend or tokens changes: the pings never carried either.

- **`--include-demo` splits into per-source opt-in.** It used to clear
  the entire exclude list, so asking to see synthetic seeds folded in
  every other excluded source too. `--include-source` (CLI, repeatable
  and comma-separated) and `include_sources` (MCP) re-admit sources by
  name:

  ```bash
  tokenops spend --include-source=demo                # seeds only
  tokenops spend --include-source=demo,mcp-session    # both
  ```

  `--include-demo` and `include_demo: true` still work, as aliases for
  `demo` alone. A named source that is not excluded by default prints a
  note rather than failing, so the flag never silently does nothing.

## 0.53.0 - 2026-08-25

Answers "does quality degrade as context grows" from the operator's own
transcripts rather than from assumption.

### Added

- **`tokenops dx` reports quality against context size.** Instructions are
  banded by how full the window was when they ran, and each band carries
  the rejection rate, the repeated-call rate, and median turns.

- **Repeated tool calls** — the agent re-issuing a call it already made
  with identical arguments. This is the signal that shows the effect: on
  the maintainer's Claude Code corpus it runs 1.7% below 600k of context
  and 2.6% above, a 53% increase over roughly 80,000 calls.

  It is drift rather than error, which is why rejection misses it — an
  operator rarely tells the agent off for redoing something, they watch it
  happen and form an impression.

  Two controls make the number mean anything: the lookback is bounded to
  fifty calls, so session length cannot inflate it, and it is scoped per
  session, because an agent cannot repeat itself across a boundary it has
  no memory of.

- **Rejection rate per band**, the operator's own quality verdict. Worth
  reporting even where it shows nothing: on this corpus it is flat, and
  rework and tool errors actively fall with context — both confounded by
  session position, since a low-context instruction is usually an early
  one while the agent is still orienting.

## 0.52.0 - 2026-08-24

### Added

- **The Stop nudge reports context against the model's window.** Dollars
  are a counterfactual on a flat-rate plan; context is not — it fills up,
  forces a compaction, and until then every turn re-reads the whole of
  it.

      Context: 873k of 1.0M (87%) — worth compacting: every turn now
      re-reads this whole context.

  Window sizes come from Anthropic's pricing page (Claude 4.6 and later
  carry the full 1M window) and are confirmed by a real 999,947-token
  turn. An `[1m]` suffix outranks the family default. An unrecognised
  model reports its context size with no percentage — a share against a
  guessed denominator looks authoritative and is not.

  The advice escalates: silent below 75%, a note at 75%, and at 90% the
  fact that an automatic compaction is imminent and will choose what to
  drop.

## 0.51.1 - 2026-08-24

### Fixed

- **The Stop nudge read as a bill.** It told an operator paying $200 a
  month for Claude Max that they were "over your $50 session budget".
  Three things were wrong at once: they never chose $50 (it is the
  shipping default, and the possessive implied an agreement never made),
  only the quietest tier said "API-equivalent" so the louder the warning
  got the more it looked like real money, and nothing said the figure is
  not a charge at all.

  On a subscription it is a counterfactual — what the session would have
  cost at list price — which is the only way to watch context drift
  compound when the actual bill is flat either way. The wording now says
  so at every tier, calls the default a default, and reserves "your
  budget" for one the operator actually set.

## 0.51.0 - 2026-08-24

The reply coach reads opencode, completing coaching coverage across every
client that keeps a local record.

### Added

- **`tokenops coach replies` reads opencode.** Same store as the prompt
  reader, opposite role filter. 14,139 replies on the maintainer's
  machine were outside the report.

  Reasoning parts are excluded: they are the model thinking, not the
  prose the operator read, and this coach measures output density as
  experienced.

- **`--source` on `coach replies`**, mirroring `coach prompts`.

## 0.50.0 - 2026-08-24

The prompt coach reads opencode.

### Added

- **`tokenops coach prompts` reads opencode.** It keeps history in SQLite
  rather than JSONL, so it is a store to open rather than a root to walk:
  prompt text lives on a part row joined to its message. 3,960 operator
  prompts on the maintainer's machine were previously invisible to the
  coach.

  Continuation prompts opencode injects for itself are marked `synthetic`
  and dropped — coaching someone on words they never wrote would be worse
  than not coaching them at all.

- **`--source` on `coach prompts`**: `auto` (default) | `claude-code` |
  `codex` | `opencode`, so a single client can be inspected on its own.

## 0.49.0 - 2026-08-24

Agent DX now reads opencode, and reports per upstream provider.

### Added

- **`tokenops dx --source opencode`.** The richest client store and the
  only multi-provider one: token counts and a completion time on every
  assistant row, tool calls joined by message_id, and compactions as an
  explicit part type rather than something to be recognised.

- **Per-provider breakdown.** Only a multi-provider client records which
  upstream served a turn, so this is the only way to ask whether one
  provider is a worse experience than another. Providers under twenty
  prompts are not reported separately — their percentages swing on single
  events.

### Fixed

- **Tool names were matched case-sensitively.** Claude Code writes "Task"
  and "Edit"; opencode writes "task" and "edit". The lookup silently
  reported zero delegation and zero rework for every client but one.

- **An unmeasured rate rendered as 0.0%**, which reads as "the agent never
  redid its work" when the truth is that no edits were seen. It reports
  n/a now — the same distinction between a missing measurement and a
  measured zero that this work keeps turning up.

## 0.48.0 - 2026-08-24

Agent DX now reads every client that keeps a local record, and the
optimizer has an explicit three-way mode instead of two states buried in
a daemon-wide flag.

### Added

- **`optimizer.mode`: `automatic` | `in_request` | `off`.** Automatic
  rewrites matching requests in flight; in_request refers the decision to
  you through the MCP surface and forwards the request untouched; off
  records what the rules would have done and changes nothing. Unset means
  off — opting into an intervention has to be an act, not a default, and
  a misspelling fails validation rather than silently reading as off.

  Routing no longer requires the daemon-wide `active` flag, which only
  gated it because there was nowhere else to express intent.

- **`tokenops dx --source codex`.** Codex marks what these metrics need
  explicitly, where Claude Code leaves it inferred: `user_message` is
  unambiguously the operator's instruction and `turn_aborted` is
  unambiguously an interrupt.

- **`tokenops dx --source cursor`.** Reads Cursor's `cursorDiskKV` store
  read-only. A store that exists but cannot be understood returns a
  schema error rather than an empty report — an unreadable store must not
  render as an idle operator.

- **`--source auto`** (the default) reads every client present. A pinned
  `--root` narrows it to one tree rather than being ignored while the
  readers scan the real home directory.

### Fixed

- **Window-pressure routing ignored the vendor's own reading.** Codex
  publishes `rate_limits` per turn and Copilot and Cursor publish quota
  snapshots; the probe counted messages instead, and that heuristic only
  works for Claude Code — so pressure rules were inert for every other
  provider. The resolver moved from the MCP adapter into the domain and
  the probe now prefers ground truth.

## 0.47.0 - 2026-08-23

Completes the agent-experience metrics and puts them to work: everything
is graded, one change is named, and the agent can read them itself.

### Added

- **Four more DX metrics.** Wall-clock per instruction (median + p90),
  tokens per instruction, tool calls per instruction, and a first-try
  rate — the share of instructions completed with no rework, no
  interrupt, and no delegation. Wall-clock matters separately from turns:
  turns measure effort, this measures waiting, and they diverge.

- **Grades on every DX metric.** Thresholds are set where a difference
  changes what an operator would do, not at a statistical quantile. The
  overall grade is the WORST of the measured dimensions rather than the
  average — an experience is only as good as its sharpest friction.
  Unmeasured metrics stay ungraded: never a zero, never an F.

- **One named recommendation**, ordered by leverage rather than severity.
  Rework and interrupts are what a clearer instruction fixes today; turn
  count is usually their symptom.

- **`tokenops_agent_dx`.** Hands the graded metrics and the
  recommendation to the agent, so it can act on the session it is in
  rather than the operator reading a report afterwards.

## 0.46.0 - 2026-08-23

TEU stopped reporting "not measured" while real uplift was happening, and
the product gained a second dimension: what sessions are like to work
with, not just what they cost.

### Added

- **`tokenops dx`.** Six agent-experience metrics derived from
  transcripts the client already writes — turns per instruction (median
  and p90), context growth per turn, rework rate, interrupt rate,
  escalation rate, compactions per session. Work is grouped by operator
  instruction, which is turns-per-task without asking anyone to mark
  tasks by hand.

  Time-to-first-token is deliberately absent: a transcript records when a
  turn finished, never when it started streaming, so no passive reader
  can populate it honestly. It stays proxy-only.

- **TEU counts client-side interventions.** The read guard prevents
  redundant re-reads inside the client, where the proxy cannot see them,
  and had reclaimed ~387,000 tokens with nothing recording it. A daemon
  poller now publishes each blocked read as an `OptimizationEvent` under a
  new `read_dedup` kind, so a client that never proxies can score on TEU
  at all. Only actual blocks count — an observe-mode `would_block` saved
  nothing.

### Fixed

- **The scorecard reported a failure as an absence.** `Build` discarded
  `Compute`'s error, so an unreadable store rendered every KPI as
  "N/A (not measured)" — indistinguishable from a metric nobody measured.
  Widening `--since-days` past what the reader loads inside its deadline
  therefore produced *fewer* numbers with no explanation, intermittently.
  The reason now travels on the scorecard and is rendered.

## 0.45.0 - 2026-08-23

The optimisation half of the product accounted in dollars. On a flat-rate
subscription dollars are always `$0.00`, so every number was either zero
or a counterfactual and no test failed. This release makes tokens and the
rate-limit window first-class, stops two KPIs grading data that was never
measured, and turns `init` into the single command that wires the machine.

Minor rather than patch: `init` now registers MCP servers and installs
hooks by default (`--no-wire` restores the old behaviour), the read guard
promotes itself to active once its ledger justifies it, and scorecard
JSON drops unmeasured KPI blocks instead of emitting them.

### Added

- **Rate-limit window routing.** `optimizer.routing_rules[].when_window_pct_above`
  scopes a rule to periods when the plan window is at least that full. On
  a subscription the window is what runs out, not money, so a rule can now
  keep you on your best model while there is headroom and conserve it only
  when there is not. A rule stays idle when the window cannot be measured
  or its reading is over ten minutes old — acting on an unmeasured
  shortage is worse than not acting.

- **Task-aware routing.** `when_class: mechanical | reasoning` scopes a
  rule to the kind of work a turn is, classified from instruction length,
  tool-call density, and whether the operator just rejected the previous
  answer. The classifier abstains rather than guesses: anything unclear
  keeps the model the client asked for.

- **Preferred model as a ceiling.** `preferred_models` per provider. Routes
  to cheaper models still apply automatically; a route to something
  pricier is refused, recorded, and surfaced through
  `tokenops_routing_proposals` / `tokenops_routing_decide` for an answer.
  Decisions apply from the next matching request without a restart.

- **Token budgets.** `basis: tokens` with `limit_tokens`. Both previous
  bases were dollar quantities and validation required a positive
  `limit_usd`, so a flat-rate operator could only budget against imaginary
  money.

- **Token forecasts.** The dashboard API, `tokenops_forecast`, and
  `tokenops spend --forecast` now project token volume alongside cost.
  `forecast.TotalTokens` had existed since the forecaster was written with
  no callers.

- **`init` wires the machine.** Registers the MCP server with every host
  found (Claude Code, Claude Desktop), installs the hooks, and reports what
  still needs a human. Re-running repairs drift. Everything is idempotent,
  backed up, and atomic; an unparseable host config is refused rather than
  clobbered.

- **Verified rate cards for Claude Opus 5 and Mythos 5**, checked against
  the vendor pricing page and cross-checked against a LiteLLM snapshot.
  Marked `verified: true` so a stale upstream cannot regress them.

- **Reply coach recommendations.** `tokenops coach replies` proposes an
  output-density target drawn from the operator's own leanest substantial
  session, with the evidence attached. It declines when a corpus is
  already within 25% of its own best.

### Fixed

- **The 5-hour window meter read `0 / 200` under any load.**
  `countsAsMessage` excluded `assistant_turn` events on sound reasoning —
  one prompt fans out into many turns — but that reader is ~99.8% of
  ingested events, so nothing was left to count. The reader now marks the
  first assistant turn after each operator prompt, identified by content
  shape (most `type:"user"` rows are tool-result echoes). On real
  transcripts a window holding 2,990 turns resolves to 69 prompts.

- **Token savings were gated on a dollar comparison.** The router returned
  `(0, 0)` whenever a route's price delta was not positive, discarding the
  token measurement with it — and TEU sums that field, so a dollar test
  starved the headline token metric.

- **Plan-covered traffic was priced at list rates** in the router and
  replay, which build synthetic events whose zero-value `CostSource`
  deserialises as metered.

- **Pricing gaps in plan-covered traffic were invisible.** The error from
  `spend.Compute` was discarded, so an unpriced model contributed nothing
  to the API-equivalent figure and left no trace. On a real store this hid
  16,429 requests — 97% of a week — behind a figure computed from the rest.

- **Two KPIs graded data that was never measured.** TEU fell back to a 15%
  constant and printed `[B]`; FVT medianed a `Latency` field no passive
  reader populated, and zero seconds grades `[A]`. Both now report N/A when
  unmeasured — and FVT is now genuinely measured, reconstructed from
  transcript timestamps with guards for idle gaps and clock skew.

- **The 24h burn line was flat under any load.** The sparkline keyed on
  `CostUSD`, zero for every plan-covered row.

- **`replay` reported 0% saved for plan-covered sessions.** Adds
  `SavingsRatioTokens` from fields the result already carried.

- **A `rules.root` pointing at a deleted directory failed silently.** Now a
  `rules_root_missing` blocker with a remediation line.

- **Retention pruned rows but never shrank the file.** Opt-in
  `retention.reclaim` runs a VACUUM after a pass that deleted rows.

- **`daemon status` did not name the daemon's log.** The obvious-looking
  `~/.tokenops/daemon.log` is written only by the MCP spawn path; a
  supervised daemon logs wherever its unit redirects stdout.

- **The CLI test suite wrote to the developer's real Claude config.**
  `TestMain` now sandboxes `$HOME` for the whole package.

### Changed

- **The read guard activates when its own ledger justifies it.** It shipped
  in observe mode and stayed there, logging redundant re-reads without
  preventing any. `init` now promotes it to active past a 50k-token bar and
  states the measured reason.

## 0.44.0 - 2026-08-18

Closing the 27-day outage class that v0.43.0 made *visible*: supervise
ingestion so it survives reboot, stop leaking the dashboard token, and
stop telling agents to run a command that does not exist.

Minor rather than patch: `tokenops_dashboard` hint and
`DaemonPresenceNextAction` strings changed (asserted verbatim in tests
and may be parsed downstream), and Gemini cache-read rates changed.

### Added

- **`tokenops daemon install / uninstall / status`.** Writes a launchd
  LaunchAgent (macOS) or systemd user unit (Linux) with the real binary
  path and `$HOME`, then loads it. The `deploy/launchd` plist used to
  contain `__TOKENOPS_BIN__` placeholders; copying it as-is exec'd a
  binary that did not exist. `--no-load` writes only; `--dry-run` prints
  the unit. `tokenops init` points here next.

- **Opt-in event-store retention.** `retention.keep` maps event types to
  windows (`30d`, `720h`, …). Empty (the default) deletes nothing. When
  set, `tokenops start` runs the pruner that previously existed only as
  a tested-but-unwired package. Audit log is never pruned.

### Fixed

- **`~/.tokenops/daemon.url` is `0600`.** It carries `dashboard_token`.
  The dedicated token file was already `0600`; the hint file was `0644`,
  so any local account could read dashboard auth.

- **Agents are told to run `tokenops start`, not `tokenops up`.**
  `tokenops_dashboard`, the dashboard HTML, and poller comments still
  named a command that does not exist. Status already said `start`.

- **archlint lists every `internal/contexts/*` package.** The AGENTS.md
  contract was already broken on main (~28 of ~51 packages). Stale
  `storageExempt` entries that did not import sqlite are gone;
  `TestDomainPackagesComplete` and `TestStorageExemptImportsSQLite` keep
  it from drifting.

### Changed

- **Gemini 2.5 catalog rows vendor-verified and pinned.** Google's
  pricing page (and Vertex) list cache-read at 10% of input: Pro
  `$1.25/$10/$0.125`, Flash `$0.30/$2.50/$0.03`, Flash-Lite
  `$0.10/$0.40/$0.01`. The catalog had the older `$0.31` / `$0.075`
  explicit-cache figures. Gemini 1.5 is no longer on the vendor page
  and stays unpinned for back-pricing. Sources:
  https://ai.google.dev/gemini-api/docs/pricing

- Docs/install path: published quickstart, SECURITY supported versions,
  CONTRIBUTING disclosure address, README release platforms, and Pages
  Node 24 now match `main` (cask on `klarlabs-studio/tap`, module
  `go.klarlabs.de/tokenops`, current minor).

## 0.43.0 - 2026-08-03

Making a dead ingestion pipeline visible. Every change here comes from a
post-mortem of a 27-day outage in which TokenOps ingested nothing while every
surface answered successfully.

### Fixed

- **The staleness warning states how long a source has actually been silent.**
  It could only ask "were there events in the last 48h?", so the text read
  identically on the second day of an outage and the twenty-seventh.
  `StaleSource.SilentFor` now comes from a new `Store.LastEventBySource`, and
  `Severity()` escalates — warning, degraded at 7 days, critical at 14.

  ```
  before  ingestion stale: claude-code-jsonl has 0 events in the last 48h
  after   ingestion stale [critical]: anthropic-cookie has produced no events
          for 52 days (checked a 48h window)
  ```

- **The remedy named the wrong program.** It said to "reconnect the MCP server
  or restart the daemon", conflating two things: `tokenops serve` is the MCP
  server and ingests nothing, `tokenops start` runs the pollers. During the
  outage eleven `serve` processes were running and `start` was not, so the
  advice pointed at the healthy half. Both the warning and
  `StaleIngestionNextAction` now say `tokenops start` explicitly.

### Added

- **Spend answers say when they cannot be trusted.** `tokenops_spend_summary`
  and `tokenops_burn_rate` carry a `measurement` block when an enabled source
  has stopped ingesting: `trusted: false`, the severity of the worst gap, and a
  note that the figure is a lower bound rather than a measurement of zero. A
  total of zero has two causes — nothing spent, or nothing measured — and they
  were formatted identically. Healthy ingestion adds no field.

- **`tokenops_status` reports when no ingestion daemon is reachable.** `serve`
  and `start` share nothing but `events.db`, so with the daemon absent serve
  answered every query against a store that had stopped being written. This
  fires immediately rather than after the stale window, and is unambiguous in a
  way a quiet source is not. A warning, not a blocker: serve genuinely answers
  queries, so it degrades `ready` the same way stale ingestion does.

- **`deploy/launchd/de.klarlabs.tokenops.plist`** supervises the ingestion
  daemon with `KeepAlive` and `RunAtLoad`. Nothing supervised `start`, so a
  reboot silently ended observability while the client kept respawning `serve`.

### Note

`measurement` is additive, but the staleness warning and next-action strings
changed. They are asserted verbatim in the MCP and CLI status tests and may be
parsed downstream — hence a minor rather than a patch.

## 0.42.x - 2026-07-08 → 2026-07-26

Not previously recorded here. Release plumbing and MCP schema work: publish a
Homebrew cask instead of a formula (#158, #159, #160), advertise output schemas
for the data tools (#157), bump `go.klarlabs.de/mcp` to v1.22.0 (#156), and CI
budget/reliability fixes (#154, #155).


## 0.41.0 - 2026-07-08

### Added

- **Verified catalog-row pinning (`verified: true`).** The runtime prices events
  with the effective-dated engine, where a fetched snapshot overrides the baseline
  for current events — so an upstream source that goes stale on a model silently
  regresses a rate you verified (LiteLLM still priced the deprecated `deepseek-chat`
  alias at `$0.28` after the catalog was corrected to `$0.14`). A row marked
  `verified: true` is now authoritative: its snapshot value is stripped before
  layering (`spend.DefaultPinnedKeys` / `pricing.SnapshotsToDatedTables`), so the
  cost engine keeps the baseline. Unpinned rows still auto-adopt and new models
  still surface. The non-Anthropic vendor-verified rows are pinned — the exact rows
  the Anthropic-family consistency guard cannot cover.
- **`pricing diff` / `pricing show` flag pinned rows.** Both render the raw snapshot
  for provenance, so a pinned row shows the source value and `diff` lists it as
  drift; they now mark those rows `[pinned]` with a legend clarifying the cost
  engine prices at the baseline — so a stale source is never "corrected" into the
  catalog.

### Changed

- **Pricing research (ADR 0002) broadened from Anthropic-only to all providers.**
  Phase 1 hard-scoped snapshots to Anthropic; the hand-maintained non-Anthropic
  baseline had since drifted (Mistral rows stale, DeepSeek off) while nothing
  sourced it. Snapshots now cover **every provider the catalog prices** (OpenAI,
  Anthropic, Mistral, Gemini, Cohere, Groq, DeepSeek, xAI, Perplexity, Cerebras).
  - **Snapshot re-keying.** `Snapshot.Rates` is now keyed `"<provider>/<model>"`
    (e.g. `anthropic/claude-opus-4-8`, `openai/gpt-4o`) instead of bare model,
    matching the multi-provider engine table (`spend.Key{Provider, Model}`). The
    embedded baseline and `Snapshot.Table()` span all providers; a fetched rate
    overrides the correct vendor's baseline row via `MergeOverrides`.
  - **All-provider LiteLLM adapter.** The source maps every `litellm_provider`
    to a tokenops provider (e.g. `vertex_ai`/`gemini`→`gemini`,
    `text-completion-openai`→`openai`), skipping providers the catalog can't
    price (fireworks/together/openrouter multiplexers) so the key-space stays
    aligned with the baseline and the diff stays meaningful.
  - **Guard scoped to Anthropic family.** The ratio heuristics (cache-read ≈10%
    of input, output ≈5× input) run **only on `anthropic/*` rows** — other
    providers price on different curves and would false-flag. All rows still get
    a conservative generic sanity check (cache-read must not exceed input).
  - `refresh`'s diff now surfaces real non-Anthropic drift; `show`/`diff` group
    by provider (keys sort `"<provider>/<model>"`).
- **Corrected Mistral, DeepSeek, o1, and grok-3 catalog rates.** With the source
  now surfacing non-Anthropic drift, each drifted row was cross-checked against the
  vendor's pricing page (never single-sourced). Mistral tracks the current
  `-latest` generation — Large 3 `$0.50/$1.50`, Medium 3.5 `$1.50/$7.50`, Small 4
  `$0.15/$0.60`; `deepseek-chat`/`deepseek-reasoner` → `deepseek-v4-flash`
  `$0.14/$0.28` (cache `$0.0028`), the models those deprecated aliases now resolve
  to (retired 2026-07-24); `o1` gains cache-read `$7.50` and `grok-3` cache-read
  `$0.75`. `codestral`, `gpt-3.5-turbo`, and `gemini-1.5-flash` were confirmed as
  false drift and left unchanged.

### Fixed

- **Current-SKU collision in the LiteLLM adapter.** When several dated SKUs
  collapsed onto one catalog key (e.g. `mistral-large*`), the adapter kept the
  lexically-first id — the *oldest* archived snapshot (`mistral-large-2402`
  `$4/$12`) — manufacturing false drift. It now picks the newest dated SKU, with
  `-latest` as a fallback (LiteLLM's `-latest` aliases are sometimes stale, e.g.
  `codestral-latest`).
- **OpenAI `MMDD` misorder and distinct-SKU bleed.** `gpt-3.5-turbo-1106` (Nov)
  out-ranked `-0125` (Jan) because 4-digit suffixes were compared numerically; a
  4-digit suffix is now treated as a date only when it is a plausible `YYMM`. And a
  broad catalog key no longer absorbs a distinct SKU tier — `grok-3` stopped
  swallowing `grok-3-fast` (`$5/$25`) and `grok-3-mini`.

## 0.40.0 - 2026-07-07

### Added

- **`claude-sonnet-5` in the baseline catalog (`$2/$10/$0.20`).** It was missing —
  the baseline had `claude-sonnet-4-6` ($3/$15/$0.30) but not Sonnet 5, so on the
  offline baseline a Sonnet-5 event wouldn't price. Rate confirmed against **both
  LiteLLM and OpenRouter** ($2 input / $10 output / $0.20 cache-read — Anthropic cut
  Sonnet pricing at 5, like Opus at 4.5). (Events already priced correctly via an
  adopted LiteLLM snapshot; this completes the offline/historical baseline.)

## 0.39.0 - 2026-07-07

### Fixed

- **Reverted the incorrect Opus "correction" from v0.38.0.** v0.38.0 changed Opus
  4.x rates to `$15/$75/$1.50` on the mistaken assumption they follow old-Opus
  pricing. They don't: Anthropic **cut Opus pricing at 4.5** — Opus 4 / 4.1 are
  `$15/$75/$1.50`, but **Opus 4.5–4.8 are `$5/$25/$0.50`** (the value the catalog
  originally had). Confirmed against the LiteLLM feed via `tokenops pricing
  refresh`. Reverted claude-opus-4-{6,7,8} to `$5/$25/$0.50` and restored the
  original tests. The consistency guard couldn't catch this (both `$5` and `$15`
  are internally consistent — cache = 10% of input, output = 5×); only the sourced
  feed could — which is exactly the case ADR 0002's researched pricing is built
  for. It caught a confidently-wrong human edit.

## 0.38.0 - 2026-07-07

### Fixed

- **Corrected Opus 4.x rates in the pricing catalog (were ⅓ too low).**
  `pricing.yaml` priced claude-opus-4-{8,7,6} at `$5/$25/$0.50` per M — exactly ⅓
  of standard Opus 4.x pricing (`$15/$75/$1.50`). Cache-read `$0.50` was
  internally consistent with the wrong `$5` input, so nothing flagged it, and
  every Opus API-equivalent cost (spend summaries, the coach-hook budget) was
  understated **3×**. Fixed to `$15/$75/$1.50`; verified the rest of the Anthropic
  block is internally consistent (cache = 10% of input, output = 5×) — Opus was
  the only miscalibration. This is exactly the silent drift ADR 0002 (below) is
  built to prevent.

### Added

- **Effective-dated cost computation (Phase 2 of
  [ADR 0002](docs/adr/0002-pricing-research-snapshots.md)).** The cost engine is
  now time-aware: each event is priced at the rate card that was **in effect at
  the event's own timestamp**, instead of one flat table. The engine holds a
  series of effective-dated tables (one per pricing snapshot, keyed by
  `fetched_at`) and selects the table with the greatest `EffectiveFrom ≤` the
  event time; events predating every snapshot — or before the first refresh —
  price on the embedded **baseline**, so costing never fails for lack of a dated
  table. Each dated table layers a snapshot's Anthropic rates onto the full
  catalog, so it stays a complete, authoritative rate card (non-Anthropic
  providers keep pricing; a missing model is a miss, not a fall-through).
  Negotiated-rate overrides (`pricing.path`) apply across every period. The
  effective-dated engine is constructed at the composition root (`internal/
  bootstrap`) and in the CLI `spend` / `replay` paths, and takes effect on the
  live proxy ingest path (priced from `Envelope.Timestamp`) and the analytics
  historical recompute of zero-cost events. Construction is fail-soft — any load
  error degrades to the flat baseline engine, so costing never breaks. With no
  refreshes yet, behavior is byte-for-byte identical to before. New API:
  `spend.NewDatedEngine` / `Engine.ComputeAt`, `pricing.EffectiveEngine` /
  `SnapshotsToDatedTables`. See
  [docs/pricing-research.md](docs/pricing-research.md).
- **`tokenops pricing` — researched, sourced, timestamped pricing snapshots
  (Phase 1 of [ADR 0002](docs/adr/0002-pricing-research-snapshots.md)).** Model
  rates stop being a single hand-maintained table and become a series of
  provenance-carrying snapshots fetched from a **pluggable source** (default:
  **LiteLLM** `model_prices_and_context_window.json` — vendor list prices, no
  key). New subcommands:
  - `pricing refresh [--source litellm] [--url] [--dir] [--dry-run]` — fetch →
    run the **consistency guard** (warn) → **diff** against the latest snapshot
    (or baseline) → write a timestamped snapshot. Drift is now **loud**: the
    historical Opus ⅓ error would have shouted
    `claude-opus-4-8 cache_read 0.5 → 1.5 (+200%)` instead of hiding. On a fetch
    error, refresh prints a clear message and exits non-zero **without writing**.
  - `pricing show [--snapshot latest|baseline|<ts>] [--json]` — list a
    snapshot's rates.
  - `pricing diff [--from] [--to]` — diff two snapshots (default
    `baseline → latest`).
  - `pricing lint [--snapshot]` — run the consistency guard (cache-read ≈10% of
    input, output ≈5× input, per family — the exact check that caught Opus) and
    exit non-zero on anomalies, for CI.

  Snapshots are append-only atomic JSON under
  `~/.tokenops/pricing/snapshots/<RFC3339>.json`; the embedded `pricing.yaml`
  remains the always-present offline **baseline**. Fetching uses an injectable
  HTTP client (tests run against `httptest`, never the live network). **No
  hot-path change:** the cost engine still uses the built-in table — snapshots
  are written and inspectable but not yet consulted. Effective-dating is Phase 2.
  See [docs/pricing-research.md](docs/pricing-research.md).

## 0.37.0 - 2026-07-07

### Added

- **Stale-ingestion health warning in `tokenops status` (CLI + `tokenops_status`
  MCP tool).** `status` now flags **runtime** ingestion staleness, not just
  config blockers: when an **enabled** vendor-usage source has emitted **0**
  events in the last **48h**, status emits a soft `warnings` entry
  (`"ingestion stale: <source> has 0 events in the last 48h — if you've been
  using it, reconnect/restart the poller …"`), appends a remediation
  `next_action`, and downgrades a `ready` state to `degraded` (still
  `ready:true`). Stale sources are **never** added to `blockers` — those stay
  config-level hard gates. This closes a real incident where tokenops silently
  served `$0`/stale spend because a `claude_code_jsonl` poller had ingested
  nothing for ~a week while `status` reported healthy. The check is
  store-interface-based and nil-safe (a missing store yields no warnings, never
  a panic), and a `0` count can also legitimately mean the vendor simply hasn't
  been used recently. The enabled-source↔SourceTag mapping is now shared
  (`config.VendorUsageSources`) so `tokenops vendor-usage status` and the health
  check can't drift.

## 0.36.0 - 2026-07-07

### Changed

- **Coach-hook Phase 1.1 (ADR 0001): cumulative session-budget alerts replace
  the flat per-turn threshold.** The `coach-hook` Stop hook now sums each turn's
  **full** API-equivalent cost (input + output + cache-write + cache-read) into a
  running **per-session total** and fires graduated, **latched** alerts as that
  total crosses fractions of a per-session budget — once at **50% / 75% / 100%**,
  then every additional budget over (**200% / 300% / …**). This catches the
  expensive real-world shape the per-turn threshold missed: long, *flat* sessions
  (observed at 7,000–9,300 turns and ~$2,400 API-equivalent) where no single turn
  is extreme but the accumulation compounds. Because the metric is dollars, it is
  **model-agnostic**. The `--threshold`/`--cooldown` flags are replaced by
  `--budget` (default `$50`) on both `coach-hook` and `hooks install`. Still
  **non-blocking** and **fail-open**. `tokenops coach-hook stats` now reports
  spend and alerts-by-tier.

### Added

- **Usage-coaching hooks (Phase 1 of ADR 0001): Stop-hook nudge +
  `tokenops hooks install`.** A new `coach-hook` Claude Code **Stop** hook reads
  the tail of the session transcript after each turn and surfaces a
  **non-blocking** `systemMessage` nudge to compact or start a fresh session when
  a session carries too much reclaimable cache-read context (alerting model
  refined in Phase 1.1, above). It works for clients that never hit the tokenops
  proxy (e.g. Claude Code on a subscription) and **fails open** on every error
  path, so it can never disrupt a session. Cost is priced via the existing
  `spend` catalog.
- **`tokenops hooks install / uninstall / status`** — a scaffolder that
  idempotently merges the tokenops hooks (`--coach`, `--read-guard`) into
  `~/.claude/settings.json` without clobbering unrelated hooks, backs the prior
  file up to `settings.json.bak`, writes atomically, and reports the binary path
  + version being wired in. `--dry-run` previews without writing.

## 0.35.0 - 2026-07-04

### Added

- **Over-time charts in `tokenops fmt analyze --svg`.** Alongside the existing
  bar charts, the analyzer now buckets your Claude Code logs by ISO week and
  renders three time-series SVGs: input vs output tokens per week (a shared
  linear axis, so output correctly reads as a persistent hairline), total
  tokens per week, and context composition (Read/Bash/prose) per week. Built on
  two new dependency-free chart primitives (`Lines`, `StackedArea`); emitted
  only when at least two weeks of timestamped data exist.
- **`--charts` flag** to select which SVGs `--svg` writes: `all` (default), a
  group (`bars` | `timeline`), or a comma-separated list of specific chart ids.
  An unknown name errors with the valid list.

### Fixed

- Over-time chart x-axis labels showed the raw week key (`2026-06-01`) instead
  of a compact `Jun 01`, from a stale date layout left after the weekly-bucket
  refactor.

## 0.34.0 - 2026-07-04

### Added

- **Predictions now read the vendor's own rate-limit meter.** `session_budget`
  and `plan_headroom` previously computed window % from a message count that
  excluded the high-signal sources — so they could report `continue` at
  high confidence while the vendor's own meter read 87%. They now read the
  authoritative snapshot the pollers already store (Anthropic cookie
  five_hour/seven_day %, Codex rate_limits primary/secondary %, Copilot/Cursor
  monthly quota) with its exact reset time, falling back to the heuristic only
  when no snapshot exists. Also scores plans that publish a window but no
  message cap (Claude Code Max/Pro), and gives Copilot/Cursor a real monthly
  overage risk where they previously got "no cap published".
- **Exact tiktoken tokenizer for OpenAI** (o200k_base — GPT-4o / o-series /
  GPT-4.1 / GPT-5). Offline, embedded vocabulary (no runtime download). Replaces
  the char-per-token heuristic — whose ~10-15% error is worst on code and JSON —
  so every downstream $ / savings / headroom figure is exact for the
  highest-volume provider. Other providers stay heuristic.
- **4 more proxy providers (13 → 17):** local runtimes and self-hosted gateways
  — Ollama, LM Studio, LiteLLM, Vercel AI Gateway — each a one-line
  OpenAI-compatible add with its documented default endpoint as the preset.

### Changed

- **`read-guard` ledger is scoped per agent-context, not per session.** A
  subagent reading a file no longer blocks the main agent's later read of it
  (each agent has its own context window). Uses Claude Code's `agent_id`;
  strictly more permissive.
- **Optimizer savings estimates now tokenize the real before/after content**
  instead of a placeholder "canary" string (which returned ~bytes/4 and
  undercounted dense payloads). `retrieval_prune` is scored below the quality
  gate (it's a positional guess with no relevance signal); the dedupe docs no
  longer claim a semantic-embedding seam it doesn't have.

## 0.33.0 - 2026-07-03

### Added

- **9 new proxy-metered providers (4 → 13).** The OpenAI-compatible fleet —
  Groq, DeepSeek, xAI, Perplexity, Fireworks, Cerebras, Together, OpenRouter —
  plus **Cohere** (its own v2 `/v2/chat` + v1 `/v1/chat` normalizer). Each meters
  with its own provider id, has a registered tokenizer, and ships list-price
  rate cards where a static card is accurate. Add any OpenAI-wire-format provider
  in one line via `NewOpenAICompatible`.
- **`tokenops provider set <name>`** now validates the name against known
  providers (an unknown name used to hard-crash the daemon at boot) and accepts
  an omitted URL to bind the built-in preset (`tokenops provider set groq`).
  `provider list` shows all presets for discovery.
- **Passive token attribution for opencode.** The first SQLite-backed vendor
  reader — opens `~/.local/share/opencode/opencode.db` read-only, attributes
  every assistant turn per project/session, multi-provider. Enable with
  `tokenops vendor-usage enable opencode`.
- **Integration coverage docs** — the three-plane model (passive/MCP/proxy), the
  full provider list, and the honest boundaries (Gemini CLI has no local token
  log; Bedrock needs SigV4; hosted agents are out of reach).

## 0.32.0 - 2026-07-03

### Changed

- **Module path moved to `go.klarlabs.de/tokenops`** (vanity import) and the repository to `github.com/klarlabs-studio/tokenops`. GitHub redirects keep old URLs and `go get` working. Homebrew tap unchanged (`brew install felixgeelhaar/tap/tokenops`).

## 0.31.1 - 2026-07-03

### Added

- **`tokenops spend --svg`** emits an input-vs-output ratio chart, and **`fmt analyze --svg`** now also emits `fmt-roi.svg` (formatter savings on your real command output). Four reproducible charts for docs/blogs.

## 0.31.0 - 2026-07-03

### Added

- **`tokenops fmt analyze --svg <dir>`** renders the analysis as
  self-contained SVG charts (composition + reads). Text uses `currentColor`
  so an inlined chart themes with its page; output is deterministic and
  reproducible from the CLI.

## 0.30.1 - 2026-07-03

### Changed

- **`read-guard stats` now explains the re-read breakdown.** Instead of a
  bare "0 reclaimable", it splits repeat reads into reclaimable (unchanged
  full re-read — real waste), post-edit (file changed since last read — not
  waste), and ranged (intentional partial re-read). Observe mode is now
  informative about *why* a re-read was or wasn't blockable, so the decision
  to switch to active mode is grounded.

## 0.30.0 - 2026-07-03

### Added

- **`tokenops read-guard` — reclaim the Read re-read waste, no proxy needed.**
  A Claude Code PreToolUse hook that prevents redundant file re-reads at the
  source, so it works for clients whose traffic never reaches the tokenops
  proxy (e.g. Claude Code on a subscription). A re-read is redundant when the
  same file is read in full more than once in a session and is unchanged
  since (cheap mtime+size fingerprint — the file is never opened); ranged
  reads (offset/limit) are always allowed.
  - **observe** mode (default): logs what it would block + reclaimable
    tokens from live sessions, zero interference.
  - **active** mode: denies the redundant re-read and tells the model to use
    the copy already in its context.
  - `tokenops read-guard hook` prints the settings.json block;
    `tokenops read-guard stats` shows reclaimable/reclaimed tokens.

- **Self-wiring fmt learning** — the fmt loop now derives its signal from the
  Claude Code logs (`~/.claude/projects`) that already exist, with no daemon,
  no wrapped commands, and no setup:
  - **`tokenops fmt analyze`**: reads your logs, reports context composition
    (Read vs Bash vs prose) and dry-runs every Bash command's output through
    the formatter engine to estimate what fmt would save on your real
    traffic. Nothing is persisted — only sizes.
  - **`tokenops fmt learn`** now folds in that log-derived signal by default
    (`--no-jsonl` to opt out), so it reflects real usage out of the box.
  - **MCP `tokenops_fmt_analyze`** (+ `tokenops_fmt_learn` folds in log
    signal) expose the same to agents.
  - **Read-side diagnostic**: `fmt analyze` now also measures the biggest
    context slice — Read (file content) — surfacing re-read waste (same file
    re-read in a session), byte-identical duplicate content, ranged-read
    hygiene, and the most-re-read files. Re-reads/dupes are a
    context-management issue (addressable by the proxy dedupe/context-trim
    optimizers), not a formatter one; the diagnostic makes the size visible.

### Fixed

- **Log discovery walked only one directory level** — the vendor-usage
  file-finder globbed `<root>/*/*.jsonl`, silently missing sessions nested
  deeper (worktrees, sub-checkouts). The fmt analyzer now walks recursively,
  seeing all sessions instead of a small fraction.
- **Compound-command attribution** — `cd /path && go test` attributed the
  command output to `cd` (no-output prefix) instead of `go`, hiding
  compressible output from the right formatter. The analyzer now splits the
  chain and skips `cd`/`export`/`source` prefixes.

### Added

- **`fmt` catalog fast-follow → 51 formatters / 57 command tokens.** New
  formatters: `nomad`, `packer`, `gem`, `swift`, `nix`, plus `oc`
  (OpenShift) routed to the `kubectl` formatter as an alias. Each ships
  golden critical-line survival + monotonic tests and is enrolled across
  every plane. `vault` intentionally deferred (its output carries secret
  values; compressing secrets is poor optics for little gain).
- **Proxy-plane regression test**: `command_fmt` is now covered by an
  end-to-end test that runs a realistic Anthropic `tool_result` through the
  default pipeline and asserts a `command_fmt` event with real savings.

## 0.28.1 - 2026-07-03

### Documentation

- Document the `tokenops fmt` command-output compression subsystem across
  the docs site and README: CLI reference (`fmt` / `bench` / `hook` /
  `recover` / `learn`), the `optimizer.command_fmt` configuration section
  (loss levels, user-defined formatters, learn loop), the new formatter and
  fmtlearn packages in the architecture doc, and the MCP tool count (26).

## 0.28.0 - 2026-07-03

### Added

- **User-extensible `fmt` catalog + local self-tuning** — the learning loop
  now closes on the user's own machine, no maintainer or recompile needed:
  - **Config-defined formatters** (`optimizer.command_fmt.formatters`): add
    or override a formatter for any command by declaring `critical` regexes
    (always preserved) and per-level `drop` regexes — no rebuild. User rules
    run through the same `enforceCritical` guard as built-ins, so a mistaken
    drop rule can never remove a critical line.
  - **`tokenops fmt learn --apply`**: writes the safe part of the learning
    report (per-command loss-level `overrides`) into the user's config
    locally. Level tuning never touches critical rules, so it is safe to
    auto-apply. New-formatter candidates are printed as a paste-ready config
    stub, never auto-written.
  - **MCP `tokenops_fmt_learn`**: exposes the advisory learning report to
    agents so they can drive catalog improvements programmatically.
  - Shared `fmtindex` adapter backs the CLI and MCP so both read one index.

## 0.27.0 - 2026-07-03

### Added

- **`tokenops fmt` catalog expanded to 46 commands / 51 tokens.** 29 new
  deterministic formatters across every category an agent shells out to:
  - RTK parity: `gh`, `jest`, `vitest`, `golangci-lint`, `ruff`, `rubocop`,
    `prettier`, `biome`, `rspec`, `playwright`, `uv`, `bundle`, `pulumi`.
  - Differentiators RTK does not support: `bazel`, `ansible`
    (+`ansible-playbook`), `helm`, `dotnet`, `aws`, `gcloud`, `az`
    (multi-cloud — RTK is AWS-only), `sbt`, `mix`, `composer`,
    `dnf` (+`yum`), `brew`, `flyway`, `alembic`, `cmake`, `ninja` — the
    noisiest build / config-mgmt / cloud / migration tooling RTK ignores.
  Cloud CLIs (`aws`/`gcloud`/`az`) pass JSON through untouched (the generic
  dedup is unsafe on structured output) and only compress table/text.
  Every formatter ships golden critical-line survival + monotonic-reduction
  tests and is enrolled across every plane (CLI, hook, proxy, bench).
  Benchmark over the checked-in corpus (47 KB, 46 commands): balanced 57%,
  aggressive 68% aggregate; standouts helm 84/92%, go test 86/92%, npm 84%,
  pip 82%, uv 78%, mvn 76%, bazel 72%.

## 0.26.0 - 2026-07-02

### Added

- **Deterministic command-output compression** (`tokenops fmt`): wraps a
  command, compresses its stdout with a per-command formatter before the
  output enters the agent context, and preserves the full output in
  `~/.tokenops/recovery/`. Two invariants hold at every loss level:
  determinism (pure `(raw, level)` function) and critical-line survival
  (errors / failures / changed state are never dropped — a formatter that
  would drop one falls back to raw passthrough). Loss is configured per
  command (`optimizer.command_fmt.default` + `overrides`:
  conservative | balanced | aggressive).
  - Formatter catalog (17): `git status`, `go test`, `npm`, `cargo`,
    `pytest`, `docker build`, `kubectl`, `terraform`, `pip` (+`pip3`),
    `tsc`, `eslint`, `yarn` (+`pnpm`), `make`, `mvn`, `gradle`,
    `apt` (+`apt-get`), `curl -v`, plus an always-safe generic noise
    scrub. Formatters can register command aliases.
  - `tokenops fmt bench --corpus <dir>` measures savings over captured
    command outputs; `tokenops fmt hook` emits env-gated shell wrappers
    (RTK-style auto-rewrite, opt-in via `TOKENOPS_FMT=1`).
  - `--emit` (or `command_fmt.emit_events`) records an OptimizationEvent
    (kind `command_fmt`) so the dashboard and scorecard count the savings.
  - Proxy plane: a `command_fmt` optimizer compresses tool-output blocks
    (Anthropic `tool_result`, OpenAI `role:tool`) in the request body via
    content-sniffing, so agents without the shell hook still benefit.
  - **Offline learning loop** (`tokenops fmt learn`): mines the recovery
    index (compression + re-access records) to propose where the catalog
    should improve — next formatters to write (commands falling back to
    the generic scrub, ranked by raw bytes) and possible over-compression
    (commands whose compact output is re-fetched often via
    `tokenops fmt recover <id>`, with loss-level tuning hints). Advisory
    only: the formatters stay deterministic; proposals become
    corpus-gated code changes, never runtime self-modification.

## 0.25.1 - 2026-06-12

### Security

- `tokenops config show` (and the MCP/dashboard config surfaces) no
  longer print secrets: the CLI bypassed the redaction snapshot
  entirely, and redaction only covered OTel headers. All secret fields
  (dashboard admin token, Anthropic admin key, claude.ai session key,
  Cursor cookie, Copilot OAuth token) are now masked through a single
  Config.Redacted() path.

## 0.25.0 - 2026-06-11

### Added

- **Mistral proxy support**: `/mistral/` route to api.mistral.ai with
  request normalization for chat-completions and FIM (codestral), plus
  a heuristic tokenizer — Mistral traffic is now observable and
  costable through the proxy like OpenAI/Anthropic/Gemini.
- **Gemini plan**: `gemini-ai-premium` (Google One AI Premium) catalog
  entry; windowless (Google publishes no caps).

### Fixed

- Copilot, Cursor, and Anthropic admin-API poller events are marked as
  quota/aggregate snapshots and no longer count as user messages in
  plan window math.

## 0.24.0 - 2026-06-11

### Added

- **API-equivalent spend metric**: `tokenops spend` shows
  `api equivalent` and `tokenops_spend_summary` returns
  `api_equivalent_usd` — what the window would have billed at API list
  prices, including plan-covered traffic. On flat plans this is the
  shadow value the subscription absorbed.
- **Budget basis** (`budgets[].basis: spend | equivalent`): flat-plan
  deployments can watch list-price value instead of real spend (which
  is ~$0 forever on a subscription). `tokenops_budget_set` accepts
  `basis`. Equivalent budgets get threshold alerts (no forecast).
- **Central plan stamping**: events are stamped `plan_included` at the
  storage sink for any provider with a plan bound — covers every
  poller (cursor, copilot, anthropic-cookie, admin API), the proxy
  observer, and future emitters without per-emitter wiring.

### Fixed

- CI/release workflows forced to Node 24 ahead of GitHub removing
  Node 20 from runners (2026-09-16).

## 0.23.1 - 2026-06-11

### Fixed

- Vendor-usage pollers (claude-code JSONL, codex JSONL, legacy stats
  cache) stamp events `plan_included` when a flat-rate plan is bound to
  their provider — subscription-covered usage is no longer repriced at
  API list rates by spend summaries, and budget alerts stop firing on
  spend that never billed. Only newly ingested events are stamped.
- Plan window math no longer counts assistant-turn events against the
  vendor's messages meter (one prompt fans out into many tool-use
  turns; the meter was reading ~10-50x over). Their tokens still count.
- Spend forecasts clamp at zero instead of predicting negative spend
  on declining trends.

## 0.23.0 - 2026-06-11

### Added

- **`tokenops_mode` ensures a daemon**: setting `active` via MCP probes
  the daemon's advertised URL and, when nothing answers, starts
  `tokenops start` detached with the freshly written config (logs to
  `daemon.log` next to `events.db`). A running daemon is left alone
  with a restart reminder. Active mode can no longer be a silent no-op.

### Fixed

- Plan-included and trial events are excluded from cost recompute and
  unpriced-model detection: recompute could invent list-price spend
  for flat-rate traffic (aggregation drops the cost source), and the
  `mcp-session` pseudo-model tripped the unpriced-model warning on the
  watcher's first tick.

## 0.22.0 - 2026-06-10

### Added

- **Claude Fable 5 pricing** (`claude-fable-5*`, $10/$50 per MTok) plus
  `claude-opus-4-8` / `claude-opus-4-6` rows in the price catalog.
- **Data-driven pricing catalog**: rates moved from Go constants to an
  embedded `pricing.yaml`; `pricing.path` (or `TOKENOPS_PRICING_PATH`)
  layers operator overrides — price newly released models or negotiated
  rates without upgrading.
- **Unpriced-model warnings**: `tokenops spend` and the
  `tokenops_spend_summary` MCP tool flag models the catalog cannot
  cost instead of silently under-reporting totals.
- **Model routing rules** (`optimizer.routing_rules`): replay reports
  per-rule "would save $X" rollups so a route can be validated on real
  history offline.
- **Operating modes** (`mode: passive | active`): active mode applies
  routing rules to live proxied traffic (recorded as applied
  optimization events; original requested model preserved in the
  observation) and runs a background spend watcher that evaluates
  `budgets` (calendar windows + Holt forecast) every `watch.interval`
  and flags unpriced models.
- **MCP config tools**: `tokenops_mode`, `tokenops_budget_set`,
  `tokenops_routing_rule_set` mutate the same config.yaml the CLI
  manages, validated before every write.
- **Configurable waste thresholds** (`coaching.context_limits`):
  per-workflow-prefix overrides for the waste detector; operator
  config now wins over the built-in claude-code/codex profiles.
- Prompt-coach savings priced at each turn's observed model instead of
  a flat claude-opus-4-7 assumption.

### Fixed

- `claude-opus-4-7` list price corrected from $15/$75 to $5/$25 per
  MTok (Opus 4.1-era rate had been carried over). Recomputed
  historical opus-4-7 costs drop ~3x; coaching savings projections
  shrink accordingly.
- Demo fixture rates now derive from the catalog (the seeded
  gemini-2.5-pro output rate had drifted).

## 0.21.1 - 2026-05-17

### Fixed

- Three F-grade scorecard artifacts that didn't reflect operator
  behaviour:
  - **TEU**: zero optimisation events now means "N/A" (default
    15% applied), not 0% F. The optimiser only runs on
    proxy-routed traffic; JSONL-only mode shouldn't trip an F.
  - **SAC**: `rowToEnvelope` now syncs `PromptEvent.SessionID`/
    `WorkflowID`/`AgentID` from the indexed columns when the
    payload JSON field is empty, so column-side backfills
    (e.g. `claude-code-jsonl` v0.14.3) propagate to the
    scorecard reader. `defaultExcludedSources` also excludes
    `vendor-usage-anthropic` and `claude-code-stats-cache`
    from the denominator — those sources don't carry per-session
    attribution by design.
  - **CGR**: `filterAutonomousLoopSentinels` drops `continue` /
    `proceed` / `keep going` prompts when they repeat >5x in the
    same session (`/loop` dynamic-mode pacing, not human acks).
- Overall grade improves from F → B on real 30-day data.

## 0.21.0 - 2026-05-17

### Added

- **`tokenops task list --metrics`** enriches every task with
  the events-store rollup over its `[StartedAt, CompletedAt]`
  window:
  - Turns (count of prompt events)
  - InputTokens / OutputTokens
  - CostUSD (cache-aware)
  - TTFUOSeconds (time from task start to first assistant turn)
  - Duration (wall-clock span)
  - CostPerTurn
  JSON output ships `{"tasks": [...], "metrics": {id → Metrics}}`
  for MCP-host UIs. The task boundary primitive from v0.20.0
  now produces task-attributed metrics agents and operators
  can act on (cost-per-task, iteration depth, TTFUO).

## 0.20.0 - 2026-05-17

### Added

- **Wired CHR/CGR/RGR data sources** so `tokenops scorecard`
  actually surfaces the v0.19.0 KPIs against the live store.
  CHR is computed in `scorecard.Compute` from PromptEvents
  (with legacy-attribute fallback for pre-v0.14.2 envelopes
  so dashboard + scorecard agree on the same %). CGR + RGR
  are computed in the CLI from JSONLs via `prompts.Extract` +
  `prompts.Analyze` (new regenerate detector matches "try
  again / redo / wrong / not what I wanted").
- **Two more scorecard KPIs** alongside the v0.19.0 trio:
  - **TCS — Tool Success Rate**: % of tool calls returning
    without `is_error`. Higher is better. Thresholds
    ≥95 / ≥85 / ≥70.
  - **DAR — Destructive Action Rate**: % of bash invocations
    matching the destructive allow-list (rm -rf, force-push,
    drop table, …). Lower is better. Thresholds
    ≤0.5 / ≤2 / ≤5%.
  - New `internal/contexts/coaching/tools/` package walks
    Claude Code JSONL `tool_use` + `tool_result` blocks and
    pairs them by `ToolUseID`.
- **`tokenops task start|done|list`** — operator-marked task
  boundaries written to `$HOME/.tokenops/tasks.jsonl`.
  Unblocks task-level metrics (cost-per-task, iteration
  depth, TTFUO) by giving the operator a way to declare a
  unit of work. The `task` command tree is the foundation;
  scorecard wiring for task-attributed metrics follows.

## 0.19.0 - 2026-05-17

### Added

- **Tangible turn-savings on the coach** (`tokenops coach prompts`):
  new `prompts.TurnStats` and `prompts.ComputeTurnStats` walk the
  same JSONL tree the extractor uses, sum assistant-turn input/
  output/cache tokens, and price them at cache-aware rates. The
  CLI renderer projects each `Recommendation`'s monthly turn
  savings into tokens / dollars / hours of attention. Header line
  shows the operator's per-turn rollup so they can verify the
  assumption.
- **Three new scorecard KPIs** alongside FVT/TEU/SAC:
  - **CHR — Cache Hit Ratio**: % of input tokens that are cache
    reads. Higher is better. Thresholds ≥90 / ≥70 / ≥50.
  - **CGR — Confirmation Gate Rate**: % of user prompts that are
    pure acks. Lower is better. Thresholds ≤10 / ≤20 / ≤30.
  - **RGR — Regenerate Rate**: % of user prompts rejecting prior
    agent output. Lower is better. Thresholds ≤5 / ≤10 / ≤20.
  - New `AgentKPIInputs` struct + `NewWithAgentKPIs` constructor;
    legacy `New(...)` stays backward-compat. `MarshalJSON` omits
    agent KPI blocks whose Grade is empty so consumers don't see
    a phantom F for a metric that was never computed.

### Changed

- `Findings.Recommendations` (v0.18) gains tangible savings via
  the renderer; the structured Recommendation shape is unchanged.
- Scorecard JSON output gains optional `cache_hit_ratio`,
  `confirmation_gate_rate`, `regenerate_rate` fields. Agent KPI
  blocks render in `String()` only when their Grade is set.

## 0.18.0 - 2026-05-17

### Added

- `tokenops coach prompts` now emits ranked structured
  `Recommendation`s grounded in the operator's own data.
  Each rec includes: `id`, `title`, `why` (data-grounded
  explanation), `evidence` (2-3 sample prompts pulled from
  the operator's JSONLs), `frequency`, `impact_score`,
  `estimated_monthly_turns_saved`, and `before`/`after`
  rewrite templates. Five rules ship: `scope_vague_directives`
  ("fix all" / "do it" / "merge it"),
  `reduce_confirmation_loops` (acks > 15%),
  `stop_repeating` (prompt issued 3+ times),
  `cite_file_paths` (≥30% short prompts without a file
  reference, min 20 prompts), `front_load_context`
  (fallback when nothing else fires).
- CLI now leads with a `BIGGEST WIN` panel — the
  highest-impact recommendation with evidence quotes and a
  before/after example — followed by an `ALSO WORTH FIXING`
  numbered list. JSON output (`--json`, MCP tool result)
  ships the structured slice for agent hosts to render
  their own UI.

### Changed

- `Findings.Recommendations` schema changed from `[]string`
  to `[]Recommendation`. The MCP tool result reflects the new
  shape — agent hosts parsing the JSON should expect the new
  fields.

## 0.17.1 - 2026-05-16

### Fixed

- Dashboard KPI tiles overflowed on real-data viewports because
  raw token counts (`6,039,345,464`) didn't fit the 140px tile
  font budget — values ellipsized mid-digit. Tokens / requests
  now render compact (`6.04B`, `14.1k`, `6.95M`); total cost
  drops cents on the tile. Full grouped values surface via the
  `title=` hover attribute. Chart Y-axes use the same compact
  formatter so cost reads `$1.0k / $800 / …` and tokens
  `600.00M / 400.00M` instead of clipped `0.0000 / 00,000`.
- Hero video on the docs site didn't autoplay on Chrome. The
  `<source>` fallback was a webm with `duration=N/A` (matroska
  doesn't write per-stream duration by default), which raced
  with the mp4 fetch and stalled playback. Dropped the webm
  source from the hero markup; mp4 is the only player spec
  advertised. New mp4 uses `yuv420p` + `color_range=tv` + Main
  level 4.0 + faststart for Safari/iOS autoplay too.

## 0.17.0 - 2026-05-16

### Added

- **`tokenops coach replies`** — output-side sibling of `coach prompts`.
  Walks the same Claude Code / Codex JSONLs but extracts assistant
  replies and scores per session:
  - article density (`a/an/the` per word)
  - filler density (`just / really / basically / sure / ...`)
  - average word length
  - code-block ratio
  - **caveman-likely verdict** + rough estimated token savings
  Use to detect when an output-compression skill (e.g. caveman) is
  engaged and how many tokens it suppressed.

### Changed

- **Anthropic admin-API poller** now stamps `agent_id = "anthropic-admin"`
  and a synthetic `workflow_id = "anthropic-admin[:k=<api_key>][:w=<workspace>]"`
  on emitted prompt events. Previously these vendor-rolled rows had
  no attribution at all, which dragged scorecard SAC down to ~1%.
  Distinct API keys / workspaces still get distinct workflow buckets
  so the analyzer can group them.

## 0.16.0 - 2026-05-16

### Added

- **Per-project breakdown** for Claude Code JSONL: poller stamps
  `agent_id = "claude-code:<project>"` and
  `workflow_id = "claude-code:<project>:<session>"` on each event,
  derived from the JSONL parent directory name. Enables
  `group=agent` rollups to answer "which project burns the most".
- **Cache hit-rate API + dashboard tile**: `GET /api/spend/cache_stats`
  returns `total_input_tokens`, `cached_input_tokens`,
  `uncached_input_tokens`, `output_tokens`, `cache_ratio`. New
  "Cache hit: XX.X%" tile alongside the existing KPI tiles.
  COALESCEs payload + legacy attributes so old events surface the
  split without a re-ingest.
- **Waste-detector profiles**: `ProfileFor("claude-code:...")` →
  900k peak / 2M growth; `ProfileFor("codex:...")` → 250k peak /
  500k growth. Stops every Code/Codex session tripping the
  short-workflow defaults (32k/16k). Operator-supplied Config
  values still win.
- **Codex parity** for the v0.14.x JSONL improvements:
  `codexjsonl` poller now sets `SessionID`, `AgentID="codex"`,
  `WorkflowID="codex:<session>"`, and `CachedInputTokens` on
  every PromptEvent. The cached split was being dropped, so
  Codex Plus/Pro users had the same ~10x cost over-estimate
  Claude Code users had before v0.14.2.
- **Prompt-coach cross-provider**: `tokenops coach prompts` auto-
  discovers both `~/.claude/projects` and `~/.codex/sessions`.
  Parses each dialect (flat `role=user` for Codex vs nested
  `type=user`+`message.content` for Claude Code). Filename
  timestamp fallback when Codex records omit their own.

## 0.15.0 - 2026-05-16

### Added

- `tokenops coach prompts` subcommand + `tokenops_coach_prompts`
  MCP tool. Heuristic prompt-quality feedback for Claude Code
  users: walks `~/.claude/projects/**/*.jsonl`, extracts
  human-typed turns, and reports length distribution
  (under-5-word, 5-15, 15-50, 50-200, >200), vague-short
  count (<15 chars / ≤3 words), pure acknowledgements
  (yes/no/ok/continue), short questions, single-sentence
  no-context, and repeated prompts (verbatim 3+ times) — with
  concrete recommendations tuned to the operator's pattern.
  Privacy: prompt text is read at scan time and is never
  persisted to the event store.

## 0.14.3 - 2026-05-16

### Added

- Claude Code JSONL events now carry `session_id` and a
  synthesized `workflow_id = "claude-code:<session>"` on the
  indexed columns (not just in attributes). `tokenops replay`,
  the workflow reconstructor, and the waste detector can now
  resolve a Claude Code session by ID — coach surface was dark
  for JSONL data because session_id only landed in the
  attributes map. Existing events can be backfilled with one
  UPDATE statement (see migration note below).

### Migration

Operators with pre-existing JSONL events should backfill via:

```sql
UPDATE events
SET session_id  = json_extract(attributes, '$.session_id'),
    workflow_id = 'claude-code:' || json_extract(attributes, '$.session_id')
WHERE source = 'claude-code-jsonl' AND session_id IS NULL;
```

After the backfill, `tokenops replay --workflow 'claude-code:<session>'`
surfaces coach findings (oversized context, runaway growth).

## 0.14.2 - 2026-05-16

### Fixed

- Dashboard cost over-estimated by ~9x against real Claude Code
  data because cache reads were billed at the new-input rate
  ($15/M for claude-opus-4-7) instead of the cache rate
  ($1.50/M). For agent workflows that reuse context heavily,
  >99% of input tokens are cache reads — order-of-magnitude
  difference. claudecodejsonl poller now writes the cache_read
  split into `PromptEvent.CachedInputTokens`; aggregator
  recompute reads it back from the payload JSON, with a fallback
  to `attributes.cache_read_input` so existing events re-cost
  correctly without a re-ingest. Verified end-to-end on a
  cache-heavy 7-day window: the figure dropped about ninefold.

## 0.14.1 - 2026-05-16

### Fixed

- Dashboard `TOTAL COST` tile rendered as `$0.00` whenever data
  came from vendor-usage-jsonl sources (Claude Code JSONL, Codex
  JSONL). Those readers ship token counts but no prices, so
  `cost_usd` lands as NULL in the events table. `AggregateBy`
  already recomputed via `spend.Engine` (the per-bucket chart was
  correct); `Summarize` bypassed that path. Fix: after the SUM,
  group unpriced events by `(provider, model)`, sum tokens, call
  `spend.Engine.Compute` once per group. Linear pricing → one
  call per group matches the per-event sum exactly. SQL filter
  uses `cost_usd IS NULL OR cost_usd = 0` because the serializer
  stores 0 floats as NULL under STRICT mode.

## 0.14.0 - 2026-05-16

### Added

- `tokenops vendor-usage enable <source>` subcommand. Writes a
  vendor-usage source's config block to the active config file so
  operators don't hand-edit YAML to flip the v0.13.0 pollers on.
  Sources: `anthropic-cookie`, `cursor`, `github-copilot`,
  `codex-jsonl`, `claude-code-jsonl`, `anthropic-admin`. Secrets
  accept env-var fallback (`TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY`,
  `TOKENOPS_CURSOR_COOKIE`, `TOKENOPS_COPILOT_OAUTH_TOKEN`,
  `TOKENOPS_ANTHROPIC_ADMIN_KEY`) so they don't leak through shell
  history. `--disable` flips `Enabled=false` without clearing
  persisted secrets — toggling on/off is one flag, not a re-paste.

## 0.13.0 - 2026-05-16

### Added

- Codex CLI JSONL reader (`vendor_usage.codex_jsonl.enabled: true`).
  Parses `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`,
  emits one PromptEvent per `event_msg` carrying OpenAI's
  authoritative `rate_limits` block (5h primary + weekly secondary
  used_percent + resets_at). 30s scan, dedup by (sessionID,
  sequence). `signal_quality` promotes to HIGH on any observation.
  Source tag `codex-jsonl`; envelope IDs `cdx-<sha8>`.
- GitHub Copilot quota poller (`vendor_usage.github_copilot.enabled:
  true`). Calls `api.github.com/copilot_internal/user` with the
  OAuth token discovered from `~/.config/github-copilot/apps.json`
  (or `hosts.json`) — same file the IDE plugins use. Returns live
  `quota_snapshots` (`chat`, `premium_interactions`) with
  `percent_remaining`, `entitlement`, `overage_count`, `unlimited`.
  Two-minute scan, dedup by `timestamp_utc`. Source tag
  `github-copilot`; envelope IDs `ghc-<sha8>`. `ProviderGitHub`
  added to eventschema.
- Cursor `/api/usage` poller (`vendor_usage.cursor.{enabled, cookie,
  user_id}`). Calls `cursor.com/api/usage?user=<id>` with the
  `WorkosCursorSessionToken` cookie the IDE uses; flat-map response
  yields one envelope per model row. Two-minute scan. Source tag
  `cursor-web`; envelope IDs `cur-<sha8>`. `ProviderCursor` added
  to eventschema.
- Anthropic cookie scraper (`vendor_usage.anthropic_cookie.{enabled,
  session_key}`). Polls `claude.ai/api/organizations` then
  `/api/organizations/{org_id}/usage` with the operator's browser
  `sessionKey` — same data Anthropic's own UI shows (5-hour, 7-day,
  7-day-opus utilization + extra_usage). Org ID auto-resolves on
  first scan; Chrome UA to bypass Cloudflare. Five-minute scan,
  dedup by `five_hour.reset_at`. **First and only source of the
  official Claude Max weekly utilization %.** Source tag
  `anthropic-cookie`; envelope IDs `ack-<sha8>`.
- `SignalSourceCodexJSONL`, `SignalSourceCopilot`,
  `SignalSourceCursor`, `SignalSourceAnthropicCookie` added to the
  `signal_quality` enum. `ClassifySignal` precedence:
  `VendorAPIWired > AnthropicCookie > ClaudeCodeJSONL >
  CodexJSONL > Copilot > Cursor > Proxy(high/medium) >
  ClaudeCodeCache(medium, deprecated) > MCPPings(low)`.
- `tokenops vendor-usage status` surfaces all four new source rows
  with config-hint copy that names the exact YAML key to set.

## 0.12.0 - 2026-05-16

### Added

- Claude Code JSONL reader (`vendor_usage.claude_code_jsonl.enabled:
  true`). Parses `~/.claude/projects/<project>/<session>.jsonl` —
  Claude Code's live per-turn conversation record — and emits one
  PromptEvent per assistant turn with the full `message.usage`
  block (`input_tokens`, `output_tokens`, `cache_read_input_tokens`,
  `cache_creation_input_tokens`, `service_tier`). Tolerates 15MB+
  files (4MB scanner buffer), skips user/tool turns and zero-token
  rows. 30s scan, dedup by Anthropic message_id. `signal_quality`
  promotes to HIGH on any observation. Source tag
  `claude-code-jsonl`; envelope IDs `ccj-<sha8>`.
- `SignalSourceClaudeCodeJSONL` added to the signal enum;
  `claude-code-stats-cache` reader deprecated (still functional;
  emits at `medium` confidence with an upgrade-path hint).

### Changed

- The v0.10.2 `claude-code-stats-cache` reader is deprecated. On
  active Claude Code users `~/.claude/stats-cache.json` lags by
  days; the JSONL reader is the supported source going forward.

## 0.11.0 - 2026-05-14

### Added

- Per-model stacked area chart on the dashboard cost panel. Top-5
  legend with a "+N more" chip; colour scale is `d3.schemeTableau10`
  ordered by sorted model name so hues stay stable across refresh.
  Single-model filter falls back to the line view.
- `tokenops vendor-usage backfill --hours N` one-shot pull of
  historical Anthropic Admin API usage into the local store.
  Deterministic envelope IDs so re-running or running alongside the
  live poller never double-counts. `--dry-run` prints would-insert
  count without writing.
- `tokenops dashboard rotate-token` CLI command that mints a fresh
  32-byte URL-safe secret, atomic-writes to
  `~/.tokenops/dashboard.token`, and reminds the operator to restart
  the daemon. Fails when `config.dashboard.admin_token` is set
  explicitly (config value wins).
- Mistral Le Chat Pro + Codex Plus plan catalog entries.
  `eventschema.ProviderMistral` plus `mistral-large/medium/small +
  codestral` rows in the default spend pricing table.
- Dashboard filter selections persist in localStorage so window /
  provider / model picks survive page refresh. Quiet failure on
  storage exceptions (Safari private mode etc).
- Inline SVG favicon for the dashboard browser tab.

## 0.10.5 - 2026-05-13

### Added

- Dashboard gains provider + model filter dropdowns. Options
  auto-populate from `group=provider` / `group=model` series queries
  so the list always reflects what the store actually contains in
  the current window.

## 0.10.4 - 2026-05-13

### Added

- `tokenops vendor-usage status` CLI command. Reads config + counts
  source-tagged envelopes per source over a configurable window;
  prints a hint per source pointing at the missing config knob when
  a source is dark. Offline (no daemon HTTP call).

## 0.10.3 - 2026-05-13

### Added

- Dashboard auth: `/dashboard` + `/api/*` require a shared-secret
  token (`/healthz`, `/readyz`, `/version` stay public). Three
  credential channels accepted in constant time: `Authorization:
  Bearer`, `?token=…` query param, session cookie. Browser-style
  query-param auth mints a session cookie and 303s to a clean URL.
- Token auto-managed: 32-byte random value persisted at
  `~/.tokenops/dashboard.token` on first start. Override via
  `cfg.Dashboard.AdminToken`.
- `tokenops_dashboard` MCP tool returns a URL with the token
  pre-attached so the operator gets a one-click authenticated visit.

## 0.10.2 - 2026-05-13

### Added

- Vendor /usage ingestion lands. Two new signal sources:
  - **Claude Code stats cache reader.** Reads
    `~/.claude/stats-cache.json` on a tick, emits one `PromptEvent`
    per (date, model) delta with `Source="claude-code-stats-cache"`.
    Promotes `signal_quality` to medium with an explicit caveat
    that the schema is undocumented and granularity is daily-only.
  - **Anthropic Admin API poller.** Calls
    `/v1/organizations/usage_report/messages` every 5min, emits one
    `PromptEvent` per (bucket, model) cell with
    `Source="vendor-usage-anthropic"`. Promotes `signal_quality` to
    high. Requires `sk-ant-admin-*` key.
- `config.vendor_usage.{claude_code,anthropic}` blocks wire the
  pollers. Both are off by default.

### Notes

- Per Anthropic's documented API surface, the Admin API covers
  metered API usage only. Claude Max plan window state has no
  documented endpoint and remains heuristic.

## 0.10.1 - 2026-05-13

### Added

- Daemon advertises itself as `tokenops.local` over zeroconf on
  Start. Dashboard URL becomes `http://tokenops.local:7878/dashboard`
  instead of a bare loopback address. The MCP `tokenops_dashboard`
  tool prefers the mDNS URL; falls back to `127.0.0.1` when `.local`
  resolution isn't available.
- URL hint file (`~/.tokenops/daemon.url`) gains a `local_url`
  field. Advertised IPs match the bind address: loopback-only
  listener publishes `127.0.0.1` so the `.local` hostname resolves
  on-host; wildcard / LAN listener publishes every non-loopback
  interface.

## 0.10.0 - 2026-05-13

### Added

- **Interactive Vue + D3 dashboard** served by the daemon at
  `/dashboard`. Cost-over-time line, tokens-per-bucket stacked bar,
  KPI tiles, 15s auto-refresh.
- **Inline SVG charts in MCP responses.** `tokenops_session_budget`
  leads with a coloured headroom gauge (green / amber / red);
  `tokenops_burn_rate` ships a sparkline. Rendered inline in
  markdown so every MCP client shows them today.
- **Auto-detect on init.** `tokenops init --detect` sniffs Claude
  Code, Claude Desktop, Cursor, ChatGPT Desktop, and standard
  API-key env vars, then prints the exact `tokenops plan set …`
  commands for what it found.
- **Dynamic-cheapest coaching router.** Coaching pipeline picks the
  lowest blended-rate model per provider from the pricing table at
  runtime. No hardcoded model names.
- **`tokenops_dashboard` MCP tool** returns a clickable URL to the
  local dashboard, or a structured `{error, hint}` payload when the
  daemon is not running.

## 0.9.4 - 2026-05-13

### Added

- `tokenops spend --include-demo` mirrors the MCP tool flag so CLI
  users can opt back into seeded data without editing the filter
  struct. Default (no flag) keeps demo events excluded.

### Fixed

- `tokenops status` falls back to the same self-report the MCP
  tokenops_status tool emits when the daemon is unreachable.
  Operators see `blockers[]`, `next_actions[]`, version, and a
  `run tokenops start` hint instead of a raw connection-refused
  error. MCP-only deployments no longer hit a confusing CLI dead
  end.

## 0.9.3 - 2026-05-13

### Fixed

- Warming-up scorecard JSON drops the empty KPI blocks. Response now
  contains only `generated_at`, `overall_grade: warming_up`, optional
  `baseline_ref`, and `checklist`. Dashboards/agents see exactly what
  they need to render the empty state.

## 0.9.2 - 2026-05-13

### Fixed

- Scorecard now excludes `source=mcp-session` events from FVT/TEU/SAC
  compute, matching the demo-data isolation done in 0.8.1. Installs
  whose only real data is MCP-ping activity now see the warming_up
  checklist instead of a misleading `F` grade.

## 0.9.1 - 2026-05-13

### Added

- `tokenops demo --reset-only`: purges `source=demo` events without
  reseeding. Closes the gap that forced operators into raw SQL when
  they wanted to clean leftover seeded data. Idempotent.

## 0.9.0 - 2026-05-13

### Added

- **`signal_quality` on every session_budget and plan_headroom**:
  closed-set `level` (low|medium|high), `source`
  (mcp_tool_pings|proxy_traffic|vendor_usage_api), one-sentence
  `caveat`, and `upgrade_paths` so callers see exactly how trustworthy
  the underlying number is. Default response leads with
  `level: low, source: mcp_tool_pings` and a disclaimer.
- **Empty-state scorecard**: when no KPI has real-data backing, the
  scorecard returns `OverallGrade: warming_up` plus a 3-step
  activation checklist instead of a misleading `F`. CLI text
  renderer special-cases the warming-up state.
- **Data-warning banner on cost/headroom responses**: when synthetic
  events make up more than 10% of the queried window,
  `tokenops_spend_summary` / `tokenops_plan_headroom` /
  `tokenops_session_budget` attach a `data_warning` object with the
  ratio, real/demo counts, and the exact reset command.
- **Hot-reload on `tokenops plan set`**: `tokenops serve` polls the
  resolved config path every 2 seconds and swaps the snapshot
  atomically on mtime change. `PlanDeps.ConfigGetter` plumbs the
  live snapshot to every plan tool — operators no longer need to
  reconnect their MCP host after `tokenops plan set`.
- **Catalog-alias migration shim**: `plans.ResolveAlias` maps
  retired catalog names to modern entries. `tokenops plan set
  claude-max` prints `renamed claude-max -> claude-max-20x` and
  writes the modern name. Stale docs / blog posts keep working.
- **Launch plan + tracker docs**:
  `docs/launch-plan.md` (Loom script, Show HN post, Discord posts,
  founder-DM template, success criteria) and
  `docs/launch-tracker.md` (10-row tracker, per-call notes,
  synthesis rubric, negative-signal log) so the maintainer can run
  the GTM cycle from a single doc.

## 0.8.1 - 2026-05-13

### Fixed

- Scorecard compute path now filters Source=demo envelopes before
  computing FVT/TEU/SAC. v0.8.0 added the isolation everywhere
  except this query path so `tokenops demo` data continued to
  inflate the wedge KPIs.

## 0.8.0 - 2026-05-13

### Added

- **Demo data isolation**: synthetic events seeded by `tokenops demo`
  are now excluded from every default analytics surface (`spend
  summary`, `top consumers`, `burn rate`, `forecast`, plan headroom +
  session budget). Opt back in with `include_demo: true` on the MCP
  tool input. `analytics.DefaultExcludedSources` is the single source
  of truth; pass `ExcludeSources: []string{}` to bypass.
- **`tokenops_data_sources` MCP tool**: groups events by source
  column (`proxy`, `mcp-session`, `demo`, `otlp`, …) so operators see
  at a glance whether headroom + spend math run on real or seeded
  data.
- **MCP session middleware**: every `tools/call` request increments
  `session.Tracker` regardless of which handler runs. Replaces the
  per-tool Record sites in `plan_headroom` / `session_budget` so the
  window-count signal is uniform across the surface.
- **`tokenops_help` MCP tool**: 6-category curated index (setup,
  session, cost, workflows, rules, debug) so agents and operators
  can navigate the 20+ tool surface without enumerating
  `tools/list`.

### Fixed

- **Rules walker**: `filepath.WalkDir` callback now tolerates
  `fs.ErrPermission` (skips the offending subtree / file) and
  `fs.ErrNotExist` (race between dir-listing and stat).
  `tokenops rules analyze --root ~/.claude` no longer aborts with
  `permission denied` from `~/Library/Saved Application State` and
  friends. Skip list extended to Library/Containers/.Trash.

## 0.7.1 - 2026-05-13

### Fixed

- `tokenops plan list` (and other read-side subcommands routed via
  `loadConfig`) returned "no plans configured" right after `tokenops
  plan set` because the loader honoured only `--config` and env vars
  while the mutation verbs defaulted to the XDG path. `loadConfig`
  now auto-discovers `$XDG_CONFIG_HOME/tokenops/config.yaml` (or
  `~/.config/tokenops/config.yaml`) when `--config` is unset.
- Empty-state hint on `plan list` now points at `tokenops plan set …`
  instead of the legacy env-var instructions.

## 0.7.0 - 2026-05-12

### Added

- **MCP-first wedge**: TokenOps now observes operator activity inside
  the MCP session rather than relying on proxy traffic, which the
  three-skill review confirmed is the wrong consumption surface for
  flat-rate Claude Code / Cursor users.
  - `internal/contexts/spend/session` package: `Tracker.Record` emits
    a plan_included synthetic `PromptEvent` for every observed MCP
    tool invocation, so `ConsumptionInWindow` / `headroom` see real
    activity without a proxy.
  - `tokenops_session_budget` MCP tool: predicts whether the current
    session will hit the rate-limit cap; returns
    `recommended_action ∈ {continue, slow_down, switch_model,
    wait_for_reset}` with a confidence band.
  - `plans.ComputeSessionBudget` pure function with 7 unit tests
    covering the recommendation matrix.
- **Config-as-code primitive**:
  - `tokenops plan set <provider> <plan>` / `tokenops plan unset`
    replace the previous JSON-edit-the-MCP-host-config flow.
  - `tokenops provider set|unset|list` mirrors the same verb shape.
  - Shared `config_mutate.go` helpers (`readMutableConfig`,
    `writeMutableConfig`) reusable for future `tokenops <subsystem>
    set` commands.
- **Hint sweep**: every structured `{error, hint}` payload now
  contains the exact corrective command (`tokenops plan set …`,
  `tokenops provider set …`) instead of an environment-variable name.
- **Customer discovery scaffolding**: `docs/customer-discovery.md`
  with a 9-question Torres-style interview script, recruitment
  targets, synthesis rubric, and reject criteria for the 5-user
  wedge validation sprint.

### Changed

- README quickstart replaces the env-var / JSON-edit instructions
  with `tokenops plan set anthropic claude-max-20x` (etc.).
- `docs/plan-cost-model.md` notes the proxy is no longer the primary
  observation surface; MCP-side activity is the new default.

## 0.6.0 - 2026-05-12

### Added

- **Rate-limit window headroom** for subscription plans that publish
  rolling windows instead of monthly token caps.
  - `Plan` gains `MessagesPerWindow` + `WindowUnit` fields; catalog
    splits generic `claude-max` into `claude-max-5x` (50 msgs / 5h)
    and `claude-max-20x` (200 msgs / 5h). Adds documented caps for
    `claude-pro`, `gpt-plus`, `gpt-team`.
  - `HeadroomReport` gains `window_cap`, `window_consumed`,
    `window_pct`, `window_resets_in`, `window_unit` fields.
    `overage_risk` headline takes the worst of monthly and window
    signals.
  - `tokenops plan headroom` text output prints both monthly and
    window lines; `tokenops_plan_headroom` MCP tool exposes the same
    fields.
  - `internal/contexts/spend/plans.ConsumptionInWindow` reader counts
    plan-included PromptEvents over a trailing window.

### Changed

- Generic `claude-max` removed from the plan catalog. Users on the
  Anthropic Max plan should pick `claude-max-5x` or `claude-max-20x`
  depending on their tier.

## 0.5.0 - 2026-05-12

### Added

- **Plan-Based Cost Model**: subscription-aware spend tracking for
  Claude Max / Pro, Claude Code Max / Pro, ChatGPT Plus / Pro / Team,
  GitHub Copilot Individual / Business, Cursor Pro / Business.
  - `PromptEvent.CostSource` enum (`metered` default,
    `plan_included`, `trial`); schema bumped to 1.2.0.
  - `internal/contexts/spend/plans` package: catalog with dated
    `SourceURL` per plan, `ComputeHeadroom` returning
    `consumed_pct` / `headroom_days` / `overage_risk` (low / medium
    / high / unknown), and `ConsumptionFor` reader.
  - `tokenops plan list|headroom|catalog` CLI subcommands and
    `tokenops_plan_headroom` MCP tool.
  - `Config.Plans` map (`plans:` YAML block or
    `TOKENOPS_PLAN_<PROVIDER>` env) validated against the catalog.
  - `tokenops demo --plan <name>` stamps PromptEvents with
    `cost_source=plan_included` so the headroom surface populates on
    a fresh install.
  - `docs/plan-cost-model.md` documents the catalog and add-a-plan
    workflow.
- Spend engine short-circuits `Compute` to zero for `plan_included`
  and `trial` events so flat-rate traffic doesn't inflate metered
  `cost_usd`.

## 0.4.0 - 2026-05-12

### Added

- `tokenops demo` now seeds `OptimizationEvent`s alongside prompts
  (~40% rate, 20–40% savings) so the first-run scorecard reflects a
  realistic optimizer mix and TEU lifts off zero. Demo output reports
  prompts vs. optimizations separately.
- Scorecard `KPIResult` gained `name` + `description` fields so
  operators can decode the FVT / TEU / SAC abbreviations inline.
  `tokenops scorecard` text output adds a Definitions block.
- Roady backlog: new `Plan-Based Cost Model` feature spec covering
  Claude Max / ChatGPT Plus / Copilot / Cursor flat-rate plans
  (cost_source on PromptEvent, plan quota tracking, headroom metrics).
  Implementation deferred to its own cycle.

## 0.3.1 - 2026-05-12

### Fixed

- `tokenops_status` returned `state: "not_ready"` when MCP serve mode
  was actually ready but the on-disk config still listed disabled
  subsystems. New `degraded` state distinguishes "ready with reduced
  surface area" from "broken".

## 0.3.0 - 2026-05-12

First-run activation and security-suppression governance.

### Added

- **`tokenops init`** scaffolds an opinionated config (sqlite storage
  + rules subsystem enabled at `$PWD`) at `$XDG_CONFIG_HOME/tokenops/
  config.yaml`. Idempotent; `--force` overwrites, `--print-only`
  emits YAML to stdout without touching disk.
- **`tokenops demo`** seeds deterministic synthetic events
  (5 providers/models, 4 workflows, 3 agents, jittered tokens + cost)
  so `tokenops spend`, `tokenops scorecard`, `tokenops forecast`, and
  the MCP analytics tools return populated data on a fresh install.
  Flags: `--days`, `--per-day`, `--reset`, `--dry-run`, `--seed`.
- **Status blockers / next-actions**: `tokenops_status` MCP tool and
  the daemon's `/readyz` endpoint now expose `blockers[]`
  (`storage_disabled`, `rules_disabled`, `providers_unconfigured`) and
  `next_actions[]` so first-run callers see exactly what to fix
  without grepping config. `config.Blockers()` + `NextActionsFor()`
  are the canonical helpers.
- **Disabled-subsystem error contract**: daemon analytics + rules
  routes (`/api/spend/*`, `/api/optimizations`, `/api/audit`,
  `/api/rules/*`) now return `503 {error, hint}` when their
  subsystem is off, instead of an opaque `404`.
- **Suppression governance gate** (`internal/secgov`): `go test`
  now enforces that every entry in `security/vex.json` carries
  `_governance.{classification, last_reviewed, reviewed_by}` and
  every `.nox.yaml` `scan.exclude` entry is preceded by the same
  metadata in comments. Review age capped at 120 days.

### Changed

- `security/vex.json` waivers gain `_governance` metadata on all
  eight existing statements; bumped doc version to 2.
- README `Getting started` is now a 3-command quickstart
  (`init` → `demo` → `start`) plus a first-run troubleshooting
  table indexed by blocker identifier.

## 0.2.0 - 2026-05-12

The Rule Intelligence wedge lands plus a full DDD refactor: rule
artifacts (`CLAUDE.md`, `AGENTS.md`, Cursor rules, MCP policies) become
first-class operational telemetry, repository layout reorganises around
bounded contexts, and the MCP / HTTP / CLI surfaces all share the same
domain services. Adopts felixgeelhaar/{bolt, fortify v1.5.0, mcp-go}.

### Added

- **Rule Intelligence** (issue #12): full subsystem treating
  `CLAUDE.md`, `AGENTS.md`, Cursor rules, MCP policies, and repo
  conventions as first-class operational telemetry.
  - `RuleSourceEvent` + `RuleAnalysisEvent` payloads (schema 1.1.0).
  - Analyzer (per-section token cost + density), Compressor (Jaccard
    near-duplicate pruning + quality gate), Router (dynamic injection
    with token + latency budgets), ROIEngine, Benchmark, Conflicts
    detector (redundant / drift / anti-pattern).
  - CLI: `tokenops rules analyze|conflicts|compress|inject|bench`.
  - MCP: `tokenops_rules_*` tools.
  - HTTP: `/api/rules/*` endpoints with cache invalidated by
    `RuleCorpusReloaded` domain event.
  - Vue dashboard `/rules` view.
- **Domain event bus** (`internal/domainevents`): typed cross-context
  pub/sub with async mode, bounded queue, panic recovery,
  cancellable subscriptions, slow-handler detection, drop / panic /
  dispatch counters. JSONL persistence with size-based rotation,
  fsync on close, lenient replay.
- **Telemetry contracts** + golden tests pinning the on-wire JSON for
  every envelope payload (`pkg/eventschema/golden_test.go`).
- **DDD architecture** (`docs/architecture-ddd.md`): bounded contexts,
  ubiquitous language glossary, layering rules. Enforced via
  `internal/archlint` — `go list -deps` based test fails CI when a
  domain package imports an adapter or undocumented infrastructure.
- **Composition root** `internal/bootstrap`: single construction site
  for sqlite store, spend engine, analytics aggregator, tokenizer
  registry, redactor, domain bus, event counter.
- **Eval gate** (`internal/contexts/optimization/eval`): regression
  thresholds on success rate, per-optimizer quality drift, optimizer
  presence. CLI: `tokenops eval [--baseline --enforce --output]`.
- **Wedge KPI scorecard** wired to live event store (FVT/TEU/SAC).
- **Coverage debt** (`internal/contexts/governance/coverdebt`):
  risk-weighted coverage report from Go cover profile.
- **Audit subscriber** wires `BudgetExceeded` + `OptimizationApplied`
  events to the audit log with bounded concurrency, drop counter.
- **`tokenops audit`** and **`tokenops events`** CLIs (with JSONL
  fallback when daemon unreachable, `--since` filter, URL-scheme
  aware).
- **`tokenops_domain_events`** MCP tool surfaces in-process event
  counts + audit drop counter.
- **fortify v1.5.0 adoption**: provider proxy routes can opt into
  `CircuitBreakerStream` via a new `resilience.*` config block. Each
  upstream gets its own circuit breaker plus FirstByte / Idle / Total
  watchdogs; stalled SSE streams surface as `504 Gateway Timeout`
  instead of hanging the client, and a flaky vendor trips without
  taking siblings offline. Off by default (no behaviour change for
  existing deployments). OTLP exporter wraps the upstream call in a
  fortify circuit breaker for finite-response fault tolerance.
- **bolt logger adoption**: `observ.NewLogger` now produces zero-alloc
  JSON via `github.com/felixgeelhaar/bolt` when `log.format=json`;
  text format retains stdlib slog.
- **mcp-go adoption**: `internal/mcp` is now a thin adapter over
  `github.com/felixgeelhaar/mcp-go`. JSON-RPC framing, schema
  generation, and stdio transport move upstream; every tool gets a
  typed input struct with auto-generated JSON schema. CLI `tokenops
  serve` calls `mcp.ServeStdio` instead of the prior handwritten loop.

### Changed

- Schema version 1.0.0 → 1.1.0 (additive: rule_source + rule_analysis
  event kinds, tokenops.rule.* OTLP attributes).
- Repository layout: domain packages moved under
  `internal/contexts/<ctx>/<pkg>`; `internal/infra/rulesfs/` carries
  filesystem-touching rule corpus loader.
- Config snapshot (`config.Config.Snapshot`) redacts OTel headers.
- Bus.Subscribe returns `*Subscription` with `Cancel()`.

### Fixed

- Bus close/publish race (queueClosed guard).
- Audit subscriber goroutine leak past shutdown.
- JSONLog rotation size tracked via `Stat`, no longer estimated.
- Daemon shutdown bounded by `cfg.Shutdown.Timeout` for both telemetry
  and domain bus drains.
- `LoadCorpus` deduplicates `RuleCorpusReloaded` events when the
  corpus digest hasn't changed.
