package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
)

// newBoundariesPresenceCmd is presence-checked approval (ADR 0015): this
// Mac's presence key, and whether approvals must be signed with it. It is
// CLI-only, as promote is: enrolling the key and turning enforcement on
// are the operator's acts, and enrolling raises the dialog.
func newBoundariesPresenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "presence",
		Short: "Presence-checked approval: enrol this Mac's approval key, and require it",
		Long: `An approval of a boundary or a model upgrade can be signed with a key in this
Mac's Secure Enclave that signs only after Touch ID, a paired Apple Watch or
your login password (ADR 0015). With boundary_approval: presence in
config.yaml, TokenOps honours only approvals signed with it (or given at
Claude Code's own permission prompt), so an agent that runs
'script -q /dev/null tokenops approve <id>' or writes the answer file itself
gets nowhere. presence-strict drops Claude Code's prompt too.

  tokenops boundaries presence enroll    create and enrol the key (one dialog)
  tokenops boundaries presence enforce   require it (boundary_approval: presence)
  tokenops boundaries presence status    what is enrolled and enforced
  tokenops boundaries presence rotate    move to a new key, signed by the old one

Needs the tokenops-presence helper, shipped beside tokenops in the macOS
release, and the logged-in user's screen: over SSH, in a launchd job or in CI
nobody can be asked, and approval is refused rather than waiting.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newPresenceEnrollCmd(), newPresenceRotateCmd(), newPresenceStatusCmd(), newPresenceEnforceCmd())
	return cmd
}

func newPresenceEnrollCmd() *cobra.Command {
	var replace, jsonOut bool
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Create this Mac's presence key and enrol it (raises the dialog once)",
		Long: `enroll creates a P-256 key in this Mac's Secure Enclave that signs only with
user presence, has it sign its enrolment statement (so the dialog appears
once, and proves the key works), and writes it to ~/.tokenops/presence.

A key already enrolled is kept: 'rotate' moves to a new one with the old
key's signature. --replace enrols a new key without it (a new Mac, a lost
key); approvals are then held for 15 minutes and the replacement is
announced on the glance and in 'tokenops boundaries status'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			helper, err := infrapresence.FindHelper()
			if err != nil {
				return err
			}
			env, err := presenceEnv(cmd)
			if err != nil {
				return err
			}
			res, err := bcap.EnrollPresence(cmd.Context(), env, prompting(cmd, helper), bcap.EnrollOptions{Replace: replace})
			if err != nil {
				return err
			}
			return writeEnrolled(cmd, res, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "enrol a new key over the current one without its signature (holds approvals for 15 minutes)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func newPresenceRotateCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Move to a new presence key, signed by the old one (two dialogs, no hold)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			helper, err := infrapresence.FindHelper()
			if err != nil {
				return err
			}
			env, err := presenceEnv(cmd)
			if err != nil {
				return err
			}
			res, err := bcap.RotatePresence(cmd.Context(), env, prompting(cmd, helper), "")
			if err != nil {
				return err
			}
			return writeEnrolled(cmd, res, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeEnrolled(cmd *cobra.Command, res bcap.Enrolled, jsonOut bool) error {
	if jsonOut {
		return writeControlJSON(cmd, res)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", res.Note, res.Dir)
	return nil
}

func newPresenceStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Whether a presence key is enrolled, and whether approvals must be signed with it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := presenceEnv(cmd)
			if err != nil {
				return err
			}
			st := bcap.Presence(env)
			if jsonOut {
				return writeControlJSON(cmd, st)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "boundary_approval: %s\nstate: %s\n%s\n", st.Approval, st.State, st.Note)
			if _, err := infrapresence.FindHelper(); err != nil {
				fmt.Fprintf(out, "helper: %v\n", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func newPresenceEnforceCmd() *cobra.Command {
	var strict, off, jsonOut bool
	cmd := &cobra.Command{
		Use:   "enforce",
		Short: "Require presence-signed approvals (boundary_approval: presence)",
		Long: `enforce sets boundary_approval in config.yaml: presence (an approval counts
only when signed with this Mac's presence key, or given at Claude Code's
prompt), presence-strict with --strict (the prompt does not count), or
channel with --off (approvals trusted by the channel they arrive on; the
check runs in shadow). Turning it on needs an enrolled key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strict && off {
				return errors.New("--strict and --off cannot be combined")
			}
			mode := bcap.ApprovalPresence
			switch {
			case strict:
				mode = bcap.ApprovalStrict
			case off:
				mode = bcap.ApprovalChannel
			}
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			res, err := bcap.SetApproval(path, bcap.Env{}, mode)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "boundary_approval: %s (wrote %s)\n%s\n", res.Approval, res.Config, res.Note)
			return nil
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "presence-strict: Claude Code's prompt no longer approves")
	cmd.Flags().BoolVar(&off, "off", false, "back to channel: approvals trusted by the channel they arrive on")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// presenceEnv is the boundary environment with boundary_approval read from
// the config; no config is channel.
func presenceEnv(cmd *cobra.Command) (bcap.Env, error) {
	mode, err := approvalModeOf(cmd)
	if err != nil {
		return bcap.Env{}, err
	}
	return bcap.Env{Approval: mode}, nil
}

// approvalModeOf reads boundary_approval from the config this command
// uses. A missing config is channel; one that cannot be read is an error,
// since guessing channel would drop a check the operator turned on.
func approvalModeOf(cmd *cobra.Command) (bcap.ApprovalMode, error) {
	path, err := resolveMutableConfigPath(configFlag(cmd))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return bcap.ApprovalChannel, nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	return cfg.ApprovalMode(), nil
}

// presenceProofFor signs an approval of a boundary decision when this Mac
// can: a key is enrolled, the helper is here and someone is at the screen.
// With presence enforced, anything that stops the signature stops the
// approval. Under channel the approval goes on unsigned, with a note,
// unless the person cancelled the dialog, which is a no.
func presenceProofFor(cmd *cobra.Command, env bcap.Env, id string, session bool) (*bcap.Proof, error) {
	st := bcap.Presence(env)
	if st.State == bcap.PresenceStateNotEnrolled || st.State == bcap.PresenceStateUnavailable {
		// Nothing to sign with; under enforcement Decide refuses with the
		// enrol command.
		return nil, nil
	}
	enforced := env.Approval.Enforced()
	helper, err := infrapresence.FindHelper()
	if err != nil {
		return nil, shadowOrFail(cmd.ErrOrStderr(), enforced, err)
	}
	proof, err := bcap.SignApproval(cmd.Context(), env, prompting(cmd, helper), id, session)
	switch {
	case err == nil:
		return proof, nil
	case errors.Is(err, infrapresence.ErrCancelled) || errors.Is(err, infrapresence.ErrAuthFailed):
		return nil, fmt.Errorf("%w: nothing was answered", err)
	}
	return nil, shadowOrFail(cmd.ErrOrStderr(), enforced, err)
}

// shadowOrFail is a signature that could not be made: the approval's end
// under enforcement, a note under channel.
func shadowOrFail(w io.Writer, enforced bool, err error) error {
	if enforced {
		return fmt.Errorf("approval refused: %w (boundary_approval needs a signed approval; denying needs none)", err)
	}
	fmt.Fprintf(w, "not signed (%v); with boundary_approval: channel the approval goes on unsigned\n", err)
	return nil
}

func isApprove(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approve", "approved":
		return true
	}
	return false
}

// routingProofFor signs a model upgrade approval when a presence key is
// enrolled, as presenceProofFor does for a boundary.
func routingProofFor(cmd *cobra.Command, storePath, key string, mode bcap.ApprovalMode) (*bcap.Proof, error) {
	st := bcap.Presence(bcap.Env{Approval: mode})
	if st.State == bcap.PresenceStateNotEnrolled || st.State == bcap.PresenceStateUnavailable {
		return nil, nil
	}
	helper, err := infrapresence.FindHelper()
	if err != nil {
		return nil, shadowOrFail(cmd.ErrOrStderr(), mode.Enforced(), err)
	}
	proof, err := actions.SignRoutingApproval(cmd.Context(), storePath, key, prompting(cmd, helper))
	switch {
	case err == nil:
		return proof, nil
	case errors.Is(err, infrapresence.ErrCancelled) || errors.Is(err, infrapresence.ErrAuthFailed):
		return nil, fmt.Errorf("%w: nothing was answered", err)
	}
	return nil, shadowOrFail(cmd.ErrOrStderr(), mode.Enforced(), err)
}

// promptingSigner says a dialog is coming just before each one, so the
// person is not left looking at a silent terminal, and only when it is.
type promptingSigner struct {
	infrapresence.Helper
	w io.Writer
}

func prompting(cmd *cobra.Command, h infrapresence.Helper) promptingSigner {
	return promptingSigner{Helper: h, w: cmd.ErrOrStderr()}
}

func (p promptingSigner) Sign(ctx context.Context, blobPath string, msg []byte, reason string) ([]byte, error) {
	fmt.Fprintf(p.w, "Confirm with Touch ID, your Apple Watch or your password: %s\n", infrapresence.DialogText(reason))
	return p.Helper.Sign(ctx, blobPath, msg, reason)
}
