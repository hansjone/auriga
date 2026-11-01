package schemas

import "slices"

var BedrockAPIValues = []BedrockAPI{
	BedrockAPIConverse,
	BedrockAPIInvoke,
	BedrockAPIMessages,
	BedrockAPIChatCompletions,
	BedrockAPIResponses,
}

type BedrockReasoningShape string
type ModelCapabilities struct {
	SupportsCachePoint              *bool `json:"supports_cache_point,omitempty"`
	SupportsInterleavedThinking     *bool `json:"supports_interleaved_thinking,omitempty"`
	SupportsSkills                  *bool `json:"supports_skills,omitempty"`
	SupportsMCP                     *bool `json:"supports_mcp,omitempty"`
	SupportsWebSearchDynamic        *bool `json:"supports_web_search_dynamic,omitempty"`
	SupportsWebFetch                *bool `json:"supports_web_fetch,omitempty"`
	SupportsCodeExecution           *bool `json:"supports_code_execution,omitempty"`
	SupportsBashTool                *bool `json:"supports_bash_tool,omitempty"`
	SupportsTextEditorTool          *bool `json:"supports_text_editor_tool,omitempty"`
	SupportsComputerToolset         *bool `json:"supports_computer_toolset,omitempty"`
	SupportsMemoryTool              *bool `json:"supports_memory_tool,omitempty"`
	SupportsToolSearch              *bool `json:"supports_tool_search,omitempty"`
	ToolNameMaxLength               *int  `json:"tool_name_max_length,omitempty"`
	SupportsNamespaceTools          *bool `json:"supports_namespace_tools,omitempty"`
	SupportsFilesAPI                *bool `json:"supports_files_api,omitempty"`
	SupportsCompaction              *bool `json:"supports_compaction,omitempty"`
	SupportsContextEditing          *bool `json:"supports_context_editing,omitempty"`
	SupportsContext1M               *bool `json:"supports_context_1m,omitempty"`
	SupportsFastMode                *bool `json:"supports_speed,omitempty"`
	SupportsAdaptiveThinking        *bool `json:"supports_adaptive_thinking,omitempty"`
	SupportsNativeEffort            *bool `json:"supports_native_effort,omitempty"`
	SupportsMidConversationSystem   *bool `json:"supports_mid_conversation_system_messages,omitempty"`
	SupportsMidConvOutputConfig     *bool `json:"supports_mid_conversation_output_config,omitempty"`
	SupportsSamplingParams          *bool `json:"supports_sampling_params,omitempty"`
	SupportsRedactThinking          *bool `json:"supports_redact_thinking,omitempty"`
	SupportsTaskBudgets             *bool `json:"supports_task_budgets,omitempty"`
	SupportsReasoningBudget         *bool `json:"supports_reasoning_budget,omitempty"`
	SupportsEagerInputStreaming     *bool `json:"supports_eager_input_streaming,omitempty"`
	SupportsAdvancedToolUse         *bool `json:"supports_advanced_tool_use,omitempty"`
	SupportsInputExamples           *bool `json:"supports_input_examples,omitempty"`
	SupportsAdvisorTool             *bool `json:"supports_advisor_tool,omitempty"`
	SupportsInferenceGeo            *bool `json:"supports_inference_geo,omitempty"`
	SupportsSafeguards              *bool `json:"supports_safeguards,omitempty"`
	SupportsPromptCachingScope      *bool `json:"supports_prompt_caching_scope,omitempty"`
	SupportsExtendedCacheTTL        *bool `json:"supports_extended_cache_ttl,omitempty"`
	SupportsPromptCacheBreakpoint   *bool `json:"supports_prompt_cache_breakpoint,omitempty"`
	SupportsReasoningContentBlocks  *bool `json:"supports_reasoning_content_blocks,omitempty"`
	SupportsMultimodalToolOutput    *bool `json:"supports_multimodal_tool_output,omitempty"`
	SupportsResponseSchemaWithTools *bool `json:"supports_response_schema_with_tools,omitempty"`
	SupportsForcedToolChoice        *bool `json:"supports_forced_tool_choice,omitempty"`
	SupportsPromptCacheBreakpoints  *bool `json:"supports_prompt_cache_breakpoints,omitempty"`
	SupportsAsyncTools              *bool `json:"supports_async_tools,omitempty"`
	SupportsCustomTools             *bool `json:"supports_custom_tools,omitempty"`
	SupportsDecisions               *bool `json:"supports_decisions,omitempty"`

	SupportsAssistantPrefill         *bool `json:"supports_assistant_prefill,omitempty"`
	SupportsSystemMessages           *bool `json:"supports_system_messages,omitempty"`
	SupportsDeveloperRole            *bool `json:"supports_developer_role,omitempty"`
	SupportsFunctionCalling          *bool `json:"supports_function_calling,omitempty"`
	SupportsParallelFunctionCalling  *bool `json:"supports_parallel_function_calling,omitempty"`
	SupportsToolChoice               *bool `json:"supports_tool_choice,omitempty"`
	SupportsReasoning                *bool `json:"supports_reasoning,omitempty"`
	SupportsResponseSchema           *bool `json:"supports_response_schema,omitempty"`
	SupportsReasoningWithToolCalls   *bool `json:"supports_reasoning_with_tool_calls,omitempty"`
	SupportsNoneReasoningEffort      *bool `json:"supports_none_reasoning_effort,omitempty"`
	SupportsReasoningSummaryDetailed *bool `json:"supports_reasoning_summary_detailed,omitempty"`
	SupportsReasoningSummaryConcise  *bool `json:"supports_reasoning_summary_concise,omitempty"`
	SupportsReasoningSummaryAuto     *bool `json:"supports_reasoning_summary_auto,omitempty"`
	SupportsServiceTier              *bool `json:"supports_service_tier,omitempty"`
	SupportsPromptCaching            *bool `json:"supports_prompt_caching,omitempty"`
	SupportsWebSearch                *bool `json:"supports_web_search,omitempty"`

	Mode *string `json:"mode,omitempty"`

	ServerSideModel *string `json:"server_side_model,omitempty"`

	SupportedEndpoints []string `json:"supported_endpoints,omitempty"`

	ServiceTiers []string `json:"service_tiers,omitempty"`

	ModelParameters []ModelParameterDescriptor `json:"model_parameters,omitempty"`

	ServerTools map[string]string `json:"server_tools,omitempty"`

	BetaHeaders map[string]string `json:"beta_headers,omitempty"`

	FieldNames map[string]string `json:"field_names,omitempty"`

	ServerToolAutoInjects map[string][]string `json:"server_tool_auto_injects,omitempty"`

	ServerToolImplicitBetas map[string]string `json:"server_tool_implicit_betas,omitempty"`

	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`

	UnsupportedFields map[string]bool `json:"unsupported_fields,omitempty"`

	ConditionallyUnsupportedFields map[string]string `json:"conditionally_unsupported_fields,omitempty"`

	SupportsReasoningEffort *bool `json:"supports_reasoning_effort,omitempty"`

	ReasoningEffortLevels []string `json:"reasoning_effort_levels,omitempty"`

	ReasoningEffortRenames map[string]string `json:"reasoning_effort_renames,omitempty"`

	SupportedReasoningContexts []string `json:"supported_reasoning_contexts,omitempty"`

	ReasoningBudget *BudgetControl `json:"reasoning_budget,omitempty"`

	SupportsReasoningDisable *bool `json:"supports_reasoning_disable,omitempty"`

	DefaultReasoningEffort *string `json:"default_reasoning_effort,omitempty"`

	SupportsBetweenToolsThinking *bool `json:"supports_between_tools_thinking,omitempty"`

	SupportsThinkingDisplayUpdates *bool `json:"supports_thinking_display_updates,omitempty"`

	SupportsDynamicReasoningBudget *bool `json:"supports_dynamic_reasoning_budget,omitempty"`

	MaxOutputTokens *int `json:"max_output_tokens,omitempty"`

	DefaultMaxTokens *int `json:"default_max_tokens,omitempty"`

	MinOutputTokens *int `json:"min_output_tokens,omitempty"`

	MinReasoningMaxTokens *int `json:"min_reasoning_max_tokens,omitempty"`

	DropToolChoicePin *bool `json:"drop_tool_choice_pin,omitempty"`

	SyntheticStructuredOutputToolPrefix *string `json:"synthetic_structured_output_tool_prefix,omitempty"`

	SyntheticSOToolChoiceOmitted *bool `json:"synthetic_so_tool_choice_omitted,omitempty"`

	RequestPath *string `json:"request_path,omitempty"`

	OuterAnthropicBetaHeaderSkipped *bool `json:"outer_anthropic_beta_header_skipped,omitempty"`

	APIVersion *string `json:"api_version,omitempty"`

	IsVertexMultiRegionOnly *bool `json:"vertex_multi_region_only,omitempty"`

	IsReasoningModel *bool `json:"is_reasoning_model,omitempty"`
	AlwaysReasoning  *bool `json:"always_reasoning,omitempty"`

	ToolChoiceStructSupported *bool `json:"tool_choice_struct_supported,omitempty"`

	ToolChoiceAnySupported *bool `json:"tool_choice_any_supported,omitempty"`

	PreservesPrediction *bool `json:"preserves_prediction,omitempty"`

	ReasoningRequired *bool `json:"reasoning_required,omitempty"`

	ReservedToolNamespaces []string `json:"reserved_tool_namespaces,omitempty"`

	SupportsRegexLookaround *bool `json:"supports_regex_lookaround,omitempty"`

	SupportsRegexNULEscape *bool `json:"supports_regex_nul_escape,omitempty"`

	SupportsSchemaPattern *bool `json:"supports_schema_pattern,omitempty"`

	SupportedSchemaFormats []string `json:"supported_schema_formats,omitempty"`

	AliasOf *string `json:"alias_of,omitempty"`

	RegionInferenceProfile *string `json:"region_inference_profile,omitempty"`

	IsCohereCommandR *bool `json:"is_cohere_command_r,omitempty"`

	BedrockAPIs []BedrockAPI `json:"bedrock_apis,omitempty"`

	BedrockReasoningShape BedrockReasoningShape `json:"bedrock_reasoning_shape,omitempty"`

	BedrockMantleBasePath BedrockMantleBasePath `json:"bedrock_mantle_base_path,omitempty"`

	BedrockRequiresSignedReasoning *bool `json:"bedrock_requires_signed_reasoning,omitempty"`

	SupportsConverseToolResultImages *bool `json:"supports_converse_tool_result_images,omitempty"`
}

var BedrockMantleBasePathValues = []BedrockMantleBasePath{
	BedrockMantleBasePathV1,
	BedrockMantleBasePathOpenAIV1,
}

type ModelParameterDescriptor struct {
	ID string `json:"id"`
}

var BedrockReasoningShapeValues = []BedrockReasoningShape{
	BedrockReasoningShapeText,
	BedrockReasoningShapeRedacted,
}

type EffortControl struct {
	Levels []string `json:"levels,omitempty"`

	Renames map[string]string `json:"renames,omitempty"`
}
type BedrockMantleBasePath string
type BedrockAPI string
type BudgetControl struct {
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
}

const (
	BedrockAPIConverse BedrockAPI = "converse"
	BedrockAPIInvoke   BedrockAPI = "invoke"
	BedrockAPIMessages BedrockAPI = "messages"

	BedrockAPIChatCompletions BedrockAPI = "chat_completions"
	BedrockAPIResponses       BedrockAPI = "responses"
)

func (a BedrockAPI) IsValid() bool {
	return slices.Contains(BedrockAPIValues, a)
}

const (
	BedrockReasoningShapeText BedrockReasoningShape = "reasoning_text"

	BedrockReasoningShapeRedacted BedrockReasoningShape = "redacted_content"
)

func (s BedrockReasoningShape) IsValid() bool {
	return slices.Contains(BedrockReasoningShapeValues, s)
}

const (
	BedrockMantleBasePathV1 BedrockMantleBasePath = "v1"

	BedrockMantleBasePathOpenAIV1 BedrockMantleBasePath = "openai/v1"
)

func (p BedrockMantleBasePath) IsValid() bool {
	return slices.Contains(BedrockMantleBasePathValues, p)
}
