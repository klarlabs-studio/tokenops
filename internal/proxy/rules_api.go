package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"
	"github.com/felixgeelhaar/tokenops/internal/domainevents"
	"github.com/felixgeelhaar/tokenops/internal/infra/rulesfs"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
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

// AttachDomainBus subscribes the handler's cache to RuleCorpusReloaded
// so the next /api/rules/analyze after a corpus change re-ingests
// rather than serving stale data. Adapters should call this from the
// daemon composition root once the bus is wired.
func (h *RulesHandlers) AttachDomainBus(bus *domainevents.Bus) {
	if h == nil || bus == nil {
		return
	}
	bus.Subscribe(domainevents.KindRuleCorpusReloaded, func(domainevents.Event) {
		h.invalidate()
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
func (h *RulesHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rules/analyze", h.analyze)
	mux.HandleFunc("GET /api/rules/conflicts", h.conflicts)
	mux.HandleFunc("GET /api/rules/compress", h.compress)
	mux.HandleFunc("GET /api/rules/inject", h.inject)
}

// WithRules installs the rules handlers on the proxy.
func WithRules(h *RulesHandlers) Option {
	return func(s *Server) { s.rulesAPI = h }
}

func (h *RulesHandlers) opts(r *http.Request) (string, string, eventschema.Provider) {
	root := r.URL.Query().Get("root")
	if root == "" {
		root = h.root
	}
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		repoID = h.repoID
	}
	prov := eventschema.ProviderOpenAI
	switch r.URL.Query().Get("provider") {
	case "anthropic":
		prov = eventschema.ProviderAnthropic
	case "gemini":
		prov = eventschema.ProviderGemini
	}
	return root, repoID, prov
}

func (h *RulesHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	root, repoID, prov := h.opts(r)
	cacheKey := root + "|" + repoID + "|" + string(prov)
	if body, ok := h.cachedAnalyze(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "hit")
		_, _ = w.Write(body)
		return
	}
	docs, err := rulesfs.LoadCorpus(root, repoID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	res, err := rules.AnalyzeDocs(docs, rules.AnalysisOptions{
		Providers: []eventschema.Provider{prov},
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	body, mErr := json.Marshal(map[string]any{
		"documents":        res.Documents,
		"duplicate_groups": res.DuplicateGroups,
	})
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
	root, repoID, _ := h.opts(r)
	docs, err := rulesfs.LoadCorpus(root, repoID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	findings := rules.DetectConflicts(docs, rules.ConflictOptions{})
	writeAPIJSON(w, http.StatusOK, map[string]any{"findings": findings})
}

func (h *RulesHandlers) compress(w http.ResponseWriter, r *http.Request) {
	root, repoID, _ := h.opts(r)
	threshold, _ := strconv.ParseFloat(r.URL.Query().Get("similarity"), 64)
	quality, _ := strconv.ParseFloat(r.URL.Query().Get("quality_floor"), 64)
	docs, err := rulesfs.LoadCorpus(root, repoID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	c := rules.NewCompressor(rules.CompressConfig{
		SimilarityThreshold: threshold,
		QualityFloor:        quality,
	}, nil)
	type view struct {
		SourceID         string  `json:"source_id"`
		Path             string  `json:"path"`
		OriginalTokens   int64   `json:"original_tokens"`
		CompressedTokens int64   `json:"compressed_tokens"`
		QualityScore     float64 `json:"quality_score"`
		Accepted         bool    `json:"accepted"`
		DroppedSections  int     `json:"dropped_sections"`
	}
	views := make([]view, 0, len(docs))
	for _, d := range docs {
		r := c.Compress(d)
		dropped := 0
		for _, s := range r.Sections {
			if s.Dropped {
				dropped++
			}
		}
		views = append(views, view{
			SourceID:         r.SourceID,
			Path:             d.Path,
			OriginalTokens:   r.OriginalTokens,
			CompressedTokens: r.CompressedTokens,
			QualityScore:     r.QualityScore,
			Accepted:         r.Accepted,
			DroppedSections:  dropped,
		})
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{"results": views})
}

func (h *RulesHandlers) inject(w http.ResponseWriter, r *http.Request) {
	root, repoID, _ := h.opts(r)
	q := r.URL.Query()
	minScore, _ := strconv.ParseFloat(q.Get("min_score"), 64)
	tokenBudget, _ := strconv.ParseInt(q.Get("token_budget"), 10, 64)
	globalAdmit := q.Get("include_global") != "false"
	docs, err := rulesfs.LoadCorpus(root, repoID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	router := rules.NewRouter(rules.RouterConfig{
		MinScore:           minScore,
		TokenBudget:        tokenBudget,
		IncludeGlobalScope: globalAdmit,
	})
	res := router.Select(docs, rules.SelectionSignals{
		WorkflowID: q.Get("workflow_id"),
		AgentID:    q.Get("agent_id"),
		RepoID:     repoID,
		FilePaths:  parseList(q.Get("files")),
		Tools:      parseList(q.Get("tools")),
		Keywords:   parseList(q.Get("keywords")),
	})
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
