package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.klarlabs.de/tokenops/internal/capability/cards"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// newGlanceCmd prints every plan as a card: the windows the vendor
// reports, their resets, spend against a limit, credit left. It is what
// bare `tokenops` shows.
func newGlanceCmd(rf *rootFlags) *cobra.Command {
	var (
		brief, jsonOut, noColor bool
		dbPath, color           string
	)
	cmd := &cobra.Command{
		Use:   "glance",
		Short: "Every plan at a glance: windows, resets, spend, as terminal cards",
		Long: `glance draws one card per plan — Claude, Codex, Gemini, pay-as-you-go
accounts — with a bar for every window the vendor reports, when it resets,
spend against a limit and credit left, busiest first, laid out to fit the
terminal. It answers from the same code as the daemon API and the menu bar.

Colour follows the terminal: the Klarlabs palette with gradient bars where
it supports true colour, 16 colours elsewhere, plain text for a pipe,
NO_COLOR or --no-color. --brief prints a table; --json the API's payload.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if noColor {
				color = "never"
			}
			return runGlance(cmd, rf, dbPath, brief, jsonOut, color)
		},
	}
	cmd.Flags().BoolVar(&brief, "brief", false, "a compact table: plan, window, used, resets")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "the glance as JSON (the daemon API's GET /api/glance)")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "plain text (same as --color never)")
	cmd.Flags().StringVar(&color, "color", "auto", "auto | always | never; always keeps colour when piping, e.g. into less -R")
	cmd.Flags().StringVar(&dbPath, "db", "", "event store path (defaults to the configured one)")
	return cmd
}

func runGlance(cmd *cobra.Command, rf *rootFlags, dbPath string, brief, jsonOut bool, color string) error {
	cfg, err := loadConfig(rf)
	if err != nil {
		return err
	}
	resolved, err := resolvePlanDB(dbPath)
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
	deps := headroom.Deps{Config: &cfg, Reader: storeReader{store: store}}
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
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(g.Payload())
	}
	opt := cards.Options{Width: terminalWidth(out), Color: terminalColor(out, color), Brief: brief, Now: now}
	if engErr == nil {
		opt.Costs = glanceCosts(ctx, analytics.New(store, eng), g, now)
	}
	_, err = io.WriteString(out, cards.Render(g, opt))
	return err
}

// glanceCosts is each plan's provider's usage today and over 30 days; a
// provider whose figures cannot be read shows none.
func glanceCosts(ctx context.Context, s spending.Summarizer, g headroom.Glance, now time.Time) map[string]spending.ProviderCost {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = map[string]spending.ProviderCost{}
	)
	seen := map[string]bool{}
	for _, r := range g.Headroom.Reports {
		if seen[r.Provider] || r.Provider == "" {
			continue
		}
		seen[r.Provider] = true
		wg.Add(1)
		go func(provider string) {
			defer wg.Done()
			if c, err := spending.CostOf(ctx, s, provider, now); err == nil {
				mu.Lock()
				out[provider] = c
				mu.Unlock()
			}
		}(r.Provider)
	}
	wg.Wait()
	return out
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
