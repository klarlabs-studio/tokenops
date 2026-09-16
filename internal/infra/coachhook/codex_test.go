package coachhook

import "testing"

// Record shapes taken verbatim from real rollouts in ~/.codex/sessions.
func codexTurnCtx(ts, model string) string {
	return `{"timestamp":"` + ts + `","type":"turn_context","payload":{"model":"` + model + `"}}`
}

func codexTokenCount(ts string, input, cached, output int64) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{` +
		`"last_token_usage":{"input_tokens":` + itoa(input) +
		`,"cached_input_tokens":` + itoa(cached) +
		`,"cache_write_input_tokens":0,"output_tokens":` + itoa(output) + `}}}}`
}

// Codex sends the same Stop payload under the same field names as Claude
// Code, so the handler needs no translation — only the transcript does.
// The dialect is detected from the records, not from the path.
func TestEvaluateReadsACodexTranscript(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		codexTurnCtx(ts(1), "gpt-4o"),
		codexTokenCount(ts(2), 1_000_000, 0, 0),
	)
	dec := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow)
	if dec.CumulativeUSD <= 0 {
		t.Fatalf("CumulativeUSD = %v, want a priced turn — a Codex session reported as free is the failure this exists to find", dec.CumulativeUSD)
	}
}

// The pricing difference that matters: cached_input_tokens is INSIDE
// input_tokens for Codex, where Claude Code keeps them disjoint. Pricing
// it the Claude Code way bills the cached tokens twice.
func TestCodexPricesCachedTokensOnlyOnce(t *testing.T) {
	const model = "gpt-4o"
	all := codexTurnCostUSD(&codexUsage{InputTokens: 1_000_000}, model)
	if all <= 0 {
		t.Skip("no rate card for " + model)
	}
	// The same turn, with most of that input served from cache, must cost
	// strictly less — never more, and never the same.
	cached := codexTurnCostUSD(&codexUsage{InputTokens: 1_000_000, CachedInputTokens: 900_000}, model)
	if cached >= all {
		t.Errorf("cached turn cost %v, uncached %v — the cached share was not discounted", cached, all)
	}
	// And it must not exceed what the uncached turn costs, which is what
	// double-counting would produce.
	if cached > all {
		t.Errorf("cached turn cost MORE than the uncached one (%v > %v): cached tokens were billed twice", cached, all)
	}
}

// A record reporting more cached than input is not something to reason
// about; it must not credit the operator for tokens they used.
func TestCodexNeverReturnsNegativeCost(t *testing.T) {
	got := codexTurnCostUSD(&codexUsage{InputTokens: 10, CachedInputTokens: 1_000_000}, "gpt-4o")
	if got < 0 {
		t.Errorf("cost = %v, want >= 0", got)
	}
}

// The tail does not always reach a turn_context: on four real rollouts
// one had none in its last 256 KiB. Without the fallback every turn in
// that window prices at zero.
func TestCodexPricesFromTheHeadWhenTheTailHasNoModel(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		codexTurnCtx(ts(1), "gpt-4o"),
		codexTokenCount(ts(2), 1_000_000, 0, 0),
	)

	// Prove the fallback itself finds the model from the head.
	if got := codexModelFromHead(tp); got != "gpt-4o" {
		t.Fatalf("codexModelFromHead = %q, want gpt-4o", got)
	}
	dec := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow)
	if dec.CumulativeUSD <= 0 {
		t.Error("a Codex turn priced at zero; the head fallback did not apply")
	}
}

// One handler, both clients: a Claude Code transcript must still be read
// exactly as before now that the reader is dialect-aware.
func TestClaudeCodeTranscriptStillReadsAfterCodexSupport(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir, turnLine(ts(1), 20_000_000, opus))
	dec := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow)
	// 20M cache-read at opus's cached rate ($0.50/M) is $10.
	if dec.CumulativeUSD < 9.99 || dec.CumulativeUSD > 10.01 {
		t.Errorf("CumulativeUSD = %v, want ~10", dec.CumulativeUSD)
	}
}

// Detection reads the record, never the path, so a transcript is parsed
// correctly wherever the hook payload points.
func TestCodexDetectionIgnoresThePath(t *testing.T) {
	if !isCodexLine([]byte(codexTokenCount(ts(1), 1, 0, 0))) {
		t.Error("a Codex token_count record was not recognised")
	}
	if isCodexLine([]byte(turnLine(ts(1), 1, opus))) {
		t.Error("a Claude Code record was misread as Codex")
	}
}

// A model the rate card does not know must say so. Codex runs `gpt-5.5`
// and `gpt-5.6-luna` today and the shipped catalog has neither, so
// without this every Codex session reports $0 and reads as a cheap one —
// this tool's own failure mode, wearing this tool's clothes.
func TestCodexNamesAModelItCannotPrice(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		codexTurnCtx(ts(1), "gpt-5.5"),
		codexTokenCount(ts(2), 1_000_000, 0, 500),
	)
	dec := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow)
	if dec.UnpricedModel != "gpt-5.5" {
		t.Errorf("UnpricedModel = %q, want gpt-5.5 — a session nobody could price is not a free one", dec.UnpricedModel)
	}
	if dec.CumulativeUSD != 0 {
		t.Errorf("CumulativeUSD = %v, want 0 — an unknown rate must not be guessed at", dec.CumulativeUSD)
	}
}

// And a priced model must not be reported as unpriced.
func TestCodexStaysQuietWhenItCanPrice(t *testing.T) {
	dir := t.TempDir()
	tp := writeTranscript(t, dir,
		codexTurnCtx(ts(1), "gpt-4o"),
		codexTokenCount(ts(2), 1_000_000, 0, 500),
	)
	if dec := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow); dec.UnpricedModel != "" {
		t.Errorf("UnpricedModel = %q on a model the catalog knows", dec.UnpricedModel)
	}
}
