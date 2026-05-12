package eventschema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Each test below pins one payload kind to its on-the-wire JSON shape so
// any unintentional rename / type-widening / field removal trips the
// telemetry-contract-and-lineage-control schema policy. Updating a
// golden requires bumping SchemaVersion per docs/telemetry-contracts.md.

const fixedID = "01HABCDEF0123456789KLMNOPQRS"

var fixedTime = time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)

func canon(t *testing.T, env Envelope) string {
	t.Helper()
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Whitespace-trim and normalize line endings so the golden compares
	// independently of platform.
	out := strings.TrimSpace(string(b))
	return strings.ReplaceAll(out, "\r\n", "\n")
}

func assertEqualGolden(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	gLines := strings.Split(got, "\n")
	wLines := strings.Split(want, "\n")
	var diff bytes.Buffer
	maxLines := max(len(gLines), len(wLines))
	for i := range maxLines {
		var g, w string
		if i < len(gLines) {
			g = gLines[i]
		}
		if i < len(wLines) {
			w = wLines[i]
		}
		if g != w {
			diff.WriteString("line ")
			writeInt(&diff, i+1)
			diff.WriteString(": got=")
			diff.WriteString(g)
			diff.WriteString("  want=")
			diff.WriteString(w)
			diff.WriteByte('\n')
		}
	}
	t.Fatalf("wire format drift; schema policy violated:\n%s\nfull-got:\n%s", diff.String(), got)
}

func writeInt(b *bytes.Buffer, n int) {
	if n < 0 {
		b.WriteByte('-')
		n = -n
	}
	if n == 0 {
		b.WriteByte('0')
		return
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(tmp[i:])
}

func TestGoldenPromptEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypePrompt,
		Timestamp:     fixedTime,
		Source:        "proxy",
		Payload: &PromptEvent{
			PromptHash:   "sha256:abc",
			Provider:     ProviderOpenAI,
			RequestModel: "gpt-4o-mini",
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
			ContextSize:  80,
			Latency:      250 * time.Millisecond,
			Streaming:    true,
			Status:       200,
			FinishReason: "stop",
		},
	}
	got := canon(t, env)
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "prompt",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "proxy",
  "payload": {
    "prompt_hash": "sha256:abc",
    "provider": "openai",
    "request_model": "gpt-4o-mini",
    "input_tokens": 100,
    "output_tokens": 50,
    "total_tokens": 150,
    "context_size": 80,
    "latency_ns": 250000000,
    "streaming": true,
    "status": 200,
    "finish_reason": "stop"
  }
}`
	assertEqualGolden(t, got, want)
}

func TestGoldenRuleSourceEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypeRuleSource,
		Timestamp:     fixedTime,
		Source:        "rule-engine",
		Payload: &RuleSourceEvent{
			SourceID:    "repo:CLAUDE.md",
			Source:      RuleSourceClaudeMD,
			Scope:       RuleScopeRepo,
			Path:        "CLAUDE.md",
			RepoID:      "repo",
			Tokenizer:   "openai/cl100k_base",
			Provider:    ProviderOpenAI,
			TotalTokens: 1200,
			TotalChars:  4800,
			Hash:        "sha256:deadbeef",
			Sections: []RuleSection{
				{ID: "repo:CLAUDE.md#Testing", Anchor: "Testing", TokenCount: 200, CharCount: 800, Hash: "sha256:s1"},
			},
			IngestedAt: fixedTime,
		},
	}
	got := canon(t, env)
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "rule_source",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "rule-engine",
  "payload": {
    "source_id": "repo:CLAUDE.md",
    "source": "claude_md",
    "scope": "repo",
    "path": "CLAUDE.md",
    "repo_id": "repo",
    "tokenizer": "openai/cl100k_base",
    "provider": "openai",
    "total_tokens": 1200,
    "total_chars": 4800,
    "hash": "sha256:deadbeef",
    "sections": [
      {
        "id": "repo:CLAUDE.md#Testing",
        "anchor": "Testing",
        "token_count": 200,
        "char_count": 800,
        "hash": "sha256:s1"
      }
    ],
    "ingested_at": "2026-05-11T12:00:00Z"
  }
}`
	assertEqualGolden(t, got, want)
}

func TestGoldenRuleAnalysisEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypeRuleAnalysis,
		Timestamp:     fixedTime,
		Source:        "rule-engine",
		Payload: &RuleAnalysisEvent{
			SourceID:         "repo:CLAUDE.md",
			SectionID:        "repo:CLAUDE.md#Testing",
			WorkflowID:       "wf-1",
			WindowStart:      fixedTime.Add(-time.Hour),
			WindowEnd:        fixedTime,
			Exposures:        87,
			ContextTokens:    17400,
			TokensSaved:      3100,
			RetriesAvoided:   12,
			ContextReduction: 0.19,
			QualityDelta:     0.03,
			ROIScore:         0.42,
			CompressedTokens: 90,
		},
	}
	got := canon(t, env)
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "rule_analysis",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "rule-engine",
  "payload": {
    "source_id": "repo:CLAUDE.md",
    "section_id": "repo:CLAUDE.md#Testing",
    "workflow_id": "wf-1",
    "window_start": "2026-05-11T11:00:00Z",
    "window_end": "2026-05-11T12:00:00Z",
    "exposures": 87,
    "context_tokens": 17400,
    "tokens_saved": 3100,
    "retries_avoided": 12,
    "context_reduction": 0.19,
    "quality_delta": 0.03,
    "roi_score": 0.42,
    "compressed_tokens": 90
  }
}`
	assertEqualGolden(t, got, want)
}

func TestGoldenWorkflowEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypeWorkflow,
		Timestamp:     fixedTime,
		Source:        "agent",
		Payload: &WorkflowEvent{
			WorkflowID:             "wf-1",
			AgentID:                "planner",
			State:                  WorkflowStateProgress,
			StepCount:              3,
			CumulativeInputTokens:  300,
			CumulativeOutputTokens: 100,
			CumulativeTotalTokens:  400,
			Duration:               500 * time.Millisecond,
		},
	}
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "workflow",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "agent",
  "payload": {
    "workflow_id": "wf-1",
    "agent_id": "planner",
    "state": "progress",
    "step_count": 3,
    "cumulative_input_tokens": 300,
    "cumulative_output_tokens": 100,
    "cumulative_total_tokens": 400,
    "duration_ns": 500000000
  }
}`
	assertEqualGolden(t, canon(t, env), want)
}

func TestGoldenOptimizationEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypeOptimization,
		Timestamp:     fixedTime,
		Source:        "optimizer",
		Payload: &OptimizationEvent{
			PromptHash:             "sha256:abc",
			Kind:                   OptimizationTypePromptCompress,
			Mode:                   OptimizationModePassive,
			EstimatedSavingsTokens: 120,
			QualityScore:           0.92,
			Decision:               OptimizationDecisionAccepted,
			Reason:                 "ok",
		},
	}
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "optimization",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "optimizer",
  "payload": {
    "prompt_hash": "sha256:abc",
    "kind": "prompt_compress",
    "mode": "passive",
    "estimated_savings_tokens": 120,
    "quality_score": 0.92,
    "decision": "accepted",
    "reason": "ok"
  }
}`
	assertEqualGolden(t, canon(t, env), want)
}

func TestGoldenCoachingEnvelope(t *testing.T) {
	env := Envelope{
		ID:            fixedID,
		SchemaVersion: SchemaVersion,
		Type:          EventTypeCoaching,
		Timestamp:     fixedTime,
		Source:        "coach",
		Payload: &CoachingEvent{
			SessionID:              "sess-1",
			WorkflowID:             "wf-1",
			Kind:                   CoachingKindReducePromptSize,
			Summary:                "Trim context",
			EstimatedSavingsTokens: 500,
			EfficiencyScore:        0.75,
		},
	}
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.1.0",
  "type": "coaching",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "coach",
  "payload": {
    "session_id": "sess-1",
    "workflow_id": "wf-1",
    "kind": "reduce_prompt_size",
    "summary": "Trim context",
    "estimated_savings_tokens": 500,
    "efficiency_score": 0.75
  }
}`
	assertEqualGolden(t, canon(t, env), want)
}

func TestGoldenSchemaVersionMatchesPolicy(t *testing.T) {
	if SchemaVersion != "1.1.0" {
		t.Errorf("SchemaVersion = %q; bumping requires updating docs/telemetry-contracts.md and golden tests", SchemaVersion)
	}
}
