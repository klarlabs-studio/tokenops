package eventschema

// Attribute keys mirror the OpenTelemetry GenAI semantic conventions where
// applicable, augmented with TokenOps-specific keys under the "tokenops.*"
// namespace. Exporters (OTLP) translate Envelope + payload fields into these
// keys; storage backends (SQLite, ClickHouse) use the same keys for indexed
// columns so traces, metrics, and analytics share a vocabulary.
const (
	// GenAI semantic conventions (github.com/open-telemetry/semantic-conventions-genai,
	// docs/registry/attributes/gen-ai.md). Every gen_ai attribute is still
	// at Development stability there.
	AttrGenAIProviderName      = "gen_ai.provider.name"
	AttrGenAIRequestModel      = "gen_ai.request.model"
	AttrGenAIResponseModel     = "gen_ai.response.model"
	AttrGenAIUsageInputTokens  = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens = "gen_ai.usage.output_tokens"
	AttrGenAIRequestMaxTokens  = "gen_ai.request.max_tokens"
	AttrGenAIResponseFinish    = "gen_ai.response.finish_reasons"
	AttrGenAIOperationName     = "gen_ai.operation.name"
	// AttrGenAIUsageCacheReadInputTokens and
	// AttrGenAIUsageCacheWriteInputTokens are the portions of
	// gen_ai.usage.input_tokens read from and written to the provider's
	// prompt cache.
	AttrGenAIUsageCacheReadInputTokens  = "gen_ai.usage.cache_read.input_tokens"
	AttrGenAIUsageCacheWriteInputTokens = "gen_ai.usage.cache_write.input_tokens"

	// AttrGenAISystem is the provider's key before the conventions renamed
	// it gen_ai.provider.name. The exporter still writes it beside the new
	// key, so collector queries written against it keep working; it goes
	// in a later release.
	AttrGenAISystem = "gen_ai.system"
	// AttrGenAIUsageTotalTokens is not a key the conventions define. The
	// exporter still writes it for the queries that read it.
	AttrGenAIUsageTotalTokens = "gen_ai.usage.total_tokens"

	// TokenOps-specific attributes.
	AttrTokenOpsSchemaVersion        = "tokenops.schema_version"
	AttrTokenOpsEventType            = "tokenops.event.type"
	AttrTokenOpsDomainKind           = "tokenops.domain.kind"
	AttrTokenOpsPromptHash           = "tokenops.prompt.hash"
	AttrTokenOpsContextSize          = "tokenops.prompt.context_size"
	AttrTokenOpsCachedInputTokens    = "tokenops.usage.cached_input_tokens"
	AttrTokenOpsCacheHit             = "tokenops.cache.hit"
	AttrTokenOpsLatencyNS            = "tokenops.latency_ns"
	AttrTokenOpsTimeToFirstTokenNS   = "tokenops.time_to_first_token_ns"
	AttrTokenOpsStreaming            = "tokenops.streaming"
	AttrTokenOpsCostUSD              = "tokenops.cost_usd"
	AttrTokenOpsWorkflowID           = "tokenops.workflow.id"
	AttrTokenOpsWorkflowParentID     = "tokenops.workflow.parent_id"
	AttrTokenOpsWorkflowState        = "tokenops.workflow.state"
	AttrTokenOpsWorkflowStepCount    = "tokenops.workflow.step_count"
	AttrTokenOpsAgentID              = "tokenops.agent.id"
	AttrTokenOpsSessionID            = "tokenops.session.id"
	AttrTokenOpsUserID               = "tokenops.user.id"
	AttrTokenOpsOptimizationType     = "tokenops.optimization.type"
	AttrTokenOpsOptimizationMode     = "tokenops.optimization.mode"
	AttrTokenOpsOptimizationDecision = "tokenops.optimization.decision"
	AttrTokenOpsEstimatedSavings     = "tokenops.optimization.estimated_savings_tokens"
	AttrTokenOpsQualityScore         = "tokenops.optimization.quality_score"
	AttrTokenOpsCoachingKind         = "tokenops.coaching.kind"
	AttrTokenOpsEfficiencyScore      = "tokenops.coaching.efficiency_score"

	// Rule Intelligence attributes (issue #12).
	AttrTokenOpsRuleSourceID         = "tokenops.rule.source_id"
	AttrTokenOpsRuleSource           = "tokenops.rule.source"
	AttrTokenOpsRuleScope            = "tokenops.rule.scope"
	AttrTokenOpsRulePath             = "tokenops.rule.path"
	AttrTokenOpsRuleRepoID           = "tokenops.rule.repo_id"
	AttrTokenOpsRuleTokenizer        = "tokenops.rule.tokenizer"
	AttrTokenOpsRuleTotalTokens      = "tokenops.rule.total_tokens"
	AttrTokenOpsRuleSectionID        = "tokenops.rule.section_id"
	AttrTokenOpsRuleSectionCount     = "tokenops.rule.section_count"
	AttrTokenOpsRuleExposures        = "tokenops.rule.exposures"
	AttrTokenOpsRuleContextTokens    = "tokenops.rule.context_tokens"
	AttrTokenOpsRuleTokensSaved      = "tokenops.rule.tokens_saved"
	AttrTokenOpsRuleRetriesAvoided   = "tokenops.rule.retries_avoided"
	AttrTokenOpsRuleContextReduction = "tokenops.rule.context_reduction"
	AttrTokenOpsRuleQualityDelta     = "tokenops.rule.quality_delta"
	AttrTokenOpsRuleROIScore         = "tokenops.rule.roi_score"
	AttrTokenOpsRuleCompressedTokens = "tokenops.rule.compressed_tokens"
)

// providerToGenAIProvider maps TokenOps Provider values whose name differs
// from the convention's well-known gen_ai.provider.name value. Any other
// provider is written as it is named, as the convention allows.
var providerToGenAIProvider = map[Provider]string{
	ProviderOpenAI:    "openai",
	ProviderAnthropic: "anthropic",
	ProviderGemini:    "gcp.gemini",
	ProviderXAI:       "x_ai",
	ProviderUnknown:   "unknown",
}

// GenAIProviderName returns the gen_ai.provider.name value for p.
func GenAIProviderName(p Provider) string { return GenAISystem(p) }

// GenAISystem returns the OpenTelemetry GenAI provider identifier for p,
// the value of both gen_ai.provider.name and the older gen_ai.system.
func GenAISystem(p Provider) string {
	if v, ok := providerToGenAIProvider[p]; ok {
		return v
	}
	return string(p)
}
