package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/statusline"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/fxrate"
)

// claudeStatus is the JSON Claude Code hands a statusLine command on
// stdin (https://code.claude.com/docs/en/statusline). Only what the line
// shows is read.
type claudeStatus struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Effort struct {
		Level string `json:"level"`
	} `json:"effort"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		ContextWindowSize int64    `json:"context_window_size"`
	} `json:"context_window"`
	PromptCache *struct {
		HitRatio *float64 `json:"hit_ratio"`
	} `json:"prompt_cache"`
	RateLimits struct {
		FiveHour   *claudeWindow `json:"five_hour"`
		SevenDay   *claudeWindow `json:"seven_day"`
		SpendLimit *claudeWindow `json:"spend_limit"`
	} `json:"rate_limits"`
}

type claudeWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
	// Only a gateway spend limit carries these, and not always.
	UsedUSD  *float64 `json:"used_usd"`
	LimitUSD *float64 `json:"limit_usd"`
	Period   string   `json:"period"`
}

// limitsReading is the rate_limits part of the update, as a reading.
func (s claudeStatus) limitsReading(now time.Time) claudelimits.Reading {
	conv := func(w *claudeWindow) *claudelimits.Window {
		if w == nil {
			return nil
		}
		return &claudelimits.Window{
			UsedPct: w.UsedPercentage, ResetsAt: w.ResetsAt,
			UsedUSD: w.UsedUSD, LimitUSD: w.LimitUSD, Period: w.Period,
		}
	}
	return claudelimits.Reading{
		ObservedAt: now.UTC(),
		FiveHour:   conv(s.RateLimits.FiveHour),
		SevenDay:   conv(s.RateLimits.SevenDay),
		SpendLimit: conv(s.RateLimits.SpendLimit),
	}
}

// statuslineWrapTimeout bounds the wrapped command. Claude Code cancels a
// statusline that is still running when the next update fires.
const statuslineWrapTimeout = 1500 * time.Millisecond

func newStatuslineCmd() *cobra.Command {
	var wrap string
	cmd := &cobra.Command{
		Use:   "statusline",
		Short: "TokenOps' line in Claude Code's status line",
		Long: `statusline prints the quota windows, the context against the point the
session compacts at, the cache hit ratio, the session's cost in your
currency and the coach's open tip, in Klarlabs colours. Claude Code runs
it on every turn with its session JSON on stdin.

It reads only what Claude Code hands it and files TokenOps already keeps,
never the event store or the network, and it fails open: on any error it
prints what it can, or nothing. It keeps the windows Claude Code reports
in ~/.tokenops/claude-limits.json, for the daemon to store as Anthropic's
own reading.

--wrap runs another status line command with the same input and prints
its output under TokenOps' line, so an existing status line keeps
working. ` + "`tokenops statusline install`" + ` sets this up.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, _ := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			out := cmd.OutOrStdout()
			for _, line := range statusLines(in) {
				fmt.Fprintln(out, line)
			}
			if wrap != "" {
				if wrapped := runWrapped(cmd.Context(), wrap, in); wrapped != "" {
					fmt.Fprintln(out, wrapped)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&wrap, "wrap", "", "another status line command to run with the same input")
	cmd.AddCommand(newStatuslineInstallCmd(), newStatuslineUninstallCmd(), newStatuslineSubagentsCmd())
	return cmd
}

// statusLines renders TokenOps' line from Claude Code's JSON. Anything
// missing is left out rather than guessed.
func statusLines(raw []byte) []string {
	var s claudeStatus
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	// The windows are the vendor's own reading; the daemon stores them so
	// headroom has them without a claude.ai login. Failing to keep them
	// must not cost the operator their status line.
	_ = claudelimits.Write("", s.limitsReading(time.Now()))
	var cfg config.Config
	if path, err := config.DefaultPath(); err == nil {
		if loaded, err := config.Load(path); err == nil {
			cfg = loaded
		}
	}
	in := statusline.Input{
		Model:    s.Model.DisplayName,
		Effort:   s.Effort.Level,
		CostUSD:  s.Cost.TotalCostUSD,
		CacheHit: -1,
		Now:      time.Now(),
		Color:    os.Getenv("NO_COLOR") == "",
	}
	if s.ContextWindow.UsedPercentage != nil {
		in.ContextPct = *s.ContextWindow.UsedPercentage
	}
	if size := s.ContextWindow.ContextWindowSize; size > 0 {
		if at := coachcap.CompactAt(cfg, "claude-code:"); at > 0 && at < size {
			in.CompactPct = float64(at) / float64(size) * 100
		}
	}
	if s.PromptCache != nil && s.PromptCache.HitRatio != nil {
		in.CacheHit = *s.PromptCache.HitRatio
	}
	for _, w := range []struct {
		label string
		win   *claudeWindow
	}{{"5h", s.RateLimits.FiveHour}, {"wk", s.RateLimits.SevenDay}, {"limit", s.RateLimits.SpendLimit}} {
		if w.win == nil {
			continue
		}
		win := statusline.Window{Label: w.label, UsedPct: w.win.UsedPercentage}
		if w.win.ResetsAt > 0 {
			win.ResetsAt = time.Unix(w.win.ResetsAt, 0)
		}
		in.Windows = append(in.Windows, win)
	}
	// A plan covers the session only through Anthropic's own endpoint
	// (ADR 0009); then the cost Claude Code shows is value, not money.
	in.Covered = statusline.Covered(cfg.PlanCovers("anthropic"), claudesettings.BaseURL()) && s.RateLimits.SpendLimit == nil
	if r, ok := fxrate.Cached(cfg.Money); ok {
		in.Rate = r
	}
	in.Tip = openTipText(cfg, s.SessionID)
	return statusline.Render(in)
}

// openTipText words the coach's open tip for this session, when the
// coach is set to speak.
func openTipText(cfg config.Config, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if v, _ := cfg.CoachVerbosity(); v == config.VerbosityQuiet {
		return ""
	}
	if coachcap.Build(cfg).Effective(config.PowerInform) == config.AutonomyOff {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(sessionID)
	b, err := os.ReadFile(filepath.Join(home, ".tokenops", "coach-hook", "session-"+safe+".json")) //nolint:gosec // TokenOps' own state
	if err != nil {
		return ""
	}
	var st struct {
		OpenTip *struct {
			Kind string `json:"kind"`
		} `json:"open_tip"`
	}
	if json.Unmarshal(b, &st) != nil || st.OpenTip == nil {
		return ""
	}
	return tipText(st.OpenTip.Kind)
}

// tipText is a tip kind in a few words.
func tipText(kind string) string {
	switch {
	case kind == "compact_now":
		return "compact before the next task (/compact)"
	case kind == "budget_over":
		return "this session is past its budget"
	case strings.HasPrefix(kind, "budget_"):
		return "this session is past " + strings.TrimPrefix(kind, "budget_") + "% of its budget"
	case strings.HasPrefix(kind, "quota_"):
		return "your plan window is " + strings.TrimPrefix(kind, "quota_") + "% used"
	case kind == "":
		return ""
	default:
		return strings.ReplaceAll(kind, "_", " ")
	}
}

// runWrapped runs another status line command with the same input and
// returns its output, or "" when it fails or takes too long.
func runWrapped(ctx context.Context, command string, in []byte) string {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, statuslineWrapTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec // the operator's own status line command
	c.Stdin = bytes.NewReader(in)
	out, err := c.Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// newStatuslineSubagentsCmd renders Claude Code's subagent rows
// (subagentStatusLine): one JSON line {"id","content"} per task.
func newStatuslineSubagentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subagents",
		Short: "Print Claude Code's subagent rows: model, effort and context per subagent (reads its JSON on stdin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, _ := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			var payload struct {
				Tasks []struct {
					ID                string `json:"id"`
					Name              string `json:"name"`
					Model             string `json:"model"`
					Effort            any    `json:"effort"`
					ContextWindowSize int64  `json:"contextWindowSize"`
					TokenCount        int64  `json:"tokenCount"`
				} `json:"tasks"`
			}
			if json.Unmarshal(in, &payload) != nil {
				return nil // fail open: Claude Code keeps its default rows
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, t := range payload.Tasks {
				if t.ID == "" {
					continue
				}
				row := statusline.Subagent{Name: t.Name, Model: t.Model, ContextPct: -1, Color: os.Getenv("NO_COLOR") == ""}
				if t.Effort != nil {
					row.Effort = fmt.Sprint(t.Effort)
				}
				if t.ContextWindowSize > 0 {
					row.ContextPct = float64(t.TokenCount) / float64(t.ContextWindowSize) * 100
				}
				_ = enc.Encode(map[string]string{"id": t.ID, "content": statusline.SubagentRow(row)})
			}
			return nil
		},
	}
}
