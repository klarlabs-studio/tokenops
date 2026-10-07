## Burn rate — last 6h

<svg xmlns="http://www.w3.org/2000/svg" width="240" height="40" viewBox="0 0 240 40" aria-label="burn last 6h (USD)"><polyline fill="none" stroke="#4159d6" stroke-width="1.5" points="0.0,1.0 120.0,39.0"/></svg>

| Metric | Value |
|---|---|
| Total | 0.4000 USD |
| Tokens | 350000 |
| API equivalent | 2.15 USD |
| Buckets | 2 hourly |
| Range | 0.0000 .. 0.4000 USD |

<details><summary>JSON</summary>

```json
{
  "api_equivalent_usd": 2.15,
  "cost": 0.4,
  "currency": "USD",
  "hourly": [
    {
      "BucketStart": "<T>",
      "GroupKey": "",
      "Requests": 1,
      "InputTokens": 90000,
      "OutputTokens": 10000,
      "TotalTokens": 100000,
      "CostUSD": 0.4,
      "APIEquivalentUSD": 0.4,
      "CostRecomputed": 0,
      "Cost": {
        "amount": 0.4,
        "quality": "measured",
        "source": "sqlite_events",
        "observed_at": "<T>",
        "coverage": {
          "included": 1
        }
      },
      "APIEquivalent": {
        "amount": 0.4,
        "quality": "derived",
        "source": "sqlite_events",
        "caveat": "list-price equivalent, not an amount billed",
        "observed_at": "<T>",
        "coverage": {
          "included": 1
        }
      }
    },
    {
      "BucketStart": "<T>",
      "GroupKey": "",
      "Requests": 1,
      "InputTokens": 225000,
      "OutputTokens": 25000,
      "TotalTokens": 250000,
      "CostUSD": 0,
      "APIEquivalentUSD": 1.75,
      "CostRecomputed": 0,
      "Cost": {
        "amount": 0,
        "quality": "measured",
        "source": "sqlite_events",
        "observed_at": "<T>",
        "coverage": {
          "included": 1
        }
      },
      "APIEquivalent": {
        "amount": 1.75,
        "quality": "derived",
        "source": "sqlite_events",
        "caveat": "list-price equivalent, not an amount billed",
        "observed_at": "<T>",
        "coverage": {
          "included": 1
        }
      }
    }
  ],
  "hours": 6,
  "tokens": 350000
}
```

</details>
