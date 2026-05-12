package rulesfs

import (
	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"

	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

// DomainBusPublisher is the narrow port LoadCorpus uses to publish
// RuleCorpusReloaded events when a new snapshot is materialised.
// *internal/domainevents.Bus satisfies it.
type DomainBusPublisher interface {
	Publish(ev domainevents.Event)
}

var (
	corpusBus       DomainBusPublisher
	lastCorpusHash  string
	lastCorpusMutex sync.Mutex
)

// SetDomainBus installs the in-process domain bus the corpus loader
// publishes to. nil clears it. Called once from the daemon composition
// root.
func SetDomainBus(b DomainBusPublisher) {
	lastCorpusMutex.Lock()
	defer lastCorpusMutex.Unlock()
	corpusBus = b
	lastCorpusHash = ""
}

// publishReloaded fires the RuleCorpusReloaded event when a corpus is
// materialised. Suppresses duplicates by comparing the deterministic
// corpus digest against the previous publication; only genuine changes
// fire the event.
func publishReloaded(docs []*rules.RuleDocument) {
	if corpusBus == nil || len(docs) == 0 {
		return
	}
	digest := corpusDigest(docs)
	lastCorpusMutex.Lock()
	if digest == lastCorpusHash {
		lastCorpusMutex.Unlock()
		return
	}
	lastCorpusHash = digest
	lastCorpusMutex.Unlock()

	var totalTokens int64
	for _, d := range docs {
		// rules.RuleDocument.CharCount/4 mirrors the lightweight token estimate
		// the router uses; a precise tokenize would require a registry.
		totalTokens += d.CharCount() / 4
	}
	corpusBus.Publish(domainevents.RuleCorpusReloaded{
		SourceCount: len(docs),
		TotalTokens: totalTokens,
		At:          time.Now(),
	})
}

// corpusDigest returns a deterministic identifier for the corpus
// content: SHA-256 of every document's (SourceID, Hash) pair sorted by
// SourceID. Stable across reload calls when nothing has changed.
func corpusDigest(docs []*rules.RuleDocument) string {
	pairs := make([]string, 0, len(docs))
	for _, d := range docs {
		pairs = append(pairs, d.SourceID+"|"+d.Hash())
	}
	sort.Strings(pairs)
	h := sha256.New()
	for _, p := range pairs {
		h.Write([]byte(p))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RuleSource is the port the corpus loader depends on. Concrete
// filesystem walkers live in the adapter (infra/fs/Ingestor) so the
// rules domain does not import `os` directly. Tests substitute an
// in-memory fs.FS via FromFS.
type RuleSource interface {
	Snapshot() ([]*rules.RuleDocument, error)
}

// LoadCorpus is the canonical application service for loading the rule
// corpus rooted at a path. Every adapter (CLI, MCP, proxy HTTP) calls
// this — no caller instantiates Ingestor directly.
//
// The function returns the materialised slice of rules.RuleDocument values
// with parsed blocks and computed hashes; downstream services
// (Analyzer, Router, Compressor, Benchmark, Conflicts) consume them.
func LoadCorpus(root, repoID string) ([]*rules.RuleDocument, error) {
	src := &Ingestor{Root: root, RepoID: repoID}
	docs, err := src.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("rules: load corpus at %q: %w", root, err)
	}
	publishReloaded(docs)
	return docs, nil
}

// LoadCorpusFromFS is the test/embedded-FS variant. Adapters that need
// to drive the loader against a virtual filesystem use this; the
// production daemon uses LoadCorpus.
func LoadCorpusFromFS(fsys fs.FS, repoID string) ([]*rules.RuleDocument, error) {
	src := &Ingestor{FS: fsys, RepoID: repoID}
	docs, err := src.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("rules: load corpus from fs: %w", err)
	}
	publishReloaded(docs)
	return docs, nil
}
