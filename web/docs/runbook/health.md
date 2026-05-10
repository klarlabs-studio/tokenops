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
