package coachhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockLine is the line Claude Code writes for one content block of an
// assistant message. Every line of a message repeats the message's id and
// its whole usage; only the timestamp moves on.
func blockLine(at, id string, cacheRead int64) string {
	return `{"type":"assistant","timestamp":"` + at + `","message":{"id":"` + id + `","model":"` + opus +
		`","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":` +
		itoa(cacheRead) + `}}}`
}

// resultLine is a tool result of n bytes: a line that carries no usage.
func resultLine(at string, n int) string {
	return `{"type":"user","timestamp":"` + at + `","message":{"role":"user","content":"` + strings.Repeat("x", n) + `"}}`
}

// pastTheTail is a tool result longer than the tail a Stop used to read.
var pastTheTail = int(tailBytes) + 1024

// A message of three content blocks is three transcript lines with the
// same usage. It was spent once.
func TestEvaluate_AMessageCountsOnceHoweverManyLines(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	tp := writeTranscript(t, dir,
		blockLine(ts(1), "msg_a", 20_000_000), // $10
		blockLine(ts(2), "msg_a", 20_000_000),
		blockLine(ts(3), "msg_a", 20_000_000),
		blockLine(ts(4), "msg_b", 20_000_000), // $10
		blockLine(ts(5), "msg_b", 20_000_000),
	)
	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 20) {
		t.Fatalf("two messages of $10 each: want $20, got %.4f", d.CumulativeUSD)
	}
	if d = Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 20) {
		t.Fatalf("a repeated Stop counted again: %.4f", d.CumulativeUSD)
	}
}

// A Stop can fall while a message is still being written. The lines that
// follow have later timestamps than the one already counted, and they may
// follow more than a tail's worth of tool output.
func TestEvaluate_AMessageFinishedAfterAStopIsNotCountedAgain(t *testing.T) {
	tests := []struct {
		name string
		gap  int
	}{
		{"its next line follows at once", 0},
		{"a long tool result comes between its lines", pastTheTail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := DefaultConfig()
			// An earlier message, so the one being written is not where
			// the file starts.
			before := blockLine(ts(0), "msg_0", 20_000_000) // $10
			first := blockLine(ts(1), "msg_a", 20_000_000)  // $10
			tp := writeTranscript(t, dir, before, first)
			if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 20) {
				t.Fatalf("want $20, got %.4f", d.CumulativeUSD)
			}

			lines := []string{before, first}
			if tc.gap > 0 {
				lines = append(lines, resultLine(ts(2), tc.gap))
			}
			lines = append(lines, blockLine(ts(3), "msg_a", 20_000_000), blockLine(ts(4), "msg_a", 20_000_000))
			rewrite(t, tp, lines...)
			if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 20) {
				t.Fatalf("the rest of a counted message was counted again: want $20, got %.4f", d.CumulativeUSD)
			}

			rewrite(t, tp, append(lines, blockLine(ts(5), "msg_b", 20_000_000))...)
			if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 30) {
				t.Fatalf("the next message: want $30, got %.4f", d.CumulativeUSD)
			}
		})
	}
}

// One instruction can run for many turns before the next Stop, and their
// tool output is far more than the tail a Stop used to read. Every turn
// since the last Stop was spent.
func TestEvaluate_CountsEveryMessageSinceTheLastStop(t *testing.T) {
	a := blockLine(ts(1), "msg_a", 20_000_000) // $10 each
	rest := []string{
		resultLine(ts(2), pastTheTail),
		blockLine(ts(3), "msg_b", 20_000_000),
		resultLine(ts(4), pastTheTail),
		blockLine(ts(5), "msg_c", 20_000_000),
	}
	t.Run("the first Stop of a session", func(t *testing.T) {
		dir := t.TempDir()
		tp := writeTranscript(t, dir, append([]string{a}, rest...)...)
		if d := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow); !approx(d.CumulativeUSD, 30) {
			t.Fatalf("three messages of $10: want $30, got %.4f", d.CumulativeUSD)
		}
	})
	t.Run("between two Stops", func(t *testing.T) {
		dir := t.TempDir()
		cfg := DefaultConfig()
		tp := writeTranscript(t, dir, a)
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 10) {
			t.Fatalf("want $10, got %.4f", d.CumulativeUSD)
		}
		rewrite(t, tp, append([]string{a}, rest...)...)
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 30) {
			t.Fatalf("three messages of $10: want $30, got %.4f", d.CumulativeUSD)
		}
		if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 30) {
			t.Fatalf("a repeated Stop counted again: %.4f", d.CumulativeUSD)
		}
	})
}

// A subagent's transcript is written the same way as the session's.
func TestEvaluate_SubagentMessagesCountOnceAndAllOfThem(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	tp := writeTranscript(t, dir, blockLine(ts(1), "msg_a", 20_000_000)) // $10
	sub := filepath.Join(dir, "transcript", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(sub, "agent-a1.jsonl")
	first := blockLine(ts(2), "msg_s1", 20_000_000) // $10
	rewrite(t, agent, first)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 20) {
		t.Fatalf("want $20, got %.4f", d.CumulativeUSD)
	}
	rewrite(t, agent,
		first,
		resultLine(ts(3), pastTheTail),
		blockLine(ts(4), "msg_s1", 20_000_000),
		blockLine(ts(5), "msg_s2", 20_000_000), // $10
		resultLine(ts(6), pastTheTail),
		blockLine(ts(7), "msg_s3", 20_000_000), // $10
		blockLine(ts(8), "msg_s3", 20_000_000),
	)
	if d := Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 40) {
		t.Fatalf("the session's $10 and the subagent's three messages: want $40, got %.4f", d.CumulativeUSD)
	}
}

// The compact tip waits for a number of turns above the limit. A turn is
// a message, not one of its lines.
func TestAccumulate_ATurnAboveTheLimitIsAMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CompactAtTokens = 1000
	tp := writeTranscript(t, t.TempDir(),
		blockLine(ts(1), "msg_a", 2000),
		blockLine(ts(2), "msg_a", 2000),
		blockLine(ts(3), "msg_a", 2000),
	)
	var st sessionState
	accumulate(tp, &st, cfg, fixedNow)
	if st.AboveTurns != 1 {
		t.Fatalf("one message above the limit: want 1 turn, got %d", st.AboveTurns)
	}
}

// writtenBy is blockLine with the session the line names as its writer.
func writtenBy(session, at, id string, cacheRead int64) string {
	return strings.Replace(blockLine(at, id, cacheRead), `{"type"`, `{"session_id":"`+session+`","type"`, 1)
}

// A resumed or forked session's transcript starts with the history of the
// session it came from. That was spent there.
func TestEvaluate_HistoryCopiedFromAnotherSessionIsNotThisSessions(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  float64
	}{
		{"copied history, then the session's own turns", []string{
			writtenBy("origin", ts(1), "msg_a", 20_000_000),
			writtenBy("origin", ts(2), "msg_b", 20_000_000),
			writtenBy("s", ts(3), "msg_c", 20_000_000),
			blockLine(ts(4), "msg_d", 20_000_000), // names no writer
		}, 20},
		{"every line names another session: one kept across /clear", []string{
			writtenBy("first", ts(1), "msg_a", 20_000_000),
			writtenBy("first", ts(2), "msg_b", 20_000_000),
		}, 20},
		{"no line names a writer", []string{
			blockLine(ts(1), "msg_a", 20_000_000),
			blockLine(ts(2), "msg_b", 20_000_000),
		}, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tp := writeTranscript(t, dir, tc.lines...)
			if d := Evaluate(dir, "s", tp, DefaultConfig(), fixedNow); !approx(d.CumulativeUSD, tc.want) {
				t.Fatalf("want $%.0f, got %.4f", tc.want, d.CumulativeUSD)
			}
		})
	}
}
