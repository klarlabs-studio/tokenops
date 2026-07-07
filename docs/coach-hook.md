# coach-hook — Stop-hook cache-read coaching nudge

`coach-hook` is a Claude Code **Stop** hook. After each turn it looks at how
much *cache-read* context your session is carrying and, when that load crosses a
threshold, surfaces a short, non-blocking nudge to reclaim it:

> tokenops: this session is carrying ~1.4M cache-read tokens/turn (~$0.70/turn
> API-equiv) — /compact or a fresh session would cut most of it.

Cache-read is the dominant recurring cost in a long Claude Code session: every
turn re-bills the whole accumulated context prefix at the cache-read rate, and
that toll repeats on *every* turn until you `/compact` or start fresh. The coach
gives you the signal to pull that lever at the right time.

It works even when your traffic never reaches the tokenops proxy (e.g. Claude
Code on a subscription), because it runs **inside** the client.

## Install

The simplest path is the scaffolder, which merges the hook into
`~/.claude/settings.json` for you (idempotent, backs up first):

```sh
tokenops hooks install --coach
```

Install both in-client hooks (coaching nudge + read dedup guard) at once:

```sh
tokenops hooks install --coach --read-guard
```

Preview without writing:

```sh
tokenops hooks install --coach --dry-run
```

Inspect / remove:

```sh
tokenops hooks status
tokenops hooks uninstall --coach
```

`hooks install`:

- targets `~/.claude/settings.json` (override with `--settings <path>`),
- **merges** the tokenops entries into `.hooks` without clobbering unrelated
  hooks you already have,
- is **idempotent** — re-running never duplicates an entry; it updates in place,
- **backs up** the prior file to `settings.json.bak` and writes atomically
  (temp + rename),
- prints the binary path + version the hook will call, so you can see exactly
  which build is wired in and get a warning if a different tokenops was already
  referenced.

If you prefer to wire it by hand, `tokenops coach-hook hook` prints the raw
settings.json block.

## Thresholds and modes

Flags on `hooks install` (and on the bare `coach-hook` command):

| Flag          | Default     | Meaning                                                        |
| ------------- | ----------- | -------------------------------------------------------------- |
| `--threshold` | `1000000`   | Cache-read tokens/turn at or above which the coach nudges.     |
| `--cooldown`  | `20`        | Turns to wait after a nudge before nudging the same session.   |

The cooldown provides hysteresis: once you have been told, the coach stays quiet
for `--cooldown` turns before it will nudge that session again.

Set `--threshold` lower if you want to be nudged earlier, higher if your normal
sessions legitimately run large.

## Stats

See how much cache-read load your sessions have been carrying:

```sh
tokenops coach-hook stats
tokenops coach-hook stats --json
```

It reports turns observed, distinct sessions, nudges surfaced, max/avg
cache-read tokens per turn, and the estimated API-equivalent cost of the nudged
turns (for models it can price).

## Cost estimate

Cost is an **API-equivalent** figure: what that per-turn cache-read load would
cost at public list prices. The rate comes from tokenops' own pricing catalog
(`spend` engine); only Opus-family models are priced (they dominate long
sessions and their cache-read rate is well defined). Other models show the token
count without a dollar figure rather than a misleading one. On a subscription
the money is not billed per turn — the figure is there to make the size of the
drag legible.

## Privacy

The hook reads only the **tail** (~256 KiB) of your local transcript jsonl to
find the most recent turn's token usage. It never reads the whole file and never
sends anything off your machine. State is a tiny per-session counter and an
append-only ledger under `~/.tokenops/coach-hook/`.

## Fail-open guarantee

A coach must never disrupt your session. On any error — unreadable transcript,
malformed payload, missing usage data — the hook exits 0 with no output and the
turn proceeds untouched. It uses `systemMessage` (a user-facing, non-blocking
channel), never `decision:"block"`, so it can surface a nudge without forcing the
agent to keep going.
