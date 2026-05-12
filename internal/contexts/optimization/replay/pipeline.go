package replay

import (
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/contexttrim"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/dedupe"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/promptcompress"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer/retrievalprune"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
)

// PipelineConfig customises the optimizer mix without forcing callers
// to instantiate each optimizer themselves. Zero values fall through to
// DefaultPipeline behavior.
type PipelineConfig struct {
	// Disable lists optimizer kinds the caller wants stripped from the
	// default pipeline (e.g. ["semantic_dedupe"] for a compress-only
	// replay).
	Disable []string
}

// DefaultPipeline returns the canonical optimizer pipeline used by every
// replay path (CLI `tokenops replay`, MCP `tokenops_replay`, and any
// future scheduled replays). Keeping the construction in one place is a
// DDD requirement: the pipeline is the domain rule for how a session is
// re-evaluated; adapters must not assemble their own variant.
//
// Pass a non-nil tokenizer.Registry to share a single registry across
// optimizers; nil constructs a fresh registry.
func DefaultPipeline(reg *tokenizer.Registry) *optimizer.Pipeline {
	return BuildPipeline(reg, PipelineConfig{})
}

// BuildPipeline is the configurable form of DefaultPipeline. The
// composition root (bootstrap) uses this to apply operator
// preferences before handing the pipeline to adapters.
func BuildPipeline(reg *tokenizer.Registry, cfg PipelineConfig) *optimizer.Pipeline {
	if reg == nil {
		reg = tokenizer.NewRegistry()
	}
	disabled := map[string]bool{}
	for _, k := range cfg.Disable {
		disabled[k] = true
	}
	all := []struct {
		kind string
		opt  optimizer.Optimizer
	}{
		{"prompt_compress", promptcompress.New(promptcompress.Config{}, reg)},
		{"semantic_dedupe", dedupe.New(dedupe.Config{}, reg)},
		{"retrieval_prune", retrievalprune.New(retrievalprune.Config{}, reg)},
		{"context_trim", contexttrim.New(contexttrim.Config{}, reg)},
	}
	opts := make([]optimizer.Optimizer, 0, len(all))
	for _, a := range all {
		if disabled[a.kind] {
			continue
		}
		opts = append(opts, a.opt)
	}
	return optimizer.NewPipeline(opts...)
}
