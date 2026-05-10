package scorecard

import (
	"strings"
	"testing"
)

func TestGradeFVT(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		want    Grade
	}{
		{"fast", 30, GradeA},
		{"moderate", 120, GradeB},
		{"slow", 400, GradeC},
		{"failing", 1000, GradeF},
		{"boundary_green", 60, GradeA},
		{"boundary_yellow", 300, GradeB},
		{"boundary_red", 900, GradeC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gradeFVT(tt.seconds); got != tt.want {
				t.Errorf("gradeFVT(%v) = %v, want %v", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestGradeTEU(t *testing.T) {
	tests := []struct {
		name string
		pct  float64
		want Grade
	}{
		{"excellent", 25, GradeA},
		{"good", 15, GradeB},
		{"fair", 8, GradeC},
		{"failing", 2, GradeF},
		{"boundary_green", 20, GradeA},
		{"boundary_yellow", 10, GradeB},
		{"boundary_red", 5, GradeC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gradeTEU(tt.pct); got != tt.want {
				t.Errorf("gradeTEU(%v) = %v, want %v", tt.pct, got, tt.want)
			}
		})
	}
}

func TestGradeSAC(t *testing.T) {
	tests := []struct {
		name string
		pct  float64
		want Grade
	}{
		{"excellent", 95, GradeA},
		{"good", 80, GradeB},
		{"fair", 60, GradeC},
		{"failing", 30, GradeF},
		{"boundary_green", 90, GradeA},
		{"boundary_yellow", 70, GradeB},
		{"boundary_red", 50, GradeC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gradeSAC(tt.pct); got != tt.want {
				t.Errorf("gradeSAC(%v) = %v, want %v", tt.pct, got, tt.want)
			}
		})
	}
}

func TestOverallGrade(t *testing.T) {
	tests := []struct {
		name   string
		grades []Grade
		want   Grade
	}{
		{"all_a", []Grade{GradeA, GradeA, GradeA}, GradeA},
		{"mixed_ab", []Grade{GradeA, GradeB, GradeA}, GradeB},
		{"one_f", []Grade{GradeA, GradeB, GradeF}, GradeF},
		{"all_f", []Grade{GradeF, GradeF, GradeF}, GradeF},
		{"empty", []Grade{}, GradeF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overallGrade(tt.grades); got != tt.want {
				t.Errorf("overallGrade(%v) = %v, want %v", tt.grades, got, tt.want)
			}
		})
	}
}

func TestNewScorecard(t *testing.T) {
	s := New(45, 18, 85, "")
	if s.FirstValueTime.Grade != GradeA {
		t.Errorf("FVT grade = %v, want A", s.FirstValueTime.Grade)
	}
	if s.TokenEfficiency.Grade != GradeB {
		t.Errorf("TEU grade = %v, want B", s.TokenEfficiency.Grade)
	}
	if s.SpendAttribution.Grade != GradeB {
		t.Errorf("SAC grade = %v, want B", s.SpendAttribution.Grade)
	}
	if s.OverallGrade != GradeB {
		t.Errorf("Overall grade = %v, want B", s.OverallGrade)
	}
}

func TestScorecardString(t *testing.T) {
	s := New(30, 25, 95, "v1.0.0")
	out := s.String()
	if !strings.Contains(out, "Overall Grade: A") {
		t.Errorf("expected overall grade A in output:\n%s", out)
	}
	if !strings.Contains(out, "v1.0.0") {
		t.Errorf("expected baseline ref in output:\n%s", out)
	}
}

func TestScorecardJSON(t *testing.T) {
	s := New(60, 10, 70, "")
	data, err := s.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !strings.Contains(string(data), "first_value_time") {
		t.Errorf("JSON missing first_value_time:\n%s", data)
	}
	if !strings.Contains(string(data), "overall_grade") {
		t.Errorf("JSON missing overall_grade:\n%s", data)
	}
}
