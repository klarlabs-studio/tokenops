package taskruntime

import (
	"regexp"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// UsageMethod says how recorded turns are placed on tasks, for every
// surface to show.
const UsageMethod = "session and time: a recorded turn goes to the task of its session that started last at or before it; subagent turns recorded under the session count toward its task"

// Turn is one recorded model call, as the event store holds it: what
// placing usage on a task needs and nothing more.
type Turn struct {
	SessionID string
	At        time.Time
	// InputTokens includes the cache portions.
	InputTokens, OutputTokens         int64
	CacheReadTokens, CacheWriteTokens int64
	// BilledUSD is what was billed (0 where a plan covered it);
	// APIEquivalentUSD what it costs at list prices. Priced is false when
	// the model has no list price, so neither figure is complete.
	BilledUSD, APIEquivalentUSD float64
	Priced                      bool
}

// Usage is what a task's recorded turns used. A task no recorded turn
// fell in has none: unknown, never zero.
type Usage struct {
	Turns int `json:"turns"`
	// InputTokens includes CacheReadTokens and CacheWriteTokens.
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// BilledUSD is what the turns were billed; APIEquivalentUSD what they
	// cost at list prices, their value where a plan covered them.
	BilledUSD        float64 `json:"billed_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	// UnpricedTurns had no list price, so both figures understate them.
	UnpricedTurns int `json:"unpriced_turns"`
}

func (u *Usage) add(t Turn) {
	u.Turns++
	u.InputTokens += t.InputTokens
	u.OutputTokens += t.OutputTokens
	u.CacheReadTokens += t.CacheReadTokens
	u.CacheWriteTokens += t.CacheWriteTokens
	u.BilledUSD += t.BilledUSD
	u.APIEquivalentUSD += t.APIEquivalentUSD
	if !t.Priced {
		u.UnpricedTurns++
	}
}

// AttachUsage places each recorded turn on the task of its session that
// started last at or before it, and sets the usage of analysed, which is
// Analyse's result for tasks, index for index. A turn of a session no task
// covers, or from before its first task, is placed on none; the count is
// returned.
func AttachUsage(tasks []story.Task, analysed []Task, turns []Turn) (unplaced int) {
	usage, unplaced := PlaceTurns(tasks, turns)
	for i, u := range usage {
		analysed[i].Usage = u
	}
	return unplaced
}

// PlaceTurns is AttachUsage's join on its own: each task's usage, index
// for index, nil for a task no turn was placed on, and how many turns went
// on none.
func PlaceTurns(tasks []story.Task, turns []Turn) (usage []*Usage, unplaced int) {
	type start struct {
		at  time.Time
		idx int
	}
	usage = make([]*Usage, len(tasks))
	bySession := map[string][]start{}
	for i, t := range tasks {
		id := sessionMatchKey(t.SessionID)
		bySession[id] = append(bySession[id], start{t.Start, i})
	}
	for _, starts := range bySession {
		sort.SliceStable(starts, func(i, j int) bool { return starts[i].at.Before(starts[j].at) })
	}
	for _, turn := range turns {
		starts := bySession[sessionMatchKey(turn.SessionID)]
		// The first task that starts after the turn; the one before it
		// holds the turn.
		n := sort.Search(len(starts), func(i int) bool { return starts[i].at.After(turn.At) })
		if turn.SessionID == "" || n == 0 {
			unplaced++
			continue
		}
		i := starts[n-1].idx
		if usage[i] == nil {
			usage[i] = &Usage{}
		}
		usage[i].add(turn)
	}
	return usage, unplaced
}

// trailingUUID is a session UUID at the end of an ID.
var trailingUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// sessionMatchKey is the part of a session ID both sides record. Codex's
// transcript reader names a session by its rollout file
// ("2026-10-29T20-00-18-01a18908-…") and its turn reader by the UUID
// alone, so an ID ending in a UUID matches on the UUID.
func sessionMatchKey(id string) string {
	if u := trailingUUID.FindString(id); u != "" {
		return u
	}
	return id
}
