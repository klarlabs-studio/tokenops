package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// `task list --metrics` reads the store every other command reads. It
// always opened ~/.tokenops/events.db, whatever TOKENOPS_STORAGE_PATH said,
// so an operator who had moved the store got "metrics unavailable".
func TestTaskMetricsReadTheConfiguredStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("TOKENOPS_STORAGE_PATH", seedFixedSpendDB(t))
	tasksPath := filepath.Join(t.TempDir(), "tasks.jsonl")
	if _, err := executeRoot(t, "task", "start", "--path", tasksPath, "fix the uploader"); err != nil {
		t.Fatal(err)
	}
	out, err := executeRoot(t, "task", "list", "--path", tasksPath, "--metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Metrics) != 1 {
		t.Errorf("metrics %v (err %v) in %s; want the task's metrics from the configured store", got.Metrics, err, out)
	}
}
