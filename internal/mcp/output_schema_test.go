package mcp

import (
	"testing"

	"go.klarlabs.de/mcp/schema"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay"
	"go.klarlabs.de/tokenops/internal/contexts/rules"
)

// TestOutputSchemasGenerate is a guard test for structured-output adoption.
//
// ToolBuilder.OutputSchema runs schema.Generate at registration time; if it
// errors, the ToolBuilder silently records the error and the tool is never
// registered (a caller would then see a missing tool, not a loud failure).
// This test asserts schema.Generate succeeds for every type advertised via
// .OutputSchema(...) across the MCP tool surface, so a schema-breaking field
// change fails CI here instead of silently dropping a tool at runtime.
//
// Every type passed to a .OutputSchema(T{}) call in this package MUST appear
// in this table.
func TestOutputSchemasGenerate(t *testing.T) {
	advertised := map[string]any{
		// tools.go
		"spendSummaryResult":  spendSummaryResult{},
		"topConsumersResult":  topConsumersResult{},
		"forecastResult":      forecastResult{},
		"workflowTraceResult": workflowTraceResult{},
		"optimizationsResult": optimizationsResult{},
		// control_tools.go
		"versionResult":      versionResult{},
		"statusResult":       statusResult{},
		"domainEventsResult": domainEventsResult{},
		// plan_tools.go
		"planHeadroomResult": planHeadroomResult{},
		// parity_tools.go
		"rules.BenchmarkResult": rules.BenchmarkResult{},
		"evalResult":            evalResult{},
		"coverdebt.Report":      coverdebt.Report{},
		"scorecard.Scorecard":   scorecard.Scorecard{},
		"replay.Result":         replay.Result{},
		"auditResult":           auditResult{},
		// rules_tools.go
		"rulesAnalyzeResult":    rulesAnalyzeResult{},
		"rulesConflictsResult":  rulesConflictsResult{},
		"rulesCompressResult":   rulesCompressResult{},
		"rules.SelectionResult": rules.SelectionResult{},
		// coach_tools.go
		"prompts.Findings": prompts.Findings{},
		// data_sources_tool.go
		"dataSourcesResult": dataSourcesResult{},
		// help_tool.go
		"helpResult": helpResult{},
	}

	for name, v := range advertised {
		t.Run(name, func(t *testing.T) {
			if _, err := schema.Generate(v); err != nil {
				t.Fatalf("schema.Generate(%s): %v", name, err)
			}
		})
	}
}
