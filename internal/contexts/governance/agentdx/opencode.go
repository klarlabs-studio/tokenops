package agentdx

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// ErrOpencodeSchema reports that an opencode database was found but did
// not have the tables this reader understands. Distinct from an empty
// result for the same reason as the Cursor reader: a store that cannot be
// read must not be reported as an operator who did no work.
var ErrOpencodeSchema = errors.New("agentdx: unrecognised opencode database schema")

// ErrNoOpencodeStore reports an opencode store on disk that this process
// has no reader for: a composition root that did not call
// UseOpencodeStore. Like ErrOpencodeSchema, it keeps an unread store from
// reading as an idle operator.
var ErrNoOpencodeStore = errors.New("agentdx: no reader for opencode's store is wired")

// opencodeStore is the reader ExtractOpencode uses; see UseOpencodeStore.
var opencodeStore opencodedb.Reader

// UseOpencodeStore installs the reader for opencode's store, the
// opencodedb.Reader port every opencode consumer reads through. The
// composition root (internal/bootstrap) installs internal/infra/opencodedb
// before anything extracts.
func UseOpencodeStore(r opencodedb.Reader) { opencodeStore = r }

// OpencodeDefaultPath returns opencode's store (opencodedb.DefaultPath:
// OPENCODE_DB, then XDG_DATA_HOME, then ~/.local/share).
func OpencodeDefaultPath() (string, error) { return opencodedb.DefaultPath() }

// ExtractOpencode reads opencode's store into Records, from opencode 1.x
// and 2.x alike (see opencodedb).
//
// opencode is the richest of the supported clients and the only
// multi-provider one: every turn records which upstream served it, so it
// is the only place the question "is OpenRouter a worse experience than
// Anthropic" can even be asked. It also records compactions explicitly
// rather than leaving them to be inferred from a summary row.
func ExtractOpencode(opts ExtractOptions) ([]Record, error) {
	path := opts.Root
	if path == "" {
		p, err := OpencodeDefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	store := opencodeStore
	if store == nil {
		if _, err := os.Stat(path); err != nil {
			// No store, nothing to read: opencode may not be installed.
			return nil, nil
		}
		return nil, fmt.Errorf("%w: found %s", ErrNoOpencodeStore, path)
	}
	var out []Record
	err := store.Read(path, opencodedb.Options{Since: opts.Since, Parts: true}, func(m opencodedb.Message) error {
		// A record needs its moment; a row without one says nothing about
		// how the work went.
		if m.Created.IsZero() {
			return nil
		}
		rec := Record{At: m.Created, SessionID: m.SessionID, Provider: m.ProviderID}
		switch m.Role {
		case opencodedb.User:
			rec.Kind = KindPrompt
			for _, t := range m.Text {
				if IsRejection(t) {
					rec.Rejects = true
				}
			}
			// The words exist for the length of a scan and only when asked
			// for, the same rule every reader follows. A message can carry
			// several text parts; the instruction is all of them, in order.
			if opts.WithPromptText {
				rec.Text = strings.Join(m.Text, "")
			}
			out = append(out, rec)
		case opencodedb.Assistant:
			rec.Kind = KindAssistantTurn
			rec.Model, rec.Effort = m.ModelID, normEffort(m.Variant)
			rec.InputTokens = m.Tokens.Input + m.Tokens.CacheRead + m.Tokens.CacheWrite
			out = append(out, rec)
			for _, tool := range m.Tools {
				out = append(out, Record{
					At: m.Created, SessionID: m.SessionID, Provider: m.ProviderID,
					Kind: KindToolUse, ToolName: tool.Name, FilePath: tool.Path(),
					CallSignature: callSignature(tool.Name, tool.Input),
				})
			}
		case opencodedb.Compaction:
			rec.Kind = KindCompaction
			out = append(out, rec)
		}
		return nil
	})
	if errors.Is(err, opencodedb.ErrSchema) {
		return nil, fmt.Errorf("%w: %v", ErrOpencodeSchema, err)
	}
	return out, err
}
