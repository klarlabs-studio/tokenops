package presentation

import "testing"

func TestForWork(t *testing.T) {
	tests := []struct {
		name     string
		steps    int
		findings []string
		level    string
		count    int
	}{
		{"no trace", 0, nil, "unavailable", 0},
		{"no detected waste", 3, nil, "no_finding", 0},
		{"finding", 2, []string{"Runaway context growth"}, "attention", 1},
		{"multiple findings", 2, []string{"Large context", "Repeated loop"}, "attention", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ForWork(tc.steps, tc.findings)
			if got.Level != tc.level || got.FindingCount != tc.count {
				t.Errorf("ForWork() = %+v, want level=%q count=%d", got, tc.level, tc.count)
			}
		})
	}
}
