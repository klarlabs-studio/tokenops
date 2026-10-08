package teamserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// smallBody bounds every request body but an upload's.
const smallBody = 16 << 10

// cleanName trims s and checks it is 1..max characters of printable text.
func cleanName(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > max || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var req teamwire.EnrollRequest
	if !decode(w, r, smallBody, &req) {
		return
	}
	name, ok := cleanName(req.DisplayName, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "display_name: 1 to 100 printable characters", "")
		return
	}
	device, ok := cleanName(req.DeviceName, 64)
	if !ok {
		writeError(w, http.StatusBadRequest, "device_name: 1 to 64 printable characters", "")
		return
	}
	if k, err := team.KindOf(req.Invite); err != nil || k != team.TokenInvite {
		writeError(w, http.StatusBadRequest, "invite: not an invite code", "ask whoever invited you for a new one")
		return
	}
	resp, err := s.store.Enroll(r.Context(), req.Invite, name, device)
	if errors.Is(err, pgstore.ErrInviteInvalid) {
		writeError(w, http.StatusForbidden, err.Error(), "invites are single-use and expire; ask for a new one")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.log.Info("device enrolled", "org", resp.OrgID, "member", resp.MemberID, "device", resp.DeviceID)
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	if k, err := team.KindOf(tok); err != nil || k != team.TokenDevice {
		s.authFailed(w, errNoCredential)
		return
	}
	dev, err := s.store.AuthDevice(r.Context(), tok)
	if err != nil {
		s.authFailed(w, err)
		return
	}
	if !s.byDev.Allow(dev.DeviceID) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many uploads", "the daemon uploads hourly; wait a minute")
		return
	}
	var u teamwire.Upload
	if !decode(w, r, teamwire.MaxUploadBytes, &u) {
		return
	}
	if err := u.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "upload refused: "+err.Error(),
			"an upload carries derived figures only; see ADR 0012")
		return
	}
	resp, err := s.store.Ingest(r.Context(), dev, u)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, c caller) {
	me, err := s.store.Me(r.Context(), c.MemberID)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// leave revokes the calling device. Its figures are erased unless
// keep=true is passed.
func (s *Server) leave(w http.ResponseWriter, r *http.Request) {
	c, err := s.authBearer(r)
	if err != nil {
		s.authFailed(w, err)
		return
	}
	if c.Device == nil {
		writeError(w, http.StatusBadRequest, "only a device can leave", "run `tokenops team leave` on the machine")
		return
	}
	erase := r.URL.Query().Get("keep") != "true"
	if err := s.store.RevokeDevice(r.Context(), c.Principal, c.Device.DeviceID, erase); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true, "erased": erase})
}

func (s *Server) loginLink(w http.ResponseWriter, r *http.Request, c caller) {
	tok, expires, err := s.store.MintLogin(r.Context(), c.MemberID)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, teamwire.LoginLink{URL: s.loginURL(tok), ExpiresAt: expires})
}

func (s *Server) loginURL(tok string) string {
	return s.public.String() + "/login?t=" + tok
}

// AggregateRow is one row of an aggregate answer.
type AggregateRow struct {
	PeriodStart string      `json:"period_start"`
	Group       string      `json:"group"`
	People      int         `json:"people"`
	Suppressed  bool        `json:"suppressed"`
	Totals      team.Totals `json:"totals"`
	Rates       team.Rates  `json:"rates"`
}

// AggregateAnswer is GET /api/v1/aggregates: released cells (ADR 0012
// §3), never figures computed for the request.
type AggregateAnswer struct {
	By           string `json:"by"`
	Period       string `json:"period"`
	Since        string `json:"since"`
	Until        string `json:"until"`
	MinGroupSize int    `json:"min_group_size"`
	// Suppressed counts the withheld rows: fewer than MinGroupSize people,
	// or withheld so that another row cannot be worked out from the rest.
	Suppressed int `json:"suppressed"`
	// ReleasedThrough is the end (exclusive) of the newest released week;
	// later days are not shown yet. Empty before the first release.
	ReleasedThrough string         `json:"released_through,omitempty"`
	Rows            []AggregateRow `json:"rows"`
}

// maxRangeDays bounds a query's span.
const maxRangeDays = 800

// span reads since/until (dates, until exclusive), defaulting to the last
// eight weeks.
func span(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	until := team.Day.Start(now).AddDate(0, 0, 1)
	since := team.Week.Start(now).AddDate(0, 0, -7*7)
	q := r.URL.Query()
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(teamwire.DayLayout, v)
		if err != nil {
			return since, until, fmt.Errorf("until %q: want YYYY-MM-DD", v)
		}
		until = t
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(teamwire.DayLayout, v)
		if err != nil {
			return since, until, fmt.Errorf("since %q: want YYYY-MM-DD", v)
		}
		since = t
	}
	if !since.Before(until) || until.Sub(since) > maxRangeDays*24*time.Hour {
		return since, until, fmt.Errorf("since must be before until, at most %d days apart", maxRangeDays)
	}
	return since, until, nil
}

// errTeamFilter answers the removed team filter. Narrowing a repository
// or kind breakdown to one team let a reader subtract one filter from
// another and isolate a person (ADR 0012 §3).
var errTeamFilter = errors.New("team: aggregates no longer take a team filter; query by=team, or by=repo or by=kind for everyone")

// aggregate answers an aggregate query for c's organisation from its
// released weeks, releasing any that have settled first. Every cell is a
// fixed one — a dimension's group on a UTC day or in an ISO week — so the
// window only chooses which released cells to show.
func (s *Server) aggregate(r *http.Request, c caller) (AggregateAnswer, int, error) {
	q := r.URL.Query()
	if q.Has("team") {
		return AggregateAnswer{}, http.StatusBadRequest, errTeamFilter
	}
	by, err := team.ParseDimension(q.Get("by"))
	if err != nil {
		return AggregateAnswer{}, http.StatusBadRequest, err
	}
	period, err := team.ParsePeriod(q.Get("period"))
	if err != nil {
		return AggregateAnswer{}, http.StatusBadRequest, err
	}
	since, until, err := span(r, time.Now().UTC())
	if err != nil {
		return AggregateAnswer{}, http.StatusBadRequest, err
	}
	since = period.Start(since)
	org, err := s.store.OrgByID(r.Context(), c.OrgID)
	if err != nil {
		return AggregateAnswer{}, http.StatusInternalServerError, err
	}
	if err := s.release(r.Context(), c.OrgID); err != nil {
		return AggregateAnswer{}, http.StatusInternalServerError, err
	}
	ans := AggregateAnswer{By: string(by), Period: string(period), Since: since.Format(teamwire.DayLayout),
		Until: until.Format(teamwire.DayLayout), MinGroupSize: org.MinGroupSize, Rows: []AggregateRow{}}
	through, err := s.store.ReleasedThrough(r.Context(), c.OrgID)
	if err != nil {
		return ans, http.StatusInternalServerError, err
	}
	if !through.IsZero() {
		ans.ReleasedThrough = through.Format(teamwire.DayLayout)
	}
	cells, err := s.store.ReleasedCells(r.Context(), pgstore.ReleasedQuery{OrgID: c.OrgID, By: by, Period: period, Since: since, Until: until})
	if err != nil {
		return ans, http.StatusInternalServerError, err
	}
	for _, cell := range cells {
		if cell.Suppressed {
			ans.Suppressed++
		}
		ans.Rows = append(ans.Rows, AggregateRow{PeriodStart: cell.PeriodStart.Format(teamwire.DayLayout), Group: cell.Group,
			People: cell.People, Suppressed: cell.Suppressed, Totals: cell.Totals, Rates: cell.Totals.Rates()})
	}
	return ans, http.StatusOK, nil
}

// release releases an organisation's settled weeks and logs what it did.
func (s *Server) release(ctx context.Context, orgID string) error {
	rel, err := s.store.EnsureReleased(ctx, orgID)
	for _, w := range rel {
		s.log.Info("week released", "org", w.OrgID, "week", w.WeekStart.Format(teamwire.DayLayout),
			"cells", w.Report.Cells, "primary", w.Report.Primary, "secondary", w.Report.Secondary,
			"checks", w.Report.Checks, "fallback", w.Report.Fallback)
	}
	return err
}

func (s *Server) aggregates(w http.ResponseWriter, r *http.Request, c caller) {
	ans, status, err := s.aggregate(r, c)
	if err != nil {
		if status >= 500 {
			s.internal(w, err)
			return
		}
		writeError(w, status, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// MemberRow is a member as the members list shows them.
type MemberRow struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Teams    []string `json:"teams"`
	CanView  bool     `json:"can_view"`
	IsCaller bool     `json:"is_you,omitempty"`
}

// visibleMembers lists the members c may see the names of: everyone for an
// administrator, the members their grants reach for a grantee, and only
// themselves otherwise.
func (s *Server) visibleMembers(r *http.Request, c caller) ([]MemberRow, error) {
	all, err := s.store.Members(r.Context(), c.OrgID)
	if err != nil {
		return nil, err
	}
	grants, err := s.store.ActiveGrants(r.Context(), c.OrgID)
	if err != nil {
		return nil, err
	}
	out := []MemberRow{}
	for _, m := range all {
		access := team.CanViewMember(c.MemberID, c.Role, m.ID, m.TeamIDs, grants)
		if !access.Allowed && !c.Role.CanAdminister() {
			continue
		}
		teams := m.TeamNames
		if teams == nil {
			teams = []string{}
		}
		out = append(out, MemberRow{ID: m.ID, Name: m.DisplayName, Role: string(m.Role), Teams: teams,
			CanView: access.Allowed, IsCaller: access.Self})
	}
	return out, nil
}

func (s *Server) members(w http.ResponseWriter, r *http.Request, c caller) {
	rows, err := s.visibleMembers(r, c)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// MemberAnswer is GET /api/v1/members/{id}/metrics.
type MemberAnswer struct {
	Member string         `json:"member"`
	Teams  []string       `json:"teams"`
	Period string         `json:"period"`
	Since  string         `json:"since"`
	Until  string         `json:"until"`
	Rows   []AggregateRow `json:"rows"`
	// Recorded is set when the view was written to the audit log, which
	// the member can read.
	Recorded bool `json:"recorded"`
}

// memberView is the drill-down: allowed for the member themself, or under
// an active grant, and recorded in the audit log whenever it is not the
// member looking.
func (s *Server) memberView(r *http.Request, c caller, id string) (MemberAnswer, int, error) {
	period, err := team.ParsePeriod(r.URL.Query().Get("period"))
	if err != nil {
		return MemberAnswer{}, http.StatusBadRequest, err
	}
	since, until, err := span(r, time.Now().UTC())
	if err != nil {
		return MemberAnswer{}, http.StatusBadRequest, err
	}
	subject, err := s.store.MemberInOrg(r.Context(), c.OrgID, id)
	if errors.Is(err, pgstore.ErrNotFound) {
		return MemberAnswer{}, http.StatusNotFound, errors.New("member not found")
	}
	if err != nil {
		return MemberAnswer{}, http.StatusInternalServerError, err
	}
	grants, err := s.store.ActiveGrants(r.Context(), c.OrgID)
	if err != nil {
		return MemberAnswer{}, http.StatusInternalServerError, err
	}
	access := team.CanViewMember(c.MemberID, c.Role, subject.ID, subject.TeamIDs, grants)
	if !access.Allowed {
		return MemberAnswer{}, http.StatusForbidden, errors.New("individual figures need a grant from an owner, which the member is told about")
	}
	// Record before reading: a view that fails to be recorded is not shown.
	if !access.Self {
		if err := s.store.RecordView(r.Context(), c.Principal, subject.ID, access.GrantID); err != nil {
			return MemberAnswer{}, http.StatusInternalServerError, err
		}
	}
	rows, err := s.store.MemberSeries(r.Context(), subject.ID, period, since, until)
	if err != nil {
		return MemberAnswer{}, http.StatusInternalServerError, err
	}
	teams := subject.TeamNames
	if teams == nil {
		teams = []string{}
	}
	ans := MemberAnswer{Member: subject.DisplayName, Teams: teams, Period: string(period), Since: since.Format(teamwire.DayLayout),
		Until: until.Format(teamwire.DayLayout), Rows: []AggregateRow{}, Recorded: !access.Self}
	for _, row := range rows {
		ans.Rows = append(ans.Rows, AggregateRow{PeriodStart: row.PeriodStart.Format(teamwire.DayLayout), Group: row.Group,
			People: row.People, Totals: row.Totals, Rates: row.Totals.Rates()})
	}
	return ans, http.StatusOK, nil
}

func (s *Server) memberMetrics(w http.ResponseWriter, r *http.Request, c caller) {
	ans, status, err := s.memberView(r, c, r.PathValue("id"))
	if err != nil {
		if status >= 500 {
			s.internal(w, err)
			return
		}
		writeError(w, status, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

func (s *Server) requireAdmin(w http.ResponseWriter, c caller) bool {
	if !c.Role.CanAdminister() || c.Device != nil {
		writeError(w, http.StatusForbidden, "needs an owner's or admin's API token", "")
		return false
	}
	return true
}

func (s *Server) requireOwner(w http.ResponseWriter, c caller) bool {
	if !c.Role.CanGrant() || c.Device != nil {
		writeError(w, http.StatusForbidden, "needs an owner's API token", "")
		return false
	}
	return true
}

// setEmail sets the address single sign-on signs a member in by. Whoever
// controls that address can sign in as the member, so only an owner sets
// one, never another owner's (that is for the server's console), every
// change is audited, and the member's own page shows the address.
func (s *Server) setEmail(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireOwner(w, c) {
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, smallBody, &req) {
		return
	}
	id := r.PathValue("id")
	target, err := s.store.MemberInOrg(r.Context(), c.OrgID, id)
	if errors.Is(err, pgstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found", "")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if target.Role == team.RoleOwner && target.ID != c.MemberID {
		writeError(w, http.StatusForbidden, "cannot set another owner's e-mail", "run tokenops-team set-email on the server")
		return
	}
	err = s.store.SetEmail(r.Context(), c.Principal, id, req.Email)
	switch {
	case errors.Is(err, pgstore.ErrConflict):
		writeError(w, http.StatusConflict, err.Error(), "")
		return
	case err != nil && !errors.Is(err, pgstore.ErrNotFound):
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	case err != nil:
		writeError(w, http.StatusNotFound, "member not found", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": strings.ToLower(strings.TrimSpace(req.Email))})
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireAdmin(w, c) {
		return
	}
	id := r.PathValue("id")
	target, err := s.store.MemberInOrg(r.Context(), c.OrgID, id)
	if errors.Is(err, pgstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member not found", "")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	if !c.Role.AtLeast(target.Role) {
		writeError(w, http.StatusForbidden, "cannot remove a member with a higher role", "")
		return
	}
	if err := s.store.RemoveMember(r.Context(), c.Principal, id); err != nil {
		writeError(w, http.StatusConflict, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// TeamRow is a team as listed.
type TeamRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
}

func (s *Server) teams(w http.ResponseWriter, r *http.Request, c caller) {
	ts, err := s.store.Teams(r.Context(), c.OrgID)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := []TeamRow{}
	for _, t := range ts {
		out = append(out, TeamRow{ID: t.ID, Name: t.Name, Members: t.Members})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireAdmin(w, c) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, smallBody, &req) {
		return
	}
	name, ok := cleanName(req.Name, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "name: 1 to 100 printable characters", "")
		return
	}
	t, err := s.store.CreateTeam(r.Context(), c.Principal, name)
	if errors.Is(err, pgstore.ErrConflict) {
		writeError(w, http.StatusConflict, "a team with that name exists", "")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, TeamRow{ID: t.ID, Name: t.Name})
}

// InviteAnswer is POST /api/v1/invites.
type InviteAnswer struct {
	Invite    string    `json:"invite"`
	ExpiresAt time.Time `json:"expires_at"`
	Join      string    `json:"join"`
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireAdmin(w, c) {
		return
	}
	var req struct {
		Team     string `json:"team"`
		Role     string `json:"role"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !decode(w, r, smallBody, &req) {
		return
	}
	if req.Role == "" {
		req.Role = string(team.RoleMember)
	}
	role, err := team.ParseRole(req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	if !team.CanInvite(c.Role, role) {
		writeError(w, http.StatusForbidden, "cannot invite someone with a higher role than yours", "")
		return
	}
	tok, expires, err := s.store.CreateInvite(r.Context(), c.Principal, req.Team, role, time.Duration(req.TTLHours)*time.Hour)
	if errors.Is(err, pgstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "team not found", "create it first")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, InviteAnswer{Invite: tok, ExpiresAt: expires,
		Join: "tokenops team join " + s.public.String() + " " + tok})
}

// GrantRow is a grant as listed.
type GrantRow struct {
	ID        string     `json:"id"`
	Grantee   string     `json:"grantee"`
	GranteeID string     `json:"grantee_id"`
	Scope     string     `json:"scope"`
	Reason    string     `json:"reason"`
	GrantedBy string     `json:"granted_by"`
	GrantedAt time.Time  `json:"granted_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func (s *Server) grants(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireAdmin(w, c) {
		return
	}
	gs, err := s.store.Grants(r.Context(), c.OrgID)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := []GrantRow{}
	for _, g := range gs {
		scope := "everyone"
		if g.TeamName != "" {
			scope = "team " + g.TeamName
		}
		out = append(out, GrantRow{ID: g.ID, Grantee: g.GranteeName, GranteeID: g.GranteeID, Scope: scope, Reason: g.Reason,
			GrantedBy: g.GrantedBy, GrantedAt: g.CreatedAt, RevokedAt: g.RevokedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createGrant(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireOwner(w, c) {
		return
	}
	var req struct {
		GranteeID string `json:"grantee_id"`
		Team      string `json:"team"`
		Reason    string `json:"reason"`
	}
	if !decode(w, r, smallBody, &req) {
		return
	}
	reason, ok := cleanName(req.Reason, 500)
	if !ok {
		writeError(w, http.StatusBadRequest, "reason: say why, in 1 to 500 characters", "the member reads it")
		return
	}
	id, err := s.store.CreateGrant(r.Context(), c.Principal, req.GranteeID, req.Team, reason)
	if errors.Is(err, pgstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "member or team not found", "")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireOwner(w, c) {
		return
	}
	err := s.store.RevokeGrant(r.Context(), c.Principal, r.PathValue("id"))
	if errors.Is(err, pgstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no active grant with that id", "")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// AuditRow is one audit entry.
type AuditRow struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Subject string    `json:"subject,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireAdmin(w, c) {
		return
	}
	entries, err := s.store.Audit(r.Context(), c.OrgID, 500)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := []AuditRow{}
	for _, e := range entries {
		out = append(out, AuditRow(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireOwner(w, c) {
		return
	}
	var req struct {
		MinGroupSize  int `json:"min_group_size"`
		RetentionDays int `json:"retention_days"`
	}
	if !decode(w, r, smallBody, &req) {
		return
	}
	if req.MinGroupSize < 1 || req.MinGroupSize > 50 {
		writeError(w, http.StatusBadRequest, "min_group_size: 1 to 50", "below 3, a group's figures are one or two people's")
		return
	}
	if req.RetentionDays < team.MinRetentionDays || req.RetentionDays > team.MaxRetentionDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("retention_days: %d to %d", team.MinRetentionDays, team.MaxRetentionDays), "")
		return
	}
	if err := s.store.SetPrivacy(r.Context(), c.Principal, req.MinGroupSize, req.RetentionDays); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}
