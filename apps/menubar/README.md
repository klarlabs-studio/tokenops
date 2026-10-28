# TokenOps menu bar

TokenOps in the menu bar: a fuel gauge for your plans. The icon is the
headroom ring with a tank inside it, filled to the share left on the plan
window closest to its limit (the tightest across every bound plan, the same
figure the panel and tooltip give). Full means 100% left; the fill drains in
eight steps; empty means used up and nothing else, because any headroom left
shows at least one step. A stale or unreadable figure draws the ring with a
dot in the middle instead, never a full or an empty tank. The icon is a
template image, so macOS tints it for a light or dark menu bar. A click opens
the panel under it with every plan's details;
hovering lists each window's share and reset (for example
**Codex · week 25% left · resets in 5d 14h**); a right click has Refresh,
Launch at Login, Alerts, Ring: tightest plan and Quit. Built on
[Vitra](https://github.com/klarlabs-studio/vitra).

The ring follows the tightest plan unless you choose one: **Show in menu
bar** under a plan's name in the panel pins the ring to that plan, however
tight the others are, and the pinned plan's tab says **In the menu bar ·
Auto**, where Auto goes back to the tightest plan (so does **Ring: tightest
plan** in the right-click menu). The choice is kept across launches. A
pinned plan with no reading right now (its plan unbound, its reader down)
shows the tightest plan instead, and the tooltip's first line says so.
Alerts fire for every plan whichever one the ring shows.

The panel switches between plans with a tab per vendor, busiest first, each
with a small bar of its fullest window. A plan's header names the account
its client is signed in with (Claude Code's; Codex keeps its account only
inside its sign-in token, which is not read) and the plan. Every window the
vendor reports (session, weekly, per model) has a bar filled to the share
left, a tick where an even pace would leave it, its reset, and its pace:
reserve lasts to the reset, over pace says when it runs out. Below come
extra usage against its limit, credit left, each window of your own budgets
on that provider's bill (`tokenops budget set --provider`) with whether it
notifies or is enforced, four figures (today, the last
30 days' cost and tokens, today's tokens) at API prices where a plan
covers it, the last 30 days as a chart, the top model, and the coach's
findings. Usage on models with no list
price yet is left out of the money and the panel says how much, rather
than show a figure that looks complete. It follows the system's light or
dark appearance in the Klarlabs palette (electric lime on ink).

With the Homebrew install on macOS:

```bash
tokenops menubar    # installs it to ~/Applications and opens it
```

Right-click the icon for Launch at Login. Upgrades replace the installed copy and
restart it if it is running. The app is signed ad hoc, not notarized, so it
reaches you through the Homebrew cask, whose install step clears macOS's
quarantine flag; a copy downloaded with a browser is blocked by Gatekeeper.
`scripts/build-menubar-app.sh` builds it (universal, arm64 and x86_64) for
the release.

From a checkout:

```bash
make menubar        # run it against the local daemon
make menubar-test   # tests, no window needed
```

- It reads only the local daemon's API (ADR 0010), with the token the
  daemon writes to `~/.tokenops/daemon.url`. The token stays in the Go
  process; the panel never sees it.
- The panel's grant names seven permissions: `glance.read`
  (`glance.follow`), `glance.refresh` (`sources.refresh`), `coach.change`
  (`coach.preset`, which the daemon writes to its audit log),
  `boundaries.deny` (`boundary.deny`), `boundaries.approve`
  (`boundary.approve`), `findings.answer` (`finding.act` and
  `finding.dismiss`) and `panel.close`.
- Decisions a boundary is waiting on (ADR 0014) are listed under *Waiting on
  you*. Deny answers through the daemon in one click and never signs.
  Approve needs a person, because the agent a boundary stopped can reach the
  daemon's API too. With a presence key enrolled (ADR 0015), Approve asks the
  daemon for the statement (`POST /api/boundaries/statements`), runs the
  helper bundled beside the app's binary
  (`TokenOps.app/Contents/MacOS/tokenops-presence sign`), which shows the
  Touch ID, Apple Watch or password dialog with the daemon's text naming the
  rule, action and session, and posts the daemon's proof back with the
  signature added. The panel's note says what happened: approved and by
  which key, or why not (cancelled, Touch ID or password did not match,
  screen locked, no screen, a key from another Mac). Without a key, without
  the helper in the bundle, or with a daemon from before the statement route,
  Approve… opens a terminal running `tokenops approve <id> --confirm`, which
  shows what is waiting and answers only when you type yes. The app never
  builds a statement itself. A decision under an organisation rule that names
  an approver shows who answers it (that role, in the team plane) and offers
  Deny only.
- Refresh, in the panel (⌘R) or the right-click menu, asks the daemon's
  usage readers to poll now (`POST /api/sources/refresh`), shows what the
  daemon has at once, and reads again as the new readings arrive. The
  daemon accepts one refresh every 30 seconds; a daemon from before the
  route shows its latest readings and says it cannot poll on demand.
- Each coach finding has a mark for its level, with its words on hover:
  ▲ needs attention, ● worth a look, ○ for your information.
- A warning about a plan's window (a quota finding, which names its
  `provider` and `window`) sits in that plan's tab, directly under the
  bar it concerns, and the tab carries a ▲ so it is seen from another
  plan; one about the plan as a whole (its reading has stopped) leads the
  tab. The *Coach* section keeps what needs attention or is worth a look
  open, and its heading counts those ("2 to look at"); information folds
  behind an "N more" button (`aria-expanded`), closed by default and
  remembered on this Mac.
- The *Coach* section starts with what the coach did on its own, from
  `GET /api/coach`'s `activity`: today's ("Today: moved 3 subagents to a
  cheaper model · refused 41 re-reads"), or the last week's, said as such,
  when it did nothing today. Under the preset menu, one line says what the
  chosen preset does, from the daemon's preset definitions.
- A finding with an operation behind it has buttons for it, the first in
  lime: *Promote pause* on the headroom pause in shadow, and *Got it*,
  *Expected* and *Snooze 4h* on unusual spend and on tips that waited. They
  run through the daemon (`POST /api/findings/act`), which checks the
  finding still offers the action, runs the same code as the CLI and
  audits it. An action that changes what binds or what is flagged
  (promote, expected) first opens a row inside the finding saying what
  will change and how to undo it, with *Confirm* and *Cancel*; nothing
  runs until Confirm. Demoting stays on the CLI. *Hide…* hides a finding
  for a day or a week (`POST /api/findings/dismiss`); it shows again
  sooner if it becomes more urgent. "2 hidden · Show" brings hidden ones
  back. A finding that changed since the panel read it is left alone and
  the note says so. `tokenops glance --findings` still lists every finding
  and says how many the menu bar hides.
- Each plan shows its vendor's own logo (`frontend/logos`, see its
  NOTICE.md for the source and the trademarks).
- The icon appears at once and fills in when the first read returns; the
  tray refreshes every minute. While a read runs for more than a second,
  the panel says so with a spinner and how long it has taken, and keeps
  what it showed. Refresh spins and reads "Refreshing…" until the new
  readings are in. A read that takes too long, or fails, says in plain
  words what is still shown and what happens next; the cause goes to the
  log. A daemon that is not running says how to start it.
- A click opens the panel; a right click opens the menu: Refresh, Launch at
  Login (macOS 13+, packaged app), Alerts and Quit.
- Alerts are desktop notifications, on until you untick them (remembered
  in `~/Library/Application Support/TokenOps/menubar.json`). A window
  alerts once each as it drops to 20% left, to 5% and to used up, with its
  reset and, while it still runs, when it runs out at this pace; one that
  alerted says so again when it resets. A plan with no window alerts on its
  spend limit the same way. A plan whose every window has gone stale (its
  sources stopped) alerts once that its reading stopped, with the fix, and
  once when it is back; stale windows alert nothing else. The first
  reading after launch only sets the
  baseline, so opening the app does not repeat what was already true.
  macOS shows notifications only for the packaged app.

It is a separate Go module because Vitra needs cgo and Go 1.26, while the
`tokenops` binary stays pure Go on 1.25.
