# Cache

The proxy ships an LRU + TTL response cache keyed by
`sha256(provider + method + path + body)`. Streaming responses are
never cached; non-2xx responses are never cached.

## Per-request control

| Header value                 | Effect                                                |
|------------------------------|-------------------------------------------------------|
| `X-Tokenops-Cache: bypass`   | Skip cache lookup *and* skip store. One-off requests. |
| `X-Tokenops-Cache: refresh`  | Skip lookup, populate cache. Forces a re-fetch.       |

The proxy responds with `X-Tokenops-Cache-Status: hit | miss | bypass | refresh | store`
so clients can confirm what happened.

## Capacity

Defaults: 1024 entries, 64MB total, 4MB per-entry, 10-minute TTL.
Sized for development; bump in production via `WithCache(opts)` in a
custom daemon build.

## When something looks stale

- Send the same request with `X-Tokenops-Cache: refresh` to force a
  re-populate.
- For broader invalidation, restart the daemon — the cache is
  in-process and resets on boot.

## Privacy

The cache stores response bodies in memory only. They are not
persisted to disk and never leave the daemon. Restarting the daemon
flushes everything.
