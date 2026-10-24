package presence

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HelperName is the presence helper's binary: a small Swift program that
// holds the Secure Enclave key (ADR 0015 §1). It ships beside tokenops and
// tokenopsd in the darwin archives and inside TokenOps.app; the CLI and
// the daemon stay pure Go and only exec it.
const HelperName = "tokenops-presence"

// HelperEnv names a helper to use instead of the one found beside the
// binary. Tests point it at a fake that signs with a software key; a fake
// gains nothing against an enrolled key, because every signature is
// checked against the pinned public key.
const HelperEnv = "TOKENOPS_PRESENCE_HELPER"

// signTimeout bounds one signature: the dialog waits on a person, who
// takes seconds, not minutes.
const signTimeout = 2 * time.Minute

// The helper's exit codes, each a reason no signature came back.
const (
	exitUsage      = 2
	exitNoEnclave  = 3
	exitNoGUI      = 4
	exitLocked     = 5
	exitCancelled  = 6
	exitAuthFailed = 7
	exitBadKey     = 8
)

// Errors the helper reports.
var (
	// ErrHelperMissing: no tokenops-presence could be found.
	ErrHelperMissing = errors.New("the presence helper (" + HelperName + ") is not installed beside tokenops")
	// ErrNoEnclave: this Mac has no Secure Enclave.
	ErrNoEnclave = errors.New("this Mac has no Secure Enclave")
	// ErrNoGUI: nobody can be asked here (SSH, a launchd job, CI).
	ErrNoGUI = errors.New("no one can be asked here: the dialog needs the logged-in user's screen (not SSH, a launchd job or CI)")
	// ErrLocked: the screen is locked, so the dialog cannot be shown.
	ErrLocked = errors.New("the screen is locked, so the dialog cannot be shown")
	// ErrCancelled: the person cancelled the dialog.
	ErrCancelled = errors.New("the dialog was cancelled")
	// ErrAuthFailed: Touch ID or the password did not match.
	ErrAuthFailed = errors.New("authentication failed")
	// ErrBadKey: the key blob is not a key this Secure Enclave holds.
	ErrBadKey = errors.New("the presence key could not be loaded (a key from another Mac, or a damaged blob)")
)

// Unavailable reports whether err means the dialog could not be shown at
// all, as opposed to a person refusing it.
func Unavailable(err error) bool {
	return errors.Is(err, ErrHelperMissing) || errors.Is(err, ErrNoEnclave) ||
		errors.Is(err, ErrNoGUI) || errors.Is(err, ErrLocked)
}

// Helper runs tokenops-presence.
type Helper struct {
	Path string
}

// FindHelper locates the helper: HelperEnv, then beside the running binary
// (with symlinks resolved, so a Homebrew bin/ link finds the cask's
// staged copy), then inside TokenOps.app, then on PATH.
func FindHelper() (Helper, error) {
	if p := strings.TrimSpace(os.Getenv(HelperEnv)); p != "" {
		return Helper{Path: p}, nil
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), HelperName))
	}
	app := filepath.Join("TokenOps.app", "Contents", "MacOS", HelperName)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Applications", app))
	}
	candidates = append(candidates, filepath.Join("/Applications", app))
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return Helper{Path: c}, nil
		}
	}
	if p, err := exec.LookPath(HelperName); err == nil {
		return Helper{Path: p}, nil
	}
	return Helper{}, ErrHelperMissing
}

// Create makes a new presence key, writes its wrapped blob to blobPath and
// returns its public key (raw X9.63). Creating a key raises no dialog.
func (h Helper) Create(ctx context.Context, blobPath string) ([]byte, error) {
	out, err := h.run(ctx, nil, "enroll", "--blob", blobPath)
	if err != nil {
		return nil, err
	}
	return decodeLine(out, "public key")
}

// Sign asks for a signature over msg with the key in blobPath. It raises
// the system dialog, whose text is reason: the dialog (and a paired Apple
// Watch) shows it, so it names what is being approved.
func (h Helper) Sign(ctx context.Context, blobPath string, msg []byte, reason string) ([]byte, error) {
	out, err := h.run(ctx, msg, "sign", "--blob", blobPath, "--reason", DialogText(reason))
	if err != nil {
		return nil, err
	}
	return decodeLine(out, "signature")
}

func (h Helper) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	if h.Path == "" {
		return nil, ErrHelperMissing
	}
	ctx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Path, args...) //nolint:gosec // TokenOps' own helper, args are ours
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, ErrHelperMissing
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("the presence helper did not answer within %s", signTimeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if known := exitError(exit.ExitCode()); known != nil {
			return nil, known
		}
		return nil, fmt.Errorf("the presence helper failed (exit %d): %s", exit.ExitCode(), firstLine(stderr.String()))
	}
	return nil, fmt.Errorf("the presence helper: %w", err)
}

func exitError(code int) error {
	switch code {
	case exitNoEnclave:
		return ErrNoEnclave
	case exitNoGUI:
		return ErrNoGUI
	case exitLocked:
		return ErrLocked
	case exitCancelled:
		return ErrCancelled
	case exitAuthFailed:
		return ErrAuthFailed
	case exitBadKey:
		return ErrBadKey
	case exitUsage:
		return errors.New("the presence helper does not understand this request (an older helper?)")
	}
	return nil
}

func decodeLine(out []byte, what string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("the presence helper returned no %s", what)
	}
	return b, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// maxDialogText bounds the dialog's text: the Touch ID sheet and the
// Watch show a sentence, not a page.
const maxDialogText = 240

// DialogText makes a reason safe to show: control characters become
// spaces and it is cut to a sentence's length.
func DialogText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxDialogText {
		s = string(r[:maxDialogText-1]) + "…"
	}
	return s
}
