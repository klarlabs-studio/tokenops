package plans

import "testing"

// Google renamed Google One AI Premium to Google AI Pro. The old catalog
// name keeps working for a config that holds it.
func TestGoogleAIProKeepsTheOldName(t *testing.T) {
	p, ok := Lookup("gemini-ai-pro")
	if !ok || p.Display != "Google AI Pro" {
		t.Fatalf("gemini-ai-pro = %+v, %v; want Google AI Pro", p, ok)
	}
	if _, ok := catalog["gemini-ai-premium"]; ok {
		t.Error("gemini-ai-premium must be an alias, not a canonical plan")
	}
	if modern, aliased := ResolveAlias("gemini-ai-premium"); !aliased || modern != "gemini-ai-pro" {
		t.Errorf("ResolveAlias(gemini-ai-premium) = %q, %v; want gemini-ai-pro", modern, aliased)
	}
	if old, ok := Lookup("gemini-ai-premium"); !ok || old.Name != "gemini-ai-pro" {
		t.Errorf("Lookup(gemini-ai-premium) = %+v, %v", old, ok)
	}
}

// Since 2026-06-18 Gemini CLI serves no Google AI Pro account: the plan
// names Antigravity as where its usage is read.
func TestGoogleAIProIsReadByAntigravity(t *testing.T) {
	p, _ := Lookup("gemini-ai-pro")
	if p.ReadBy != "antigravity-local" {
		t.Errorf("ReadBy = %q, want antigravity-local", p.ReadBy)
	}
}
