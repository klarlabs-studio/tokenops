# Health and readiness

| Endpoint   | What it means                                                        |
|------------|----------------------------------------------------------------------|
| `/healthz` | Process is alive. Always 200 once the listener binds.                |
| `/readyz`  | Storage migrations applied, providers wired. 200 → ready, 503 → not. |
| `/version` | Build metadata: version, commit, build date.                         |

`tokenops status` calls all three and prints a one-line summary.
Pair it with a watchdog that alerts on a non-200 from `/readyz` for
more than 30 seconds.

## When `/readyz` flips to 503

1. Inspect the log line at the previous `INFO` boundary — the daemon
   logs every dependency it sets up.
2. The most common cause is a sqlite migration failure. Rename the
   DB to `events.db.bak`, restart, and confirm a fresh DB applies
   migrations cleanly. If yes, the problem is in the data; open
   the old DB with `sqlite3` and check for partial rows.
3. If TLS is enabled, a missing or expired leaf cert flips ready
   to false. Run `tokenops cert rotate` to mint a fresh leaf.

## When `tokenops status` cannot reach the daemon

`tokenops status` reports two things separately: whether *this client*
reached the daemon (`reachability`), and what that says about the
daemon itself (`daemon_health`). A client that cannot connect has not
shown that the daemon is down, so the readiness it reports depends on
why the connection failed:

| `reachability.class` | What it means | Do first |
|---|---|---|
| `permission_denied` | The OS refused this client the connection (EPERM/EACCES), typically a coding agent's sandbox or a network policy. The daemon may be running normally. | Run `tokenops status` from an unrestricted shell, or allow the client loopback connections to the daemon address. Restart only if it is unreachable from there too. |
| `refused` | Nothing listens at the address: the daemon is most likely not running. `state` stays `not_running`. | `tokenops daemon restart` (supervised) or `tokenops daemon install`. |
| `timeout` | The address did not answer in time: the daemon may be busy or hung. | `tokenops daemon status` and the daemon log; restart if it stays unresponsive. |
| `discovery` | No usable address: missing, malformed or does not resolve. | Check where it comes from: `--addr`, `listen` in the config (`tokenops config show`), or the URL hint the daemon writes at startup. |

For every class except `refused`, `state` is `unknown` and
`daemon_health` is `unknown`. `status: daemon_unreachable` and
`ready: false` are kept for scripts that branch on them. `supervisor`
says whether a launchd or systemd unit file is installed; it does not
query the job, so an installed unit is not proof that the daemon runs.
A daemon that answers with a non-2xx status is reachable, and its
status codes are reported per endpoint.

The MCP server's `tokenops_status` follows the same rule: it says
nothing is writing to the event store only when the connection was
refused. Otherwise it says it could not reach the daemon and why, and
`daemon_reachability` carries the class. Warnings about readers that
have gone quiet are reported independently of it.
