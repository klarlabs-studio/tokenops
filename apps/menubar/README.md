# TokenOps menu bar

TokenOps in the menu bar: the plan window closest to its limit next to the
icon (for example **Codex 49%**), with the icon's ring filled to that share,
and a panel when you click it. Built on
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
- The tray refreshes every minute. A daemon that is slow keeps the last
  reading on screen, marked; one that is not running says how to start it.
- Left click opens the panel; the menu has Show Details, Refresh, Launch at
  Login (macOS 13+, packaged app) and Quit.

It is a separate Go module because Vitra needs cgo and Go 1.26, while the
`tokenops` binary stays pure Go on 1.25.
