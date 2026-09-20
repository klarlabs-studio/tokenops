package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// startProxyWithTokenizer is startProxyForObservation with the tokenizer
// under the caller's control, so a daemon configured without one can be
// exercised the way it actually runs.
func startProxyWithTokenizer(t *testing.T, upstream *httptest.Server, tk *tokenizer.Registry) (string, *captureBus) {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	openai, _ := providers.Lookup(eventschema.ProviderOpenAI)

	bus := &captureBus{}
	opts := []Option{
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{{Provider: openai, Upstream: u}}),
		WithEventBus(bus),
	}
	if tk != nil {
		opts = append(opts, WithTokenizer(tk))
	}
	srv := New("127.0.0.1:0", opts...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return "http://" + srv.Addr(), bus
}

func echoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a fairly long reply for counting"}}]}`))
	}))
	t.Cleanup(up.Close)
	return up
}

func postChat(t *testing.T, base string) {
	t.Helper()
	postChatAs(t, base, "")
}

// postChatAs sends a request carrying a session id, the way a wrapped
// agent identifies itself.
func postChatAs(t *testing.T, base, session string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		base+"/openai/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello world"}]}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set(headerSessionID, session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
}

func promptEvents(t *testing.T, envs []*eventschema.Envelope) []*eventschema.PromptEvent {
	t.Helper()
	var out []*eventschema.PromptEvent
	for _, e := range envs {
		if p, ok := e.Payload.(*eventschema.PromptEvent); ok {
			out = append(out, p)
		}
	}
	return out
}

// A daemon with no tokenizer wired emits events with zero token counts
// "by documented design". Downstream, those zeros are indistinguishable
// from a request that genuinely consumed nothing — so a misconfigured
// install reports no usage as confidently as a correct one reports real
// usage.
func TestEventsFromAProxyWithoutATokenizerSayTheyAreUncounted(t *testing.T) {
	base, bus := startProxyWithTokenizer(t, echoUpstream(t), nil)
	postChat(t, base)

	events := promptEvents(t, waitForEvent(t, bus, 1))
	if len(events) == 0 {
		t.Fatal("no prompt event")
	}
	for _, p := range events {
		if p.TokensCounted() {
			t.Errorf("an event from a tokenizer-less proxy claims counted tokens: %+v", p)
		}
		if p.TokenProvenance() != eventschema.TokenSourceUncounted {
			t.Errorf("token source = %q, want uncounted", p.TokenProvenance())
		}
	}
}

// With a tokenizer the counts are real and the event says so, which is
// what keeps the flag from being noise.
func TestEventsFromAProxyWithATokenizerAreCounted(t *testing.T) {
	base, bus := startProxyWithTokenizer(t, echoUpstream(t), tokenizer.NewRegistry())
	postChat(t, base)

	events := promptEvents(t, waitForEvent(t, bus, 1))
	if len(events) == 0 {
		t.Fatal("no prompt event")
	}
	for _, p := range events {
		if !p.TokensCounted() {
			t.Errorf("an event from a tokenizer-equipped proxy claims uncounted: %+v", p)
		}
		if p.TotalTokens == 0 {
			t.Errorf("no tokens were counted: %+v", p)
		}
	}
}
