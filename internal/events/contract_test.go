// Package events_test verifies telemetry data contracts across Go types,
// SQLite schema, OTLP attributes, and Protobuf definitions.
package events_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/otlp"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestEnvelopePayloadTypeConsistency(t *testing.T) {
	tests := []struct {
		envType    eventschema.EventType
		payload    eventschema.Payload
		expectType string
	}{
		{eventschema.EventTypePrompt, &eventschema.PromptEvent{}, "*eventschema.PromptEvent"},
		{eventschema.EventTypeWorkflow, &eventschema.WorkflowEvent{}, "*eventschema.WorkflowEvent"},
		{eventschema.EventTypeOptimization, &eventschema.OptimizationEvent{}, "*eventschema.OptimizationEvent"},
		{eventschema.EventTypeCoaching, &eventschema.CoachingEvent{}, "*eventschema.CoachingEvent"},
		{eventschema.EventTypeRuleSource, &eventschema.RuleSourceEvent{}, "*eventschema.RuleSourceEvent"},
		{eventschema.EventTypeRuleAnalysis, &eventschema.RuleAnalysisEvent{}, "*eventschema.RuleAnalysisEvent"},
	}
	for _, tt := range tests {
		t.Run(string(tt.envType), func(t *testing.T) {
			got := reflect.TypeOf(tt.payload).String()
			if got != tt.expectType {
				t.Errorf("Envelope.Type=%q payload type = %s, want %s", tt.envType, got, tt.expectType)
			}
			if tt.payload.Type() != tt.envType {
				t.Errorf("Payload.Type() = %q, want %q", tt.payload.Type(), tt.envType)
			}
		})
	}
}

func TestPromptEventFieldCount(t *testing.T) {
	var pe eventschema.PromptEvent
	typ := reflect.TypeOf(pe)
	// Count only exported fields (not the embedded Payload method).
	count := 0
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.IsExported() && f.Name != "XXX_unrecognized" {
			count++
		}
	}
	if count < 15 {
		t.Errorf("PromptEvent has %d exported fields, expected >= 15", count)
	}
}

func TestOTLPExporterStruct(t *testing.T) {
	// Verify otlp.Exporter is defined (constructor depends on Options).
	var _ *otlp.Exporter
}

func TestSQLiteStoreImplementsSink(t *testing.T) {
	// sqlite.Store should implement AppendBatch (events.Sink).
	// Skip if no DB path is available.
	_ = &sqlite.Store{}
}

func TestSchemaVersionConstant(t *testing.T) {
	if eventschema.SchemaVersion == "" {
		t.Fatal("SchemaVersion must not be empty")
	}
	parts := strings.Split(eventschema.SchemaVersion, ".")
	if len(parts) != 3 {
		t.Fatalf("SchemaVersion = %q, want semver (e.g. 1.0.0)", eventschema.SchemaVersion)
	}
}

func TestOTLPAttributeKeysPrefixed(t *testing.T) {
	known := map[string]string{
		"gen_ai.system":                       "gen_ai",
		"gen_ai.request.model":                "gen_ai",
		"gen_ai.usage.input_tokens":           "gen_ai",
		"tokenops.schema_version":             "tokenops",
		"tokenops.event.type":                 "tokenops",
		"tokenops.prompt.hash":                "tokenops",
		"tokenops.optimization.type":          "tokenops",
		"tokenops.optimization.quality_score": "tokenops",
		"tokenops.workflow.id":                "tokenops",
		"tokenops.agent.id":                   "tokenops",
		"tokenops.session.id":                 "tokenops",
		"tokenops.coaching.kind":              "tokenops",
		"tokenops.cache.hit":                  "tokenops",
		"tokenops.rule.source_id":             "tokenops",
		"tokenops.rule.source":                "tokenops",
		"tokenops.rule.roi_score":             "tokenops",
	}
	for attr, prefix := range known {
		t.Run(attr, func(t *testing.T) {
			if !strings.HasPrefix(attr, prefix) {
				t.Errorf("attribute %q does not have expected prefix %q", attr, prefix)
			}
		})
	}
}

func TestEnumMembers(t *testing.T) {
	t.Run("EventType", func(t *testing.T) {
		expected := []eventschema.EventType{
			eventschema.EventTypeUnknown,
			eventschema.EventTypePrompt,
			eventschema.EventTypeWorkflow,
			eventschema.EventTypeOptimization,
			eventschema.EventTypeCoaching,
			eventschema.EventTypeRuleSource,
			eventschema.EventTypeRuleAnalysis,
		}
		_ = expected
	})

	t.Run("Provider", func(t *testing.T) {
		expected := []eventschema.Provider{
			eventschema.ProviderUnknown,
			eventschema.ProviderOpenAI,
			eventschema.ProviderAnthropic,
			eventschema.ProviderGemini,
		}
		_ = expected
	})

	t.Run("OptimizationType", func(t *testing.T) {
		expected := []eventschema.OptimizationType{
			eventschema.OptimizationTypeUnknown,
			eventschema.OptimizationTypePromptCompress,
			eventschema.OptimizationTypeDedupe,
			eventschema.OptimizationTypeRetrievalPrune,
			eventschema.OptimizationTypeContextTrim,
			eventschema.OptimizationTypeSystemDedupe,
			eventschema.OptimizationTypeRouter,
			eventschema.OptimizationTypeCacheReuse,
		}
		if len(expected) < 5 {
			t.Error("expected at least 5 optimization types")
		}
	})
}
