package cli

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseEtime(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "minutes and seconds", in: "01:02", want: 62 * time.Second},
		{name: "hours", in: "1:02:03", want: time.Hour + 2*time.Minute + 3*time.Second},
		{name: "days", in: "2-03:04:05", want: 2*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second},
		{name: "zero", in: "00:00", want: 0},
		{name: "not a number", in: "ab:cd", wantErr: true},
		{name: "too many fields", in: "1:2:3:4", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEtime(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseEtime(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEtime(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseEtime(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBrewStuckWarnings(t *testing.T) {
	const extract = "/usr/bin/ruby /opt/homebrew/Library/Homebrew/sandbox_operation.rb extract --bottle x"
	tests := []struct {
		name  string
		procs []brewProcess
		want  int
	}{
		{name: "no processes", want: 0},
		{name: "extract under the threshold", procs: []brewProcess{{Command: extract, Elapsed: time.Minute}}, want: 0},
		{name: "extract exactly at the threshold", procs: []brewProcess{{Command: extract, Elapsed: 2 * time.Minute}}, want: 0},
		{name: "extract past the threshold", procs: []brewProcess{{Command: extract, Elapsed: 2*time.Minute + time.Second}}, want: 1},
		{name: "long-running unrelated process", procs: []brewProcess{{Command: "/usr/bin/ruby brew.rb install", Elapsed: time.Hour}}, want: 0},
		{name: "one stuck extract among others", procs: []brewProcess{
			{Command: "/bin/sleep 9000", Elapsed: time.Hour},
			{Command: extract, Elapsed: 5 * time.Minute},
		}, want: 1},
		{name: "two stuck extracts warn once", procs: []brewProcess{
			{Command: extract, Elapsed: 5 * time.Minute},
			{Command: extract, Elapsed: 6 * time.Minute},
		}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := func(context.Context) ([]brewProcess, error) { return tt.procs, nil }
			got := brewStuckWarnings(context.Background(), list)
			if len(got) != tt.want {
				t.Fatalf("brewStuckWarnings() = %q, want %d warning(s)", got, tt.want)
			}
			if tt.want == 1 && got[0] != brewStuckMessage {
				t.Fatalf("warning = %q, want %q", got[0], brewStuckMessage)
			}
		})
	}
}

func TestBrewStuckWarningsDegradesOnListError(t *testing.T) {
	list := func(context.Context) ([]brewProcess, error) { return nil, errors.New("ps: not permitted") }
	if got := brewStuckWarnings(context.Background(), list); len(got) != 0 {
		t.Fatalf("brewStuckWarnings() on list error = %q, want none", got)
	}
}

func TestParsePsLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want brewProcess
		ok   bool
	}{
		{name: "padded etime", in: "   02:15 /usr/bin/ruby sandbox_operation.rb extract", want: brewProcess{Command: "/usr/bin/ruby sandbox_operation.rb extract", Elapsed: 135 * time.Second}, ok: true},
		{name: "blank line", in: "   ", ok: false},
		{name: "bad etime", in: "xx:yy /bin/true", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePsLine(tt.in)
			if ok != tt.ok {
				t.Fatalf("parsePsLine(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("parsePsLine(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
