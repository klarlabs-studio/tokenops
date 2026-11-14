package codexjsonl

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// planLine is a token_count turn reporting planType; an empty one carries
// no rate_limits.
func planLine(i int, planType string) string {
	limits := ""
	if planType != "" {
		limits = fmt.Sprintf(`,"rate_limits":{"primary":{"used_percent":%d.0,"window_minutes":300,"resets_at":1776431887},"plan_type":%q}`, i%100, planType)
	}
	return fmt.Sprintf(`{"timestamp":"2026-04-17T08:%02d:%02d.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"output_tokens":2,"total_tokens":%d}}%s}}`,
		(i/60)%60, i%60, 10+i, 12+i, limits)
}

// randomLine is one rollout line of any kind, most of them turns.
func randomLine(rng *rand.Rand, i int) string {
	switch rng.IntN(8) {
	case 0:
		return `{"timestamp":"2026-04-17T08:44:43.195Z","type":"session_meta","payload":{"id":"s","model_provider":"openai"}}`
	case 1:
		return `{"timestamp":"2026-04-17T08:44:50.000Z","type":"turn_context","payload":{"model":"gpt-5.3-codex"}}`
	case 2:
		// A turn with no usage is skipped, its plan_type with it.
		return `{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}},"rate_limits":{"plan_type":"skipped"}}}`
	case 3:
		return planLine(i, "")
	case 4:
		return "not json"
	default:
		return planLine(i, []string{"plus", "pro", "team", "business"}[rng.IntN(4)])
	}
}

func randomLines(rng *rand.Rand, n, from int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(randomLine(rng, from+i))
		b.WriteByte('\n')
	}
	return b.String()
}

// samePlanTypes checks that p answers each of paths as a whole-file read
// does.
func samePlanTypes(t *testing.T, p *PlanTypes, paths []string, step string) {
	t.Helper()
	got := p.Last(paths)
	for _, path := range paths {
		if want := LastPlanType(path); got[path] != want {
			t.Fatalf("%s: %s: kept %q, whole-file read %q", step, filepath.Base(path), got[path], want)
		}
	}
	if len(got) != len(paths) {
		t.Fatalf("%s: answered %d rollouts, asked %d", step, len(got), len(paths))
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// bump moves a file's modification time on, so a rewrite of the same size
// in the same clock tick is still seen as a change.
func bump(t *testing.T, path string, n int) {
	t.Helper()
	at := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// TestPlanTypesMatchWholeFileReads drives plain and compressed rollouts
// through appends, unfinished lines, truncation, replacement and
// recompression, and requires PlanTypes to answer each as LastPlanType
// reads the whole file.
func TestPlanTypesMatchWholeFileReads(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 7))
			dir := t.TempDir()
			plain := []string{filepath.Join(dir, "rollout-a.jsonl"), filepath.Join(dir, "rollout-b.jsonl")}
			packed := filepath.Join(dir, "rollout-c.jsonl.zst")
			for _, path := range plain {
				appendTo(t, path, randomLines(rng, 5, 0))
			}
			compressRollout(t, packed, randomLines(rng, 5, 0))
			paths := append(append([]string(nil), plain...), packed)
			p := &PlanTypes{}
			samePlanTypes(t, p, paths, "first read")
			line := 100
			for step := range 40 {
				path := plain[rng.IntN(len(plain))]
				var did string
				switch rng.IntN(7) {
				case 0, 1:
					did = "append"
					appendTo(t, path, randomLines(rng, 1+rng.IntN(4), line))
				case 2:
					did = "unfinished line"
					appendTo(t, path, planLine(line, "pro"))
				case 3:
					did = "unfinished line, half written"
					full := planLine(line, "team")
					appendTo(t, path, full[:len(full)/2])
				case 4:
					did = "truncate"
					if err := os.WriteFile(path, []byte(randomLines(rng, 1+rng.IntN(2), line)), 0o600); err != nil {
						t.Fatal(err)
					}
				case 5:
					did = "replace"
					tmp := path + ".new"
					if err := os.WriteFile(tmp, []byte(randomLines(rng, 3+rng.IntN(5), line)), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(tmp, path); err != nil {
						t.Fatal(err)
					}
				case 6:
					did = "recompress"
					path = packed
					compressRollout(t, packed, randomLines(rng, 2+rng.IntN(5), line))
				}
				line += 10
				bump(t, path, line)
				// Each read sees the rollouts in another order, and now and
				// then without one.
				asked := append([]string(nil), paths...)
				rng.Shuffle(len(asked), func(i, j int) { asked[i], asked[j] = asked[j], asked[i] })
				if rng.IntN(5) == 0 {
					asked = asked[1:]
				}
				samePlanTypes(t, p, asked, fmt.Sprintf("step %d (%s %s)", step, did, filepath.Base(path)))
			}
		})
	}
}

// TestPlanTypesReadOnlyWhatChanged pins the saving: an unchanged rollout
// is not opened, and an appended one is read from where the last read
// stopped.
func TestPlanTypesReadOnlyWhatChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-a.jsonl")
	packed := filepath.Join(dir, "rollout-b.jsonl.zst")
	appendTo(t, path, planLine(1, "plus")+"\n"+planLine(2, "plus")+"\n")
	compressRollout(t, packed, planLine(1, "pro")+"\n")
	paths := []string{path, packed}
	p := &PlanTypes{}
	if got := p.Last(paths); got[path] != "plus" || got[packed] != "pro" {
		t.Fatalf("first read = %v", got)
	}
	if p.FilesRead != 2 {
		t.Fatalf("first read opened %d rollouts, want 2", p.FilesRead)
	}
	p.Last(paths)
	if p.FilesRead != 2 {
		t.Fatalf("an unchanged rollout was read again: %d opened", p.FilesRead)
	}
	before := p.tail.BytesParsed
	added := planLine(3, "team") + "\n"
	appendTo(t, path, added)
	bump(t, path, 1)
	if got := p.Last(paths); got[path] != "team" || got[packed] != "pro" {
		t.Fatalf("after an append = %v", got)
	}
	if p.FilesRead != 3 || p.tail.BytesParsed-before != int64(len(added)) {
		t.Fatalf("an append read %d rollouts and %d bytes, want 3 and %d", p.FilesRead, p.tail.BytesParsed-before, len(added))
	}
}

func BenchmarkPlanTypes(b *testing.B) {
	dir := b.TempDir()
	paths := make([]string, 0, 5)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 5 {
		path := filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", i))
		if err := os.WriteFile(path, []byte(randomLines(rng, 5000, 0)), 0o600); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, path)
	}
	b.Run("whole files", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, path := range paths {
				_ = LastPlanType(path)
			}
		}
	})
	b.Run("kept", func(b *testing.B) {
		p := &PlanTypes{}
		p.Last(paths)
		b.ReportAllocs()
		for b.Loop() {
			_ = p.Last(paths)
		}
	})
}
