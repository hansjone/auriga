package schemas

import (
	"slices"

	"sync/atomic"
)

type ModelCaps struct {
	provider ModelProvider
	model    string
	record   *ModelCapabilities
}

var capabilityResolver atomic.Pointer[func(ModelProvider, string) *ModelCapabilities]

func CapabilitiesFor(provider ModelProvider, model string) *ModelCapabilities {
	if model == "" {
		return nil
	}
	resolve := capabilityResolver.Load()
	if resolve == nil {
		return nil
	}
	return (*resolve)(provider, normalizeCapabilityModel(provider, model))
}

func normalizeCapabilityModel(provider ModelProvider, model string) string {
	if p, bare := ParseModelString(model, ""); p == provider && bare != "" {
		return bare
	}
	return model
}

func ResolveModelCaps(provider ModelProvider, model string) ModelCaps {
	model = normalizeCapabilityModel(provider, model)
	return ModelCaps{
		provider: provider,
		model:    model,
		record:   CapabilitiesFor(provider, model),
	}
}

func (c ModelCaps) Provider() ModelProvider { return c.provider }

func (c ModelCaps) Model() string { return c.model }

func (c ModelCaps) SupportsFastMode(fallback bool) bool {
	if c.record != nil && c.record.SupportsFastMode != nil {
		return *c.record.SupportsFastMode
	}
	return fallback
}

func (c ModelCaps) SupportsSafeguards(fallback bool) bool {
	if c.record != nil && c.record.SupportsSafeguards != nil {
		return *c.record.SupportsSafeguards
	}
	return fallback
}

const (
	FieldTopP                 = "top_p"
	FieldTemperature          = "temperature"
	FieldTopLogprobs          = "top_logprobs"
	FieldLogprobs             = "logprobs"
	FieldPresencePenalty      = "presence_penalty"
	FieldFrequencyPenalty     = "frequency_penalty"
	FieldStop                 = "stop"
	FieldReasoningEffort      = "reasoning_effort"
	FieldPrediction           = "prediction"
	FieldPromptCacheKey       = "prompt_cache_key"
	FieldPromptCacheRetention = "prompt_cache_retention"
	FieldPromptCacheOptions   = "prompt_cache_options"
	FieldVerbosity            = "verbosity"
	FieldStore                = "store"
	FieldWebSearchOptions     = "web_search_options"
	FieldSearchContentTypes   = "search_content_types"
)

const ConditionWhenEffortNone = "when_effort_none"

func (c ModelCaps) FieldUnsupported(field string, fallback bool) bool {
	if c.record != nil {
		if unsupported, ok := c.record.UnsupportedFields[field]; ok {
			return unsupported
		}
	}
	return fallback
}

func (c ModelCaps) SupportsSamplingParams(fallback bool) bool {
	if c.record != nil && c.record.SupportsSamplingParams != nil {
		return *c.record.SupportsSamplingParams
	}
	return fallback
}

func (c ModelCaps) SupportsToolChoice(fallback bool) bool {
	if c.record != nil && c.record.SupportsToolChoice != nil {
		return *c.record.SupportsToolChoice
	}
	return fallback
}

func (c ModelCaps) SupportsParallelFunctionCalling(fallback bool) bool {
	if c.record != nil && c.record.SupportsParallelFunctionCalling != nil {
		return *c.record.SupportsParallelFunctionCalling
	}
	return fallback
}

func (c ModelCaps) SupportsResponseSchema(fallback bool) bool {
	if c.record != nil && c.record.SupportsResponseSchema != nil {
		return *c.record.SupportsResponseSchema
	}
	return fallback
}

func (c ModelCaps) SupportsSystemMessages(fallback bool) bool {
	if c.record != nil && c.record.SupportsSystemMessages != nil {
		return *c.record.SupportsSystemMessages
	}
	return fallback
}

func (c ModelCaps) SupportsDeveloperRole(fallback bool) bool {
	if c.record != nil && c.record.SupportsDeveloperRole != nil {
		return *c.record.SupportsDeveloperRole
	}
	return fallback
}

func (c ModelCaps) SupportsAssistantPrefill(fallback bool) bool {
	if c.record != nil && c.record.SupportsAssistantPrefill != nil {
		return *c.record.SupportsAssistantPrefill
	}
	return fallback
}

func (c ModelCaps) SupportsCachePoint(fallback bool) bool {
	if c.record != nil && c.record.SupportsCachePoint != nil {
		return *c.record.SupportsCachePoint
	}
	return fallback
}

func (c ModelCaps) SupportsPromptCacheBreakpoint(fallback bool) bool {
	if c.record != nil && c.record.SupportsPromptCacheBreakpoint != nil {
		return *c.record.SupportsPromptCacheBreakpoint
	}
	return fallback
}

func (c ModelCaps) SupportsAsyncTools(fallback bool) bool {
	if c.record != nil && c.record.SupportsAsyncTools != nil {
		return *c.record.SupportsAsyncTools
	}
	return fallback
}

func (c ModelCaps) SupportsCustomTools(fallback bool) bool {
	if c.record != nil && c.record.SupportsCustomTools != nil {
		return *c.record.SupportsCustomTools
	}
	return fallback
}

func (c ModelCaps) SupportsPromptCaching(fallback bool) bool {
	if c.record != nil && c.record.SupportsPromptCaching != nil {
		return *c.record.SupportsPromptCaching
	}
	return fallback
}

func (c ModelCaps) SupportsPromptCachingScope(fallback bool) bool {
	if c.record != nil && c.record.SupportsPromptCachingScope != nil {
		return *c.record.SupportsPromptCachingScope
	}
	return fallback
}

func (c ModelCaps) SupportsExtendedCacheTTL(fallback bool) bool {
	if c.record != nil && c.record.SupportsExtendedCacheTTL != nil {
		return *c.record.SupportsExtendedCacheTTL
	}
	return fallback
}

func (c ModelCaps) ToolChoiceStructSupported(fallback bool) bool {
	if c.record != nil && c.record.ToolChoiceStructSupported != nil {
		return *c.record.ToolChoiceStructSupported
	}
	return fallback
}

func (c ModelCaps) ToolChoiceAnySupported(fallback bool) bool {
	if c.record != nil && c.record.ToolChoiceAnySupported != nil {
		return *c.record.ToolChoiceAnySupported
	}
	return fallback
}

func (c ModelCaps) SupportsForcedToolChoice(fallback bool) bool {
	if c.record != nil && c.record.SupportsForcedToolChoice != nil {
		return *c.record.SupportsForcedToolChoice
	}
	return fallback
}

func (c ModelCaps) SupportsDecisions(fallback bool) bool {
	if c.record != nil && c.record.SupportsDecisions != nil {
		return *c.record.SupportsDecisions
	}
	return fallback
}

func (c ModelCaps) SupportsPromptCacheBreakpoints(fallback bool) bool {
	if c.record != nil && c.record.SupportsPromptCacheBreakpoints != nil {
		return *c.record.SupportsPromptCacheBreakpoints
	}
	return fallback
}

func (c ModelCaps) SyntheticSOToolChoiceOmitted(fallback bool) bool {
	if c.record != nil && c.record.SyntheticSOToolChoiceOmitted != nil {
		return *c.record.SyntheticSOToolChoiceOmitted
	}
	return fallback
}

func (c ModelCaps) ServiceTierSupported(tier BifrostServiceTier, fallback bool) bool {
	if c.record != nil && len(c.record.ServiceTiers) > 0 {
		return slices.Contains(c.record.ServiceTiers, string(tier))
	}
	return fallback
}

func (c ModelCaps) ServerTool(name string) (string, bool) {
	if c.record == nil {
		return "", false
	}
	version, ok := c.record.ServerTools[name]
	return version, ok
}

func (c ModelCaps) FieldCondition(field string) string {
	if c.record == nil {
		return ""
	}
	return c.record.ConditionallyUnsupportedFields[field]
}

func (c ModelCaps) SupportsMultimodalToolOutput(fallback bool) bool {
	if c.record != nil && c.record.SupportsMultimodalToolOutput != nil {
		return *c.record.SupportsMultimodalToolOutput
	}
	return fallback
}

func (c ModelCaps) SupportsResponseSchemaWithTools(fallback bool) bool {
	if c.record != nil && c.record.SupportsResponseSchemaWithTools != nil {
		return *c.record.SupportsResponseSchemaWithTools
	}
	return fallback
}

func (c ModelCaps) SupportsNativeEffort(fallback bool) bool {
	if c.record != nil && c.record.SupportsNativeEffort != nil {
		return *c.record.SupportsNativeEffort
	}
	return fallback
}

func (c ModelCaps) SupportsMidConversationSystem(fallback bool) bool {
	if c.record != nil && c.record.SupportsMidConversationSystem != nil {
		return *c.record.SupportsMidConversationSystem
	}
	return fallback
}

func (c ModelCaps) SupportsMidConvOutputConfig(fallback bool) bool {
	if c.record != nil && c.record.SupportsMidConvOutputConfig != nil {
		return *c.record.SupportsMidConvOutputConfig
	}
	return fallback
}

func (c ModelCaps) SupportsMCP(fallback bool) bool {
	if c.record != nil && c.record.SupportsMCP != nil {
		return *c.record.SupportsMCP
	}
	return fallback
}

func (c ModelCaps) SupportsWebSearch(fallback bool) bool {
	if c.record != nil && c.record.SupportsWebSearch != nil {
		return *c.record.SupportsWebSearch
	}
	return fallback
}

func (c ModelCaps) SupportsWebSearchDynamic(fallback bool) bool {
	if c.record != nil && c.record.SupportsWebSearchDynamic != nil {
		return *c.record.SupportsWebSearchDynamic
	}
	return fallback
}

func (c ModelCaps) SupportsWebFetch(fallback bool) bool {
	if c.record != nil && c.record.SupportsWebFetch != nil {
		return *c.record.SupportsWebFetch
	}
	return fallback
}

func (c ModelCaps) SupportsCodeExecution(fallback bool) bool {
	if c.record != nil && c.record.SupportsCodeExecution != nil {
		return *c.record.SupportsCodeExecution
	}
	return fallback
}

func (c ModelCaps) SupportsBashTool(fallback bool) bool {
	if c.record != nil && c.record.SupportsBashTool != nil {
		return *c.record.SupportsBashTool
	}
	return fallback
}

func (c ModelCaps) SupportsMemoryTool(fallback bool) bool {
	if c.record != nil && c.record.SupportsMemoryTool != nil {
		return *c.record.SupportsMemoryTool
	}
	return fallback
}

func (c ModelCaps) SupportsToolSearch(fallback bool) bool {
	if c.record != nil && c.record.SupportsToolSearch != nil {
		return *c.record.SupportsToolSearch
	}
	return fallback
}

func (c ModelCaps) SupportsNamespaceTools(fallback bool) bool {
	if c.record != nil && c.record.SupportsNamespaceTools != nil {
		return *c.record.SupportsNamespaceTools
	}
	return fallback
}

func (c ModelCaps) ToolNameMaxLength(fallback int) int {
	if c.record != nil && c.record.ToolNameMaxLength != nil && *c.record.ToolNameMaxLength > 0 {
		return *c.record.ToolNameMaxLength
	}
	return fallback
}

func (c ModelCaps) ReservedToolNamespaces(fallback []string) []string {
	if c.record != nil && len(c.record.ReservedToolNamespaces) > 0 {
		return c.record.ReservedToolNamespaces

	}
	return fallback
}

func (c ModelCaps) SupportsRegexLookaround(fallback bool) bool {
	if c.record != nil && c.record.SupportsRegexLookaround != nil {
		return *c.record.SupportsRegexLookaround
	}
	return fallback
}

func (c ModelCaps) SupportsRegexNULEscape(fallback bool) bool {
	if c.record != nil && c.record.SupportsRegexNULEscape != nil {
		return *c.record.SupportsRegexNULEscape
	}
	return fallback
}

func (c ModelCaps) SupportsSchemaPattern(fallback bool) bool {
	if c.record != nil && c.record.SupportsSchemaPattern != nil {
		return *c.record.SupportsSchemaPattern
	}
	return fallback
}

func (c ModelCaps) SupportedSchemaFormats(fallback []string) []string {
	if c.record != nil && len(c.record.SupportedSchemaFormats) > 0 {
		return c.record.SupportedSchemaFormats
	}
	return fallback
}

func (c ModelCaps) SupportsAdvisorTool(fallback bool) bool {
	if c.record != nil && c.record.SupportsAdvisorTool != nil {
		return *c.record.SupportsAdvisorTool
	}
	return fallback
}

func (c ModelCaps) SupportsSkills(fallback bool) bool {
	if c.record != nil && c.record.SupportsSkills != nil {
		return *c.record.SupportsSkills
	}
	return fallback
}

func (c ModelCaps) SupportsTaskBudgets(fallback bool) bool {
	if c.record != nil && c.record.SupportsTaskBudgets != nil {
		return *c.record.SupportsTaskBudgets
	}
	return fallback
}

func (c ModelCaps) SupportsInferenceGeo(fallback bool) bool {
	if c.record != nil && c.record.SupportsInferenceGeo != nil {
		return *c.record.SupportsInferenceGeo
	}
	return fallback
}

func (c ModelCaps) SupportsCompaction(fallback bool) bool {
	if c.record != nil && c.record.SupportsCompaction != nil {
		return *c.record.SupportsCompaction
	}
	return fallback
}

func (c ModelCaps) SupportsContextEditing(fallback bool) bool {
	if c.record != nil && c.record.SupportsContextEditing != nil {
		return *c.record.SupportsContextEditing
	}
	return fallback
}

func (c ModelCaps) SupportsAdvancedToolUse(fallback bool) bool {
	if c.record != nil && c.record.SupportsAdvancedToolUse != nil {
		return *c.record.SupportsAdvancedToolUse
	}
	return fallback
}

func (c ModelCaps) SupportsInputExamples(fallback bool) bool {
	if c.record != nil && c.record.SupportsInputExamples != nil {
		return *c.record.SupportsInputExamples
	}
	return fallback
}

func (c ModelCaps) SupportsEagerInputStreaming(fallback bool) bool {
	if c.record != nil && c.record.SupportsEagerInputStreaming != nil {
		return *c.record.SupportsEagerInputStreaming
	}
	return fallback
}

func (c ModelCaps) SupportsInterleavedThinking(fallback bool) bool {
	if c.record != nil && c.record.SupportsInterleavedThinking != nil {
		return *c.record.SupportsInterleavedThinking
	}
	return fallback
}

func (c ModelCaps) SupportsFilesAPI(fallback bool) bool {
	if c.record != nil && c.record.SupportsFilesAPI != nil {
		return *c.record.SupportsFilesAPI
	}
	return fallback
}

func (c ModelCaps) SupportsComputerToolset(fallback bool) bool {
	if c.record != nil && c.record.SupportsComputerToolset != nil {
		return *c.record.SupportsComputerToolset
	}
	return fallback
}

func (c ModelCaps) SupportsTextEditorTool(fallback bool) bool {
	if c.record != nil && c.record.SupportsTextEditorTool != nil {
		return *c.record.SupportsTextEditorTool
	}
	return fallback
}

func (c ModelCaps) BedrockAPIs() []BedrockAPI {
	if c.record == nil || len(c.record.BedrockAPIs) == 0 {
		return nil
	}
	apis := make([]BedrockAPI, 0, len(c.record.BedrockAPIs))
	for _, api := range c.record.BedrockAPIs {
		if api.IsValid() && !slices.Contains(apis, api) {
			apis = append(apis, api)
		}
	}
	if len(apis) == 0 {
		return nil
	}
	return apis
}

func (c ModelCaps) MinOutputTokens(fallback int) int {
	if c.record != nil && c.record.MinOutputTokens != nil {
		return *c.record.MinOutputTokens
	}
	return fallback
}

func (c ModelCaps) MaxOutputTokens(fallback int) int {
	if c.record != nil && c.record.MaxOutputTokens != nil && *c.record.MaxOutputTokens > 0 {
		return *c.record.MaxOutputTokens
	}
	return fallback
}

func (c ModelCaps) BedrockReasoningShape(fallback BedrockReasoningShape) BedrockReasoningShape {
	if c.record != nil && c.record.BedrockReasoningShape.IsValid() {
		return c.record.BedrockReasoningShape
	}
	return fallback
}

func (c ModelCaps) BedrockMantleBasePath(fallback BedrockMantleBasePath) BedrockMantleBasePath {
	if c.record != nil && c.record.BedrockMantleBasePath.IsValid() {
		return c.record.BedrockMantleBasePath
	}
	return fallback
}

func (c ModelCaps) BedrockRequiresSignedReasoning(fallback bool) bool {
	if c.record != nil && c.record.BedrockRequiresSignedReasoning != nil {
		return *c.record.BedrockRequiresSignedReasoning
	}
	return fallback
}

func (c ModelCaps) SupportsConverseToolResultImages(fallback bool) bool {
	if c.record != nil && c.record.SupportsConverseToolResultImages != nil {
		return *c.record.SupportsConverseToolResultImages
	}
	return fallback
}
