package schemas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ResponsesShellCall struct {
	Environment     *ResponsesShellCallEnvironment `json:"environment,omitempty"`
	MaxOutputLength *int                           `json:"max_output_length,omitempty"`
}
type ResponsesTextConfigFormatJSONSchema struct {
	Name                 *string                     `json:"name,omitempty"`
	Schema               *JSONSchemaOrBool           `json:"schema,omitempty"`
	Description          *string                     `json:"description,omitempty"`
	Strict               *bool                       `json:"strict,omitempty"`
	AdditionalProperties *AdditionalPropertiesStruct `json:"additionalProperties,omitempty"`
	Properties           *OrderedMap                 `json:"properties,omitempty"`
	Required             []string                    `json:"required,omitempty"`
	Type                 *string                     `json:"type,omitempty"`

	Defs        *OrderedMap `json:"$defs,omitempty"`
	Definitions *OrderedMap `json:"definitions,omitempty"`
	Ref         *string     `json:"$ref,omitempty"`

	Items    *OrderedMap `json:"items,omitempty"`
	MinItems *int64      `json:"minItems,omitempty"`
	MaxItems *int64      `json:"maxItems,omitempty"`

	AnyOf []OrderedMap `json:"anyOf,omitempty"`
	OneOf []OrderedMap `json:"oneOf,omitempty"`
	AllOf []OrderedMap `json:"allOf,omitempty"`

	Format    *string `json:"format,omitempty"`
	Pattern   *string `json:"pattern,omitempty"`
	MinLength *int64  `json:"minLength,omitempty"`
	MaxLength *int64  `json:"maxLength,omitempty"`

	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`

	Title            *string     `json:"title,omitempty"`
	Default          interface{} `json:"default,omitempty"`
	Nullable         *bool       `json:"nullable,omitempty"`
	Enum             []string    `json:"enum,omitempty"`
	PropertyOrdering []string    `json:"propertyOrdering,omitempty"`

	keyOrder []string
}
type ResponsesToolCodeInterpreter struct {
	Container interface{} `json:"container"`

	Version *string `json:"code_execution_version,omitempty"`
}
type ResponsesToolSearchCall struct {
	ToolReferences []string `json:"tool_references,omitempty"`
}
type ResponsesToolCustomFormat struct {
	Type string `json:"type"`

	Definition *string `json:"definition,omitempty"`
	Syntax     *string `json:"syntax,omitempty"`
}
type ResponsesMessageOutputConfig struct {
	Effort *string `json:"effort,omitempty"`
}
type ResponsesToolWebSearchFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`

	TimeRangeFilter *Interval `json:"time_range_filter,omitempty"`
}
type ResponsesMessageContentBlock struct {
	Type      ResponsesMessageContentBlockType `json:"type"`
	FileID    *string                          `json:"file_id,omitempty"`
	Text      *string                          `json:"text,omitempty"`
	Signature *string                          `json:"signature,omitempty"`
	ToolName  *string                          `json:"tool_name,omitempty"`

	EncryptedContent *string `json:"encrypted_content,omitempty"`

	*ResponsesInputMessageContentBlockImage
	*ResponsesInputMessageContentBlockFile
	Audio *ResponsesInputMessageContentBlockAudio `json:"input_audio,omitempty"`

	*ResponsesOutputMessageContentText
	*ResponsesOutputMessageContentRefusal
	*ResponsesOutputMessageContentRenderedContent
	*ResponsesOutputMessageContentCompaction
	*ResponsesOutputMessageContentFallback

	ToolChange *ResponsesToolChangeTarget `json:"tool,omitempty"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`
	Citations    *Citations    `json:"citations,omitempty"`

	MediaResolution *MediaResolution `json:"media_resolution,omitempty"`

	PromptCacheBreakpoint *PromptCacheBreakpoint `json:"prompt_cache_breakpoint,omitempty"`

	GuardContent *GuardContent `json:"guard_content,omitempty"`
}
type ResponsesShellCallOutcome struct {
	Type     string `json:"type"`
	ExitCode *int   `json:"exit_code,omitempty"`
}
type ResponsesToolFileSearchComparisonFilter struct {
	Key   string      `json:"key"`
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}
type ResponsesApplyPatchCall struct {
	Operation *ResponsesApplyPatchOperation `json:"operation,omitempty"`
}
type ResponsesToolMCPAllowedToolsApprovalSetting struct {
	Setting *string                                     `json:",omitempty"`
	Always  *ResponsesToolMCPAllowedToolsApprovalFilter `json:"always,omitempty"`
	Never   *ResponsesToolMCPAllowedToolsApprovalFilter `json:"never,omitempty"`
}
type BifrostResponsesResponse struct {
	ID     *string `json:"id,omitempty"`
	Object string  `json:"object"`

	Background           *bool                               `json:"background,omitempty"`
	Conversation         *ResponsesResponseConversation      `json:"conversation,omitempty"`
	CreatedAt            int                                 `json:"created_at"`
	CompletedAt          *int                                `json:"completed_at"`
	Error                *ResponsesResponseError             `json:"error"`
	Include              []string                            `json:"include,omitempty"`
	IncompleteDetails    *ResponsesResponseIncompleteDetails `json:"incomplete_details"`
	Instructions         *ResponsesResponseInstructions      `json:"instructions"`
	MaxOutputTokens      *int                                `json:"max_output_tokens"`
	MaxToolCalls         *int                                `json:"max_tool_calls"`
	Metadata             *map[string]any                     `json:"metadata"`
	Model                string                              `json:"model"`
	Output               []ResponsesMessage                  `json:"output"`
	ParallelToolCalls    *bool                               `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID   *string                             `json:"previous_response_id"`
	Prompt               *ResponsesPrompt                    `json:"prompt,omitempty"`
	PromptCacheKey       *string                             `json:"prompt_cache_key"`
	PromptCacheRetention *string                             `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *PromptCacheOptions                 `json:"prompt_cache_options,omitempty"`
	PresencePenalty      *float64                            `json:"presence_penalty,omitempty"`
	FrequencyPenalty     *float64                            `json:"frequency_penalty,omitempty"`
	Reasoning            *ResponsesParametersReasoning       `json:"reasoning"`
	SafetyIdentifier     *string                             `json:"safety_identifier"`
	ServiceTier          *BifrostServiceTier                 `json:"service_tier"`
	Speed                *string                             `json:"speed,omitempty"`
	InferenceGeo         *string                             `json:"inference_geo,omitempty"`
	Diagnostics          *CacheDiagnostics                   `json:"diagnostics,omitempty"`
	SafeguardResults     json.RawMessage                     `json:"safeguard_results,omitempty"`
	Container            *ResponsesResponseContainer         `json:"container,omitempty"`
	Status               *string                             `json:"status,omitempty"`
	StreamOptions        *ResponsesStreamOptions             `json:"stream_options,omitempty"`
	StopReason           *string                             `json:"stop_reason,omitempty"`
	StopDetails          *ResponsesStopDetails               `json:"stop_details,omitempty"`
	StopSequence         *string                             `json:"stop_sequence,omitempty"`
	Store                *bool                               `json:"store,omitempty"`
	Temperature          *float64                            `json:"temperature,omitempty"`
	Text                 *ResponsesTextConfig                `json:"text,omitempty"`
	TopLogProbs          *int                                `json:"top_logprobs,omitempty"`
	TopP                 *float64                            `json:"top_p,omitempty"`
	ToolChoice           *ResponsesToolChoice                `json:"tool_choice,omitempty"`
	Tools                []ResponsesTool                     `json:"tools"`
	Truncation           *string                             `json:"truncation,omitempty"`
	Usage                *ResponsesResponseUsage             `json:"usage"`
	ToolUsage            *ToolUsage                          `json:"tool_usage,omitempty"`
	ExtraFields          BifrostResponseExtraFields          `json:"extra_fields,omitzero"`
	ProviderExtraFields  map[string]interface{}              `json:"provider_extra_fields,omitempty"`

	SearchResults []SearchResult `json:"search_results,omitempty"`
	Videos        []VideoResult  `json:"videos,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
}
type BifrostResponsesDeleteRequest struct {
	Provider       ModelProvider `json:"provider"`
	ResponseID     string        `json:"response_id"`
	RawRequestBody []byte        `json:"-"`
}
type ResponsesToolMessageError struct {
	ResponsesToolMessageErrorStr    *string
	ResponsesToolMessageErrorStruct *ResponsesToolMessageErrorStruct
}
type ResponsesShellToolCallAction struct {
	Commands        []string `json:"commands"`
	TimeoutMS       *int     `json:"timeout_ms,omitempty"`
	MaxOutputLength *int     `json:"max_output_length,omitempty"`
}
type ResponsesCodeExecutionFileOutput struct {
	FileID string `json:"file_id"`
}
type ResponsesMCPApprovalResponse struct {
	ApprovalRequestID string  `json:"approval_request_id"`
	Approve           bool    `json:"approve"`
	Reason            *string `json:"reason,omitempty"`
}
type ResponsesToolMCPToolConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	DeferLoading *bool `json:"defer_loading,omitempty"`
}
type Interval struct {
	StartTime time.Time `json:"start_time,omitempty"`

	EndTime time.Time `json:"end_time,omitempty"`
}
type ResponsesToolShellEnvironment struct {
	Type string `json:"type"`

	FileIDs       []string                         `json:"file_ids,omitempty"`
	MemoryLimit   *string                          `json:"memory_limit,omitempty"`
	NetworkPolicy *ResponsesToolShellNetworkPolicy `json:"network_policy,omitempty"`

	Skills []ResponsesToolShellSkill `json:"skills,omitempty"`

	ContainerID *string `json:"container_id,omitempty"`
}
type ResponsesToolChoiceAllowedToolDef struct {
	Type        string  `json:"type"`
	Name        *string `json:"name,omitempty"`
	ServerLabel *string `json:"server_label,omitempty"`
}
type ResponsesToolWebSearch struct {
	ExternalWebAccess  *bool                               `json:"external_web_access,omitempty"`
	Filters            *ResponsesToolWebSearchFilters      `json:"filters,omitempty"`
	SearchContentTypes []string                            `json:"search_content_types,omitempty"`
	SearchContextSize  *string                             `json:"search_context_size,omitempty"`
	UserLocation       *ResponsesToolWebSearchUserLocation `json:"user_location,omitempty"`

	MaxUses *int `json:"max_uses,omitempty"`
}
type ResponsesStopDetails struct {
	Type             string  `json:"type"`
	Category         *string `json:"category,omitempty"`
	Explanation      *string `json:"explanation,omitempty"`
	RecommendedModel *string `json:"recommended_model,omitempty"`

	FallbackCreditToken *string `json:"fallback_credit_token,omitempty"`

	FallbackHasPrefillClaim *bool `json:"fallback_has_prefill_claim,omitempty"`
}
type ResponsesFunctionToolCallOutput struct {
	ResponsesFunctionToolCallOutputStr    *string
	ResponsesFunctionToolCallOutputBlocks []ResponsesMessageContentBlock
}
type ResponsesToolMCPAllowedToolsApprovalFilter struct {
	ReadOnly  *bool    `json:"read_only,omitempty"`
	ToolNames []string `json:"tool_names,omitempty"`
}
type NamespaceToolAlias struct {
	Namespace string
	Name      string
}
type ResponsesToolCustom struct {
	Format *ResponsesToolCustomFormat `json:"format,omitempty"`
}
type ResponsesToolWebSearchUserLocation struct {
	City     *string `json:"city,omitempty"`
	Country  *string `json:"country,omitempty"`
	Region   *string `json:"region,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
	Type     *string `json:"type,omitempty"`

	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}
type ResponsesToolFileSearchCompoundFilter struct {
	Filters []ResponsesToolFileSearchFilter `json:"filters"`
}
type ResponsesImageGenerationCall struct {
	Result string `json:"result"`

	Background    *string `json:"background,omitempty"`
	OutputFormat  *string `json:"output_format,omitempty"`
	Quality       *string `json:"quality,omitempty"`
	RevisedPrompt *string `json:"revised_prompt,omitempty"`
	Size          *string `json:"size,omitempty"`
}
type ResponsesToolToolSearch struct {
	Execution  *string                 `json:"execution,omitempty"`
	Parameters *ToolFunctionParameters `json:"parameters,omitempty"`
}
type ResponsesComputerToolCall struct {
	PendingSafetyChecks []ResponsesComputerToolCallPendingSafetyCheck `json:"pending_safety_checks,omitempty"`
	Actions             []ResponsesComputerToolCallAction             `json:"actions,omitempty"`
}
type ResponsesToolShell struct {
	Environment *ResponsesToolShellEnvironment `json:"environment,omitempty"`
}
type ResponsesMessage struct {
	ID     *string               `json:"id,omitempty"`
	Type   *ResponsesMessageType `json:"type,omitempty"`
	Status *string               `json:"status,omitempty"`

	Phase *string `json:"phase,omitempty"`

	Role    *ResponsesMessageRoleType `json:"role,omitempty"`
	Content *ResponsesMessageContent  `json:"content,omitempty"`

	OutputConfig *ResponsesMessageOutputConfig `json:"output_config,omitempty"`

	Author    json.RawMessage `json:"author,omitempty"`
	Recipient json.RawMessage `json:"recipient,omitempty"`

	ToolSearchOutputTools json.RawMessage `json:"-"`

	AdditionalTools json.RawMessage `json:"-"`

	ProviderNativeParts json.RawMessage `json:"-"`

	*ResponsesToolMessage

	CacheControl *CacheControl `json:"cache_control,omitempty"`

	*ResponsesReasoning

	rawPreserved []byte
}
type ResponsesToolType string
type ResponsesMessageContent struct {
	ContentStr *string

	ContentBlocks []ResponsesMessageContentBlock
}
type ResponsesResponseOutputTokens struct {
	TextTokens               int  `json:"text_tokens,omitempty"`
	AcceptedPredictionTokens int  `json:"accepted_prediction_tokens,omitempty"`
	AudioTokens              int  `json:"audio_tokens,omitempty"`
	ImageTokens              *int `json:"image_tokens,omitempty"`
	ReasoningTokens          int  `json:"reasoning_tokens"`
	RejectedPredictionTokens int  `json:"rejected_prediction_tokens,omitempty"`
	CitationTokens           *int `json:"citation_tokens,omitempty"`

	NumSearchQueries *int `json:"num_search_queries,omitempty"`

	StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens,omitempty"`
	CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens,omitempty"`
}
type ResponsesResponseInputTokens struct {
	TextTokens  int `json:"text_tokens,omitempty"`
	AudioTokens int `json:"audio_tokens,omitempty"`
	ImageTokens int `json:"image_tokens,omitempty"`

	CachedReadTokens        int                          `json:"cached_read_tokens"`
	CachedWriteTokens       int                          `json:"cached_write_tokens"`
	CachedWriteTokenDetails *ChatCachedWriteTokenDetails `json:"cached_write_token_details,omitempty"`

	StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens,omitempty"`
	CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens,omitempty"`
}
type BifrostResponsesCancelRequest struct {
	Provider       ModelProvider `json:"provider"`
	ResponseID     string        `json:"response_id"`
	RawRequestBody []byte        `json:"-"`
}
type ResponsesToolFileSearchFilter struct {
	Type string `json:"type"`

	*ResponsesToolFileSearchComparisonFilter
	*ResponsesToolFileSearchCompoundFilter
}
type ResponsesToolFileSearchRankingOptions struct {
	Ranker         *string  `json:"ranker,omitempty"`
	ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}
type ResponsesOutputMessageContentFallback struct {
	FromModel string `json:"from_model,omitempty"`
	ToModel   string `json:"to_model,omitempty"`

	TriggerType     string  `json:"trigger_type,omitempty"`
	TriggerCategory *string `json:"trigger_category,omitempty"`
}
type ResponsesToolImageGenerationInputImageMask struct {
	FileID   *string `json:"file_id,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}
type ResponsesShellCallEnvironment struct {
	Type        string  `json:"type"`
	ContainerID *string `json:"container_id,omitempty"`
}
type MediaResolution struct {
	Level     string `json:"level,omitempty"`
	NumTokens *int32 `json:"num_tokens,omitempty"`
}
type ResponsesOutputMessageContentTextLogProb struct {
	Bytes       []int     `json:"bytes"`
	LogProb     float64   `json:"logprob"`
	Token       string    `json:"token"`
	TopLogProbs []LogProb `json:"top_logprobs"`
}
type JSONSchemaOrBool struct {
	SchemaBool *bool
	SchemaMap  *OrderedMap
}
type ResponsesToolAdvisor struct {
	Model     string                       `json:"model,omitempty"`
	MaxUses   *int                         `json:"max_uses,omitempty"`
	MaxTokens *int                         `json:"max_tokens,omitempty"`
	Caching   *ResponsesToolAdvisorCaching `json:"caching,omitempty"`
}
type BifrostResponsesRequest struct {
	Provider       ModelProvider        `json:"provider"`
	Model          string               `json:"model"`
	Input          []ResponsesMessage   `json:"input,omitempty"`
	Params         *ResponsesParameters `json:"params,omitempty"`
	Fallbacks      []Fallback           `json:"fallbacks,omitempty"`
	RawRequestBody []byte               `json:"-"`

	NamespaceToolAliases map[string]NamespaceToolAlias `json:"-"`

	anthropicBillingHeader *anthropicBillingHeader
}
type BifrostResponsesStreamResponse struct {
	Type           ResponsesStreamResponseType `json:"type"`
	SequenceNumber int                         `json:"sequence_number"`

	Response *BifrostResponsesResponse `json:"response,omitempty"`

	OutputIndex *int `json:"output_index,omitempty"`

	Item *ResponsesMessage `json:"item,omitempty"`

	SummaryIndex *int `json:"summary_index,omitempty"`

	ContentIndex *int                          `json:"content_index,omitempty"`
	ItemID       *string                       `json:"item_id,omitempty"`
	Part         *ResponsesMessageContentBlock `json:"part,omitempty"`

	Delta     *string `json:"delta,omitempty"`
	Signature *string `json:"signature,omitempty"`

	Obfuscation *string                                    `json:"obfuscation,omitempty"`
	LogProbs    []ResponsesOutputMessageContentTextLogProb `json:"logprobs"`

	Text *string `json:"text,omitempty"`

	Refusal *string `json:"refusal,omitempty"`

	Arguments *string `json:"arguments,omitempty"`

	Input *string `json:"input,omitempty"`

	Command          *string                           `json:"command,omitempty"`
	CommandIndex     *int                              `json:"command_index,omitempty"`
	Output           []ResponsesShellCallOutputContent `json:"output,omitempty"`
	ShellOutputDelta *ResponsesShellCallOutputDelta    `json:"-"`

	Diff *string `json:"diff,omitempty"`

	PartialImageB64   *string `json:"partial_image_b64,omitempty"`
	PartialImageIndex *int    `json:"partial_image_index,omitempty"`

	Annotation      *ResponsesOutputMessageContentTextAnnotation `json:"annotation,omitempty"`
	AnnotationIndex *int                                         `json:"annotation_index,omitempty"`

	Error   *ResponsesResponseError `json:"error,omitempty"`
	Code    *string                 `json:"code,omitempty"`
	Message *string                 `json:"message,omitempty"`
	Param   *string                 `json:"param,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`

	SafeguardResults json.RawMessage `json:"safeguard_results,omitempty"`

	SearchResults []SearchResult `json:"search_results,omitempty"`
	Videos        []VideoResult  `json:"videos,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
}
type ResponsesToolChangeTarget struct {
	Type       string         `json:"type"`
	Name       *string        `json:"name,omitempty"`
	ServerName *string        `json:"server_name,omitempty"`
	Definition *ResponsesTool `json:"definition,omitempty"`
}
type ResponsesWebSearchToolCallActionSearchSource struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
	Name string `json:"name,omitempty"`

	Title            *string `json:"title,omitempty"`
	EncryptedContent *string `json:"encrypted_content,omitempty"`
	PageAge          *string `json:"page_age,omitempty"`

	ImageURL *string `json:"image_url,omitempty"`
	Domain   *string `json:"domain,omitempty"`
}
type Citations struct {
	Enabled *bool `json:"enabled,omitempty"`
}
type ResponsesParametersReasoning struct {
	Context         *string `json:"context,omitempty"`
	Effort          *string `json:"effort"`
	GenerateSummary *string `json:"generate_summary,omitempty"`
	Mode            *string `json:"mode,omitempty"`
	Summary         *string `json:"summary"`
	MaxTokens       *int    `json:"max_tokens,omitempty"`
	Type            *string `json:"type,omitempty"`
}
type ResponsesMCPListTools struct {
	Tools []ResponsesMCPTool `json:"tools"`
}
type ResponsesCodeInterpreterOutputImage struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
type ResponsesShellCallOutputContent struct {
	Stdout    string                    `json:"stdout"`
	Stderr    string                    `json:"stderr"`
	Outcome   ResponsesShellCallOutcome `json:"outcome"`
	CreatedBy *string                   `json:"created_by,omitempty"`
}

var clientToolSearchNames = []string{"tool_search", "ToolSearch"}

type ResponsesResponseIncompleteDetails struct {
	Reason string `json:"reason"`
}
type ResponsesResponseUsage struct {
	Type                *string                        `json:"type,omitempty"`
	Model               *string                        `json:"model,omitempty"`
	InputTokens         int                            `json:"input_tokens"`
	InputTokensDetails  *ResponsesResponseInputTokens  `json:"input_tokens_details"`
	OutputTokens        int                            `json:"output_tokens"`
	OutputTokensDetails *ResponsesResponseOutputTokens `json:"output_tokens_details"`
	TotalTokens         int                            `json:"total_tokens"`
	AudioSeconds        *float64                       `json:"audio_seconds,omitempty"`
	Cost                *BifrostCost                   `json:"cost,omitempty"`
	Iterations          []ResponsesResponseUsage       `json:"iterations,omitempty"`
	ToolUsage           *ToolUsage                     `json:"tool_usage,omitempty"`

	NumSourcesUsed             *int                                 `json:"num_sources_used,omitempty"`
	NumServerSideToolsUsed     *int                                 `json:"num_server_side_tools_used,omitempty"`
	CostInUsdTicks             *int64                               `json:"cost_in_usd_ticks,omitempty"`
	ServerSideToolUsageDetails *ResponsesServerSideToolUsageDetails `json:"server_side_tool_usage_details,omitempty"`
	ContextDetails             *ResponsesContextDetails             `json:"context_details,omitempty"`
}
type CacheMissReason struct {
	Type                   string `json:"type"`
	CacheMissedInputTokens *int   `json:"cache_missed_input_tokens,omitempty"`
}
type PromptCacheOptions struct {
	Mode *string `json:"mode,omitempty"`
	TTL  *string `json:"ttl,omitempty"`
}
type ResponsesMessageType string
type BifrostResponsesDeleteResponse struct {
	ID          string                     `json:"id"`
	Object      string                     `json:"object,omitempty"`
	Deleted     bool                       `json:"deleted"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCompactionRequest struct {
	Provider             ModelProvider          `json:"provider"`
	Model                string                 `json:"model"`
	Input                []ResponsesMessage     `json:"input,omitempty"`
	Instructions         *string                `json:"instructions,omitempty"`
	PreviousResponseID   *string                `json:"previous_response_id,omitempty"`
	PromptCacheKey       *string                `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention *string                `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *PromptCacheOptions    `json:"prompt_cache_options,omitempty"`
	ServiceTier          *BifrostServiceTier    `json:"service_tier,omitempty"`
	Fallbacks            []Fallback             `json:"fallbacks,omitempty"`
	ExtraParams          map[string]interface{} `json:"-"`
	RawRequestBody       []byte                 `json:"-"`
}
type ResponsesToolChoice struct {
	ResponsesToolChoiceStr    *string
	ResponsesToolChoiceStruct *ResponsesToolChoiceStruct
}
type ResponsesComputerToolCallOutput struct {
	AcknowledgedSafetyChecks []ResponsesComputerToolCallAcknowledgedSafetyCheck `json:"acknowledged_safety_checks,omitempty"`
}
type ResponsesComputerToolCallActionPath struct {
	X int `json:"x"`
	Y int `json:"y"`
}
type ResponsesToolMessage struct {
	CallID    *string                           `json:"call_id,omitempty"`
	Name      *string                           `json:"name,omitempty"`
	Namespace *string                           `json:"namespace,omitempty"`
	Arguments *string                           `json:"arguments,omitempty"`
	Execution *string                           `json:"execution,omitempty"`
	Async     *bool                             `json:"async,omitempty"`
	Output    *ResponsesToolMessageOutputStruct `json:"output,omitempty"`
	Action    *ResponsesToolMessageActionStruct `json:"action,omitempty"`
	Error     *ResponsesToolMessageError        `json:"error,omitempty"`

	Caller *ResponsesToolCaller `json:"tool_caller,omitempty"`

	ToolsetName *string `json:"toolset_name,omitempty"`

	ItemCaller  *ResponsesShellCaller `json:"caller,omitempty"`
	CreatedBy   *string               `json:"created_by,omitempty"`
	ServerLabel *string               `json:"server_label,omitempty"`

	*ResponsesFileSearchToolCall
	*ResponsesComputerToolCall
	*ResponsesComputerToolCallOutput
	*ResponsesCodeInterpreterToolCall
	*ResponsesCustomToolCall
	*ResponsesImageGenerationCall

	*ResponsesMCPListTools
	*ResponsesMCPApprovalResponse

	*ResponsesShellCall

	*ResponsesApplyPatchCall

	*ResponsesAdvisorCall

	*ResponsesToolSearchCall

	*ResponsesWebFetchCall

	*ResponsesCodeExecutionCall
}
type ResponsesToolImageGeneration struct {
	Action            *string                                     `json:"action,omitempty"`
	Background        *string                                     `json:"background,omitempty"`
	InputFidelity     *string                                     `json:"input_fidelity,omitempty"`
	InputImageMask    *ResponsesToolImageGenerationInputImageMask `json:"input_image_mask,omitempty"`
	Model             *string                                     `json:"model,omitempty"`
	Moderation        *string                                     `json:"moderation,omitempty"`
	OutputCompression *int                                        `json:"output_compression,omitempty"`
	OutputFormat      *string                                     `json:"output_format,omitempty"`
	PartialImages     *int                                        `json:"partial_images,omitempty"`
	Quality           *string                                     `json:"quality,omitempty"`
	Size              *string                                     `json:"size,omitempty"`
}
type ResponsesWebFetchCall struct {
	ResultType  string                     `json:"web_fetch_result_type,omitempty"`
	URL         *string                    `json:"web_fetch_result_url,omitempty"`
	RetrievedAt *string                    `json:"web_fetch_retrieved_at,omitempty"`
	Document    *ResponsesWebFetchDocument `json:"web_fetch_document,omitempty"`
	ErrorCode   *string                    `json:"web_fetch_error_code,omitempty"`
}
type ResponsesInputMessageContentBlockImage struct {
	ImageURL *string `json:"image_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}
type ResponsesResponseContainer struct {
	ID        string  `json:"id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}
type ResponsesToolWebFetch struct {
	MaxUses           *int                           `json:"max_uses,omitempty"`
	Filters           *ResponsesToolWebSearchFilters `json:"filters,omitempty"`
	MaxContentTokens  *int                           `json:"max_content_tokens,omitempty"`
	UseCache          *bool                          `json:"use_cache,omitempty"`
	ResponseInclusion *string                        `json:"response_inclusion,omitempty"`
}
type ResponsesReasoningSummary struct {
	Type ResponsesReasoningContentBlockType `json:"type"`
	Text string                             `json:"text"`
}
type BifrostCompactionResponse struct {
	ID          *string                    `json:"id,omitempty"`
	Object      string                     `json:"object"`
	Model       string                     `json:"model,omitempty"`
	CreatedAt   int                        `json:"created_at"`
	Output      []ResponsesMessage         `json:"output"`
	Usage       *ResponsesResponseUsage    `json:"usage,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type ResponsesFileSearchToolCall struct {
	Queries []string                            `json:"queries"`
	Results []ResponsesFileSearchToolCallResult `json:"results,omitempty"`
}
type CacheDiagnostics struct {
	CacheMissReason *CacheMissReason `json:"cache_miss_reason"`
}
type ResponsesToolShellNetworkPolicy struct {
	Type           string                           `json:"type"`
	AllowedDomains []string                         `json:"allowed_domains,omitempty"`
	DomainSecrets  []ResponsesToolShellDomainSecret `json:"domain_secrets,omitempty"`
}
type ResponsesResponseError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ResponsesOutputMessageContentRefusal struct {
	Refusal string `json:"refusal"`
}
type ResponsesToolFunction struct {
	Parameters   *ToolFunctionParameters `json:"parameters,omitempty"`
	Strict       *bool                   `json:"strict"`
	OutputSchema *OrderedMap             `json:"output_schema,omitempty"`
}
type BifrostResponsesRetrieveRequest struct {
	Provider           ModelProvider `json:"provider"`
	ResponseID         string        `json:"response_id"`
	Include            []string      `json:"include,omitempty"`
	StartingAfter      *int          `json:"starting_after,omitempty"`
	IncludeObfuscation *bool         `json:"include_obfuscation,omitempty"`

	Stream         *bool  `json:"stream,omitempty"`
	RawRequestBody []byte `json:"-"`
}
type ResponsesCodeInterpreterToolCall struct {
	Code        *string                          `json:"code"`
	ContainerID string                           `json:"container_id"`
	Outputs     []ResponsesCodeInterpreterOutput `json:"outputs"`
}
type ResponsesComputerToolCallPendingSafetyCheck struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ResponsesToolMessageActionStruct struct {
	ResponsesToolCallActionStr        *string
	ResponsesComputerToolCallAction   *ResponsesComputerToolCallAction
	ResponsesWebSearchToolCallAction  *ResponsesWebSearchToolCallAction
	ResponsesWebFetchToolCallAction   *ResponsesWebFetchToolCallAction
	ResponsesLocalShellToolCallAction *ResponsesLocalShellToolCallAction
	ResponsesShellToolCallAction      *ResponsesShellToolCallAction
	ResponsesMCPApprovalRequestAction *ResponsesMCPApprovalRequestAction
}
type ResponsesComputerToolCallAction struct {
	Type    string                                `json:"type"`
	X       *int                                  `json:"x,omitempty"`
	Y       *int                                  `json:"y,omitempty"`
	Button  *string                               `json:"button,omitempty"`
	Path    []ResponsesComputerToolCallActionPath `json:"path,omitempty"`
	Keys    []string                              `json:"keys,omitempty"`
	ScrollX *int                                  `json:"scroll_x,omitempty"`
	ScrollY *int                                  `json:"scroll_y,omitempty"`
	Text    *string                               `json:"text,omitempty"`
	Region  []int                                 `json:"region,omitempty"`
}
type ResponsesCodeInterpreterOutput struct {
	*ResponsesCodeInterpreterOutputLogs
	*ResponsesCodeInterpreterOutputImage
}
type PromptCacheBreakpoint struct {
	Mode *string `json:"mode,omitempty"`
}

var ErrUnsatisfiableSchema = errors.New("json schema is the boolean schema 'false', which no output can satisfy")

type ResponsesCodeExecutionCall struct {
	ToolName string `json:"code_execution_tool_name,omitempty"`

	Input *string `json:"code_execution_input,omitempty"`

	ResultType string `json:"code_execution_result_type,omitempty"`

	Stdout          *string `json:"code_execution_stdout,omitempty"`
	Stderr          *string `json:"code_execution_stderr,omitempty"`
	ReturnCode      *int    `json:"code_execution_return_code,omitempty"`
	EncryptedStdout *string `json:"code_execution_encrypted_stdout,omitempty"`

	FileType     *string  `json:"code_execution_file_type,omitempty"`
	FileContent  *string  `json:"code_execution_file_content,omitempty"`
	StartLine    *int     `json:"code_execution_start_line,omitempty"`
	NumLines     *int     `json:"code_execution_num_lines,omitempty"`
	TotalLines   *int     `json:"code_execution_total_lines,omitempty"`
	IsFileUpdate *bool    `json:"code_execution_is_file_update,omitempty"`
	OldStart     *int     `json:"code_execution_old_start,omitempty"`
	OldLines     *int     `json:"code_execution_old_lines,omitempty"`
	NewStart     *int     `json:"code_execution_new_start,omitempty"`
	NewLines     *int     `json:"code_execution_new_lines,omitempty"`
	Lines        []string `json:"code_execution_lines,omitempty"`

	ErrorCode *string `json:"code_execution_error_code,omitempty"`

	Files []ResponsesCodeExecutionFileOutput `json:"code_execution_files,omitempty"`

	ContainerExpiresAt *string `json:"code_execution_container_expires_at,omitempty"`

	Caller *ResponsesToolCaller `json:"code_execution_caller,omitempty"`
}
type ResponsesToolMessageErrorStruct struct {
	Type    string          `json:"type"`
	Code    *int            `json:"code,omitempty"`
	Message *string         `json:"message,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}
type ResponsesStreamResponseType string
type ResponsesToolFileSearch struct {
	VectorStoreIDs []string                               `json:"vector_store_ids"`
	Filters        *ResponsesToolFileSearchFilter         `json:"filters,omitempty"`
	MaxNumResults  *int                                   `json:"max_num_results,omitempty"`
	RankingOptions *ResponsesToolFileSearchRankingOptions `json:"ranking_options,omitempty"`
}
type ResponsesInputMessageContentBlockAudio struct {
	Format string `json:"format"`
	Data   string `json:"data"`
}
type BifrostResponsesInputItemsResponse struct {
	Object      string                     `json:"object"`
	Data        []ResponsesMessage         `json:"data"`
	HasMore     bool                       `json:"has_more"`
	FirstID     string                     `json:"first_id,omitempty"`
	LastID      string                     `json:"last_id,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type ResponsesTextConfigFormat struct {
	Type        string                               `json:"type"`
	Name        *string                              `json:"name,omitempty"`
	Description *string                              `json:"description,omitempty"`
	JSONSchema  *ResponsesTextConfigFormatJSONSchema `json:"schema,omitempty"`
	Strict      *bool                                `json:"strict,omitempty"`
}
type ResponsesCodeInterpreterOutputLogs struct {
	Logs string `json:"logs"`
	Type string `json:"type"`
}
type ResponsesToolNamespace struct {
	Tools []ResponsesTool `json:"tools,omitempty"`
}
type ResponsesWebSearchToolCallAction struct {
	Type    string                                         `json:"type"`
	URL     *string                                        `json:"url,omitempty"`
	Query   *string                                        `json:"query,omitempty"`
	Queries []string                                       `json:"queries,omitempty"`
	Sources []ResponsesWebSearchToolCallActionSearchSource `json:"sources,omitempty"`
	Pattern *string                                        `json:"pattern,omitempty"`

	ImageQueries []string `json:"image_queries,omitempty"`
}
type ResponsesWebFetchToolCallAction struct {
	Type string `json:"type,omitempty"`
	URL  string `json:"url"`
}
type ResponsesOutputMessageContentCompaction struct {
	Summary string `json:"summary,omitempty"`

	ToolChanges []ResponsesMessageContentBlock `json:"tool_changes,omitempty"`
}
type ResponsesAdvisorCall struct {
	ResultType       string  `json:"result_type,omitempty"`
	Text             *string `json:"advisor_text,omitempty"`
	EncryptedContent *string `json:"advisor_encrypted_content,omitempty"`
	ErrorCode        *string `json:"advisor_error_code,omitempty"`
	StopReason       *string `json:"advisor_stop_reason,omitempty"`
}
type ResponsesToolMessageOutputStruct struct {
	ResponsesToolCallOutputStr            *string
	ResponsesFunctionToolCallOutputBlocks []ResponsesMessageContentBlock
	ResponsesComputerToolCallOutput       *ResponsesComputerToolCallOutputData
	ResponsesShellCallOutput              []ResponsesShellCallOutputContent
}
type ResponsesResponseConversationStruct struct {
	ID string `json:"id"`
}
type GuardContent struct {
	Qualifiers []string `json:"qualifiers,omitempty"`
}
type ResponsesMessageRoleType string
type ResponsesResponseConversation struct {
	ResponsesResponseConversationStr    *string
	ResponsesResponseConversationStruct *ResponsesResponseConversationStruct
}
type ResponsesShellCaller struct {
	Type     string  `json:"type"`
	CallerID *string `json:"caller_id,omitempty"`
}
type ResponsesCustomToolCall struct {
	Input string `json:"input"`
}
type ResponsesToolCaller struct {
	Type   string  `json:"type"`
	ToolID *string `json:"tool_id,omitempty"`
}
type ResponsesLocalShellToolCallAction struct {
	Command          []string `json:"command"`
	Env              []string `json:"env"`
	Type             string   `json:"type"`
	TimeoutMS        *int     `json:"timeout_ms,omitempty"`
	User             *string  `json:"user,omitempty"`
	WorkingDirectory *string  `json:"working_directory,omitempty"`
}
type ResponsesOutputMessageContentText struct {
	Annotations []ResponsesOutputMessageContentTextAnnotation `json:"annotations"`
	LogProbs    []ResponsesOutputMessageContentTextLogProb    `json:"logprobs,omitzero"`
}
type BifrostResponsesInputItemsRequest struct {
	Provider       ModelProvider `json:"provider"`
	ResponseID     string        `json:"response_id"`
	After          string        `json:"after,omitempty"`
	Include        []string      `json:"include,omitempty"`
	Limit          *int          `json:"limit,omitempty"`
	Order          string        `json:"order,omitempty"`
	RawRequestBody []byte        `json:"-"`
}
type ResponsesToolMCPAllowedTools struct {
	ToolNames []string
	Filter    *ResponsesToolMCPAllowedToolsFilter
}
type ResponsesContextDetails struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
type ResponsesReasoningContentBlockType string
type ResponsesToolMCP struct {
	ServerLabel       string                                       `json:"server_label"`
	AllowedTools      *ResponsesToolMCPAllowedTools                `json:"allowed_tools,omitempty"`
	Authorization     *string                                      `json:"authorization,omitempty"`
	ConnectorID       *string                                      `json:"connector_id,omitempty"`
	Headers           *map[string]string                           `json:"headers,omitempty"`
	RequireApproval   *ResponsesToolMCPAllowedToolsApprovalSetting `json:"require_approval,omitempty"`
	ServerDescription *string                                      `json:"server_description,omitempty"`
	ServerURL         *string                                      `json:"server_url,omitempty"`
	TunnelID          *string                                      `json:"tunnel_id,omitempty"`

	DefaultConfig *ResponsesToolMCPToolConfig            `json:"default_config,omitempty"`
	ToolConfigs   map[string]*ResponsesToolMCPToolConfig `json:"tool_configs,omitempty"`
}
type ResponsesComputerToolCallOutputData struct {
	Type     string  `json:"type"`
	FileID   *string `json:"file_id,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}
type ResponsesToolMCPAllowedToolsFilter struct {
	ReadOnly  *bool    `json:"read_only,omitempty"`
	ToolNames []string `json:"tool_names,omitempty"`
}
type ResponsesMCPTool struct {
	Name        string          `json:"name"`
	InputSchema map[string]any  `json:"input_schema"`
	Description *string         `json:"description,omitempty"`
	Annotations *map[string]any `json:"annotations,omitempty"`
}
type ResponsesPrompt struct {
	ID        string         `json:"id"`
	Variables map[string]any `json:"variables"`
	Version   *string        `json:"version,omitempty"`
}
type ResponsesToolShellSkill struct {
	Type        *string                        `json:"type,omitempty"`
	Name        *string                        `json:"name,omitempty"`
	Description *string                        `json:"description,omitempty"`
	Path        *string                        `json:"path,omitempty"`
	SkillID     *string                        `json:"skill_id,omitempty"`
	Version     *string                        `json:"version,omitempty"`
	Source      *ResponsesToolShellSkillSource `json:"source,omitempty"`
}
type ResponsesToolChoiceStruct struct {
	Type        ResponsesToolChoiceType             `json:"type"`
	Mode        *string                             `json:"mode,omitempty"`
	Name        *string                             `json:"name,omitempty"`
	ServerLabel *string                             `json:"server_label,omitempty"`
	Tools       []ResponsesToolChoiceAllowedToolDef `json:"tools,omitempty"`
}
type ResponsesOutputMessageContentRenderedContent struct {
	RenderedContent string `json:"rendered_content"`
}
type ResponsesToolWebSearchPreview struct {
	SearchContextSize *string                             `json:"search_context_size,omitempty"`
	UserLocation      *ResponsesToolWebSearchUserLocation `json:"user_location,omitempty"`
}
type ResponsesServerSideToolUsageDetails struct {
	WebSearchCalls       int `json:"web_search_calls"`
	XSearchCalls         int `json:"x_search_calls"`
	CodeInterpreterCalls int `json:"code_interpreter_calls"`
	FileSearchCalls      int `json:"file_search_calls"`
	MCPCalls             int `json:"mcp_calls"`
	DocumentSearchCalls  int `json:"document_search_calls"`
}
type ResponsesFileSearchToolCallResult struct {
	Attributes *map[string]any `json:"attributes,omitempty"`
	FileID     *string         `json:"file_id,omitempty"`
	Filename   *string         `json:"filename,omitempty"`
	Score      *float64        `json:"score,omitempty"`
	Text       *string         `json:"text,omitempty"`
}
type ResponsesResponseInstructions struct {
	ResponsesResponseInstructionsStr   *string
	ResponsesResponseInstructionsArray []ResponsesMessage
}
type ResponsesWebFetchDocument struct {
	Type      string                   `json:"type,omitempty"`
	Text      *string                  `json:"text,omitempty"`
	Title     *string                  `json:"title,omitempty"`
	Source    *ResponsesWebFetchSource `json:"source,omitempty"`
	Citations *Citations               `json:"citations,omitempty"`
	Context   *string                  `json:"context,omitempty"`
}
type ResponsesToolComputerUsePreview struct {
	DisplayHeight int    `json:"display_height"`
	DisplayWidth  int    `json:"display_width"`
	Environment   string `json:"environment"`

	EnableZoom *bool `json:"enable_zoom,omitempty"`
}
type ResponsesTextConfig struct {
	Format    *ResponsesTextConfigFormat `json:"format,omitempty"`
	Verbosity *string                    `json:"verbosity,omitempty"`
}
type ResponsesToolShellDomainSecret struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}
type ResponsesWebFetchSource struct {
	Type      string  `json:"type,omitempty"`
	MediaType *string `json:"media_type,omitempty"`
	Data      *string `json:"data,omitempty"`
	URL       *string `json:"url,omitempty"`
	FileID    *string `json:"file_id,omitempty"`
}
type ResponsesToolShellSkillSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
type ResponsesComputerToolCallAcknowledgedSafetyCheck struct {
	ID      string  `json:"id"`
	Code    *string `json:"code,omitempty"`
	Message *string `json:"message,omitempty"`
}
type ResponsesParameters struct {
	Background           *bool                          `json:"background,omitempty"`
	Conversation         *ResponsesResponseConversation `json:"conversation,omitempty"`
	Include              []string                       `json:"include,omitempty"`
	Instructions         *string                        `json:"instructions,omitempty"`
	MaxOutputTokens      *int                           `json:"max_output_tokens,omitempty"`
	MaxToolCalls         *int                           `json:"max_tool_calls,omitempty"`
	Metadata             *map[string]any                `json:"metadata,omitempty"`
	ParallelToolCalls    *bool                          `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID   *string                        `json:"previous_response_id,omitempty"`
	PromptCacheKey       *string                        `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention *string                        `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *PromptCacheOptions            `json:"prompt_cache_options,omitempty"`
	Reasoning            *ResponsesParametersReasoning  `json:"reasoning,omitempty"`
	SafetyIdentifier     *string                        `json:"safety_identifier,omitempty"`
	ServiceTier          *BifrostServiceTier            `json:"service_tier,omitempty"`
	StreamOptions        *ResponsesStreamOptions        `json:"stream_options,omitempty"`
	Store                *bool                          `json:"store,omitempty"`
	Temperature          *float64                       `json:"temperature,omitempty"`
	Text                 *ResponsesTextConfig           `json:"text,omitempty"`
	TopLogProbs          *int                           `json:"top_logprobs,omitempty"`
	TopP                 *float64                       `json:"top_p,omitempty"`
	ToolChoice           *ResponsesToolChoice           `json:"tool_choice,omitempty"`
	Tools                []ResponsesTool                `json:"tools,omitempty"`
	Truncation           *string                        `json:"truncation,omitempty"`
	User                 *string                        `json:"user,omitempty"`

	IncludeServerSideToolInvocations *bool `json:"include_server_side_tool_invocations,omitempty"`

	ContextManagement json.RawMessage `json:"context_management,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type ResponsesToolXSearch struct {
	AllowedXHandles []string `json:"allowed_x_handles,omitempty"`

	ExcludedXHandles []string `json:"excluded_x_handles,omitempty"`

	FromDate *string `json:"from_date,omitempty"`

	ToDate *string `json:"to_date,omitempty"`

	EnableImageUnderstanding *bool `json:"enable_image_understanding,omitempty"`

	EnableVideoUnderstanding *bool `json:"enable_video_understanding,omitempty"`
}
type ResponsesMessageContentBlockType string
type ResponsesTool struct {
	Type        ResponsesToolType `json:"type"`
	Name        *string           `json:"name,omitempty"`
	Description *string           `json:"description,omitempty"`
	Async       *bool             `json:"async,omitempty"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`

	DeferLoading        *bool                  `json:"defer_loading,omitempty"`
	AllowedCallers      []string               `json:"allowed_callers,omitempty"`
	InputExamples       []ChatToolInputExample `json:"input_examples,omitempty"`
	EagerInputStreaming *bool                  `json:"eager_input_streaming,omitempty"`
	MaxCharacters       *int                   `json:"max_characters,omitempty"`

	*ResponsesToolFunction
	*ResponsesToolFileSearch
	*ResponsesToolComputerUsePreview
	*ResponsesToolWebSearch
	*ResponsesToolWebFetch
	*ResponsesToolMCP
	*ResponsesToolCodeInterpreter
	*ResponsesToolImageGeneration
	*ResponsesToolLocalShell
	*ResponsesToolShell
	*ResponsesToolCustom
	*ResponsesToolWebSearchPreview
	*ResponsesToolToolSearch
	*ResponsesToolNamespace
	*ResponsesToolXSearch
	*ResponsesToolAdvisor
}
type ResponsesToolChoiceType string
type ResponsesToolLocalShell struct {
}
type ResponsesShellCallOutputDelta struct {
	Stdout *string `json:"stdout,omitempty"`
	Stderr *string `json:"stderr,omitempty"`
}
type ResponsesOutputMessageContentTextAnnotation struct {
	Type        string  `json:"type"`
	Index       *int    `json:"index,omitempty"`
	FileID      *string `json:"file_id,omitempty"`
	Text        *string `json:"text,omitempty"`
	StartIndex  *int    `json:"start_index,omitempty"`
	EndIndex    *int    `json:"end_index,omitempty"`
	Filename    *string `json:"filename,omitempty"`
	Title       *string `json:"title,omitempty"`
	URL         *string `json:"url,omitempty"`
	ContainerID *string `json:"container_id,omitempty"`

	StartCharIndex  *int    `json:"start_char_index,omitempty"`
	EndCharIndex    *int    `json:"end_char_index,omitempty"`
	StartPageNumber *int    `json:"start_page_number,omitempty"`
	EndPageNumber   *int    `json:"end_page_number,omitempty"`
	StartBlockIndex *int    `json:"start_block_index,omitempty"`
	EndBlockIndex   *int    `json:"end_block_index,omitempty"`
	Source          *string `json:"source,omitempty"`
	EncryptedIndex  *string `json:"encrypted_index,omitempty"`
}
type ResponsesMCPApprovalRequestAction struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	ServerLabel string `json:"server_label"`
	Arguments   string `json:"arguments"`
}
type ResponsesReasoning struct {
	Summary          []ResponsesReasoningSummary `json:"summary"`
	EncryptedContent *string                     `json:"encrypted_content,omitempty"`
}
type ResponsesApplyPatchOperation struct {
	Type string  `json:"type"`
	Path string  `json:"path"`
	Diff *string `json:"diff,omitempty"`
}
type ResponsesInputMessageContentBlockFile struct {
	FileData *string `json:"file_data,omitempty"`
	FileURL  *string `json:"file_url,omitempty"`
	Filename *string `json:"filename,omitempty"`
	FileType *string `json:"file_type,omitempty"`
}
type ResponsesStreamOptions struct {
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}
type ResponsesToolAdvisorCaching struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

func (r *BifrostResponsesRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

func (r *BifrostResponsesRetrieveRequest) GetRawRequestBody() []byte {
	if r == nil {
		return nil
	}
	return r.RawRequestBody
}

func (r *BifrostResponsesDeleteRequest) GetRawRequestBody() []byte {
	if r == nil {
		return nil
	}
	return r.RawRequestBody
}

func (r *BifrostResponsesCancelRequest) GetRawRequestBody() []byte {
	if r == nil {
		return nil
	}
	return r.RawRequestBody
}

func (r *BifrostResponsesInputItemsRequest) GetRawRequestBody() []byte {
	if r == nil {
		return nil
	}
	return r.RawRequestBody
}

func (r *BifrostCompactionRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

func (r *BifrostResponsesResponse) UnmarshalJSON(data []byte) error {
	type Alias BifrostResponsesResponse
	aux := &struct {
		CreatedAt   float64  `json:"created_at"`
		CompletedAt *float64 `json:"completed_at"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}
	r.CreatedAt = int(aux.CreatedAt)
	if aux.CompletedAt != nil {
		v := int(*aux.CompletedAt)
		r.CompletedAt = &v
	}
	if r.ToolUsage != nil && r.Usage != nil && r.Usage.ToolUsage == nil {
		r.Usage.ToolUsage = r.ToolUsage
	}
	return nil
}

func (r BifrostResponsesResponse) MarshalJSON() ([]byte, error) {
	type alias BifrostResponsesResponse
	if r.Output == nil {
		r.Output = []ResponsesMessage{}
	}
	if r.Usage != nil && r.Usage.ToolUsage != nil {
		if r.ToolUsage == nil {
			r.ToolUsage = r.Usage.ToolUsage
		}
		usage := *r.Usage
		usage.ToolUsage = nil
		r.Usage = &usage
	}
	return Marshal(alias(r))
}

func (resp *BifrostResponsesResponse) BackfillParams(request *BifrostResponsesRequest) {
	if resp == nil || request == nil {
		return
	}
	if resp.Model == "" {
		resp.Model = request.Model
	}
	if resp.Object == "" {
		resp.Object = "response"
	}
	if resp.CreatedAt == 0 {
		resp.CreatedAt = int(time.Now().Unix())
	}
}

func (s *ResponsesTextConfigFormatJSONSchema) UnmarshalJSON(data []byte) error {
	type Alias ResponsesTextConfigFormatJSONSchema
	var aux Alias
	if err := Unmarshal(data, &aux); err != nil {
		return err
	}
	*s = ResponsesTextConfigFormatJSONSchema(aux)
	s.keyOrder = jsonObjectKeyOrder(data)
	return nil
}

func (s ResponsesTextConfigFormatJSONSchema) MarshalJSON() ([]byte, error) {
	type Alias ResponsesTextConfigFormatJSONSchema
	raw, err := MarshalSorted(Alias(s))
	if err != nil {
		return nil, err
	}
	return reorderJSONObjectKeys(raw, s.keyOrder), nil
}

func jsonObjectKeyOrder(data []byte) []string {
	parsed := gjson.ParseBytes(data)
	if !parsed.IsObject() {
		return nil
	}
	keys := make([]string, 0, 8)
	parsed.ForEach(func(key, _ gjson.Result) bool {
		keys = append(keys, key.String())
		return true
	})
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func reorderJSONObjectKeys(raw []byte, order []string) []byte {
	if len(order) == 0 {
		return raw
	}
	parsed := gjson.ParseBytes(raw)
	if !parsed.IsObject() {
		return raw
	}

	type jsonPair struct{ key, value string }
	pairs := make([]jsonPair, 0, 8)
	index := make(map[string]int, 8)
	parsed.ForEach(func(key, value gjson.Result) bool {
		keyRaw := key.Raw
		if len(keyRaw) == 0 || keyRaw[0] != '"' {
			keyRaw = strconv.Quote(key.String())
		}
		index[key.String()] = len(pairs)
		pairs = append(pairs, jsonPair{key: keyRaw, value: value.Raw})
		return true
	})
	if len(pairs) == 0 {
		return raw
	}

	written := make([]bool, len(pairs))
	var buf bytes.Buffer
	buf.WriteByte('{')
	writePair := func(p jsonPair) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.WriteString(p.key)
		buf.WriteByte(':')
		buf.WriteString(p.value)
	}
	for _, key := range order {
		if i, ok := index[key]; ok && !written[i] {
			written[i] = true
			writePair(pairs[i])
		}
	}
	for i, p := range pairs {
		if !written[i] {
			writePair(p)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func (s JSONSchemaOrBool) MarshalJSON() ([]byte, error) {
	if s.SchemaBool != nil && s.SchemaMap != nil {
		return nil, fmt.Errorf("both SchemaBool and SchemaMap are set; only one should be non-nil")
	}
	if s.SchemaBool != nil {
		return MarshalSorted(*s.SchemaBool)
	}
	if s.SchemaMap != nil {
		return MarshalSorted(s.SchemaMap)
	}
	return nil, fmt.Errorf("schema cannot be null; omit the field instead")
}

func (s *JSONSchemaOrBool) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		s.SchemaBool = nil
		s.SchemaMap = nil
		return nil
	}

	var boolValue bool
	if err := Unmarshal(data, &boolValue); err == nil {
		s.SchemaMap = nil
		s.SchemaBool = &boolValue
		return nil
	}

	var mapValue OrderedMap
	if err := Unmarshal(data, &mapValue); err == nil {
		s.SchemaBool = nil
		s.SchemaMap = &mapValue
		return nil
	}

	return fmt.Errorf("schema must be either a boolean or an object")
}

func (s *ResponsesTextConfigFormatJSONSchema) CompositeSchema() (*OrderedMap, bool, error) {
	if s == nil || s.Schema == nil {
		return nil, false, nil
	}
	if s.Schema.SchemaMap != nil {
		return s.Schema.SchemaMap, false, nil
	}
	if s.Schema.SchemaBool != nil {
		if *s.Schema.SchemaBool {
			return nil, true, nil
		}
		return nil, false, ErrUnsatisfiableSchema
	}
	return nil, false, nil
}

func JSONSchemaFromMap(v interface{}) *ResponsesTextConfigFormatJSONSchema {
	var m map[string]interface{}
	var keyOrder []string
	switch src := v.(type) {
	case map[string]interface{}:
		m = src
	case *OrderedMap:
		if src == nil {
			return nil
		}
		m = src.ToMap()
		keyOrder = src.Keys()
	case OrderedMap:
		m = src.ToMap()
		keyOrder = src.Keys()
	case json.RawMessage:
		decoded := NewOrderedMap()
		if err := decoded.UnmarshalJSON(src); err != nil {
			return nil
		}
		m = decoded.ToMap()
		keyOrder = decoded.Keys()
	case []byte:
		decoded := NewOrderedMap()
		if err := decoded.UnmarshalJSON(src); err != nil {
			return nil
		}
		m = decoded.ToMap()
		keyOrder = decoded.Keys()
	default:
		return nil
	}
	s := &ResponsesTextConfigFormatJSONSchema{keyOrder: keyOrder}
	if t, ok := m["type"].(string); ok {
		s.Type = Ptr(t)
	}
	if props, ok := SafeExtractOrderedMap(m["properties"]); ok {
		s.Properties = props
	}
	if req, ok := m["required"].([]interface{}); ok {
		strs := make([]string, 0, len(req))
		for _, r := range req {
			if str, ok := r.(string); ok {
				strs = append(strs, str)
			}
		}
		s.Required = strs
	} else if req, ok := m["required"].([]string); ok {
		s.Required = req
	}
	if desc, ok := m["description"].(string); ok {
		s.Description = Ptr(desc)
	}
	if title, ok := m["title"].(string); ok {
		s.Title = Ptr(title)
	}
	if ref, ok := m["$ref"].(string); ok {
		s.Ref = Ptr(ref)
	}
	if defs, ok := SafeExtractOrderedMap(m["$defs"]); ok {
		s.Defs = defs
	}
	if defs, ok := SafeExtractOrderedMap(m["definitions"]); ok {
		s.Definitions = defs
	}
	if items, ok := SafeExtractOrderedMap(m["items"]); ok {
		s.Items = items
	}
	if b, ok := m["additionalProperties"].(bool); ok {
		s.AdditionalProperties = &AdditionalPropertiesStruct{AdditionalPropertiesBool: Ptr(b)}
	} else if ap, ok := SafeExtractOrderedMap(m["additionalProperties"]); ok {
		s.AdditionalProperties = &AdditionalPropertiesStruct{AdditionalPropertiesMap: ap}
	}
	if f, ok := m["format"].(string); ok {
		s.Format = Ptr(f)
	}
	if p, ok := m["pattern"].(string); ok {
		s.Pattern = Ptr(p)
	}
	if enums, ok := m["enum"].([]interface{}); ok {
		strs := make([]string, 0, len(enums))
		for _, e := range enums {
			if str, ok := e.(string); ok {
				strs = append(strs, str)
			}
		}
		s.Enum = strs
	}
	if n, ok := m["nullable"].(bool); ok {
		s.Nullable = Ptr(n)
	}
	if extractSliceOfMaps := func(key string) []OrderedMap {
		switch raw := m[key].(type) {
		case []interface{}:
			out := make([]OrderedMap, 0, len(raw))
			for _, item := range raw {
				if om, ok := SafeExtractOrderedMap(item); ok {
					out = append(out, *om)
				}
			}
			return out
		case []OrderedMap:
			return raw
		}
		return nil
	}; true {
		if ao := extractSliceOfMaps("anyOf"); len(ao) > 0 {
			s.AnyOf = ao
		}
		if oo := extractSliceOfMaps("oneOf"); len(oo) > 0 {
			s.OneOf = oo
		}
		if ao := extractSliceOfMaps("allOf"); len(ao) > 0 {
			s.AllOf = ao
		}
	}

	if n, ok := m["minItems"].(float64); ok {
		x := int64(n)
		s.MinItems = Ptr(x)
	}
	if n, ok := m["maxItems"].(float64); ok {
		x := int64(n)
		s.MaxItems = Ptr(x)
	}
	if n, ok := m["minimum"].(float64); ok {
		s.Minimum = Ptr(n)
	}
	if n, ok := m["maximum"].(float64); ok {
		s.Maximum = Ptr(n)
	}
	if n, ok := m["minLength"].(float64); ok {
		x := int64(n)
		s.MinLength = Ptr(x)
	}
	if n, ok := m["maxLength"].(float64); ok {
		x := int64(n)
		s.MaxLength = Ptr(x)
	}
	if d, ok := m["default"]; ok {
		s.Default = d
	}
	if po, ok := m["propertyOrdering"].([]interface{}); ok {
		strs := make([]string, 0, len(po))
		for _, v := range po {
			if str, ok := v.(string); ok {
				strs = append(strs, str)
			}
		}
		s.PropertyOrdering = strs
	}
	return s
}

func (s *ResponsesTextConfigFormatJSONSchema) RawSchemaJSON() json.RawMessage {
	if s == nil {
		return nil
	}

	if s.Schema != nil {
		if s.Schema.SchemaMap != nil {
			encoded, err := MarshalSorted(s.Schema.SchemaMap)
			if err != nil {
				return nil
			}
			return encoded
		}
		if s.Schema.SchemaBool != nil {
			if *s.Schema.SchemaBool {
				return json.RawMessage("true")
			}
			return json.RawMessage("false")
		}
	}

	encoded, err := MarshalSorted(s)
	if err != nil {
		return nil
	}
	for _, key := range []string{"name", "strict", "schema"} {
		encoded, err = sjson.DeleteBytes(encoded, key)
		if err != nil {
			return nil
		}
	}
	if len(gjson.ParseBytes(encoded).Map()) == 0 {
		return nil
	}
	return encoded
}

func (s *ResponsesTextConfigFormatJSONSchema) ToMap() interface{} {
	if s == nil {
		return nil
	}
	if s.Schema != nil {
		if s.Schema.SchemaMap != nil {
			return s.Schema.SchemaMap
		}
		if s.Schema.SchemaBool != nil {
			return *s.Schema.SchemaBool
		}
	}
	m := make(map[string]interface{})
	if s.Type != nil {
		m["type"] = *s.Type
	}
	if s.Properties != nil {
		m["properties"] = s.Properties
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Description != nil {
		m["description"] = *s.Description
	}
	if s.Title != nil {
		m["title"] = *s.Title
	}
	if s.Ref != nil {
		m["$ref"] = *s.Ref
	}
	if s.Defs != nil {
		m["$defs"] = s.Defs
	}
	if s.Definitions != nil {
		m["definitions"] = s.Definitions
	}
	if s.Items != nil {
		m["items"] = s.Items
	}
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.AdditionalPropertiesBool != nil {
			m["additionalProperties"] = *s.AdditionalProperties.AdditionalPropertiesBool
		} else if s.AdditionalProperties.AdditionalPropertiesMap != nil {
			m["additionalProperties"] = s.AdditionalProperties.AdditionalPropertiesMap
		}
	}
	if s.Format != nil {
		m["format"] = *s.Format
	}
	if s.Pattern != nil {
		m["pattern"] = *s.Pattern
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if s.Nullable != nil {
		m["nullable"] = *s.Nullable
	}
	if s.Default != nil {
		m["default"] = s.Default
	}
	if len(s.AnyOf) > 0 {
		m["anyOf"] = s.AnyOf
	}
	if len(s.OneOf) > 0 {
		m["oneOf"] = s.OneOf
	}
	if len(s.AllOf) > 0 {
		m["allOf"] = s.AllOf
	}
	if s.MinItems != nil {
		m["minItems"] = *s.MinItems
	}
	if s.MaxItems != nil {
		m["maxItems"] = *s.MaxItems
	}
	if s.Minimum != nil {
		m["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		m["maximum"] = *s.Maximum
	}
	if s.MinLength != nil {
		m["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		m["maxLength"] = *s.MaxLength
	}
	if len(m) == 0 {
		return nil
	}

	return orderedMapWithKeyOrder(m, s.keyOrder)
}

func orderedMapWithKeyOrder(m map[string]interface{}, order []string) *OrderedMap {
	if m == nil {
		return nil
	}
	om := NewOrderedMapWithCapacity(len(m))
	for _, key := range order {
		if value, ok := m[key]; ok {
			om.Set(key, value)
		}
	}
	if om.Len() == len(m) {
		return om
	}

	remaining := make([]string, 0, len(m)-om.Len())
	for key := range m {
		if _, taken := om.Get(key); !taken {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		om.Set(key, m[key])
	}
	return om
}

func (rc ResponsesResponseConversation) MarshalJSON() ([]byte, error) {

	if rc.ResponsesResponseConversationStr != nil && rc.ResponsesResponseConversationStruct != nil {
		return nil, fmt.Errorf("both ResponsesResponseConversationStr and ResponsesResponseConversationStruct are set; only one should be non-nil")
	}

	if rc.ResponsesResponseConversationStr != nil {
		return MarshalSorted(*rc.ResponsesResponseConversationStr)
	}
	if rc.ResponsesResponseConversationStruct != nil {
		return MarshalSorted(rc.ResponsesResponseConversationStruct)
	}

	return MarshalSorted(nil)
}

func (rc *ResponsesResponseConversation) UnmarshalJSON(data []byte) error {

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		*rc = ResponsesResponseConversation{ResponsesResponseConversationStr: &stringContent}
		return nil
	}

	var structContent ResponsesResponseConversationStruct
	if err := Unmarshal(data, &structContent); err == nil {
		*rc = ResponsesResponseConversation{ResponsesResponseConversationStruct: &structContent}
		return nil
	}

	return fmt.Errorf("content field is neither a string nor a struct")
}

func (rc ResponsesResponseInstructions) MarshalJSON() ([]byte, error) {

	if rc.ResponsesResponseInstructionsStr != nil && rc.ResponsesResponseInstructionsArray != nil {
		return nil, fmt.Errorf("both ResponsesMessageContentStr and ResponsesMessageContentBlocks are set; only one should be non-nil")
	}

	if rc.ResponsesResponseInstructionsStr != nil {
		return MarshalSorted(*rc.ResponsesResponseInstructionsStr)
	}
	if rc.ResponsesResponseInstructionsArray != nil {
		return MarshalSorted(rc.ResponsesResponseInstructionsArray)
	}

	return MarshalSorted(nil)
}

func (rc *ResponsesResponseInstructions) UnmarshalJSON(data []byte) error {

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		rc.ResponsesResponseInstructionsStr = &stringContent
		return nil
	}

	var arrayContent []ResponsesMessage
	if err := Unmarshal(data, &arrayContent); err == nil {
		rc.ResponsesResponseInstructionsArray = arrayContent
		return nil
	}

	return fmt.Errorf("content field is neither a string nor an array of Messages")
}

const (
	ResponsesResponseStatusInProgress = "in_progress"
	ResponsesResponseStatusCompleted  = "completed"
	ResponsesResponseStatusIncomplete = "incomplete"
	ResponsesResponseStatusFailed     = "failed"
	ResponsesResponseStatusCancelled  = "cancelled"
	ResponsesResponseStatusQueued     = "queued"
)

const (
	ResponsesResponseIncompleteReasonMaxOutputTokens = "max_output_tokens"
	ResponsesResponseIncompleteReasonContentFilter   = "content_filter"
)

func (d *ResponsesResponseInputTokens) UnmarshalJSON(data []byte) error {
	var raw struct {
		TextTokens              int                          `json:"text_tokens"`
		AudioTokens             int                          `json:"audio_tokens"`
		ImageTokens             int                          `json:"image_tokens"`
		CachedReadTokens        int                          `json:"cached_read_tokens"`
		CachedWriteTokens       int                          `json:"cached_write_tokens"`
		CachedWriteTokenDetails *ChatCachedWriteTokenDetails `json:"cached_write_token_details"`
		CachedTokens            *int                         `json:"cached_tokens"`
		CacheWriteTokens        *int                         `json:"cache_write_tokens"`

		StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens"`
		CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens"`
	}
	if err := Unmarshal(data, &raw); err != nil {
		return err
	}
	d.TextTokens = raw.TextTokens
	d.AudioTokens = raw.AudioTokens
	d.ImageTokens = raw.ImageTokens
	d.CachedReadTokens = raw.CachedReadTokens
	d.CachedWriteTokens = raw.CachedWriteTokens
	d.CachedWriteTokenDetails = raw.CachedWriteTokenDetails
	d.StandardSpeechAudioTokens = raw.StandardSpeechAudioTokens
	d.CustomSpeechAudioTokens = raw.CustomSpeechAudioTokens

	if raw.CachedTokens != nil && raw.CachedReadTokens == 0 && raw.CachedWriteTokens == 0 {
		d.CachedReadTokens = *raw.CachedTokens
	}

	if raw.CacheWriteTokens != nil && d.CachedWriteTokens == 0 {
		d.CachedWriteTokens = *raw.CacheWriteTokens
	}
	return nil
}

func (d ResponsesResponseInputTokens) MarshalJSON() ([]byte, error) {
	type raw struct {
		TextTokens              int                          `json:"text_tokens,omitempty"`
		AudioTokens             int                          `json:"audio_tokens,omitempty"`
		ImageTokens             int                          `json:"image_tokens,omitempty"`
		CachedReadTokens        int                          `json:"cached_read_tokens"`
		CachedWriteTokens       int                          `json:"cached_write_tokens"`
		CachedWriteTokenDetails *ChatCachedWriteTokenDetails `json:"cached_write_token_details,omitempty"`
		CachedTokens            int                          `json:"cached_tokens"`

		CacheWriteTokens int `json:"cache_write_tokens"`

		StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens,omitempty"`
		CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens,omitempty"`
	}
	return MarshalSorted(raw{
		TextTokens:              d.TextTokens,
		AudioTokens:             d.AudioTokens,
		ImageTokens:             d.ImageTokens,
		CachedReadTokens:        d.CachedReadTokens,
		CachedWriteTokens:       d.CachedWriteTokens,
		CachedWriteTokenDetails: d.CachedWriteTokenDetails,
		CachedTokens:            d.CachedReadTokens,
		CacheWriteTokens:        d.CachedWriteTokens,

		StandardSpeechAudioTokens: d.StandardSpeechAudioTokens,
		CustomSpeechAudioTokens:   d.CustomSpeechAudioTokens,
	})
}

const (
	ResponsesMessageTypeMessage              ResponsesMessageType = "message"
	ResponsesMessageTypeFileSearchCall       ResponsesMessageType = "file_search_call"
	ResponsesMessageTypeComputerCall         ResponsesMessageType = "computer_call"
	ResponsesMessageTypeComputerCallOutput   ResponsesMessageType = "computer_call_output"
	ResponsesMessageTypeWebSearchCall        ResponsesMessageType = "web_search_call"
	ResponsesMessageTypeWebFetchCall         ResponsesMessageType = "web_fetch_call"
	ResponsesMessageTypeFunctionCall         ResponsesMessageType = "function_call"
	ResponsesMessageTypeFunctionCallOutput   ResponsesMessageType = "function_call_output"
	ResponsesMessageTypeToolSearchCall       ResponsesMessageType = "tool_search_call"
	ResponsesMessageTypeToolSearchOutput     ResponsesMessageType = "tool_search_output"
	ResponsesMessageTypeCodeInterpreterCall  ResponsesMessageType = "code_interpreter_call"
	ResponsesMessageTypeLocalShellCall       ResponsesMessageType = "local_shell_call"
	ResponsesMessageTypeLocalShellCallOutput ResponsesMessageType = "local_shell_call_output"
	ResponsesMessageTypeShellCall            ResponsesMessageType = "shell_call"
	ResponsesMessageTypeShellCallOutput      ResponsesMessageType = "shell_call_output"
	ResponsesMessageTypeApplyPatchCall       ResponsesMessageType = "apply_patch_call"
	ResponsesMessageTypeApplyPatchCallOutput ResponsesMessageType = "apply_patch_call_output"
	ResponsesMessageTypeMCPCall              ResponsesMessageType = "mcp_call"
	ResponsesMessageTypeCustomToolCall       ResponsesMessageType = "custom_tool_call"
	ResponsesMessageTypeCustomToolCallOutput ResponsesMessageType = "custom_tool_call_output"
	ResponsesMessageTypeImageGenerationCall  ResponsesMessageType = "image_generation_call"
	ResponsesMessageTypeMCPListTools         ResponsesMessageType = "mcp_list_tools"
	ResponsesMessageTypeMCPApprovalRequest   ResponsesMessageType = "mcp_approval_request"
	ResponsesMessageTypeMCPApprovalResponses ResponsesMessageType = "mcp_approval_response"
	ResponsesMessageTypeReasoning            ResponsesMessageType = "reasoning"
	ResponsesMessageTypeItemReference        ResponsesMessageType = "item_reference"
	ResponsesMessageTypeRefusal              ResponsesMessageType = "refusal"
	ResponsesMessageTypeCompaction           ResponsesMessageType = "compaction"
	ResponsesMessageTypeAdditionalTools      ResponsesMessageType = "additional_tools"
	ResponsesMessageTypeAdvisorCall          ResponsesMessageType = "advisor_call"
	ResponsesMessageTypeConfigurationUpdate  ResponsesMessageType = "configuration_update"
)

func isRawPreservedItem(t string) bool {
	return t == string(ResponsesMessageTypeToolSearchCall) ||
		t == string(ResponsesMessageTypeToolSearchOutput) ||
		t == string(ResponsesMessageTypeAdditionalTools) ||
		t == string(ResponsesMessageTypeConfigurationUpdate)
}

func (m *ResponsesMessage) UnmarshalJSON(data []byte) error {

	*m = ResponsesMessage{}
	if t := gjson.GetBytes(data, "type").String(); isRawPreservedItem(t) {
		mt := ResponsesMessageType(t)
		m.Type = &mt

		if id := gjson.GetBytes(data, "id"); id.Type == gjson.String && id.String() != "" {
			m.ID = Ptr(id.String())
		}
		if status := gjson.GetBytes(data, "status"); status.Type == gjson.String && status.String() != "" {
			m.Status = Ptr(status.String())
		}
		if callID := gjson.GetBytes(data, "call_id"); callID.Type == gjson.String && callID.String() != "" {
			m.ResponsesToolMessage = &ResponsesToolMessage{CallID: Ptr(callID.String())}
		}

		if t == string(ResponsesMessageTypeToolSearchCall) {
			if name := gjson.GetBytes(data, "name"); name.Type == gjson.String && name.String() != "" {
				if m.ResponsesToolMessage == nil {
					m.ResponsesToolMessage = &ResponsesToolMessage{}
				}
				m.Name = new(name.String())
			}
			var toolReferences []string
			for _, ref := range gjson.GetBytes(data, "tool_references").Array() {
				if ref.Type == gjson.String && ref.String() != "" {
					toolReferences = append(toolReferences, ref.String())
				}
			}
			if len(toolReferences) > 0 {
				if m.ResponsesToolMessage == nil {
					m.ResponsesToolMessage = &ResponsesToolMessage{}
				}
				m.ResponsesToolSearchCall = &ResponsesToolSearchCall{ToolReferences: toolReferences}
			}
		}

		m.setToolArguments(json.RawMessage(gjson.GetBytes(data, "arguments").Raw))

		if execution := gjson.GetBytes(data, "execution"); execution.Type == gjson.String && execution.String() != "" {
			if m.ResponsesToolMessage == nil {
				m.ResponsesToolMessage = &ResponsesToolMessage{}
			}
			m.Execution = Ptr(execution.String())
		}

		if t == string(ResponsesMessageTypeToolSearchOutput) {
			if tools := gjson.GetBytes(data, "tools"); tools.IsArray() {
				m.ToolSearchOutputTools = json.RawMessage(tools.Raw)
			}
		}

		if t == string(ResponsesMessageTypeAdditionalTools) {
			if tools := gjson.GetBytes(data, "tools"); tools.IsArray() {
				m.AdditionalTools = json.RawMessage(tools.Raw)
			}
		}
		m.rawPreserved = append([]byte(nil), data...)
		return nil
	}

	type Alias ResponsesMessage
	aux := &struct {
		Arguments json.RawMessage `json:"arguments,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}

	if err := Unmarshal(data, aux); err != nil {
		return err
	}

	m.setToolArguments(aux.Arguments)

	if m.Type != nil && *m.Type == ResponsesMessageTypeReasoning &&
		(m.ResponsesReasoning == nil || m.ResponsesReasoning.Summary == nil) {
		m.liftReasoningWrapper(data)
	}

	if m.Type != nil && *m.Type == ResponsesMessageTypeToolSearchOutput {
		var probe struct {
			Tools json.RawMessage `json:"tools,omitempty"`
		}
		if err := Unmarshal(data, &probe); err == nil && len(probe.Tools) > 0 && string(probe.Tools) != "null" {
			m.ToolSearchOutputTools = probe.Tools
		}
		if m.ResponsesToolMessage != nil {
			m.ResponsesMCPListTools = nil
		}
	}

	return nil
}

func (m *ResponsesMessage) liftReasoningWrapper(data []byte) {
	wrapper := gjson.GetBytes(data, "reasoning")
	if !wrapper.IsObject() {
		return
	}
	summary := wrapper.Get("summary")
	encrypted := wrapper.Get("encrypted_content")
	if !summary.IsArray() && encrypted.Type != gjson.String {
		return
	}
	reasoning := m.ResponsesReasoning
	if reasoning == nil {
		reasoning = &ResponsesReasoning{}
	}
	if summary.IsArray() && reasoning.Summary == nil {
		entries := summary.Array()
		reasoning.Summary = make([]ResponsesReasoningSummary, 0, len(entries))
		for _, entry := range entries {
			switch {
			case entry.Type == gjson.String:
				reasoning.Summary = append(reasoning.Summary, ResponsesReasoningSummary{
					Type: ResponsesReasoningContentBlockTypeSummaryText,
					Text: entry.String(),
				})
			case entry.IsObject():
				var block ResponsesReasoningSummary
				if err := Unmarshal([]byte(entry.Raw), &block); err != nil {
					continue
				}
				if block.Type == "" {
					block.Type = ResponsesReasoningContentBlockTypeSummaryText
				}
				reasoning.Summary = append(reasoning.Summary, block)
			}
		}
	}
	if encrypted.Type == gjson.String && reasoning.EncryptedContent == nil {
		reasoning.EncryptedContent = Ptr(encrypted.String())
	}
	m.ResponsesReasoning = reasoning
}

func (m *ResponsesMessage) setToolArguments(raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	args := responsesToolArgumentsToString(raw)
	if m.ResponsesToolMessage == nil {
		m.ResponsesToolMessage = &ResponsesToolMessage{}
	}
	m.Arguments = &args
}

func responsesToolArgumentsToString(raw json.RawMessage) string {
	var str string
	if err := Unmarshal(raw, &str); err == nil {
		return str
	}
	return string(raw)
}

func (m ResponsesMessage) MarshalJSON() ([]byte, error) {
	type Alias ResponsesMessage

	if len(m.rawPreserved) > 0 {
		return append([]byte(nil), m.rawPreserved...), nil
	}

	if m.Type != nil && *m.Type == ResponsesMessageTypeToolSearchOutput {
		aux := &struct {
			Arguments json.RawMessage `json:"arguments,omitempty"`
			Tools     json.RawMessage `json:"tools,omitempty"`
			*Alias
		}{
			Alias: (*Alias)(&m),
		}
		if m.ToolSearchOutputTools != nil {
			aux.Tools = m.ToolSearchOutputTools
		}
		if m.ResponsesToolMessage != nil && m.Arguments != nil {
			aux.Arguments = json.RawMessage(*m.Arguments)
		}
		return MarshalSorted(aux)
	}

	aux := &struct {
		Arguments json.RawMessage `json:"arguments,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(&m),
	}

	if m.ResponsesToolMessage != nil && m.Arguments != nil {
		if m.Type != nil && *m.Type == ResponsesMessageTypeToolSearchCall {
			aux.Arguments = json.RawMessage(*m.Arguments)
		} else {
			encoded, err := Marshal(*m.Arguments)
			if err != nil {
				return nil, err
			}
			aux.Arguments = encoded
		}
	}

	return MarshalSorted(aux)
}

func (m *ResponsesMessage) IsEffortOnlySystemItem() bool {
	if m == nil || m.OutputConfig == nil || m.Role == nil {
		return false
	}
	if *m.Role != ResponsesInputMessageRoleSystem && *m.Role != ResponsesInputMessageRoleDeveloper {
		return false
	}
	if m.Content == nil {
		return true
	}
	if m.Content.ContentStr != nil && *m.Content.ContentStr != "" {
		return false
	}
	return len(m.Content.ContentBlocks) == 0
}

const (
	ResponsesInputMessageRoleAssistant ResponsesMessageRoleType = "assistant"
	ResponsesInputMessageRoleUser      ResponsesMessageRoleType = "user"
	ResponsesInputMessageRoleSystem    ResponsesMessageRoleType = "system"
	ResponsesInputMessageRoleDeveloper ResponsesMessageRoleType = "developer"
)

func (rc ResponsesMessageContent) MarshalJSON() ([]byte, error) {

	if rc.ContentStr != nil && rc.ContentBlocks != nil {
		return nil, fmt.Errorf("both ResponsesMessageContentStr and ResponsesMessageContentBlocks are set; only one should be non-nil")
	}

	if rc.ContentStr != nil {
		return MarshalSorted(*rc.ContentStr)
	}
	if rc.ContentBlocks != nil {
		return MarshalSorted(rc.ContentBlocks)
	}

	return MarshalSorted("")
}

func (rc *ResponsesMessageContent) UnmarshalJSON(data []byte) error {

	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '"':
			var stringContent string
			if err := Unmarshal(data, &stringContent); err != nil {
				return fmt.Errorf("content field is neither a string nor an array of Content blocks")
			}
			rc.ContentStr = &stringContent
			return nil
		case '[':
			var arrayContent []ResponsesMessageContentBlock
			if err := Unmarshal(data, &arrayContent); err != nil {
				return fmt.Errorf("content field is neither a string nor an array of Content blocks")
			}
			rc.ContentBlocks = arrayContent
			return nil
		case 'n':

			var nullContent string
			if err := Unmarshal(data, &nullContent); err != nil {
				return fmt.Errorf("content field is neither a string nor an array of Content blocks")
			}
			rc.ContentStr = &nullContent
			return nil
		}
		break
	}
	return fmt.Errorf("content field is neither a string nor an array of Content blocks")
}

const (
	ResponsesInputMessageContentBlockTypeText          ResponsesMessageContentBlockType = "input_text"
	ResponsesInputMessageContentBlockTypeImage         ResponsesMessageContentBlockType = "input_image"
	ResponsesInputMessageContentBlockTypeFile          ResponsesMessageContentBlockType = "input_file"
	ResponsesInputMessageContentBlockTypeAudio         ResponsesMessageContentBlockType = "input_audio"
	ResponsesInputMessageContentBlockTypeContainer     ResponsesMessageContentBlockType = "input_container"
	ResponsesInputMessageContentBlockTypeToolReference ResponsesMessageContentBlockType = "tool_reference"

	ResponsesOutputMessageContentTypeText      ResponsesMessageContentBlockType = "output_text"
	ResponsesOutputMessageContentTypeRefusal   ResponsesMessageContentBlockType = "refusal"
	ResponsesOutputMessageContentTypeReasoning ResponsesMessageContentBlockType = "reasoning_text"

	ResponsesOutputMessageContentTypeSummaryText ResponsesMessageContentBlockType = "summary_text"

	ResponsesOutputMessageContentTypeRenderedContent ResponsesMessageContentBlockType = "rendered_content"

	ResponsesOutputMessageContentTypeCompaction ResponsesMessageContentBlockType = "compaction"

	ResponsesOutputMessageContentTypeFallback ResponsesMessageContentBlockType = "fallback"

	ResponsesInputMessageContentBlockTypeToolAddition ResponsesMessageContentBlockType = "tool_addition"
	ResponsesInputMessageContentBlockTypeToolRemoval  ResponsesMessageContentBlockType = "tool_removal"
)

const ToolSearchExecutionClient = "client"

func (m *ResponsesMessage) IsVerbatimItem() bool {
	return len(m.rawPreserved) > 0
}

func (m *ResponsesMessage) IsClientToolSearchItem() bool {
	return m.ResponsesToolMessage != nil && m.Execution != nil && *m.Execution == ToolSearchExecutionClient
}

func (m *ResponsesMessage) ToolSearchOutputToolNames() []string {
	if len(m.ToolSearchOutputTools) == 0 {
		return nil
	}
	var names []string
	for _, tool := range gjson.ParseBytes(m.ToolSearchOutputTools).Array() {
		if tool.Get("type").String() == string(ResponsesToolTypeNamespace) {
			for _, member := range tool.Get("tools").Array() {
				if name := member.Get("name").String(); name != "" {
					names = append(names, name)
				}
			}
			continue
		}
		if name := tool.Get("name").String(); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func ClientToolSearchFunctionName(tools []ResponsesTool, input []ResponsesMessage) string {
	if !slices.ContainsFunc(tools, func(t ResponsesTool) bool { return t.DeferLoading != nil && *t.DeferLoading }) {
		return ""
	}
	isEagerFunction := func(name string) bool {
		return slices.ContainsFunc(tools, func(t ResponsesTool) bool {
			return t.Type == ResponsesToolTypeFunction && t.Name != nil && *t.Name == name &&
				(t.DeferLoading == nil || !*t.DeferLoading)
		})
	}
	for _, name := range clientToolSearchNames {
		if isEagerFunction(name) {
			return name
		}
	}
	for i := range input {
		output := &input[i]
		if output.Type == nil || *output.Type != ResponsesMessageTypeFunctionCallOutput ||
			output.ResponsesToolMessage == nil || output.CallID == nil || output.Output == nil ||
			!slices.ContainsFunc(output.Output.ResponsesFunctionToolCallOutputBlocks, func(b ResponsesMessageContentBlock) bool {
				return b.Type == ResponsesInputMessageContentBlockTypeToolReference
			}) {
			continue
		}
		for j := range input[:i] {
			call := &input[j]
			if call.Type != nil && *call.Type == ResponsesMessageTypeFunctionCall &&
				call.ResponsesToolMessage != nil && call.CallID != nil && *call.CallID == *output.CallID &&
				call.Name != nil && isEagerFunction(*call.Name) {
				return *call.Name
			}
		}
	}
	return ""
}

const (
	ResponsesToolCallerDirect              = "direct"
	ResponsesToolCallerProgrammatic        = "programmatic"
	ResponsesToolCallerCodeExecutionPrefix = "code_execution_"
)

func (action ResponsesToolMessageActionStruct) MarshalJSON() ([]byte, error) {
	if action.ResponsesToolCallActionStr != nil {
		return MarshalSorted(*action.ResponsesToolCallActionStr)
	}
	if action.ResponsesComputerToolCallAction != nil {
		return MarshalSorted(action.ResponsesComputerToolCallAction)
	}
	if action.ResponsesWebSearchToolCallAction != nil {
		return MarshalSorted(action.ResponsesWebSearchToolCallAction)
	}
	if action.ResponsesWebFetchToolCallAction != nil {
		return MarshalSorted(action.ResponsesWebFetchToolCallAction)
	}
	if action.ResponsesLocalShellToolCallAction != nil {
		return MarshalSorted(action.ResponsesLocalShellToolCallAction)
	}
	if action.ResponsesShellToolCallAction != nil {
		return MarshalSorted(action.ResponsesShellToolCallAction)
	}
	if action.ResponsesMCPApprovalRequestAction != nil {
		return MarshalSorted(action.ResponsesMCPApprovalRequestAction)
	}
	return nil, fmt.Errorf("responses tool message action struct is empty")
}

func (action *ResponsesToolMessageActionStruct) UnmarshalJSON(data []byte) error {

	var str string
	if err := Unmarshal(data, &str); err == nil {
		action.ResponsesToolCallActionStr = &str
		return nil
	}

	if !gjson.GetBytes(data, "type").Exists() && gjson.GetBytes(data, "commands").IsArray() {
		var shellToolCallAction ResponsesShellToolCallAction
		if err := Unmarshal(data, &shellToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal shell tool call action: %w", err)
		}
		action.ResponsesShellToolCallAction = &shellToolCallAction
		return nil
	}

	var typeStruct struct {
		Type string `json:"type"`
	}
	if err := Unmarshal(data, &typeStruct); err != nil {
		return fmt.Errorf("failed to peek at type field: %w", err)
	}

	switch typeStruct.Type {
	case "exec":
		var localShellToolCallAction ResponsesLocalShellToolCallAction
		if err := Unmarshal(data, &localShellToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal local shell tool call action: %w", err)
		}
		action.ResponsesLocalShellToolCallAction = &localShellToolCallAction
		return nil

	case "mcp_approval_request":
		var mcpApprovalRequestAction ResponsesMCPApprovalRequestAction
		if err := Unmarshal(data, &mcpApprovalRequestAction); err != nil {
			return fmt.Errorf("failed to unmarshal mcp approval request action: %w", err)
		}
		action.ResponsesMCPApprovalRequestAction = &mcpApprovalRequestAction
		return nil

	case "search", "open_page", "find":
		var webSearchToolCallAction ResponsesWebSearchToolCallAction
		if err := Unmarshal(data, &webSearchToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal web search tool call action: %w", err)
		}
		action.ResponsesWebSearchToolCallAction = &webSearchToolCallAction
		return nil

	case "fetch":
		var webFetchToolCallAction ResponsesWebFetchToolCallAction
		if err := Unmarshal(data, &webFetchToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal web fetch tool call action: %w", err)
		}
		action.ResponsesWebFetchToolCallAction = &webFetchToolCallAction
		return nil

	case "click", "double_click", "drag", "keypress", "move", "screenshot", "scroll", "type", "wait", "zoom":
		var computerToolCallAction ResponsesComputerToolCallAction
		if err := Unmarshal(data, &computerToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal computer tool call action: %w", err)
		}
		action.ResponsesComputerToolCallAction = &computerToolCallAction
		return nil

	default:

		var computerToolCallAction ResponsesComputerToolCallAction
		if err := Unmarshal(data, &computerToolCallAction); err != nil {
			return fmt.Errorf("failed to unmarshal computer tool call action: %w", err)
		}
		action.ResponsesComputerToolCallAction = &computerToolCallAction
		return nil
	}
}

func (output ResponsesToolMessageOutputStruct) MarshalJSON() ([]byte, error) {
	if output.ResponsesToolCallOutputStr != nil {
		return MarshalSorted(*output.ResponsesToolCallOutputStr)
	}
	if output.ResponsesShellCallOutput != nil {
		return MarshalSorted(output.ResponsesShellCallOutput)
	}
	if output.ResponsesFunctionToolCallOutputBlocks != nil {
		return MarshalSorted(output.ResponsesFunctionToolCallOutputBlocks)
	}
	if output.ResponsesComputerToolCallOutput != nil {
		return MarshalSorted(output.ResponsesComputerToolCallOutput)
	}

	return MarshalSorted("")
}

func (output *ResponsesToolMessageOutputStruct) UnmarshalJSON(data []byte) error {
	var str string
	if err := Unmarshal(data, &str); err == nil {
		output.ResponsesToolCallOutputStr = &str
		return nil
	}
	if isShellCallOutputArray(data) {
		var shellOutput []ResponsesShellCallOutputContent
		if err := Unmarshal(data, &shellOutput); err == nil {
			output.ResponsesShellCallOutput = shellOutput
			return nil
		}
	}
	var array []ResponsesMessageContentBlock
	if err := Unmarshal(data, &array); err == nil {
		output.ResponsesFunctionToolCallOutputBlocks = array
		return nil
	}

	objectType := gjson.GetBytes(data, "type")
	isScreenshot := objectType.String() == "computer_screenshot" ||
		(!objectType.Exists() && (gjson.GetBytes(data, "image_url").Exists() || gjson.GetBytes(data, "file_id").Exists()))
	if isScreenshot {
		var computerToolCallOutput ResponsesComputerToolCallOutputData
		if err := Unmarshal(data, &computerToolCallOutput); err == nil {
			output.ResponsesComputerToolCallOutput = &computerToolCallOutput
			return nil
		}
	}
	var object map[string]interface{}
	if err := Unmarshal(data, &object); err == nil && object != nil {
		encoded, err := MarshalSorted(object)
		if err != nil {
			return fmt.Errorf("responses tool message output object could not be re-encoded: %w", err)
		}
		str := string(encoded)
		output.ResponsesToolCallOutputStr = &str
		return nil
	}
	return fmt.Errorf("responses tool message output struct is neither a string nor an array of responses message content blocks nor a computer tool call output data nor an image generation call output")
}

func (e ResponsesToolMessageError) MarshalJSON() ([]byte, error) {
	if e.ResponsesToolMessageErrorStr != nil && e.ResponsesToolMessageErrorStruct != nil {
		return nil, fmt.Errorf("both ResponsesToolMessageErrorStr and ResponsesToolMessageErrorStruct are set; only one should be non-nil")
	}
	if e.ResponsesToolMessageErrorStr != nil {
		return MarshalSorted(*e.ResponsesToolMessageErrorStr)
	}
	if e.ResponsesToolMessageErrorStruct != nil {
		return MarshalSorted(e.ResponsesToolMessageErrorStruct)
	}
	return MarshalSorted(nil)
}

func (e *ResponsesToolMessageError) UnmarshalJSON(data []byte) error {
	var str string
	if err := Unmarshal(data, &str); err == nil {
		*e = ResponsesToolMessageError{ResponsesToolMessageErrorStr: &str}
		return nil
	}
	var errStruct ResponsesToolMessageErrorStruct
	if err := Unmarshal(data, &errStruct); err == nil {
		*e = ResponsesToolMessageError{ResponsesToolMessageErrorStruct: &errStruct}
		return nil
	}
	return fmt.Errorf("responses tool message error is neither a string nor an error object")
}

func (e *ResponsesToolMessageError) IsError() bool {
	if e == nil {
		return false
	}
	if e.ResponsesToolMessageErrorStruct != nil {
		return true
	}
	return e.ResponsesToolMessageErrorStr != nil && *e.ResponsesToolMessageErrorStr != ""
}

func (e *ResponsesToolMessageError) Text() string {
	if e == nil {
		return ""
	}
	if e.ResponsesToolMessageErrorStr != nil {
		return *e.ResponsesToolMessageErrorStr
	}
	if s := e.ResponsesToolMessageErrorStruct; s != nil {
		if s.Message != nil {
			return *s.Message
		}
		if len(s.Content) > 0 && string(s.Content) != "null" {
			var str string
			if err := Unmarshal(s.Content, &str); err == nil {
				return str
			}
			return string(s.Content)
		}
		return s.Type
	}
	return ""
}

func (rf ResponsesFunctionToolCallOutput) MarshalJSON() ([]byte, error) {

	if rf.ResponsesFunctionToolCallOutputStr != nil && rf.ResponsesFunctionToolCallOutputBlocks != nil {
		return nil, fmt.Errorf("both ResponsesFunctionToolCallOutputStr and ResponsesFunctionToolCallOutputBlocks are set; only one should be non-nil")
	}

	if rf.ResponsesFunctionToolCallOutputStr != nil {
		return MarshalSorted(*rf.ResponsesFunctionToolCallOutputStr)
	}
	if rf.ResponsesFunctionToolCallOutputBlocks != nil {
		return MarshalSorted(rf.ResponsesFunctionToolCallOutputBlocks)
	}

	return MarshalSorted(nil)
}

func (rf *ResponsesFunctionToolCallOutput) UnmarshalJSON(data []byte) error {

	var genericObj map[string]interface{}
	if err := Unmarshal(data, &genericObj); err != nil {
		return err
	}

	hasContentFields := false
	for key := range genericObj {
		if key == "content" || key == "output" || key == "result" {
			hasContentFields = true
			break
		}
	}

	if !hasContentFields {
		return nil
	}

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		rf.ResponsesFunctionToolCallOutputStr = &stringContent
		return nil
	}

	var arrayContent []ResponsesMessageContentBlock
	if err := Unmarshal(data, &arrayContent); err == nil {
		rf.ResponsesFunctionToolCallOutputBlocks = arrayContent
		return nil
	}

	return fmt.Errorf("content field is neither a string nor an array of Content blocks")
}

const (
	ResponsesReasoningContentBlockTypeSummaryText ResponsesReasoningContentBlockType = "summary_text"
)

func (o ResponsesCodeInterpreterOutput) MarshalJSON() ([]byte, error) {

	if o.ResponsesCodeInterpreterOutputLogs != nil && o.ResponsesCodeInterpreterOutputImage != nil {
		return nil, fmt.Errorf("ResponsesCodeInterpreterOutput cannot have both Logs and Image set")
	}

	if o.ResponsesCodeInterpreterOutputLogs != nil {
		return MarshalSorted(o.ResponsesCodeInterpreterOutputLogs)
	}
	if o.ResponsesCodeInterpreterOutputImage != nil {
		return MarshalSorted(o.ResponsesCodeInterpreterOutputImage)
	}

	return []byte("null"), nil
}

func (o *ResponsesCodeInterpreterOutput) UnmarshalJSON(data []byte) error {

	if string(data) == "null" {
		return nil
	}

	var typeStruct struct {
		Type string `json:"type"`
	}
	if err := Unmarshal(data, &typeStruct); err != nil {
		return fmt.Errorf("failed to read type field: %w", err)
	}

	switch typeStruct.Type {
	case "logs":
		var logs ResponsesCodeInterpreterOutputLogs
		if err := Unmarshal(data, &logs); err != nil {
			return fmt.Errorf("failed to unmarshal logs output: %w", err)
		}
		o.ResponsesCodeInterpreterOutputLogs = &logs
		o.ResponsesCodeInterpreterOutputImage = nil
		return nil

	case "image":
		var image ResponsesCodeInterpreterOutputImage
		if err := Unmarshal(data, &image); err != nil {
			return fmt.Errorf("failed to unmarshal image output: %w", err)
		}
		o.ResponsesCodeInterpreterOutputImage = &image
		o.ResponsesCodeInterpreterOutputLogs = nil
		return nil

	default:
		return fmt.Errorf("unknown ResponsesCodeInterpreterOutput type: %s", typeStruct.Type)
	}
}

func ShellCallOutputText(output []ResponsesShellCallOutputContent) string {
	var sb strings.Builder
	for _, content := range output {
		for _, stream := range []string{content.Stdout, content.Stderr} {
			if stream == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(stream)
		}
	}
	return sb.String()
}

func isShellCallOutputArray(data []byte) bool {
	parsed := gjson.ParseBytes(data)
	if !parsed.IsArray() {
		return false
	}
	items := parsed.Array()
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !item.IsObject() || !item.Get("outcome").IsObject() {
			return false
		}
		if !item.Get("stdout").Exists() && !item.Get("stderr").Exists() {
			return false
		}
	}
	return true
}

const (
	ResponsesToolChoiceTypeNone ResponsesToolChoiceType = "none"

	ResponsesToolChoiceTypeAuto ResponsesToolChoiceType = "auto"

	ResponsesToolChoiceTypeAny ResponsesToolChoiceType = "any"

	ResponsesToolChoiceTypeRequired ResponsesToolChoiceType = "required"

	ResponsesToolChoiceTypeFunction ResponsesToolChoiceType = "function"

	ResponsesToolChoiceTypeAllowedTools ResponsesToolChoiceType = "allowed_tools"

	ResponsesToolChoiceTypeFileSearch ResponsesToolChoiceType = "file_search"

	ResponsesToolChoiceTypeWebSearchPreview ResponsesToolChoiceType = "web_search_preview"

	ResponsesToolChoiceTypeComputerUsePreview ResponsesToolChoiceType = "computer_use_preview"

	ResponsesToolChoiceTypeCodeInterpreter ResponsesToolChoiceType = "code_interpreter"

	ResponsesToolChoiceTypeImageGeneration ResponsesToolChoiceType = "image_generation"

	ResponsesToolChoiceTypeMCP ResponsesToolChoiceType = "mcp"

	ResponsesToolChoiceTypeCustom ResponsesToolChoiceType = "custom"
)

func (tc *ResponsesToolChoice) IsForced() bool {
	if tc == nil {
		return false
	}
	if tc.ResponsesToolChoiceStr != nil {
		return forcedResponsesToolChoiceMode(*tc.ResponsesToolChoiceStr)
	}
	if s := tc.ResponsesToolChoiceStruct; s != nil {
		switch s.Type {
		case ResponsesToolChoiceTypeNone, ResponsesToolChoiceTypeAuto:
			return false
		case ResponsesToolChoiceTypeAllowedTools:

			return s.Mode != nil && forcedResponsesToolChoiceMode(*s.Mode)
		case "":

			return s.Mode != nil && forcedResponsesToolChoiceMode(*s.Mode)
		default:
			return true
		}
	}
	return false
}

func forcedResponsesToolChoiceMode(mode string) bool {
	switch ResponsesToolChoiceType(mode) {
	case ResponsesToolChoiceTypeNone, ResponsesToolChoiceTypeAuto:
		return false
	default:
		return true
	}
}

func (tc ResponsesToolChoice) MarshalJSON() ([]byte, error) {

	if tc.ResponsesToolChoiceStr != nil && tc.ResponsesToolChoiceStruct != nil {
		return nil, fmt.Errorf("both ResponsesToolChoiceStr, ResponsesToolChoiceStruct are set; only one should be non-nil")
	}

	if tc.ResponsesToolChoiceStr != nil {
		return MarshalSorted(tc.ResponsesToolChoiceStr)
	}
	if tc.ResponsesToolChoiceStruct != nil {

		if s := tc.ResponsesToolChoiceStruct; s.Name == nil && s.ServerLabel == nil && len(s.Tools) == 0 {
			switch s.Type {
			case ResponsesToolChoiceTypeAuto, ResponsesToolChoiceTypeNone,
				ResponsesToolChoiceTypeRequired, ResponsesToolChoiceTypeAny:
				return MarshalSorted(string(s.Type))
			}
			if s.Mode != nil && s.Type == "" {
				return MarshalSorted(*s.Mode)
			}
		}
		return MarshalSorted(tc.ResponsesToolChoiceStruct)
	}

	return MarshalSorted(nil)
}

func (tc *ResponsesToolChoice) UnmarshalJSON(data []byte) error {

	var toolChoiceStr string
	if err := Unmarshal(data, &toolChoiceStr); err == nil {
		tc.ResponsesToolChoiceStr = &toolChoiceStr
		return nil
	}

	var responsesToolChoiceStruct ResponsesToolChoiceStruct
	if err := Unmarshal(data, &responsesToolChoiceStruct); err == nil {
		tc.ResponsesToolChoiceStruct = &responsesToolChoiceStruct
		return nil
	}

	return fmt.Errorf("tool_choice field is neither a string nor a ResponsesToolChoiceStruct object")
}

const (
	ResponsesToolTypeFunction                ResponsesToolType = "function"
	ResponsesToolTypeFileSearch              ResponsesToolType = "file_search"
	ResponsesToolTypeComputerUsePreview      ResponsesToolType = "computer_use_preview"
	ResponsesToolTypeComputer                ResponsesToolType = "computer"
	ResponsesToolTypeWebSearch               ResponsesToolType = "web_search"
	ResponsesToolTypeWebFetch                ResponsesToolType = "web_fetch"
	ResponsesToolTypeMCP                     ResponsesToolType = "mcp"
	ResponsesToolTypeCodeInterpreter         ResponsesToolType = "code_interpreter"
	ResponsesToolTypeImageGeneration         ResponsesToolType = "image_generation"
	ResponsesToolTypeLocalShell              ResponsesToolType = "local_shell"
	ResponsesToolTypeShell                   ResponsesToolType = "shell"
	ResponsesToolTypeProgrammaticToolCalling ResponsesToolType = "programmatic_tool_calling"
	ResponsesToolTypeApplyPatch              ResponsesToolType = "apply_patch"
	ResponsesToolTypeCustom                  ResponsesToolType = "custom"
	ResponsesToolTypeWebSearchPreview        ResponsesToolType = "web_search_preview"
	ResponsesToolTypeMemory                  ResponsesToolType = "memory"
	ResponsesToolTypeToolSearch              ResponsesToolType = "tool_search"
	ResponsesToolTypeNamespace               ResponsesToolType = "namespace"
	ResponsesToolTypeXSearch                 ResponsesToolType = "x_search"
	ResponsesToolTypeAdvisor                 ResponsesToolType = "advisor"
)

const ResponsesToolTypeOpenRouterPrefix = "openrouter:"

func normalizeResponsesToolType(t ResponsesToolType) ResponsesToolType {
	s := string(t)
	switch {

	case t == ResponsesToolTypeWebSearchPreview:
		return t
	case strings.HasPrefix(s, "web_search_preview"):
		return ResponsesToolTypeWebSearchPreview
	case t == ResponsesToolTypeWebSearch:
		return t
	case strings.HasPrefix(s, "web_search"):
		return ResponsesToolTypeWebSearch
	case t == ResponsesToolTypeWebFetch:
		return t
	case strings.HasPrefix(s, "web_fetch"):
		return ResponsesToolTypeWebFetch
	case t == ResponsesToolTypeComputer:
		return t
	case strings.HasPrefix(s, "computer") && t != ResponsesToolTypeComputerUsePreview:

		return ResponsesToolTypeComputerUsePreview
	case strings.HasPrefix(s, "code_execution"):
		return ResponsesToolTypeCodeInterpreter
	case strings.HasPrefix(s, "memory") && t != ResponsesToolTypeMemory:
		return ResponsesToolTypeMemory
	case strings.HasPrefix(s, "advisor") && t != ResponsesToolTypeAdvisor:

		return ResponsesToolTypeAdvisor
	case ToolSearchVariantName(s) != "":

		return ResponsesToolTypeToolSearch
	default:
		return t
	}
}

func ToolSearchVariantName(rawType string) string {
	const prefix = "tool_search_tool_"
	if !strings.HasPrefix(rawType, prefix) {
		return ""
	}

	variant := strings.TrimPrefix(rawType, prefix)
	switch {
	case variant == "regex" || strings.HasPrefix(variant, "regex_"):
		return "tool_search_tool_regex"
	case variant == "bm25" || strings.HasPrefix(variant, "bm25_"):
		return "tool_search_tool_bm25"
	default:
		return ""
	}
}

func mergeJSONFields(dst, src []byte) ([]byte, error) {
	var mergeErr error
	gjson.ParseBytes(src).ForEach(func(key, value gjson.Result) bool {
		dst, mergeErr = sjson.SetRawBytes(dst, key.String(), []byte(value.Raw))
		return mergeErr == nil
	})
	return dst, mergeErr
}

func (t ResponsesTool) MarshalJSON() ([]byte, error) {

	data := []byte(`{}`)
	var err error

	if data, err = sjson.SetBytes(data, "type", t.Type); err != nil {
		return nil, err
	}
	if t.Name != nil {
		if data, err = sjson.SetBytes(data, "name", *t.Name); err != nil {
			return nil, err
		}
	}
	if t.Description != nil {
		if data, err = sjson.SetBytes(data, "description", *t.Description); err != nil {
			return nil, err
		}
	}
	if t.Async != nil {
		if data, err = sjson.SetBytes(data, "async", *t.Async); err != nil {
			return nil, err
		}
	}
	if t.CacheControl != nil {
		ccBytes, ccErr := MarshalSorted(t.CacheControl)
		if ccErr != nil {
			return nil, ccErr
		}
		if data, err = sjson.SetRawBytes(data, "cache_control", ccBytes); err != nil {
			return nil, err
		}
	}

	if t.DeferLoading != nil {
		if data, err = sjson.SetBytes(data, "defer_loading", *t.DeferLoading); err != nil {
			return nil, err
		}
	}
	if len(t.AllowedCallers) > 0 {
		callersBytes, callersErr := MarshalSorted(t.AllowedCallers)
		if callersErr != nil {
			return nil, callersErr
		}
		if data, err = sjson.SetRawBytes(data, "allowed_callers", callersBytes); err != nil {
			return nil, err
		}
	}
	if len(t.InputExamples) > 0 {
		examplesBytes, examplesErr := MarshalSorted(t.InputExamples)
		if examplesErr != nil {
			return nil, examplesErr
		}
		if data, err = sjson.SetRawBytes(data, "input_examples", examplesBytes); err != nil {
			return nil, err
		}
	}
	if t.EagerInputStreaming != nil {
		if data, err = sjson.SetBytes(data, "eager_input_streaming", *t.EagerInputStreaming); err != nil {
			return nil, err
		}
	}
	if t.MaxCharacters != nil {
		if data, err = sjson.SetBytes(data, "max_characters", *t.MaxCharacters); err != nil {
			return nil, err
		}
	}

	var typeBytes []byte
	switch t.Type {
	case ResponsesToolTypeFunction:
		if t.ResponsesToolFunction != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolFunction)
		}
	case ResponsesToolTypeFileSearch:
		if t.ResponsesToolFileSearch != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolFileSearch)
		}
	case ResponsesToolTypeComputerUsePreview:
		if t.ResponsesToolComputerUsePreview != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolComputerUsePreview)
		}
	case ResponsesToolTypeWebSearch:
		if t.ResponsesToolWebSearch != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolWebSearch)
		}
	case ResponsesToolTypeWebFetch:
		if t.ResponsesToolWebFetch != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolWebFetch)
		}
	case ResponsesToolTypeMCP:
		if t.ResponsesToolMCP != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolMCP)
		}
	case ResponsesToolTypeCodeInterpreter:
		if t.ResponsesToolCodeInterpreter != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolCodeInterpreter)
		}
	case ResponsesToolTypeImageGeneration:
		if t.ResponsesToolImageGeneration != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolImageGeneration)
		}
	case ResponsesToolTypeLocalShell:
		if t.ResponsesToolLocalShell != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolLocalShell)
		}
	case ResponsesToolTypeShell:
		if t.ResponsesToolShell != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolShell)
		}
	case ResponsesToolTypeCustom:
		if t.ResponsesToolCustom != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolCustom)
		}
	case ResponsesToolTypeWebSearchPreview:
		if t.ResponsesToolWebSearchPreview != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolWebSearchPreview)
		}
	case ResponsesToolTypeToolSearch:
		if t.ResponsesToolToolSearch != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolToolSearch)
		}
	case ResponsesToolTypeNamespace:
		if t.ResponsesToolNamespace != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolNamespace)
		}
	case ResponsesToolTypeXSearch:
		if t.ResponsesToolXSearch != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolXSearch)
		}
	case ResponsesToolTypeAdvisor:
		if t.ResponsesToolAdvisor != nil {
			typeBytes, err = MarshalSorted(t.ResponsesToolAdvisor)
		}
	}
	if err != nil {
		return nil, err
	}

	if typeBytes != nil {
		data, err = mergeJSONFields(data, typeBytes)
		if err != nil {
			return nil, err
		}
	}

	return data, nil
}

func (t *ResponsesTool) UnmarshalJSON(data []byte) error {

	if !gjson.ValidBytes(data) {
		return fmt.Errorf("invalid JSON in ResponsesTool")
	}

	fields := gjson.GetManyBytes(data,
		"type",
		"name",
		"description",
		"cache_control",
		"defer_loading",
		"allowed_callers",
		"input_examples",
		"eager_input_streaming",
		"function",
		"async",
		"max_characters",
	)

	typeField := fields[0]
	if !typeField.Exists() {
		return fmt.Errorf("missing required 'type' field in ResponsesTool")
	}
	if typeField.Type != gjson.String {
		return fmt.Errorf("'type' field must be a string")
	}
	typeStr := typeField.String()
	t.Type = normalizeResponsesToolType(ResponsesToolType(typeStr))

	if v := fields[1]; v.Type == gjson.String {
		t.Name = new(v.String())
	}
	if v := fields[2]; v.Type == gjson.String {
		t.Description = new(v.String())
	}
	if v := fields[9]; v.IsBool() {
		t.Async = new(v.Bool())
	}
	if v := fields[3]; v.Exists() {
		var cc CacheControl
		if err := Unmarshal([]byte(v.Raw), &cc); err != nil {
			return err
		}
		t.CacheControl = &cc
	}

	if v := fields[4]; v.IsBool() {
		t.DeferLoading = new(v.Bool())
	}
	if v := fields[5]; v.Exists() {
		if err := Unmarshal([]byte(v.Raw), &t.AllowedCallers); err != nil {
			return err
		}
	}
	if v := fields[6]; v.Exists() {
		if err := Unmarshal([]byte(v.Raw), &t.InputExamples); err != nil {
			return err
		}
	}
	if v := fields[7]; v.IsBool() {
		t.EagerInputStreaming = new(v.Bool())
	}
	if v := fields[10]; v.Type == gjson.Number {
		t.MaxCharacters = new(int(v.Int()))
	}

	if t.Type == ResponsesToolTypeToolSearch && t.Name == nil {
		if variant := ToolSearchVariantName(typeStr); variant != "" {
			t.Name = new(variant)
		}
	}

	switch t.Type {
	case ResponsesToolTypeFunction:
		var funcTool ResponsesToolFunction
		if err := Unmarshal(data, &funcTool); err != nil {
			return err
		}

		if wrapper := fields[8]; wrapper.Exists() && wrapper.Type != gjson.Null {
			var nested struct {
				Name        *string                 `json:"name"`
				Description *string                 `json:"description"`
				Parameters  *ToolFunctionParameters `json:"parameters"`
				Strict      *bool                   `json:"strict"`
			}
			if err := Unmarshal([]byte(wrapper.Raw), &nested); err != nil {
				return fmt.Errorf("invalid 'function' object in ResponsesTool: %w", err)
			}
			if t.Name == nil {
				t.Name = nested.Name
			}
			if t.Description == nil {
				t.Description = nested.Description
			}
			if funcTool.Parameters == nil {
				funcTool.Parameters = nested.Parameters
			}
			if funcTool.Strict == nil {
				funcTool.Strict = nested.Strict
			}
		}
		t.ResponsesToolFunction = &funcTool

	case ResponsesToolTypeFileSearch:
		var fileSearchTool ResponsesToolFileSearch
		if err := Unmarshal(data, &fileSearchTool); err != nil {
			return err
		}
		t.ResponsesToolFileSearch = &fileSearchTool

	case ResponsesToolTypeComputerUsePreview:
		var computerTool ResponsesToolComputerUsePreview
		if err := Unmarshal(data, &computerTool); err != nil {
			return err
		}
		t.ResponsesToolComputerUsePreview = &computerTool

	case ResponsesToolTypeWebSearch:
		var webSearchTool ResponsesToolWebSearch
		if err := Unmarshal(data, &webSearchTool); err != nil {
			return err
		}
		t.ResponsesToolWebSearch = &webSearchTool

	case ResponsesToolTypeWebFetch:
		var webFetchTool ResponsesToolWebFetch
		if err := Unmarshal(data, &webFetchTool); err != nil {
			return err
		}
		t.ResponsesToolWebFetch = &webFetchTool

	case ResponsesToolTypeMCP:
		var mcpTool ResponsesToolMCP
		if err := Unmarshal(data, &mcpTool); err != nil {
			return err
		}
		t.ResponsesToolMCP = &mcpTool

	case ResponsesToolTypeCodeInterpreter:
		var codeInterpreterTool ResponsesToolCodeInterpreter
		if err := Unmarshal(data, &codeInterpreterTool); err != nil {
			return err
		}
		t.ResponsesToolCodeInterpreter = &codeInterpreterTool

	case ResponsesToolTypeImageGeneration:
		var imageGenTool ResponsesToolImageGeneration
		if err := Unmarshal(data, &imageGenTool); err != nil {
			return err
		}
		t.ResponsesToolImageGeneration = &imageGenTool

	case ResponsesToolTypeLocalShell:
		var localShellTool ResponsesToolLocalShell
		if err := Unmarshal(data, &localShellTool); err != nil {
			return err
		}
		t.ResponsesToolLocalShell = &localShellTool

	case ResponsesToolTypeShell:
		var shellTool ResponsesToolShell
		if err := Unmarshal(data, &shellTool); err != nil {
			return err
		}
		t.ResponsesToolShell = &shellTool

	case ResponsesToolTypeCustom:
		var customTool ResponsesToolCustom
		if err := Unmarshal(data, &customTool); err != nil {
			return err
		}
		t.ResponsesToolCustom = &customTool

	case ResponsesToolTypeWebSearchPreview:
		var webSearchPreviewTool ResponsesToolWebSearchPreview
		if err := Unmarshal(data, &webSearchPreviewTool); err != nil {
			return err
		}
		t.ResponsesToolWebSearchPreview = &webSearchPreviewTool

	case ResponsesToolTypeToolSearch:
		var toolSearchTool ResponsesToolToolSearch
		if err := Unmarshal(data, &toolSearchTool); err != nil {
			return err
		}
		t.ResponsesToolToolSearch = &toolSearchTool

	case ResponsesToolTypeNamespace:
		var namespaceTool ResponsesToolNamespace
		if err := Unmarshal(data, &namespaceTool); err != nil {
			return err
		}
		t.ResponsesToolNamespace = &namespaceTool

	case ResponsesToolTypeXSearch:
		var xSearchTool ResponsesToolXSearch
		if err := Unmarshal(data, &xSearchTool); err != nil {
			return err
		}
		t.ResponsesToolXSearch = &xSearchTool

	case ResponsesToolTypeAdvisor:
		var advisorTool ResponsesToolAdvisor
		if err := Unmarshal(data, &advisorTool); err != nil {
			return err
		}
		t.ResponsesToolAdvisor = &advisorTool
	}

	return nil
}

func (f *ResponsesToolFileSearchFilter) MarshalJSON() ([]byte, error) {

	if f.ResponsesToolFileSearchComparisonFilter != nil && f.ResponsesToolFileSearchCompoundFilter != nil {
		return nil, fmt.Errorf("both comparison and compound filters are set; only one should be non-nil")
	}
	if f.ResponsesToolFileSearchComparisonFilter == nil && f.ResponsesToolFileSearchCompoundFilter == nil {
		return nil, fmt.Errorf("neither comparison nor compound filter is set; exactly one must be non-nil")
	}

	data := []byte(`{}`)
	var err error

	if data, err = sjson.SetBytes(data, "type", f.Type); err != nil {
		return nil, err
	}

	switch f.Type {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "nin":
		if f.ResponsesToolFileSearchComparisonFilter == nil {
			return nil, fmt.Errorf("comparison filter is nil but type is %s", f.Type)
		}
		if data, err = sjson.SetBytes(data, "key", f.ResponsesToolFileSearchComparisonFilter.Key); err != nil {
			return nil, err
		}
		if data, err = sjson.SetBytes(data, "value", f.ResponsesToolFileSearchComparisonFilter.Value); err != nil {
			return nil, err
		}
	case "and", "or":
		if f.ResponsesToolFileSearchCompoundFilter == nil {
			return nil, fmt.Errorf("compound filter is nil but type is %s", f.Type)
		}
		filtersBytes, fErr := MarshalSorted(f.ResponsesToolFileSearchCompoundFilter.Filters)
		if fErr != nil {
			return nil, fErr
		}
		if data, err = sjson.SetRawBytes(data, "filters", filtersBytes); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown filter type: %s", f.Type)
	}

	return data, nil
}

func (f *ResponsesToolFileSearchFilter) UnmarshalJSON(data []byte) error {

	var raw map[string]interface{}
	if err := Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal filter JSON: %w", err)
	}

	typeValue, ok := raw["type"]
	if !ok {
		return fmt.Errorf("missing required 'type' field in filter")
	}

	typeStr, ok := typeValue.(string)
	if !ok {
		return fmt.Errorf("'type' field must be a string, got %T", typeValue)
	}

	f.Type = typeStr

	switch typeStr {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "nin":

		f.ResponsesToolFileSearchComparisonFilter = &ResponsesToolFileSearchComparisonFilter{}
		f.ResponsesToolFileSearchCompoundFilter = nil

		if err := Unmarshal(data, f.ResponsesToolFileSearchComparisonFilter); err != nil {
			return fmt.Errorf("failed to unmarshal comparison filter: %w", err)
		}

		if f.ResponsesToolFileSearchComparisonFilter.Key == "" {
			return fmt.Errorf("comparison filter missing required 'key' field")
		}
		if f.ResponsesToolFileSearchComparisonFilter.Value == nil {
			return fmt.Errorf("comparison filter missing required 'value' field")
		}

	case "and", "or":

		f.ResponsesToolFileSearchCompoundFilter = &ResponsesToolFileSearchCompoundFilter{}
		f.ResponsesToolFileSearchComparisonFilter = nil

		if err := Unmarshal(data, f.ResponsesToolFileSearchCompoundFilter); err != nil {
			return fmt.Errorf("failed to unmarshal compound filter: %w", err)
		}

		if f.ResponsesToolFileSearchCompoundFilter.Filters == nil {
			return fmt.Errorf("compound filter missing required 'filters' field")
		}
		if len(f.ResponsesToolFileSearchCompoundFilter.Filters) == 0 {
			return fmt.Errorf("compound filter 'filters' array cannot be empty")
		}

	default:
		return fmt.Errorf("unknown filter type: %s (supported types: eq, ne, gt, gte, lt, lte, in, nin, and, or)", typeStr)
	}

	return nil
}

func (i *Interval) UnmarshalJSON(data []byte) error {
	type Alias Interval
	aux := &struct {
		StartTime *time.Time `json:"start_time,omitempty"`
		EndTime   *time.Time `json:"end_time,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(i),
	}

	if err := Unmarshal(data, &aux); err != nil {
		return err
	}

	if !reflect.ValueOf(aux.StartTime).IsZero() {
		i.StartTime = time.Time(*aux.StartTime)
	}

	if !reflect.ValueOf(aux.EndTime).IsZero() {
		i.EndTime = time.Time(*aux.EndTime)
	}

	return nil
}

func (i *Interval) MarshalJSON() ([]byte, error) {
	type Alias Interval
	aux := &struct {
		StartTime *time.Time `json:"start_time,omitempty"`
		EndTime   *time.Time `json:"end_time,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(i),
	}

	if !reflect.ValueOf(i.StartTime).IsZero() {
		aux.StartTime = (*time.Time)(&i.StartTime)
	}

	if !reflect.ValueOf(i.EndTime).IsZero() {
		aux.EndTime = (*time.Time)(&i.EndTime)
	}

	return MarshalSorted(aux)
}

func (a ResponsesToolMCPAllowedTools) MarshalJSON() ([]byte, error) {
	if a.ToolNames != nil && a.Filter != nil {
		return nil, fmt.Errorf("both ToolNames and Filter are set; only one should be non-nil")
	}
	if a.ToolNames != nil {
		return MarshalSorted(a.ToolNames)
	}
	if a.Filter != nil {
		return MarshalSorted(a.Filter)
	}
	return MarshalSorted(nil)
}

func (a *ResponsesToolMCPAllowedTools) UnmarshalJSON(data []byte) error {
	var toolNames []string
	if err := Unmarshal(data, &toolNames); err == nil {
		*a = ResponsesToolMCPAllowedTools{ToolNames: toolNames}
		return nil
	}
	var filter ResponsesToolMCPAllowedToolsFilter
	if err := Unmarshal(data, &filter); err == nil {
		*a = ResponsesToolMCPAllowedTools{Filter: &filter}
		return nil
	}
	return fmt.Errorf("mcp allowed_tools is neither an array of tool names nor a filter object")
}

func (as ResponsesToolMCPAllowedToolsApprovalSetting) MarshalJSON() ([]byte, error) {

	if as.Setting != nil && (as.Always != nil || as.Never != nil) {
		return nil, fmt.Errorf("only one of 'Setting' or ('Always'/'Never') can be set")
	}

	if as.Setting != nil {
		return MarshalSorted(*as.Setting)
	}
	if as.Always != nil || as.Never != nil {

		data := []byte(`{}`)
		var err error
		if as.Always != nil {
			alwaysBytes, aErr := MarshalSorted(as.Always)
			if aErr != nil {
				return nil, aErr
			}
			if data, err = sjson.SetRawBytes(data, "always", alwaysBytes); err != nil {
				return nil, err
			}
		}
		if as.Never != nil {
			neverBytes, nErr := MarshalSorted(as.Never)
			if nErr != nil {
				return nil, nErr
			}
			if data, err = sjson.SetRawBytes(data, "never", neverBytes); err != nil {
				return nil, err
			}
		}
		return data, nil
	}

	return MarshalSorted(nil)
}

func (as *ResponsesToolMCPAllowedToolsApprovalSetting) UnmarshalJSON(data []byte) error {

	var settingStr string
	if err := Unmarshal(data, &settingStr); err == nil {
		as.Setting = &settingStr
		return nil
	}

	var obj struct {
		Always *ResponsesToolMCPAllowedToolsApprovalFilter `json:"always,omitempty"`
		Never  *ResponsesToolMCPAllowedToolsApprovalFilter `json:"never,omitempty"`
	}
	if err := Unmarshal(data, &obj); err == nil {
		as.Always = obj.Always
		as.Never = obj.Never
		return nil
	}

	return fmt.Errorf("require_approval field is neither a string nor an object with always/never filters")
}

const (
	ResponsesStreamResponseTypePing ResponsesStreamResponseType = "response.ping"

	ResponsesStreamResponseTypeProviderRawEvent ResponsesStreamResponseType = "response.provider_raw_event"

	ResponsesStreamResponseTypeSafeguardsUpdate ResponsesStreamResponseType = "response.safeguards_update"

	ResponsesStreamResponseTypeCreated    ResponsesStreamResponseType = "response.created"
	ResponsesStreamResponseTypeInProgress ResponsesStreamResponseType = "response.in_progress"
	ResponsesStreamResponseTypeCompleted  ResponsesStreamResponseType = "response.completed"
	ResponsesStreamResponseTypeFailed     ResponsesStreamResponseType = "response.failed"
	ResponsesStreamResponseTypeIncomplete ResponsesStreamResponseType = "response.incomplete"

	ResponsesStreamResponseTypeOutputItemAdded ResponsesStreamResponseType = "response.output_item.added"
	ResponsesStreamResponseTypeOutputItemDone  ResponsesStreamResponseType = "response.output_item.done"

	ResponsesStreamResponseTypeContentPartAdded ResponsesStreamResponseType = "response.content_part.added"
	ResponsesStreamResponseTypeContentPartDone  ResponsesStreamResponseType = "response.content_part.done"

	ResponsesStreamResponseTypeOutputTextDelta ResponsesStreamResponseType = "response.output_text.delta"
	ResponsesStreamResponseTypeOutputTextDone  ResponsesStreamResponseType = "response.output_text.done"

	ResponsesStreamResponseTypeRefusalDelta ResponsesStreamResponseType = "response.refusal.delta"
	ResponsesStreamResponseTypeRefusalDone  ResponsesStreamResponseType = "response.refusal.done"

	ResponsesStreamResponseTypeFunctionCallArgumentsDelta     ResponsesStreamResponseType = "response.function_call_arguments.delta"
	ResponsesStreamResponseTypeFunctionCallArgumentsDone      ResponsesStreamResponseType = "response.function_call_arguments.done"
	ResponsesStreamResponseTypeFileSearchCallInProgress       ResponsesStreamResponseType = "response.file_search_call.in_progress"
	ResponsesStreamResponseTypeFileSearchCallSearching        ResponsesStreamResponseType = "response.file_search_call.searching"
	ResponsesStreamResponseTypeFileSearchCallResultsAdded     ResponsesStreamResponseType = "response.file_search_call.results.added"
	ResponsesStreamResponseTypeFileSearchCallResultsCompleted ResponsesStreamResponseType = "response.file_search_call.results.completed"
	ResponsesStreamResponseTypeWebSearchCallInProgress        ResponsesStreamResponseType = "response.web_search_call.in_progress"
	ResponsesStreamResponseTypeWebSearchCallSearching         ResponsesStreamResponseType = "response.web_search_call.searching"
	ResponsesStreamResponseTypeWebSearchCallCompleted         ResponsesStreamResponseType = "response.web_search_call.completed"
	ResponsesStreamResponseTypeWebSearchCallResultsAdded      ResponsesStreamResponseType = "response.web_search_call.results.added"
	ResponsesStreamResponseTypeWebSearchCallResultsCompleted  ResponsesStreamResponseType = "response.web_search_call.results.completed"

	ResponsesStreamResponseTypeWebFetchCallInProgress ResponsesStreamResponseType = "response.web_fetch_call.in_progress"
	ResponsesStreamResponseTypeWebFetchCallFetching   ResponsesStreamResponseType = "response.web_fetch_call.fetching"
	ResponsesStreamResponseTypeWebFetchCallCompleted  ResponsesStreamResponseType = "response.web_fetch_call.completed"

	ResponsesStreamResponseTypeReasoningSummaryPartAdded ResponsesStreamResponseType = "response.reasoning_summary_part.added"
	ResponsesStreamResponseTypeReasoningSummaryPartDone  ResponsesStreamResponseType = "response.reasoning_summary_part.done"
	ResponsesStreamResponseTypeReasoningSummaryTextDelta ResponsesStreamResponseType = "response.reasoning_summary_text.delta"
	ResponsesStreamResponseTypeReasoningSummaryTextDone  ResponsesStreamResponseType = "response.reasoning_summary_text.done"

	ResponsesStreamResponseTypeImageGenerationCallCompleted    ResponsesStreamResponseType = "response.image_generation_call.completed"
	ResponsesStreamResponseTypeImageGenerationCallGenerating   ResponsesStreamResponseType = "response.image_generation_call.generating"
	ResponsesStreamResponseTypeImageGenerationCallInProgress   ResponsesStreamResponseType = "response.image_generation_call.in_progress"
	ResponsesStreamResponseTypeImageGenerationCallPartialImage ResponsesStreamResponseType = "response.image_generation_call.partial_image"

	ResponsesStreamResponseTypeMCPCallArgumentsDelta  ResponsesStreamResponseType = "response.mcp_call_arguments.delta"
	ResponsesStreamResponseTypeMCPCallArgumentsDone   ResponsesStreamResponseType = "response.mcp_call_arguments.done"
	ResponsesStreamResponseTypeMCPCallCompleted       ResponsesStreamResponseType = "response.mcp_call.completed"
	ResponsesStreamResponseTypeMCPCallFailed          ResponsesStreamResponseType = "response.mcp_call.failed"
	ResponsesStreamResponseTypeMCPCallInProgress      ResponsesStreamResponseType = "response.mcp_call.in_progress"
	ResponsesStreamResponseTypeMCPListToolsCompleted  ResponsesStreamResponseType = "response.mcp_list_tools.completed"
	ResponsesStreamResponseTypeMCPListToolsFailed     ResponsesStreamResponseType = "response.mcp_list_tools.failed"
	ResponsesStreamResponseTypeMCPListToolsInProgress ResponsesStreamResponseType = "response.mcp_list_tools.in_progress"

	ResponsesStreamResponseTypeCodeInterpreterCallInProgress   ResponsesStreamResponseType = "response.code_interpreter_call.in_progress"
	ResponsesStreamResponseTypeCodeInterpreterCallInterpreting ResponsesStreamResponseType = "response.code_interpreter_call.interpreting"
	ResponsesStreamResponseTypeCodeInterpreterCallCompleted    ResponsesStreamResponseType = "response.code_interpreter_call.completed"
	ResponsesStreamResponseTypeCodeInterpreterCallCodeDelta    ResponsesStreamResponseType = "response.code_interpreter_call_code.delta"
	ResponsesStreamResponseTypeCodeInterpreterCallCodeDone     ResponsesStreamResponseType = "response.code_interpreter_call_code.done"

	ResponsesStreamResponseTypeOutputTextAnnotationAdded ResponsesStreamResponseType = "response.output_text.annotation.added"
	ResponsesStreamResponseTypeOutputTextAnnotationDone  ResponsesStreamResponseType = "response.output_text.annotation.done"

	ResponsesStreamResponseTypeQueued ResponsesStreamResponseType = "response.queued"

	ResponsesStreamResponseTypeCustomToolCallInputDelta ResponsesStreamResponseType = "response.custom_tool_call_input.delta"
	ResponsesStreamResponseTypeCustomToolCallInputDone  ResponsesStreamResponseType = "response.custom_tool_call_input.done"

	ResponsesStreamResponseTypeShellCallCommandAdded       ResponsesStreamResponseType = "response.shell_call_command.added"
	ResponsesStreamResponseTypeShellCallCommandDelta       ResponsesStreamResponseType = "response.shell_call_command.delta"
	ResponsesStreamResponseTypeShellCallCommandDone        ResponsesStreamResponseType = "response.shell_call_command.done"
	ResponsesStreamResponseTypeShellCallOutputContentDelta ResponsesStreamResponseType = "response.shell_call_output_content.delta"
	ResponsesStreamResponseTypeShellCallOutputContentDone  ResponsesStreamResponseType = "response.shell_call_output_content.done"

	ResponsesStreamResponseTypeApplyPatchCallOperationDiffDelta ResponsesStreamResponseType = "response.apply_patch_call_operation_diff.delta"
	ResponsesStreamResponseTypeApplyPatchCallOperationDiffDone  ResponsesStreamResponseType = "response.apply_patch_call_operation_diff.done"

	ResponsesStreamResponseTypeError ResponsesStreamResponseType = "error"
)

func (resp BifrostResponsesStreamResponse) MarshalJSON() ([]byte, error) {
	type alias BifrostResponsesStreamResponse

	if resp.Type == ResponsesStreamResponseTypeReasoningSummaryTextDelta && resp.Delta == nil {
		resp.Delta = new("")
	}
	encoded, err := Marshal(alias(resp))
	if err != nil {
		return nil, err
	}
	if resp.LogProbs == nil && gjson.GetBytes(encoded, "logprobs").Exists() {
		if encoded, err = sjson.DeleteBytes(encoded, "logprobs"); err != nil {
			return nil, err
		}
	}
	if resp.ShellOutputDelta != nil {
		deltaBytes, deltaErr := MarshalSorted(resp.ShellOutputDelta)
		if deltaErr != nil {
			return nil, deltaErr
		}
		return sjson.SetRawBytes(encoded, "delta", deltaBytes)
	}
	return encoded, nil
}

func UnmarshalResponsesStreamObjectDelta(data []byte, resp *BifrostResponsesStreamResponse) error {
	delta := gjson.GetBytes(data, "delta")
	if !delta.IsObject() {
		return fmt.Errorf("stream event delta is not an object")
	}
	stripped, err := sjson.DeleteBytes(append([]byte(nil), data...), "delta")
	if err != nil {
		return err
	}
	if err := Unmarshal(stripped, resp); err != nil {
		return err
	}
	var shellDelta ResponsesShellCallOutputDelta
	if err := Unmarshal([]byte(delta.Raw), &shellDelta); err != nil {
		return err
	}
	resp.ShellOutputDelta = &shellDelta
	return nil
}
