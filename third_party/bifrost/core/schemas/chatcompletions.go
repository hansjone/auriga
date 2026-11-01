// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"

	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ChatStreamOptions struct {
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
	IncludeUsage       *bool `json:"include_usage,omitempty"`
}
type ChatToolInputExample struct {
	Input       json.RawMessage `json:"input"`
	Description *string         `json:"description,omitempty"`
}
type MCPToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}
type ChatWebSearchOptionsFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`

	TimeRangeFilter *Interval `json:"time_range_filter,omitempty"`
}
type ChatContainerObject struct {
	ID     *string              `json:"id,omitempty"`
	Skills []ChatContainerSkill `json:"skills,omitempty"`
}
type ChatToolCustom struct {
	Format *ChatToolCustomFormat `json:"format,omitempty"`
}
type ChatStreamResponseChoiceDelta struct {
	Role             *string                          `json:"role,omitempty"`
	Content          *string                          `json:"content,omitempty"`
	Refusal          *string                          `json:"refusal,omitempty"`
	Audio            *ChatAudioMessageAudio           `json:"audio,omitempty"`
	Images           []ChatAssistantMessageImage      `json:"images,omitempty"`
	Reasoning        *string                          `json:"reasoning,omitempty"`
	ReasoningDetails []ChatReasoningDetails           `json:"reasoning_details,omitempty"`
	Annotations      []ChatAssistantMessageAnnotation `json:"annotations,omitempty"`
	ToolCalls        []ChatAssistantMessageToolCall   `json:"tool_calls,omitempty"`
	ExtraContent     json.RawMessage                  `json:"extra_content,omitempty"`
}
type BifrostServiceTier string
type ChatToolFunction struct {
	Name        string                  `json:"name"`
	Description *string                 `json:"description,omitempty"`
	Parameters  *ToolFunctionParameters `json:"parameters,omitempty"`
	Strict      *bool                   `json:"strict,omitempty"`
}
type ChatToolChoiceAllowedTools struct {
	Mode  string                           `json:"mode"`
	Tools []ChatToolChoiceAllowedToolsTool `json:"tools"`
}
type ChatReasoningDetails struct {
	ID        *string                     `json:"id,omitempty"`
	Index     int                         `json:"index"`
	Type      BifrostReasoningDetailsType `json:"type"`
	Summary   *string                     `json:"summary,omitempty"`
	Text      *string                     `json:"text,omitempty"`
	Signature *string                     `json:"signature,omitempty"`
	Data      *string                     `json:"data,omitempty"`
}
type ChatAudioMessageAudio struct {
	ID         string `json:"id"`
	Data       string `json:"data"`
	ExpiresAt  int    `json:"expires_at"`
	Transcript string `json:"transcript"`
}
type ChatTaskBudget struct {
	Type      string `json:"type"`
	Total     int    `json:"total"`
	Remaining *int   `json:"remaining,omitempty"`
}
type BifrostChatRequest struct {
	Provider       ModelProvider   `json:"provider"`
	Model          string          `json:"model"`
	Input          []ChatMessage   `json:"input,omitempty"`
	Params         *ChatParameters `json:"params,omitempty"`
	Fallbacks      []Fallback      `json:"fallbacks,omitempty"`
	RawRequestBody []byte          `json:"-"`
}
type BifrostChatResponse struct {
	ID                  string                     `json:"id"`
	Choices             []BifrostResponseChoice    `json:"choices"`
	Created             int                        `json:"created"`
	Model               string                     `json:"model"`
	Object              string                     `json:"object"`
	ServiceTier         *BifrostServiceTier        `json:"service_tier,omitempty"`
	Speed               *string                    `json:"speed,omitempty"`
	InferenceGeo        *string                    `json:"inference_geo,omitempty"`
	Diagnostics         *CacheDiagnostics          `json:"diagnostics,omitempty"`
	SystemFingerprint   string                     `json:"system_fingerprint"`
	PromptFilterResults json.RawMessage            `json:"prompt_filter_results,omitempty"`
	Usage               *BifrostLLMUsage           `json:"usage"`
	ExtraFields         BifrostResponseExtraFields `json:"extra_fields"`
	ExtraParams         map[string]interface{}     `json:"-"`

	SearchResults []SearchResult `json:"search_results,omitempty"`
	Videos        []VideoResult  `json:"videos,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
}
type ChatAssistantMessageAnnotationCitation struct {
	StartIndex int          `json:"start_index"`
	EndIndex   int          `json:"end_index"`
	Title      string       `json:"title"`
	URL        *string      `json:"url,omitempty"`
	Text       *string      `json:"text,omitempty"`
	Sources    *interface{} `json:"sources,omitempty"`
	Type       *string      `json:"type,omitempty"`
}
type ChatToolCustomGrammarFormat struct {
	Definition string `json:"definition"`
	Syntax     string `json:"syntax"`
}
type ChatReasoning struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	Effort    *string `json:"effort,omitempty"`
	MaxTokens *int    `json:"max_tokens,omitempty"`
	Display   *string `json:"display,omitempty"`
	Type      *string `json:"type,omitempty"`
	Mode      *string `json:"mode,omitempty"`
	Summary   *string `json:"summary,omitempty"`
}
type ChatToolChoiceCustom struct {
	Name string `json:"name"`
}
type ChatPrediction struct {
	Type    string      `json:"type"`
	Content interface{} `json:"content"`
}
type ChatWebSearchOptionsUserLocation struct {
	Type        string                                       `json:"type"`
	Approximate *ChatWebSearchOptionsUserLocationApproximate `json:"approximate,omitempty"`
}
type ChatCompletionTokensDetails struct {
	TextTokens               int  `json:"text_tokens,omitempty"`
	AcceptedPredictionTokens int  `json:"accepted_prediction_tokens,omitempty"`
	AudioTokens              int  `json:"audio_tokens,omitempty"`
	CitationTokens           *int `json:"citation_tokens,omitempty"`

	NumSearchQueries         *int `json:"num_search_queries,omitempty"`
	ReasoningTokens          int  `json:"reasoning_tokens,omitempty"`
	ImageTokens              *int `json:"image_tokens,omitempty"`
	RejectedPredictionTokens int  `json:"rejected_prediction_tokens,omitempty"`

	StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens,omitempty"`
	CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens,omitempty"`
}
type BifrostLLMUsage struct {
	PromptTokens            int                          `json:"prompt_tokens,omitempty"`
	PromptTokensDetails     *ChatPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokens        int                          `json:"completion_tokens,omitempty"`
	CompletionTokensDetails *ChatCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	ReasoningTokens         int                          `json:"reasoning_tokens,omitempty"`
	TotalTokens             int                          `json:"total_tokens"`

	AudioSeconds *float64 `json:"audio_seconds,omitempty"`

	SearchUnits *int         `json:"search_units,omitempty"`
	ToolUsage   *ToolUsage   `json:"tool_usage,omitempty"`
	Cost        *BifrostCost `json:"cost,omitempty"`

	CostInUsdTicks *int64 `json:"cost_in_usd_ticks,omitempty"`

	StateTokens        *int     `json:"state_tokens,omitempty"`
	StateTokensDropped *int     `json:"state_tokens_dropped,omitempty"`
	Truncated          *bool    `json:"truncated,omitempty"`
	TruncatedQuestions []string `json:"truncated_questions,omitempty"`

	Speed        *string `json:"-"`
	InferenceGeo *string `json:"-"`

	ServerSideFallbackModel *string                  `json:"-"`
	Iterations              []ResponsesResponseUsage `json:"-"`
}
type ChatToolChoice struct {
	ChatToolChoiceStr    *string
	ChatToolChoiceStruct *ChatToolChoiceStruct
}
type ChatAssistantMessageToolCallFunction struct {
	Name      *string `json:"name,omitempty"`
	Arguments string  `json:"arguments"`
}
type VideoResult struct {
	URL             string   `json:"url"`
	ThumbnailURL    *string  `json:"thumbnail_url,omitempty"`
	ThumbnailWidth  *int     `json:"thumbnail_width,omitempty"`
	ThumbnailHeight *int     `json:"thumbnail_height,omitempty"`
	Duration        *float64 `json:"duration,omitempty"`
}
type ChatToolChoiceType string
type ChatToolChoiceFunction struct {
	Name string `json:"name"`
}
type ChatStreamResponseChoice struct {
	Delta *ChatStreamResponseChoiceDelta `json:"delta,omitempty"`
}
type ToolFunctionParameters struct {
	Type                 string                      `json:"type"`
	Description          *string                     `json:"description,omitempty"`
	Properties           *OrderedMap                 `json:"properties"`
	Required             []string                    `json:"required,omitempty"`
	AdditionalProperties *AdditionalPropertiesStruct `json:"additionalProperties,omitempty"`
	Enum                 []string                    `json:"enum,omitempty"`

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

	Title    *string     `json:"title,omitempty"`
	Default  interface{} `json:"default,omitempty"`
	Nullable *bool       `json:"nullable,omitempty"`

	keyOrder JSONKeyOrder `json:"-"`

	explicitEmptyObject bool `json:"-"`
}
type ChatMCPServer struct {
	Type               string  `json:"type"`
	URL                string  `json:"url"`
	Name               string  `json:"name"`
	AuthorizationToken *string `json:"authorization_token,omitempty"`
}
type BifrostCost struct {
	InputCost             float64                `json:"input_cost,omitempty"`
	InputCostDetails      *InputCostDetails      `json:"input_cost_details,omitempty"`
	OutputCost            float64                `json:"output_cost,omitempty"`
	OutputCostDetails     *OutputCostDetails     `json:"output_cost_details,omitempty"`
	AdditionalCost        float64                `json:"additional_cost,omitempty"`
	AdditionalCostDetails *AdditionalCostDetails `json:"additional_cost_details,omitempty"`
	TotalCost             float64                `json:"total_cost,omitempty"`
}
type ChatContentBlockType string
type ChatAssistantMessageAnnotation struct {
	Type        string                                 `json:"type"`
	URLCitation ChatAssistantMessageAnnotationCitation `json:"url_citation"`
}
type ChatMessage struct {
	Name    *string             `json:"name,omitempty"`
	Role    ChatMessageRole     `json:"role,omitempty"`
	Content *ChatMessageContent `json:"content"`

	*ChatToolMessage
	*ChatAssistantMessage
}
type CacheControl struct {
	Type  CacheControlType `json:"type"`
	TTL   *string          `json:"ttl,omitempty"`
	Scope *string          `json:"scope,omitempty"`
}
type BifrostLogProbs struct {
	Content []ContentLogProb `json:"content,omitempty"`
	Refusal []LogProb        `json:"refusal,omitempty"`

	*TextCompletionLogProb
}
type BifrostReasoningDetailsType string
type InputCostDetails struct {
	TextCost        float64 `json:"text_cost,omitempty"`
	AudioCost       float64 `json:"audio_cost,omitempty"`
	ImageCost       float64 `json:"image_cost,omitempty"`
	CachedReadCost  float64 `json:"cached_read_cost,omitempty"`
	CachedWriteCost float64 `json:"cached_write_cost,omitempty"`

	RequestCost float64 `json:"request_cost,omitempty"`
}
type TextCompletionResponseChoice struct {
	Text *string `json:"text,omitempty"`
}
type ChatMCPToolsetConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	DeferLoading *bool `json:"defer_loading,omitempty"`
}
type ChatToolMessage struct {
	ToolCallID *string `json:"tool_call_id,omitempty"`

	IsError *bool `json:"is_error,omitempty"`

	mcpResult        *mcp.CallToolResult
	mcpResultIsText  bool
	mcpResultText    string
	mcpResultContent []string
}
type ChatAssistantMessageToolCall struct {
	Index        uint16                               `json:"index"`
	Type         *string                              `json:"type,omitempty"`
	ID           *string                              `json:"id,omitempty"`
	Function     ChatAssistantMessageToolCallFunction `json:"function"`
	ExtraContent json.RawMessage                      `json:"extra_content,omitempty"`
	CacheControl *CacheControl                        `json:"cache_control,omitempty"`
}
type ChatContainer struct {
	ContainerStr    *string
	ContainerObject *ChatContainerObject
}
type ChatMessageContent struct {
	ContentStr    *string
	ContentBlocks []ChatContentBlock
}
type SearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Date        *string `json:"date,omitempty"`
	LastUpdated *string `json:"last_updated,omitempty"`
	Snippet     *string `json:"snippet,omitempty"`
	Source      *string `json:"source,omitempty"`
}
type ChatInputAudio struct {
	Data   string  `json:"data"`
	Format *string `json:"format,omitempty"`
}
type ChatToolCustomFormat struct {
	Type    string                       `json:"type"`
	Grammar *ChatToolCustomGrammarFormat `json:"grammar,omitempty"`
}
type ChatNonStreamResponseChoice struct {
	Message    *ChatMessage `json:"message"`
	StopString *string      `json:"stop,omitempty"`
}
type ChatInputFile struct {
	FileData *string `json:"file_data,omitempty"`
	FileURL  *string `json:"file_url,omitempty"`
	FileID   *string `json:"file_id,omitempty"`
	Filename *string `json:"filename,omitempty"`
	FileType *string `json:"file_type,omitempty"`
}
type ChatWebSearchOptionsUserLocationApproximate struct {
	City     *string `json:"city,omitempty"`
	Country  *string `json:"country,omitempty"`
	Region   *string `json:"region,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}
type ChatToolUserLocation struct {
	Type     *string `json:"type,omitempty"`
	City     *string `json:"city,omitempty"`
	Region   *string `json:"region,omitempty"`
	Country  *string `json:"country,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}
type ChatAudioParameters struct {
	Format string `json:"format,omitempty"`
	Voice  string `json:"voice,omitempty"`
}
type CacheControlType string
type BifrostFinishReason string
type CachePoint struct {
	Type string  `json:"type"`
	TTL  *string `json:"ttl,omitempty"`
}
type ChatToolType string
type ContentLogProb struct {
	Bytes       []int     `json:"bytes"`
	LogProb     float64   `json:"logprob"`
	Token       string    `json:"token"`
	TopLogProbs []LogProb `json:"top_logprobs"`
}
type ChatTool struct {
	Type         ChatToolType        `json:"type"`
	Function     *ChatToolFunction   `json:"function,omitempty"`
	Custom       *ChatToolCustom     `json:"custom,omitempty"`
	CacheControl *CacheControl       `json:"cache_control,omitempty"`
	Annotations  *MCPToolAnnotations `json:"-"`
	MCP          *MCPToolMetadata    `json:"-"`

	DeferLoading        *bool                  `json:"defer_loading,omitempty"`
	AllowedCallers      []string               `json:"allowed_callers,omitempty"`
	InputExamples       []ChatToolInputExample `json:"input_examples,omitempty"`
	EagerInputStreaming *bool                  `json:"eager_input_streaming,omitempty"`

	Name string `json:"name,omitempty"`

	MaxUses        *int                  `json:"max_uses,omitempty"`
	AllowedDomains []string              `json:"allowed_domains,omitempty"`
	BlockedDomains []string              `json:"blocked_domains,omitempty"`
	UserLocation   *ChatToolUserLocation `json:"user_location,omitempty"`

	MaxContentTokens *int                     `json:"max_content_tokens,omitempty"`
	Citations        *ChatToolCitationsConfig `json:"citations,omitempty"`
	UseCache         *bool                    `json:"use_cache,omitempty"`

	DisplayWidthPx  *int  `json:"display_width_px,omitempty"`
	DisplayHeightPx *int  `json:"display_height_px,omitempty"`
	DisplayNumber   *int  `json:"display_number,omitempty"`
	EnableZoom      *bool `json:"enable_zoom,omitempty"`

	MaxCharacters *int `json:"max_characters,omitempty"`

	MCPServerName string                           `json:"mcp_server_name,omitempty"`
	DefaultConfig *ChatMCPToolsetConfig            `json:"default_config,omitempty"`
	Configs       map[string]*ChatMCPToolsetConfig `json:"configs,omitempty"`

	serialized []byte
}
type AdditionalCostDetails struct {
	GuardrailCost     float64 `json:"guardrail_cost,omitempty"`
	MCPCost           float64 `json:"mcp_cost,omitempty"`
	SemanticCacheCost float64 `json:"semantic_cache_cost,omitempty"`

	RoutingCost float64 `json:"routing_cost,omitempty"`
}
type ChatInputImage struct {
	URL    string  `json:"url,omitempty"`
	FileID *string `json:"file_id,omitempty"`
	Detail *string `json:"detail,omitempty"`
}
type LogProb struct {
	Bytes   []int   `json:"bytes,omitempty"`
	LogProb float64 `json:"logprob"`
	Token   string  `json:"token"`
}
type ChatParameters struct {
	Audio                *ChatAudioParameters  `json:"audio,omitempty"`
	FrequencyPenalty     *float64              `json:"frequency_penalty,omitempty"`
	LogitBias            *map[string]float64   `json:"logit_bias,omitempty"`
	LogProbs             *bool                 `json:"logprobs,omitempty"`
	MaxCompletionTokens  *int                  `json:"max_completion_tokens,omitempty"`
	Metadata             *map[string]any       `json:"metadata,omitempty"`
	Modalities           []string              `json:"modalities,omitempty"`
	N                    *int                  `json:"n,omitempty"`
	ParallelToolCalls    *bool                 `json:"parallel_tool_calls,omitempty"`
	Prediction           *ChatPrediction       `json:"prediction,omitempty"`
	PresencePenalty      *float64              `json:"presence_penalty,omitempty"`
	PromptCacheKey       *string               `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention *string               `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *PromptCacheOptions   `json:"prompt_cache_options,omitempty"`
	Reasoning            *ChatReasoning        `json:"reasoning,omitempty"`
	ResponseFormat       *interface{}          `json:"response_format,omitempty"`
	SafetyIdentifier     *string               `json:"safety_identifier,omitempty"`
	Seed                 *int                  `json:"seed,omitempty"`
	ServiceTier          *BifrostServiceTier   `json:"service_tier,omitempty"`
	StreamOptions        *ChatStreamOptions    `json:"stream_options,omitempty"`
	Stop                 []string              `json:"stop,omitempty"`
	Store                *bool                 `json:"store,omitempty"`
	Temperature          *float64              `json:"temperature,omitempty"`
	TopLogProbs          *int                  `json:"top_logprobs,omitempty"`
	TopP                 *float64              `json:"top_p,omitempty"`
	ToolChoice           *ChatToolChoice       `json:"tool_choice,omitempty"`
	Tools                []ChatTool            `json:"tools,omitempty"`
	User                 *string               `json:"user,omitempty"`
	Verbosity            *string               `json:"verbosity,omitempty"`
	WebSearchOptions     *ChatWebSearchOptions `json:"web_search_options,omitempty"`

	TopK              *int            `json:"top_k,omitempty"`
	Speed             *string         `json:"speed,omitempty"`
	InferenceGeo      *string         `json:"inference_geo,omitempty"`
	MCPServers        []ChatMCPServer `json:"mcp_servers,omitempty"`
	Container         *ChatContainer  `json:"container,omitempty"`
	CacheControl      *CacheControl   `json:"cache_control,omitempty"`
	TaskBudget        *ChatTaskBudget `json:"task_budget,omitempty"`
	ContextManagement json.RawMessage `json:"context_management,omitempty"`

	IncludeServerSideToolInvocations *bool `json:"include_server_side_tool_invocations,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type OutputCostDetails struct {
	TextCost          float64 `json:"text_cost,omitempty"`
	AudioCost         float64 `json:"audio_cost,omitempty"`
	ImageCost         float64 `json:"image_cost,omitempty"`
	ReasoningCost     float64 `json:"reasoning_cost,omitempty"`
	CitationCost      float64 `json:"citation_cost,omitempty"`
	SearchQueriesCost float64 `json:"search_queries_cost,omitempty"`
}
type WebSearchToolUsage struct {
	NumRequests int `json:"num_requests"`
}
type ChatPromptTokensDetails struct {
	TextTokens  int `json:"text_tokens,omitempty"`
	AudioTokens int `json:"audio_tokens,omitempty"`
	ImageTokens int `json:"image_tokens,omitempty"`

	CachedReadTokens        int                          `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens       int                          `json:"cached_write_tokens,omitempty"`
	CachedWriteTokenDetails *ChatCachedWriteTokenDetails `json:"cached_write_token_details,omitempty"`

	StandardSpeechAudioTokens int `json:"standard_speech_audio_tokens,omitempty"`
	CustomSpeechAudioTokens   int `json:"custom_speech_audio_tokens,omitempty"`
}
type ToolUsage struct {
	WebSearch *WebSearchToolUsage `json:"web_search,omitempty"`
}
type ChatAssistantMessageImage struct {
	Type     string         `json:"type"`
	ImageURL ChatInputImage `json:"image_url"`
}
type BifrostResponseChoice struct {
	Index                int              `json:"index"`
	FinishReason         *string          `json:"finish_reason"`
	LogProbs             *BifrostLogProbs `json:"logprobs"`
	ContentFilterResults json.RawMessage  `json:"content_filter_results,omitempty"`

	*TextCompletionResponseChoice
	*ChatNonStreamResponseChoice
	*ChatStreamResponseChoice
}
type ChatWebSearchOptions struct {
	SearchContextSize *string                           `json:"search_context_size,omitempty"`
	UserLocation      *ChatWebSearchOptionsUserLocation `json:"user_location,omitempty"`
	Filters           *ChatWebSearchOptionsFilters      `json:"filters,omitempty"`
}
type ChatContentBlock struct {
	Type           ChatContentBlockType `json:"type"`
	Text           *string              `json:"text,omitempty"`
	Refusal        *string              `json:"refusal,omitempty"`
	ImageURLStruct *ChatInputImage      `json:"image_url,omitempty"`
	InputAudio     *ChatInputAudio      `json:"input_audio,omitempty"`
	File           *ChatInputFile       `json:"file,omitempty"`

	CacheControl *CacheControl `json:"cache_control,omitempty"`
	Citations    *Citations    `json:"citations,omitempty"`

	PromptCacheBreakpoint *PromptCacheBreakpoint `json:"prompt_cache_breakpoint,omitempty"`

	CachePoint *CachePoint `json:"cachePoint,omitempty"`

	GuardContent *GuardContent `json:"guard_content,omitempty"`
}
type ChatMessageRole string
type ChatToolChoiceStruct struct {
	Type         ChatToolChoiceType          `json:"type"`
	Function     *ChatToolChoiceFunction     `json:"function,omitempty"`
	Custom       *ChatToolChoiceCustom       `json:"custom,omitempty"`
	AllowedTools *ChatToolChoiceAllowedTools `json:"allowed_tools,omitempty"`
}
type ChatContainerSkill struct {
	SkillID string  `json:"skill_id"`
	Type    string  `json:"type"`
	Version *string `json:"version,omitempty"`
}
type legacyBifrostCost struct {
	InputTokensCost     *float64 `json:"input_tokens_cost"`
	OutputTokensCost    *float64 `json:"output_tokens_cost"`
	CacheReadTokensCost *float64 `json:"cache_read_tokens_cost"`
	ReasoningTokensCost *float64 `json:"reasoning_tokens_cost"`
	CitationTokensCost  *float64 `json:"citation_tokens_cost"`
	SearchQueriesCost   *float64 `json:"search_queries_cost"`
	RequestCost         *float64 `json:"request_cost"`
}
type AdditionalPropertiesStruct struct {
	AdditionalPropertiesBool *bool
	AdditionalPropertiesMap  *OrderedMap
}
type ChatToolCitationsConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}
type ChatAssistantMessage struct {
	Refusal          *string                          `json:"refusal"`
	Audio            *ChatAudioMessageAudio           `json:"audio,omitempty"`
	Images           []ChatAssistantMessageImage      `json:"images,omitempty"`
	Reasoning        *string                          `json:"reasoning,omitempty"`
	ReasoningDetails []ChatReasoningDetails           `json:"reasoning_details,omitempty"`
	Annotations      []ChatAssistantMessageAnnotation `json:"annotations,omitempty"`
	ToolCalls        []ChatAssistantMessageToolCall   `json:"tool_calls,omitempty"`
}
type ChatCachedWriteTokenDetails struct {
	CachedWriteTokens5m int `json:"cached_write_tokens_5m"`
	CachedWriteTokens1h int `json:"cached_write_tokens_1h"`
}
type ChatToolChoiceAllowedToolsTool struct {
	Type     string                 `json:"type"`
	Function ChatToolChoiceFunction `json:"function,omitempty"`
}

func (cr *BifrostChatRequest) GetRawRequestBody() []byte {
	return cr.RawRequestBody
}

func (cr *BifrostChatRequest) GetExtraParams() map[string]interface{} {
	if cr.Params == nil {
		return make(map[string]interface{}, 0)
	}
	return cr.Params.ExtraParams
}

func (cr *BifrostChatResponse) BackfillParams(request *BifrostChatRequest) {
	if cr == nil || request == nil {
		return
	}
	if cr.Model == "" {
		cr.Model = request.Model
	}
	if cr.Object == "" {
		cr.Object = "chat.completion"
	}
	if cr.Created == 0 {
		cr.Created = int(time.Now().Unix())
	}
}

func (cp *ChatParameters) UnmarshalJSON(data []byte) error {

	type Alias ChatParameters

	var aux struct {
		*Alias
		ReasoningEffort    *string `json:"reasoning_effort"`
		ReasoningMaxTokens *int    `json:"reasoning_max_tokens"`
		ReasoningDisplay   *string `json:"reasoning_display"`
	}

	aux.Alias = (*Alias)(cp)

	if err := Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.ReasoningEffort != nil && aux.Reasoning != nil && aux.Reasoning.Effort != nil &&
		*aux.ReasoningEffort != *aux.Reasoning.Effort {
		return fmt.Errorf("reasoning_effort (%q) conflicts with reasoning.effort (%q)", *aux.ReasoningEffort, *aux.Reasoning.Effort)
	}
	if aux.ReasoningMaxTokens != nil && aux.Reasoning != nil && aux.Reasoning.MaxTokens != nil &&
		*aux.ReasoningMaxTokens != *aux.Reasoning.MaxTokens {
		return fmt.Errorf("reasoning_max_tokens (%d) conflicts with reasoning.max_tokens (%d)", *aux.ReasoningMaxTokens, *aux.Reasoning.MaxTokens)
	}
	if aux.ReasoningDisplay != nil && aux.Reasoning != nil && aux.Reasoning.Display != nil &&
		*aux.ReasoningDisplay != *aux.Reasoning.Display {
		return fmt.Errorf("reasoning_display (%q) conflicts with reasoning.display (%q)", *aux.ReasoningDisplay, *aux.Reasoning.Display)
	}

	if aux.ReasoningEffort != nil || aux.ReasoningMaxTokens != nil || aux.ReasoningDisplay != nil {
		if cp.Reasoning == nil {
			cp.Reasoning = &ChatReasoning{}
		}

		if aux.ReasoningEffort != nil {
			cp.Reasoning.Effort = aux.ReasoningEffort
		}
		if aux.ReasoningMaxTokens != nil {
			cp.Reasoning.MaxTokens = aux.ReasoningMaxTokens
		}
		if aux.ReasoningDisplay != nil {
			cp.Reasoning.Display = aux.ReasoningDisplay
		}
	}

	if rf := gjson.GetBytes(data, "response_format"); rf.IsObject() {
		var value interface{} = json.RawMessage(rf.Raw)
		cp.ResponseFormat = &value
	}

	return nil
}

const (
	ChatToolTypeFunction ChatToolType = "function"
	ChatToolTypeCustom   ChatToolType = "custom"
)

func (t *ChatTool) normalizeShape() {
	switch t.Type {
	case ChatToolTypeFunction:
		t.Custom = nil
		t.Name = ""
		t.clearServerToolVariantFields()
	case ChatToolTypeCustom:
		t.Function = nil
		t.clearServerToolVariantFields()
	default:
		t.Function = nil
		t.Custom = nil
	}
}

func (t *ChatTool) clearServerToolVariantFields() {
	t.MaxUses = nil
	t.AllowedDomains = nil
	t.BlockedDomains = nil
	t.UserLocation = nil
	t.MaxContentTokens = nil
	t.Citations = nil
	t.UseCache = nil
	t.DisplayWidthPx = nil
	t.DisplayHeightPx = nil
	t.DisplayNumber = nil
	t.EnableZoom = nil
	t.MaxCharacters = nil
	t.MCPServerName = ""
	t.DefaultConfig = nil
	t.Configs = nil
}

func (t ChatTool) MarshalJSON() ([]byte, error) {
	if len(t.serialized) > 0 {
		return t.serialized, nil
	}
	normalized := t
	normalized.normalizeShape()
	type Alias ChatTool
	return MarshalSorted((*Alias)(&normalized))
}

func (t *ChatTool) InvalidateSerialized() {
	t.serialized = nil
}

func (t *ChatTool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*t = ChatTool{}
		return nil
	}

	type Alias ChatTool
	var temp Alias
	if err := Unmarshal(data, &temp); err != nil {
		return err
	}
	*t = ChatTool(temp)
	t.normalizeShape()
	return nil
}

func (t ToolFunctionParameters) MarshalJSON() ([]byte, error) {
	if t.explicitEmptyObject && !t.hasDefinedSchemaFields() {
		return []byte("{}"), nil
	}
	if t.Properties == nil {

		t.Properties = &OrderedMap{values: make(map[string]interface{})}
	}
	type Alias ToolFunctionParameters
	data, err := MarshalSorted(Alias(t))
	if err != nil {
		return nil, err
	}
	return t.keyOrder.Apply(data)
}

func (t *ToolFunctionParameters) UnmarshalJSON(data []byte) error {

	var jsonStr string
	if err := Unmarshal(data, &jsonStr); err == nil {
		data = []byte(jsonStr)
	}
	type Alias ToolFunctionParameters
	var temp Alias
	if err := Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal ToolFunctionParameters: %w", err)
	}
	*t = ToolFunctionParameters(temp)

	if t.AdditionalProperties != nil &&
		t.AdditionalProperties.AdditionalPropertiesBool == nil &&
		t.AdditionalProperties.AdditionalPropertiesMap == nil {
		t.AdditionalProperties = nil
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' {
		inner := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
		t.explicitEmptyObject = len(inner) == 0
	} else {
		t.explicitEmptyObject = false
	}
	t.keyOrder.Capture(data)
	return nil
}

func (t *ToolFunctionParameters) Normalized() *ToolFunctionParameters {
	if t == nil {
		return nil
	}
	out := *t
	out.keyOrder = JSONKeyOrder{}

	out.Properties = t.Properties.preserveKeysWithPropertyAwareness()
	out.Defs = t.Defs.SortedCopyPreservingProperties()
	out.Definitions = t.Definitions.SortedCopyPreservingProperties()
	out.Items = t.Items.SortedCopyPreservingProperties()
	if len(t.AnyOf) > 0 {
		out.AnyOf = make([]OrderedMap, len(t.AnyOf))
		for i := range t.AnyOf {
			if cp := t.AnyOf[i].SortedCopyPreservingProperties(); cp != nil {
				out.AnyOf[i] = *cp
			}
		}
	}
	if len(t.OneOf) > 0 {
		out.OneOf = make([]OrderedMap, len(t.OneOf))
		for i := range t.OneOf {
			if cp := t.OneOf[i].SortedCopyPreservingProperties(); cp != nil {
				out.OneOf[i] = *cp
			}
		}
	}
	if len(t.AllOf) > 0 {
		out.AllOf = make([]OrderedMap, len(t.AllOf))
		for i := range t.AllOf {
			if cp := t.AllOf[i].SortedCopyPreservingProperties(); cp != nil {
				out.AllOf[i] = *cp
			}
		}
	}
	if t.AdditionalProperties != nil && t.AdditionalProperties.AdditionalPropertiesMap != nil {
		out.AdditionalProperties = &AdditionalPropertiesStruct{
			AdditionalPropertiesBool: t.AdditionalProperties.AdditionalPropertiesBool,
			AdditionalPropertiesMap:  t.AdditionalProperties.AdditionalPropertiesMap.SortedCopyPreservingProperties(),
		}
	}
	switch v := t.Default.(type) {
	case *OrderedMap:
		out.Default = v.SortedCopy()
	case map[string]interface{}:
		out.Default = OrderedMapFromMap(v).SortedCopy()
	case []interface{}:
		out.Default = sortedCopySlice(v)
	}
	return &out
}

func (t *ToolFunctionParameters) hasDefinedSchemaFields() bool {
	if t == nil {
		return false
	}
	if t.Type != "" || t.Description != nil || len(t.Required) > 0 || t.AdditionalProperties != nil || len(t.Enum) > 0 {
		return true
	}
	if t.Properties != nil || t.Defs != nil || t.Definitions != nil || t.Ref != nil {
		return true
	}
	if t.Items != nil || t.MinItems != nil || t.MaxItems != nil {
		return true
	}
	if len(t.AnyOf) > 0 || len(t.OneOf) > 0 || len(t.AllOf) > 0 {
		return true
	}
	if t.Format != nil || t.Pattern != nil || t.MinLength != nil || t.MaxLength != nil {
		return true
	}
	if t.Minimum != nil || t.Maximum != nil {
		return true
	}
	return t.Title != nil || t.Default != nil || t.Nullable != nil
}

func (a AdditionalPropertiesStruct) MarshalJSON() ([]byte, error) {

	if a.AdditionalPropertiesBool != nil && a.AdditionalPropertiesMap != nil {
		return nil, fmt.Errorf("both AdditionalPropertiesBool and AdditionalPropertiesMap are set; only one should be non-nil")
	}

	if a.AdditionalPropertiesBool != nil {
		return MarshalSorted(*a.AdditionalPropertiesBool)
	}

	if a.AdditionalPropertiesMap != nil {
		return MarshalSorted(a.AdditionalPropertiesMap)
	}

	return nil, fmt.Errorf("additionalProperties cannot be null; omit the field instead")
}

func (a *AdditionalPropertiesStruct) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		a.AdditionalPropertiesBool = nil
		a.AdditionalPropertiesMap = nil
		return nil
	}

	var boolValue bool
	if err := Unmarshal(data, &boolValue); err == nil {
		a.AdditionalPropertiesMap = nil
		a.AdditionalPropertiesBool = &boolValue
		return nil
	}

	var mapValue OrderedMap
	if err := Unmarshal(data, &mapValue); err == nil {
		a.AdditionalPropertiesBool = nil
		a.AdditionalPropertiesMap = &mapValue
		return nil
	}

	return fmt.Errorf("additionalProperties must be either a boolean or an object")
}

const (
	ChatToolChoiceTypeNone     ChatToolChoiceType = "none"
	ChatToolChoiceTypeAuto     ChatToolChoiceType = "auto"
	ChatToolChoiceTypeAny      ChatToolChoiceType = "any"
	ChatToolChoiceTypeRequired ChatToolChoiceType = "required"

	ChatToolChoiceTypeFunction ChatToolChoiceType = "function"

	ChatToolChoiceTypeAllowedTools ChatToolChoiceType = "allowed_tools"

	ChatToolChoiceTypeCustom ChatToolChoiceType = "custom"
)

func (s ChatToolChoiceStruct) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	typeBytes, err := MarshalSorted(string(s.Type))
	if err != nil {
		return nil, err
	}
	buf.WriteString(`"type":`)
	buf.Write(typeBytes)

	switch s.Type {
	case ChatToolChoiceTypeFunction:
		if s.Function != nil {
			funcBytes, err := MarshalSorted(s.Function)
			if err != nil {
				return nil, err
			}
			buf.WriteString(`,"function":`)
			buf.Write(funcBytes)
		}
	case ChatToolChoiceTypeCustom:
		if s.Custom != nil {
			customBytes, err := MarshalSorted(s.Custom)
			if err != nil {
				return nil, err
			}
			buf.WriteString(`,"custom":`)
			buf.Write(customBytes)
		}
	case ChatToolChoiceTypeAllowedTools:
		if s.AllowedTools != nil {
			allowedBytes, err := MarshalSorted(s.AllowedTools)
			if err != nil {
				return nil, err
			}
			buf.WriteString(`,"allowed_tools":`)
			buf.Write(allowedBytes)
		}
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (s *ChatToolChoiceStruct) UnmarshalJSON(data []byte) error {
	type Alias ChatToolChoiceStruct
	var temp Alias
	if err := Unmarshal(data, &temp); err != nil {
		return err
	}
	*s = ChatToolChoiceStruct(temp)

	switch s.Type {
	case ChatToolChoiceTypeFunction:
		s.Custom = nil
		s.AllowedTools = nil
	case ChatToolChoiceTypeCustom:
		s.Function = nil
		s.AllowedTools = nil
	case ChatToolChoiceTypeAllowedTools:
		s.Function = nil
		s.Custom = nil
	}

	return nil
}

func (ctc *ChatToolChoice) IsForced() bool {
	if ctc == nil {
		return false
	}
	if ctc.ChatToolChoiceStr != nil {
		switch ChatToolChoiceType(*ctc.ChatToolChoiceStr) {
		case ChatToolChoiceTypeNone, ChatToolChoiceTypeAuto:
			return false
		default:
			return true
		}
	}
	if ctc.ChatToolChoiceStruct != nil {
		switch ctc.ChatToolChoiceStruct.Type {
		case ChatToolChoiceTypeNone, ChatToolChoiceTypeAuto:
			return false
		case ChatToolChoiceTypeAllowedTools:

			return ctc.ChatToolChoiceStruct.AllowedTools != nil &&
				ctc.ChatToolChoiceStruct.AllowedTools.Mode == string(ChatToolChoiceTypeRequired)
		default:
			return true
		}
	}
	return false
}

func (ctc ChatToolChoice) MarshalJSON() ([]byte, error) {

	if ctc.ChatToolChoiceStr != nil && ctc.ChatToolChoiceStruct != nil {
		return nil, fmt.Errorf("both ChatToolChoiceStr, ChatToolChoiceStruct are set; only one should be non-nil")
	}

	if ctc.ChatToolChoiceStr != nil {
		return MarshalSorted(ctc.ChatToolChoiceStr)
	}
	if ctc.ChatToolChoiceStruct != nil {
		return MarshalSorted(ctc.ChatToolChoiceStruct)
	}

	return MarshalSorted(nil)
}

func (ctc *ChatToolChoice) UnmarshalJSON(data []byte) error {

	var toolChoiceStr string
	if err := Unmarshal(data, &toolChoiceStr); err == nil {
		ctc.ChatToolChoiceStr = &toolChoiceStr
		ctc.ChatToolChoiceStruct = nil
		return nil
	}

	var chatToolChoice ChatToolChoiceStruct
	if err := Unmarshal(data, &chatToolChoice); err == nil {
		ctc.ChatToolChoiceStr = nil
		ctc.ChatToolChoiceStruct = &chatToolChoice
		return nil
	}

	return fmt.Errorf("tool_choice field is neither a string nor a ChatToolChoiceStruct object")
}

const (
	ChatMessageRoleAssistant ChatMessageRole = "assistant"
	ChatMessageRoleUser      ChatMessageRole = "user"
	ChatMessageRoleSystem    ChatMessageRole = "system"
	ChatMessageRoleTool      ChatMessageRole = "tool"
	ChatMessageRoleDeveloper ChatMessageRole = "developer"
)

func (cm ChatMessage) MarshalJSON() ([]byte, error) {

	base, err := Marshal(struct {
		Name    *string             `json:"name,omitempty"`
		Role    ChatMessageRole     `json:"role,omitempty"`
		Content *ChatMessageContent `json:"content"`
	}{Name: cm.Name, Role: cm.Role, Content: cm.Content})
	if err != nil {
		return nil, err
	}
	fragments := [][]byte{base}

	if cm.ChatToolMessage != nil {
		encoded, err := Marshal(cm.ChatToolMessage)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, encoded)
	}
	if cm.ChatAssistantMessage != nil {
		encoded, err := Marshal(cm.ChatAssistantMessage)
		if err != nil {
			return nil, err
		}
		fragments = append(fragments, encoded)
	}

	return spliceJSONObjects(fragments), nil
}

func spliceJSONObjects(fragments [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, fragment := range fragments {
		trimmed := bytes.TrimSpace(fragment)
		if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
			continue
		}
		body := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
		if len(body) == 0 {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		buf.Write(body)
		first = false
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func (cm *ChatMessage) UnmarshalJSON(data []byte) error {

	type baseFields struct {
		Name    *string             `json:"name,omitempty"`
		Role    ChatMessageRole     `json:"role,omitempty"`
		Content *ChatMessageContent `json:"content,omitempty"`
	}
	var base baseFields
	if err := Unmarshal(data, &base); err != nil {
		return err
	}
	cm.Name = base.Name
	cm.Role = base.Role
	cm.Content = base.Content

	type toolMsgAlias ChatToolMessage
	var toolMsg toolMsgAlias
	if err := Unmarshal(data, &toolMsg); err != nil {
		return err
	}

	if toolMsg.ToolCallID != nil || toolMsg.IsError != nil {
		cm.ChatToolMessage = (*ChatToolMessage)(&toolMsg)
	}

	var assistantMsg ChatAssistantMessage
	if err := Unmarshal(data, &assistantMsg); err != nil {
		return err
	}

	if assistantMsg.Refusal != nil || assistantMsg.Reasoning != nil ||
		len(assistantMsg.ReasoningDetails) > 0 || len(assistantMsg.Annotations) > 0 ||
		len(assistantMsg.ToolCalls) > 0 || assistantMsg.Audio != nil || len(assistantMsg.Images) > 0 {
		cm.ChatAssistantMessage = &assistantMsg
	}

	return nil
}

func (mc ChatMessageContent) MarshalJSON() ([]byte, error) {

	if mc.ContentStr != nil && mc.ContentBlocks != nil {
		return nil, fmt.Errorf("both Content string and Content blocks are set; only one should be non-nil")
	}

	if mc.ContentStr != nil {
		return MarshalSorted(*mc.ContentStr)
	}
	if mc.ContentBlocks != nil {
		return MarshalSorted(mc.ContentBlocks)
	}

	return MarshalSorted(nil)
}

func (mc *ChatMessageContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		mc.ContentStr = nil
		mc.ContentBlocks = nil
		return nil
	}

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		mc.ContentStr = &stringContent
		mc.ContentBlocks = nil
		return nil
	}

	var arrayContent []ChatContentBlock
	if err := Unmarshal(data, &arrayContent); err == nil {
		mc.ContentBlocks = arrayContent
		mc.ContentStr = nil
		return nil
	}

	return fmt.Errorf("content field is neither a string nor an array of Content blocks")
}

const (
	ChatContentBlockTypeText       ChatContentBlockType = "text"
	ChatContentBlockTypeImage      ChatContentBlockType = "image_url"
	ChatContentBlockTypeInputAudio ChatContentBlockType = "input_audio"
	ChatContentBlockTypeFile       ChatContentBlockType = "file"
	ChatContentBlockTypeRefusal    ChatContentBlockType = "refusal"
)

func (c *ChatContentBlock) UnmarshalJSON(data []byte) error {

	type alias ChatContentBlock

	if blockType := gjson.GetBytes(data, "type"); blockType.Type == gjson.String && blockType.String() == "document" {
		rewritten, err := rewriteDocumentBlock(data)
		if err != nil {
			return err
		}
		data = rewritten
	}

	var a alias
	if err := Unmarshal(data, &a); err != nil {
		return err
	}
	*c = ChatContentBlock(a)
	return nil
}

func rewriteDocumentBlock(data []byte) ([]byte, error) {
	srcType := gjson.GetBytes(data, "source.type").String()

	out, err := sjson.SetBytes(data, "type", "file")
	if err != nil {
		return nil, fmt.Errorf("document rewrite: set type: %w", err)
	}
	out, err = sjson.DeleteBytes(out, "source")
	if err != nil {
		return nil, fmt.Errorf("document rewrite: drop source: %w", err)
	}

	switch srcType {
	case "base64", "text":
		mediaType := gjson.GetBytes(data, "source.media_type").String()
		dataField := gjson.GetBytes(data, "source.data").String()
		if dataField == "" {
			return nil, fmt.Errorf("document rewrite: source.data is required for source.type=%q", srcType)
		}
		if out, err = sjson.SetBytes(out, "file.file_data", dataField); err != nil {
			return nil, fmt.Errorf("document rewrite: set file_data: %w", err)
		}
		if mediaType != "" {
			if out, err = sjson.SetBytes(out, "file.file_type", mediaType); err != nil {
				return nil, fmt.Errorf("document rewrite: set file_type: %w", err)
			}
		}
	case "url":
		urlField := gjson.GetBytes(data, "source.url").String()
		if urlField == "" {
			return nil, fmt.Errorf("document rewrite: source.url is required for source.type=url")
		}
		if out, err = sjson.SetBytes(out, "file.file_url", urlField); err != nil {
			return nil, fmt.Errorf("document rewrite: set file_url: %w", err)
		}
	case "file":
		fileID := gjson.GetBytes(data, "source.file_id").String()
		if fileID == "" {
			return nil, fmt.Errorf("document rewrite: source.file_id is required for source.type=file")
		}
		if out, err = sjson.SetBytes(out, "file.file_id", fileID); err != nil {
			return nil, fmt.Errorf("document rewrite: set file_id: %w", err)
		}
	case "content":
		return nil, fmt.Errorf("document rewrite: source.type=content (Anthropic inline content array) is not supported; use base64/text/url/file")
	default:
		return nil, fmt.Errorf("document rewrite: unsupported source.type %q", srcType)
	}

	if name := gjson.GetBytes(data, "title"); name.Exists() {
		if out, err = sjson.SetBytes(out, "file.filename", name.String()); err != nil {
			return nil, fmt.Errorf("document rewrite: set filename: %w", err)
		}
	}

	return out, nil
}

const (
	CacheControlTypeEphemeral CacheControlType = "ephemeral"
)

func (c ChatContainer) MarshalJSON() ([]byte, error) {
	if c.ContainerStr != nil && c.ContainerObject != nil {
		return nil, fmt.Errorf("both ContainerStr and ContainerObject are set; only one should be non-nil")
	}
	if c.ContainerStr != nil {
		return MarshalSorted(*c.ContainerStr)
	}
	if c.ContainerObject != nil {
		return MarshalSorted(c.ContainerObject)
	}
	return MarshalSorted(nil)
}

func (c *ChatContainer) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		c.ContainerStr = nil
		c.ContainerObject = nil
		return nil
	}

	var s string
	if err := Unmarshal(data, &s); err == nil {
		c.ContainerStr = &s
		c.ContainerObject = nil
		return nil
	}
	var obj ChatContainerObject
	if err := Unmarshal(data, &obj); err == nil {
		c.ContainerStr = nil
		c.ContainerObject = &obj
		return nil
	}
	return fmt.Errorf("container field is neither a string nor an object")
}

func (cm ChatAssistantMessage) MarshalJSON() ([]byte, error) {
	type Alias ChatAssistantMessage
	if cm.Reasoning == nil {
		return Marshal(Alias(cm))
	}
	return Marshal(struct {
		Alias
		ReasoningContent *string `json:"reasoning_content,omitempty"`
	}{
		Alias:            Alias(cm),
		ReasoningContent: cm.Reasoning,
	})
}

func (cm *ChatAssistantMessage) UnmarshalJSON(data []byte) error {
	if cm == nil {
		return nil
	}

	type Alias ChatAssistantMessage

	var aux struct {
		Alias
		ReasoningContent *string `json:"reasoning_content,omitempty"`
	}

	if err := Unmarshal(data, &aux); err != nil {
		return err
	}

	*cm = ChatAssistantMessage(aux.Alias)

	if aux.ReasoningContent != nil && cm.Reasoning == nil {
		cm.Reasoning = aux.ReasoningContent
	}

	cm.ReasoningDetails = pruneEmptyReasoningDetails(cm.ReasoningDetails)
	if cm.Reasoning != nil && *cm.Reasoning == "" {
		cm.Reasoning = nil
	}

	if cm.Reasoning != nil && len(cm.ReasoningDetails) == 0 {
		text := *cm.Reasoning
		cm.ReasoningDetails = []ChatReasoningDetails{
			{
				Index: 0,
				Type:  BifrostReasoningDetailsTypeText,
				Text:  &text,
			},
		}
	}

	return nil
}

func pruneEmptyReasoningDetails(details []ChatReasoningDetails) []ChatReasoningDetails {
	isEmpty := func(d ChatReasoningDetails) bool {
		return (d.Text == nil || *d.Text == "") && d.Summary == nil && d.Signature == nil && d.Data == nil
	}
	needsPrune := false
	for _, d := range details {
		if isEmpty(d) {
			needsPrune = true
			break
		}
	}
	if !needsPrune {
		return details
	}
	kept := make([]ChatReasoningDetails, 0, len(details))
	for _, d := range details {
		if !isEmpty(d) {
			kept = append(kept, d)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

const (
	BifrostFinishReasonStop      BifrostFinishReason = "stop"
	BifrostFinishReasonLength    BifrostFinishReason = "length"
	BifrostFinishReasonToolCalls BifrostFinishReason = "tool_calls"
)

const (
	BifrostServiceTierAuto        BifrostServiceTier = "auto"
	BifrostServiceTierDefault     BifrostServiceTier = "default"
	BifrostServiceTierFlex        BifrostServiceTier = "flex"
	BifrostServiceTierPriority    BifrostServiceTier = "priority"
	BifrostServiceTierUltrafast   BifrostServiceTier = "ultrafast"
	BifrostServiceTierProvisioned BifrostServiceTier = "provisioned"

	BifrostServiceTierFast BifrostServiceTier = "fast"
)

const (
	BifrostReasoningDetailsTypeSummary       BifrostReasoningDetailsType = "reasoning.summary"
	BifrostReasoningDetailsTypeEncrypted     BifrostReasoningDetailsType = "reasoning.encrypted"
	BifrostReasoningDetailsTypeText          BifrostReasoningDetailsType = "reasoning.text"
	BifrostReasoningDetailsTypeContentBlocks BifrostReasoningDetailsType = "reasoning.content_blocks"
)

func (d ChatStreamResponseChoiceDelta) MarshalJSON() ([]byte, error) {
	type Alias ChatStreamResponseChoiceDelta
	if d.Reasoning == nil {
		return Marshal(Alias(d))
	}
	return Marshal(struct {
		Alias
		ReasoningContent *string `json:"reasoning_content,omitempty"`
	}{
		Alias:            Alias(d),
		ReasoningContent: d.Reasoning,
	})
}

func (d *ChatStreamResponseChoiceDelta) UnmarshalJSON(data []byte) error {

	type Alias ChatStreamResponseChoiceDelta

	var aux struct {
		Alias
		ReasoningContent *string `json:"reasoning_content,omitempty"`
	}

	if err := Unmarshal(data, &aux); err != nil {
		return err
	}

	*d = ChatStreamResponseChoiceDelta(aux.Alias)

	if aux.ReasoningContent != nil && d.Reasoning == nil {
		d.Reasoning = aux.ReasoningContent
	}

	d.ReasoningDetails = pruneEmptyReasoningDetails(d.ReasoningDetails)
	if d.Reasoning != nil && *d.Reasoning == "" {
		d.Reasoning = nil
	}

	if d.Reasoning != nil && len(d.ReasoningDetails) == 0 {
		text := *d.Reasoning
		d.ReasoningDetails = []ChatReasoningDetails{
			{
				Index: 0,
				Type:  BifrostReasoningDetailsTypeText,
				Text:  &text,
			},
		}
	}

	return nil
}

func (u *BifrostLLMUsage) NormalizeReasoningTokens() {
	if u == nil || u.ReasoningTokens == 0 {
		return
	}
	if u.CompletionTokensDetails == nil {
		u.CompletionTokensDetails = &ChatCompletionTokensDetails{}
	}
	if u.CompletionTokensDetails.ReasoningTokens == 0 {
		u.CompletionTokensDetails.ReasoningTokens = u.ReasoningTokens
	}
	u.ReasoningTokens = 0
}

func (d *ChatPromptTokensDetails) UnmarshalJSON(data []byte) error {
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

func (d ChatPromptTokensDetails) MarshalJSON() ([]byte, error) {
	type raw struct {
		TextTokens              int                          `json:"text_tokens,omitempty"`
		AudioTokens             int                          `json:"audio_tokens,omitempty"`
		ImageTokens             int                          `json:"image_tokens,omitempty"`
		CachedReadTokens        int                          `json:"cached_read_tokens,omitempty"`
		CachedWriteTokens       int                          `json:"cached_write_tokens,omitempty"`
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

func (t *ToolUsage) Add(o *ToolUsage) *ToolUsage {
	if t == nil && o == nil {
		return nil
	}
	sum := &ToolUsage{}
	if t != nil && t.WebSearch != nil {
		sum.WebSearch = &WebSearchToolUsage{NumRequests: t.WebSearch.NumRequests}
	}
	if o != nil && o.WebSearch != nil {
		if sum.WebSearch == nil {
			sum.WebSearch = &WebSearchToolUsage{}
		}
		sum.WebSearch.NumRequests += o.WebSearch.NumRequests
	}
	return sum
}

func (t *ToolUsage) DeepCopy() *ToolUsage {
	if t == nil {
		return nil
	}
	c := *t
	if t.WebSearch != nil {
		ws := *t.WebSearch
		c.WebSearch = &ws
	}
	return &c
}

func (bc *BifrostCost) UnmarshalJSON(data []byte) error {

	var costFloat float64
	if err := Unmarshal(data, &costFloat); err == nil {
		bc.TotalCost = costFloat
		return nil
	}

	type Alias BifrostCost
	var nested Alias
	if err := Unmarshal(data, &nested); err != nil {
		return fmt.Errorf("cost field is neither a float nor an object: %w", err)
	}
	*bc = BifrostCost(nested)

	if bc.InputCost == 0 && bc.OutputCost == 0 && bc.InputCostDetails == nil && bc.OutputCostDetails == nil {
		var legacy legacyBifrostCost
		if err := Unmarshal(data, &legacy); err == nil {
			legacy.mergeInto(bc)
		}
	}
	return nil
}

func (l legacyBifrostCost) mergeInto(bc *BifrostCost) {
	if l.InputTokensCost == nil && l.OutputTokensCost == nil && l.RequestCost == nil &&
		l.CacheReadTokensCost == nil && l.ReasoningTokensCost == nil &&
		l.CitationTokensCost == nil && l.SearchQueriesCost == nil {
		return
	}
	val := func(p *float64) float64 {
		if p != nil {
			return *p
		}
		return 0
	}
	inTok, outTok := val(l.InputTokensCost), val(l.OutputTokensCost)
	cacheRead, request := val(l.CacheReadTokensCost), val(l.RequestCost)
	reasoning, citation, search := val(l.ReasoningTokensCost), val(l.CitationTokensCost), val(l.SearchQueriesCost)

	bc.InputCost = inTok + request
	bc.OutputCost = outTok + reasoning + citation + search
	if bc.InputCost != 0 || cacheRead != 0 {
		bc.InputCostDetails = &InputCostDetails{
			TextCost:       inTok - cacheRead,
			CachedReadCost: cacheRead,
			RequestCost:    request,
		}
	}
	if bc.OutputCost != 0 {
		bc.OutputCostDetails = &OutputCostDetails{
			TextCost:          outTok,
			ReasoningCost:     reasoning,
			CitationCost:      citation,
			SearchQueriesCost: search,
		}
	}
}

func (bc *BifrostCost) DeepCopy() *BifrostCost {
	if bc == nil {
		return nil
	}
	return &BifrostCost{
		InputCost:             bc.InputCost,
		InputCostDetails:      bc.InputCostDetails.deepCopy(),
		OutputCost:            bc.OutputCost,
		OutputCostDetails:     bc.OutputCostDetails.deepCopy(),
		AdditionalCost:        bc.AdditionalCost,
		AdditionalCostDetails: bc.AdditionalCostDetails.deepCopy(),
		TotalCost:             bc.TotalCost,
	}
}

func (a *InputCostDetails) deepCopy() *InputCostDetails {
	if a == nil {
		return nil
	}
	return &InputCostDetails{
		TextCost:        a.TextCost,
		AudioCost:       a.AudioCost,
		ImageCost:       a.ImageCost,
		CachedReadCost:  a.CachedReadCost,
		CachedWriteCost: a.CachedWriteCost,
		RequestCost:     a.RequestCost,
	}
}

func (a *OutputCostDetails) deepCopy() *OutputCostDetails {
	if a == nil {
		return nil
	}
	return &OutputCostDetails{
		TextCost:          a.TextCost,
		AudioCost:         a.AudioCost,
		ImageCost:         a.ImageCost,
		ReasoningCost:     a.ReasoningCost,
		CitationCost:      a.CitationCost,
		SearchQueriesCost: a.SearchQueriesCost,
	}
}

func (a *AdditionalCostDetails) deepCopy() *AdditionalCostDetails {
	if a == nil {
		return nil
	}
	return &AdditionalCostDetails{
		GuardrailCost:     a.GuardrailCost,
		MCPCost:           a.MCPCost,
		SemanticCacheCost: a.SemanticCacheCost,
		RoutingCost:       a.RoutingCost,
	}
}

func (bc *BifrostCost) Add(other *BifrostCost) *BifrostCost {
	if bc == nil {
		return other
	}
	if other == nil {
		return bc
	}
	return &BifrostCost{
		InputCost:             bc.InputCost + other.InputCost,
		InputCostDetails:      bc.InputCostDetails.add(other.InputCostDetails),
		OutputCost:            bc.OutputCost + other.OutputCost,
		OutputCostDetails:     bc.OutputCostDetails.add(other.OutputCostDetails),
		AdditionalCost:        bc.AdditionalCost + other.AdditionalCost,
		AdditionalCostDetails: bc.AdditionalCostDetails.add(other.AdditionalCostDetails),
		TotalCost:             bc.TotalCost + other.TotalCost,
	}
}

func (a *InputCostDetails) add(b *InputCostDetails) *InputCostDetails {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &InputCostDetails{
		TextCost:        a.TextCost + b.TextCost,
		AudioCost:       a.AudioCost + b.AudioCost,
		ImageCost:       a.ImageCost + b.ImageCost,
		CachedReadCost:  a.CachedReadCost + b.CachedReadCost,
		CachedWriteCost: a.CachedWriteCost + b.CachedWriteCost,
		RequestCost:     a.RequestCost + b.RequestCost,
	}
}

func (a *OutputCostDetails) add(b *OutputCostDetails) *OutputCostDetails {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &OutputCostDetails{
		TextCost:          a.TextCost + b.TextCost,
		AudioCost:         a.AudioCost + b.AudioCost,
		ImageCost:         a.ImageCost + b.ImageCost,
		ReasoningCost:     a.ReasoningCost + b.ReasoningCost,
		CitationCost:      a.CitationCost + b.CitationCost,
		SearchQueriesCost: a.SearchQueriesCost + b.SearchQueriesCost,
	}
}

func (a *AdditionalCostDetails) add(b *AdditionalCostDetails) *AdditionalCostDetails {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &AdditionalCostDetails{
		GuardrailCost:     a.GuardrailCost + b.GuardrailCost,
		MCPCost:           a.MCPCost + b.MCPCost,
		SemanticCacheCost: a.SemanticCacheCost + b.SemanticCacheCost,
		RoutingCost:       a.RoutingCost + b.RoutingCost,
	}
}

func (u *BifrostLLMUsage) DeepCopy() *BifrostLLMUsage {
	if u == nil {
		return nil
	}
	c := *u
	if u.PromptTokensDetails != nil {
		pd := *u.PromptTokensDetails
		if u.PromptTokensDetails.CachedWriteTokenDetails != nil {
			cw := *u.PromptTokensDetails.CachedWriteTokenDetails
			pd.CachedWriteTokenDetails = &cw
		}
		c.PromptTokensDetails = &pd
	}
	if u.CompletionTokensDetails != nil {
		cd := *u.CompletionTokensDetails
		cd.CitationTokens = copyIntPtr(u.CompletionTokensDetails.CitationTokens)
		cd.NumSearchQueries = copyIntPtr(u.CompletionTokensDetails.NumSearchQueries)
		cd.ImageTokens = copyIntPtr(u.CompletionTokensDetails.ImageTokens)
		c.CompletionTokensDetails = &cd
	}
	if u.SearchUnits != nil {
		su := *u.SearchUnits
		c.SearchUnits = &su
	}
	c.ToolUsage = u.ToolUsage.DeepCopy()
	c.Cost = u.Cost.DeepCopy()
	if u.CostInUsdTicks != nil {
		t := *u.CostInUsdTicks
		c.CostInUsdTicks = &t
	}
	if u.Speed != nil {
		s := *u.Speed
		c.Speed = &s
	}
	c.StateTokens = copyIntPtr(u.StateTokens)
	c.StateTokensDropped = copyIntPtr(u.StateTokensDropped)
	if u.Truncated != nil {
		tr := *u.Truncated
		c.Truncated = &tr
	}
	if u.TruncatedQuestions != nil {
		c.TruncatedQuestions = append([]string(nil), u.TruncatedQuestions...)
	}
	if u.InferenceGeo != nil {
		g := *u.InferenceGeo
		c.InferenceGeo = &g
	}
	if u.ServerSideFallbackModel != nil {
		m := *u.ServerSideFallbackModel
		c.ServerSideFallbackModel = &m
	}
	return &c
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
