package geminicli

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type countingBus struct{ n int }

func (b *countingBus) Publish(*eventschema.Envelope) { b.n++ }
func (b *countingBus) PublishWait(context.Context, *eventschema.Envelope) error {
	b.n++
	return nil
}
func (b *countingBus) DroppedCount() int64       { return 0 }
func (b *countingBus) PublishedCount() int64     { return int64(b.n) }
func (b *countingBus) Close(time.Duration) error { return nil }
