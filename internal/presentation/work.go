package presentation

import "fmt"

// WorkInsight summarizes only the workflow trace and enabled waste checks.
// A missing finding is not evidence of task success or overall quality.
type WorkInsight struct {
	Level        string `json:"level" jsonschema:"description=attention when current waste checks report findings, no_finding when checks report none, unavailable when no work trace is reconstructed"`
	Summary      string `json:"summary"`
	FindingCount int    `json:"finding_count"`
	Scope        string `json:"scope"`
}

// ForWork turns existing trace and waste-detector outputs into a compact
// insight. It introduces no additional thresholds or quality inference.
func ForWork(stepCount int, findingSummaries []string) WorkInsight {
	if stepCount <= 0 {
		return WorkInsight{
			Level:   "unavailable",
			Summary: "No workflow steps were reconstructed; verify the workflow_id and source ingestion.",
			Scope:   "workflow_trace_and_waste_detector",
		}
	}
	if len(findingSummaries) == 0 {
		return WorkInsight{
			Level:   "no_finding",
			Summary: "No known waste pattern was detected by current checks; this is not an assessment of task success or overall work quality.",
			Scope:   "workflow_trace_and_waste_detector",
		}
	}
	first := findingSummaries[0]
	if first == "" {
		first = "See findings for the reported pattern."
	}
	return WorkInsight{
		Level:        "attention",
		Summary:      fmt.Sprintf("Current waste checks reported %d finding(s); first: %s", len(findingSummaries), first),
		FindingCount: len(findingSummaries),
		Scope:        "workflow_trace_and_waste_detector",
	}
}
