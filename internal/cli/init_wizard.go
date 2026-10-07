package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/planswitch"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
)

// wizard asks, after init has done everything it can on its own, only
// what nothing on the machine could answer: which plan when the evidence
// names several, what the operator pays, an Enterprise spend limit nothing
// reports, the coach preset on a new config, and whether to supervise the
// daemon. Every question has a default, so Enter accepts what init would
// have done anyway.
type wizard struct {
	in  *bufio.Reader
	out io.Writer
	// closed is set once input ends. From then on nothing more is asked
	// and nothing more is done: running out of answers is not consent.
	closed bool
	// installDaemon installs the supervised unit; a variable for tests.
	installDaemon func(out io.Writer) error
}

// interactiveInput reports whether init may ask: its input is a terminal,
// and --yes was not passed.
func interactiveInput(r io.Reader, yes bool) bool {
	if yes {
		return false
	}
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func newWizard(in io.Reader, out io.Writer) *wizard {
	return &wizard{in: bufio.NewReader(in), out: out, installDaemon: execDaemonInstall}
}

// line reads one answer, trimmed. ok is false once input has ended.
func (w *wizard) line() (string, bool) {
	if w.closed {
		return "", false
	}
	s, err := w.in.ReadString('\n')
	if err != nil && strings.TrimSpace(s) == "" {
		w.closed = true
		fmt.Fprintln(w.out)
		return "", false
	}
	return strings.TrimSpace(s), true
}

// choose offers options and returns the index chosen: def on Enter, or
// -1 (skip) on Enter when skippable. Once input has ended it returns -1
// when skippable and def otherwise, which is what init does unasked.
func (w *wizard) choose(question string, options []string, def int, skippable bool) int {
	if w.closed {
		if skippable {
			return -1
		}
		return def
	}
	fmt.Fprintf(w.out, "\n%s\n", question)
	for i, o := range options {
		mark := " "
		if i == def && !skippable {
			mark = "*"
		}
		fmt.Fprintf(w.out, "  %s %d) %s\n", mark, i+1, o)
	}
	hint := fmt.Sprintf("Enter for %d", def+1)
	if skippable {
		hint = "Enter to skip"
	}
	for {
		fmt.Fprintf(w.out, "choice (%s): ", hint)
		ans, ok := w.line()
		if !ok {
			if skippable {
				return -1
			}
			return def
		}
		if ans == "" {
			if skippable {
				return -1
			}
			return def
		}
		if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
		fmt.Fprintf(w.out, "  pick 1-%d\n", len(options))
	}
}

// amount asks for a positive number; 0 on Enter.
func (w *wizard) amount(question string) float64 {
	for {
		if w.closed {
			return 0
		}
		fmt.Fprintf(w.out, "%s ", question)
		raw, ok := w.line()
		ans := strings.TrimPrefix(raw, "$")
		if !ok || ans == "" {
			return 0
		}
		if v, err := strconv.ParseFloat(strings.ReplaceAll(ans, ",", "."), 64); err == nil && v > 0 {
			return v
		}
		fmt.Fprintln(w.out, "  a number, e.g. 200, or Enter to skip")
	}
}

// confirm asks yes or no: def on Enter, and no once input has ended,
// because what it guards does something.
func (w *wizard) confirm(question string, def bool) bool {
	if w.closed {
		return false
	}
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	fmt.Fprintf(w.out, "\n%s [%s] ", question, hint)
	ans, ok := w.line()
	if !ok {
		return false
	}
	switch strings.ToLower(ans) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

// plans asks for the plans the evidence could not settle, then what each
// plan bound in this run costs, and an Enterprise limit nothing reports.
func (w *wizard) plans(cfgPath, home string, boundBefore map[string]string) {
	cfg, err := config.ReadMutable(cfgPath)
	if err != nil {
		return
	}
	for _, e := range planevidence.All(home) {
		if cfg.Plans[e.Provider] != "" || len(e.Candidates) == 0 {
			continue
		}
		i := w.choose(fmt.Sprintf("%s reports %s. Which plan are you on?", e.From, e.Detail), e.Candidates, 0, true)
		if i < 0 {
			continue
		}
		if _, err := actions.SetPlan(context.Background(), cfgPath, actions.PlanRequest{
			Provider: e.Provider, Plan: e.Candidates[i], Actor: "init",
		}, time.Now().UTC()); err != nil {
			fmt.Fprintf(w.out, "  could not bind it: %v\n", err)
		}
	}
	cfg, err = config.ReadMutable(cfgPath)
	if err != nil {
		return
	}
	currency := cfg.Money.Currency
	if currency == "" {
		currency = "USD"
	}
	for _, provider := range slices.Sorted(maps.Keys(cfg.Plans)) {
		name := cfg.Plans[provider]
		if boundBefore[provider] == name {
			continue // the operator's own binding, priced or not already
		}
		p, ok := planswitch.Lookup(name)
		if !ok {
			continue
		}
		req := actions.PlanRequest{Provider: provider, Plan: name, Actor: "init", Currency: currency}
		if p.SpendDenominated && cfg.PlanLimits[provider].SpendLimitUSD == 0 {
			req.SpendLimitUSD = w.amount(fmt.Sprintf("\n%s is billed by usage. Your monthly spend limit in USD (Enter if Claude reports it to TokenOps):", p.Display))
		} else {
			req.Price = w.amount(fmt.Sprintf("\nWhat do you pay for %s a month, in %s, as on your bill? (Enter to skip)", p.Display, currency))
		}
		if req.Price == 0 && req.SpendLimitUSD == 0 {
			continue
		}
		if _, err := actions.SetPlan(context.Background(), cfgPath, req, time.Now().UTC()); err != nil {
			fmt.Fprintf(w.out, "  could not record it: %v\n", err)
		}
	}
}

// preset asks for the coach preset; advise is the default.
func (w *wizard) preset() string {
	presets := coachcap.Presets()
	options := make([]string, 0, len(presets))
	def := 0
	for i, p := range presets {
		options = append(options, p.Name+": "+p.Summary)
		if p.Name == coachcap.PresetDefault {
			def = i
		}
	}
	return presets[w.choose("How much should the coach do?", options, def, false)].Name
}

// daemon offers to supervise ingestion when nothing does.
func (w *wizard) daemon(steps []setupStep) {
	for _, s := range steps {
		if s.Name != "daemon unit" || !s.Manual {
			continue
		}
		if !w.confirm("Keep ingesting across reboots? This installs a launchd/systemd unit for `tokenops start`.", true) {
			return
		}
		if err := w.installDaemon(w.out); err != nil {
			fmt.Fprintf(w.out, "  the daemon was not installed: %v — `tokenops daemon install` retries\n", err)
		}
		return
	}
}

// execDaemonInstall runs `tokenops daemon install` as this binary, so the
// wizard and the command cannot install it differently.
func execDaemonInstall(out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "daemon", "install") //nolint:gosec // this binary, fixed arguments
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}
