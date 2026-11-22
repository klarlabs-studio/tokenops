package sqlite

import (
	"math"
	"time"
	"weak"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// packedEvent is one event as the event cache holds it between reads.
//
// The cache holds a month of prompt events for as long as the daemon
// runs: on a busy machine 560,000 of them. Held as decoded envelopes, each
// was an Envelope and a PromptEvent of mostly empty fields, about 600
// bytes, which made the cache two thirds of the daemon's live heap. A
// prompt event is packed instead: what is its own (time, id, counts) in
// one small struct, and everything its session's turns repeat (source,
// models, session, attributes, status) in one promptShared they point to.
//
// A read hands out envelopes, as before. The envelope built for a packed
// event is kept through a weak pointer: every read while one reader still
// holds it gets the same envelope, and once no reader does the collector
// frees it and the next read builds it again. What the cache keeps between
// reads is the packed events only.
//
// An event that does not pack (another type, or a prompt carrying a trace
// or span id, which no session repeats) is held as its envelope.
type packedEvent struct {
	ts int64
	id string
	// shared is what the event has in common with its session's other
	// turns, attrs its attribute set (shared through the interner), n its
	// own figures; all unset for an event held as it is.
	shared *promptShared
	attrs  map[string]string
	n      promptCounts
	// rare holds what few events carry: the envelope of an event held as
	// it is, or a prompt hash. Nil for most.
	rare *packedRare
	// built is the envelope last handed out for a packed event, while a
	// reader holds it. Read and set under the cache's lock.
	built weak.Pointer[eventschema.Envelope]
}

// packedRare is what few of the cache's events carry.
type packedRare struct {
	// full is the envelope of an event held as it is.
	full *eventschema.Envelope
	// hash is a packed prompt event's PromptHash.
	hash string
}

// promptCounts are a prompt event's own figures. Token counts are held
// in 32 bits: one turn's never come near 2^31, and an event whose do is
// held as it is.
type promptCounts struct {
	input, output, total, cachedInput, cacheWrite, cacheWrite1h int32
	contextSize, maxOutput, toolCalls                           int32
	latency, firstToken                                         time.Duration
	cost                                                        float64
}

// countsOf packs prompt's figures, and reports false when a token count
// does not fit.
func countsOf(prompt *eventschema.PromptEvent) (promptCounts, bool) {
	tokens := [...]int64{
		prompt.InputTokens, prompt.OutputTokens, prompt.TotalTokens,
		prompt.CachedInputTokens, prompt.CacheWriteInputTokens, prompt.CacheWrite1hInputTokens,
		prompt.ContextSize, prompt.MaxOutputTokens, prompt.ToolCallCount,
	}
	for _, v := range tokens {
		if v < math.MinInt32 || v > math.MaxInt32 {
			return promptCounts{}, false
		}
	}
	return promptCounts{
		input: int32(tokens[0]), output: int32(tokens[1]), total: int32(tokens[2]),
		cachedInput: int32(tokens[3]), cacheWrite: int32(tokens[4]), cacheWrite1h: int32(tokens[5]),
		contextSize: int32(tokens[6]), maxOutput: int32(tokens[7]), toolCalls: int32(tokens[8]),
		latency: prompt.Latency, firstToken: prompt.TimeToFirstToken, cost: prompt.CostUSD,
	}, true
}

// promptShared is what a prompt event has in common with the other turns
// of its session, but for its attributes, which move from turn to turn
// more often. The cache's interner hands out one per different value.
type promptShared struct {
	key promptKey
}

// promptKey is a promptShared's value.
type promptKey struct {
	schemaVersion, source string
	association           eventschema.Association
	correlation           eventschema.Correlation

	provider                               eventschema.Provider
	requestModel, responseModel            string
	finishReason, errorCode                string
	tokenSource                            eventschema.TokenSource
	costSource                             eventschema.CostSource
	workflowID, agentID, sessionID, userID string
	status                                 int
	streaming, cacheHit, costMeasured      bool
}

// noAttributes is the one empty attribute set packed events share: an
// event stored with "{}" decodes to an empty map, not to none.
var noAttributes = map[string]string{}

// pack is env as the cache holds it, its strings and attributes shared
// through in. env must already be readied by in.cached.
func (in *interner) pack(env *eventschema.Envelope) *packedEvent {
	p := &packedEvent{ts: env.Timestamp.UnixNano(), id: env.ID}
	prompt, ok := env.Payload.(*eventschema.PromptEvent)
	var counts promptCounts
	if ok {
		counts, ok = countsOf(prompt)
	}
	if !ok || env.Type != eventschema.EventTypePrompt || env.TraceID != "" || env.SpanID != "" ||
		env.Timestamp.Location() != time.UTC {
		p.rare = &packedRare{full: env}
		return p
	}
	p.attrs = env.Attributes
	if p.attrs != nil && len(p.attrs) == 0 {
		p.attrs = noAttributes
	}
	key := promptKey{
		schemaVersion: env.SchemaVersion, source: env.Source, association: env.Association, correlation: env.Correlation,
		provider: prompt.Provider, requestModel: prompt.RequestModel, responseModel: prompt.ResponseModel,
		finishReason: prompt.FinishReason, errorCode: prompt.ErrorCode,
		tokenSource: prompt.TokenSource, costSource: prompt.CostSource,
		workflowID: prompt.WorkflowID, agentID: prompt.AgentID, sessionID: prompt.SessionID, userID: prompt.UserID,
		status: prompt.Status, streaming: prompt.Streaming, cacheHit: prompt.CacheHit, costMeasured: prompt.CostMeasured,
	}
	p.shared = in.promptShared(key)
	if prompt.PromptHash != "" {
		p.rare = &packedRare{hash: prompt.PromptHash}
	}
	p.n = counts
	return p
}

// promptShared is the promptShared handed out for key, or a new one.
func (in *interner) promptShared(key promptKey) *promptShared {
	if s, ok := in.prompts[key]; ok {
		return s
	}
	in.room()
	s := &promptShared{key: key}
	in.prompts[key] = s
	return s
}

// source is the event's source, without building its envelope.
func (p *packedEvent) source() string {
	if full := p.heldAsIs(); full != nil {
		return full.Source
	}
	return p.shared.key.source
}

// envelope is the event's envelope: the one held, the one a reader still
// holds, or a new one. The cache's lock is held.
func (p *packedEvent) envelope() *eventschema.Envelope {
	if full := p.heldAsIs(); full != nil {
		return full
	}
	if env := p.built.Value(); env != nil {
		return env
	}
	env := p.unpack()
	p.built = weak.Make(env)
	return env
}

// heldAsIs is the envelope of an event held as it is, or nil.
func (p *packedEvent) heldAsIs() *eventschema.Envelope {
	if p.rare == nil {
		return nil
	}
	return p.rare.full
}

// scratchEnvelope is one envelope and payload a scan builds every packed
// event into in turn.
type scratchEnvelope struct {
	env    eventschema.Envelope
	prompt eventschema.PromptEvent
}

// of builds p into s and returns it; valid until the next call.
func (s *scratchEnvelope) of(p *packedEvent) *eventschema.Envelope {
	p.unpackInto(&s.env, &s.prompt)
	return &s.env
}

// unpack builds the envelope p was packed from.
func (p *packedEvent) unpack() *eventschema.Envelope {
	env, prompt := new(eventschema.Envelope), new(eventschema.PromptEvent)
	p.unpackInto(env, prompt)
	return env
}

// unpackInto builds the envelope p was packed from into env, its payload
// into prompt.
func (p *packedEvent) unpackInto(env *eventschema.Envelope, prompt *eventschema.PromptEvent) {
	k := &p.shared.key
	hash := ""
	if p.rare != nil {
		hash = p.rare.hash
	}
	*prompt = eventschema.PromptEvent{
		PromptHash: hash, Provider: k.provider, RequestModel: k.requestModel, ResponseModel: k.responseModel,
		InputTokens: int64(p.n.input), OutputTokens: int64(p.n.output), TotalTokens: int64(p.n.total),
		CachedInputTokens: int64(p.n.cachedInput), CacheWriteInputTokens: int64(p.n.cacheWrite),
		CacheWrite1hInputTokens: int64(p.n.cacheWrite1h),
		ContextSize:             int64(p.n.contextSize), MaxOutputTokens: int64(p.n.maxOutput),
		Latency: p.n.latency, TimeToFirstToken: p.n.firstToken,
		Streaming: k.streaming, Status: k.status, FinishReason: k.finishReason, ToolCallCount: int64(p.n.toolCalls),
		ErrorCode: k.errorCode, CacheHit: k.cacheHit, CostUSD: p.n.cost, CostMeasured: k.costMeasured,
		TokenSource: k.tokenSource, CostSource: k.costSource,
		WorkflowID: k.workflowID, AgentID: k.agentID, SessionID: k.sessionID, UserID: k.userID,
	}
	*env = eventschema.Envelope{
		ID: p.id, SchemaVersion: k.schemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: timeFromNS(p.ts), Source: k.source, Attributes: p.attrs,
		Association: k.association, Correlation: k.correlation,
		Payload: prompt,
	}
}

// Packer packs envelopes as the event cache holds them, for a reader that
// keeps events between reads (the waste report's trace cache): what a
// packed event holds is a fraction of its envelope, and the strings and
// attribute sets its session repeats are held once. Like the event cache
// it leaves out the attributes no reader of kept events asks for
// (uncachedAttributes): with them, a Codex turn's set is its own, never
// shared. A Packer is not safe for concurrent use.
type Packer struct {
	in *interner
}

// NewPacker returns an empty Packer.
func NewPacker() *Packer {
	return &Packer{in: newInterner()}
}

// PackedEvent is one envelope as a Packer packed it.
type PackedEvent struct {
	p *packedEvent
}

// Pack packs env. It shares env's strings with the events packed before,
// which changes none of its values; env must not be modified after.
func (k *Packer) Pack(env *eventschema.Envelope) PackedEvent {
	k.in.cached(env)
	return PackedEvent{p: k.in.pack(env)}
}

// Envelope is the envelope e was packed from: a new one for a packed
// event, which its caller may keep, and the envelope itself for one held
// as it is, which must not be modified. Its attribute set is shared with
// the other events packed with the same set and must not be modified.
func (e PackedEvent) Envelope() *eventschema.Envelope {
	if full := e.p.heldAsIs(); full != nil {
		return full
	}
	return e.p.unpack()
}
