package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStatusOutputCharacterization pins `tokenops status` byte for byte
// against a canned daemon, so moving its state derivation into
// internal/capability/state cannot change what an operator reads. The
// config names a retention source that never wrote an event, and the
// daemon reports dropped rows, so both warning paths are exercised.
func TestStatusOutputCharacterization(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedFixedSpendDB(t)
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "storage:\n  enabled: true\n  path: " + dbPath + "\n" +
		"retention:\n  keep_by_source:\n    no-such-source: 30d\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		golden string
		ready  fakeResponse
		health string
		json   bool
	}{
		{"status/ready.txt", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok"}`, false},
		{"status/dropped.json", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok","dropped_events":12}`, true},
		{"status/not_configured.txt", fakeResponse{503, `{"status":"not_configured"}`}, `{"status":"ok"}`, false},
		{"status/not_ready.json", fakeResponse{503, `{"status":"not_ready"}`}, `{"status":"ok"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			prev := statusClient
			t.Cleanup(func() { SetStatusClient(prev) })
			SetStatusClient(&fakeDoer{responses: map[string]fakeResponse{
				"/healthz": {status: 200, body: tc.health},
				"/readyz":  tc.ready,
				"/version": {status: 200, body: `{"version":"dev"}`},
			}})
			args := []string{"--config", cfgPath, "status", "--addr", "127.0.0.1:7878"}
			if tc.json {
				args = append(args, "--json")
			}
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
