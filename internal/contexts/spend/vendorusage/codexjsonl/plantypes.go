package codexjsonl

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail"
)

// LastPlanType is the last plan_type a rollout's turns report, "" for
// none, read from the whole file. It is the reference PlanTypes keeps.
func LastPlanType(path string) string {
	last := ""
	_ = ReadFile(path, func(t Turn) error {
		if t.RateLimits.PlanType != "" {
			last = t.RateLimits.PlanType
		}
		return nil
	})
	return last
}

// PlanTypes keeps the last plan_type each rollout reports, as
// LastPlanType gives it, reading only what changed since the last call: a
// plain rollout from where the last read stopped (jsonltail), a
// compressed one again only when its size or modification time changed.
// A plain rollout that was truncated, replaced or rewritten is read again
// from the start.
//
// The daemon asks every quarter of an hour which plan Codex reports.
// Reading the newest rollouts whole each time re-parsed tens of megabytes
// of transcript that had not changed.
//
// The zero value is ready to use. It is not safe for concurrent use.
type PlanTypes struct {
	tail jsonltail.Tail[planState]
	// last is each plain rollout's answer as of its last read, its
	// unfinished last line included.
	last map[string]string
	// packed is each compressed rollout's answer, by the identity it was
	// read at.
	packed map[string]packedPlan

	// FilesRead counts the rollouts opened, for tests.
	FilesRead int
}

// planState is what a plain rollout's next line depends on: the reader's
// state and the last plan_type of the complete lines read.
type planState struct {
	read readState
	last string
}

type packedPlan struct {
	size    int64
	modTime time.Time
	last    string
}

// Last is the last plan_type each of paths reports, by path; a rollout
// that cannot be read reports "". Rollouts not in paths are forgotten.
func (p *PlanTypes) Last(paths []string) map[string]string {
	var plain, compressed []string
	for _, path := range paths {
		if IsCompressed(path) {
			compressed = append(compressed, path)
		} else {
			plain = append(plain, path)
		}
	}
	if p.last == nil {
		p.last = map[string]string{}
	}
	p.tail.Scan(context.Background(), plain, p.readPlain, func(string, error) {})
	out := make(map[string]string, len(paths))
	for _, path := range plain {
		out[path] = p.last[path]
	}
	for path := range p.last {
		if _, live := out[path]; !live {
			delete(p.last, path)
		}
	}
	packed := make(map[string]packedPlan, len(compressed))
	for _, path := range compressed {
		info, err := os.Stat(path)
		if err != nil {
			out[path] = ""
			continue
		}
		prev, ok := p.packed[path]
		if !ok || prev.size != info.Size() || !prev.modTime.Equal(info.ModTime()) {
			p.FilesRead++
			prev = packedPlan{size: info.Size(), modTime: info.ModTime(), last: LastPlanType(path)}
		}
		packed[path] = prev
		out[path] = prev.last
	}
	p.packed = packed
	return out
}

// readPlain reads the complete lines a plain rollout gained into st, and
// records its answer with the unfinished last line, if any, read as a
// whole-file read reads it: Codex may be midway through writing it, so it
// is read again next time rather than kept in st.
func (p *PlanTypes) readPlain(path string, r io.Reader, st *planState) (int64, error) {
	p.FilesRead++
	rest := &restKeeper{r: r}
	n, err := readLines(rest, &st.read, false, func(t Turn) error {
		if t.RateLimits.PlanType != "" {
			st.last = t.RateLimits.PlanType
		}
		return nil
	})
	last := st.last
	if err == nil && len(rest.rest) > 0 && !rest.over {
		read := st.read
		_, _ = readLines(bytes.NewReader(rest.rest), &read, true, func(t Turn) error {
			if t.RateLimits.PlanType != "" {
				last = t.RateLimits.PlanType
			}
			return nil
		})
	}
	p.last[path] = last
	return n, err
}

// restKeeper passes r through and keeps what follows its last newline:
// once r is read to its end, the unfinished last line.
type restKeeper struct {
	r    io.Reader
	rest []byte
	// over says the unfinished line outgrew jsonltail.MaxLineBytes, which
	// a whole-file read skips.
	over bool
}

func (k *restKeeper) Read(b []byte) (int, error) {
	n, err := k.r.Read(b)
	chunk := b[:n]
	if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
		k.rest, k.over = k.rest[:0], false
		chunk = chunk[i+1:]
	}
	if !k.over {
		if len(k.rest)+len(chunk) > jsonltail.MaxLineBytes+1 {
			k.rest, k.over = k.rest[:0], true
		} else {
			k.rest = append(k.rest, chunk...)
		}
	}
	return n, err
}
