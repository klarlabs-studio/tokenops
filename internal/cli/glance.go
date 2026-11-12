package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.klarlabs.de/tokenops/internal/capability/cards"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/mcp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// newGlanceCmd prints every plan as a card: the windows the vendor
// reports, their resets, spend against a limit, credit left. It is what
// bare `tokenops` shows.
func newGlanceCmd(rf *rootFlags) *cobra.Command {
	var (
		brief, jsonOut, noColor, onlyFindings, local bool
		dbPath, color                                string
	)
	cmd := &cobra.Command{
		Use:   "glance",
		Short: "Every plan at a glance: windows, pace, cost and the coach's findings",
		Long: `glance draws one card per plan — Claude, Codex, Gemini, pay-as-you-go
accounts — with a bar for every window the vendor reports, when it resets,
spend against a limit and credit left, busiest first, laid out to fit the
terminal. It answers from the same code as the daemon API and the menu bar.

Colour follows the terminal: the Klarlabs palette with gradient bars where
it supports true colour, 16 colours elsewhere, plain text for a pipe,
NO_COLOR or --no-color. --brief prints a table; --json the API's payload. Below the cards, the
coach's findings: a window that runs out before its reset, files the agent
keeps re-reading, sessions past their budget, the change that would most
improve how sessions go, sources that cannot be read. --findings lists
them all.

The running daemon holds the glance already, computed in the background,
so glance asks it and draws its answer: the time at the top is when the
daemon computed it. With no daemon answering, with --db or with --local,
glance computes it here from the event store.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if noColor {
				color = "never"
			}
			return runGlance(cmd, rf, dbPath, glanceView{brief: brief, json: jsonOut, findings: onlyFindings, local: local}, color)
		},
	}
	cmd.Flags().BoolVar(&brief, "brief", false, "a compact table: plan, window, used, resets")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "the glance as JSON (the daemon API's GET /api/glance); with --findings, GET /api/findings")
	cmd.Flags().BoolVar(&onlyFindings, "findings", false, "only the coach's findings, every one of them")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "plain text (same as --color never)")
	cmd.Flags().StringVar(&color, "color", "auto", "auto | always | never; always keeps colour when piping, e.g. into less -R")
	cmd.Flags().StringVar(&dbPath, "db", "", "event store path (defaults to the configured one); computes the glance here")
	cmd.Flags().BoolVar(&local, "local", false, "compute the glance here instead of asking the running daemon")
	return cmd
}

// glanceView is which view of the glance to print.
type glanceView struct{ brief, json, findings, local bool }

func runGlance(cmd *cobra.Command, rf *rootFlags, dbPath string, view glanceView, color string) error {
	cfg, err := loadConfig(rf)
	if err != nil {
		return err
	}
	if dbPath == "" && !view.local {
		if answered, err := glanceFromDaemon(cmd, mcp.DaemonPlanReader(mcp.ConfiguredDaemonURL(cfg)), view, color); answered {
			return err
		}
	}
	resolved, err := resolvePlanDBIn(dbPath, cfg.Storage.Path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	store, err := sqlite.OpenReadOnly(ctx, resolved)
	if err != nil {
		return fmt.Errorf("open store: %w (run `tokenops init`)", err)
	}
	defer func() { _ = store.Close() }()
	deps := headroom.LocalDeps(&cfg, storeReader{store: store}, nil)
	eng, engErr := buildSpendEngine(cfg)
	if engErr == nil {
		deps.Price = eng.ComputeAt
	}
	now := time.Now().UTC()
	g, err := headroom.ComputeGlance(ctx, deps, now)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	report := coachcap.Status(cfg, coachLedger(), contextLevers(), now)
	found := findings.Compute(findings.Gather(&g, &report, &cfg, findings.DefaultDir()))
	if view.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if view.findings {
			return enc.Encode(found)
		}
		return enc.Encode(g.Payload())
	}
	opt := cards.Options{Width: terminalWidth(out), Color: terminalColor(out, color), Brief: view.brief, Now: now,
		Findings: &found, OnlyFindings: view.findings}
	if engErr == nil && !view.findings {
		// Thirty days of usage is the heaviest read here; on a busy
		// machine the cards go out without cost rather than wait for it.
		costCtx, cancelCost := context.WithTimeout(ctx, costBudget)
		opt.Costs = glanceCosts(costCtx, spending.NewAggregator(store, eng), g, now)
		opt.CostsLate = costCtx.Err() != nil
		cancelCost()
	}
	if _, err = io.WriteString(out, cards.Render(g, opt)); err != nil || !view.findings {
		return err
	}
	ds, _ := findings.DefaultDismissals().Load(now)
	_, err = io.WriteString(out, hiddenNote(found, ds, now))
	return err
}

// glanceDaemonWait bounds asking the daemon. It answers the glance from
// its last computation at once; one that takes longer is busy, and the
// glance is computed here instead.
const glanceDaemonWait = 5 * time.Second

// glanceFromDaemon draws the glance the daemon holds, and reports whether
// it answered: the daemon computes the glance and its findings in the
// background, and computing them again here took seconds of CPU for the
// answer the daemon gives in milliseconds. Not answered, the caller
// computes them here.
func glanceFromDaemon(cmd *cobra.Command, ask mcp.DaemonJSON, view glanceView, color string) (bool, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), glanceDaemonWait)
	defer cancel()
	var p headroom.GlancePayload
	if err := ask(ctx, "/api/glance", &p); err != nil {
		return false, nil
	}
	// Every finding, as the CLI lists them; the hidden note below says
	// which the menu bar hides.
	var found findings.Report
	if err := ask(ctx, "/api/findings?dismissed=include", &found); err != nil {
		return false, nil
	}
	out := cmd.OutOrStdout()
	if view.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if view.findings {
			return true, enc.Encode(found)
		}
		return true, enc.Encode(&p)
	}
	now := time.Now().UTC()
	at := p.ComputedAt
	if at.IsZero() {
		at = now
	}
	opt := cards.Options{Width: terminalWidth(out), Color: terminalColor(out, color), Brief: view.brief, Now: at,
		Findings: &found, OnlyFindings: view.findings}
	if !view.findings {
		// A daemon from before the glance carried costs has none: they
		// are shown as not read rather than read here.
		opt.Costs, opt.CostsLate = p.ProviderCosts, p.ProviderCostsLate || p.ProviderCosts == nil
	}
	if _, err := io.WriteString(out, cards.Render(p.Glance(), opt)); err != nil || !view.findings {
		return true, err
	}
	ds, _ := findings.DefaultDismissals().Load(now)
	_, err := io.WriteString(out, hiddenNote(found, ds, now))
	return true, err
}

// hiddenNote says how many of the findings listed are hidden in the menu
// bar. The CLI lists every finding: a dismissal tidies the panel, it does
// not make a finding untrue.
func hiddenNote(r findings.Report, ds []findings.Dismissal, now time.Time) string {
	switch n := findings.Filter(r, ds, now).Hidden; n {
	case 0:
		return ""
	case 1:
		return "\n1 of these is hidden in the menu bar until it changes or its time is up.\n"
	default:
		return fmt.Sprintf("\n%d of these are hidden in the menu bar until they change or their time is up.\n", n)
	}
}

// costBudget bounds the cost lookups.
const costBudget = 4 * time.Second

// glanceCosts is each plan's provider's usage today and over 30 days; a
// provider whose figures cannot be read shows none.
func glanceCosts(ctx context.Context, s spending.Summarizer, g headroom.Glance, now time.Time) map[string]spending.ProviderCost {
	return spending.CostsOf(ctx, s, g.Headroom.Providers(), now)
}

// terminalWidth is $COLUMNS, else the terminal's width, else 80.
func terminalWidth(w io.Writer) int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	if f, ok := w.(*os.File); ok {
		if n, _, err := term.GetSize(int(f.Fd())); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// terminalColor is plain for never, NO_COLOR, or (on auto) a pipe or a
// dumb terminal; otherwise true colour where the terminal says it takes
// it, and 16 colours elsewhere.
func terminalColor(w io.Writer, mode string) cards.Color {
	switch mode {
	case "never":
		return cards.NoColor
	case "always":
	default:
		f, ok := w.(*os.File)
		if os.Getenv("NO_COLOR") != "" || !ok || !term.IsTerminal(int(f.Fd())) || os.Getenv("TERM") == "dumb" {
			return cards.NoColor
		}
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return cards.TrueColor
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "ghostty", "vscode", "Apple_Terminal":
		return cards.TrueColor
	}
	return cards.Basic
}
