package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seedWasteWorkflowDB writes one workflow at fixed instants that trips the
// waste detector's defaults: five consecutive steps from one agent and one
// step past the oversized-context ceiling.
func seedWasteWorkflowDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	for i, in := range []int64{1_200, 4_800, 40_000, 9_000, 12_500} {
		env := &eventschema.Envelope{
			ID: fmt.Sprintf("wt-%d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Duration(i) * time.Minute),
			Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: fmt.Sprintf("sha256:%d", i), Provider: eventschema.ProviderAnthropic,
				RequestModel: "claude-sonnet-4-5", ResponseModel: "claude-sonnet-4-5",
				InputTokens: in, OutputTokens: 300, TotalTokens: in + 300, ContextSize: in,
				Status: 200, WorkflowID: "wf-waste", AgentID: "agent-loop", SessionID: "sess-waste",
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// generatedAt masks the one field of replay's JSON that is the wall clock.
var generatedAt = regexp.MustCompile(`"generated_at": "[^"]*"`)

// TestReplayWasteCharacterization pins `tokenops replay` with a workflow
// selector — the replay plus the waste findings it attaches — byte for
// byte, so moving the trace onto the workflowtrace capability cannot
// change it.
func TestReplayWasteCharacterization(t *testing.T) {
	path := seedWasteWorkflowDB(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		golden string
		args   []string
	}{
		{"replay/workflow.txt", nil},
		{"replay/workflow.json", []string{"--json"}},
		{"replay/session_only.txt", []string{"--workflow-id", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			args := append([]string{"replay", "sess-waste", "--config", cfg, "--db", path, "--workflow-id", "wf-waste"}, tc.args...)
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			out = generatedAt.ReplaceAllString(out, `"generated_at": "<T>"`)
			if strings.HasSuffix(tc.golden, ".json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
