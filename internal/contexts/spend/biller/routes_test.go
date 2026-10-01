package biller

import (
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

const fw = "https://api.fireworks.ai/inference"

func TestRoutesAt(t *testing.T) {
	r := Routes{
		{Harness: "claude-code", BaseURL: "", From: time.Time{}},
		{Harness: "claude-code", BaseURL: fw, From: day(10)},
		{Harness: "codex", BaseURL: "x", From: day(1)},
	}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{day(5), ""}, {day(10), fw}, {day(20), fw}} {
		if got, _ := r.At("claude-code", tc.at); got != tc.want {
			t.Errorf("At(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
	// Before the first record the first route applies.
	if got, ok := (Routes{{Harness: "claude-code", BaseURL: fw, From: day(10)}}).At("claude-code", day(1)); !ok || got != fw {
		t.Errorf("before first: %q %v", got, ok)
	}
	if _, ok := r.At("opencode", day(1)); ok {
		t.Error("unknown harness reported a route")
	}
	st := r.Stretches("claude-code", day(30))
	if len(st) != 2 || !st[0].From.IsZero() || !st[0].To.Equal(day(10)) || st[1].BaseURL != fw || !st[1].To.Equal(day(30)) {
		t.Errorf("stretches %+v", st)
	}
}

func TestEndpointNameAndPlanApplies(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"", "anthropic"},
		{"https://api.anthropic.com", "anthropic"},
		{"http://127.0.0.1:7878/anthropic", "anthropic"},
		{fw, "fireworks"},
		{"https://llm.corp.example/anthropic", EndpointGateway},
	} {
		if got := EndpointName(tc.url, "anthropic"); got != tc.want {
			t.Errorf("EndpointName(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
	if !PlanApplies("anthropic", "anthropic") || !PlanApplies("anthropic", "") {
		t.Error("the vendor's own endpoint is covered by its plan")
	}
	if PlanApplies("anthropic", "fireworks") {
		t.Error("a Claude turn through FireRouter runs on an API key, not the plan")
	}
}
