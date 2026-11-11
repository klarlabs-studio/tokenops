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
// routes' deps. The routes are mounted only when one of them is wired.
func (s *Server) findingsDeps() FindingsDeps {
	if s.findings != nil {
		return s.findings()
	}
	return FindingsDeps{Report: s.computeFindings, Dismissals: findings.DefaultDismissals()}
}

// computeFindings ranks what the coach and the session analysis observed
// around the glance, from the last computation of both (view). A finding
// acted on since that computation is left out: the next one no longer
// draws it.
func (s *Server) computeFindings(ctx context.Context, now time.Time) (findings.Report, error) {
	v, at, err := s.view(ctx, now)
	if err != nil {
		return findings.Report{}, err
	}
	return s.withoutActed(v.Findings, at), nil
}

// withoutActed is r without the findings acted on after at, when r was
// computed. Actions older than at are forgotten: r already reflects them.
func (s *Server) withoutActed(r findings.Report, at time.Time) findings.Report {
	s.actedMu.Lock()
	defer s.actedMu.Unlock()
	if len(s.acted) == 0 {
		return r
	}
	out := r
	out.Findings = make([]findings.Finding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if when, ok := s.acted[f.Key]; ok && !when.Before(at) {
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	for k, when := range s.acted {
		if when.Before(at) {
			delete(s.acted, k)
		}
	}
	return out
}

// noteActed records an action on the finding key at now, and has the next
// request recompute the glance and findings.
func (s *Server) noteActed(key string, now time.Time) {
	s.actedMu.Lock()
	if s.acted == nil {
		s.acted = map[string]time.Time{}
	}
	s.acted[key] = now
	s.actedMu.Unlock()
	if s.glances != nil {
		s.glances.stale()
	}
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
		d := s.findingsDeps()
		now := time.Now().UTC()
		switch r.URL.Query().Get("dismissed") {
		case "":
			body, err := shown(r.Context(), d, now)
			writeAPIResult(w, body, err)
		case "include":
			// The CLI lists every finding: a dismissal tidies the menu
			// bar's panel, it does not make a finding untrue.
			body, err := d.Report(r.Context(), now)
			writeAPIResult(w, body, err)
		default:
			writeAPIError(w, http.StatusBadRequest, errors.New("dismissed must be include or omitted"))
		}
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
	d := s.findingsDeps()
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
	d := s.findingsDeps()
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
	s.noteActed(in.Key, now)
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
