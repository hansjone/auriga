package schemas

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
)

type BifrostCacheMetadata struct {
	CacheHit bool `json:"cache_hit"`

	CacheID *string `json:"cache_id,omitempty"`
	HitType *string `json:"hit_type,omitempty"`

	RequestedProvider *string `json:"requested_provider,omitempty"`
	RequestedModel    *string `json:"requested_model,omitempty"`

	ProviderUsed *string `json:"provider_used,omitempty"`
	ModelUsed    *string `json:"model_used,omitempty"`
	InputTokens  *int    `json:"input_tokens,omitempty"`

	Threshold  *float64 `json:"threshold,omitempty"`
	Similarity *float64 `json:"similarity,omitempty"`

	CacheHitLatency *int64 `json:"cache_hit_latency,omitempty"`
}
type BifrostConfig struct {
	Account    Account
	LLMPlugins []LLMPlugin

	Logger             Logger
	Tracer             Tracer
	InitialPoolSize    int
	DropExcessRequests bool

	KeySelector     KeySelector
	KeyPoolFilter   KeyPoolFilter
	KVStore         KVStore
	SessionAffinity SessionAffinity
	ModelCatalog    ModelInfoProvider
}
type BifrostResponse struct {
	ListModelsResponse            *BifrostListModelsResponse
	ModelRetrieveResponse         *BifrostModelRetrieveResponse
	TextCompletionResponse        *BifrostTextCompletionResponse
	ChatResponse                  *BifrostChatResponse
	ResponsesResponse             *BifrostResponsesResponse
	ResponsesStreamResponse       *BifrostResponsesStreamResponse
	ResponsesDeleteResponse       *BifrostResponsesDeleteResponse
	ResponsesInputItemsResponse   *BifrostResponsesInputItemsResponse
	CountTokensResponse           *BifrostCountTokensResponse
	CompactionResponse            *BifrostCompactionResponse
	EmbeddingResponse             *BifrostEmbeddingResponse
	RerankResponse                *BifrostRerankResponse
	DecisionResponse              *BifrostDecisionResponse
	OCRResponse                   *BifrostOCRResponse
	SpeechResponse                *BifrostSpeechResponse
	SpeechStreamResponse          *BifrostSpeechStreamResponse
	TranscriptionResponse         *BifrostTranscriptionResponse
	TranscriptionStreamResponse   *BifrostTranscriptionStreamResponse
	ImageGenerationResponse       *BifrostImageGenerationResponse
	ImageGenerationStreamResponse *BifrostImageGenerationStreamResponse
	VideoGenerationResponse       *BifrostVideoGenerationResponse
	VideoDownloadResponse         *BifrostVideoDownloadResponse
	VideoListResponse             *BifrostVideoListResponse
	LiveSession                   *LiveSessionLog
	VideoDeleteResponse           *BifrostVideoDeleteResponse
	FileUploadResponse            *BifrostFileUploadResponse
	FileListResponse              *BifrostFileListResponse
	FileRetrieveResponse          *BifrostFileRetrieveResponse
	FileDeleteResponse            *BifrostFileDeleteResponse
	FileContentResponse           *BifrostFileContentResponse
	LiveContentResponse           *LiveContentResponse
	CachedContentCreateResponse   *BifrostCachedContentCreateResponse
	CachedContentListResponse     *BifrostCachedContentListResponse
	CachedContentRetrieveResponse *BifrostCachedContentRetrieveResponse
	CachedContentUpdateResponse   *BifrostCachedContentUpdateResponse
	CachedContentDeleteResponse   *BifrostCachedContentDeleteResponse
	BatchCreateResponse           *BifrostBatchCreateResponse
	BatchListResponse             *BifrostBatchListResponse
	BatchRetrieveResponse         *BifrostBatchRetrieveResponse
	BatchCancelResponse           *BifrostBatchCancelResponse
	BatchResultsResponse          *BifrostBatchResultsResponse
	BatchDeleteResponse           *BifrostBatchDeleteResponse
	ContainerCreateResponse       *BifrostContainerCreateResponse
	ContainerListResponse         *BifrostContainerListResponse
	ContainerRetrieveResponse     *BifrostContainerRetrieveResponse
	ContainerDeleteResponse       *BifrostContainerDeleteResponse
	ContainerFileCreateResponse   *BifrostContainerFileCreateResponse
	ContainerFileListResponse     *BifrostContainerFileListResponse
	ContainerFileRetrieveResponse *BifrostContainerFileRetrieveResponse
	ContainerFileContentResponse  *BifrostContainerFileContentResponse
	ContainerFileDeleteResponse   *BifrostContainerFileDeleteResponse
	PassthroughResponse           *BifrostPassthroughResponse
}
type BifrostMCPCompleteResponse struct {
	Completion *mcp.CompleteResult
}
type BifrostMCPResponseExtraFields struct {
	MCPRequestType MCPRequestType `json:"mcp_request_type"`
	ClientName     string         `json:"client_name"`
	ToolName       string         `json:"tool_name"`
	Latency        int64          `json:"latency"`
}
type BifrostMCPExecuteToolResponse struct {
}
type MCPServerCapabilities struct {
	Tools     bool `json:"tools"`
	Resources bool `json:"resources"`
	Prompts   bool `json:"prompts"`
	Logging   bool `json:"logging"`
}
type BifrostRoutingMetadata struct {
	Calls []BifrostRoutingCall `json:"calls,omitempty"`
}
type BifrostStreamChunk struct {
	*BifrostTextCompletionResponse
	*BifrostChatResponse
	*BifrostResponsesStreamResponse
	*BifrostSpeechStreamResponse
	*BifrostTranscriptionStreamResponse
	*BifrostImageGenerationStreamResponse
	*BifrostPassthroughResponse
	*BifrostError
}
type BifrostMCPPingResponse struct {
}
type PluginLogEntry struct {
	PluginName string   `json:"plugin_name"`
	Level      LogLevel `json:"level"`
	Message    string   `json:"message"`
	Timestamp  int64    `json:"timestamp"`
}
type BifrostMCPReadResourceResponse struct {
	Resource *mcp.ReadResourceResult
}
type BifrostRoutingCall struct {
	RequestType  RequestType `json:"request_type,omitempty"`
	ProviderUsed *string     `json:"provider_used,omitempty"`
	ModelUsed    *string     `json:"model_used,omitempty"`
	InputTokens  *int        `json:"input_tokens,omitempty"`

	OutputTokens *int `json:"output_tokens,omitempty"`

	CountTowardBudgets bool `json:"count_toward_budgets,omitempty"`
}
type BifrostErrorExtraFields struct {
	RoutingInfo RoutingInfo `json:"routing_info"`

	Provider ModelProvider `json:"provider,omitempty"`

	OriginalModelRequested string `json:"original_model_requested,omitempty"`

	ResolvedModelUsed string         `json:"resolved_model_used,omitempty"`
	RequestType       RequestType    `json:"request_type,omitempty"`
	MCPRequestType    MCPRequestType `json:"mcp_request_type,omitempty"`

	A2ARequestType            A2ARequestType        `json:"a2a_request_type,omitempty"`
	A2AAgentName              string                `json:"a2a_agent_name,omitempty"`
	RawRequest                interface{}           `json:"raw_request,omitempty"`
	RawResponse               interface{}           `json:"raw_response,omitempty"`
	ConvertedRequestType      RequestType           `json:"converted_request_type,omitempty"`
	DroppedCompatPluginParams []string              `json:"dropped_compat_plugin_params,omitempty"`
	Latency                   int64                 `json:"latency,omitempty"`
	RetryAfter                int64                 `json:"retry_after_ms,omitempty"`
	KeyStatuses               []KeyStatus           `json:"key_statuses,omitempty"`
	MCPAuthRequired           *MCPAuthRequiredError `json:"mcp_auth_required,omitempty"`

	BilledUsage         *BifrostLLMUsage `json:"billed_usage,omitempty"`
	NativeErrorResponse json.RawMessage  `json:"-"`

	ErrorType ErrorType `json:"error_type,omitempty"`
}
type MCPRequestType string
type LargePayloadMetadata struct {
	ResponseModalities []string
	SpeechConfig       bool
	Model              string
	StreamRequested    *bool
	ThreadType         string
}
type SkippedMCPTool struct {
	OriginalName string `json:"original_name"`
	Reason       string `json:"reason"`
}
type BifrostMCPResponse struct {
	*BifrostMCPPingResponse
	*BifrostMCPListToolsResponse
	*BifrostMCPListResourcesResponse
	*BifrostMCPListResourceTemplatesResponse
	*BifrostMCPListPromptsResponse
	*BifrostMCPGetPromptResponse
	*BifrostMCPReadResourceResponse
	*BifrostMCPCompleteResponse

	ChatMessage      *ChatMessage
	ResponsesMessage *ResponsesMessage

	*BifrostMCPExecuteToolResponse

	ExtraFields BifrostMCPResponseExtraFields
}
type BifrostRequest struct {
	RequestType RequestType

	ListModelsRequest            *BifrostListModelsRequest
	ModelRetrieveRequest         *BifrostModelRetrieveRequest
	TextCompletionRequest        *BifrostTextCompletionRequest
	ChatRequest                  *BifrostChatRequest
	ResponsesRequest             *BifrostResponsesRequest
	ResponsesRetrieveRequest     *BifrostResponsesRetrieveRequest
	ResponsesDeleteRequest       *BifrostResponsesDeleteRequest
	ResponsesCancelRequest       *BifrostResponsesCancelRequest
	ResponsesInputItemsRequest   *BifrostResponsesInputItemsRequest
	CountTokensRequest           *BifrostResponsesRequest
	CompactionRequest            *BifrostCompactionRequest
	EmbeddingRequest             *BifrostEmbeddingRequest
	RerankRequest                *BifrostRerankRequest
	DecisionRequest              *BifrostDecisionRequest
	OCRRequest                   *BifrostOCRRequest
	SpeechRequest                *BifrostSpeechRequest
	TranscriptionRequest         *BifrostTranscriptionRequest
	ImageGenerationRequest       *BifrostImageGenerationRequest
	ImageEditRequest             *BifrostImageEditRequest
	ImageVariationRequest        *BifrostImageVariationRequest
	VideoGenerationRequest       *BifrostVideoGenerationRequest
	VideoEditRequest             *BifrostVideoEditRequest
	VideoRetrieveRequest         *BifrostVideoRetrieveRequest
	VideoDownloadRequest         *BifrostVideoDownloadRequest
	VideoListRequest             *BifrostVideoListRequest
	VideoRemixRequest            *BifrostVideoRemixRequest
	VideoDeleteRequest           *BifrostVideoDeleteRequest
	FileUploadRequest            *BifrostFileUploadRequest
	FileListRequest              *BifrostFileListRequest
	FileRetrieveRequest          *BifrostFileRetrieveRequest
	FileDeleteRequest            *BifrostFileDeleteRequest
	FileContentRequest           *BifrostFileContentRequest
	LiveContentRequest           *BifrostLiveContentRequest
	CachedContentCreateRequest   *BifrostCachedContentCreateRequest
	CachedContentListRequest     *BifrostCachedContentListRequest
	CachedContentRetrieveRequest *BifrostCachedContentRetrieveRequest
	CachedContentUpdateRequest   *BifrostCachedContentUpdateRequest
	CachedContentDeleteRequest   *BifrostCachedContentDeleteRequest
	BatchCreateRequest           *BifrostBatchCreateRequest
	BatchListRequest             *BifrostBatchListRequest
	BatchRetrieveRequest         *BifrostBatchRetrieveRequest
	BatchCancelRequest           *BifrostBatchCancelRequest
	BatchResultsRequest          *BifrostBatchResultsRequest
	BatchDeleteRequest           *BifrostBatchDeleteRequest
	ContainerCreateRequest       *BifrostContainerCreateRequest
	ContainerListRequest         *BifrostContainerListRequest
	ContainerRetrieveRequest     *BifrostContainerRetrieveRequest
	ContainerDeleteRequest       *BifrostContainerDeleteRequest
	ContainerFileCreateRequest   *BifrostContainerFileCreateRequest
	ContainerFileListRequest     *BifrostContainerFileListRequest
	ContainerFileRetrieveRequest *BifrostContainerFileRetrieveRequest
	ContainerFileContentRequest  *BifrostContainerFileContentRequest
	ContainerFileDeleteRequest   *BifrostContainerFileDeleteRequest
	PassthroughRequest           *BifrostPassthroughRequest
}
type MCPServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type BifrostMCPListPromptsResponse struct {
	Prompts []mcp.Prompt
}
type BifrostError struct {
	EventID        *string                 `json:"event_id,omitempty"`
	Type           *string                 `json:"type,omitempty"`
	IsBifrostError bool                    `json:"is_bifrost_error"`
	StatusCode     *int                    `json:"status_code,omitempty"`
	Error          *ErrorField             `json:"error"`
	AllowFallbacks *bool                   `json:"-"`
	StreamControl  *StreamControl          `json:"-"`
	ExtraFields    BifrostErrorExtraFields `json:"extra_fields"`
}
type BifrostMCPGetPromptResponse struct {
	Prompt *mcp.GetPromptResult
}
type Fallback struct {
	Provider ModelProvider `json:"provider"`
	Model    string        `json:"model"`
	KeyID    string        `json:"key_id,omitempty"`
	Inferred bool          `json:"-"`
}
type RoutingEngineLogEntry struct {
	Engine    string   `json:"engine"`
	Level     LogLevel `json:"level"`
	Message   string   `json:"message"`
	Timestamp int64    `json:"timestamp"`
}
type StreamControl struct {
	LogError   *bool `json:"log_error,omitempty"`
	SkipStream *bool `json:"skip_stream,omitempty"`
}
type RoutingInfo struct {
	Provider ModelProvider `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	Key      string        `json:"key,omitempty"`

	ResolvedKeyAlias *ResolvedKeyAlias `json:"resolved_key_alias,omitempty"`

	IsFallback bool `json:"is_fallback,omitempty"`

	PrimaryProvider *ModelProvider `json:"primary_provider,omitempty"`
	PrimaryModel    *string        `json:"primary_model,omitempty"`

	RequestedProvider ModelProvider `json:"requested_provider,omitempty"`
	RequestedModel    string        `json:"requested_model,omitempty"`

	ServerSideFallbackModel *string `json:"server_side_fallback_model,omitempty"`
}
type ModelProvider string
type PricingSurface string
type BifrostContextKey string
type BifrostMCPListResourcesResponse struct {
	Resources []mcp.Resource
}

var StandardProviders = []ModelProvider{OpenAI, Anthropic, Gemini, MiniMax, DeepSeek}
var streamCommitMarker = &BifrostStreamChunk{}

type KeySelector func(ctx *BifrostContext, keys []Key, providerKey ModelProvider, model string) (Key, error)
type ErrorField struct {
	Type    *string     `json:"type,omitempty"`
	Code    *string     `json:"code,omitempty"`
	Message string      `json:"message"`
	Error   error       `json:"-"`
	Param   interface{} `json:"param,omitempty"`
	EventID *string     `json:"event_id,omitempty"`
}
type BifrostMCPConnectResponse struct {
	ConnectionInfo     *MCPClientConnectionInfo
	ServerInfo         *MCPServerInfo
	ProtocolVersion    string
	ServerCapabilities *MCPServerCapabilities

	Instructions string
	ExtraFields  BifrostMCPResponseExtraFields
}
type KeyPoolFilter func(ctx *BifrostContext, provider ModelProvider, model string, keys []Key) ([]Key, error)
type KeyAttemptRecord struct {
	Attempt           int          `json:"attempt"`
	KeyID             string       `json:"key_id"`
	KeyName           string       `json:"key_name"`
	FailReason        *string      `json:"fail_reason,omitempty"`
	FailureClass      FailureClass `json:"failure_class,omitempty"`
	StatusCode        *int         `json:"status_code,omitempty"`
	RetryAfter        int64        `json:"retry_after_ms,omitempty"`
	TriggeredRotation bool         `json:"triggered_rotation"`
}
type BifrostMCPListResourceTemplatesResponse struct {
	ResourceTemplates []mcp.ResourceTemplate
}
type RequestType string
type ResolvedKeyAlias struct {
	ModelID     string       `json:"model_id"`
	ModelName   *string      `json:"model_name,omitempty"`
	ModelFamily *ModelFamily `json:"model_family,omitempty"`
}
type BifrostMCPListToolsResponse struct {
	Tools           map[string]ChatTool
	ToolNameMapping map[string]string
	RawToolCount    int
	SkippedTools    []SkippedMCPTool
}

var SupportedBaseProviders = []ModelProvider{OpenAI, Anthropic, Gemini, MiniMax, DeepSeek}

type BifrostResponseExtraFields struct {
	RequestType RequestType `json:"request_type"`

	PricingRequestType RequestType `json:"pricing_request_type,omitempty"`
	RoutingInfo        RoutingInfo `json:"routing_info"`

	PricingSurface PricingSurface `json:"pricing_surface,omitempty"`

	Provider ModelProvider `json:"provider,omitempty"`

	OriginalModelRequested string `json:"original_model_requested,omitempty"`

	ResolvedModelUsed string `json:"resolved_model_used,omitempty"`
	Latency           int64  `json:"latency"`

	UpstreamLatency *int64 `json:"upstream_latency,omitempty"`

	OverheadLatency *int64      `json:"-"`
	ChunkIndex      int         `json:"chunk_index"`
	RawRequest      interface{} `json:"raw_request,omitempty"`
	RawResponse     interface{} `json:"raw_response,omitempty"`

	CacheDebug                *BifrostCacheMetadata     `json:"cache_debug,omitempty"`
	RoutingMetadata           *BifrostRoutingMetadata   `json:"routing_metadata,omitempty"`
	GuardrailDebug            *BifrostGuardrailMetadata `json:"guardrail_debug,omitempty"`
	ParseErrors               []BatchError              `json:"parse_errors,omitempty"`
	ConvertedRequestType      RequestType               `json:"converted_request_type,omitempty"`
	DroppedCompatPluginParams []string                  `json:"dropped_compat_plugin_params,omitempty"`

	DroppedUnsupportedTools []string          `json:"dropped_unsupported_tools,omitempty"`
	ProviderResponseHeaders map[string]string `json:"provider_response_headers,omitempty"`
	PassthroughPath         string            `json:"passthrough_path,omitempty"`
}

const (
	OpenAI        ModelProvider = "openai"
	Azure         ModelProvider = "azure"
	Anthropic     ModelProvider = "anthropic"
	Bedrock       ModelProvider = "bedrock"
	BedrockMantle ModelProvider = "bedrock_mantle"
	SageMaker     ModelProvider = "sagemaker"
	Cohere        ModelProvider = "cohere"
	Vertex        ModelProvider = "vertex"
	Mistral       ModelProvider = "mistral"
	Ollama        ModelProvider = "ollama"
	OpencodeGo    ModelProvider = "opencode-go"
	OpencodeZen   ModelProvider = "opencode-zen"
	Groq          ModelProvider = "groq"
	SGL           ModelProvider = "sgl"
	Parasail      ModelProvider = "parasail"
	Perplexity    ModelProvider = "perplexity"
	Cerebras      ModelProvider = "cerebras"
	DeepSeek      ModelProvider = "deepseek"
	Gemini        ModelProvider = "gemini"
	OpenRouter    ModelProvider = "openrouter"
	Elevenlabs    ModelProvider = "elevenlabs"
	HuggingFace   ModelProvider = "huggingface"
	Nebius        ModelProvider = "nebius"
	XAI           ModelProvider = "xai"
	Replicate     ModelProvider = "replicate"
	VLLM          ModelProvider = "vllm"
	Runway        ModelProvider = "runway"
	Runware       ModelProvider = "runware"
	Fireworks     ModelProvider = "fireworks"
	Sarvam        ModelProvider = "sarvam"
	Wafer         ModelProvider = "wafer"
	GithubCopilot ModelProvider = "github-copilot"
	Databricks    ModelProvider = "databricks"
	Cloudflare    ModelProvider = "cloudflare"
	Typesafe      ModelProvider = "typesafe"
	MiniMax       ModelProvider = "minimax"
	BytePlus      ModelProvider = "byteplus"
)

func (r RequestType) Value() (driver.Value, error) {
	return string(r), nil
}

const (
	ListModelsRequest              RequestType = "list_models"
	ModelRetrieveRequest           RequestType = "model_retrieve"
	TextCompletionRequest          RequestType = "text_completion"
	TextCompletionStreamRequest    RequestType = "text_completion_stream"
	ChatCompletionRequest          RequestType = "chat_completion"
	ChatCompletionStreamRequest    RequestType = "chat_completion_stream"
	ResponsesRequest               RequestType = "responses"
	ResponsesStreamRequest         RequestType = "responses_stream"
	ResponsesRetrieveRequest       RequestType = "responses_retrieve"
	ResponsesRetrieveStreamRequest RequestType = "responses_retrieve_stream"
	ResponsesDeleteRequest         RequestType = "responses_delete"
	ResponsesCancelRequest         RequestType = "responses_cancel"
	ResponsesInputItemsRequest     RequestType = "responses_input_items"
	EmbeddingRequest               RequestType = "embedding"
	SpeechRequest                  RequestType = "speech"
	SpeechStreamRequest            RequestType = "speech_stream"
	TranscriptionRequest           RequestType = "transcription"
	TranscriptionStreamRequest     RequestType = "transcription_stream"
	ImageGenerationRequest         RequestType = "image_generation"
	ImageGenerationStreamRequest   RequestType = "image_generation_stream"
	ImageEditRequest               RequestType = "image_edit"
	ImageEditStreamRequest         RequestType = "image_edit_stream"
	ImageVariationRequest          RequestType = "image_variation"
	VideoGenerationRequest         RequestType = "video_generation"
	VideoEditRequest               RequestType = "video_edit"
	VideoRetrieveRequest           RequestType = "video_retrieve"
	VideoDownloadRequest           RequestType = "video_download"
	VideoDeleteRequest             RequestType = "video_delete"
	VideoListRequest               RequestType = "video_list"
	VideoRemixRequest              RequestType = "video_remix"
	BatchCreateRequest             RequestType = "batch_create"
	BatchListRequest               RequestType = "batch_list"
	BatchRetrieveRequest           RequestType = "batch_retrieve"
	BatchCancelRequest             RequestType = "batch_cancel"
	BatchResultsRequest            RequestType = "batch_results"
	BatchDeleteRequest             RequestType = "batch_delete"
	FileUploadRequest              RequestType = "file_upload"
	FileListRequest                RequestType = "file_list"
	FileRetrieveRequest            RequestType = "file_retrieve"
	FileDeleteRequest              RequestType = "file_delete"
	CachedContentCreateRequest     RequestType = "cached_content_create"
	CachedContentListRequest       RequestType = "cached_content_list"
	CachedContentRetrieveRequest   RequestType = "cached_content_retrieve"
	CachedContentUpdateRequest     RequestType = "cached_content_update"
	CachedContentDeleteRequest     RequestType = "cached_content_delete"
	FileContentRequest             RequestType = "file_content"
	ContainerCreateRequest         RequestType = "container_create"
	ContainerListRequest           RequestType = "container_list"
	ContainerRetrieveRequest       RequestType = "container_retrieve"
	ContainerDeleteRequest         RequestType = "container_delete"
	ContainerFileCreateRequest     RequestType = "container_file_create"
	ContainerFileListRequest       RequestType = "container_file_list"
	ContainerFileRetrieveRequest   RequestType = "container_file_retrieve"
	ContainerFileContentRequest    RequestType = "container_file_content"
	ContainerFileDeleteRequest     RequestType = "container_file_delete"
	RerankRequest                  RequestType = "rerank"
	DecisionRequest                RequestType = "decisions"
	OCRRequest                     RequestType = "ocr"
	CountTokensRequest             RequestType = "count_tokens"
	CompactionRequest              RequestType = "compaction"
	MCPToolExecutionRequest        RequestType = "mcp_tool_execution"
	RelayRequest                   RequestType = "relay"
	PassthroughRequest             RequestType = "passthrough"
	PassthroughStreamRequest       RequestType = "passthrough_stream"
	UnknownRequest                 RequestType = "unknown"
	WebSocketResponsesRequest      RequestType = "websocket_responses"
	RealtimeRequest                RequestType = "realtime"
	LiveRequest                    RequestType = "live"
	LiveContentRequest             RequestType = "live_content"
	LiveSidebandRequest            RequestType = "live_sideband"
)

const (
	BifrostContextKeySessionToken       BifrostContextKey = "bifrost-session-token"
	BifrostContextKeyAcceptedCredential BifrostContextKey = "bifrost-accepted-credential"
	BifrostContextKeyVirtualKey         BifrostContextKey = "x-bf-vk"
	BifrostContextKeyAPIKeyName         BifrostContextKey = "x-bf-api-key"
	BifrostContextKeyAPIKeyID           BifrostContextKey = "x-bf-api-key-id"
	BifrostContextKeyDirectKey          BifrostContextKey = "x-bf-direct-key"
	BifrostContextKeyRequestID          BifrostContextKey = "request-id"
	BifrostContextKeyFallbackRequestID  BifrostContextKey = "fallback-request-id"
	BifrostContextKeyBillingNonce       BifrostContextKey = "bifrost-billing-nonce"

	MCPContextKeyIncludeClients BifrostContextKey = "mcp-include-clients"
	MCPContextKeyIncludeTools   BifrostContextKey = "mcp-include-tools"

	BifrostContextKeyGovernanceDisableContentLogging BifrostContextKey = "bifrost-governance-disable-content-logging"

	BifrostContextKeySelectedKeyID                       BifrostContextKey = "bifrost-selected-key-id"
	BifrostContextKeySelectedKeyName                     BifrostContextKey = "bifrost-selected-key-name"
	BifrostContextKeyGovernanceRequestProvider           BifrostContextKey = "bifrost-governance-request-provider"
	BifrostContextKeyGovernanceScopedCustomerID          BifrostContextKey = "bifrost-governance-scoped-customer-id"
	BifrostContextKeyGovernanceRoutingRuleID             BifrostContextKey = "bifrost-governance-routing-rule-id"
	BifrostContextKeyGovernanceRoutingRuleName           BifrostContextKey = "bifrost-governance-routing-rule-name"
	BifrostContextKeyGovernanceComplexityTier            BifrostContextKey = "bifrost-governance-complexity-tier"
	BifrostContextKeyGovernanceComplexityMechanism       BifrostContextKey = "bifrost-governance-complexity-mechanism"
	BifrostContextKeyGovernanceComplexityScore           BifrostContextKey = "bifrost-governance-complexity-score"
	BifrostContextKeyRoutingPinnedAPIKeyID               BifrostContextKey = "bifrost-routing-pinned-api-key-id"
	BifrostContextKeySelectedPromptName                  BifrostContextKey = "bifrost-selected-prompt-name"
	BifrostContextKeySelectedPromptVersion               BifrostContextKey = "bifrost-selected-prompt-version"
	BifrostContextKeySelectedPromptID                    BifrostContextKey = "bifrost-selected-prompt-id"
	BifrostContextKeyGovernanceIncludeOnlyKeys           BifrostContextKey = "bf-governance-include-only-keys"
	BifrostContextKeyNumberOfRetries                     BifrostContextKey = "bifrost-number-of-retries"
	BifrostContextKeyFallbackIndex                       BifrostContextKey = "bifrost-fallback-index"
	BifrostContextKeyResolvedAlias                       BifrostContextKey = "bifrost-resolved-alias"
	BifrostContextKeyProviderProxyConfig                 BifrostContextKey = "bifrost-provider-proxy-config"
	BifrostContextKeyRoutingInfo                         BifrostContextKey = "bifrost-routing-info"
	BifrostContextKeyRequestedProvider                   BifrostContextKey = "bifrost-requested-provider"
	BifrostContextKeyRequestedModel                      BifrostContextKey = "bifrost-requested-model"
	BifrostContextKeyStreamEndIndicator                  BifrostContextKey = "bifrost-stream-end-indicator"
	BifrostContextKeyStreamGated                         BifrostContextKey = "bifrost-stream-gated"
	BifrostContextKeyStreamIdleTimeout                   BifrostContextKey = "bifrost-stream-idle-timeout"
	BifrostContextKeyStreamFirstTokenTimeout             BifrostContextKey = "bifrost-stream-first-token-timeout"
	BifrostContextKeyStreamAttemptAbort                  BifrostContextKey = "bifrost-stream-attempt-abort"
	BifrostContextKeyStreamCommitWatch                   BifrostContextKey = "bifrost-stream-commit-watch"
	BifrostContextKeySkipKeySelection                    BifrostContextKey = "bifrost-skip-key-selection"
	BifrostContextKeyExtraHeaders                        BifrostContextKey = "bifrost-extra-headers"
	BifrostContextKeyPassthroughHeaders                  BifrostContextKey = "bifrost-anthropic-passthrough-headers"
	BifrostContextKeyURLPath                             BifrostContextKey = "bifrost-extra-url-path"
	BifrostContextKeyUseRawRequestBody                   BifrostContextKey = "bifrost-use-raw-request-body"
	BifrostContextKeyRawRequestBodyTextRewriter          BifrostContextKey = "bifrost-raw-request-body-text-rewriter"
	BifrostContextKeyRawRequestBodyTextTransformer       BifrostContextKey = "bifrost-raw-request-body-text-transformer"
	BifrostContextKeyRawResponseTextTransformer          BifrostContextKey = "bifrost-raw-response-text-transformer"
	BifrostContextKeyRawStreamTextCodec                  BifrostContextKey = "bifrost-raw-stream-text-codec"
	BifrostContextKeyAnthropicNativeRequestSurface       BifrostContextKey = "bifrost-anthropic-native-request-surface"
	BifrostContextKeyChangeRequestType                   BifrostContextKey = "bifrost-change-request-type"
	BifrostContextKeySendBackRawRequest                  BifrostContextKey = "bifrost-send-back-raw-request"
	BifrostContextKeyPromptCacheAutoInject               BifrostContextKey = "bifrost-prompt-cache-auto-inject"
	BifrostContextKeySendBackRawResponse                 BifrostContextKey = "bifrost-send-back-raw-response"
	BifrostContextKeyIntegrationType                     BifrostContextKey = "bifrost-integration-type"
	BifrostContextKeyIsResponsesToChatCompletionFallback BifrostContextKey = "bifrost-is-responses-to-chat-completion-fallback"
	BifrostMCPAgentOriginalRequestID                     BifrostContextKey = "bifrost-mcp-agent-original-request-id"
	BifrostContextKeyParentMCPRequestID                  BifrostContextKey = "bf-parent-mcp-request-id"
	BifrostContextKeyStructuredOutputToolName            BifrostContextKey = "bifrost-structured-output-tool-name"
	BifrostContextKeyUserAgent                           BifrostContextKey = "bifrost-user-agent"
	BifrostContextKeyApp                                 BifrostContextKey = "app"
	BifrostContextKeySkipBudgetAndRateLimits             BifrostContextKey = "bifrost-skip-budget-and-rate-limits"
	BifrostContextKeySessionContinuation                 BifrostContextKey = "bifrost-session-continuation"
	BifrostContextKeyLiveSessionID                       BifrostContextKey = "bifrost-live-session-id"
	BifrostContextKeyLiveUnit                            BifrostContextKey = "bifrost-live-unit"
	BifrostContextKeyLiveSessionStart                    BifrostContextKey = "bifrost-live-session-start"
	BifrostContextKeyLiveSessionEnd                      BifrostContextKey = "bifrost-live-session-end"
	BifrostContextKeyLiveDelegationID                    BifrostContextKey = "bifrost-live-delegation-id"
	BifrostContextKeyLiveDelegationStartMs               BifrostContextKey = "bifrost-live-delegation-start-ms"
	BifrostContextKeyLiveDelegationFailed                BifrostContextKey = "bifrost-live-delegation-failed"
	BifrostContextKeySkipProviderCheck                   BifrostContextKey = "bifrost-skip-provider-check"
	BifrostContextKeySkipModelCheck                      BifrostContextKey = "bifrost-skip-model-check"
	BifrostContextKeySkipVirtualKeyUsageTracking         BifrostContextKey = "bifrost-skip-virtual-key-usage-tracking"
	BifrostContextKeyAdmitUngrantedUser                  BifrostContextKey = "bifrost-admit-ungranted-user"
	BifrostContextKeyTraceID                             BifrostContextKey = "bifrost-trace-id"
	BifrostContextKeyExportTraceID                       BifrostContextKey = "bifrost-export-trace-id"
	BifrostContextKeySpanID                              BifrostContextKey = "bifrost-span-id"
	BifrostContextKeyParentSpanID                        BifrostContextKey = "bifrost-parent-span-id"
	BifrostContextKeyStreamStartTime                     BifrostContextKey = "bifrost-stream-start-time"
	BifrostContextKeyRequestStartTime                    BifrostContextKey = "bifrost-request-start-time"
	BifrostContextKeyTracer                              BifrostContextKey = "bifrost-tracer"
	BifrostContextKeyModelCatalog                        BifrostContextKey = "bifrost-model-catalog"
	BifrostContextKeyDeferTraceCompletion                BifrostContextKey = "bifrost-defer-trace-completion"
	BifrostContextKeyTraceCompleter                      BifrostContextKey = "bifrost-trace-completer"
	BifrostContextKeyAccumulatorID                       BifrostContextKey = "bifrost-accumulator-id"
	BifrostContextKeyMCPSessionID                        BifrostContextKey = "bifrost-mcp-session-id"
	BifrostContextKeyMCPCallbackBaseURL                  BifrostContextKey = "bifrost-mcp-callback-base-url"
	BifrostContextKeyIsMCPGateway                        BifrostContextKey = "bifrost-is-mcp-gateway"
	BifrostContextKeyHasEmittedMessageDelta              BifrostContextKey = "bifrost-has-emitted-message-delta"
	BifrostContextKeyAnthropicStreamDeltaPromptUsage     BifrostContextKey = "bifrost-anthropic-stream-delta-prompt-usage"
	BifrostContextKeySkipDBUpdate                        BifrostContextKey = "bifrost-skip-db-update"
	BifrostContextKeyGovernancePluginName                BifrostContextKey = "governance-plugin-name"
	BifrostContextKeyClusterNodeID                       BifrostContextKey = "bifrost-cluster-node-id"
	BifrostContextKeyPromptsPluginName                   BifrostContextKey = "prompts-plugin-name"
	BifrostContextKeyIsEnterprise                        BifrostContextKey = "is-enterprise"
	BifrostContextKeyAvailableProviders                  BifrostContextKey = "available-providers"
	BifrostContextKeyStoreRawRequestResponse             BifrostContextKey = "bifrost-store-raw-request-response"
	BifrostContextKeyCaptureRawRequest                   BifrostContextKey = "bifrost-capture-raw-request"
	BifrostContextKeyCaptureRawResponse                  BifrostContextKey = "bifrost-capture-raw-response"
	BifrostContextKeyDropRawRequestFromClient            BifrostContextKey = "bifrost-drop-raw-request-from-client"
	BifrostContextKeyDropRawResponseFromClient           BifrostContextKey = "bifrost-drop-raw-response-from-client"
	BifrostContextKeyShouldStoreRawInLogs                BifrostContextKey = "bifrost-should-store-raw-in-logs"
	BifrostContextKeyRelayContentLoggingEnabledByDefault BifrostContextKey = "bifrost-relay-content-logging-enabled-by-default"
	BifrostContextKeyRelayContentRetainedWhenDisabled    BifrostContextKey = "bifrost-relay-content-retained-when-disabled"
	BifrostContextKeyRetryDBFetch                        BifrostContextKey = "bifrost-retry-db-fetch"
	BifrostContextKeyIsCustomProvider                    BifrostContextKey = "bifrost-is-custom-provider"
	BifrostContextKeyBaseProviderType                    BifrostContextKey = "bifrost-base-provider-type"
	BifrostContextKeyDoesNotSendDoneMarker               BifrostContextKey = "bifrost-does-not-send-done-marker"
	BifrostContextKeyWaitForUsage                        BifrostContextKey = "bifrost-wait-for-usage"
	BifrostContextKeyHTTPRequestType                     BifrostContextKey = "bifrost-http-request-type"
	BifrostContextKeyHTTPRoute                           BifrostContextKey = "bifrost-http-route"
	BifrostContextKeyPassthroughExtraParams              BifrostContextKey = "bifrost-passthrough-extra-params"
	BifrostContextKeyRoutingEnginesUsed                  BifrostContextKey = "bifrost-routing-engines-used"
	BifrostContextKeyRoutingEngineLogs                   BifrostContextKey = "bifrost-routing-engine-logs"
	BifrostContextKeyTransportPluginLogs                 BifrostContextKey = "bifrost-transport-plugin-logs"
	BifrostContextKeyTransportPostHookCompleter          BifrostContextKey = "bifrost-transport-posthook-completer"
	BifrostContextKeySkipPluginPipeline                  BifrostContextKey = "bifrost-skip-plugin-pipeline"
	BifrostContextKeyParentRequestID                     BifrostContextKey = "bifrost-parent-request-id"
	BifrostContextKeyRealtimeSessionID                   BifrostContextKey = "bifrost-realtime-session-id"
	BifrostContextKeyRealtimeProviderSessionID           BifrostContextKey = "bifrost-realtime-provider-session-id"
	BifrostContextKeyRealtimeSource                      BifrostContextKey = "bifrost-realtime-source"
	BifrostContextKeyRealtimeEventType                   BifrostContextKey = "bifrost-realtime-event-type"
	BifrostContextKeyRealtimeConnectionID                BifrostContextKey = "bifrost-realtime-connection-id"
	BifrostContextKeyRealtimeTransport                   BifrostContextKey = "bifrost-realtime-transport"
	BifrostContextKeyA2ADownstreamTransport              BifrostContextKey = "bifrost-a2a-downstream-transport"
	BifrostContextKeyA2ARequestOrigin                    BifrostContextKey = "bifrost-a2a-request-origin"
	BifrostContextKeyA2AUpstreamTransport                BifrostContextKey = "bifrost-a2a-upstream-transport"
	BifrostContextKeyRealtimeVoice                       BifrostContextKey = "bifrost-realtime-voice"
	BifrostContextKeyRealtimePricingSurface              BifrostContextKey = "bifrost-realtime-pricing-surface"
	BifrostIsAsyncRequest                                BifrostContextKey = "bifrost-is-async-request"
	BifrostContextKeyRequestHeaders                      BifrostContextKey = "bifrost-request-headers"
	BifrostContextKeyRequestQuery                        BifrostContextKey = "bifrost-request-query"
	BifrostContextKeyRoutingAllowedProviders             BifrostContextKey = "bifrost-routing-allowed-providers"
	BifrostContextKeyAllowPerRequestStorageOverride      BifrostContextKey = "bifrost-allow-per-request-storage-override"
	BifrostContextKeyAllowPerRequestRawOverride          BifrostContextKey = "bifrost-allow-per-request-raw-override"
	BifrostContextKeyGuardrailMetadata                   BifrostContextKey = "bifrost-guardrail-debug"
	BifrostContextKeyGuardrailHeaders                    BifrostContextKey = "bifrost-guardrail-headers"
	BifrostContextKeyCacheMetadata                       BifrostContextKey = "bifrost-cache-debug"
	BifrostContextKeyCacheLookup                         BifrostContextKey = "bifrost-cache-lookup"
	BifrostContextKeyRoutingMetadata                     BifrostContextKey = "bifrost-routing-metadata"
	BifrostContextKeyRedactionData                       BifrostContextKey = "bifrost-redaction-data"
	BifrostContextKeyDisableContentLogging               BifrostContextKey = "x-bf-disable-content-logging"
	BifrostContextKeySkipListModelsGovernanceFiltering   BifrostContextKey = "bifrost-skip-list-models-governance-filtering"
	BifrostContextKeySCIMClaims                          BifrostContextKey = "scim_claims"
	BifrostContextKeyAuthCredential                      BifrostContextKey = "bifrost-auth-credential"
	BifrostContextKeyMCPInboundBearer                    BifrostContextKey = "bifrost-mcp-inbound-bearer"
	BifrostContextKeyMCPInboundBearerOmitted             BifrostContextKey = "bifrost-mcp-inbound-bearer-omitted"
	BifrostContextKeyQueryScope                          BifrostContextKey = "bifrost-query-scope"
	BifrostContextKeyDimensionScope                      BifrostContextKey = "bifrost-dimension-scope"
	BifrostContextKeyVisibilityFilterProvider            BifrostContextKey = "bifrost-visibility-filter-provider"
	BifrostContextKeyTargetUserID                        BifrostContextKey = "target_user_id"
	BifrostContextKeyIsAzureUserAgent                    BifrostContextKey = "bifrost-is-azure-user-agent"
	BifrostContextKeyUserRoleID                          BifrostContextKey = "bifrost-user-role-id"
	BifrostContextKeyRuntimeVersion                      BifrostContextKey = "bifrost-runtime-version"
	BifrostContextKeyVideoOutputRequested                BifrostContextKey = "bifrost-video-output-requested"
	BifrostContextKeyValidateKeys                        BifrostContextKey = "bifrost-validate-keys"
	BifrostContextKeyProviderResponseHeaders             BifrostContextKey = "bifrost-provider-response-headers"
	BifrostContextKeyDroppedUnsupportedTools             BifrostContextKey = "bifrost-dropped-unsupported-tools"
	BifrostContextKeyMCPAddedTools                       BifrostContextKey = "bifrost-mcp-added-tools"
	BifrostContextKeyMCPInstructionsInjected             BifrostContextKey = "bifrost-mcp-instructions-injected"
	BifrostContextKeyMCPInstructionsText                 BifrostContextKey = "bifrost-mcp-instructions-text"
	BifrostContextKeyMCPAgentServedRoute                 BifrostContextKey = "bifrost-mcp-agent-served-route"
	BifrostContextKeyLargePayloadMode                    BifrostContextKey = "bifrost-large-payload-mode"
	BifrostContextKeyLargePayloadReader                  BifrostContextKey = "bifrost-large-payload-reader"
	BifrostContextKeyLargePayloadContentLength           BifrostContextKey = "bifrost-large-payload-content-length"
	BifrostContextKeyLargePayloadContentType             BifrostContextKey = "bifrost-large-payload-content-type"
	BifrostContextKeyLargePayloadMetadata                BifrostContextKey = "bifrost-large-payload-metadata"
	BifrostContextKeyLargePayloadRequestThreshold        BifrostContextKey = "bifrost-large-payload-request-threshold"
	BifrostContextKeyLargeResponseMode                   BifrostContextKey = "bifrost-large-response-mode"
	BifrostContextKeyLargePayloadRequestPreview          BifrostContextKey = "bifrost-large-payload-request-preview"
	BifrostContextKeyLargePayloadResponsePreview         BifrostContextKey = "bifrost-large-payload-response-preview"
	BifrostContextKeyLargeResponseReader                 BifrostContextKey = "bifrost-large-response-reader"
	BifrostContextKeyLargeResponseContentLength          BifrostContextKey = "bifrost-large-response-content-length"
	BifrostContextKeyLargeResponseContentType            BifrostContextKey = "bifrost-large-response-content-type"
	BifrostContextKeyLargeResponseContentDisposition     BifrostContextKey = "bifrost-large-response-content-disposition"
	BifrostContextKeyLargeResponseThreshold              BifrostContextKey = "bifrost-large-response-threshold"
	BifrostContextKeyLargePayloadPrefetchSize            BifrostContextKey = "bifrost-large-payload-prefetch-size"
	BifrostContextKeyDeferredUsage                       BifrostContextKey = "bifrost-deferred-usage"
	BifrostContextKeyDeferredGuardrail                   BifrostContextKey = "bifrost-deferred-guardrail"
	BifrostContextKeyStreamAccumulatedUsage              BifrostContextKey = "bifrost-stream-accumulated-usage"
	BifrostContextKeyDeferredLargePayloadMetadata        BifrostContextKey = "bifrost-deferred-large-payload-metadata"
	BifrostContextKeySSEReaderFactory                    BifrostContextKey = "bifrost-sse-reader-factory"
	BifrostContextKeySessionID                           BifrostContextKey = "bifrost-session-id"
	BifrostContextKeySessionTTL                          BifrostContextKey = "bifrost-session-ttl"
	BifrostContextKeySessionAffinity                     BifrostContextKey = "bifrost-session-affinity"
	BifrostContextKeyMCPExtraHeaders                     BifrostContextKey = "bifrost-mcp-extra-headers"
	BifrostContextKeyMCPLogID                            BifrostContextKey = "bifrost-mcp-log-id"
	BifrostContextKeyMCPUnattendedExecution              BifrostContextKey = "bifrost-mcp-unattended-execution"
	BifrostContextKeyStreamTurnPending                   BifrostContextKey = "bifrost-stream-turn-pending"
	BifrostContextKeyInjectedToolsExecuted               BifrostContextKey = "bifrost-injected-tools-executed"
	BifrostContextKeyMCPHealthCheckRequest               BifrostContextKey = "bifrost-mcp-health-check-request"
	BifrostContextKeyCompatConvertTextToChat             BifrostContextKey = "bifrost-compat-convert-text-to-chat"
	BifrostContextKeyCompatConvertChatToResponses        BifrostContextKey = "bifrost-compat-convert-chat-to-responses"
	BifrostContextKeyCompatShouldDropParams              BifrostContextKey = "bifrost-compat-should-drop-params"
	BifrostContextKeyCompatShouldConvertParams           BifrostContextKey = "bifrost-compat-should-convert-params"
	BifrostContextKeyCompatAzureDeepseek                 BifrostContextKey = "bifrost-compat-azure-deepseek"
	BifrostContextKeyCompatForceReasoningOnlyToResponses BifrostContextKey = "bifrost-compat-reasoning-only-to-responses"
	BifrostContextKeyCompatConvertDecisionToResponses    BifrostContextKey = "bifrost-compat-decision-to-responses"
	BifrostContextKeySupportsAssistantPrefill            BifrostContextKey = "bifrost-supports-assistant-prefill"
	BifrostContextKeyDisableStore                        BifrostContextKey = "bifrost-disable-store"
	BifrostContextKeyCompatDroppedParams                 BifrostContextKey = "bifrost-compat-dropped-params"
	BifrostContextKeyAttemptTrail                        BifrostContextKey = "bifrost-attempt-trail"
	BifrostContextKeyDimensions                          BifrostContextKey = "bifrost-dimensions"
	BifrostContextKeyLoadBalancerAttempt                 BifrostContextKey = "bifrost-lb-attempt"
	IsAPIKeyAuthContextKey                               BifrostContextKey = "is_api_key_auth"
	IsLocalAdminContextKey                               BifrostContextKey = "is_local_admin"
	BifrostContextKeyAuthBypassed                        BifrostContextKey = "bifrost-auth-bypassed"
	BifrostContextKeySetupTokenAuthenticated             BifrostContextKey = "bifrost-setup-token-authenticated"
	BifrostContextKeyPassthroughOverridesPresent         BifrostContextKey = "passthrough_overrides_present"
	BifrostContextKeyConnectionClosed                    BifrostContextKey = "connection_closed"
	BifrostContextKeyStreamBodyExhausted                 BifrostContextKey = "stream_body_exhausted"
	BifrostContextKeyStreamParkedAfterFinish             BifrostContextKey = "stream_parked_after_finish"
	BifrostContextKeyTempTokenScope                      BifrostContextKey = "bifrost-temp-token-scope"
	BifrostContextKeyTempTokenResourceID                 BifrostContextKey = "bifrost-temp-token-resource-id"
	BifrostContextKeyAsyncWebhookEndpoint                BifrostContextKey = "bifrost-async-webhook-endpoint"
	BifrostContextKeyUpstreamLatency                     BifrostContextKey = "bifrost-upstream-latency"
	BifrostContextKeyStreamOverhead                      BifrostContextKey = "bifrost-stream-overhead"
)

const (
	BifrostContextKeyGovernanceVirtualKeyID BifrostContextKey = "bifrost-governance-virtual-key-id"

	BifrostContextKeyGovernanceVirtualKeyName BifrostContextKey = "bifrost-governance-virtual-key-name"

	BifrostContextKeyGovernanceTeamID BifrostContextKey = "bifrost-governance-team-id"

	BifrostContextKeyGovernanceTeamName BifrostContextKey = "bifrost-governance-team-name"

	BifrostContextKeyGovernanceCustomerID BifrostContextKey = "bifrost-governance-customer-id"

	BifrostContextKeyGovernanceCustomerName BifrostContextKey = "bifrost-governance-customer-name"

	BifrostContextKeyGovernanceBusinessUnitID BifrostContextKey = "bifrost-governance-business-unit-id"

	BifrostContextKeyGovernanceBusinessUnitName BifrostContextKey = "bifrost-governance-business-unit-name"

	BifrostContextKeyGovernanceTeamIDs BifrostContextKey = "bifrost-governance-team-ids"

	BifrostContextKeyGovernanceTeamNames BifrostContextKey = "bifrost-governance-team-names"

	BifrostContextKeyGovernanceBusinessUnitIDs BifrostContextKey = "bifrost-governance-business-unit-ids"

	BifrostContextKeyGovernanceBusinessUnitNames BifrostContextKey = "bifrost-governance-business-unit-names"

	BifrostContextKeyGovernanceCustomerIDs BifrostContextKey = "bifrost-governance-customer-ids"

	BifrostContextKeyGovernanceCustomerNames BifrostContextKey = "bifrost-governance-customer-names"

	BifrostContextKeyGovernanceProjectID BifrostContextKey = "bifrost-governance-project-id"

	BifrostContextKeyGovernanceProjectName BifrostContextKey = "bifrost-governance-project-name"

	BifrostContextKeyGovernanceBudgetIDs BifrostContextKey = "bifrost-governance-budget-ids"

	BifrostContextKeyGovernanceRateLimitIDs BifrostContextKey = "bifrost-governance-rate-limit-ids"

	BifrostContextKeyUserID BifrostContextKey = "bifrost-user-id"

	BifrostContextKeyUserName BifrostContextKey = "bifrost-user-name"

	BifrostContextKeyUserEmail BifrostContextKey = "bifrost-user-email"
)

const (
	RoutingEngineGovernance     = "governance"
	RoutingEngineRoutingRule    = "routing-rule"
	RoutingEngineLoadbalancing  = "loadbalancing"
	RoutingEngineModelCatalog   = "model-catalog"
	RoutingEngineCircuitBreaker = "circuit-breaker"

	RoutingEngineSessionAffinity = "session-affinity"

	RoutingEngineCore = "core"
)

func (br *BifrostRequest) GetRequestFields() (provider ModelProvider, model string, fallbacks []Fallback) {
	switch {
	case br.ListModelsRequest != nil:
		return br.ListModelsRequest.Provider, "", nil
	case br.ModelRetrieveRequest != nil:
		return br.ModelRetrieveRequest.Provider, br.ModelRetrieveRequest.Model, nil
	case br.TextCompletionRequest != nil:
		return br.TextCompletionRequest.Provider, br.TextCompletionRequest.Model, br.TextCompletionRequest.Fallbacks
	case br.ChatRequest != nil:
		return br.ChatRequest.Provider, br.ChatRequest.Model, br.ChatRequest.Fallbacks
	case br.ResponsesRequest != nil:
		return br.ResponsesRequest.Provider, br.ResponsesRequest.Model, br.ResponsesRequest.Fallbacks
	case br.ResponsesRetrieveRequest != nil:
		return br.ResponsesRetrieveRequest.Provider, "", nil
	case br.ResponsesDeleteRequest != nil:
		return br.ResponsesDeleteRequest.Provider, "", nil
	case br.ResponsesCancelRequest != nil:
		return br.ResponsesCancelRequest.Provider, "", nil
	case br.ResponsesInputItemsRequest != nil:
		return br.ResponsesInputItemsRequest.Provider, "", nil
	case br.CountTokensRequest != nil:
		return br.CountTokensRequest.Provider, br.CountTokensRequest.Model, br.CountTokensRequest.Fallbacks
	case br.CompactionRequest != nil:
		return br.CompactionRequest.Provider, br.CompactionRequest.Model, br.CompactionRequest.Fallbacks
	case br.EmbeddingRequest != nil:
		return br.EmbeddingRequest.Provider, br.EmbeddingRequest.Model, br.EmbeddingRequest.Fallbacks
	case br.RerankRequest != nil:
		return br.RerankRequest.Provider, br.RerankRequest.Model, br.RerankRequest.Fallbacks
	case br.DecisionRequest != nil:
		return br.DecisionRequest.Provider, br.DecisionRequest.Model, br.DecisionRequest.Fallbacks
	case br.OCRRequest != nil:
		return br.OCRRequest.Provider, br.OCRRequest.Model, br.OCRRequest.Fallbacks
	case br.SpeechRequest != nil:
		return br.SpeechRequest.Provider, br.SpeechRequest.Model, br.SpeechRequest.Fallbacks
	case br.TranscriptionRequest != nil:
		return br.TranscriptionRequest.Provider, br.TranscriptionRequest.Model, br.TranscriptionRequest.Fallbacks
	case br.ImageGenerationRequest != nil:
		return br.ImageGenerationRequest.Provider, br.ImageGenerationRequest.Model, br.ImageGenerationRequest.Fallbacks
	case br.ImageEditRequest != nil:
		return br.ImageEditRequest.Provider, br.ImageEditRequest.Model, br.ImageEditRequest.Fallbacks
	case br.ImageVariationRequest != nil:
		return br.ImageVariationRequest.Provider, br.ImageVariationRequest.Model, br.ImageVariationRequest.Fallbacks
	case br.VideoGenerationRequest != nil:
		return br.VideoGenerationRequest.Provider, br.VideoGenerationRequest.Model, br.VideoGenerationRequest.Fallbacks
	case br.VideoEditRequest != nil:
		return br.VideoEditRequest.Provider, br.VideoEditRequest.Model, br.VideoEditRequest.Fallbacks
	case br.VideoRetrieveRequest != nil:
		return br.VideoRetrieveRequest.Provider, "", nil
	case br.VideoDownloadRequest != nil:
		return br.VideoDownloadRequest.Provider, "", nil
	case br.VideoListRequest != nil:
		return br.VideoListRequest.Provider, "", nil
	case br.VideoDeleteRequest != nil:
		return br.VideoDeleteRequest.Provider, "", nil
	case br.VideoRemixRequest != nil:
		return br.VideoRemixRequest.Provider, "", nil
	case br.FileUploadRequest != nil:
		if br.FileUploadRequest.Model != nil {
			return br.FileUploadRequest.Provider, *br.FileUploadRequest.Model, nil
		}
		return br.FileUploadRequest.Provider, "", nil
	case br.FileListRequest != nil:
		if br.FileListRequest.Model != nil {
			return br.FileListRequest.Provider, *br.FileListRequest.Model, nil
		}
		return br.FileListRequest.Provider, "", nil
	case br.FileRetrieveRequest != nil:
		if br.FileRetrieveRequest.Model != nil {
			return br.FileRetrieveRequest.Provider, *br.FileRetrieveRequest.Model, nil
		}
		return br.FileRetrieveRequest.Provider, "", nil
	case br.FileDeleteRequest != nil:
		if br.FileDeleteRequest.Model != nil {
			return br.FileDeleteRequest.Provider, *br.FileDeleteRequest.Model, nil
		}
		return br.FileDeleteRequest.Provider, "", nil
	case br.FileContentRequest != nil:
		if br.FileContentRequest.Model != nil {
			return br.FileContentRequest.Provider, *br.FileContentRequest.Model, nil
		}
		return br.FileContentRequest.Provider, "", nil
	case br.LiveContentRequest != nil:
		return br.LiveContentRequest.Provider, "", nil
	case br.CachedContentCreateRequest != nil:
		return br.CachedContentCreateRequest.Provider, br.CachedContentCreateRequest.Model, nil
	case br.CachedContentListRequest != nil:
		if br.CachedContentListRequest.Model != nil {
			return br.CachedContentListRequest.Provider, *br.CachedContentListRequest.Model, nil
		}
		return br.CachedContentListRequest.Provider, "", nil
	case br.CachedContentRetrieveRequest != nil:
		if br.CachedContentRetrieveRequest.Model != nil {
			return br.CachedContentRetrieveRequest.Provider, *br.CachedContentRetrieveRequest.Model, nil
		}
		return br.CachedContentRetrieveRequest.Provider, "", nil
	case br.CachedContentUpdateRequest != nil:
		if br.CachedContentUpdateRequest.Model != nil {
			return br.CachedContentUpdateRequest.Provider, *br.CachedContentUpdateRequest.Model, nil
		}
		return br.CachedContentUpdateRequest.Provider, "", nil
	case br.CachedContentDeleteRequest != nil:
		if br.CachedContentDeleteRequest.Model != nil {
			return br.CachedContentDeleteRequest.Provider, *br.CachedContentDeleteRequest.Model, nil
		}
		return br.CachedContentDeleteRequest.Provider, "", nil
	case br.BatchCreateRequest != nil:
		if br.BatchCreateRequest.Model != nil {
			return br.BatchCreateRequest.Provider, *br.BatchCreateRequest.Model, nil
		}
		return br.BatchCreateRequest.Provider, "", nil
	case br.BatchListRequest != nil:
		if br.BatchListRequest.Model != nil {
			return br.BatchListRequest.Provider, *br.BatchListRequest.Model, nil
		}
		return br.BatchListRequest.Provider, "", nil
	case br.BatchRetrieveRequest != nil:
		if br.BatchRetrieveRequest.Model != nil {
			return br.BatchRetrieveRequest.Provider, *br.BatchRetrieveRequest.Model, nil
		}
		return br.BatchRetrieveRequest.Provider, "", nil
	case br.BatchCancelRequest != nil:
		if br.BatchCancelRequest.Model != nil {
			return br.BatchCancelRequest.Provider, *br.BatchCancelRequest.Model, nil
		}
		return br.BatchCancelRequest.Provider, "", nil
	case br.BatchResultsRequest != nil:
		if br.BatchResultsRequest.Model != nil {
			return br.BatchResultsRequest.Provider, *br.BatchResultsRequest.Model, nil
		}
		return br.BatchResultsRequest.Provider, "", nil
	case br.BatchDeleteRequest != nil:
		if br.BatchDeleteRequest.Model != nil {
			return br.BatchDeleteRequest.Provider, *br.BatchDeleteRequest.Model, nil
		}
		return br.BatchDeleteRequest.Provider, "", nil
	case br.ContainerCreateRequest != nil:
		return br.ContainerCreateRequest.Provider, "", nil
	case br.ContainerListRequest != nil:
		return br.ContainerListRequest.Provider, "", nil
	case br.ContainerRetrieveRequest != nil:
		return br.ContainerRetrieveRequest.Provider, "", nil
	case br.ContainerDeleteRequest != nil:
		return br.ContainerDeleteRequest.Provider, "", nil
	case br.ContainerFileCreateRequest != nil:
		return br.ContainerFileCreateRequest.Provider, "", nil
	case br.ContainerFileListRequest != nil:
		return br.ContainerFileListRequest.Provider, "", nil
	case br.ContainerFileRetrieveRequest != nil:
		return br.ContainerFileRetrieveRequest.Provider, "", nil
	case br.ContainerFileContentRequest != nil:
		return br.ContainerFileContentRequest.Provider, "", nil
	case br.ContainerFileDeleteRequest != nil:
		return br.ContainerFileDeleteRequest.Provider, "", nil
	case br.PassthroughRequest != nil:
		return br.PassthroughRequest.Provider, br.PassthroughRequest.Model, nil
	}
	return "", "", nil
}

func (br *BifrostRequest) SetProvider(provider ModelProvider) {
	switch {
	case br.ListModelsRequest != nil:
		br.ListModelsRequest.Provider = provider
	case br.ModelRetrieveRequest != nil:
		br.ModelRetrieveRequest.Provider = provider
	case br.TextCompletionRequest != nil:
		br.TextCompletionRequest.Provider = provider
	case br.ChatRequest != nil:
		br.ChatRequest.Provider = provider
	case br.ResponsesRequest != nil:
		br.ResponsesRequest.Provider = provider
	case br.ResponsesRetrieveRequest != nil:
		br.ResponsesRetrieveRequest.Provider = provider
	case br.ResponsesDeleteRequest != nil:
		br.ResponsesDeleteRequest.Provider = provider
	case br.ResponsesCancelRequest != nil:
		br.ResponsesCancelRequest.Provider = provider
	case br.ResponsesInputItemsRequest != nil:
		br.ResponsesInputItemsRequest.Provider = provider
	case br.CountTokensRequest != nil:
		br.CountTokensRequest.Provider = provider
	case br.CompactionRequest != nil:
		br.CompactionRequest.Provider = provider
	case br.EmbeddingRequest != nil:
		br.EmbeddingRequest.Provider = provider
	case br.RerankRequest != nil:
		br.RerankRequest.Provider = provider
	case br.DecisionRequest != nil:
		br.DecisionRequest.Provider = provider
	case br.OCRRequest != nil:
		br.OCRRequest.Provider = provider
	case br.SpeechRequest != nil:
		br.SpeechRequest.Provider = provider
	case br.TranscriptionRequest != nil:
		br.TranscriptionRequest.Provider = provider
	case br.ImageGenerationRequest != nil:
		br.ImageGenerationRequest.Provider = provider
	case br.ImageEditRequest != nil:
		br.ImageEditRequest.Provider = provider
	case br.ImageVariationRequest != nil:
		br.ImageVariationRequest.Provider = provider
	case br.VideoGenerationRequest != nil:
		br.VideoGenerationRequest.Provider = provider
	case br.VideoEditRequest != nil:
		br.VideoEditRequest.Provider = provider
	case br.VideoRetrieveRequest != nil:
		br.VideoRetrieveRequest.Provider = provider
	case br.VideoDownloadRequest != nil:
		br.VideoDownloadRequest.Provider = provider
	case br.VideoListRequest != nil:
		br.VideoListRequest.Provider = provider
	case br.VideoDeleteRequest != nil:
		br.VideoDeleteRequest.Provider = provider
	case br.VideoRemixRequest != nil:
		br.VideoRemixRequest.Provider = provider
	case br.CachedContentCreateRequest != nil:
		br.CachedContentCreateRequest.Provider = provider
	case br.CachedContentListRequest != nil:
		br.CachedContentListRequest.Provider = provider
	case br.CachedContentRetrieveRequest != nil:
		br.CachedContentRetrieveRequest.Provider = provider
	case br.CachedContentUpdateRequest != nil:
		br.CachedContentUpdateRequest.Provider = provider
	case br.CachedContentDeleteRequest != nil:
		br.CachedContentDeleteRequest.Provider = provider
	}
}

func (br *BifrostRequest) SetModel(model string) {
	switch {
	case br.ModelRetrieveRequest != nil:
		br.ModelRetrieveRequest.Model = model
	case br.TextCompletionRequest != nil:
		br.TextCompletionRequest.Model = model
	case br.ChatRequest != nil:
		br.ChatRequest.Model = model
	case br.ResponsesRequest != nil:
		br.ResponsesRequest.Model = model
	case br.CountTokensRequest != nil:
		br.CountTokensRequest.Model = model
	case br.CompactionRequest != nil:
		br.CompactionRequest.Model = model
	case br.EmbeddingRequest != nil:
		br.EmbeddingRequest.Model = model
	case br.RerankRequest != nil:
		br.RerankRequest.Model = model
	case br.DecisionRequest != nil:
		br.DecisionRequest.Model = model
	case br.OCRRequest != nil:
		br.OCRRequest.Model = model
	case br.SpeechRequest != nil:
		br.SpeechRequest.Model = model
	case br.TranscriptionRequest != nil:
		br.TranscriptionRequest.Model = model
	case br.ImageGenerationRequest != nil:
		br.ImageGenerationRequest.Model = model
	case br.ImageEditRequest != nil:
		br.ImageEditRequest.Model = model
	case br.ImageVariationRequest != nil:
		br.ImageVariationRequest.Model = model
	case br.VideoGenerationRequest != nil:
		br.VideoGenerationRequest.Model = model
	case br.VideoEditRequest != nil:
		br.VideoEditRequest.Model = model
	case br.BatchCreateRequest != nil:
		if br.BatchCreateRequest.Model != nil {
			br.BatchCreateRequest.Model = new(model)
		}
	case br.CachedContentCreateRequest != nil:
		br.CachedContentCreateRequest.Model = model
	case br.CachedContentListRequest != nil:
		if br.CachedContentListRequest.Model != nil {
			br.CachedContentListRequest.Model = new(model)
		}
	case br.CachedContentRetrieveRequest != nil:
		if br.CachedContentRetrieveRequest.Model != nil {
			br.CachedContentRetrieveRequest.Model = new(model)
		}
	case br.CachedContentUpdateRequest != nil:
		if br.CachedContentUpdateRequest.Model != nil {
			br.CachedContentUpdateRequest.Model = new(model)
		}
	case br.CachedContentDeleteRequest != nil:
		if br.CachedContentDeleteRequest.Model != nil {
			br.CachedContentDeleteRequest.Model = new(model)
		}
	}
}

func (br *BifrostRequest) SetFallbacks(fallbacks []Fallback) {
	switch {
	case br.TextCompletionRequest != nil:
		br.TextCompletionRequest.Fallbacks = fallbacks
	case br.ChatRequest != nil:
		br.ChatRequest.Fallbacks = fallbacks
	case br.ResponsesRequest != nil:
		br.ResponsesRequest.Fallbacks = fallbacks
	case br.CountTokensRequest != nil:
		br.CountTokensRequest.Fallbacks = fallbacks
	case br.CompactionRequest != nil:
		br.CompactionRequest.Fallbacks = fallbacks
	case br.EmbeddingRequest != nil:
		br.EmbeddingRequest.Fallbacks = fallbacks
	case br.RerankRequest != nil:
		br.RerankRequest.Fallbacks = fallbacks
	case br.DecisionRequest != nil:
		br.DecisionRequest.Fallbacks = fallbacks
	case br.OCRRequest != nil:
		br.OCRRequest.Fallbacks = fallbacks
	case br.SpeechRequest != nil:
		br.SpeechRequest.Fallbacks = fallbacks
	case br.TranscriptionRequest != nil:
		br.TranscriptionRequest.Fallbacks = fallbacks
	case br.ImageGenerationRequest != nil:
		br.ImageGenerationRequest.Fallbacks = fallbacks
	case br.ImageEditRequest != nil:
		br.ImageEditRequest.Fallbacks = fallbacks
	case br.ImageVariationRequest != nil:
		br.ImageVariationRequest.Fallbacks = fallbacks
	case br.VideoGenerationRequest != nil:
		br.VideoGenerationRequest.Fallbacks = fallbacks
	case br.VideoEditRequest != nil:
		br.VideoEditRequest.Fallbacks = fallbacks
	}
}

func (r *BifrostResponse) GetExtraFields() *BifrostResponseExtraFields {
	switch {
	case r.ListModelsResponse != nil:
		return &r.ListModelsResponse.ExtraFields
	case r.ModelRetrieveResponse != nil:
		return &r.ModelRetrieveResponse.ExtraFields
	case r.TextCompletionResponse != nil:
		return &r.TextCompletionResponse.ExtraFields
	case r.ChatResponse != nil:
		return &r.ChatResponse.ExtraFields
	case r.ResponsesResponse != nil:
		return &r.ResponsesResponse.ExtraFields
	case r.ResponsesStreamResponse != nil:
		return &r.ResponsesStreamResponse.ExtraFields
	case r.ResponsesDeleteResponse != nil:
		return &r.ResponsesDeleteResponse.ExtraFields
	case r.ResponsesInputItemsResponse != nil:
		return &r.ResponsesInputItemsResponse.ExtraFields
	case r.CountTokensResponse != nil:
		return &r.CountTokensResponse.ExtraFields
	case r.CompactionResponse != nil:
		return &r.CompactionResponse.ExtraFields
	case r.EmbeddingResponse != nil:
		return &r.EmbeddingResponse.ExtraFields
	case r.RerankResponse != nil:
		return &r.RerankResponse.ExtraFields
	case r.DecisionResponse != nil:
		return &r.DecisionResponse.ExtraFields
	case r.OCRResponse != nil:
		return &r.OCRResponse.ExtraFields
	case r.SpeechResponse != nil:
		return &r.SpeechResponse.ExtraFields
	case r.SpeechStreamResponse != nil:
		return &r.SpeechStreamResponse.ExtraFields
	case r.TranscriptionResponse != nil:
		return &r.TranscriptionResponse.ExtraFields
	case r.TranscriptionStreamResponse != nil:
		return &r.TranscriptionStreamResponse.ExtraFields
	case r.ImageGenerationResponse != nil:
		return &r.ImageGenerationResponse.ExtraFields
	case r.ImageGenerationStreamResponse != nil:
		return &r.ImageGenerationStreamResponse.ExtraFields
	case r.FileUploadResponse != nil:
		return &r.FileUploadResponse.ExtraFields
	case r.FileListResponse != nil:
		return &r.FileListResponse.ExtraFields
	case r.FileRetrieveResponse != nil:
		return &r.FileRetrieveResponse.ExtraFields
	case r.FileDeleteResponse != nil:
		return &r.FileDeleteResponse.ExtraFields
	case r.FileContentResponse != nil:
		return &r.FileContentResponse.ExtraFields
	case r.LiveContentResponse != nil:
		return &r.LiveContentResponse.ExtraFields
	case r.VideoGenerationResponse != nil:
		return &r.VideoGenerationResponse.ExtraFields
	case r.VideoDownloadResponse != nil:
		return &r.VideoDownloadResponse.ExtraFields
	case r.VideoListResponse != nil:
		return &r.VideoListResponse.ExtraFields
	case r.VideoDeleteResponse != nil:
		return &r.VideoDeleteResponse.ExtraFields
	case r.BatchCreateResponse != nil:
		return &r.BatchCreateResponse.ExtraFields
	case r.BatchListResponse != nil:
		return &r.BatchListResponse.ExtraFields
	case r.BatchRetrieveResponse != nil:
		return &r.BatchRetrieveResponse.ExtraFields
	case r.BatchCancelResponse != nil:
		return &r.BatchCancelResponse.ExtraFields
	case r.BatchDeleteResponse != nil:
		return &r.BatchDeleteResponse.ExtraFields
	case r.BatchResultsResponse != nil:
		return &r.BatchResultsResponse.ExtraFields
	case r.ContainerCreateResponse != nil:
		return &r.ContainerCreateResponse.ExtraFields
	case r.ContainerListResponse != nil:
		return &r.ContainerListResponse.ExtraFields
	case r.ContainerRetrieveResponse != nil:
		return &r.ContainerRetrieveResponse.ExtraFields
	case r.ContainerDeleteResponse != nil:
		return &r.ContainerDeleteResponse.ExtraFields
	case r.ContainerFileCreateResponse != nil:
		return &r.ContainerFileCreateResponse.ExtraFields
	case r.ContainerFileListResponse != nil:
		return &r.ContainerFileListResponse.ExtraFields
	case r.ContainerFileRetrieveResponse != nil:
		return &r.ContainerFileRetrieveResponse.ExtraFields
	case r.ContainerFileContentResponse != nil:
		return &r.ContainerFileContentResponse.ExtraFields
	case r.ContainerFileDeleteResponse != nil:
		return &r.ContainerFileDeleteResponse.ExtraFields
	case r.PassthroughResponse != nil:
		return &r.PassthroughResponse.ExtraFields
	case r.CachedContentCreateResponse != nil:
		return &r.CachedContentCreateResponse.ExtraFields
	case r.CachedContentListResponse != nil:
		return &r.CachedContentListResponse.ExtraFields
	case r.CachedContentRetrieveResponse != nil:
		return &r.CachedContentRetrieveResponse.ExtraFields
	case r.CachedContentUpdateResponse != nil:
		return &r.CachedContentUpdateResponse.ExtraFields
	case r.CachedContentDeleteResponse != nil:
		return &r.CachedContentDeleteResponse.ExtraFields
	}

	return &BifrostResponseExtraFields{}
}

func syncDeprecatedFromRoutingInfo(info RoutingInfo, provider *ModelProvider, originalModelRequested, resolvedModelUsed *string) {
	if provider != nil && info.Provider != "" {
		*provider = info.Provider
	}

	if originalModelRequested != nil {
		if info.IsFallback && info.PrimaryModel != nil && *info.PrimaryModel != "" {
			*originalModelRequested = *info.PrimaryModel
		} else if info.Model != "" {
			*originalModelRequested = info.Model
		}
	}

	if resolvedModelUsed != nil {
		if info.ResolvedKeyAlias != nil && info.ResolvedKeyAlias.ModelID != "" {
			*resolvedModelUsed = info.ResolvedKeyAlias.ModelID
		} else if info.Model != "" {
			*resolvedModelUsed = info.Model
		}
	}
}

func (r *BifrostResponse) PopulateRoutingInfo(info RoutingInfo) {
	if r == nil {
		return
	}
	if ef := r.GetExtraFields(); ef != nil {

		if info.ServerSideFallbackModel == nil {
			info.ServerSideFallbackModel = ef.RoutingInfo.ServerSideFallbackModel
		}
		ef.RoutingInfo = info
		syncDeprecatedFromRoutingInfo(info, &ef.Provider, &ef.OriginalModelRequested, &ef.ResolvedModelUsed)
	}
}

func (e *BifrostError) PopulateRoutingInfo(info RoutingInfo) {
	if e == nil {
		return
	}
	e.ExtraFields.RoutingInfo = info
	syncDeprecatedFromRoutingInfo(info, &e.ExtraFields.Provider, &e.ExtraFields.OriginalModelRequested, &e.ExtraFields.ResolvedModelUsed)
}

func (r *BifrostResponse) SetFallbackRoutingInfo(primaryProvider ModelProvider, primaryModel string) {
	if r == nil {
		return
	}
	ef := r.GetExtraFields()
	if ef == nil {
		return
	}
	ef.RoutingInfo.IsFallback = true
	if primaryProvider != "" {
		p := primaryProvider
		ef.RoutingInfo.PrimaryProvider = &p
	}
	if primaryModel != "" {
		m := primaryModel
		ef.RoutingInfo.PrimaryModel = &m
	}
	syncDeprecatedFromRoutingInfo(ef.RoutingInfo, &ef.Provider, &ef.OriginalModelRequested, &ef.ResolvedModelUsed)
}

func (e *BifrostError) SetFallbackRoutingInfo(primaryProvider ModelProvider, primaryModel string) {
	if e == nil {
		return
	}
	e.ExtraFields.RoutingInfo.IsFallback = true
	if primaryProvider != "" {
		p := primaryProvider
		e.ExtraFields.RoutingInfo.PrimaryProvider = &p
	}
	if primaryModel != "" {
		m := primaryModel
		e.ExtraFields.RoutingInfo.PrimaryModel = &m
	}
	syncDeprecatedFromRoutingInfo(e.ExtraFields.RoutingInfo, &e.ExtraFields.Provider, &e.ExtraFields.OriginalModelRequested, &e.ExtraFields.ResolvedModelUsed)
}

func (r *BifrostResponse) PopulateExtraFields(requestType RequestType, provider ModelProvider, originalModelRequested string, resolvedModelUsed string) {
	if r == nil {
		return
	}
	resolvedModel := resolvedModelUsed
	if resolvedModel == "" {
		resolvedModel = originalModelRequested
	}
	switch {
	case r.ListModelsResponse != nil:
		setCoreExtraFields(&r.ListModelsResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ModelRetrieveResponse != nil:
		setCoreExtraFields(&r.ModelRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.TextCompletionResponse != nil:
		setCoreExtraFields(&r.TextCompletionResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ChatResponse != nil:
		setCoreExtraFields(&r.ChatResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ResponsesResponse != nil:
		setCoreExtraFields(&r.ResponsesResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ResponsesDeleteResponse != nil:
		setCoreExtraFields(&r.ResponsesDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ResponsesInputItemsResponse != nil:
		setCoreExtraFields(&r.ResponsesInputItemsResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ResponsesStreamResponse != nil:
		setCoreExtraFields(&r.ResponsesStreamResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CountTokensResponse != nil:
		setCoreExtraFields(&r.CountTokensResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CompactionResponse != nil:
		setCoreExtraFields(&r.CompactionResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.EmbeddingResponse != nil:
		setCoreExtraFields(&r.EmbeddingResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.RerankResponse != nil:
		setCoreExtraFields(&r.RerankResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.DecisionResponse != nil:
		setCoreExtraFields(&r.DecisionResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.SpeechResponse != nil:
		setCoreExtraFields(&r.SpeechResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.SpeechStreamResponse != nil:
		setCoreExtraFields(&r.SpeechStreamResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.TranscriptionResponse != nil:
		setCoreExtraFields(&r.TranscriptionResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.TranscriptionStreamResponse != nil:
		setCoreExtraFields(&r.TranscriptionStreamResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ImageGenerationResponse != nil:
		setCoreExtraFields(&r.ImageGenerationResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ImageGenerationStreamResponse != nil:
		setCoreExtraFields(&r.ImageGenerationStreamResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.VideoGenerationResponse != nil:
		setCoreExtraFields(&r.VideoGenerationResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.VideoDownloadResponse != nil:
		setCoreExtraFields(&r.VideoDownloadResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.VideoListResponse != nil:
		setCoreExtraFields(&r.VideoListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.VideoDeleteResponse != nil:
		setCoreExtraFields(&r.VideoDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.FileUploadResponse != nil:
		setCoreExtraFields(&r.FileUploadResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.FileListResponse != nil:
		setCoreExtraFields(&r.FileListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.FileRetrieveResponse != nil:
		setCoreExtraFields(&r.FileRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.FileDeleteResponse != nil:
		setCoreExtraFields(&r.FileDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.FileContentResponse != nil:
		setCoreExtraFields(&r.FileContentResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.LiveContentResponse != nil:
		setCoreExtraFields(&r.LiveContentResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchCreateResponse != nil:
		setCoreExtraFields(&r.BatchCreateResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchListResponse != nil:
		setCoreExtraFields(&r.BatchListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchRetrieveResponse != nil:
		setCoreExtraFields(&r.BatchRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchCancelResponse != nil:
		setCoreExtraFields(&r.BatchCancelResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchDeleteResponse != nil:
		setCoreExtraFields(&r.BatchDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.BatchResultsResponse != nil:
		setCoreExtraFields(&r.BatchResultsResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerCreateResponse != nil:
		setCoreExtraFields(&r.ContainerCreateResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerListResponse != nil:
		setCoreExtraFields(&r.ContainerListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerRetrieveResponse != nil:
		setCoreExtraFields(&r.ContainerRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerDeleteResponse != nil:
		setCoreExtraFields(&r.ContainerDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerFileCreateResponse != nil:
		setCoreExtraFields(&r.ContainerFileCreateResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerFileListResponse != nil:
		setCoreExtraFields(&r.ContainerFileListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerFileRetrieveResponse != nil:
		setCoreExtraFields(&r.ContainerFileRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerFileContentResponse != nil:
		setCoreExtraFields(&r.ContainerFileContentResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.ContainerFileDeleteResponse != nil:
		setCoreExtraFields(&r.ContainerFileDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.OCRResponse != nil:
		setCoreExtraFields(&r.OCRResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.PassthroughResponse != nil:
		setCoreExtraFields(&r.PassthroughResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CachedContentCreateResponse != nil:
		setCoreExtraFields(&r.CachedContentCreateResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CachedContentListResponse != nil:
		setCoreExtraFields(&r.CachedContentListResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CachedContentRetrieveResponse != nil:
		setCoreExtraFields(&r.CachedContentRetrieveResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CachedContentUpdateResponse != nil:
		setCoreExtraFields(&r.CachedContentUpdateResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	case r.CachedContentDeleteResponse != nil:
		setCoreExtraFields(&r.CachedContentDeleteResponse.ExtraFields, requestType, provider, originalModelRequested, resolvedModel)
	}
}

func setCoreExtraFields(ef *BifrostResponseExtraFields, requestType RequestType, provider ModelProvider, originalModelRequested, resolvedModelUsed string) {
	if ef.RequestType != requestType {
		ef.RequestType = requestType
	}
	if ef.Provider != provider {
		ef.Provider = provider
	}
	if ef.OriginalModelRequested != originalModelRequested {
		ef.OriginalModelRequested = originalModelRequested
	}
	if ef.ResolvedModelUsed != resolvedModelUsed {
		ef.ResolvedModelUsed = resolvedModelUsed
	}
}

func (r *BifrostMCPConnectResponse) PopulateExtraFields(clientName string) {
	if r == nil {
		return
	}
	if r.ExtraFields.ClientName == "" {
		r.ExtraFields.ClientName = clientName
	}
}

func (r *BifrostMCPResponse) PopulateExtraFields(mcpRequestType MCPRequestType, clientName, toolName string) {
	if r == nil {
		return
	}
	if r.ExtraFields.MCPRequestType == "" {
		r.ExtraFields.MCPRequestType = mcpRequestType
	}
	if r.ExtraFields.ClientName == "" {
		r.ExtraFields.ClientName = clientName
	}
	if r.ExtraFields.ToolName == "" {
		r.ExtraFields.ToolName = toolName
	}
}

func IsStreamCommitMarker(chunk *BifrostStreamChunk) bool { return chunk == streamCommitMarker }

func (bs BifrostStreamChunk) MarshalJSON() ([]byte, error) {
	if bs.BifrostTextCompletionResponse != nil {
		return MarshalSorted(bs.BifrostTextCompletionResponse)
	} else if bs.BifrostChatResponse != nil {
		return MarshalSorted(bs.BifrostChatResponse)
	} else if bs.BifrostResponsesStreamResponse != nil {
		return MarshalSorted(bs.BifrostResponsesStreamResponse)
	} else if bs.BifrostSpeechStreamResponse != nil {
		return MarshalSorted(bs.BifrostSpeechStreamResponse)
	} else if bs.BifrostTranscriptionStreamResponse != nil {
		return MarshalSorted(bs.BifrostTranscriptionStreamResponse)
	} else if bs.BifrostImageGenerationStreamResponse != nil {
		return MarshalSorted(bs.BifrostImageGenerationStreamResponse)
	} else if bs.BifrostPassthroughResponse != nil {
		return MarshalSorted(bs.BifrostPassthroughResponse)
	} else if bs.BifrostError != nil {
		return MarshalSorted(bs.BifrostError)
	}

	return []byte("{}"), nil
}

func (e *BifrostError) PopulateExtraFields(requestType RequestType, provider ModelProvider, originalModelRequested string, resolvedModelUsed string) {
	if e == nil {
		return
	}
	e.ExtraFields.RequestType = requestType
	e.ExtraFields.Provider = provider
	e.ExtraFields.OriginalModelRequested = originalModelRequested
	if resolvedModelUsed != "" {
		e.ExtraFields.ResolvedModelUsed = resolvedModelUsed
	} else {
		e.ExtraFields.ResolvedModelUsed = originalModelRequested
	}
}

const (
	ProviderAutoResolveErrorMessage = "could not auto resolve a provider for the request, please specify a provider explicitly"
	ModelAutoResolveErrorMessage    = "could not auto resolve a model for the request, please specify a model explicitly"
)

func (e *BifrostError) String() string {
	if e == nil {
		return "<nil>"
	}
	b, err := MarshalSorted(e)
	if err != nil {
		return fmt.Sprintf("BifrostError{marshal_err=%v}", err)
	}
	return string(b)
}

func (e *BifrostError) GetErrorString() string {
	if e == nil {
		return ""
	}
	if e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	} else if e.StatusCode != nil {
		switch *e.StatusCode {
		case 401:
			return "unauthorized"
		case 403:
			return "forbidden"
		case 404:
			return "endpoint not found"
		case 405:
			return "method not allowed"
		case 429:
			return "rate limit exceeded"
		case 500:
			return "internal server error"
		case 502:
			return "bad gateway"
		case 503:
			return "service unavailable"
		case 504:
			return "gateway timeout"
		default:
			if e.Error != nil && e.Error.Message != "" {
				return e.Error.Message
			}
			return fmt.Sprintf("HTTP %d error", *e.StatusCode)
		}
	} else if e.Type != nil {
		return *e.Type
	} else {
		return "unknown error"
	}
}

func (e *ErrorField) MarshalJSON() ([]byte, error) {
	type Alias ErrorField
	aux := &struct {
		Error *string `json:"error,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(e),
	}

	if e.Error != nil {
		errStr := e.Error.Error()
		aux.Error = &errStr
	}

	return json.Marshal(aux)
}

func (e *ErrorField) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Type    *string     `json:"type,omitempty"`
		Code    interface{} `json:"code,omitempty"`
		Message string      `json:"message"`
		Error   *string     `json:"error,omitempty"`
		Param   interface{} `json:"param,omitempty"`
		EventID *string     `json:"event_id,omitempty"`
	}{}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	e.Type = aux.Type
	e.Message = aux.Message
	e.Param = aux.Param
	e.EventID = aux.EventID
	if aux.Error != nil {
		e.Error = errors.New(*aux.Error)
	}
	if aux.Code != nil {
		switch v := aux.Code.(type) {
		case string:
			e.Code = &v
		case float64:
			s := strconv.FormatInt(int64(v), 10)
			e.Code = &s
		default:
			s := fmt.Sprint(aux.Code)
			e.Code = &s
		}
	}
	return nil
}
