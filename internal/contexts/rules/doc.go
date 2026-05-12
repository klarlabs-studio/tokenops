// Package rules implements TokenOps Rule Intelligence (issue #12): the
// subsystem that treats operational rule artifacts (CLAUDE.md, AGENTS.md,
// Cursor rules, MCP policies, repo conventions) as first-class telemetry.
//
// This package owns the ingestion and parsing path. Downstream consumers —
// the analyzer, ROI engine, conflict detector, compressor, dynamic-injection
// router, benchmarking harness, and CLI — depend on the RuleDocument /
// RuleBlock types defined here and on the RuleSourceEvent payloads emitted
// via pkg/eventschema.
//
// The ingestion path is deliberately small and dependency-free: discovery is
// path-based, parsing is a streaming Markdown heading splitter, and content
// is reduced to anchors + hashes + sizes. Raw text never leaves the local
// rule store, so redaction is structural rather than configurable.
package rules
