package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withConfigFlag gives a command built on its own the global --config it
// inherits from the root in the real tree.
func withConfigFlag(cmd *cobra.Command) *cobra.Command {
	if cmd.Flag("config") == nil {
		cmd.PersistentFlags().StringP("config", "c", "", "path to config.yaml")
	}
	return cmd
}

// The global --config is the file every command changes, wherever it sits
// on the command line: a write that went to the default config instead
// would look like it worked.
func TestGlobalConfigFlagReachesCommandsThatWrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{
		{"-c", "PATH", "plan", "set", "anthropic", "claude-max-20x", "--no-restart"},
		{"plan", "set", "anthropic", "claude-max-20x", "--config", "PATH", "--no-restart"},
	} {
		path := filepath.Join(t.TempDir(), "other.yaml")
		if err := os.WriteFile(path, []byte("storage:\n  enabled: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for i, a := range args {
			if a == "PATH" {
				args[i] = path
			}
		}
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		b, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(b), "claude-max-20x") {
			t.Errorf("%v did not write %s:\n%s", args, path, b)
		}
		if def, err := defaultConfigPath(); err == nil {
			if _, err := os.Stat(def); err == nil {
				t.Errorf("%v also wrote the default config %s", args, def)
			}
		}
	}
}
