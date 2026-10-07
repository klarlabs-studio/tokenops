// Package backfill pulls a vendor's usage history into the event store
// in one pass, so an operator who has just connected a source sees the
// days before it instead of waiting for the poller to drip in only new
// buckets.
package backfill

import (
	"context"
	"fmt"
	"time"

	anthropicusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Appender stores an envelope; *sqlite.Store is one. A duplicate is a
// no-op, which is what makes a backfill safe to repeat or to run beside
// the live poller.
type Appender interface {
	Append(ctx context.Context, env *eventschema.Envelope) error
}

// AnthropicRequest is one Anthropic Admin backfill.
type AnthropicRequest struct {
	AdminKey string
	// Hours is the window back from now; the Admin API keeps 168 hours
	// of hourly buckets.
	Hours int
	// DryRun counts what would be stored without storing it.
	DryRun bool
	// BaseURL replaces api.anthropic.com in tests.
	BaseURL string
}

// AnthropicResult is what a backfill read and stored.
type AnthropicResult struct {
	Buckets  int `json:"buckets"`
	Inserted int `json:"inserted"`
	// Skipped are the zero-token rows, which carry nothing to store.
	Skipped int `json:"skipped"`
}

// Anthropic reads the Admin usage report for the last r.Hours, every page
// of it, and stores one prompt envelope per (bucket, model) row.
func Anthropic(ctx context.Context, store Appender, r AnthropicRequest, now time.Time) (AnthropicResult, error) {
	client := anthropicapi.NewAdminClient(r.AdminKey)
	if r.BaseURL != "" {
		client.BaseURL = r.BaseURL
	}
	buckets, err := anthropicusage.FetchAll(ctx, client, anthropicusage.MessagesUsageRequest{
		StartingAt:  now.Add(-time.Duration(r.Hours) * time.Hour),
		EndingAt:    now,
		BucketWidth: anthropicusage.BucketWidthHour,
		GroupBy:     []string{"model"},
	})
	if err != nil {
		return AnthropicResult{}, fmt.Errorf("fetch usage: %w", err)
	}
	out := AnthropicResult{Buckets: len(buckets)}
	for _, bucket := range buckets {
		for _, row := range bucket.Results {
			env, ok := anthropicusage.NewEnvelope(bucket.StartingAt, bucket.EndingAt, row)
			if !ok {
				out.Skipped++
				continue
			}
			if !r.DryRun {
				if err := store.Append(ctx, env); err != nil {
					return out, fmt.Errorf("append envelope %s: %w", env.ID, err)
				}
			}
			out.Inserted++
		}
	}
	return out, nil
}
