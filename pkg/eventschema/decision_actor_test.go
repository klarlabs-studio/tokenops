package eventschema

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	eventschemav1 "go.klarlabs.de/tokenops/pkg/eventschema/proto/v1"
)

func TestGoldenBoundaryDecisionCarriesActorAndApproval(t *testing.T) {
	env := Envelope{
		ID: fixedID, SchemaVersion: SchemaVersion, Type: EventTypeDecision,
		Timestamp: fixedTime, Source: "boundary",
		Correlation: Correlation{Decision: "bnd-0123456789ab"},
		Payload: &DecisionEvent{
			Kind: "boundary", Stage: DecisionStageAccepted,
			Policy: "boundary:session-spend", Authority: "require_approval",
			Rationale: "session cost 23.10 USD crossed 20.00 USD",
			Actor:     DecisionActor{Harness: "claude-code", SessionID: "s1", AgentID: "claude-code:proj", User: "felix"},
			Approval:  DecisionApproval{Approver: "felix", Method: "cli", At: fixedTime, Rule: "session-spend", Scope: "session"},
		},
	}
	want := `{
  "id": "01HABCDEF0123456789KLMNOPQRS",
  "schema_version": "1.6.0",
  "type": "decision",
  "timestamp": "2026-05-11T12:00:00Z",
  "source": "boundary",
  "correlation": {
    "decision": "bnd-0123456789ab"
  },
  "payload": {
    "kind": "boundary",
    "stage": "accepted",
    "policy": "boundary:session-spend",
    "authority": "require_approval",
    "rationale": "session cost 23.10 USD crossed 20.00 USD",
    "executable": false,
    "actor": {
      "harness": "claude-code",
      "session_id": "s1",
      "agent_id": "claude-code:proj",
      "user": "felix"
    },
    "approval": {
      "approver": "felix",
      "method": "cli",
      "at": "2026-05-11T12:00:00Z",
      "rule": "session-spend",
      "scope": "session"
    }
  }
}`
	assertEqualGolden(t, canon(t, env), want)

	var back DecisionEvent
	b, _ := json.Marshal(env.Payload)
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Actor != env.Payload.(*DecisionEvent).Actor || !back.Approval.At.Equal(fixedTime) {
		t.Fatalf("round trip lost actor or approval: %+v", back)
	}
}

func TestDecisionWithoutActorOmitsIt(t *testing.T) {
	b, err := json.Marshal(&DecisionEvent{Kind: "model_route", Stage: DecisionStageShadow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "actor") || strings.Contains(string(b), "approval") {
		t.Fatalf("zero actor/approval serialised: %s", b)
	}
}

// The .proto is the contract for other languages; the Go types are
// hand-written. Each control message's fields must agree by name.
func TestDecisionProtoMatchesGo(t *testing.T) {
	cases := []struct {
		goType reflect.Type
		msg    protoreflect.MessageDescriptor
	}{
		{reflect.TypeFor[DecisionEvent](), (&eventschemav1.DecisionEvent{}).ProtoReflect().Descriptor()},
		{reflect.TypeFor[DecisionActor](), (&eventschemav1.DecisionActor{}).ProtoReflect().Descriptor()},
		{reflect.TypeFor[DecisionApproval](), (&eventschemav1.DecisionApproval{}).ProtoReflect().Descriptor()},
	}
	for _, tc := range cases {
		var goNames, protoNames []string
		for f := range tc.goType.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			goNames = append(goNames, name)
		}
		fields := tc.msg.Fields()
		for i := range fields.Len() {
			protoNames = append(protoNames, string(fields.Get(i).Name()))
		}
		sort.Strings(goNames)
		sort.Strings(protoNames)
		if !reflect.DeepEqual(goNames, protoNames) {
			t.Errorf("%s: Go fields %v; proto fields %v", tc.goType.Name(), goNames, protoNames)
		}
	}
}
