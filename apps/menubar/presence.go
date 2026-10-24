package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Inline approval (ADR 0015 §3). With a presence key enrolled, Approve
// raises the system dialog (Touch ID, a paired Apple Watch or the login
// password) and posts the signed answer through the daemon, with no
// Terminal window:
//
//  1. the daemon builds the statement for the waiting decision
//     (POST /api/boundaries/statements): the exact bytes the key signs and
//     the dialog's text, which names the rule, the action and the session.
//     The menu bar never assembles a statement;
//  2. the presence helper bundled beside this binary signs those bytes,
//     raising the dialog;
//  3. the menu bar posts the daemon's proof back, with only the signature
//     added (POST /api/boundaries/decisions), and the daemon verifies it
//     against the enrolled key.
//
// With no key enrolled, no helper in the bundle, or a daemon from before
// the statement route, Approve opens the terminal as before (approve.go).
// Deny never signs.

// helperName is the presence helper's binary inside TokenOps.app.
const helperName = "tokenops-presence"

// signTimeout bounds one signature: the dialog waits on a person, who
// takes seconds, not minutes.
const signTimeout = 2 * time.Minute

// helperPath is the presence helper beside the running binary, with
// symlinks resolved: TokenOps.app/Contents/MacOS/tokenops-presence. Only
// the bundle's own copy is used. A variable so tests use a fake.
var helperPath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errHelperMissing
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	p := filepath.Join(filepath.Dir(exe), helperName)
	if st, err := os.Stat(p); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", errHelperMissing
	}
	return p, nil
}

// errFallback is a reason to approve in the terminal instead: inline
// approval is not available here, which is not the person refusing.
type errFallback struct{ why string }

func (e errFallback) Error() string { return e.why }

var errHelperMissing = errFallback{"the presence helper is not in this TokenOps.app"}

// refusal is why an inline approval answered nothing, worded for the
// panel's note: the person cancelled, could not be asked, or the daemon
// refused the signature.
type refusal string

func (r refusal) Error() string { return string(r) }

// The helper's exit codes (apps/presence), each a reason no signature
// came back.
const (
	exitUsage      = 2
	exitNoEnclave  = 3
	exitNoGUI      = 4
	exitLocked     = 5
	exitCancelled  = 6
	exitAuthFailed = 7
	exitBadKey     = 8
)

// helperExit says what an exit code means for the person who clicked
// Approve. Nothing was answered in any of these cases.
func helperExit(code int) string {
	switch code {
	case exitCancelled:
		return "Not approved: the dialog was cancelled."
	case exitAuthFailed:
		return "Not approved: Touch ID or the password didn't match."
	case exitLocked:
		return "Not approved: the screen is locked, so the dialog couldn't be shown. Unlock your Mac and try again."
	case exitNoGUI:
		return "Not approved: the dialog needs your screen, and none could be asked. Try again from your logged-in session."
	case exitBadKey:
		return "Not approved: this Mac's presence key can't be used (a key from another Mac, or a damaged one). " +
			"Re-enrol with `tokenops boundaries presence enroll --replace`."
	case exitNoEnclave:
		return "Not approved: this Mac has no Secure Enclave, so it can't sign approvals. Use `tokenops approve` in a terminal."
	case exitUsage:
		return "Not approved: the presence helper in this TokenOps.app is too old for this request. Update TokenOps."
	}
	return fmt.Sprintf("Not approved: the presence helper failed (exit %d).", code)
}

// statement is the daemon's statement for a waiting decision. Proof stays
// raw: it goes back as the daemon sent it, with sig added.
type statement struct {
	Proof     map[string]json.RawMessage `json:"proof"`
	Statement []byte                     `json:"statement"`
	Reason    string                     `json:"reason"`
	Blob      string                     `json:"blob"`
}

// boundaryStatement asks the daemon for the statement approving id. A
// machine where no key can sign (409 presence_unavailable) or a daemon
// without the route is a fallback to the terminal.
func (d *daemon) boundaryStatement(ctx context.Context, id string, session bool) (statement, error) {
	var st statement
	err := d.do(ctx, http.MethodPost, "/api/boundaries/statements", map[string]any{"id": id, "session": session}, &st)
	var se *statusError
	switch {
	case err == nil:
		if len(st.Statement) == 0 || len(st.Proof) == 0 || st.Blob == "" {
			return statement{}, errors.New("the daemon returned no statement to sign")
		}
		return st, nil
	case errors.As(err, &se) && se.code == http.StatusConflict:
		return statement{}, errFallback{se.msg}
	case errors.As(err, &se) && (se.code == http.StatusNotFound || se.code == http.StatusMethodNotAllowed):
		return statement{}, errFallback{"this daemon cannot prepare a signed approval yet"}
	}
	return statement{}, err
}

// approved is the daemon's answer to a signed approval.
type approved struct {
	Rule        string `json:"rule"`
	Presence    string `json:"presence"`
	PresenceKey string `json:"presence_key"`
	Note        string `json:"note"`
}

// approveBoundary posts a signed approval of id.
func (d *daemon) approveBoundary(ctx context.Context, id string, session bool, proof map[string]json.RawMessage) (approved, error) {
	var out approved
	err := d.do(ctx, http.MethodPost, "/api/boundaries/decisions",
		map[string]any{"id": id, "decision": "approve", "session": session, "presence_proof": proof}, &out)
	return out, err
}

// sign runs the helper over the statement's bytes, raising the dialog with
// the daemon's reason, and returns the base64 signature. Not tied to the
// caller's context beyond its values: the panel may close while the
// dialog is up, and killing the helper then would cancel an approval the
// person is giving.
func sign(ctx context.Context, helper string, st statement) (string, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, "sign", "--blob", st.Blob, "--reason", st.Reason) //nolint:gosec // the bundle's own helper; the arguments come from the daemon and are passed without a shell
	cmd.Stdin = bytes.NewReader(st.Statement)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
		return "", errHelperMissing
	case ctx.Err() != nil:
		return "", refusal(fmt.Sprintf("Not approved: the dialog wasn't answered within %s.", signTimeout))
	case errors.As(err, &exit):
		return "", refusal(helperExit(exit.ExitCode()))
	default:
		return "", refusal("Not approved: the presence helper could not run: " + err.Error())
	}
	sig := strings.TrimSpace(stdout.String())
	if b, err := base64.StdEncoding.DecodeString(sig); err != nil || len(b) == 0 {
		return "", refusal("Not approved: the presence helper returned no signature.")
	}
	return sig, nil
}

// approveInline approves id with the presence key: statement, dialog,
// signed answer. An errFallback means inline approval is not available
// here and the terminal should ask instead; any other error is a refusal
// or failure to show the person, and nothing was answered.
func (d *daemon) approveInline(ctx context.Context, id string, session bool) (approved, error) {
	if !validDecisionID(id) {
		return approved{}, errBadDecision
	}
	helper, err := helperPath()
	if err != nil {
		return approved{}, err
	}
	st, err := d.boundaryStatement(ctx, id, session)
	if err != nil {
		return approved{}, err
	}
	sig, err := sign(ctx, helper, st)
	if err != nil {
		return approved{}, err
	}
	proof := make(map[string]json.RawMessage, len(st.Proof)+1)
	for k, v := range st.Proof {
		proof[k] = v
	}
	proof["sig"], _ = json.Marshal(sig)
	// The daemon's answer comes after the dialog; the panel may be gone,
	// and the approval is still the person's.
	out, err := d.approveBoundary(context.WithoutCancel(ctx), id, session, proof)
	if err != nil {
		return approved{}, refusal("Not approved: the daemon refused the signed approval: " + err.Error())
	}
	return out, nil
}

// approvalOutcome says what Approve did, for the panel's note, and whether
// it is a failure to show as one.
func approvalOutcome(id string, a approved, err error) (string, bool) {
	var fb errFallback
	switch {
	case err == nil:
		msg := "Approved " + a.Rule + " (" + id + ")"
		if a.PresenceKey != "" {
			msg += ", signed with presence key " + a.PresenceKey
		}
		if a.Note != "" {
			msg += ": " + a.Note
		}
		return msg + ".", false
	case errors.As(err, &fb):
		return "Approve " + id + " in the terminal that opened (" + fb.why + ").", false
	}
	return err.Error(), true
}
