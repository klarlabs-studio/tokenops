package mcp_test

import (
	"bytes"
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"go.klarlabs.de/tokenops/internal/cli"
	"go.klarlabs.de/tokenops/internal/mcp"
)

// shellWords runs command in bash with tokenops defined as a function that
// prints its arguments, so the words are the ones a shell hands the binary
// when the operator pastes the line: quoting, expansion and splitting are
// bash's own. Nothing but printf runs. One entry per tokenops invocation.
func shellWords(t *testing.T, command string) [][]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	script := `tokenops() { printf '%s\0' "$@"; printf '\1'; }; ` + command
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_DATA_HOME=" + home, "XDG_STATE_HOME=" + home}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("bash %q: %v %s", command, err, stderr.String())
	}
	recs := strings.Split(strings.TrimSuffix(string(out), "\x01"), "\x01")
	calls := make([][]string, 0, len(recs))
	for _, rec := range recs {
		calls = append(calls, strings.Split(strings.TrimSuffix(rec, "\x00"), "\x00"))
	}
	return calls
}

// parsed is what the terminal's command tree makes of words: the command
// it resolves to, its positional words and the flags set. No command
// runs: every run and pre-run hook is replaced by one that records.
func parsed(t *testing.T, words []string) mcp.Invocation {
	t.Helper()
	root := cli.NewRoot()
	var got *mcp.Invocation
	var strip func(c *cobra.Command)
	strip = func(c *cobra.Command) {
		c.PersistentPreRun, c.PersistentPreRunE, c.PreRun, c.PreRunE = nil, nil, nil, nil
		c.PostRun, c.PostRunE, c.PersistentPostRun, c.PersistentPostRunE = nil, nil, nil, nil
		c.Run = nil
		c.RunE = func(cmd *cobra.Command, args []string) error {
			inv := mcp.Invocation{Path: strings.TrimPrefix(cmd.CommandPath(), root.Name()+" "), Args: args, Flags: map[string]any{}}
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if s, ok := f.Value.(pflag.SliceValue); ok {
					inv.Flags[f.Name] = s.GetSlice()
					return
				}
				inv.Flags[f.Name] = f.Value.String()
			})
			got = &inv
			return nil
		}
		for _, sub := range c.Commands() {
			strip(sub)
		}
	}
	strip(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(words)
	if _, err := root.ExecuteContextC(context.Background()); err != nil {
		t.Fatalf("%q does not parse: %v", words, err)
	}
	if got == nil {
		t.Fatalf("%q reached no command", words)
	}
	return *got
}

// The command each call shows the operator, pasted into a shell, reaches
// the same tokenops subcommand with the same positional words and flag
// values the tool applies: values that start with a dash stay positional,
// and quotes, $, backticks and spaces reach the command as they were.
func TestShownCommandsParseToWhatTheToolApplies(t *testing.T) {
	isolate(t)
	for _, c := range mcp.ExactCases {
		route, command, err := mcp.ShownCommand(c.Tool, c.Args)
		if err != nil {
			t.Fatalf("%s %v: %v", c.Tool, c.Args, err)
		}
		t.Run(c.Tool+"/"+route+"/"+command, func(t *testing.T) {
			calls := shellWords(t, command)
			if len(calls) != len(c.Want) {
				t.Fatalf("%q runs tokenops %d times, want %d", command, len(calls), len(c.Want))
			}
			for i, words := range calls {
				got, want := parsed(t, words), c.Want[i]
				if want.Flags == nil {
					want.Flags = map[string]any{}
				}
				if want.Args == nil {
					want.Args = []string{}
				}
				if got.Args == nil {
					got.Args = []string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%q parses as\n  %#v\nwant\n  %#v", command, got, want)
				}
			}
		})
	}
}

// isolate points every directory tokenops could read or write at a
// temporary one, though no command runs here.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, dir)
	}
}
