# CLI reference

The `tokenops` binary is a thin wrapper around the daemon's local
event store.

## `tokenops start`

Starts the daemon in the foreground. Honors all config + env knobs.
Stop with SIGINT / SIGTERM (Ctrl-C).

## `tokenops status`

Queries `/healthz`, `/readyz`, `/version`. Use `--addr` to point at a
remote daemon, `--json` for machine-readable output.

## `tokenops config show`

Dumps the resolved configuration as YAML (or JSON with `--json`).

## `tokenops replay [SESSION_ID]`

Replays past prompts through the optimizer pipeline.

```bash
tokenops replay sess-abc123 --json
tokenops replay --workflow research-summariser --since 24h
tokenops replay --agent planner --since 7d --limit 200
```

Add `--workflow ID` to also run the waste detector against the
reconstructed workflow trace.

## `tokenops spend`

Spend report — totals, top consumers, 24h burn rate, optional forecast.

```bash
tokenops spend                                   # last 7 days, top 5 by model
tokenops spend --by provider --top 3 --since 24h
tokenops spend --forecast --forecast-days 14
tokenops spend --json
```

## `tokenops version`

Prints the binary version + commit + build date.
