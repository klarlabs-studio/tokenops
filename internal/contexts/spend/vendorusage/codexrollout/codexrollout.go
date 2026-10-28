// Package codexrollout opens Codex rollouts, the per-session transcripts
// under $CODEX_HOME/sessions. A rollout is plain rollout-<id>.jsonl while
// it is live; once cold, Codex compresses it to rollout-<id>.jsonl.zst and
// removes the plain copy (codex-rs/rollout/src/compression.rs, zstd level 3).
package codexrollout

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// CompressedSuffix ends the name of a compressed rollout.
const CompressedSuffix = ".jsonl.zst"

// PlainSuffix ends the name of a plain rollout.
const PlainSuffix = ".jsonl"

// maxWindowBytes bounds the zstd window a rollout may declare. Codex
// compresses at level 3, whose window is a few MiB; a frame asking for
// more is not one Codex wrote.
const maxWindowBytes = 128 << 20

// IsRolloutName reports whether a file name is a rollout, plain or
// compressed.
func IsRolloutName(name string) bool {
	return strings.HasSuffix(name, PlainSuffix) || strings.HasSuffix(name, CompressedSuffix)
}

// IsCompressed reports whether a rollout path is a compressed copy.
func IsCompressed(path string) bool {
	return strings.HasSuffix(path, CompressedSuffix)
}

// Open opens a rollout for reading. A compressed copy is decompressed as
// it is read, so memory stays bounded by the zstd window, and a corrupt
// one fails on read rather than reading as empty.
func Open(path string) (io.ReadCloser, error) {
	f, err := os.Open(path) //nolint:gosec // a rollout under Codex's home
	if err != nil {
		return nil, err
	}
	if !IsCompressed(path) {
		return f, nil
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(maxWindowBytes))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("open compressed rollout %s: %w", filepath.Base(path), err)
	}
	return &decompressed{dec: dec, f: f}, nil
}

// decompressed reads through a zstd decoder and closes the file under it.
type decompressed struct {
	dec *zstd.Decoder
	f   *os.File
}

func (z *decompressed) Read(p []byte) (int, error) { return z.dec.Read(p) }

func (z *decompressed) Close() error {
	z.dec.Close()
	return z.f.Close()
}

// SessionID is the session a rollout name carries: the part after
// "rollout-", without the suffix. ok is false for a name that is not a
// rollout.
func SessionID(name string) (string, bool) {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(name), CompressedSuffix), PlainSuffix)
	if !strings.HasPrefix(base, "rollout-") {
		return base, false
	}
	return strings.TrimPrefix(base, "rollout-"), true
}
