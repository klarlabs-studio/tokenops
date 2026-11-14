package spend

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Pricing a turn on the built-in cards, by a model the card keys exactly
// and by one it finds through a gateway name.
func BenchmarkComputeAt(b *testing.B) {
	eng := NewEngine(DefaultTable())
	at := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	for _, model := range []string{"claude-opus-4-7", "accounts/anthropic/models/claude-opus-4-7"} {
		p := &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: model,
			InputTokens: 12000, CachedInputTokens: 9000, OutputTokens: 800}
		b.Run(model, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := eng.ComputeAt(p, at); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
