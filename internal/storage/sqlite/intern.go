package sqlite

import (
	"hash/maphash"
	"slices"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// interner hands out one copy of each string and each attribute set it is
// given, so the events a cache holds share what repeats instead of each
// holding its own.
//
// A month of a busy machine's prompt events (350,000, most of them Codex
// turns) carries five million attribute keys and values, fewer than
// 200,000 of them different, in fewer than 70,000 different sets: the
// turns of a session repeat its session, plan and window readings until
// one of them moves. Decoded, every event held its own map, about half the
// cache's memory, and every string in it was its own allocation for the
// collector to walk. Shared, the maps are a fifth of what they were.
//
// The sets it hands out are shared between events, as the cache's events
// are already shared between reads: neither may be modified.
//
// It only dedupes: what it has forgotten is handed out as it comes. So
// when it outgrows internLimit it starts again empty, which bounds it on
// a daemon that keeps one window up to date for weeks.
type interner struct {
	seed maphash.Seed
	strs map[string]string
	// sets holds the attribute sets handed out, by setHash.
	sets map[uint64][]map[string]string
	// keys holds the session keys handed out (SessionUsageCache).
	keys map[sessionKey]*sessionKey
	// held counts the strings and sets held.
	held int
}

// internLimit is how many strings and sets an interner holds before it
// starts again.
const internLimit = 1 << 18

func newInterner() *interner {
	in := &interner{seed: maphash.MakeSeed()}
	in.reset()
	return in
}

func (in *interner) reset() {
	in.strs = map[string]string{}
	in.sets = map[uint64][]map[string]string{}
	in.keys = map[sessionKey]*sessionKey{}
	in.held = 0
}

// room starts the interner again when it is full.
func (in *interner) room() {
	if in.held >= internLimit {
		in.reset()
	}
	in.held++
}

// str is s, as the copy handed out first.
func (in *interner) str(s string) string {
	if s == "" {
		return ""
	}
	if c, ok := in.strs[s]; ok {
		return c
	}
	in.room()
	in.strs[s] = s
	return s
}

// cached readies env for the cache: its attributes trimmed of what no
// reader of the cache asks for, and its strings and attribute set shared.
func (in *interner) cached(env *eventschema.Envelope) {
	env.SchemaVersion = in.str(env.SchemaVersion)
	env.Source = in.str(env.Source)
	env.Association.Work = in.str(env.Association.Work)
	env.Association.Execution = in.str(env.Association.Execution)
	env.Association.Actor = in.str(env.Association.Actor)
	env.Correlation.Decision = in.str(env.Correlation.Decision)
	env.Correlation.Intervention = in.str(env.Correlation.Intervention)
	env.Correlation.Experiment = in.str(env.Correlation.Experiment)
	env.Attributes = in.attributes(env.Attributes)
	if p, ok := env.Payload.(*eventschema.PromptEvent); ok {
		in.prompt(p)
	}
}

// attributes is attrs without uncachedAttributes: the set handed out
// before when one holds the same, otherwise a new map, sized for what is
// left (deleting from a Go map does not free its storage), with its keys
// and values shared.
func (in *interner) attributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return attrs
	}
	keep, sum := 0, uint64(0)
	for k, v := range attrs {
		if slices.Contains(uncachedAttributes, k) {
			continue
		}
		keep++
		sum += in.entryHash(k, v)
	}
	for _, set := range in.sets[sum] {
		if holdsKept(set, attrs, keep) {
			return set
		}
	}
	out := make(map[string]string, keep)
	for k, v := range attrs {
		if !slices.Contains(uncachedAttributes, k) {
			out[in.str(k)] = in.str(v)
		}
	}
	in.room()
	in.sets[sum] = append(in.sets[sum], out)
	return out
}

// entryHash hashes one attribute. A set's hash is the sum of its entries',
// which does not depend on the order a map is ranged in.
func (in *interner) entryHash(k, v string) uint64 {
	hk := maphash.String(in.seed, k)
	hv := maphash.String(in.seed, v)
	return hk ^ (hv*0x9e3779b97f4a7c15 + hk<<6 + hk>>2)
}

// holdsKept reports whether set is attrs without uncachedAttributes, keep
// being how many attributes that leaves. set holds no left-out key, so
// keep entries of it all found in attrs are all attrs keeps.
func holdsKept(set, attrs map[string]string, keep int) bool {
	if len(set) != keep {
		return false
	}
	for k, v := range set {
		if w, ok := attrs[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// prompt shares p's strings. The prompt hash is per prompt, and left as
// it is.
func (in *interner) prompt(p *eventschema.PromptEvent) {
	p.Provider = eventschema.Provider(in.str(string(p.Provider)))
	p.RequestModel = in.str(p.RequestModel)
	p.ResponseModel = in.str(p.ResponseModel)
	p.FinishReason = in.str(p.FinishReason)
	p.ErrorCode = in.str(p.ErrorCode)
	p.TokenSource = eventschema.TokenSource(in.str(string(p.TokenSource)))
	p.CostSource = eventschema.CostSource(in.str(string(p.CostSource)))
	p.WorkflowID = in.str(p.WorkflowID)
	p.AgentID = in.str(p.AgentID)
	p.SessionID = in.str(p.SessionID)
	p.UserID = in.str(p.UserID)
}
