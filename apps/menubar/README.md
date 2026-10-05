# TokenOps menu bar

TokenOps in the menu bar: an icon whose ring fills to the plan window
closest to its limit. A click opens the panel under it with every plan's
details; hovering lists each window's share and reset (for example
**Codex · week 75% · resets in 5d 14h**); a right click has Refresh,
Launch at Login and Quit. Built on
[Vitra](https://github.com/klarlabs-studio/vitra).

The panel switches between plans with a tab per vendor, busiest first, each
with a small bar of its fullest window. A plan shows every window the
vendor reports (session, weekly, per model) with a bar, its reset and its
pace: behind lasts to the reset, ahead says when it runs out. Below come
extra usage against its limit, credit left, and cost today and over 30
days, at API prices where a plan covers it. Usage on models with no list
price yet is left out of the money and the panel says how much, rather
than show a figure that looks complete. It follows the system's light or
dark appearance in the Klarlabs palette.

With the Homebrew install on macOS:

```bash
tokenops menubar    # installs it to ~/Applications and opens it
```

Its menu has Launch at Login. Upgrades replace the installed copy and
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
- The panel's grant names three permissions: `glance.read`
  (`glance.follow`), `coach.change` (`coach.preset`, which the daemon
  writes to its audit log) and `panel.close`.
- The icon appears at once and fills in when the first read returns; the
  tray refreshes every minute. A daemon that is slow keeps the last
  reading on screen, marked; one that is not running says how to start it.
- A click opens the panel; a right click opens the menu: Refresh, Launch at
  Login (macOS 13+, packaged app) and Quit.

It is a separate Go module because Vitra needs cgo and Go 1.26, while the
`tokenops` binary stays pure Go on 1.25.
