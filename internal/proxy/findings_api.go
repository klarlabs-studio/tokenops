package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
)

// FindingsDeps is where the findings routes read the findings and keep
// what the operator dismissed.
type FindingsDeps struct {
	// Report computes the findings as they are now, before dismissals.
	Report func(ctx context.Context, now time.Time) (findings.Report, error)
	// Dismissals is the file of findings the operator hid.
	Dismissals findings.Dismissals
}

// WithFindings overrides where the findings come from; without it they
// are computed from the plan routes' deps, around the same glance:
//
//	GET  /api/findings
//	POST /api/findings/dismiss {"key", "for": "day" | "week" | "none"}
//	POST /api/findings/act     {"key", "action"}
func WithFindings(deps func() FindingsDeps) Option {
	return func(s *Server) { s.findings = deps }
}

// FindingDismissRequest hides a finding for a day or a week. With for "none" it shows the finding again, and with no key every hidden finding. A hidden finding shows again early when it becomes more urgent than it was.
type FindingDismissRequest struct {
	Key string `json:"key"`
	For string `json:"for"`
}

// FindingActRequest runs one of the actions a finding offers now (its actions[].id): boundary.promote, tip.ack, tip.expected or tip.snooze.
type FindingActRequest struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

// FindingDismissResult is what a dismissal did, and the findings after it.
type FindingDismissResult struct {
	Key string `json:"key,omitempty"`
	For string `json:"for"`
	// Until is when the finding shows again, absent for "none".
	Until *time.Time `json:"until,omitempty"`
	// Restored counts the dismissals "none" removed.
	Restored int             `json:"restored,omitempty"`
	Findings findings.Report `json:"findings"`
}

// FindingActResult is what an action did, and the findings after it.
type FindingActResult struct {
	Acted    findings.Acted  `json:"acted"`
	Findings findings.Report `json:"findings"`
}

// findingsDeps is the override, or the findings computed from the plan
// routes' deps; false when neither is wired.
func (s *Server) findingsDeps() (FindingsDeps, bool) {
	if s.findings != nil {
		return s.findings(), true
	}
	if s.plans == nil {
		return FindingsDeps{}, false
	}
	return FindingsDeps{Report: s.computeFindings, Dismissals: findings.DefaultDismissals()}, true
}

// computeFindings ranks what the coach and the session analysis observed
// around the glance; the coach's report comes from the state routes' deps
// when they are mounted.
func (s *Server) computeFindings(ctx context.Context, now time.Time) (findings.Report, error) {
	d := s.plans()
	g, err := headroom.ComputeGlance(ctx, d, now)
	if err != nil {
		return findings.Report{}, err
	}
	var c *coachcap.Report
	if s.state != nil {
		if st := s.state(); st.Coach != nil {
			r := st.Coach(now)
			c = &r
		}
	}
	return findings.Compute(findings.Gather(&g, c, d.Config, findings.DefaultDir())), nil
}

// shown is the findings with the dismissed ones left out. A dismissal
// file that cannot be read hides nothing.
func shown(ctx context.Context, d FindingsDeps, now time.Time) (findings.Report, error) {
	r, err := d.Report(ctx, now)
	if err != nil {
		return findings.Report{}, err
	}
	ds, _ := d.Dismissals.Load(now)
	return findings.Filter(r, ds, now), nil
}

func (s *Server) registerFindingRoutes(mux RouteMux) {
	if s.findings == nil && s.plans == nil {
		return
	}
	mux.HandleFunc("GET /api/findings", func(w http.ResponseWriter, r *http.Request) {
		d, _ := s.findingsDeps()
		body, err := shown(r.Context(), d, time.Now().UTC())
		writeAPIResult(w, body, err)
	})
}

func (s *Server) registerFindingActionRoutes(mux RouteMux) {
	// Writes are served only behind the token, like every action.
	if s.actions == nil || s.dashAuth == nil || (s.findings == nil && s.plans == nil) {
		return
	}
	mux.HandleFunc("POST /api/findings/dismiss", s.findingAction("finding_dismiss", s.dismissFinding))
	mux.HandleFunc("POST /api/findings/act", s.findingAction("finding_action", s.actOnFinding))
}

// findingAction runs do and records what it changed in the audit log.
// A finding that is no longer shown, or no longer offers the action, is a
// conflict: the surface is showing an older reading and should refresh.
func (s *Server) findingAction(target string, do func(r *http.Request, now time.Time) (audit map[string]any, body any, err error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		audit, body, err := do(r, time.Now().UTC())
		switch {
		case errors.Is(err, findings.ErrStale):
			writeAPIError(w, http.StatusConflict, err)
			return
		case actions.IsInput(err):
			writeAPIError(w, http.StatusBadRequest, err)
			return
		case err != nil:
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		if d := s.actions(); d.Audit != nil {
			d.Audit(r.Context(), target, audit)
		}
		writeAPIJSON(w, http.StatusOK, body)
	}
}

func (s *Server) dismissFinding(r *http.Request, now time.Time) (map[string]any, any, error) {
	var in FindingDismissRequest
	if err := decodeAction(r, &in); err != nil {
		return nil, nil, err
	}
	if in.Key != "" && !findings.ValidKey(in.Key) {
		return nil, nil, actions.InputError{Err: fmt.Errorf("%q is not a finding's key", in.Key)}
	}
	d, _ := s.findingsDeps()
	out := FindingDismissResult{Key: in.Key, For: in.For}
	if in.For == "none" {
		n, err := d.Dismissals.Restore(in.Key, now)
		if err != nil {
			return nil, nil, err
		}
		out.Restored = n
	} else {
		dur, ok := findings.DismissFor(in.For)
		if !ok {
			return nil, nil, actions.InputError{Err: fmt.Errorf("for must be day, week or none, not %q", in.For)}
		}
		if in.Key == "" {
			return nil, nil, actions.InputError{Err: errors.New("name the finding to hide by its key")}
		}
		r, err := d.Report(r.Context(), now)
		if err != nil {
			return nil, nil, err
		}
		f, ok := r.Find(in.Key)
		if !ok {
			return nil, nil, fmt.Errorf("%w: no finding %q is shown now", findings.ErrStale, in.Key)
		}
		until := now.Add(dur)
		if err := d.Dismissals.Dismiss(in.Key, f.Level, until, now); err != nil {
			return nil, nil, err
		}
		out.Until = &until
	}
	report, err := shown(r.Context(), d, now)
	if err != nil {
		return nil, nil, err
	}
	out.Findings = report
	audit := map[string]any{"key": in.Key, "for": in.For}
	if out.Until != nil {
		audit["until"] = out.Until.Format(time.RFC3339)
	}
	return audit, out, nil
}

func (s *Server) actOnFinding(r *http.Request, now time.Time) (map[string]any, any, error) {
	var in FindingActRequest
	if err := decodeAction(r, &in); err != nil {
		return nil, nil, err
	}
	if !findings.ValidKey(in.Key) {
		return nil, nil, actions.InputError{Err: fmt.Errorf("%q is not a finding's key", in.Key)}
	}
	d, _ := s.findingsDeps()
	current, err := d.Report(r.Context(), now)
	if err != nil {
		return nil, nil, err
	}
	path := s.actions().ConfigPath
	if path == "" {
		if path, err = config.DefaultPath(); err != nil {
			return nil, nil, err
		}
	}
	var ledger coachcap.Ledger
	if env := s.actions().Coach; env != nil {
		ledger, _ = env()
	}
	acted, err := findings.Act(current, in.Key, in.Action, findings.ActEnv{ConfigPath: path, Ledger: ledger, Now: now})
	if err != nil {
		return nil, nil, err
	}
	report, err := shown(r.Context(), d, now)
	if err != nil {
		return nil, nil, err
	}
	audit, err := withDefault(acted, "note", "")
	if err != nil {
		return nil, nil, err
	}
	return audit, FindingActResult{Acted: acted, Findings: report}, nil
}
