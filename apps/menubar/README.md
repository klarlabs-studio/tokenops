# TokenOps menu bar

TokenOps in the menu bar: an icon with two rings for the plan closest to
its limit, its week outside and its session inside, each filled to the
share left. A click opens the panel under it with every plan's details;
hovering lists each window's share and reset (for example
**Codex · week 25% left · resets in 5d 14h**); a right click has Refresh,
Launch at Login, Alerts and Quit. Built on
[Vitra](https://github.com/klarlabs-studio/vitra).

The panel switches between plans with a tab per vendor, busiest first, each
with a small bar of its fullest window. A plan's header names the account
its client is signed in with (Claude Code's; Codex keeps its account only
inside its sign-in token, which is not read) and the plan. Every window the
vendor reports (session, weekly, per model) has a bar filled to the share
left, a tick where an even pace would leave it, its reset, and its pace:
reserve lasts to the reset, over pace says when it runs out. Below come
extra usage against its limit, credit left, four figures (today, the last
30 days' cost and tokens, today's tokens) at API prices where a plan
covers it, the last 30 days as a chart, the top model, and the coach's
findings. Usage on models with no list
price yet is left out of the money and the panel says how much, rather
than show a figure that looks complete. It follows the system's light or
dark appearance in the Klarlabs palette.

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
- The panel's grant names four permissions: `glance.read`
  (`glance.follow`), `glance.refresh` (`sources.refresh`), `coach.change`
  (`coach.preset`, which the daemon writes to its audit log) and
  `panel.close`.
- Refresh, in the panel (⌘R) or the right-click menu, asks the daemon's
  usage readers to poll now (`POST /api/sources/refresh`), shows what the
  daemon has at once, and reads again as the new readings arrive. The
  daemon accepts one refresh every 30 seconds; a daemon from before the
  route shows its latest readings and says it cannot poll on demand.
- Each coach finding has a mark for its level, with its words on hover:
  ▲ needs attention, ● worth a look, ○ for your information.
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
