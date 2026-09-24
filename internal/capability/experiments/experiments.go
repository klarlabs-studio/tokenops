// Package experiments manages bounded, explicitly enrolled local trials.
package experiments

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const (
	defaultPairs    = 10
	maxPairs        = 10
	defaultDuration = 14 * 24 * time.Hour
)

// Ledger is the append-only storage port required by the manager.
type Ledger interface {
	Append(context.Context, *eventschema.Envelope) error
	ExperimentEvents(context.Context, string) ([]*eventschema.Envelope, error)
}

// Manager serialises assignment within one process and persists every step.
type Manager struct {
	ledger Ledger
	mu     sync.Mutex
}

// New constructs a manager. A nil ledger disables experiments safely.
func New(ledger Ledger) *Manager { return &Manager{ledger: ledger} }

// StartInput defines one same-provider, downward model trial.
type StartInput struct {
	Provider, BaselineModel, VariantModel string
	Fingerprint                           string
	At                                    time.Time
	Duration                              time.Duration
	MaxPairs                              int
}

// Start explicitly enrolls a bounded trial.
func (m *Manager) Start(ctx context.Context, in StartInput) (State, error) {
	if m == nil || m.ledger == nil {
		return State{}, errors.New("experiments: storage is disabled")
	}
	if in.Provider == "" || in.BaselineModel == "" || in.VariantModel == "" {
		return State{}, errors.New("experiments: provider, baseline model and variant model are required")
	}
	if in.BaselineModel == in.VariantModel {
		return State{}, errors.New("experiments: baseline and variant models must differ")
	}
	pairs := in.MaxPairs
	if pairs == 0 {
		pairs = defaultPairs
	}
	if pairs < 1 || pairs > maxPairs {
		return State{}, fmt.Errorf("experiments: max_pairs must be between 1 and %d", maxPairs)
	}
	duration := in.Duration
	if duration == 0 {
		duration = defaultDuration
	}
	if duration <= 0 || duration > defaultDuration {
		return State{}, errors.New("experiments: duration must be positive and no more than 14 days")
	}
	at := utcNow(in.At)
	id := "experiment:" + uuid.NewString()
	ev := experimentEnvelope(id, "", at, &eventschema.ExperimentEvent{
		Stage: eventschema.ExperimentStarted, Kind: "model_route",
		Baseline: eventschema.ResourceOption{Provider: in.Provider, Model: in.BaselineModel},
		Variant:  eventschema.ResourceOption{Provider: in.Provider, Model: in.VariantModel},
		MaxPairs: pairs, EndsAt: at.Add(duration), Fingerprint: in.Fingerprint,
	})
	if err := m.ledger.Append(ctx, ev); err != nil {
		return State{}, err
	}
	return State{ID: id, Stage: eventschema.ExperimentStarted, Baseline: ev.Payload.(*eventschema.ExperimentEvent).Baseline, Variant: ev.Payload.(*eventschema.ExperimentEvent).Variant, MaxPairs: pairs, EndsAt: at.Add(duration), Fingerprint: in.Fingerprint}, nil
}

// AssignmentInput describes an eligible proxy route.
type AssignmentInput struct {
	Provider, BaselineModel, VariantModel string
	ExecutionID, Fingerprint              string
	At                                    time.Time
}

// Assignment is the arm chosen for one execution.
type Assignment struct {
	ExperimentID string
	Pair         int
	Variant      bool
}

// Assign chooses the next arm in a deterministically randomised pair.
func (m *Manager) Assign(ctx context.Context, in AssignmentInput) (Assignment, bool, error) {
	if m == nil || m.ledger == nil {
		return Assignment{}, false, nil
	}
	// Randomising a request without an execution key creates an assignment
	// that cannot be joined to the outcome used to evaluate it. Stay on the
	// ordinary policy path rather than persisting untestable experiment data.
	if in.ExecutionID == "" || len(in.ExecutionID) > 256 {
		return Assignment{}, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	events, err := m.ledger.ExperimentEvents(ctx, "")
	if err != nil {
		return Assignment{}, false, err
	}
	at := utcNow(in.At)
	for _, state := range Active(events, at) {
		if state.Baseline.Provider != in.Provider || state.Baseline.Model != in.BaselineModel || state.Variant.Model != in.VariantModel {
			continue
		}
		if state.Fingerprint != "" && state.Fingerprint != in.Fingerprint {
			continue
		}
		// One harness execution must stay in one arm across all proxy
		// requests. Reuse its first durable assignment instead of counting
		// every request as a new randomized sample.
		for _, env := range events {
			if env == nil || env.Correlation.Experiment != state.ID || env.Association.Execution != in.ExecutionID {
				continue
			}
			p, ok := env.Payload.(*eventschema.ExperimentEvent)
			if !ok || p.Stage != eventschema.ExperimentAssigned {
				continue
			}
			return Assignment{
				ExperimentID: state.ID,
				Pair:         p.Pair,
				Variant:      p.Assignment == "variant",
			}, true, nil
		}
		used := state.Assignments
		if used >= state.MaxPairs*2 {
			continue
		}
		pair, position := used/2+1, used%2
		firstVariant := randomizedFirst(state.ID, pair)
		variant := firstVariant
		if position == 1 {
			variant = !firstVariant
		}
		arm := "baseline"
		if variant {
			arm = "variant"
		}
		ev := experimentEnvelope(state.ID, in.ExecutionID, at, &eventschema.ExperimentEvent{
			Stage: eventschema.ExperimentAssigned, Kind: "model_route", Assignment: arm,
			Pair: pair, Baseline: state.Baseline, Variant: state.Variant,
			MaxPairs: state.MaxPairs, EndsAt: state.EndsAt, Fingerprint: state.Fingerprint,
		})
		if err := m.ledger.Append(ctx, ev); err != nil {
			return Assignment{}, false, err
		}
		return Assignment{ExperimentID: state.ID, Pair: pair, Variant: variant}, true, nil
	}
	return Assignment{}, false, nil
}

// Stop ends a trial without deleting its evidence.
func (m *Manager) Stop(ctx context.Context, id, reason string, at time.Time) error {
	if m == nil || m.ledger == nil {
		return errors.New("experiments: storage is disabled")
	}
	state, ok, err := m.Status(ctx, id, at)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("experiments: experiment not found")
	}
	return m.ledger.Append(ctx, experimentEnvelope(id, "", utcNow(at), &eventschema.ExperimentEvent{
		Stage: eventschema.ExperimentStopped, Kind: "model_route", Reason: reason,
		Baseline: state.Baseline, Variant: state.Variant, MaxPairs: state.MaxPairs,
		EndsAt: state.EndsAt, Fingerprint: state.Fingerprint,
	}))
}

// State is a folded experiment ledger.
type State struct {
	ID          string                      `json:"id"`
	Stage       eventschema.ExperimentStage `json:"stage"`
	Baseline    eventschema.ResourceOption  `json:"baseline"`
	Variant     eventschema.ResourceOption  `json:"variant"`
	MaxPairs    int                         `json:"max_pairs"`
	Assignments int                         `json:"assignments"`
	EndsAt      time.Time                   `json:"ends_at"`
	Fingerprint string                      `json:"fingerprint,omitempty"`
	Reason      string                      `json:"reason,omitempty"`
}

// Status folds one experiment.
func (m *Manager) Status(ctx context.Context, id string, at time.Time) (State, bool, error) {
	if m == nil || m.ledger == nil {
		return State{}, false, errors.New("experiments: storage is disabled")
	}
	events, err := m.ledger.ExperimentEvents(ctx, id)
	if err != nil {
		return State{}, false, err
	}
	states := Fold(events, utcNow(at))
	state, ok := states[id]
	return state, ok, nil
}

// Events returns one trial's canonical history for verification and learning.
func (m *Manager) Events(ctx context.Context, id string) ([]*eventschema.Envelope, error) {
	if m == nil || m.ledger == nil {
		return nil, errors.New("experiments: storage is disabled")
	}
	return m.ledger.ExperimentEvents(ctx, id)
}

// States returns every persisted experiment folded to its current state.
func (m *Manager) States(ctx context.Context, at time.Time) ([]State, error) {
	if m == nil || m.ledger == nil {
		return nil, errors.New("experiments: storage is disabled")
	}
	events, err := m.ledger.ExperimentEvents(ctx, "")
	if err != nil {
		return nil, err
	}
	states := Fold(events, utcNow(at))
	out := make([]State, 0, len(states))
	for _, state := range states {
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndsAt.After(out[j].EndsAt) })
	return out, nil
}

// Active returns active trials oldest first.
func Active(events []*eventschema.Envelope, at time.Time) []State {
	states := Fold(events, at)
	out := make([]State, 0, len(states))
	for _, s := range states {
		if s.Stage == eventschema.ExperimentStarted || s.Stage == eventschema.ExperimentAssigned {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EndsAt.Before(out[j].EndsAt) })
	return out
}

// Fold reconstructs current state from append-only events.
func Fold(events []*eventschema.Envelope, at time.Time) map[string]State {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	out := map[string]State{}
	for _, env := range events {
		if env == nil || env.Correlation.Experiment == "" {
			continue
		}
		p, ok := env.Payload.(*eventschema.ExperimentEvent)
		if !ok {
			continue
		}
		s := out[env.Correlation.Experiment]
		s.ID, s.Stage = env.Correlation.Experiment, p.Stage
		if p.Baseline.Model != "" {
			s.Baseline, s.Variant = p.Baseline, p.Variant
			s.MaxPairs, s.EndsAt, s.Fingerprint = p.MaxPairs, p.EndsAt, p.Fingerprint
		}
		if p.Stage == eventschema.ExperimentAssigned {
			s.Assignments++
		}
		s.Reason = p.Reason
		out[s.ID] = s
	}
	for id, s := range out {
		if (s.Stage == eventschema.ExperimentStarted || s.Stage == eventschema.ExperimentAssigned) && (!s.EndsAt.After(at) || s.Assignments >= s.MaxPairs*2) {
			s.Stage = eventschema.ExperimentCompleted
			out[id] = s
		}
	}
	return out
}

func experimentEnvelope(id, execution string, at time.Time, payload *eventschema.ExperimentEvent) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeExperiment, Timestamp: at, Source: "experiment",
		Association: eventschema.Association{Execution: execution},
		Correlation: eventschema.Correlation{Experiment: id}, Payload: payload,
	}
}

func randomizedFirst(id string, pair int) bool {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", id, pair)))
	return sum[0]&1 == 1
}

func utcNow(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now().UTC()
	}
	return at.UTC()
}
