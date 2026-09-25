# Launch Plan — TokenOps Wedge (v0.8.x → v0.9.0)

GTM-skill recommendation: post one Loom + one Show HN + four Discord posts + 25 founder DMs. Target 10 installs and 3 discovery-call bookings inside a 7-day window. No new features ship until at least one real-user signal lands.

## Day 1 — Loom + Show HN

### Loom outline (60 seconds)

Dry-run revealed three issues with the original 90s cut: a real
rate-limit screenshot is hard to source on-demand, a fresh install
returns anticlimactic zeros, and `slow_down` / `wait_for_reset`
states require ≥80% window usage which can't be staged honestly. The
60s revision swaps live demos for honest text + pre-recorded JSON
cards where the live path doesn't pay off.

**0:00–0:10 — Hook (voice over a Claude Code session in progress)**
Stay on a normal-looking coding session. No fake screenshot. Voice:
> "Claude Max cuts you off mid-task. No warning. You lose 90 minutes of focus. Your AI agent should see this coming."

**0:10–0:30 — Install (live terminal, large font)**
Show exactly two commands typed live. Output rendered inline.
```bash
tokenops init
tokenops plan set anthropic claude-max-20x
```
Voice: "Two commands. Local install. No cloud."

**0:30–0:50 — Live agent call (split: terminal + Claude Code)**
In Claude Code, ask: *"How much headroom do I have right now?"* Agent calls `tokenops_session_budget`. Show the actual response (whatever your real session returns at recording time — modest numbers are fine).
```json
{
  "window_consumed": 7,
  "window_cap": 200,
  "window_pct": 3.5,
  "recommended_action": "continue",
  "confidence": "medium",
  "signal_quality": {
    "level": "low",
    "caveat": "TokenOps observes MCP tool invocations only..."
  }
}
```
Voice: "Now your agent can ask. Live. In-band."

**0:50–1:00 — Honest close (pre-recorded JSON card, full-screen)**
Display a pre-recorded card showing what happens at 85% window:
```json
{
  "window_consumed": 170,
  "window_cap": 200,
  "window_pct": 85,
  "recommended_action": "slow_down",
  "will_hit_cap_within": "32m",
  "confidence": "high"
}
```
Voice: "At 85% it says slow_down. When you're cooked, wait_for_reset. Open source. Github link below."

### Pre-recording checklist

Run this exact sequence to avoid re-shoots:

1. **Burn in real signal** — open Claude Code, call `tokenops_session_budget` 8-10 times so window_consumed climbs to a non-zero number with `confidence: medium` or `high`. Empty store demos look broken.
2. **Verify telemetry provenance** — use `tokenops_data_sources` to confirm session readers are ingesting real local transcripts.
3. **Terminal setup** — iTerm or Ghostty, font 18pt+, dark theme, prompt trimmed to `$`. Window 1280x720.
4. **Claude Code setup** — empty conversation, MCP server reconnected (`/mcp`), no leftover tool output visible. Single split with terminal on the right.
5. **Recording** — Loom desktop app, 30fps, "Show clicks" on, no countdown overlay.
6. **Two takes minimum**. Scene transitions are unforgiving; the second take is always tighter.
7. **Pre-record the closing JSON card** as a static image (png or screen capture of a real-looking response). Drop in during editing.

### Post-recording checklist

1. Trim to ≤60s. If the cut runs 65s, trim hook + close; never trim the live walkthrough.
2. Caption-burn the voice-over (Loom auto-captions are good enough; check the technical words).
3. Set Loom video title to: **"TokenOps — predict your Claude Max rate-limit cutoffs from inside Claude Code (60s)"**.
4. Get a stable shareable URL. Paste into:
   - Show HN body (replace the existing "Repo:" line).
   - `docs/launch-plan.md` Show HN section (commit the URL).
   - Discord cross-posts (replace the `[Loom link]` placeholder).
   - 25-founder DMs.
5. Smoke-test: send the URL to one person who doesn't know TokenOps. Ask "what is this?". If they don't get it in 10 seconds, recut before posting publicly.

### Show HN post

**Title** (≤80 chars):
> Show HN: Predict your Claude Max rate-limit cutoffs from inside Claude Code

**Body**:
> Hi HN — I built TokenOps because I was tired of hitting Claude Max 20x rate limits mid-refactor with no warning. It's a local Go daemon + MCP server. After `tokenops init` and `tokenops plan set anthropic claude-max-20x`, the agent can call `tokenops_session_budget` and get back `{continue|slow_down|switch_model|wait_for_reset}` with a confidence band and the time-until-cap.
>
> Honesty note: the v0.8.1 release explicitly types `signal_quality` on every response. Right now the math runs on MCP-tool activity, not on your actual Claude turns (those don't flow through TokenOps). So the signal is a heuristic. The product says so. Vendor `/usage` API ingestion is queued.
>
> What's in for v0.9: empty-state scorecard with a first-week checklist instead of a useless F grade; hot-reload on `tokenops plan set` so MCP doesn't need a manual reconnect.
>
> Repo: https://github.com/klarlabs-studio/tokenops
>
> Looking for: 5 people on Claude Max 5x/20x or ChatGPT Plus who'd hop on a 15-minute discovery call. Reply or DM @felixgeelhaar.

**First comment (plant immediately, by author)**:
> Three things I'd love feedback on:
> 1. Is `recommended_action` the right output, or do you just want the raw "N hours until reset"?
> 2. Anyone using GitHub Copilot Premium-request math? I haven't built that surface yet — would value the use case.
> 3. The MCP-ping heuristic vs. proxying client traffic — which would you actually wire up?

## Day 1 — Discord cross-post

Identical message, adapted to each community's norms:

### Claude Builders #show-and-tell
> Hey 👋 just shipped a small tool that predicts Max plan rate-limit cutoffs from inside Claude Code (MCP server, local-only, brew install). 90s Loom below. Would love 5 of you to install it and trade 15 min on a call so I can hear what's missing. [Loom link] [Repo link]

### Cursor Discord #showcase
> Built a TokenOps MCP server that gives Cursor agents a `session_budget` tool for plan headroom. Works for Cursor Pro and Claude Max 20x today; ChatGPT Plus and Copilot in catalog. Looking for 3 beta users to trade install for feedback. [Loom] [Repo]

### aider Discord
> Aider users on Claude subscriptions: shipped a CLI + MCP server that predicts rate-limit cutoffs from session activity. Works alongside aider's existing budget tracking. Free / open source. Loom: [link]. Github: [link]. Discovery calls open this week.

### AI Engineer Foundation Discord
> Released TokenOps v0.8.1 — MCP-resident headroom prediction for Claude Max / ChatGPT Plus / Copilot / Cursor. Honest about the signal-quality gap (response carries a `signal_quality` field). Looking for 5 power users to install and chat. [Loom] [Repo]

## Day 2–5 — 25 founder DMs

Find 25 solo AI-native founders / staff engineers on X or Bluesky who recently posted about AI workflow pain. Adapt this template per person:

> Hi {name} — saw your post about {their specific complaint, paraphrased to prove you read it}. I shipped a small CLI that predicts Claude Max 20x rate-limit cutoffs from inside Claude Code: {Loom link}. Open source, brew install, no signup. If it's a 5-second "no" — totally fine, ignore. If it'd be useful, I'd love 15 minutes to watch you install it and see what breaks.

Rules:
- Five DMs per day max. Quality > volume.
- Personalise the first sentence — never generic.
- Loom link first, repo link second. Watching is lower-friction than installing.
- If they reply: book a Calendly slot inside 24 hours. Use `docs/customer-discovery.md` interview script.

## Day 4 — Long-form blog post

Title (working): "How I predict Claude Max rate-limit cutoffs without hitting the Anthropic API."

Word count: 1200–1500. Sections:
1. The struggling moment (your own story).
2. Why vendor dashboards don't help (after-the-fact, no prediction).
3. The MCP-ping heuristic — what it actually counts and what it doesn't.
4. The `signal_quality` honesty contract.
5. Install in 90 seconds. Drop-in for Claude Code / Cursor / aider.
6. What's broken / on the roadmap (vendor `/usage` ingestion).

Cross-post: dev.to, Hashnode, personal blog. Submit to HN under "Show HN" only if Day 1's Show HN didn't fire. Otherwise just regular submission.

## Success criteria (by Day 7)

| Metric | Target |
|---|---|
| Loom views | ≥200 |
| Show HN front-page (≥80 points) | yes / no |
| GitHub stars added | ≥30 |
| `tokenops` brew installs (homebrew analytics) | ≥10 |
| DM replies | ≥5 |
| Discovery calls booked | ≥3 |
| Calls completed + tracker rows filled | ≥3 |

If targets miss by >50% on day 7, **the wedge hypothesis is in question.** Do not ship more code. Re-read the interview transcripts (even if only 1 or 2). Decide whether to pivot, narrow, or pause.

## What NOT to do

- No paid ads. No SEO marathon. No conference submissions. No category-creation pitch.
- Do not ship a new feature this week. The product is good enough to test demand.
- Do not write a "What we believe about AI" manifesto. Nobody on day 1 wants that.
- Do not respond to negative HN comments defensively. Note them in `docs/launch-tracker.md`, move on.

## Post-launch retrospective (Day 8)

Update `docs/customer-discovery.md` synthesis section with the actual interview data. File a Roady feature per top-three product gap surfaced. Kill any in-flight feature that doesn't trace to an interview quote.
