// Package tasklog keeps the operator's task markers: a description and a
// span, started and finished by hand, and what the work in that span cost
// according to the event store. `tokenops task` reads and writes it.
package tasklog

import (
	"context"
	"fmt"

	"go.klarlabs.de/tokenops/internal/contexts/tasks"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Task is one marked task.
type Task = tasks.Task

// Metrics is what a task's span cost: tokens, turns, money and the time
// to its first useful output.
type Metrics = tasks.Metrics

// Start opens a task in the log at path (empty: ~/.tokenops/tasks.jsonl).
func Start(path, description, sessionID string) (Task, error) {
	return tasks.Start(path, description, sessionID, nil)
}

// Done closes the open task in the log at path.
func Done(path string) (Task, error) { return tasks.Done(path, nil) }

// List is every task in the log at path, oldest first.
func List(path string) ([]Task, error) { return tasks.List(path) }

// MetricsFor rolls up each task's span from store, keyed by task ID.
func MetricsFor(ctx context.Context, store *sqlite.Store, all []Task) (map[string]Metrics, error) {
	out := make(map[string]Metrics, len(all))
	for _, t := range all {
		m, err := tasks.MetricsFor(ctx, store, t, nil)
		if err != nil {
			return nil, fmt.Errorf("metrics for %s: %w", t.ID, err)
		}
		out[t.ID] = m
	}
	return out, nil
}
