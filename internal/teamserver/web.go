package teamserver

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

//go:embed templates/*.html static/app.css
var assets embed.FS

// pages holds one template set per page, each the layout plus the page.
type pages struct {
	sets map[string]*template.Template
	css  []byte
}

var funcs = template.FuncMap{
	"usd": func(v float64) string { return "$" + strconv.FormatFloat(v, 'f', 2, 64) },
	"num": func(v int64) string {
		s := strconv.FormatInt(v, 10)
		var b strings.Builder
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 && c != '-' {
				b.WriteByte(',')
			}
			b.WriteRune(c)
		}
		return b.String()
	},
	"pct":  func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + "%" },
	"dec1": func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
	"join": strings.Join,
}

func loadPages() (*pages, error) {
	css, err := fs.ReadFile(assets, "static/app.css")
	if err != nil {
		return nil, err
	}
	p := &pages{sets: map[string]*template.Template{}, css: css}
	for _, name := range []string{"signin", "login", "sso", "signedin", "overview", "me", "members", "member", "audit", "error"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		p.sets[name] = t
	}
	return p, nil
}

// view is what every page renders from.
type view struct {
	Title string
	User  *pgstore.Principal
	Admin bool
	Data  any
	// Refresh, when set, sends the browser on to this path at once.
	Refresh string
}

// signinData is the sign-in page's: whether single sign-on is offered, and
// why the last attempt did not work.
type signinData struct {
	SSO   bool
	Error string
}

// signin renders the sign-in page, offering single sign-on when any
// organisation here has it.
func (s *Server) signin(w http.ResponseWriter, r *http.Request, status int, title string) {
	sso, err := s.store.SSOEnabled(r.Context())
	if err != nil {
		s.log.Error("sso lookup", "err", err)
	}
	s.render(w, status, "signin", view{Title: title, Data: signinData{SSO: sso}})
}

func (s *Server) render(w http.ResponseWriter, status int, page string, v view) {
	var buf bytes.Buffer
	if err := s.pages.sets[page].ExecuteTemplate(&buf, "layout.html", v); err != nil {
		s.log.Error("render", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(s.pages.css)
}

// cookieName is the session cookie. Over HTTPS it carries the __Host-
// prefix, which the browser only accepts Secure, host-only and on /.
func (s *Server) cookieName() string {
	if s.public.Scheme == "https" {
		return "__Host-tokenops_team"
	}
	return "tokenops_team"
}

// sameOrigin rejects a form post another site sent: the browser names the
// page's origin, and it must be this service.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	return origin == s.public.Scheme+"://"+s.public.Host
}

// web wraps a page that needs a signed-in browser.
func (s *Server) web(h func(http.ResponseWriter, *http.Request, caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(s.cookieName())
		if err == nil {
			if k, kerr := team.KindOf(ck.Value); kerr == nil && k == team.TokenSession {
				p, aerr := s.store.AuthToken(r.Context(), ck.Value, team.TokenSession)
				if aerr == nil {
					h(w, r, caller{Principal: p})
					return
				}
				if !errors.Is(aerr, pgstore.ErrUnauthorized) {
					s.internal(w, aerr)
					return
				}
			}
		}
		s.signin(w, r, http.StatusUnauthorized, "Sign in")
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if k, err := team.KindOf(tok); err != nil || k != team.TokenLogin {
		s.signin(w, r, http.StatusBadRequest, "Sign in")
		return
	}
	// A link opened by a mail scanner or a preview must not use it up, so
	// the GET only shows a button; the POST redeems.
	s.render(w, http.StatusOK, "login", view{Title: "Sign in", Data: tok})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, smallBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	session, _, err := s.store.RedeemLogin(r.Context(), r.PostForm.Get("t"))
	if errors.Is(err, pgstore.ErrUnauthorized) {
		s.render(w, http.StatusForbidden, "error", view{Title: "Link used or expired",
			Data: "This sign-in link was already used or has expired. Run `tokenops team web` for a new one."})
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setSession hands the browser its session cookie.
func (s *Server) setSession(w http.ResponseWriter, session string) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: session, Path: "/", HttpOnly: true,
		Secure: s.public.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: int(team.SessionTTL.Seconds())})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if ck, err := r.Cookie(s.cookieName()); err == nil {
		_ = s.store.RevokeToken(r.Context(), ck.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", HttpOnly: true,
		Secure: s.public.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	s.signin(w, r, http.StatusOK, "Signed out")
}

// periodGroup is one period's rows on the overview.
type periodGroup struct {
	Start string
	Rows  []AggregateRow
}

type overviewData struct {
	AggregateAnswer
	Periods []periodGroup
	Teams   []pgstore.Team
	Error   string
}

func groupByPeriod(rows []AggregateRow) []periodGroup {
	var out []periodGroup
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].Start != r.PeriodStart {
			out = append(out, periodGroup{Start: r.PeriodStart})
		}
		out[len(out)-1].Rows = append(out[len(out)-1].Rows, r)
	}
	// Newest period first: the week being asked about is the last one.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Server) overviewPage(w http.ResponseWriter, r *http.Request, c caller) {
	ans, status, err := s.aggregate(r, c)
	data := overviewData{AggregateAnswer: ans}
	if err != nil {
		if status >= 500 {
			s.internal(w, err)
			return
		}
		data.Error = err.Error()
	}
	data.Periods = groupByPeriod(ans.Rows)
	data.Teams, _ = s.store.Teams(r.Context(), c.OrgID)
	s.render(w, http.StatusOK, "overview", view{Title: "Team overview", User: &c.Principal, Admin: c.Role.CanAdminister(), Data: data})
}

func (s *Server) mePage(w http.ResponseWriter, r *http.Request, c caller) {
	me, err := s.store.Me(r.Context(), c.MemberID)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.render(w, http.StatusOK, "me", view{Title: "What you share", User: &c.Principal, Admin: c.Role.CanAdminister(),
		Data: struct {
			Me     teamwire.Me
			Fields []string
		}{me, uploadFields}})
}

// uploadFields names what an upload carries, for the member's page.
var uploadFields = []string{"day (UTC)", "repository label", "kind of work", "sessions", "instructions", "turns",
	"tool calls", "time waiting on the agent", "first-try, reworked, interrupted, escalated and rejected counts",
	"tokens", "cost and API-equivalent value", "unpriced turns"}

func (s *Server) membersPage(w http.ResponseWriter, r *http.Request, c caller) {
	rows, err := s.visibleMembers(r, c)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.render(w, http.StatusOK, "members", view{Title: "Members", User: &c.Principal, Admin: c.Role.CanAdminister(), Data: rows})
}

func (s *Server) memberPage(w http.ResponseWriter, r *http.Request, c caller) {
	ans, status, err := s.memberView(r, c, r.PathValue("id"))
	if err != nil {
		if status >= 500 {
			s.internal(w, err)
			return
		}
		s.render(w, status, "error", view{Title: "Not available", User: &c.Principal, Admin: c.Role.CanAdminister(), Data: err.Error()})
		return
	}
	s.render(w, http.StatusOK, "member", view{Title: ans.Member, User: &c.Principal, Admin: c.Role.CanAdminister(),
		Data: struct {
			MemberAnswer
			Periods []periodGroup
		}{ans, groupByPeriod(ans.Rows)}})
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request, c caller) {
	if !c.Role.CanAdminister() {
		s.render(w, http.StatusForbidden, "error", view{Title: "Not available", User: &c.Principal, Data: "The audit log is for owners and admins."})
		return
	}
	entries, err := s.store.Audit(r.Context(), c.OrgID, 500)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.render(w, http.StatusOK, "audit", view{Title: "Audit log", User: &c.Principal, Admin: true, Data: entries})
}
