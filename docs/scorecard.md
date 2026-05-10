# Operator Wedge KPI Scorecard

The scorecard measures three key outcomes for TokenOps operators.
Each KPI has thresholds that map to a grade (A–F). The overall
grade is the worst individual grade.

## Metrics

### First-Value Time (FVT)

*How quickly the operator gets their first observable result.*

**Definition:** Seconds from `tokenopsd start` to the first recorded
`PromptEvent` (i.e., the first proxied LLM request that produces a
spend event in the local store).

**Formula:**
```
FVT = timestamp(first_prompt_event) - timestamp(daemon_start)
```

**Thresholds:**

| Grade | Condition |
|-------|-----------|
| A     | ≤ 60 seconds |
| B     | ≤ 300 seconds (5 min) |
| C     | ≤ 900 seconds (15 min) |
| F     | > 900 seconds |

### Token Efficiency Uplift (TEU)

*How much the optimizer pipeline reduces token consumption.*

**Definition:** Percentage reduction in input+output tokens when
the optimizer pipeline is active (`mode=interactive`) compared to
passive (`mode=passive`). Computed via the replay engine over a
representative session window.

**Formula:**
```
TEU (%) = (passive_tokens - optimized_tokens) / passive_tokens * 100
```

**Thresholds:**

| Grade | Condition |
|-------|-----------|
| A     | ≥ 20% |
| B     | ≥ 10% |
| C     | ≥ 5% |
| F     | < 5% |

### Spend Attribution Completeness (SAC)

*How much of total spend can be attributed to known entities.*

**Definition:** Percentage of total spend (in USD) that is associated
with a known `workflow_id`, `agent_id`, or `session_id` in the event
store. Unattributed spend is a sign of incomplete instrumentation.

**Formula:**
```
SAC (%) = attributed_spend / total_spend * 100
```

**Thresholds:**

| Grade | Condition |
|-------|-----------|
| A     | ≥ 90% |
| B     | ≥ 70% |
| C     | ≥ 50% |
| F     | < 50% |

## CLI

```bash
# Show the scorecard (text output)
tokenops scorecard

# Show as JSON (for programmatic use)
tokenops scorecard --json

# Capture a baseline for future comparison
tokenops scorecard --baseline-ref v1.0.0 --json > .scorecard-baseline.json
```

## Baseline Workflow

1. **Capture** the first scorecard after the proxy has been running
   for at least one representative session:

   ```bash
   tokenops scorecard --baseline-ref "$(tokenops version | head -1)" \
     --json > .scorecard-baseline.json
   ```

2. **Compare** subsequent scorecards against the baseline (planned):
   - Each KPI shows its current value vs. the baseline value
   - Drift is flagged when a KPI drops by one or more letter grades

3. **Iterate** on optimizations (config changes, pipeline tuning,
   instrumentation improvements) and re-run the scorecard to measure
   impact.

## Rollout Strategy

| Phase | Threshold | Target Audience |
|-------|-----------|----------------|
| 1. Monitor | Any grade; informational only | Internal / early adopters |
| 2. Advisory | Grade C or better expected | Opt-in teams |
| 3. Gate | Grade B or better required | All production deployments |

The scorecard is informational in Phase 1. It becomes advisory in
Phase 2 (warnings on CI without blocking). Full gating in Phase 3
blocks deploys that would regregate the scorecard below B.
