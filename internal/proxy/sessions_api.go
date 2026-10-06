package proxy

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// SessionRoots are where the daemon reads transcripts; empty resolves each
// client's default.
type SessionRoots struct {
	// Transcripts is the root for agent DX and the work account.
	Transcripts string
	// Prompts is Claude Code's project root for prompt scoring.
	Prompts string
}

// WithSessions serves what agent sessions are like to work with, from the
// same capability the MCP tools call (ADR 0010, slice 3):
//
//	GET /api/dx?days=&all=              graded agent DX
//	GET /api/story?days=&all=&limit=    recent work, task by task
//	GET /api/coach/prompts?since=&until=&session_id=&limit=
//
// Both carry derived figures only: story titles and quoted instructions
// are withheld (ADR 0010 §5). roots is called per request; nil leaves the
// routes unmounted.
func WithSessions(roots func() SessionRoots) Option {
	return func(s *Server) { s.sessions = roots }
}

func (s *Server) registerSessionRoutes(mux RouteMux) {
	if s.sessions == nil {
		return
	}
	mux.HandleFunc("GET /api/dx", func(w http.ResponseWriter, r *http.Request) {
		win, err := sessionWindow(r, s.sessions().Transcripts)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, findings.DX(win, time.Now()))
	})
	mux.HandleFunc("GET /api/story", func(w http.ResponseWriter, r *http.Request) {
		win, err := sessionWindow(r, s.sessions().Transcripts)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		limit, err := intParam(r, "limit")
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, sessions.ComputeStory(win, limit, false, time.Now()))
	})
	mux.HandleFunc("GET /api/coach/prompts", func(w http.ResponseWriter, r *http.Request) {
		limit, err := intParam(r, "limit")
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		since, until, err := sinceUntil(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if r.URL.Query().Get("since") == "" {
			since = time.Time{} // the capability's own default: seven days
		}
		pw := sessions.PromptWindow{Root: s.sessions().Prompts, SessionID: r.URL.Query().Get("session_id"),
			Limit: limit, Since: since, Until: until}
		f, err := sessions.PromptFindings(pw, false, time.Now())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, f)
	})
}

// sessionWindow reads ?days= and ?all=.
func sessionWindow(r *http.Request, root string) (sessions.Window, error) {
	days, err := intParam(r, "days")
	if err != nil {
		return sessions.Window{}, err
	}
	all := false
	if v := r.URL.Query().Get("all"); v != "" {
		if all, err = strconv.ParseBool(v); err != nil {
			return sessions.Window{}, errors.New("all must be true or false")
		}
	}
	return sessions.Window{Root: root, Days: days, All: all}, nil
}

// intParam reads an optional integer query parameter.
func intParam(r *http.Request, name string) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New(name + " must be an integer")
	}
	return n, nil
}
