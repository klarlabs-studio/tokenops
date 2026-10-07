package analytics

// Stored events carry the cache portions of their input in the payload.
// Claude Code events written before the payload had cache-write fields
// recorded the writes as the cache_creation_input attribute, inside an
// InputTokens that already included them, so they are read as writes too.
// No other source recorded writes inside InputTokens before the payload
// field existed, so no other attribute is a safe fallback.
const (
	cacheReadExpr    = `CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER)`
	cacheWriteExpr   = `CAST(COALESCE(json_extract(payload, '$.cache_write_input_tokens'), json_extract(attributes, '$.cache_creation_input')) AS INTEGER)`
	cacheWrite1hExpr = `CAST(json_extract(payload, '$.cache_write_1h_input_tokens') AS INTEGER)`
)
