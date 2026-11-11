package sessions

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
)

// This file is everything outside the packages it imports that decides
// what a transcript cache holds. TranscriptsRevision is made from it and
// from those packages, so what is here stays here.

// TranscriptCache is the agent-DX domain's per-file transcript cache.
type TranscriptCache = agentdx.TranscriptCache

// NewTranscriptCache returns an empty transcript cache for Window.Cache.
func NewTranscriptCache() *TranscriptCache { return agentdx.NewTranscriptCache() }

// NewPromptFactsCache returns an empty transcript cache for Window.Cache
// that keeps, for each instruction, what the work-classifying surfaces
// derive from its words (story boundaries, the kind of work) and not the
// words. Units read through it carry PromptFacts instead of Prompt, so a
// caller that reads them repeatedly (the team upload) reads only what
// changed and still holds no prompt text between reads.
func NewPromptFactsCache() *TranscriptCache {
	return agentdx.NewTranscriptCacheWithFacts(promptFacts)
}

// NewSharedTranscriptCache returns a prompt-facts cache that holds the
// last keep of transcripts whatever a read's window, so readers of
// different windows (the daemon's week of session analysis, its two weeks
// of team upload) share one copy: neither's cut-off makes the other read
// the files again, and the records are held once.
func NewSharedTranscriptCache(keep time.Duration) *TranscriptCache {
	return agentdx.NewTranscriptCacheWithFacts(promptFacts,
		agentdx.KeepFrom(func() time.Time { return time.Now().Add(-keep) }))
}

// promptFacts derives an instruction's facts from its words.
func promptFacts(text string) agentdx.PromptFacts {
	f := agentdx.PromptFacts{Known: true, Continues: story.ContinuesText(text)}
	if k := taskclass.OwnKind(text); k != taskclass.KindUnknown {
		f.Kind = string(k)
	}
	return f
}
