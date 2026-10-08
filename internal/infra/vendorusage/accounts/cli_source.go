package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// cliSource is the generic CLI poller: it runs the vendor CLI its source's
// descriptor names (providers.Command) and parses the output. Everything
// about running it is the descriptor's: the binary, the argument lists,
// the deadline and the environment it may see; only the parser is code.
// It is Keyless: a CLI that is not installed is skipped silently, and a
// CLI that is not signed in is a refusal, never a sign-in started from
// the background.
type cliSource struct {
	provider eventschema.Provider
	tag      string
	parse    func(out string) (usage.Reading, error)
	// bin overrides finding the binary, for tests.
	bin string
}

func (c cliSource) Endpoint() string               { return c.tag }
func (c cliSource) Provider() eventschema.Provider { return c.provider }
func (c cliSource) Source() string                 { return c.tag }
func (cliSource) Keyless()                         {}

// errNoUsage is output the parser found no usage in.
var errNoUsage = errors.New("no usage in the output")

func (c cliSource) Read(ctx context.Context, _ string) (usage.Reading, error) {
	src, ok := providers.ForSource(c.tag)
	if !ok || src.Command == nil {
		return usage.Reading{}, fmt.Errorf("accounts: %s has no command", c.tag)
	}
	cmd := src.Command
	bin, found := c.bin, c.bin != ""
	if !found {
		override := ""
		if cmd.PathEnv != "" {
			override = strings.TrimSpace(os.Getenv(cmd.PathEnv))
		}
		bin, found = locateCLI(cmd.Binary, override)
	}
	if !found {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	env := append(os.Environ(), "NO_COLOR=1")
	if len(cmd.EnvAllow) > 0 {
		env = allowedEnv(cmd.EnvAllow)
	}
	var lastErr error
	for _, args := range cmd.Args {
		out, err := runCommand(ctx, cmd.Timeout, bin, env, args...)
		if len(cmd.SignedOut) > 0 && mentions(out, cmd.SignedOut...) {
			return usage.Reading{}, cliAuthError(cmd.Binary)
		}
		if err != nil {
			lastErr = err
			continue
		}
		r, err := c.parse(out)
		if err == nil {
			return r, nil
		}
		// The parse error names what was missing, never the output.
		lastErr = fmt.Errorf("accounts: %s %s: %w", cmd.Binary, strings.Join(args[:min(2, len(args))], " "), err)
	}
	return usage.Reading{}, lastErr
}

// cliWindow names a window by its label and length.
func cliWindow(d time.Duration, used float64, reset time.Time) usage.Window {
	return usage.Window{Name: windowName(d), UsedPct: clampPct(used), Duration: d, ResetsAt: reset}
}
