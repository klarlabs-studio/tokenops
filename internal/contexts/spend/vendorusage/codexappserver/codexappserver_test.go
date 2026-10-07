package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// pipeConn is the client's end of a fake app server.
type pipeConn struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (p pipeConn) Close() error {
	for _, c := range p.closers {
		_ = c.Close()
	}
	return nil
}

// fakeServer answers like `codex app-server`: a notification before each
// answer, so the client must skip what is not its response. It records
// the methods it was asked.
func fakeServer(t *testing.T, rateLimits string) (Dial, *[]string) {
	t.Helper()
	var (
		mu      sync.Mutex
		methods []string
	)
	return func(context.Context) (Conn, error) {
		toServer, fromClient := io.Pipe()
		toClient, fromServer := io.Pipe()
		// Like an OS pipe, the server's output is buffered: a real app
		// server can write a notification while the client is still
		// writing its next request, and an unbuffered io.Pipe would
		// deadlock where the process does not.
		out := make(chan string, 16)
		go func() {
			defer func() { _ = fromServer.Close() }()
			for line := range out {
				if _, err := io.WriteString(fromServer, line); err != nil {
					return
				}
			}
		}()
		go func() {
			defer close(out)
			sc := bufio.NewScanner(toServer)
			for sc.Scan() {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.Unmarshal(sc.Bytes(), &req)
				mu.Lock()
				methods = append(methods, req.Method)
				mu.Unlock()
				out <- `{"method":"account/updated","params":{}}` + "\n"
				switch req.Method {
				case "initialize":
					out <- `{"id":1,"result":{"userAgent":"codex"}}` + "\n"
				case "account/rateLimits/read":
					out <- `{"id":2,` + rateLimits + `}` + "\n"
				}
			}
		}()
		return pipeConn{Reader: toClient, Writer: fromClient, closers: []io.Closer{fromClient, toClient, toServer}}, nil
	}, &methods
}

const proLite = `"result":{"rateLimits":{"planType":"prolite","primary":{"usedPercent":100,"windowDurationMins":10080,"resetsAt":1791607287},"secondary":null,"rateLimitReachedType":"rate_limit_reached"},"rateLimitsByLimitId":{"codex":{}}}`

// The client initializes, says initialized, then reads the rate limits,
// skipping notifications; the shape is what codex-cli 0.156 returned.
func TestRead(t *testing.T) {
	dial, methods := fakeServer(t, proLite)
	s, err := Read(context.Background(), dial)
	if err != nil || s.PlanType != "prolite" || s.Primary == nil || s.Primary.UsedPercent != 100 || s.Primary.WindowDurationMins != 10080 || s.Secondary != nil {
		t.Fatalf("read = %+v, %v", s, err)
	}
	if got := strings.Join(*methods, ","); got != "initialize,initialized,account/rateLimits/read" {
		t.Errorf("methods %s", got)
	}
	dial, _ = fakeServer(t, `"error":{"message":"not logged in"}`)
	if _, err := Read(context.Background(), dial); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error answer: %v", err)
	}
	dial, _ = fakeServer(t, `"result":{"rateLimits":{"planType":null,"primary":null,"secondary":null}}`)
	if _, err := Read(context.Background(), dial); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("no plan: %v", err)
	}
}

// A reading is in the rollouts' attribute shape, carries no model and no
// tokens, and is stored once per change.
func TestPoller(t *testing.T) {
	at := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)
	env := Envelope(Snapshot{PlanType: "prolite", Primary: &Window{UsedPercent: 100, WindowDurationMins: 10080, ResetsAt: 1791607287}}, at)
	a := env.Attributes
	if a["primary_used_pct"] != "100.00" || a["primary_window_min"] != "10080" || a["primary_resets_at"] != "1791607287" ||
		a["granularity"] != "quota_snapshot" || env.Source != SourceTag {
		t.Errorf("envelope %+v", a)
	}
	if pe, ok := env.Payload.(*eventschema.PromptEvent); !ok || pe.RequestModel != "" || pe.TotalTokens != 0 || pe.Provider != eventschema.ProviderOpenAI {
		t.Errorf("payload %+v", env.Payload)
	}
	dial, _ := fakeServer(t, proLite)
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Dial: dial, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	p.Scan(context.Background())
	if len(bus.got) != 1 {
		t.Errorf("stored %d readings of one unchanged snapshot", len(bus.got))
	}
}

type recordingBus struct {
	mu  sync.Mutex
	got []*eventschema.Envelope
}

func (b *recordingBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, env)
}

func (b *recordingBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
func (b *recordingBus) DroppedCount() int64       { return 0 }
func (b *recordingBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *recordingBus) Close(time.Duration) error { return nil }
