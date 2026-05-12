# Customer Discovery: Plan-Based AI Subscriptions

Five story-based interviews with developers on Claude Max / ChatGPT Plus
/ Copilot / Cursor flat-rate plans. Goal: validate (or kill) the wedge
hypothesis that TokenOps's plan-headroom + session-budget surface
solves a real, recurring struggling moment — not just a curiosity.

## Wedge hypothesis under test

> We believe that developers on flat-rate AI subscriptions hit a
> recurring struggling moment around mid-session rate-limit cutoffs,
> and that a proactive headroom signal accessible from inside their
> agent (Claude Code, Cursor) — not a separate dashboard — would
> change their behaviour before the cutoff lands.

Reject the hypothesis when:

- Fewer than 3 of 5 interviewees recall a rate-limit incident in the
  last 30 days, OR
- Fewer than 3 of 5 say they would want the agent itself to warn them
  ("I'd just check the vendor's status page" / "I switch tabs and
  forget about it" = soft rejection).

## Recruitment

Target mix (one each, minimum):

- Heavy Claude Max user (Max 20x) running long Claude Code sessions
- ChatGPT Plus user hitting GPT-4o rate limits mid-conversation
- Cursor Pro user watching their monthly request count
- Copilot user on the new premium-request pricing
- Multi-provider operator running ≥2 of the above in parallel

Recruitment channels: friendly Slack / Discord communities (Claude
Builders, Cursor Discord, AI Engineer Foundation), open GitHub issues
on TokenOps, X DMs to users who post about plan limits.

## Story-based interview script (Torres methodology)

Open with the struggling moment — never with the product.

### Block 1 — Surface the struggling moment (5–8 min)

1. *Tell me about the last time you ran into a rate limit or quota
   warning on your AI subscription.* Wait. Listen. Take notes verbatim
   on what they tried first.

   Follow-ups:
   - What were you trying to accomplish when the limit hit?
   - How did you find out — visible UI message, slow response, agent
     stopped mid-task?
   - What did you do in the next 5 minutes?
   - What did the rest of that day look like?

2. *Walk me through how you decided which plan to subscribe to.*
   (Surfaces price-anchoring vs. capability-anchoring buyers.)

### Block 2 — Today's coping mechanisms (5 min)

3. *Do you do anything today to avoid running out of quota?* Look for:
   manual session pacing, checking the vendor dashboard, switching
   between Claude / GPT mid-session, multi-account workarounds.

4. *Have you ever switched models or providers because you suspected
   you were close to a limit?* (Validates whether `switch_model` is a
   realistic action.)

### Block 3 — Wish list (5 min)

5. *If your agent could tell you something useful about your plan
   right now, what would you want it to say?* (No leading. Capture
   their language verbatim — it's positioning copy.)

6. *Where would you want to see this — inside the chat, in the
   editor, as a system tray indicator, somewhere else?*

### Block 4 — TokenOps reveal (only after blocks 1–3) (5 min)

Show the actual `tokenops plan headroom` output and the new
`tokenops_session_budget` MCP tool response.

7. *Would you use this? Why or why not?*
8. *What would make you stop using it after a week?*
9. *What would you pay for if anything?* (Optional — only when the
   conversation is warm and they bring up money first.)

## Tracker template

Copy this into a working doc (one row per interviewee).

| Date | Handle | Plan | Last incident | What they tried | Wish | Reaction to demo | Quote |
|---|---|---|---|---|---|---|---|
| YYYY-MM-DD |  |  |  |  |  |  |  |

Quote rules:

- Verbatim, not paraphrased.
- Capture the emotional word ("blocked", "kicked out", "stuck", "fine
  about it").
- Note follow-up probes you wish you had asked.

## Synthesis rubric

After the 5 interviews, score against the wedge hypothesis:

- **Frequency** — ≥3 of 5 hit a rate limit in the last 30 days? (yes
  / partial / no)
- **Severity** — Did the incident materially block work, or was it
  shrugged off?
- **Action gap** — Could a proactive signal have changed their
  behaviour?
- **Surface fit** — Did they expect to see headroom in the agent, in
  a separate dashboard, or in the vendor's own UI?
- **Willingness to install** — Would they install a CLI + MCP server
  for this?

Write the verdict in a single paragraph. Publish to
`docs/discovery-2026-05-wedge-validation.md` (or similar) so the
result is durable. If the hypothesis is rejected, kill the wedge and
rerun the JTBD framing before shipping more code.

## After the interviews

Whether the hypothesis lands or not:

1. Send each interviewee a one-paragraph summary of what you heard
   from them and ask "did I miss anything?" — closes the loop and
   often surfaces a follow-up.
2. Rewrite the README quickstart in the words the interviewees used
   in Block 1. If they said "Claude Code stops mid-refactor", that
   wording goes in the README; "operator wedge KPI scorecard" does
   not.
3. Pick the top three product gaps the interviews surfaced and file
   them as Roady features. Everything outside that top-three list
   gets demoted.
