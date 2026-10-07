package claudeusagemeter

const sampleOrgs = `[{"uuid":"org-abc","name":"My Org","capabilities":["claude_pro"]}]`

// enterpriseUsage is a real Claude Enterprise response (2026-09-19), cut to
// the blocks this meter reads plus neighbours it must ignore: every window
// null, spend in extra_usage.
const enterpriseUsage = `{
  "five_hour": null, "seven_day": null, "seven_day_opus": null,
  "seven_day_sonnet": null, "limits": null,
  "extra_usage": {"is_enabled": true, "monthly_limit": 150000, "used_credits": 109563,
    "utilization": 73.042, "currency": "USD", "decimal_places": 2, "disabled_reason": null,
    "user_disabled": false, "spend_limit_reached": false, "credits_ever_enabled": true,
    "daily": null, "weekly": null}
}`

// chatOnlyUsage is a personal chat-only org on the same account: nothing to
// meter at all.
const chatOnlyUsage = `{"five_hour": null, "seven_day": null, "seven_day_opus": null, "extra_usage": null}`
