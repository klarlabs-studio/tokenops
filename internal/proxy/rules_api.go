package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/ruleintel"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// RulesHandlers serves the Rule Intelligence read-only API (issue #12).
// Each request triggers a fresh ingest under the configured Root so the
// dashboard always reflects the corpus on disk; results are not cached so
// rule edits surface immediately. Bodies are never returned — only
// metrics, anchors, hashes, and routing rationale — keeping redaction
// structural.
type RulesHandlers struct {
	root   string
	repoID string

	cacheMu sync.RWMutex
	cache   map[string]cachedAnalysis
}

type cachedAnalysis struct {
	at   time.Time
	body []byte
}

// NewRulesHandlers builds the handler with the rule-source root the
// daemon should scan. Empty root falls back to "." (daemon working dir).
func NewRulesHandlers(root, repoID string) (*RulesHandlers, error) {
	if root == "" {
		return nil, errors.New("proxy: RulesHandlers requires a root")
	}
	return &RulesHandlers{root: root, repoID: repoID, cache: map[string]cachedAnalysis{}}, nil
}

// AttachEventBus subscribes the handler's cache to canonical domain events
// so the next /api/rules/analyze after a corpus change re-ingests
// rather than serving stale data. Adapters should call this from the
// daemon composition root once the bus is wired. The returned function
// detaches the observer during shutdown.
func (h *RulesHandlers) AttachEventBus(bus events.Observable) func() {
	if h == nil || bus == nil {
		return func() {}
	}
	return bus.Subscribe(func(env *eventschema.Envelope) {
		if env == nil || env.Type != eventschema.EventTypeDomain {
			return
		}
		payload, ok := env.Payload.(*eventschema.DomainEvent)
		if ok && payload.Kind == "rule_corpus.reloaded" {
			h.invalidate()
		}
	})
}

func (h *RulesHandlers) invalidate() {
	h.cacheMu.Lock()
	h.cache = map[string]cachedAnalysis{}
	h.cacheMu.Unlock()
}

// cacheTTL caps how long a cached analysis result may live even when
// no RuleCorpusReloaded fires (defence against externally-mutated
// corpora the bus never sees).
const cacheTTL = 30 * time.Second

func (h *RulesHandlers) cachedAnalyze(key string) ([]byte, bool) {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	c, ok := h.cache[key]
	if !ok {
		return nil, false
	}
	if time.Since(c.at) > cacheTTL {
		return nil, false
	}
	return c.body, true
}

func (h *RulesHandlers) storeAnalyze(key string, body []byte) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cache[key] = cachedAnalysis{at: time.Now(), body: body}
}

// Register installs every Rule Intelligence endpoint on mux.
func (h *RulesHandlers) Register(mux RouteMux) {
	mux.HandleFunc("GET /api/rules/analyze", h.analyze)
	mux.HandleFunc("GET /api/rules/conflicts", h.conflicts)
	mux.HandleFunc("GET /api/rules/compress", h.compress)
	mux.HandleFunc("GET /api/rules/inject", h.inject)
}

// WithRules installs the rules handlers on the proxy.
func WithRules(h *RulesHandlers) Option {
	return func(s *Server) { s.rulesAPI = h }
}

func (h *RulesHandlers) opts(r *http.Request) (ruleintel.Corpus, eventschema.Provider) {
	root := r.URL.Query().Get("root")
	if root == "" {
		root = h.root
	}
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		repoID = h.repoID
	}
	return ruleintel.Corpus{Root: root, RepoID: repoID}, ruleintel.Provider(r.URL.Query().Get("provider"))
}

func (h *RulesHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	corpus, prov := h.opts(r)
	cacheKey := corpus.Root + "|" + corpus.RepoID + "|" + string(prov)
	if body, ok := h.cachedAnalyze(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "hit")
		_, _ = w.Write(body)
		return
	}
	res, err := ruleintel.Analyze(corpus, prov)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	body, mErr := json.Marshal(res)
	if mErr != nil {
		writeAPIError(w, http.StatusInternalServerError, mErr)
		return
	}
	h.storeAnalyze(cacheKey, body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "miss")
	_, _ = w.Write(body)
}

func (h *RulesHandlers) conflicts(w http.ResponseWriter, r *http.Request) {
	corpus, _ := h.opts(r)
	res, err := ruleintel.DetectConflicts(corpus)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func (h *RulesHandlers) compress(w http.ResponseWriter, r *http.Request) {
	corpus, _ := h.opts(r)
	threshold, _ := strconv.ParseFloat(r.URL.Query().Get("similarity"), 64)
	quality, _ := strconv.ParseFloat(r.URL.Query().Get("quality_floor"), 64)
	res, err := ruleintel.Compress(corpus, ruleintel.CompressOptions{
		SimilarityThreshold: threshold,
		QualityFloor:        quality,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func (h *RulesHandlers) inject(w http.ResponseWriter, r *http.Request) {
	corpus, _ := h.opts(r)
	q := r.URL.Query()
	minScore, _ := strconv.ParseFloat(q.Get("min_score"), 64)
	tokenBudget, _ := strconv.ParseInt(q.Get("token_budget"), 10, 64)
	res, err := ruleintel.Inject(corpus, ruleintel.InjectQuery{
		MinScore:           minScore,
		TokenBudget:        tokenBudget,
		IncludeGlobalScope: q.Get("include_global") != "false",
		WorkflowID:         q.Get("workflow_id"),
		AgentID:            q.Get("agent_id"),
		FilePaths:          parseList(q.Get("files")),
		Tools:              parseList(q.Get("tools")),
		Keywords:           parseList(q.Get("keywords")),
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func parseList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	// Fallback: comma-separated.
	for _, p := range splitCSV(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitCSV(s string) []string {
	res := make([]string, 0, 4)
	cur := []byte{}
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			res = append(res, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, s[i])
	}
	res = append(res, string(cur))
	return res
}
