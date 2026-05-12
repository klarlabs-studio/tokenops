package replay

import (
	"testing"
)

func TestDefaultPipelineHasFourOptimizers(t *testing.T) {
	p := DefaultPipeline(nil)
	if got := len(p.Optimizers()); got != 4 {
		t.Errorf("optimizers = %d, want 4", got)
	}
}

func TestBuildPipelineRespectsDisable(t *testing.T) {
	p := BuildPipeline(nil, PipelineConfig{Disable: []string{"semantic_dedupe", "context_trim"}})
	if got := len(p.Optimizers()); got != 2 {
		t.Errorf("optimizers = %d, want 2 after disabling 2", got)
	}
}
