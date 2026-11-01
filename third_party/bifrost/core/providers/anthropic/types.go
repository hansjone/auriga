// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type AnthropicMessageRequest struct {
	Model             string                 `json:"model"`
	MaxTokens         int                    `json:"max_tokens"`
	Messages          []AnthropicMessage     `json:"messages"`
	Metadata          *AnthropicMetaData     `json:"metadata,omitempty"`
	System            *AnthropicContent      `json:"system,omitempty"`
	CacheControl      *schemas.CacheControl  `json:"cache_control,omitempty"`
	Temperature       *float64               `json:"temperature,omitempty"`
	TopP              *float64               `json:"top_p,omitempty"`
	TopK              *int                   `json:"top_k,omitempty"`
	StopSequences     []string               `json:"stop_sequences,omitempty"`
	Stream            *bool                  `json:"stream,omitempty"`
	Tools             []AnthropicTool        `json:"tools,omitempty"`
	ToolChoice        *AnthropicToolChoice   `json:"tool_choice,omitempty"`
	MCPServers        []AnthropicMCPServerV2 `json:"mcp_servers,omitempty"`
	Thinking          *AnthropicThinking     `json:"thinking,omitempty"`
	OutputFormat      json.RawMessage        `json:"output_format,omitempty"`
	OutputConfig      *AnthropicOutputConfig `json:"output_config,omitempty"`
	Speed             *string                `json:"speed,omitempty"`
	ServiceTier       *string                `json:"service_tier,omitempty"`
	InferenceGeo      *string                `json:"inference_geo,omitempty"`
	ContextManagement *ContextManagement     `json:"context_management,omitempty"`
	Container         *AnthropicContainer    `json:"container,omitempty"`
	Diagnostics       *AnthropicDiagnostics  `json:"diagnostics,omitempty"`

	FallbackCreditToken *string `json:"fallback_credit_token,omitempty"`

	Safeguards json.RawMessage `json:"safeguards,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`

	Fallbacks *AnthropicFallbacks `json:"fallbacks,omitempty"`

	stripCacheControlScope bool `json:"-"`
}
type AnthropicContentBlockType string
type AnthropicMCPToolsetTool struct {
	Type          string                                `json:"type"`
	MCPServerName string                                `json:"mcp_server_name"`
	DefaultConfig *AnthropicMCPToolsetConfig            `json:"default_config,omitempty"`
	Configs       map[string]*AnthropicMCPToolsetConfig `json:"configs,omitempty"`
	CacheControl  *schemas.CacheControl                 `json:"cache_control,omitempty"`
}
type ProviderFeatureSupport struct {
	WebSearch              bool
	WebSearchNova          bool
	WebSearchDynamic       bool
	WebFetch               bool
	CodeExecution          bool
	CodeExecNova           bool
	ComputerUse            bool
	Bash                   bool
	Memory                 bool
	TextEditor             bool
	ToolSearch             bool
	MCP                    bool
	AdvancedToolUse        bool
	InputExamples          bool
	StructuredOutputs      bool
	PromptCachingScope     bool
	Compaction             bool
	ContextEditing         bool
	ContextManagementField bool
	FilesAPI               bool
	InterleavedThinking    bool
	Skills                 bool
	ContainerBasic         bool
	Context1M              bool
	FastMode               bool
	RedactThinking         bool
	TaskBudgets            bool
	InferenceGeo           bool
	EagerInputStreaming    bool
	AdvisorTool            bool
	FileSearch             bool
	ImageGeneration        bool
	ServiceTier            bool
	Diagnostics            bool
	ServerSideFallback     bool
	FallbackCredit         bool
	Safeguards             bool
	MidConvToolChanges     bool
	MidConvOutputConfig    bool
	ThinkingDisplayUpdates bool
}
type AnthropicToolWebSearchUserLocation struct {
	Type     *string `json:"type,omitempty"`
	City     *string `json:"city,omitempty"`
	Region   *string `json:"region,omitempty"`
	Country  *string `json:"country,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}
type AnthropicToolWebSearch struct {
	MaxUses        *int                                `json:"max_uses,omitempty"`
	AllowedDomains []string                            `json:"allowed_domains,omitempty"`
	BlockedDomains []string                            `json:"blocked_domains,omitempty"`
	UserLocation   *AnthropicToolWebSearchUserLocation `json:"user_location,omitempty"`
}
type AnthropicContainer struct {
	ContainerStr    *string
	ContainerObject *AnthropicContainerObject
}
type CompactManagementEditConfig struct {
	Trigger              *CompactManagementEditTypeAndValue `json:"trigger,omitempty"`
	PauseAfterCompaction *bool                              `json:"pause_after_compaction,omitempty"`
	Instructions         *string                            `json:"instructions,omitempty"`
}
type AnthropicContentBlock struct {
	Type             AnthropicContentBlockType `json:"type"`
	Text             *string                   `json:"text,omitempty"`
	Thinking         *string                   `json:"thinking,omitempty"`
	Signature        *string                   `json:"signature,omitempty"`
	Data             *string                   `json:"data,omitempty"`
	ToolUseID        *string                   `json:"tool_use_id,omitempty"`
	ID               *string                   `json:"id,omitempty"`
	Name             *string                   `json:"name,omitempty"`
	Input            json.RawMessage           `json:"input,omitempty"`
	ServerName       *string                   `json:"server_name,omitempty"`
	Content          *AnthropicContent         `json:"content,omitempty"`
	IsError          *bool                     `json:"is_error,omitempty"`
	Source           *AnthropicBlockSource     `json:"source,omitempty"`
	CacheControl     *schemas.CacheControl     `json:"cache_control,omitempty"`
	Citations        *AnthropicCitations       `json:"citations,omitempty"`
	Context          *string                   `json:"context,omitempty"`
	Title            *string                   `json:"title,omitempty"`
	URL              *string                   `json:"url,omitempty"`
	EncryptedContent *string                   `json:"encrypted_content,omitempty"`
	PageAge          *string                   `json:"page_age,omitempty"`
	ErrorCode        *string                   `json:"error_code,omitempty"`
	StopReason       *string                   `json:"stop_reason,omitempty"`
	Caller           *AnthropicToolCaller      `json:"caller,omitempty"`

	ToolsetName *string `json:"toolset_name,omitempty"`

	Stdout          *string `json:"stdout,omitempty"`
	Stderr          *string `json:"stderr,omitempty"`
	ReturnCode      *int    `json:"return_code,omitempty"`
	EncryptedStdout *string `json:"encrypted_stdout,omitempty"`

	FileType     *string  `json:"file_type,omitempty"`
	StartLine    *int     `json:"start_line,omitempty"`
	NumLines     *int     `json:"num_lines,omitempty"`
	TotalLines   *int     `json:"total_lines,omitempty"`
	IsFileUpdate *bool    `json:"is_file_update,omitempty"`
	OldStart     *int     `json:"old_start,omitempty"`
	OldLines     *int     `json:"old_lines,omitempty"`
	NewStart     *int     `json:"new_start,omitempty"`
	NewLines     *int     `json:"new_lines,omitempty"`
	Lines        []string `json:"lines,omitempty"`
	ErrorMessage *string  `json:"error_message,omitempty"`

	ToolReferences []AnthropicContentBlock `json:"tool_references,omitempty"`

	ToolName *string `json:"tool_name,omitempty"`

	FileID *string `json:"file_id,omitempty"`

	RetrievedAt *string `json:"retrieved_at,omitempty"`

	From    *AnthropicFallbackModel   `json:"from,omitempty"`
	To      *AnthropicFallbackModel   `json:"to,omitempty"`
	Trigger *AnthropicFallbackTrigger `json:"trigger,omitempty"`

	Tool *AnthropicToolChangeTarget `json:"tool,omitempty"`

	ToolChanges []AnthropicContentBlock `json:"tool_changes,omitempty"`
}
type AnthropicMessage struct {
	Role         AnthropicMessageRole          `json:"role"`
	Content      AnthropicContent              `json:"content"`
	OutputConfig *AnthropicMessageOutputConfig `json:"output_config,omitempty"`
}
type AnthropicToolComputerUse struct {
	DisplayWidthPx  *int  `json:"display_width_px,omitempty"`
	DisplayHeightPx *int  `json:"display_height_px,omitempty"`
	DisplayNumber   *int  `json:"display_number,omitempty"`
	EnableZoom      *bool `json:"enable_zoom,omitempty"`
}
type AnthropicNativeFallback struct {
	Model        string                 `json:"model"`
	MaxTokens    *int                   `json:"max_tokens,omitempty"`
	Thinking     *AnthropicThinking     `json:"thinking,omitempty"`
	OutputConfig *AnthropicOutputConfig `json:"output_config,omitempty"`
	Speed        *string                `json:"speed,omitempty"`
}
type AnthropicStreamDelta struct {
	Type         AnthropicStreamDeltaType `json:"type,omitempty"`
	Text         *string                  `json:"text,omitempty"`
	Content      *string                  `json:"content,omitempty"`
	PartialJSON  *string                  `json:"partial_json,omitempty"`
	Thinking     *string                  `json:"thinking,omitempty"`
	Signature    *string                  `json:"signature,omitempty"`
	Citation     *AnthropicTextCitation   `json:"citation,omitempty"`
	StopReason   *AnthropicStopReason     `json:"stop_reason,omitempty"`
	StopDetails  *AnthropicStopDetails    `json:"stop_details,omitempty"`
	StopSequence *string                  `json:"stop_sequence"`

	Container *AnthropicResponseContainer `json:"container,omitempty"`

	SafeguardResults json.RawMessage `json:"safeguard_results,omitempty"`
}
type AnthropicFileListResponse struct {
	Data    []AnthropicFileResponse `json:"data"`
	HasMore bool                    `json:"has_more"`
	FirstID *string                 `json:"first_id,omitempty"`
	LastID  *string                 `json:"last_id,omitempty"`
}
type CompactManagementEditTypeAndValue struct {
	TypeAndValueString *string
	TypeAndValueObject *CompactManagementEditTypeAndValueObject
}

var anthropicMessageRequestKnownFields = map[string]bool{
	"model":                 true,
	"max_tokens":            true,
	"messages":              true,
	"metadata":              true,
	"system":                true,
	"cache_control":         true,
	"temperature":           true,
	"top_p":                 true,
	"top_k":                 true,
	"stop_sequences":        true,
	"stream":                true,
	"tools":                 true,
	"tool_choice":           true,
	"mcp_servers":           true,
	"thinking":              true,
	"output_format":         true,
	"output_config":         true,
	"speed":                 true,
	"service_tier":          true,
	"inference_geo":         true,
	"context_management":    true,
	"container":             true,
	"diagnostics":           true,
	"fallback_credit_token": true,
	"safeguards":            true,
	"extra_params":          true,
	"fallbacks":             true,
}

type AnthropicDiagnostics struct {
	PreviousMessageID *string `json:"previous_message_id"`
}
type AnthropicSource struct {
	Type      string          `json:"type"`
	MediaType *string         `json:"media_type,omitempty"`
	Data      *string         `json:"data,omitempty"`
	URL       *string         `json:"url,omitempty"`
	FileID    *string         `json:"file_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}
type AnthropicFileResponse struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Filename     string `json:"filename"`
	MimeType     string `json:"mime_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	CreatedAt    string `json:"created_at"`
	Downloadable *bool  `json:"downloadable,omitempty"`
}
type AnthropicToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name,omitempty"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use,omitempty"`
}
type AnthropicToolType string
type CompactManagementEditClearToolUses struct {
	ClearToolInputs *ClearToolInputs                   `json:"clear_tool_inputs,omitempty"`
	ClearAtLast     *CompactManagementEditTypeAndValue `json:"clear_at_last,omitempty"`
	Keep            *CompactManagementEditTypeAndValue `json:"keep,omitempty"`
	ExcludeTools    []string                           `json:"exclude_tools,omitempty"`
	Trigger         *CompactManagementEditTypeAndValue `json:"trigger,omitempty"`
}
type AnthropicThinking struct {
	Type         string  `json:"type"`
	BudgetTokens *int    `json:"budget_tokens,omitempty"`
	Display      *string `json:"display,omitempty"`
}
type AnthropicResponseContainer struct {
	ID        string  `json:"id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}
type AnthropicFileDeleteResponse struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}
type AnthropicContent struct {
	ContentStr    *string
	ContentBlocks []AnthropicContentBlock

	ContentObj *AnthropicContentBlock
}
type AnthropicMCPToolsetConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	DeferLoading *bool `json:"defer_loading,omitempty"`
}
type AnthropicStreamEvent struct {
	ID           *string                   `json:"id,omitempty"`
	Type         AnthropicStreamEventType  `json:"type"`
	Message      *AnthropicMessageResponse `json:"message,omitempty"`
	Index        *int                      `json:"index,omitempty"`
	ContentBlock *AnthropicContentBlock    `json:"content_block,omitempty"`
	Delta        *AnthropicStreamDelta     `json:"delta,omitempty"`
	Usage        *AnthropicUsage           `json:"usage,omitempty"`
	Error        *AnthropicStreamError     `json:"error,omitempty"`

	SafeguardResults json.RawMessage `json:"safeguard_results,omitempty"`
}
type AnthropicBlockSource struct {
	SourceStr *string
	SourceObj *AnthropicSource
}
type AnthropicMCPServerV2 struct {
	Type               string  `json:"type"`
	URL                string  `json:"url"`
	Name               string  `json:"name"`
	AuthorizationToken *string `json:"authorization_token,omitempty"`
}
type AnthropicToolTextEditor struct {
	MaxCharacters *int `json:"max_characters,omitempty"`
}
type AnthropicMetaData struct {
	UserID *string `json:"user_id"`
}
type AnthropicStreamDeltaType string
type AnthropicTextResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Completion string `json:"completion"`
	Model      string `json:"model"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
type ContextManagementEditType string
type AnthropicContainerSkill struct {
	SkillID string  `json:"skill_id"`
	Type    string  `json:"type"`
	Version *string `json:"version,omitempty"`
}
type AnthropicToolInputExample struct {
	Input       json.RawMessage `json:"input"`
	Description *string         `json:"description,omitempty"`
}
type AnthropicOutputTokensDetails struct {
	ThinkingTokens int `json:"thinking_tokens"`
}
type AnthropicUsage struct {
	Type  *string `json:"type,omitempty"`
	Model *string `json:"model,omitempty"`

	InputTokens              int                           `json:"input_tokens"`
	CacheCreationInputTokens int                           `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                           `json:"cache_read_input_tokens"`
	CacheCreation            AnthropicUsageCacheCreation   `json:"cache_creation"`
	OutputTokens             int                           `json:"output_tokens"`
	OutputTokensDetails      *AnthropicOutputTokensDetails `json:"output_tokens_details,omitempty"`
	ServerToolUse            *AnthropicServerToolUseUsage  `json:"server_tool_use,omitempty"`
	ServiceTier              *string                       `json:"service_tier,omitempty"`
	Speed                    *string                       `json:"speed,omitempty"`
	InferenceGeo             *string                       `json:"inference_geo,omitempty"`
	Iterations               []AnthropicUsage              `json:"iterations,omitempty"`

	absentPromptCounters uint8
}
type AnthropicStreamEventType string
type AnthropicUsageCacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}
type ClearToolInputs struct {
	ClearToolInputsBoolean *bool
	ClearToolInputsArray   []string
}
type AnthropicToolCaller struct {
	Type   AnthropicToolCallerType `json:"type"`
	ToolID *string                 `json:"tool_id,omitempty"`
}
type AnthropicContainerObject struct {
	ID     *string                   `json:"id,omitempty"`
	Skills []AnthropicContainerSkill `json:"skills,omitempty"`
}
type AnthropicOutputConfig struct {
	Format     json.RawMessage      `json:"format,omitempty"`
	Effort     *string              `json:"effort,omitempty"`
	TaskBudget *AnthropicTaskBudget `json:"task_budget,omitempty"`
}
type AnthropicServerToolUseUsage struct {
	WebSearchRequests int `json:"web_search_requests"`
}
type AnthropicFallbacks struct {
	Entries []AnthropicFallbackEntry
	Preset  string
}
type AnthropicCitations struct {
	Config *schemas.Citations

	TextCitations []AnthropicTextCitation
}
type AnthropicCitationType string
type AnthropicImageContent struct {
	Type      schemas.ImageContentType `json:"type"`
	URL       string                   `json:"url"`
	MediaType string                   `json:"media_type,omitempty"`
}
type AnthropicToolAdvisor struct {
	Model     string                       `json:"model,omitempty"`
	MaxUses   *int                         `json:"max_uses,omitempty"`
	MaxTokens *int                         `json:"max_tokens,omitempty"`
	Caching   *AnthropicToolAdvisorCaching `json:"caching,omitempty"`
}
type AnthropicToolChangeTarget struct {
	Type       string         `json:"type"`
	Name       *string        `json:"name,omitempty"`
	ServerName *string        `json:"server_name,omitempty"`
	Definition *AnthropicTool `json:"definition,omitempty"`
}

var ProviderFeatures = map[schemas.ModelProvider]ProviderFeatureSupport{

	schemas.Anthropic: {
		WebSearch: true, WebSearchDynamic: true, WebFetch: true, CodeExecution: true,
		ComputerUse: true, Bash: true, Memory: true, TextEditor: true, ToolSearch: true,
		MCP: true, AdvancedToolUse: true, InputExamples: true, StructuredOutputs: true, PromptCachingScope: true,
		Compaction: true, ContextEditing: true, ContextManagementField: true, FilesAPI: true,
		InterleavedThinking: true, Skills: true, ContainerBasic: true, Context1M: true,
		FastMode: true, RedactThinking: true, TaskBudgets: true,
		InferenceGeo: true, EagerInputStreaming: true, AdvisorTool: true,
		ServiceTier:         true,
		Diagnostics:         true,
		ServerSideFallback:  true,
		FallbackCredit:      true,
		Safeguards:          true,
		MidConvToolChanges:  true,
		MidConvOutputConfig: true,

		ThinkingDisplayUpdates: true,
	},

	schemas.Vertex: {
		WebSearch:   true,
		ComputerUse: true, Bash: true, Memory: true, TextEditor: true, ToolSearch: true,
		ContainerBasic:         true,
		Compaction:             true,
		ContextEditing:         true,
		ContextManagementField: true,
		InterleavedThinking:    true,
		Context1M:              true,
		EagerInputStreaming:    true,
		FallbackCredit:         true,
		ThinkingDisplayUpdates: true,
		Safeguards:             true,
	},

	schemas.Bedrock: {
		WebSearchNova: true,
		CodeExecNova:  true,
		ComputerUse:   true, Bash: true, Memory: true, TextEditor: true,
		ToolSearch:             true,
		ContainerBasic:         true,
		StructuredOutputs:      true,
		Compaction:             true,
		ContextEditing:         true,
		ContextManagementField: true,
		InterleavedThinking:    true,
		Context1M:              true,
		EagerInputStreaming:    true,
		InputExamples:          true,

		ServiceTier:    true,
		FallbackCredit: true,
		Safeguards:     true,

		ThinkingDisplayUpdates: true,
	},

	schemas.BedrockMantle: {
		ComputerUse: true, Bash: true, Memory: true, TextEditor: true, ToolSearch: true,
		ContainerBasic:         true,
		Compaction:             true,
		ContextEditing:         true,
		ContextManagementField: true,
		InterleavedThinking:    true,
		Context1M:              true,
		EagerInputStreaming:    true,
		InputExamples:          true,
		ServiceTier:            true,
		FallbackCredit:         true,
		MidConvToolChanges:     true,
		ThinkingDisplayUpdates: true,
		Safeguards:             true,
	},

	schemas.Azure: {
		WebSearch: true, WebSearchDynamic: true, WebFetch: true, CodeExecution: true,
		ComputerUse: true, Bash: true, Memory: true, TextEditor: true, ToolSearch: true,
		MCP: true, AdvancedToolUse: true, InputExamples: true, StructuredOutputs: true, PromptCachingScope: true,
		Compaction: true, ContextEditing: true, ContextManagementField: true, FilesAPI: true,
		InterleavedThinking: true, Skills: true, ContainerBasic: true, Context1M: true,
		RedactThinking:      true,
		EagerInputStreaming: true,

		ServiceTier:    true,
		FallbackCredit: true,
		Safeguards:     true,

		ThinkingDisplayUpdates: true,
	},
	schemas.DeepSeek: {
		WebSearch:              true,
		WebSearchDynamic:       true,
		ContainerBasic:         true,
		ContextManagementField: true,
		Compaction:             true,
		ContextEditing:         true,
		PromptCachingScope:     true,
		AdvancedToolUse:        true,
		InputExamples:          true,
		EagerInputStreaming:    true,
		StructuredOutputs:      true,
		InterleavedThinking:    true,
		ServiceTier:            true,
	},

	schemas.Fireworks: {
		ToolSearch:  true,
		ServiceTier: true,
	},

	schemas.VLLM: {},
	schemas.SGL:  {},

	schemas.GithubCopilot: {
		ToolSearch:             true,
		AdvancedToolUse:        true,
		ContextEditing:         true,
		ContextManagementField: true,
		InterleavedThinking:    true,
	},
}

type AnthropicCountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}
type AnthropicFallbackTrigger struct {
	Type     string  `json:"type"`
	Category *string `json:"category,omitempty"`
}
type AnthropicTextRequest struct {
	Model             string   `json:"model"`
	Prompt            string   `json:"prompt"`
	MaxTokensToSample int      `json:"max_tokens_to_sample"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	TopK              *int     `json:"top_k,omitempty"`
	Stream            *bool    `json:"stream,omitempty"`
	StopSequences     []string `json:"stop_sequences,omitempty"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type ContextManagement struct {
	Edits []ContextManagementEdit `json:"edits,omitempty"`
}
type AnthropicToolAdvisorCaching struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}
type AnthropicToolWebFetch struct {
	MaxUses           *int                `json:"max_uses,omitempty"`
	AllowedDomains    []string            `json:"allowed_domains,omitempty"`
	BlockedDomains    []string            `json:"blocked_domains,omitempty"`
	MaxContentTokens  *int                `json:"max_content_tokens,omitempty"`
	Citations         *AnthropicCitations `json:"citations,omitempty"`
	UseCache          *bool               `json:"use_cache,omitempty"`
	ResponseInclusion *string             `json:"response_inclusion,omitempty"`
}
type AnthropicFallbackEntry struct {
	BifrostModel string
	Native       *AnthropicNativeFallback
}
type AnthropicMessageRole string
type AnthropicToolName string
type CompactManagementEditClearThinking struct {
	Keep *CompactManagementEditTypeAndValue `json:"keep,omitempty"`
}
type AnthropicFallbackModel struct {
	Model string `json:"model"`
}
type AnthropicTool struct {
	Name                string                          `json:"name,omitempty"`
	Type                *AnthropicToolType              `json:"type,omitempty"`
	Description         *string                         `json:"description,omitempty"`
	InputSchema         *schemas.ToolFunctionParameters `json:"input_schema,omitempty"`
	CacheControl        *schemas.CacheControl           `json:"cache_control,omitempty"`
	DeferLoading        *bool                           `json:"defer_loading,omitempty"`
	Strict              *bool                           `json:"strict,omitempty"`
	AllowedCallers      []string                        `json:"allowed_callers,omitempty"`
	InputExamples       []AnthropicToolInputExample     `json:"input_examples,omitempty"`
	EagerInputStreaming *bool                           `json:"eager_input_streaming,omitempty"`

	*AnthropicToolComputerUse
	*AnthropicToolWebSearch
	*AnthropicToolWebFetch
	*AnthropicToolTextEditor
	*AnthropicToolAdvisor

	MCPToolset *AnthropicMCPToolsetTool `json:"-"`
}
type AnthropicModel struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	DisplayName    string          `json:"display_name"`
	CreatedAt      time.Time       `json:"created_at"`
	MaxInputTokens *int            `json:"max_input_tokens,omitempty"`
	MaxTokens      *int            `json:"max_tokens,omitempty"`
	Capabilities   json.RawMessage `json:"capabilities,omitempty"`
}
type ContextManagementEdit struct {
	Type ContextManagementEditType `json:"type"`
	*CompactManagementEditConfig
	*CompactManagementEditClearThinking
	*CompactManagementEditClearToolUses
}
type AnthropicMessageOutputConfig struct {
	Effort *string `json:"effort,omitempty"`
}
type AnthropicListModelsResponse struct {
	Data    []AnthropicModel `json:"data"`
	FirstID *string          `json:"first_id,omitempty"`
	HasMore bool             `json:"has_more"`
	LastID  *string          `json:"last_id,omitempty"`
}
type CompactManagementEditTypeAndValueObject struct {
	Type  string `json:"type"`
	Value *int   `json:"value,omitempty"`
}
type AnthropicTaskBudget struct {
	Type      string `json:"type"`
	Total     int    `json:"total"`
	Remaining *int   `json:"remaining,omitempty"`
}
type AnthropicToolCallerType string
type AnthropicStopReason string
type AnthropicTextCitation struct {
	Type      AnthropicCitationType `json:"type"`
	CitedText string                `json:"cited_text"`

	FileID *string `json:"file_id,omitempty"`

	DocumentIndex *int    `json:"document_index,omitempty"`
	DocumentTitle *string `json:"document_title,omitempty"`

	StartCharIndex *int `json:"start_char_index,omitempty"`
	EndCharIndex   *int `json:"end_char_index,omitempty"`

	StartPageNumber *int `json:"start_page_number,omitempty"`
	EndPageNumber   *int `json:"end_page_number,omitempty"`

	StartBlockIndex *int `json:"start_block_index,omitempty"`
	EndBlockIndex   *int `json:"end_block_index,omitempty"`

	EncryptedIndex *string `json:"encrypted_index,omitempty"`
	Title          *string `json:"title,omitempty"`
	URL            *string `json:"url,omitempty"`

	SearchResultIndex *int    `json:"search_result_index,omitempty"`
	Source            *string `json:"source,omitempty"`
}
type AnthropicMessageResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   AnthropicStopReason     `json:"stop_reason"`
	StopDetails  *AnthropicStopDetails   `json:"stop_details,omitempty"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`

	Container *AnthropicResponseContainer `json:"container,omitempty"`

	Diagnostics *schemas.CacheDiagnostics `json:"diagnostics,omitempty"`

	SafeguardResults json.RawMessage `json:"safeguard_results,omitempty"`

	ExtraFields *schemas.BifrostResponseExtraFields `json:"extra_fields,omitempty"`
}
type AnthropicStreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
type AnthropicStopDetails struct {
	Type             string  `json:"type"`
	Category         *string `json:"category,omitempty"`
	Explanation      *string `json:"explanation,omitempty"`
	RecommendedModel *string `json:"recommended_model,omitempty"`

	FallbackCreditToken *string `json:"fallback_credit_token,omitempty"`

	FallbackHasPrefillClaim *bool `json:"fallback_has_prefill_claim,omitempty"`
}
type AnthropicError struct {
	Type  string `json:"type"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const (
	AnthropicDefaultMaxTokens = 4096
	MinimumReasoningMaxTokens = 1024

	AnthropicBetaHeader = "anthropic-beta"

	AnthropicDangerousToolUseBetaHeader       = "dangerous-tool-use-2026-09-03"
	AnthropicDangerousToolUseBetaHeaderPrefix = "dangerous-tool-use-"

	AnthropicAutoModeClassifierBetaHeaderPrefix = "auto-mode-classifier-"

	AnthropicFilesAPIBetaHeader = "files-api-2025-04-14"

	AnthropicStructuredOutputsBetaHeader = "structured-outputs-2025-11-13"

	AnthropicAdvancedToolUseBetaHeader = "advanced-tool-use-2025-11-20"

	AnthropicToolSearchBetaHeader = "tool-search-tool-2025-10-19"

	AnthropicToolExamplesBetaHeader = "tool-examples-2025-10-29"

	AnthropicMCPClientBetaHeader = "mcp-client-2025-11-20"

	AnthropicMCPClientBetaHeaderDeprecated = "mcp-client-2025-04-04"

	AnthropicMCPClientInlineToolsBetaHeader = "mcp-client-2026-09-15"

	AnthropicPromptCachingScopeBetaHeader = "prompt-caching-scope-2026-01-05"

	AnthropicCompactionBetaHeader = "compact-2026-01-12"

	AnthropicContextManagementBetaHeader = "context-management-2025-06-27"

	AnthropicInterleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

	AnthropicSkillsBetaHeader = "skills-2025-10-02"

	AnthropicContext1MBetaHeader = "context-1m-2025-08-07"

	AnthropicFastModeBetaHeader = "fast-mode-2026-02-01"

	AnthropicRedactThinkingBetaHeader = "redact-thinking-2026-02-12"

	AnthropicTaskBudgetsBetaHeader = "task-budgets-2026-03-13"

	AnthropicAdvisorBetaHeader = "advisor-tool-2026-03-01"

	AnthropicCacheDiagnosisBetaHeader = "cache-diagnosis-2026-04-07"

	AnthropicEagerInputStreamingBetaHeader = "fine-grained-tool-streaming-2025-05-14"

	AnthropicServerSideFallbackBetaHeader = "server-side-fallback-2026-06-01"

	AnthropicServerSideFallbackDefaultBetaHeader = "server-side-fallback-2026-07-01"

	AnthropicFallbackCreditBetaHeader = "fallback-credit-2026-06-01"

	AnthropicMidConversationToolChangesBetaHeader = "mid-conversation-tool-changes-2026-07-01"

	AnthropicInlineToolsBetaHeader = "inline-tools-2026-09-15"

	AnthropicMidConversationOutputConfigBetaHeader = "mid-conversation-output-config-2026-07-01"

	AnthropicThinkingDisplayUpdatesBetaHeader = "thinking-display-updates-2026-08-18"

	AnthropicThinkingDisplayUpdates = "updates"

	AnthropicComputerUseBetaHeader20251124 = "computer-use-2025-11-24"

	AnthropicComputerUseBetaHeader20250124 = "computer-use-2025-01-24"

	AnthropicAdvancedToolUseBetaHeaderPrefix     = "advanced-tool-use-"
	AnthropicToolSearchBetaHeaderPrefix          = "tool-search-tool-"
	AnthropicToolExamplesBetaHeaderPrefix        = "tool-examples-"
	AnthropicStructuredOutputsBetaHeaderPrefix   = "structured-outputs-"
	AnthropicPromptCachingScopeBetaHeaderPrefix  = "prompt-caching-scope-"
	AnthropicMCPClientBetaHeaderPrefix           = "mcp-client-"
	AnthropicInterleavedThinkingBetaHeaderPrefix = "interleaved-thinking-"
	AnthropicSkillsBetaHeaderPrefix              = "skills-"
	AnthropicContext1MBetaHeaderPrefix           = "context-1m-"
	AnthropicFastModeBetaHeaderPrefix            = "fast-mode-"
	AnthropicCacheDiagnosisBetaHeaderPrefix      = "cache-diagnosis-"
	AnthropicRedactThinkingBetaHeaderPrefix      = "redact-thinking-"
	AnthropicTaskBudgetsBetaHeaderPrefix         = "task-budgets-"
	AnthropicEagerInputStreamingBetaHeaderPrefix = "fine-grained-tool-streaming-"
	AnthropicContextManagementBetaHeaderPrefix   = "context-management-"
	AnthropicCompactionBetaHeaderPrefix          = "compact-"
	AnthropicAdvisorBetaHeaderPrefix             = "advisor-tool-"
	AnthropicServerSideFallbackBetaHeaderPrefix  = "server-side-fallback-"
	AnthropicFallbackCreditBetaHeaderPrefix      = "fallback-credit-"

	AnthropicMidConversationToolChangesBetaHeaderPrefix  = "mid-conversation-tool-changes-"
	AnthropicMidConversationOutputConfigBetaHeaderPrefix = "mid-conversation-output-config-"
	AnthropicInlineToolsBetaHeaderPrefix                 = "inline-tools-"
	AnthropicThinkingDisplayUpdatesBetaHeaderPrefix      = "thinking-display-updates-"
)

func (req *AnthropicTextRequest) GetExtraParams() map[string]interface{} {
	return req.ExtraParams
}

func (c AnthropicContainer) MarshalJSON() ([]byte, error) {
	if c.ContainerStr != nil && c.ContainerObject != nil {
		return nil, fmt.Errorf("both ContainerStr and ContainerObject are set; only one should be non-nil")
	}
	if c.ContainerStr != nil {
		return providerUtils.MarshalSorted(*c.ContainerStr)
	}
	if c.ContainerObject != nil {
		return providerUtils.MarshalSorted(c.ContainerObject)
	}
	return providerUtils.MarshalSorted(nil)
}

func (c *AnthropicContainer) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		c.ContainerStr = nil
		c.ContainerObject = nil
		return nil
	}
	var s string
	if err := sonic.Unmarshal(data, &s); err == nil {
		c.ContainerStr = &s
		c.ContainerObject = nil
		return nil
	}
	var obj AnthropicContainerObject
	if err := sonic.Unmarshal(data, &obj); err == nil {
		c.ContainerStr = nil
		c.ContainerObject = &obj
		return nil
	}
	return fmt.Errorf("container field is neither a string nor a container object")
}

func (e *AnthropicFallbackEntry) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty fallback entry")
	}
	switch trimmed[0] {
	case '"':
		return sonic.Unmarshal(trimmed, &e.BifrostModel)
	case '{':
		var native AnthropicNativeFallback
		if err := sonic.Unmarshal(trimmed, &native); err != nil {
			return err
		}
		e.Native = &native
		return nil
	default:
		return fmt.Errorf("fallback entry must be a string or object, got: %s", trimmed)
	}
}

func (e AnthropicFallbackEntry) MarshalJSON() ([]byte, error) {
	if e.Native != nil {
		return sonic.Marshal(e.Native)
	}
	return sonic.Marshal(e.BifrostModel)
}

func (f *AnthropicFallbacks) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		return sonic.Unmarshal(trimmed, &f.Preset)
	}
	return sonic.Unmarshal(trimmed, &f.Entries)
}

func (f AnthropicFallbacks) MarshalJSON() ([]byte, error) {
	if f.Preset != "" {
		return sonic.Marshal(f.Preset)
	}
	return sonic.Marshal(f.Entries)
}

func (req *AnthropicMessageRequest) nativeFallbacks() []AnthropicNativeFallback {
	var out []AnthropicNativeFallback
	if req.Fallbacks == nil {
		return out
	}
	for _, f := range req.Fallbacks.Entries {
		if f.Native != nil {
			out = append(out, *f.Native)
		}
	}
	return out
}

func (req *AnthropicMessageRequest) fallbacksDefaultRouting() bool {
	return req.Fallbacks != nil && req.Fallbacks.Preset == "default"
}

func (req *AnthropicMessageRequest) SetStripCacheControlScope(strip bool) {
	req.stripCacheControlScope = strip
}

func (req *AnthropicMessageRequest) GetExtraParams() map[string]interface{} {
	return req.ExtraParams
}

const (
	ContextManagementEditTypeClearToolUses ContextManagementEditType = "clear_tool_uses_20250919"
	ContextManagementEditTypeClearThinking ContextManagementEditType = "clear_thinking_20251015"
	ContextManagementEditTypeCompact       ContextManagementEditType = "compact_20260112"
)

func (tv CompactManagementEditTypeAndValue) MarshalJSON() ([]byte, error) {

	if tv.TypeAndValueString != nil && tv.TypeAndValueObject != nil {
		return nil, fmt.Errorf("both TypeAndValueString and TypeAndValueObject are set; only one should be non-nil")
	}

	if tv.TypeAndValueString != nil {
		return providerUtils.MarshalSorted(*tv.TypeAndValueString)
	}
	if tv.TypeAndValueObject != nil {
		return providerUtils.MarshalSorted(tv.TypeAndValueObject)
	}
	return providerUtils.MarshalSorted(nil)
}

func (tv *CompactManagementEditTypeAndValue) UnmarshalJSON(data []byte) error {

	var typeAndValueString string
	if err := sonic.Unmarshal(data, &typeAndValueString); err == nil {
		tv.TypeAndValueString = &typeAndValueString
		return nil
	}

	var objectContent CompactManagementEditTypeAndValueObject
	if err := sonic.Unmarshal(data, &objectContent); err == nil {
		tv.TypeAndValueObject = &objectContent
		return nil
	}

	return fmt.Errorf("field is neither a string nor a CompactManagementEditTypeAndValueObject")
}

func (ct ClearToolInputs) MarshalJSON() ([]byte, error) {

	if ct.ClearToolInputsBoolean != nil && ct.ClearToolInputsArray != nil {
		return nil, fmt.Errorf("both ClearToolInputsBoolean and ClearToolInputsArray are set; only one should be non-nil")
	}

	if ct.ClearToolInputsBoolean != nil {
		return providerUtils.MarshalSorted(*ct.ClearToolInputsBoolean)
	}
	if ct.ClearToolInputsArray != nil {
		return providerUtils.MarshalSorted(ct.ClearToolInputsArray)
	}
	return providerUtils.MarshalSorted(nil)
}

func (ct *ClearToolInputs) UnmarshalJSON(data []byte) error {

	var clearToolInputsBoolean bool
	if err := sonic.Unmarshal(data, &clearToolInputsBoolean); err == nil {
		ct.ClearToolInputsBoolean = &clearToolInputsBoolean
		return nil
	}

	var arrayContent []string
	if err := sonic.Unmarshal(data, &arrayContent); err == nil {
		ct.ClearToolInputsArray = arrayContent
		return nil
	}

	return fmt.Errorf("clear_tool_inputs field is neither a boolean nor an array of strings")
}

func (edit ContextManagementEdit) MarshalJSON() ([]byte, error) {

	type Alias ContextManagementEdit

	switch edit.Type {
	case ContextManagementEditTypeCompact:
		if edit.CompactManagementEditConfig == nil {
			return providerUtils.MarshalSorted(struct {
				Type ContextManagementEditType `json:"type"`
			}{
				Type: edit.Type,
			})
		}
		return providerUtils.MarshalSorted(struct {
			Type ContextManagementEditType `json:"type"`
			*CompactManagementEditConfig
		}{
			Type:                        edit.Type,
			CompactManagementEditConfig: edit.CompactManagementEditConfig,
		})
	case ContextManagementEditTypeClearThinking:
		if edit.CompactManagementEditClearThinking == nil {
			return nil, fmt.Errorf("compact management edit clear thinking is nil for type clear_thinking_20251015")
		}
		return providerUtils.MarshalSorted(struct {
			Type ContextManagementEditType `json:"type"`
			*CompactManagementEditClearThinking
		}{
			Type:                               edit.Type,
			CompactManagementEditClearThinking: edit.CompactManagementEditClearThinking,
		})
	case ContextManagementEditTypeClearToolUses:
		if edit.CompactManagementEditClearToolUses == nil {
			return nil, fmt.Errorf("compact management edit clear tool uses is nil for type clear_tool_uses_20250919")
		}
		return providerUtils.MarshalSorted(struct {
			Type ContextManagementEditType `json:"type"`
			*CompactManagementEditClearToolUses
		}{
			Type:                               edit.Type,
			CompactManagementEditClearToolUses: edit.CompactManagementEditClearToolUses,
		})
	default:
		return nil, fmt.Errorf("unknown context management edit type: %s", edit.Type)
	}
}

func (edit *ContextManagementEdit) UnmarshalJSON(data []byte) error {

	var typeStruct struct {
		Type ContextManagementEditType `json:"type"`
	}
	if err := sonic.Unmarshal(data, &typeStruct); err != nil {
		return fmt.Errorf("failed to peek at type field: %w", err)
	}

	edit.Type = typeStruct.Type

	switch typeStruct.Type {
	case ContextManagementEditTypeCompact:
		var config CompactManagementEditConfig
		if err := sonic.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to unmarshal compact management edit config: %w", err)
		}
		edit.CompactManagementEditConfig = &config
		return nil

	case ContextManagementEditTypeClearThinking:
		var clearThinking CompactManagementEditClearThinking
		if err := sonic.Unmarshal(data, &clearThinking); err != nil {
			return fmt.Errorf("failed to unmarshal compact management edit clear thinking: %w", err)
		}
		edit.CompactManagementEditClearThinking = &clearThinking
		return nil

	case ContextManagementEditTypeClearToolUses:
		var clearToolUses CompactManagementEditClearToolUses
		if err := sonic.Unmarshal(data, &clearToolUses); err != nil {
			return fmt.Errorf("failed to unmarshal compact management edit clear tool uses: %w", err)
		}
		edit.CompactManagementEditClearToolUses = &clearToolUses
		return nil

	default:
		return fmt.Errorf("unknown context management edit type: %s", typeStruct.Type)
	}
}

func (req *AnthropicMessageRequest) UnmarshalJSON(data []byte) error {

	type Alias AnthropicMessageRequest

	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(req),
	}

	if err := sonic.Unmarshal(data, aux); err != nil {
		return err
	}

	extractAnthropicMessageRequestUnknownFields(data, req)

	if len(req.OutputFormat) > 0 {
		var buf bytes.Buffer
		if err := json.Compact(&buf, req.OutputFormat); err == nil {
			req.OutputFormat = json.RawMessage(buf.Bytes())
		}
	}
	if req.OutputConfig != nil && len(req.OutputConfig.Format) > 0 {
		var buf bytes.Buffer
		if err := json.Compact(&buf, req.OutputConfig.Format); err == nil {
			req.OutputConfig.Format = json.RawMessage(buf.Bytes())
		}
	}
	if len(req.Safeguards) > 0 {
		var buf bytes.Buffer
		if err := json.Compact(&buf, req.Safeguards); err == nil {
			req.Safeguards = json.RawMessage(buf.Bytes())
		}
	}

	return nil
}

func extractAnthropicMessageRequestUnknownFields(data []byte, req *AnthropicMessageRequest) {
	gjson.ParseBytes(data).ForEach(func(key, value gjson.Result) bool {
		keyString := key.String()
		if anthropicMessageRequestKnownFields[keyString] {
			return true
		}

		if req.ExtraParams == nil {
			req.ExtraParams = make(map[string]interface{})
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(value.Raw)); err == nil {
			req.ExtraParams[keyString] = json.RawMessage(buf.Bytes())
		} else {
			req.ExtraParams[keyString] = json.RawMessage(value.Raw)
		}
		return true
	})
}

func (req *AnthropicMessageRequest) MarshalJSON() ([]byte, error) {

	if req.OutputFormat != nil && req.OutputConfig != nil {
		return nil, fmt.Errorf("both OutputFormat and OutputConfig are set; only one should be non-nil")
	}

	type Alias AnthropicMessageRequest

	if req.stripCacheControlScope {
		reqCopy := *req
		reqCopy.stripCacheControlScope = false

		if reqCopy.CacheControl != nil && reqCopy.CacheControl.Scope != nil {
			cc := *reqCopy.CacheControl
			cc.Scope = nil
			reqCopy.CacheControl = &cc
		}

		if len(reqCopy.Tools) > 0 {
			toolsCopy := make([]AnthropicTool, len(reqCopy.Tools))
			for i, tool := range reqCopy.Tools {
				toolsCopy[i] = tool
				if tool.CacheControl != nil && tool.CacheControl.Scope != nil {

					toolsCopy[i].CacheControl = &schemas.CacheControl{
						Type: tool.CacheControl.Type,
						TTL:  tool.CacheControl.TTL,
					}
				}
			}
			reqCopy.Tools = toolsCopy
		}

		if reqCopy.System != nil {
			reqCopy.System = stripScopeFromContent(reqCopy.System)
		}

		if len(reqCopy.Messages) > 0 {
			messagesCopy := make([]AnthropicMessage, len(reqCopy.Messages))
			for i, msg := range reqCopy.Messages {
				messagesCopy[i] = msg
				messagesCopy[i].Content = *stripScopeFromContent(&msg.Content)
			}
			reqCopy.Messages = messagesCopy
		}

		return providerUtils.MarshalSorted((*Alias)(&reqCopy))
	}

	return providerUtils.MarshalSorted((*Alias)(req))
}

func stripScopeFromContent(content *AnthropicContent) *AnthropicContent {
	if content == nil {
		return nil
	}

	result := &AnthropicContent{
		ContentStr: content.ContentStr,
	}

	if len(content.ContentBlocks) > 0 {
		blocksCopy := make([]AnthropicContentBlock, len(content.ContentBlocks))
		for i, block := range content.ContentBlocks {
			blocksCopy[i] = block
			if block.CacheControl != nil && block.CacheControl.Scope != nil {

				blocksCopy[i].CacheControl = &schemas.CacheControl{
					Type: block.CacheControl.Type,
					TTL:  block.CacheControl.TTL,
				}
			}
		}
		result.ContentBlocks = blocksCopy
	}

	return result
}

const (
	AnthropicMessageRoleUser      AnthropicMessageRole = "user"
	AnthropicMessageRoleAssistant AnthropicMessageRole = "assistant"
	AnthropicMessageRoleSystem    AnthropicMessageRole = "system"
)

func (mc AnthropicContent) MarshalJSON() ([]byte, error) {

	if mc.ContentStr != nil && mc.ContentBlocks != nil {
		return nil, fmt.Errorf("both ContentStr and ContentBlocks are set; only one should be non-nil")
	}

	if mc.ContentStr != nil {
		return providerUtils.MarshalSorted(*mc.ContentStr)
	}
	if mc.ContentObj != nil {
		data, err := providerUtils.MarshalSorted(mc.ContentObj)

		if err == nil && mc.ContentObj.Type == AnthropicContentBlockTypeToolSearchToolSearchResult && len(mc.ContentObj.ToolReferences) == 0 {
			return providerUtils.SetRawJSONField(data, "tool_references", []byte("[]"))
		}
		return data, err
	}
	if mc.ContentBlocks != nil {
		return providerUtils.MarshalSorted(mc.ContentBlocks)
	}

	return []byte("[]"), nil
}

func (mc *AnthropicContent) UnmarshalJSON(data []byte) error {

	var stringContent string
	if err := sonic.Unmarshal(data, &stringContent); err == nil {
		mc.ContentStr = &stringContent
		return nil
	}

	var arrayContent []AnthropicContentBlock
	if err := sonic.Unmarshal(data, &arrayContent); err == nil {
		mc.ContentBlocks = arrayContent
		return nil
	}

	var singleBlock AnthropicContentBlock
	if err := sonic.Unmarshal(data, &singleBlock); err == nil && singleBlock.Type != "" {
		mc.ContentBlocks = []AnthropicContentBlock{singleBlock}
		return nil
	}

	return fmt.Errorf("content field is neither a string nor an array of ContentBlock")
}

const (
	AnthropicContentBlockTypeText                              AnthropicContentBlockType = "text"
	AnthropicContentBlockTypeImage                             AnthropicContentBlockType = "image"
	AnthropicContentBlockTypeDocument                          AnthropicContentBlockType = "document"
	AnthropicContentBlockTypeSearchResult                      AnthropicContentBlockType = "search_result"
	AnthropicContentBlockTypeToolUse                           AnthropicContentBlockType = "tool_use"
	AnthropicContentBlockTypeServerToolUse                     AnthropicContentBlockType = "server_tool_use"
	AnthropicContentBlockTypeToolResult                        AnthropicContentBlockType = "tool_result"
	AnthropicContentBlockTypeWebSearchToolResult               AnthropicContentBlockType = "web_search_tool_result"
	AnthropicContentBlockTypeWebSearchToolResultError          AnthropicContentBlockType = "web_search_tool_result_error"
	AnthropicContentBlockTypeWebSearchResult                   AnthropicContentBlockType = "web_search_result"
	AnthropicContentBlockTypeWebFetchToolResult                AnthropicContentBlockType = "web_fetch_tool_result"
	AnthropicContentBlockTypeCodeExecutionToolResult           AnthropicContentBlockType = "code_execution_tool_result"
	AnthropicContentBlockTypeBashCodeExecutionToolResult       AnthropicContentBlockType = "bash_code_execution_tool_result"
	AnthropicContentBlockTypeTextEditorCodeExecutionToolResult AnthropicContentBlockType = "text_editor_code_execution_tool_result"
	AnthropicContentBlockTypeToolSearchToolResult              AnthropicContentBlockType = "tool_search_tool_result"
	AnthropicContentBlockTypeToolSearchToolSearchResult        AnthropicContentBlockType = "tool_search_tool_search_result"
	AnthropicContentBlockTypeToolReference                     AnthropicContentBlockType = "tool_reference"
	AnthropicContentBlockTypeContainerUpload                   AnthropicContentBlockType = "container_upload"
	AnthropicContentBlockTypeAdvisorToolResult                 AnthropicContentBlockType = "advisor_tool_result"
	AnthropicContentBlockTypeMCPToolUse                        AnthropicContentBlockType = "mcp_tool_use"
	AnthropicContentBlockTypeMCPToolResult                     AnthropicContentBlockType = "mcp_tool_result"
	AnthropicContentBlockTypeThinking                          AnthropicContentBlockType = "thinking"
	AnthropicContentBlockTypeRedactedThinking                  AnthropicContentBlockType = "redacted_thinking"
	AnthropicContentBlockTypeCompaction                        AnthropicContentBlockType = "compaction"
	AnthropicContentBlockTypeFallback                          AnthropicContentBlockType = "fallback"
	AnthropicContentBlockTypeToolAddition                      AnthropicContentBlockType = "tool_addition"
	AnthropicContentBlockTypeToolRemoval                       AnthropicContentBlockType = "tool_removal"

	AnthropicContentBlockTypeCodeExecutionResult                AnthropicContentBlockType = "code_execution_result"
	AnthropicContentBlockTypeEncryptedCodeExecutionResult       AnthropicContentBlockType = "encrypted_code_execution_result"
	AnthropicContentBlockTypeBashCodeExecutionResult            AnthropicContentBlockType = "bash_code_execution_result"
	AnthropicContentBlockTypeTextEditorCodeExecutionResult      AnthropicContentBlockType = "text_editor_code_execution_result"
	AnthropicContentBlockTypeCodeExecutionToolResultError       AnthropicContentBlockType = "code_execution_tool_result_error"
	AnthropicContentBlockTypeBashCodeExecutionToolResultError   AnthropicContentBlockType = "bash_code_execution_tool_result_error"
	AnthropicContentBlockTypeTextEditorCodeExecutionResultError AnthropicContentBlockType = "text_editor_code_execution_tool_result_error"

	AnthropicContentBlockTypeCodeExecutionOutput     AnthropicContentBlockType = "code_execution_output"
	AnthropicContentBlockTypeBashCodeExecutionOutput AnthropicContentBlockType = "bash_code_execution_output"
)

const (
	AnthropicToolChangeTargetTypeToolReference       = "tool_reference"
	AnthropicToolChangeTargetTypeMCPToolReference    = "mcp_tool_reference"
	AnthropicToolChangeTargetTypeMCPToolsetReference = "mcp_toolset_reference"
	AnthropicToolChangeTargetTypeToolDefinition      = "tool_definition"
)

func (b *AnthropicContentBlock) DiscoveredToolReferences() []AnthropicContentBlock {
	if b == nil {
		return nil
	}
	if len(b.ToolReferences) > 0 {
		return b.ToolReferences
	}
	if b.Content == nil {
		return nil
	}
	if b.Content.ContentObj != nil && len(b.Content.ContentObj.ToolReferences) > 0 {
		return b.Content.ContentObj.ToolReferences
	}
	for _, inner := range b.Content.ContentBlocks {
		if len(inner.ToolReferences) > 0 {
			return inner.ToolReferences
		}
	}
	return nil
}

func (s AnthropicBlockSource) MarshalJSON() ([]byte, error) {
	if s.SourceStr != nil && s.SourceObj != nil {
		return nil, fmt.Errorf("both SourceStr and SourceObj are set; only one should be non-nil")
	}
	if s.SourceStr != nil {
		return providerUtils.MarshalSorted(*s.SourceStr)
	}
	if s.SourceObj != nil {
		return providerUtils.MarshalSorted(s.SourceObj)
	}
	return providerUtils.MarshalSorted(nil)
}

func (s *AnthropicBlockSource) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		s.SourceStr = nil
		s.SourceObj = nil
		return nil
	}
	var str string
	if err := sonic.Unmarshal(data, &str); err == nil {
		s.SourceStr = &str
		s.SourceObj = nil
		return nil
	}
	var obj AnthropicSource
	if err := sonic.Unmarshal(data, &obj); err == nil {
		s.SourceStr = nil
		s.SourceObj = &obj
		return nil
	}
	return fmt.Errorf("source field is neither a string nor an AnthropicSource object")
}

const (
	AnthropicCitationTypeCharLocation            AnthropicCitationType = "char_location"
	AnthropicCitationTypePageLocation            AnthropicCitationType = "page_location"
	AnthropicCitationTypeContentBlockLocation    AnthropicCitationType = "content_block_location"
	AnthropicCitationTypeWebSearchResultLocation AnthropicCitationType = "web_search_result_location"
	AnthropicCitationTypeSearchResultLocation    AnthropicCitationType = "search_result_location"
)

func (ac *AnthropicCitations) MarshalJSON() ([]byte, error) {
	if len(ac.TextCitations) == 0 {
		ac.TextCitations = nil
	}
	if ac.Config != nil && ac.TextCitations != nil {
		return nil, fmt.Errorf("both Config and TextCitations are set; only one should be non-nil")
	}

	if ac.Config != nil {
		return providerUtils.MarshalSorted(ac.Config)
	}
	if ac.TextCitations != nil {
		return providerUtils.MarshalSorted(ac.TextCitations)
	}
	return providerUtils.MarshalSorted(nil)
}

func (ac *AnthropicCitations) UnmarshalJSON(data []byte) error {

	var textCitations []AnthropicTextCitation
	if err := sonic.Unmarshal(data, &textCitations); err == nil {
		ac.Config = nil
		ac.TextCitations = textCitations
		return nil
	}

	var config schemas.Citations
	if err := sonic.Unmarshal(data, &config); err == nil {
		ac.TextCitations = nil
		ac.Config = &config
		return nil
	}

	return fmt.Errorf("citations field is neither a config object nor an array of citations")
}

const (
	AnthropicToolTypeCustom             AnthropicToolType = "custom"
	AnthropicToolTypeBash20241022       AnthropicToolType = "bash_20241022"
	AnthropicToolTypeBash20250124       AnthropicToolType = "bash_20250124"
	AnthropicToolTypeComputer20241022   AnthropicToolType = "computer_20241022"
	AnthropicToolTypeComputer20250124   AnthropicToolType = "computer_20250124"
	AnthropicToolTypeComputer20251124   AnthropicToolType = "computer_20251124"
	AnthropicToolTypeTextEditor20250124 AnthropicToolType = "text_editor_20250124"
	AnthropicToolTypeTextEditor20250429 AnthropicToolType = "text_editor_20250429"
	AnthropicToolTypeTextEditor20250728 AnthropicToolType = "text_editor_20250728"

	AnthropicToolTypeComputerToolset20260801 AnthropicToolType = "computer_toolset_20260801"
	AnthropicToolTypeBrowserToolset20260801  AnthropicToolType = "browser_toolset_20260801"

	AnthropicToolTypeCodeExecution20250522 AnthropicToolType = "code_execution_20250522"
	AnthropicToolTypeCodeExecution         AnthropicToolType = "code_execution_20250825"
	AnthropicToolTypeCodeExecution20260120 AnthropicToolType = "code_execution_20260120"
	AnthropicToolTypeCodeExecution20260521 AnthropicToolType = "code_execution_20260521"

	AnthropicToolTypeWebSearch20250305 AnthropicToolType = "web_search_20250305"
	AnthropicToolTypeWebSearch20260209 AnthropicToolType = "web_search_20260209"

	AnthropicToolTypeWebFetch20250910 AnthropicToolType = "web_fetch_20250910"
	AnthropicToolTypeWebFetch20260209 AnthropicToolType = "web_fetch_20260209"
	AnthropicToolTypeWebFetch20260309 AnthropicToolType = "web_fetch_20260309"
	AnthropicToolTypeWebFetch20260318 AnthropicToolType = "web_fetch_20260318"

	AnthropicToolTypeMemory20250818 AnthropicToolType = "memory_20250818"

	AnthropicToolTypeToolSearchBM25          AnthropicToolType = "tool_search_tool_bm25"
	AnthropicToolTypeToolSearchBM2520251119  AnthropicToolType = "tool_search_tool_bm25_20251119"
	AnthropicToolTypeToolSearchRegex         AnthropicToolType = "tool_search_tool_regex"
	AnthropicToolTypeToolSearchRegex20251119 AnthropicToolType = "tool_search_tool_regex_20251119"

	AnthropicToolTypeAdvisor20260301 AnthropicToolType = "advisor_20260301"
)

const (
	AnthropicToolNameComputer   AnthropicToolName = "computer"
	AnthropicToolNameWebSearch  AnthropicToolName = "web_search"
	AnthropicToolNameWebFetch   AnthropicToolName = "web_fetch"
	AnthropicToolNameBash       AnthropicToolName = "bash"
	AnthropicToolNameTextEditor AnthropicToolName = "str_replace_based_edit_tool"

	AnthropicToolNameTextEditorLegacy AnthropicToolName = "str_replace_editor"
	AnthropicToolNameCodeExecution    AnthropicToolName = "code_execution"

	AnthropicToolNameBashCodeExecution       AnthropicToolName = "bash_code_execution"
	AnthropicToolNameTextEditorCodeExecution AnthropicToolName = "text_editor_code_execution"
	AnthropicToolNameMemory                  AnthropicToolName = "memory"
	AnthropicToolNameToolSearchBM25          AnthropicToolName = "tool_search_tool_bm25"
	AnthropicToolNameToolSearchRegex         AnthropicToolName = "tool_search_tool_regex"
	AnthropicToolNameAdvisor                 AnthropicToolName = "advisor"
)

func (t AnthropicTool) MarshalJSON() ([]byte, error) {
	if t.MCPToolset != nil {
		return providerUtils.MarshalSorted(t.MCPToolset)
	}

	type Alias AnthropicTool
	data, err := providerUtils.MarshalSorted((Alias)(t))
	if err != nil {
		return nil, err
	}

	maxUses, allowed, blocked := t.sharedServerToolFields()
	if maxUses != nil {
		if data, err = sjson.SetBytes(data, "max_uses", *maxUses); err != nil {
			return nil, err
		}
	}
	if len(allowed) > 0 {
		if data, err = sjson.SetBytes(data, "allowed_domains", allowed); err != nil {
			return nil, err
		}
	}
	if len(blocked) > 0 {
		if data, err = sjson.SetBytes(data, "blocked_domains", blocked); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (t AnthropicTool) sharedServerToolFields() (maxUses *int, allowed, blocked []string) {
	switch {
	case t.AnthropicToolWebSearch != nil:
		return t.AnthropicToolWebSearch.MaxUses, t.AnthropicToolWebSearch.AllowedDomains, t.AnthropicToolWebSearch.BlockedDomains
	case t.AnthropicToolWebFetch != nil:
		return t.AnthropicToolWebFetch.MaxUses, t.AnthropicToolWebFetch.AllowedDomains, t.AnthropicToolWebFetch.BlockedDomains
	case t.AnthropicToolAdvisor != nil:
		return t.AnthropicToolAdvisor.MaxUses, nil, nil
	}
	return nil, nil, nil
}

func (t *AnthropicTool) UnmarshalJSON(data []byte) error {

	var peek struct {
		Type string `json:"type"`
	}
	if err := sonic.Unmarshal(data, &peek); err == nil && peek.Type == "mcp_toolset" {
		var toolset AnthropicMCPToolsetTool
		if err := sonic.Unmarshal(data, &toolset); err != nil {
			return err
		}
		t.MCPToolset = &toolset
		return nil
	}

	type Alias AnthropicTool
	if err := sonic.Unmarshal(data, (*Alias)(t)); err != nil {
		return err
	}

	var shared struct {
		MaxUses        *int     `json:"max_uses"`
		AllowedDomains []string `json:"allowed_domains"`
		BlockedDomains []string `json:"blocked_domains"`
	}
	if err := sonic.Unmarshal(data, &shared); err != nil {
		return err
	}
	t.applySharedServerToolFields(shared.MaxUses, shared.AllowedDomains, shared.BlockedDomains)
	return nil
}

func (t *AnthropicTool) applySharedServerToolFields(maxUses *int, allowed, blocked []string) {
	if t.Type == nil || (maxUses == nil && allowed == nil && blocked == nil) {
		return
	}
	switch typeStr := string(*t.Type); {
	case strings.HasPrefix(typeStr, "web_search"):
		if t.AnthropicToolWebSearch == nil {
			t.AnthropicToolWebSearch = &AnthropicToolWebSearch{}
		}
		t.AnthropicToolWebSearch.MaxUses = maxUses
		t.AnthropicToolWebSearch.AllowedDomains = allowed
		t.AnthropicToolWebSearch.BlockedDomains = blocked
	case strings.HasPrefix(typeStr, "web_fetch"):
		if t.AnthropicToolWebFetch == nil {
			t.AnthropicToolWebFetch = &AnthropicToolWebFetch{}
		}
		t.AnthropicToolWebFetch.MaxUses = maxUses
		t.AnthropicToolWebFetch.AllowedDomains = allowed
		t.AnthropicToolWebFetch.BlockedDomains = blocked
	case strings.HasPrefix(typeStr, "advisor"):
		if t.AnthropicToolAdvisor == nil {
			t.AnthropicToolAdvisor = &AnthropicToolAdvisor{}
		}
		t.AnthropicToolAdvisor.MaxUses = maxUses
	}
}

const (
	AnthropicStopReasonEndTurn                    AnthropicStopReason = "end_turn"
	AnthropicStopReasonMaxTokens                  AnthropicStopReason = "max_tokens"
	AnthropicStopReasonStopSequence               AnthropicStopReason = "stop_sequence"
	AnthropicStopReasonToolUse                    AnthropicStopReason = "tool_use"
	AnthropicStopReasonPauseTurn                  AnthropicStopReason = "pause_turn"
	AnthropicStopReasonRefusal                    AnthropicStopReason = "refusal"
	AnthropicStopReasonModelContextWindowExceeded AnthropicStopReason = "model_context_window_exceeded"
	AnthropicStopReasonCompaction                 AnthropicStopReason = "compaction"
)

func (r AnthropicStopReason) MarshalJSON() ([]byte, error) {
	if r == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(r))
}

const AnthropicUsageIterationTypeFallbackMessage = "fallback_message"

const AnthropicUsageIterationTypeCompaction = "compaction"

func billableAnthropicUsage(u *AnthropicUsage) *AnthropicUsage {
	if u == nil {
		return nil
	}
	if len(u.Iterations) == 0 {
		return u
	}
	out := *u
	out.Type = nil
	out.Model = nil
	out.Iterations = nil
	if u.OutputTokensDetails != nil {
		details := *u.OutputTokensDetails
		out.OutputTokensDetails = &details
	}
	if u.ServerToolUse != nil {
		serverToolUse := *u.ServerToolUse
		out.ServerToolUse = &serverToolUse
	}
	for i := range u.Iterations {
		it := &u.Iterations[i]
		if it.Type == nil || *it.Type != AnthropicUsageIterationTypeCompaction {
			continue
		}
		addAnthropicUsageTokenFields(&out, it)
	}
	return &out
}

func addAnthropicUsageTokenFields(dst, src *AnthropicUsage) {
	if dst == nil || src == nil {
		return
	}
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadInputTokens += src.CacheReadInputTokens
	dst.CacheCreationInputTokens += src.CacheCreationInputTokens
	dst.CacheCreation.Ephemeral5mInputTokens += src.CacheCreation.Ephemeral5mInputTokens
	dst.CacheCreation.Ephemeral1hInputTokens += src.CacheCreation.Ephemeral1hInputTokens
	if src.OutputTokensDetails != nil && src.OutputTokensDetails.ThinkingTokens > 0 {
		if dst.OutputTokensDetails == nil {
			dst.OutputTokensDetails = &AnthropicOutputTokensDetails{}
		}
		if src.OutputTokensDetails.ThinkingTokens > dst.OutputTokensDetails.ThinkingTokens {
			dst.OutputTokensDetails.ThinkingTokens = src.OutputTokensDetails.ThinkingTokens
		}
	}
	if src.ServerToolUse != nil {
		if dst.ServerToolUse == nil {
			dst.ServerToolUse = &AnthropicServerToolUseUsage{}
		}
		if src.ServerToolUse.WebSearchRequests > dst.ServerToolUse.WebSearchRequests {
			dst.ServerToolUse.WebSearchRequests = src.ServerToolUse.WebSearchRequests
		}
	}
}

func (u *AnthropicUsage) ServerSideFallbackModel() *string {
	if u == nil {
		return nil
	}
	var served *string
	for i := range u.Iterations {
		it := u.Iterations[i]
		if it.Type == nil || *it.Type != AnthropicUsageIterationTypeFallbackMessage {
			continue
		}
		if it.Model != nil && *it.Model != "" {
			m := *it.Model
			served = &m
		}
	}
	return served
}

const (
	AnthropicStreamEventTypeMessageStart      AnthropicStreamEventType = "message_start"
	AnthropicStreamEventTypeMessageStop       AnthropicStreamEventType = "message_stop"
	AnthropicStreamEventTypeContentBlockStart AnthropicStreamEventType = "content_block_start"
	AnthropicStreamEventTypeContentBlockDelta AnthropicStreamEventType = "content_block_delta"
	AnthropicStreamEventTypeContentBlockStop  AnthropicStreamEventType = "content_block_stop"
	AnthropicStreamEventTypeMessageDelta      AnthropicStreamEventType = "message_delta"
	AnthropicStreamEventTypePing              AnthropicStreamEventType = "ping"
	AnthropicStreamEventTypeSafeguardsUpdate  AnthropicStreamEventType = "safeguards_update"
	AnthropicStreamEventTypeError             AnthropicStreamEventType = "error"
)

const (
	AnthropicStreamDeltaTypeText       AnthropicStreamDeltaType = "text_delta"
	AnthropicStreamDeltaTypeInputJSON  AnthropicStreamDeltaType = "input_json_delta"
	AnthropicStreamDeltaTypeThinking   AnthropicStreamDeltaType = "thinking_delta"
	AnthropicStreamDeltaTypeSignature  AnthropicStreamDeltaType = "signature_delta"
	AnthropicStreamDeltaTypeCitations  AnthropicStreamDeltaType = "citations_delta"
	AnthropicStreamDeltaTypeCompaction AnthropicStreamDeltaType = "compaction_delta"
)

func (r *AnthropicFileResponse) ToBifrostFileUploadResponse(latency time.Duration, sendBackRawRequest bool, sendBackRawResponse bool, rawRequest interface{}, rawResponse interface{}) *schemas.BifrostFileUploadResponse {
	resp := &schemas.BifrostFileUploadResponse{
		ID:             r.ID,
		Object:         r.Type,
		Bytes:          r.SizeBytes,
		CreatedAt:      parseAnthropicFileTimestamp(r.CreatedAt),
		Filename:       r.Filename,
		ContentType:    r.MimeType,
		Downloadable:   r.Downloadable,
		Purpose:        schemas.FilePurposeBatch,
		Status:         schemas.FileStatusProcessed,
		StorageBackend: schemas.FileStorageAPI,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if sendBackRawRequest {
		resp.ExtraFields.RawRequest = rawRequest
	}

	if sendBackRawResponse {
		resp.ExtraFields.RawResponse = rawResponse
	}

	return resp
}

func (r *AnthropicFileResponse) ToBifrostFileRetrieveResponse(latency time.Duration, sendBackRawRequest bool, sendBackRawResponse bool, rawRequest interface{}, rawResponse interface{}) *schemas.BifrostFileRetrieveResponse {
	resp := &schemas.BifrostFileRetrieveResponse{
		ID:             r.ID,
		Object:         r.Type,
		Bytes:          r.SizeBytes,
		CreatedAt:      parseAnthropicFileTimestamp(r.CreatedAt),
		Filename:       r.Filename,
		ContentType:    r.MimeType,
		Downloadable:   r.Downloadable,
		Purpose:        schemas.FilePurposeBatch,
		Status:         schemas.FileStatusProcessed,
		StorageBackend: schemas.FileStorageAPI,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if sendBackRawRequest {
		resp.ExtraFields.RawRequest = rawRequest
	}

	if sendBackRawResponse {
		resp.ExtraFields.RawResponse = rawResponse
	}

	return resp
}

func parseAnthropicFileTimestamp(timestamp string) int64 {
	if timestamp == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return 0
	}
	return t.Unix()
}
