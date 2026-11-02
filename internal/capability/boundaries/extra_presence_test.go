package boundaries

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/internal/infra/presence/presencetest"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// isolateHome points every home-relative path at a temp dir, so nothing
// here can reach the operator's real config or ledgers.
func isolateHome(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, t.TempDir())
	}
}

// Under presence a grant is signed through the helper, or refused with
// the reason it cannot be; nothing refused is recorded.
func TestGrantExtraUnderPresence(t *testing.T) {
	tests := []struct {
		name        string
		mode        presence.Mode
		enrol       bool
		noSigner    bool
		fail        int
		wantErr     string
		unavailable bool
		cancelled   bool
	}{
		{name: "no key enrolled", mode: presence.ModePresence, wantErr: "no presence key is enrolled", unavailable: true},
		{name: "no helper", mode: presence.ModePresence, enrol: true, noSigner: true, wantErr: "presence helper is not installed", unavailable: true},
		{name: "nobody at the screen", mode: presence.ModePresence, enrol: true, fail: presencetest.ExitNoGUI, unavailable: true, wantErr: "must be signed"},
		{name: "screen locked", mode: presence.ModePresence, enrol: true, fail: presencetest.ExitLocked, unavailable: true, wantErr: "must be signed"},
		{name: "dialog cancelled", mode: presence.ModePresence, enrol: true, fail: presencetest.ExitCancelled, cancelled: true, wantErr: "nothing was granted"},
		{name: "signed", mode: presence.ModePresence, enrol: true},
		{name: "signed, strict", mode: presence.ModeStrict, enrol: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			env := enforcedEnv(t, tc.mode, false)
			s, f := fakeSigner(t)
			var enrolled Enrolled
			if tc.enrol {
				enrolled = enrolFake(t, env, s)
			}
			if tc.fail != 0 {
				f.Fail(tc.fail)
			}
			req := ExtraRequest{For: 30 * time.Minute, Method: MethodCLI, Why: "release day", Signer: s}
			if tc.noSigner {
				req.Signer = nil
			}
			got, err := GrantExtra(context.Background(), rules(t, enforcedPause()), env, req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if errors.Is(err, ErrPresenceUnavailable) != tc.unavailable {
					t.Errorf("unavailable = %v for %v", !tc.unavailable, err)
				}
				if errors.Is(err, infrapresence.ErrCancelled) != tc.cancelled {
					t.Errorf("cancelled = %v for %v", !tc.cancelled, err)
				}
				if all, _ := env.Dir.Extras(); len(all) != 0 {
					t.Fatalf("a refused grant was recorded: %+v", all)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Presence != PresenceSigned || got.PresenceKey != enrolled.KeyID || !strings.Contains(got.Note, "presence key "+enrolled.KeyID) {
				t.Fatalf("granted %+v", got)
			}
			// The dialog names what it lifts, until when and why.
			calls := f.Calls()
			dialog := calls[len(calls)-1]
			for _, want := range []string{"extra allowance", config.HeadroomPauseName, "release day"} {
				if !strings.Contains(dialog, want) {
					t.Errorf("dialog %q lacks %q", dialog, want)
				}
			}
			// The grant decision records the key.
			envs, err := env.Dir.Decision(got.ID)
			if err != nil || len(envs) != 1 {
				t.Fatalf("decisions %v %v", envs, err)
			}
			d := envs[0].Payload.(*eventschema.DecisionEvent)
			if d.Approval.Presence != PresenceSigned || d.Approval.PresenceKey != enrolled.KeyID || !strings.Contains(d.Rationale, "signed in person") {
				t.Errorf("decision %+v", d)
			}
			if v := ActiveExtras(env); len(v) != 1 || v[0].Presence != PresenceSigned || !v[0].Lifts() {
				t.Errorf("active %+v", v)
			}
		})
	}
}

// writeExtras replaces extras.json, as a process editing it by hand would.
func writeExtras(t *testing.T, env Env, extras []boundaryledger.Extra) {
	t.Helper()
	b, err := json.Marshal(extras)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.Dir.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Dir.Root(), "extras.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Under presence the pause point honours an extra only while its
// signature verifies against the grant as stored: an unsigned, hand-written
// or edited extras.json lifts nothing, and status says why.
func TestPresenceExtraLiftsOnlyWhenSigned(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(e *boundaryledger.Extra)
		lifts  bool
		reason presence.Reason
	}{
		{name: "signed, as granted", lifts: true},
		{name: "until stretched", tamper: func(e *boundaryledger.Extra) { e.Until = e.Until.Add(2 * time.Hour) }, reason: presence.ReasonGrantAltered},
		{name: "until stretched in the statement too", tamper: func(e *boundaryledger.Extra) {
			e.Until = e.Until.Add(2 * time.Hour)
			e.Grant.Until = e.Until
		}, reason: presence.ReasonSignature},
		{name: "rule added", tamper: func(e *boundaryledger.Extra) { e.Rules = append(e.Rules, "session-spend") }, reason: presence.ReasonRule},
		{name: "rule added in the statement too", tamper: func(e *boundaryledger.Extra) {
			e.Rules = append(e.Rules, "session-spend")
			e.Grant.Rules = e.Rules
		}, reason: presence.ReasonSignature},
		{name: "signature stripped", tamper: func(e *boundaryledger.Extra) { e.Grant = nil }, reason: presence.ReasonUnsigned},
		{name: "a hand-written grant", tamper: func(e *boundaryledger.Extra) {
			*e = boundaryledger.Extra{ID: "bnd-0123456789ab", Rules: e.Rules, At: testNow.Add(-time.Minute),
				Until: testNow.Add(time.Hour), Granter: "felix", Method: MethodCLI}
		}, reason: presence.ReasonUnsigned},
		{name: "signature moved to another grant", tamper: func(e *boundaryledger.Extra) { e.ID = "bnd-ffffffffffff" }, reason: presence.ReasonDecision},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			env := enforcedEnv(t, presence.ModePresence, false)
			s, _ := fakeSigner(t)
			enrolFake(t, env, s)
			r := rules(t, enforcedPause(), spendRule("deny", "enforce"))
			writeLimits(t, env, 95)
			if _, err := GrantExtra(context.Background(), r, env, ExtraRequest{For: 20 * time.Minute, Method: MethodCLI, Signer: s}); err != nil {
				t.Fatal(err)
			}
			if tc.tamper != nil {
				all, err := env.Dir.Extras()
				if err != nil || len(all) != 1 {
					t.Fatalf("extras %v %v", all, err)
				}
				tc.tamper(&all[0])
				writeExtras(t, env, all)
			}
			res, err := Pause(r, call("s1"), env)
			if err != nil {
				t.Fatal(err)
			}
			if lifted := res.Verdict == VerdictNone; lifted != tc.lifts {
				t.Fatalf("lifted = %v (%+v), want %v", lifted, res, tc.lifts)
			}
			views := ActiveExtras(env)
			if len(views) != 1 {
				t.Fatalf("active %+v", views)
			}
			v := views[0]
			if tc.lifts {
				if v.Presence != PresenceSigned || !v.Lifts() {
					t.Fatalf("view %+v", v)
				}
				_, a, rationale := lastApproval(t, env, domain.DecisionID(config.HeadroomPauseName, "s1"))
				if a.Method != MethodExtra || a.Presence != PresenceSigned || !strings.Contains(rationale, "signed with presence key") {
					t.Fatalf("lifted decision %+v %q", a, rationale)
				}
				// It still ends on its own.
				later := env
				later.Now = func() time.Time { return testNow.Add(21 * time.Minute) }
				if res, _ := Pause(r, call("s2"), later); res.Verdict != VerdictDeny {
					t.Fatalf("after the extra ended: %+v", res)
				}
				return
			}
			if v.Presence != PresenceRefused || v.Lifts() || v.Ignored != tc.reason.Phrase() {
				t.Fatalf("view %+v, want refused for %q", v, tc.reason.Phrase())
			}
		})
	}
}

// Outside presence a grant is neither signed nor checked: no dialog, and
// it lifts as before, even with a key enrolled.
func TestChannelExtraIsUnsignedAndLifts(t *testing.T) {
	isolateHome(t)
	env := enforcedEnv(t, presence.ModeChannel, false)
	s, f := fakeSigner(t)
	enrolFake(t, env, s)
	before := len(f.Calls())
	r := rules(t, enforcedPause())
	writeLimits(t, env, 95)
	got, err := GrantExtra(context.Background(), r, env, ExtraRequest{For: 20 * time.Minute, Method: MethodCLI, Signer: s})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Calls()) != before || got.Presence != "" {
		t.Fatalf("a channel grant raised a dialog or was signed: %+v %v", got, f.Calls())
	}
	if res, _ := Pause(r, call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("a channel grant did not lift: %+v", res)
	}
	if v := ActiveExtras(env); len(v) != 1 || v[0].Presence != "" || !v[0].Lifts() {
		t.Fatalf("view %+v", v)
	}
}

// Turning presence on after an unsigned grant stops it lifting; ending
// it needs no presence.
func TestEnforcingPresenceStopsAnUnsignedGrantAndEndStillWorks(t *testing.T) {
	isolateHome(t)
	env := enforcedEnv(t, presence.ModeChannel, false)
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	r := rules(t, enforcedPause())
	writeLimits(t, env, 95)
	if _, err := GrantExtra(context.Background(), r, env, ExtraRequest{For: time.Hour, Method: MethodCLI}); err != nil {
		t.Fatal(err)
	}
	env.Approval = presence.ModePresence
	if res, _ := Pause(r, call("s1"), env); res.Verdict == VerdictNone {
		t.Fatalf("an unsigned grant lifted under presence: %+v", res)
	}
	ended, err := EndExtras(env)
	if err != nil || len(ended) != 1 {
		t.Fatalf("end under presence: %+v %v", ended, err)
	}
}
