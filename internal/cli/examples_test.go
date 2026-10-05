package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// shellFields splits an example line as a shell would, enough for the
// examples: quotes group words and # starts a comment.
func shellFields(line string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == '#' && !inWord:
			return out
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// Every command a person can see has an example, and every example runs
// as far as parsing: the command exists, its flags exist and take the
// values shown, and its arguments fit.
func TestExamplesParse(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			if s.Hidden || s.Name() == "help" || s.Name() == "completion" {
				continue
			}
			if s.Runnable() && s.Example == "" {
				t.Errorf("%s has no example", s.CommandPath())
			}
			// Join continued lines before checking them.
			text := strings.ReplaceAll(s.Example, "\\\n", " ")
			for _, line := range strings.Split(text, "\n") {
				fields := shellFields(strings.TrimSpace(line))
				if len(fields) == 0 || fields[0] != "tokenops" {
					continue
				}
				root := NewRoot()
				cmd, rest, err := root.Find(fields[1:])
				if err != nil {
					t.Errorf("%s example %q: %v", s.CommandPath(), line, err)
					continue
				}
				if err := cmd.ParseFlags(rest); err != nil {
					t.Errorf("%s example %q: %v", s.CommandPath(), line, err)
					continue
				}
				if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
					t.Errorf("%s example %q: %v", s.CommandPath(), line, err)
				}
			}
			walk(s)
		}
	}
	walk(NewRoot())
}
