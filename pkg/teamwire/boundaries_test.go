package teamwire

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// approvalStrings is every string an ApprovalRequest may carry, with the
// rule that keeps free text out. A string field not listed here fails
// TestApprovalHasNoFreeText: a request leaves a member's machine, so a new
// string is a change to the privacy boundary (ADR 0014 §4).
var approvalStrings = map[string]string{
	"ApprovalRequest.DecisionID": "ValidDecisionID",
	"ApprovalRequest.Rule":       "boundaries.ValidName: the organisation's own rule name",
	"ApprovalRequest.Action":     "enumeration",
	"ApprovalRequest.Harness":    "enumeration",
	"Crossing.Field":             "enumeration",
	"Crossing.Op":                "enumeration",
	"Crossing.Window":            "enumeration",
	"Crossing.Destination":       "enumeration",
}

func TestApprovalHasNoFreeText(t *testing.T) {
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			name := typ.Name() + "." + f.Name
			ft := f.Type
			switch {
			case numericKinds[ft.Kind()]:
			case ft.Kind() == reflect.String:
				if _, ok := approvalStrings[name]; !ok {
					t.Errorf("%s is a string with no rule against free text; an approval request leaves "+
						"the member's machine (ADR 0014 §4). Constrain it and list it in approvalStrings", name)
				}
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
				walk(ft.Elem())
			default:
				t.Errorf("%s has kind %s, which can carry arbitrary content", name, ft.Kind())
			}
		}
	}
	walk(reflect.TypeOf(ApprovalRequest{}))
}

func validRequest() ApprovalRequest {
	return ApprovalRequest{
		DecisionID: "bnd-0123456789ab", Rule: "big-subagents", Version: 3, Action: "subagent_model", Harness: "claude-code",
		Crossings: []Crossing{{Field: "model_tier", Op: ">", Value: 3, Threshold: 2}},
	}
}

func TestApprovalRequestValidate(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ApprovalRequest){
		"decision id": func(a *ApprovalRequest) { a.DecisionID = "fix the login bug" },
		"rule":        func(a *ApprovalRequest) { a.Rule = "Fix The Login Bug" },
		"version":     func(a *ApprovalRequest) { a.Version = 0 },
		"action":      func(a *ApprovalRequest) { a.Action = "deploy" },
		"harness":     func(a *ApprovalRequest) { a.Harness = "/Users/alice" },
		"field":       func(a *ApprovalRequest) { a.Crossings[0].Field = "prompt" },
		"window":      func(a *ApprovalRequest) { a.Crossings[0].Window = "secret-project" },
		"destination": func(a *ApprovalRequest) { a.Crossings[0].Destination = "slack" },
		"nan":         func(a *ApprovalRequest) { a.Crossings[0].Value = nanValue() },
		"too many":    func(a *ApprovalRequest) { a.Crossings = make([]Crossing, 9) },
	}
	for name, mutate := range cases {
		a := validRequest()
		mutate(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, a)
		}
	}
}

func nanValue() float64 { var z float64; return z / z }

func TestValidateBoundariesNeedsTeamRoles(t *testing.T) {
	ok := []BoundaryRule{{Name: "big", Action: "subagent_model", When: map[string]string{"model_tier": "> sonnet"}, Require: "approver:lead", Stage: "enforce"}}
	if err := ValidateBoundaries(ok); err != nil {
		t.Fatal(err)
	}
	bad := []BoundaryRule{{Name: "big", Action: "continue", Require: "approver:cfo"}}
	if err := ValidateBoundaries(bad); err == nil || !strings.Contains(err.Error(), "not a role") {
		t.Fatalf("err = %v", err)
	}
	if err := ValidateBoundaries([]BoundaryRule{{Name: "x", Action: "deploy", Require: "ask"}}); err == nil {
		t.Fatal("an unknown action was accepted")
	}
}

func TestSignedBoundariesVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	doc := OrgBoundaries{OrgID: "o1", Version: 2, UpdatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC),
		Rules: []BoundaryRule{{Name: "floor", Action: "continue", Require: "deny", Stage: "enforce", When: map[string]string{"plan_left_pct": "< 5"}}}}
	s, err := SignBoundaries(doc, priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(nil)
	if err != nil || got.Version != 2 || got.Rules[0].Name != "floor" {
		t.Fatalf("first fetch = %+v, %v", got, err)
	}
	if _, err := s.Verify(pub); err != nil {
		t.Fatalf("pinned = %v", err)
	}
	tampered := s
	tampered.Document = []byte(strings.Replace(string(s.Document), `"deny"`, `"allow"`, 1))
	if _, err := tampered.Verify(pub); err == nil {
		t.Fatal("an edited document verified")
	}
	other, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = other
	resigned, _ := SignBoundaries(doc, otherPriv)
	if _, err := resigned.Verify(pub); !errors.Is(err, ErrBoundaryKeyChanged) {
		t.Fatalf("another key = %v", err)
	}
	rules, err := got.Parsed()
	if err != nil || rules[0].Name != "org:floor" || !rules[0].Enforced() {
		t.Fatalf("parsed = %+v, %v", rules, err)
	}
}

func TestBoundaryJSONNames(t *testing.T) {
	want := map[string][]string{
		"ApprovalRequest":  {"action", "crossings", "decision_id", "harness", "rule", "version"},
		"SignedBoundaries": {"document", "key", "signature"},
	}
	got := map[string][]string{
		"ApprovalRequest":  jsonKeys(t, validRequest()),
		"SignedBoundaries": jsonKeys(t, SignedBoundaries{}),
	}
	for k, w := range want {
		if strings.Join(got[k], ",") != strings.Join(w, ",") {
			t.Errorf("%s keys = %v; want %v", k, got[k], w)
		}
	}
}

// An organisation loop breaker's crossing is a count of calls: it may
// travel, as figures only.
func TestALoopCrossingIsAFigure(t *testing.T) {
	a := validRequest()
	a.Action = "continue"
	a.Crossings = []Crossing{{Field: "loop_calls", Op: ">=", Value: 6, Threshold: 4}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOrganisationRulesMayNameAFolderAndHours(t *testing.T) {
	rules := []BoundaryRule{{
		Name: "client-hours", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"folder": "/srv/clients/acme", "hours": "mon-fri 09:00-18:00"},
	}}
	if err := ValidateBoundaries(rules); err != nil {
		t.Fatal(err)
	}
	parsed, err := OrgBoundaries{Version: 1, Rules: rules}.Parsed()
	if err != nil || len(parsed) != 1 || len(parsed[0].When) != 2 {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	bad := []BoundaryRule{{Name: "x", Action: "continue", Require: "ask", When: map[string]string{"folder": "clients/acme"}}}
	if err := ValidateBoundaries(bad); err == nil {
		t.Fatal("a relative folder was accepted")
	}
}
