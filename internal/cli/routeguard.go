package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// userPromptSubmitInput is the subset of the UserPromptSubmit payload
// the guard needs.
type userPromptSubmitInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
	// Model is the active model slug. Codex and Cursor send it on every
	// hook event; Claude Code does not, and the transcript is the only
	// place its current model is written down.
	Model string `json:"model"`
	// ProviderID and ModelID are what the opencode shim forwards from
	// its chat.message input, where the provider is named explicitly
	// rather than implied by the client.
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
}

// promptHookOutput injects context into the turn the operator just
// opened. additionalContext is advice, not instruction: the model stays
// the caller's choice.
type promptHookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func newRouteGuardCmd() *cobra.Command {
	var mode, dir, provider string
	cmd := &cobra.Command{
		Use:   "route-guard",
		Short: "UserPromptSubmit hook that checks the model still fits the work",
		Long: `route-guard asks, once per turn, whether the model this session is running
on still fits what it has been asked to do.

A model is chosen at the start of a session and stays chosen. Every turn
after that runs on it, including the retrieval and reading-around a cheaper,
faster model would do just as well — nothing asks the question again, so the
choice made for the first turn governs the hundredth.

It only ever suggests routing DOWN. Suggesting a pricier model is a spending
decision nobody delegated, and on a subscription it can walk you into a cap
you did not choose. It argues a case once per kind of work per session, not
on every prompt.

Modes: advise states the case; delegate additionally marks work that may be
handed to a subagent on the cheaper model; auto allows that for every kind it
is confident about; off disables it.

Bare invocation is the hook handler (reads UserPromptSubmit JSON on stdin).
Use 'tokenops route-guard hook' to print the settings.json block.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var m routeguard.Mode
			if strings.TrimSpace(mode) != "" {
				m = routeguard.ParseMode(mode)
			}
			return runRouteGuardHook(cmd, m, dir, provider)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "advise | delegate | auto | off")
	cmd.Flags().StringVar(&dir, "dir", "", "state dir (defaults to ~/.tokenops/route-guard)")
	cmd.Flags().StringVar(&provider, "provider", "", "provider whose models this client runs (anthropic|openai|cursor|...)")
	cmd.AddCommand(newRouteGuardHookCmd())
	return cmd
}

// runRouteGuardHook reads the UserPromptSubmit JSON and, when the model
// is over-spec for the work, injects the case for a cheaper one.
//
// Every failure path is silent: a guard that cannot decide must let the
// turn proceed untouched rather than interrupt it with its own troubles.
func runRouteGuardHook(cmd *cobra.Command, mode routeguard.Mode, dir, provider string) error {
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil
	}
	var in userPromptSubmitInput
	if json.Unmarshal(body, &in) != nil || strings.TrimSpace(in.Prompt) == "" {
		return nil
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		dir = filepath.Join(home, ".tokenops", "route-guard")
	}
	// Prefer what the client stated. Codex and Cursor put the active
	// model on every hook payload; the opencode shim forwards it from
	// chat.message. Only Claude Code makes this a search problem.
	model := firstNonEmptyStr(in.ModelID, in.Model)
	if model == "" {
		model = latestTranscriptModel(in.TranscriptPath)
	}
	if model == "" {
		return nil
	}
	prov := eventschema.Provider(firstNonEmptyStr(in.ProviderID, provider, string(eventschema.ProviderAnthropic)))
	// Load("") returns defaults without reading anything, so the path
	// has to be resolved first — otherwise the guard silently runs with
	// no model set and abstains on every turn while appearing wired.
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil
	}
	sr := cfg.Optimizer.SmartRouting
	candidates := sr.Models[string(prov)]
	var autoKinds []taskclass.Kind
	for _, k := range sr.AutoKinds {
		autoKinds = append(autoKinds, taskclass.Kind(k))
	}
	if mode == "" {
		mode = routeguard.ParseMode(sr.Intervention)
	}
	dec := routeguard.Evaluate(routeguard.Input{
		Dir: dir, SessionID: in.SessionID, Prompt: in.Prompt,
		CurrentModel: model, Provider: prov,
		Mode: mode, Catalog: routeCatalog(), Candidates: candidates,
		AutoKinds: autoKinds,
	})
	if !dec.Advise {
		return nil
	}
	out := promptHookOutput{}
	out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	out.HookSpecificOutput.AdditionalContext = fmt.Sprintf(
		"tokenops: %s. Consider %s for this task (switch with /model, or delegate it to a subagent on that model). Staying on %s is fine if you prefer.",
		dec.Reason, dec.To, dec.From)
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// routeCatalog builds the tier catalog from the effective-dated card the
// daemon maintains, falling back to the embedded baseline.
//
// The baseline alone is the wrong table to decide from: it does not know
// the prices a refresh has since learned, and pricing from it is what
// made an earlier coach report nothing was worth saying.
func routeCatalog() *modeltier.Catalog {
	table := spend.DefaultTable()
	if home, err := os.UserHomeDir(); err == nil {
		snaps := pricing.LoadSnapshots(filepath.Join(home, ".tokenops", "pricing"))
		if dated := pricing.SnapshotsToDatedTables(snaps); len(dated) > 0 {
			table = dated[len(dated)-1].Table
		}
	}
	return modeltier.New(table, nil)
}

// latestTranscriptModel reads the model off the most recent assistant
// entry. The payload does not carry it, and the transcript is the only
// place the session's current model is written down.
func latestTranscriptModel(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec // operator's own transcript
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	model := ""
	for sc.Scan() {
		var tl struct {
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &tl) != nil {
			continue
		}
		if tl.Message.Model != "" {
			model = tl.Message.Model
		}
	}
	return model
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func newRouteGuardHookCmd() *cobra.Command {
	var mode string
	return &cobra.Command{
		Use:   "hook",
		Short: "Print the settings.json block to wire route-guard into Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe := "tokenops"
			if p, err := os.Executable(); err == nil {
				exe = p
			}
			block := map[string]any{
				"hooks": map[string]any{
					"UserPromptSubmit": []any{
						map[string]any{
							"hooks": []any{
								map[string]any{
									"type":    "command",
									"command": exe,
									"args":    []string{"route-guard", "--mode", string(routeguard.ParseMode(mode))},
									"timeout": 10,
								},
							},
						},
					},
				},
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(block)
		},
	}
}
