package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// codexRow is a Codex turn as the poller stored it: out and total are the
// stored figures, reasoning the reasoning_output attribute. marked rows
// carry the parser_version the fixed poller writes.
type codexRow struct {
	id, source        string
	in, out, total    int64
	reasoning         string
	marked            bool
	cost              float64
	costMeasured      bool
	wantOut, wantTot  int64
	wantMarked        bool
	wantCost          float64
	wantCostMeasured  bool
	wantCostInPayload bool
}

func (r codexRow) envelope(at time.Time) *eventschema.Envelope {
	attrs := map[string]string{"endpoint": "api.openai.com", "cached_input": "10"}
	if r.reasoning != "" {
		attrs["reasoning_output"] = r.reasoning
	}
	if r.marked {
		attrs["parser_version"] = "2"
	}
	return &eventschema.Envelope{
		ID: r.id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: at, Source: r.source, Attributes: attrs,
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-5.5",
			InputTokens: r.in, CachedInputTokens: 10, OutputTokens: r.out, TotalTokens: r.total,
			CostUSD: r.cost, CostMeasured: r.costMeasured, CostSource: eventschema.CostSourceMetered,
			SessionID: "s1", Status: 200,
		},
	}
}

// TestCorrectCodexOutputSubtractsReasoningOnce stores Codex turns as
// releases up to v0.118.18 did (output_tokens = Codex's output_tokens +
// reasoning_output, which it already included) beside rows that must be
// left alone, then corrects them in batches smaller than the store so the
// walk has to page.
func TestCorrectCodexOutputSubtractsReasoningOnce(t *testing.T) {
	rows := []codexRow{
		// Old parser, reasoning 30 of a real output of 50: stored 80/180.
		{id: "old", source: "codex-jsonl", in: 100, out: 80, total: 180, reasoning: "30",
			wantOut: 50, wantTot: 150, wantMarked: true},
		// Old parser, no reasoning: the figures were right, only marked.
		{id: "old-no-reasoning", source: "codex-jsonl", in: 100, out: 50, total: 150, reasoning: "0",
			wantOut: 50, wantTot: 150, wantMarked: true},
		// Old parser, the whole output was reasoning: stored 40/140.
		{id: "old-all-reasoning", source: "codex-jsonl", in: 100, out: 40, total: 140, reasoning: "20",
			wantOut: 20, wantTot: 120, wantMarked: true},
		// Fixed parser: already right, never touched.
		{id: "new", source: "codex-jsonl", in: 100, out: 50, total: 150, reasoning: "30", marked: true,
			wantOut: 50, wantTot: 150, wantMarked: true},
		// Another source with the same attribute name is not Codex's poller.
		{id: "other-source", source: "proxy", in: 100, out: 80, total: 180, reasoning: "30",
			wantOut: 80, wantTot: 180},
		// Not the old poller's shape (reasoning above output): left alone.
		{id: "reasoning-above-output", source: "codex-jsonl", in: 100, out: 20, total: 120, reasoning: "30",
			wantOut: 20, wantTot: 120},
		// Not the old poller's shape (total is not input + output).
		{id: "total-mismatch", source: "codex-jsonl", in: 100, out: 80, total: 200, reasoning: "30",
			wantOut: 80, wantTot: 200},
		// No reasoning attribute: nothing says how much to take off.
		{id: "no-attribute", source: "codex-jsonl", in: 100, out: 80, total: 180,
			wantOut: 80, wantTot: 180},
		// A cost priced from the rate card is cleared, to be priced on read.
		{id: "rate-card-cost", source: "codex-jsonl", in: 100, out: 80, total: 180, reasoning: "30", cost: 0.5,
			wantOut: 50, wantTot: 150, wantMarked: true},
		// A cost the recorder stated is what was billed: kept.
		{id: "measured-cost", source: "codex-jsonl", in: 100, out: 80, total: 180, reasoning: "30",
			cost: 0.5, costMeasured: true,
			wantOut: 50, wantTot: 150, wantMarked: true, wantCost: 0.5, wantCostMeasured: true, wantCostInPayload: true},
	}

	ctx := context.Background()
	s := newTestStore(t)
	at := time.Now().Add(-time.Hour).UTC()
	for i, r := range rows {
		// Several rows share a timestamp, so paging must break ties.
		if err := s.Append(ctx, r.envelope(at.Add(time.Duration(i/3)*time.Second))); err != nil {
			t.Fatalf("append %s: %v", r.id, err)
		}
	}

	n, err := s.CorrectCodexOutput(ctx, 2)
	if err != nil {
		t.Fatalf("correct: %v", err)
	}
	// old, old-no-reasoning, old-all-reasoning, rate-card-cost, measured-cost.
	if n != 5 {
		t.Errorf("corrected %d rows, want 5", n)
	}
	for _, r := range rows {
		t.Run(r.id, func(t *testing.T) { checkCodexRow(t, s, r) })
	}

	// Idempotent: a second pass changes nothing.
	n, err = s.CorrectCodexOutput(ctx, 2)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass corrected %d rows, want 0", n)
	}
	for _, r := range rows {
		t.Run(r.id+"/after-second-pass", func(t *testing.T) { checkCodexRow(t, s, r) })
	}
}

func TestCorrectCodexOutputStopsWhenCancelled(t *testing.T) {
	s := newTestStore(t)
	r := codexRow{id: "old", source: "codex-jsonl", in: 100, out: 80, total: 180, reasoning: "30"}
	if err := s.Append(context.Background(), r.envelope(time.Now())); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.CorrectCodexOutput(ctx, 10); err == nil {
		t.Fatal("cancelled pass returned no error")
	}
}

func checkCodexRow(t *testing.T, s *Store, r codexRow) {
	t.Helper()
	var out, total int64
	var cost *float64
	if err := s.DB().QueryRow(`SELECT coalesce(output_tokens, 0), coalesce(total_tokens, 0), cost_usd FROM events WHERE id = ?`, r.id).
		Scan(&out, &total, &cost); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if out != r.wantOut || total != r.wantTot {
		t.Errorf("columns output/total = %d/%d, want %d/%d", out, total, r.wantOut, r.wantTot)
	}
	gotCost := 0.0
	if cost != nil {
		gotCost = *cost
	}
	wantColCost := r.wantCost
	if !r.wantMarked {
		wantColCost = r.cost // an untouched row keeps what it had
	}
	if gotCost != wantColCost {
		t.Errorf("cost_usd = %v, want %v", gotCost, wantColCost)
	}

	envs, err := s.Query(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, env := range envs {
		if env.ID != r.id {
			continue
		}
		p := env.Payload.(*eventschema.PromptEvent)
		if p.OutputTokens != r.wantOut || p.TotalTokens != r.wantTot {
			t.Errorf("payload output/total = %d/%d, want %d/%d", p.OutputTokens, p.TotalTokens, r.wantOut, r.wantTot)
		}
		if p.InputTokens != r.in || p.CachedInputTokens != 10 {
			t.Errorf("input changed: %d/%d", p.InputTokens, p.CachedInputTokens)
		}
		if got := env.Attributes["parser_version"] == "2"; got != r.wantMarked {
			t.Errorf("parser_version marked = %v, want %v", got, r.wantMarked)
		}
		if r.wantMarked && r.cost > 0 {
			if (p.CostUSD != 0) != r.wantCostInPayload || p.CostMeasured != r.wantCostMeasured {
				t.Errorf("payload cost = %v measured %v, want present=%v measured=%v",
					p.CostUSD, p.CostMeasured, r.wantCostInPayload, r.wantCostMeasured)
			}
		}
		if env.Attributes["reasoning_output"] != r.reasoning {
			t.Errorf("reasoning_output = %q, want %q", env.Attributes["reasoning_output"], r.reasoning)
		}
		return
	}
	t.Fatalf("event %s not found", r.id)
}

// TestCorrectCodexOutputPlan pins that each batch reads only its own
// rows: the walk by events_source_ts_idx alone, the update by rowid. A
// batch that scanned every Codex turn took minutes on a real store.
func TestCorrectCodexOutputPlan(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct {
		name, sql, want string
		args            []any
	}{
		{"walk", codexBatchSQL, "COVERING INDEX events_source_ts_idx", []any{codexSource, 0, 0, 10}},
		{"update", correctCodexOutputSQL, "USING INTEGER PRIMARY KEY", []any{"[1]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.DB().Query("EXPLAIN QUERY PLAN "+tc.sql, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if !strings.Contains(plan[0], tc.want) {
				t.Errorf("plan %q, want its first step to use %q", plan, tc.want)
			}
		})
	}
}
