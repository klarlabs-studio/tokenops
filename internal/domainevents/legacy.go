// Package domainevents contains read-only compatibility code for importing
// pre-envelope domain-event JSONL files. Live events use pkg/eventschema.
package domainevents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	KindWorkflowStarted     = "workflow.started"
	KindWorkflowObserved    = "workflow.observed"
	KindWorkflowProgressed  = "workflow.progressed"
	KindWorkflowCompleted   = "workflow.completed"
	KindWorkflowFailed      = "workflow.failed"
	KindOptimizationApplied = "optimization.applied"
	KindRuleCorpusReloaded  = "rule_corpus.reloaded"
	KindBudgetExceeded      = "budget.exceeded"
)

// Record is one legacy JSONL event. It is retained solely for migration reads.
type Record struct {
	Kind    string          `json:"kind"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// Replay reads legacy records and stops on malformed JSON or a callback error.
// A missing file is treated as an empty history.
func Replay(path string, fn func(Record) error) error {
	_, err := replay(path, fn, false)
	return err
}

// ReplayLenient skips malformed JSON lines and reports their count.
func ReplayLenient(path string, fn func(Record) error) (int, error) {
	return replay(path, fn, true)
}

func replay(path string, fn func(Record) error, lenient bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	skipped := 0
	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			if lenient {
				skipped++
				continue
			}
			return skipped, fmt.Errorf("domainevents: decode record: %w", err)
		}
		if err := fn(rec); err != nil {
			return skipped, err
		}
	}
	return skipped, scanner.Err()
}
