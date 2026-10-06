# ADR 0011 — Several sources per provider, tried in order

- **Status:** Accepted 2026-10-06. Phase 1 shipped (#559, #560); phase 2 shipped: Claude Code sign-in (opt-in) and Codex app server (on by default).
- **Date:** 2026-10-06
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0003 (authoritative cost), ADR 0010 (daemon API), `docs/competitive-landscape.md` (update 2026-10-06), `internal/contexts/observability/freshness`

## Context

On 2026-10-05 claude.ai expired the meter's session. For 20 hours, the only
source of Claude's plan windows was refused every five minutes, and every
surface carried on:

- source health said `healthy`, on the previous day's events;
- `glance` and the menu bar showed day-old windows as current;
- the coach, with no window to judge against, fell back to a dollar figure
  nobody pays.

PR #558 made each of those failures visible. It did not give TokenOps a
second way to read the windows. Claude had one source in practice: the
Claude Code status line feed existed, but only operators who had installed
TokenOps' status line produced it.

CodexBar, the closest rival on plan headroom, reads Claude three ways
(OAuth API, `claude /usage` in a PTY, claude.ai cookies) and falls back in
order. Its open issues show the cost of each one:

- Reading Claude Code's Keychain item prompts again whenever Claude Code
  rewrites it (#3798).
- The OAuth usage endpoint rate-limits (#575, #1679).
- The PTY probe used to create a claude.ai session per probe (#2251).

Claude Code's own documentation (checked 2026-10-06, v2.1.289):

- `/usage` is interactive only; nothing prints plan usage as JSON.
- The status line's `rate_limits` (five hour, seven day, gateway spend
  limit) is the documented interface, delivered only while a session is
  open.
- Credentials sit in the macOS Keychain or a `0600` file, and the docs
  give no guidance on other tools reading them.

## Decision

1. **Every provider with a plan has an ordered list of sources.** Each
   source is an adapter that writes window readings under its own source
   tag, so provenance survives (ADR 0004). The order, cheapest and least
   invasive first:

   1. a feed the harness gives TokenOps on its own (status line, rollout
      `rate_limits`);
   2. the vendor's own records on disk;
   3. a vendor endpoint TokenOps signs in to with a credential the operator
      gave it (claude.ai session, account keys);
   4. a credential owned by another application (Claude Code's OAuth
      token). This is opt-in only, never read in the background without a
      grant, and never refreshed by TokenOps: refreshing would rotate the
      owner's token and sign it out.

2. **Readings merge by freshness, per window.** The newest reading of each
   window wins, whichever source wrote it (`plans.MergeReadings`). A
   window older than `headroom.LiveFreshness` is never shown as current:
   surfaces show its age, or say it is unknown.

3. **Each source has its own health** (`freshness`). A source refused for
   15 minutes is `failing`, whatever older events it holds (#558).

4. **A refused source tries its own recovery before it fails over.** The
   claude.ai meter re-reads the browser on an expired session, whether the
   session was first read from a browser or pasted.

5. **The operator hears once when every source of a provider has stopped:**
   a coach finding, so it reaches `glance`, the menu bar's alerts and the
   API. A single failing source while another is fresh is shown in
   `tokenops status` and the data-sources view, not alerted.

6. **Setup turns sources on by inference.** `init` installs the status
   line, keeps the meter refreshing from the browser, and lists every
   source with its state. The operator corrects afterwards; nothing asks
   first.

## Sources by provider (phase 1 target)

| Provider | 1 Harness feed | 2 Records on disk | 3 Own sign-in | 4 Another app's credential |
|---|---|---|---|---|
| Claude | status line `rate_limits` (installed by `init`) | — | claude.ai meter, browser refresh | Claude Code OAuth (`setup claude-code`, opt-in) |
| Codex | `codex app-server` `account/rateLimits/read`: Codex signs in itself (shipped, on by default) | rollout `rate_limits` per turn | — | Codex `auth.json`: not planned; the app server makes it unnecessary |
| Gemini, Copilot, Cursor and account providers | — | chat recordings, hook ledger | quota endpoints, account keys | — |

## Consequences

- **The Claude status line is the primary source.** It covers every live
  session at no credential cost. The meter covers the gaps between
  sessions.
- **Two Claude sources means two can disagree.** The newest wins per
  window, and each keeps its tag, so the disagreement is visible and
  explainable.
- **Opt-in sources need consent the daemon cannot ask for.** They read
  only after a command the operator runs.
- **Out of scope:** CodexBar's PTY probe of `claude /usage`. It drives an
  interactive screen not meant for parsing, and the status line gives the
  same windows through a documented interface.
