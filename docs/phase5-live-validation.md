# Phase 5: Real-Work Validation

This runbook uses ordinary, operator-selected work. It does not seed events or
infer success from task completion.

## Evidence required

An experiment is useful only when both arms are attributable to comparable
work. Route requests through the TokenOps proxy and send the same unique
`X-Tokenops-Execution-Id` on every request in one harness execution. The proxy
records the assignment and strips this local correlation header before
forwarding upstream. Requests without an execution ID remain outside the
randomized cohort.

For every baseline and variant execution, retain counted prompt usage and one
strong outcome assessment after the final edit: either an observed human
assessment or a recognized verifier result. A missing outcome is `unknown`,
not success. Human-attention minutes are optional and must be actual
operator-reported effort.

## Bounded trial procedure

1. Confirm the Anthropic provider, proxy, and model-routing rule are configured
   with `tokenops provider list`, `tokenops status`, and
   `tokenops routing rule list`.
2. Select a comparable, non-critical task class and explicitly approve the
   baseline and variant models. Declare a lower-is-better objective, the
   minimum improvement worth acting on, and every guardrail. For example:
   `tokenops experiment start anthropic <baseline> <variant> --pairs 5 --days
   14 --objective tokens --min-improvement-pct 10 --guardrail quality
   --guardrail latency_ms:10`. Here 10% is an operator-selected trial
   threshold, not a universal token or dollar weight. Trials are capped at 10
   pairs and 14 days; stop early if quality or policy concerns arise.
3. Launch each bounded Claude attempt through `tokenops anthropic-bridge --
   claude`. It creates one execution ID, prints it, and injects it on every
   request through the local proxy. Use one child process for one task attempt;
   existing sessions cannot be retrofitted. Keep task scope and other
   workflow conditions as comparable as practical.
4. After the final edit, use `tokenops outcome detect <execution-id> --session
   <session-id>` when a recognized verifier actually ran, or record the
   operator's real assessment with `tokenops outcome record <execution-id>
   --result achieved --decision <decision-id>` (choose `partial` or
   `not_achieved` when that is the observed result). Never infer an outcome
   from “done.”
5. Review `tokenops experiment status <experiment-id>` and
   `tokenops verify --experiment-id <experiment-id>`. Incomplete attribution,
   missing usage, or missing outcomes means the comparison is not established.

One complete pair demonstrates the evidence path; it is not a causal claim.
The current learning promotion gate requires at least five fresh matched
pairs, 60% strong outcome coverage, quality non-inferiority in at least 80% of
pairs, and at least 10% median resource improvement with 60% of pairs improving
by at least 10%. Trusted evidence requires at least 20 pairs and 90% coverage,
and does not itself grant execution authority.

## Coding-agent extension

The exact-output cohort is not sufficient evidence for a coding agent. A
coding-agent trial must use the same execution ID for every request in one
attempt and additionally verify all of the following from the emitted prompt
events:

- the provider endpoint is Responses or Messages and the response is streamed;
- every request in the execution remains in the assigned arm;
- provider-reported input, cached-input, and output usage is present;
- at least one provider-reported tool-call item is observed when the task
  requires a tool; TokenOps records only the count, never its name, arguments,
  or result;
- latency and time-to-first-token are measured for every call;
- the final repository verifier is recorded as the execution outcome.

Model routing is valid only when the target accepts the inbound request's
capabilities. TokenOps preserves the baseline instead of rewriting Anthropic
requests that Sonnet 5 cannot represent, including mid-conversation system
messages, manual extended thinking, explicit sampling parameters, and
assistant prefills. A skipped incompatible route is safe-path evidence, not a
variant assignment. Select two request-compatible models for a paired trial.

OpenAI Responses usage is read from the terminal `response.completed` event.
Anthropic Messages usage is combined from `message_start` and `message_delta`.
The observer decodes a bounded copy of gzip-compressed provider responses for
usage and error classification; the bytes sent to the client are not changed.
Opaque Anthropic HTTP 400 responses after a model rewrite are classified as
route-associated without storing the request or response body. A 400 in either
arm invalidates the pair even when the coding agent retries successfully.
An incomplete stream has no authoritative usage and must not be admitted as a
complete paired execution. Keep the task bounded to a disposable fixture or an
isolated worktree, cap the number of pairs, and stop on the first policy,
quality, or upstream-compatibility failure.

## Utility objective and guardrails

Verification can report counted tokens, plan-quota usage, verified metered
cost, proxy latency, outcomes, and explicitly reported human attention.
Supported objectives and numeric guardrails are counted tokens, provider-scoped
plan quota tokens, measured metered cost, proxy latency, and explicitly
reported human attention. Quality is a required strict non-inferiority
guardrail based on strong outcome assessments. Every experiment stores its
policy with the append-only start event. Missing objective or guardrail
measurements, including unknown latency or cost, block promotion. There are no
universal dollar/token weights: the measured vector remains available and the
operator supplies thresholds for each trial.

Legacy trials do not have a declared policy and remain observational. A
supported belief still does not itself authorize autonomous routing.
