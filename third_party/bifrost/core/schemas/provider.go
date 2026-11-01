package schemas

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"time"
)

type CustomProviderConfig struct {
	CustomProviderKey     string                 `json:"-"`
	IsKeyLess             bool                   `json:"is_key_less"`
	BaseProviderType      ModelProvider          `json:"base_provider_type"`
	AllowedRequests       *AllowedRequests       `json:"allowed_requests,omitempty"`
	RequestPathOverrides  map[RequestType]string `json:"request_path_overrides,omitempty"`
	DoesNotSendDoneMarker bool                   `json:"does_not_send_done_marker"`
	WaitForUsage          bool                   `json:"wait_for_usage"`
}
type AllowedRequests struct {
	ListModels            bool `json:"list_models"`
	ModelRetrieve         bool `json:"model_retrieve"`
	TextCompletion        bool `json:"text_completion"`
	TextCompletionStream  bool `json:"text_completion_stream"`
	ChatCompletion        bool `json:"chat_completion"`
	ChatCompletionStream  bool `json:"chat_completion_stream"`
	Responses             bool `json:"responses"`
	ResponsesStream       bool `json:"responses_stream"`
	ResponsesRetrieve     bool `json:"responses_retrieve"`
	ResponsesDelete       bool `json:"responses_delete"`
	ResponsesCancel       bool `json:"responses_cancel"`
	ResponsesInputItems   bool `json:"responses_input_items"`
	CountTokens           bool `json:"count_tokens"`
	Compaction            bool `json:"compaction"`
	Embedding             bool `json:"embedding"`
	Rerank                bool `json:"rerank"`
	Decision              bool `json:"decisions"`
	OCR                   bool `json:"ocr"`
	Speech                bool `json:"speech"`
	SpeechStream          bool `json:"speech_stream"`
	Transcription         bool `json:"transcription"`
	TranscriptionStream   bool `json:"transcription_stream"`
	ImageGeneration       bool `json:"image_generation"`
	ImageGenerationStream bool `json:"image_generation_stream"`
	ImageEdit             bool `json:"image_edit"`
	ImageEditStream       bool `json:"image_edit_stream"`
	ImageVariation        bool `json:"image_variation"`
	VideoGeneration       bool `json:"video_generation"`
	VideoEdit             bool `json:"video_edit"`
	VideoRetrieve         bool `json:"video_retrieve"`
	VideoDownload         bool `json:"video_download"`
	VideoDelete           bool `json:"video_delete"`
	VideoList             bool `json:"video_list"`
	VideoRemix            bool `json:"video_remix"`
	BatchCreate           bool `json:"batch_create"`
	BatchList             bool `json:"batch_list"`
	BatchRetrieve         bool `json:"batch_retrieve"`
	BatchCancel           bool `json:"batch_cancel"`
	BatchDelete           bool `json:"batch_delete"`
	BatchResults          bool `json:"batch_results"`
	FileUpload            bool `json:"file_upload"`
	FileList              bool `json:"file_list"`
	FileRetrieve          bool `json:"file_retrieve"`
	FileDelete            bool `json:"file_delete"`
	FileContent           bool `json:"file_content"`
	ContainerCreate       bool `json:"container_create"`
	ContainerList         bool `json:"container_list"`
	ContainerRetrieve     bool `json:"container_retrieve"`
	ContainerDelete       bool `json:"container_delete"`
	ContainerFileCreate   bool `json:"container_file_create"`
	ContainerFileList     bool `json:"container_file_list"`
	ContainerFileRetrieve bool `json:"container_file_retrieve"`
	ContainerFileContent  bool `json:"container_file_content"`
	ContainerFileDelete   bool `json:"container_file_delete"`
	Passthrough           bool `json:"passthrough"`
	PassthroughStream     bool `json:"passthrough_stream"`
	WebSocketResponses    bool `json:"websocket_responses"`
	Realtime              bool `json:"realtime"`
	Live                  bool `json:"live"`
	CachedContentCreate   bool `json:"cached_content_create"`
	CachedContentList     bool `json:"cached_content_list"`
	CachedContentRetrieve bool `json:"cached_content_retrieve"`
	CachedContentUpdate   bool `json:"cached_content_update"`
	CachedContentDelete   bool `json:"cached_content_delete"`
}
type NetworkConfig struct {
	BaseURL                        string               `json:"base_url,omitempty"`
	ExtraHeaders                   map[string]SecretVar `json:"extra_headers,omitempty"`
	DefaultRequestTimeoutInSeconds int                  `json:"default_request_timeout_in_seconds"`
	MaxRetries                     int                  `json:"max_retries"`
	RetryBackoffInitial            time.Duration        `json:"retry_backoff_initial"`
	RetryBackoffMax                time.Duration        `json:"retry_backoff_max"`
	InsecureSkipVerify             bool                 `json:"insecure_skip_verify,omitempty"`
	CACertPEM                      *SecretVar           `json:"ca_cert_pem,omitempty"`
	StreamIdleTimeoutInSeconds     int                  `json:"stream_idle_timeout_in_seconds,omitempty"`
	KeepAliveTimeoutInSeconds      int                  `json:"keep_alive_timeout_in_seconds,omitempty"`
	MaxConnsPerHost                int                  `json:"max_conns_per_host,omitempty"`
	EnforceHTTP2                   bool                 `json:"enforce_http2,omitempty"`
	HTTP2PingIntervalInSeconds     int                  `json:"http2_ping_interval_in_seconds,omitempty"`
	BetaHeaderOverrides            map[string]bool      `json:"beta_header_overrides,omitempty"`
	AllowPrivateNetwork            bool                 `json:"allow_private_network,omitempty"`
}
type ProxyConfig struct {
	Type      ProxyType  `json:"type"`
	URL       *SecretVar `json:"url"`
	Username  *SecretVar `json:"username"`
	Password  *SecretVar `json:"password"`
	CACertPEM *SecretVar `json:"ca_cert_pem"`

	NoProxy string `json:"-"`

	SkipTLSVerify bool `json:"-"`
}
type CacheControlInjectionPoint struct {
	Location string `json:"location"`

	Role *string `json:"role,omitempty"`

	Index *int `json:"index,omitempty"`
}
type InjectedTool struct {
	Name          string `json:"name"`
	MCPClientName string `json:"mcp_client_name"`
	ToolName      string `json:"tool_name"`
}
type PromptCacheConfig struct {
	AutoInject bool `json:"auto_inject"`

	TTL *string `json:"ttl,omitempty"`

	InjectionPoints []CacheControlInjectionPoint `json:"cache_control_injection_points,omitempty"`
}
type OpenAIConfig struct {
	DisableStore bool `json:"disable_store"`
}
type ResponsesNamespaceToolProvider interface {
	SupportsResponsesNamespaceTools(ctx *BifrostContext, key Key, model string) bool
}
type ProviderConfig struct {
	NetworkConfig            NetworkConfig            `json:"network_config"`
	ConcurrencyAndBufferSize ConcurrencyAndBufferSize `json:"concurrency_and_buffer_size"`

	Logger                  Logger                `json:"-"`
	ProxyConfig             *ProxyConfig          `json:"proxy_config,omitempty"`
	SendBackRawRequest      bool                  `json:"send_back_raw_request"`
	SendBackRawResponse     bool                  `json:"send_back_raw_response"`
	StoreRawRequestResponse bool                  `json:"store_raw_request_response"`
	IgnoreProviderCost      bool                  `json:"ignore_provider_cost"`
	CustomProviderConfig    *CustomProviderConfig `json:"custom_provider_config,omitempty"`
	OpenAIConfig            *OpenAIConfig         `json:"openai_config,omitempty"`
	PromptCache             *PromptCacheConfig    `json:"prompt_cache,omitempty"`
	InjectedTools           []InjectedTool        `json:"injected_tools,omitempty"`
	Passthrough             *PassthroughPolicy    `json:"passthrough,omitempty"`
}
type PostHookRunner func(ctx *BifrostContext, result *BifrostResponse, err *BifrostError) (*BifrostResponse, *BifrostError)
type Provider interface {
	GetProviderKey() ModelProvider

	ListModels(ctx *BifrostContext, keys []Key, request *BifrostListModelsRequest) (*BifrostListModelsResponse, *BifrostError)

	ChatCompletion(ctx *BifrostContext, key Key, request *BifrostChatRequest) (*BifrostChatResponse, *BifrostError)

	ChatCompletionStream(ctx *BifrostContext, postHookRunner PostHookRunner, postHookSpanFinalizer func(context.Context), key Key, request *BifrostChatRequest) (chan *BifrostStreamChunk, *BifrostError)

	Responses(ctx *BifrostContext, key Key, request *BifrostResponsesRequest) (*BifrostResponsesResponse, *BifrostError)

	ResponsesStream(ctx *BifrostContext, postHookRunner PostHookRunner, postHookSpanFinalizer func(context.Context), key Key, request *BifrostResponsesRequest) (chan *BifrostStreamChunk, *BifrostError)
}
type ProxyType string
type ConcurrencyAndBufferSize struct {
	Concurrency int `json:"concurrency"`
	BufferSize  int `json:"buffer_size"`
}

const (
	DefaultMaxRetries                 = 0
	DefaultRetryBackoffInitial        = 500 * time.Millisecond
	DefaultRetryBackoffMax            = 5 * time.Second
	DefaultRequestTimeoutInSeconds    = 300
	DefaultMaxConnDurationInSeconds   = 300
	DefaultBufferSize                 = 5000
	DefaultConcurrency                = 1000
	DefaultStreamBufferSize           = 256
	DefaultStreamIdleTimeoutInSeconds = 120
	DefaultKeepAliveTimeoutInSeconds  = 30
	DefaultMaxConnsPerHost            = 5000
	MaxConnsPerHostUpperBound         = 10000
	DefaultMaxIdleConnsPerHost        = 40

	HTTP2PingIntervalUpperBoundSeconds = 3600
)

const (
	ErrProviderRequestTimedOut      = "request timed out (default is 300 seconds). You can increase it by setting the default_request_timeout_in_seconds in the network_config or in UI - Providers > Provider Name > Network Config."
	ErrRequestCancelled             = "request cancelled by caller"
	ErrStreamFirstTokenTimeout      = "TTFT timeout: stream produced no first token before the deadline"
	FirstTokenTimeoutErrorCode      = "ttft_timeout"
	ErrRequestBodyConversion        = "failed to convert bifrost request to the expected provider request body"
	ErrProviderRequestMarshal       = "failed to marshal request body to JSON"
	ErrProviderCreateRequest        = "failed to create HTTP request to provider API"
	ErrProviderDoRequest            = "failed to execute HTTP request to provider API"
	ErrProviderNetworkError         = "network error occurred while connecting to provider API (DNS lookup, connection refused, etc.)"
	ErrProviderResponseDecode       = "failed to decode response body from provider API"
	ErrProviderResponseUnmarshal    = "failed to unmarshal response from provider API"
	ErrProviderResponseEmpty        = "empty response received from provider"
	ErrProviderResponseHTML         = "HTML response received from provider"
	ErrProviderRawRequestUnmarshal  = "failed to unmarshal raw request from provider API"
	ErrProviderRawResponseUnmarshal = "failed to unmarshal raw response from provider API"
	ErrProviderResponseDecompress   = "failed to decompress provider's response"
	ErrProviderStreamTruncated      = "provider closed the stream before sending a completion marker (upstream connection ended mid-stream)"
)

func (nc *NetworkConfig) UnmarshalJSON(data []byte) error {

	type NetworkConfigAlias struct {
		BaseURL                        string               `json:"base_url,omitempty"`
		ExtraHeaders                   map[string]SecretVar `json:"extra_headers,omitempty"`
		DefaultRequestTimeoutInSeconds int                  `json:"default_request_timeout_in_seconds"`
		MaxRetries                     int                  `json:"max_retries"`
		RetryBackoffInitial            json.RawMessage      `json:"retry_backoff_initial"`
		RetryBackoffMax                json.RawMessage      `json:"retry_backoff_max"`
		InsecureSkipVerify             bool                 `json:"insecure_skip_verify,omitempty"`
		CACertPEM                      *SecretVar           `json:"ca_cert_pem,omitempty"`
		StreamIdleTimeoutInSeconds     int                  `json:"stream_idle_timeout_in_seconds,omitempty"`
		KeepAliveTimeoutInSeconds      int                  `json:"keep_alive_timeout_in_seconds,omitempty"`
		MaxConnsPerHost                int                  `json:"max_conns_per_host,omitempty"`
		EnforceHTTP2                   bool                 `json:"enforce_http2,omitempty"`
		HTTP2PingIntervalInSeconds     int                  `json:"http2_ping_interval_in_seconds,omitempty"`
		BetaHeaderOverrides            map[string]bool      `json:"beta_header_overrides,omitempty"`
		AllowPrivateNetwork            bool                 `json:"allow_private_network,omitempty"`
	}

	var alias NetworkConfigAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	nc.BaseURL = alias.BaseURL
	nc.ExtraHeaders = alias.ExtraHeaders
	nc.DefaultRequestTimeoutInSeconds = alias.DefaultRequestTimeoutInSeconds
	nc.MaxRetries = alias.MaxRetries
	nc.InsecureSkipVerify = alias.InsecureSkipVerify
	nc.CACertPEM = alias.CACertPEM
	nc.StreamIdleTimeoutInSeconds = alias.StreamIdleTimeoutInSeconds
	nc.KeepAliveTimeoutInSeconds = alias.KeepAliveTimeoutInSeconds
	nc.MaxConnsPerHost = alias.MaxConnsPerHost
	nc.EnforceHTTP2 = alias.EnforceHTTP2
	nc.HTTP2PingIntervalInSeconds = alias.HTTP2PingIntervalInSeconds
	nc.BetaHeaderOverrides = alias.BetaHeaderOverrides
	nc.AllowPrivateNetwork = alias.AllowPrivateNetwork

	if len(alias.RetryBackoffInitial) > 0 && string(alias.RetryBackoffInitial) != "null" {
		dur, err := parseNetworkBackoffDuration(alias.RetryBackoffInitial, "retry_backoff_initial")
		if err != nil {
			return err
		}
		if dur > 0 {
			nc.RetryBackoffInitial = dur
		}
	}

	if len(alias.RetryBackoffMax) > 0 && string(alias.RetryBackoffMax) != "null" {
		dur, err := parseNetworkBackoffDuration(alias.RetryBackoffMax, "retry_backoff_max")
		if err != nil {
			return err
		}
		if dur > 0 {
			nc.RetryBackoffMax = dur
		}
	}

	return nil
}

func parseNetworkBackoffDuration(data json.RawMessage, fieldName string) (time.Duration, error) {
	if len(data) == 0 || string(data) == "null" {
		return 0, nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return 0, err
		}
		dur, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid %s %q: use a Go duration string like \"500ms\", \"5s\", \"1m\"", fieldName, s)
		}
		return dur, nil
	}

	var ms int64
	if err := json.Unmarshal(data, &ms); err != nil {
		return 0, fmt.Errorf("invalid %s: expected a duration string (e.g. \"500ms\") or integer milliseconds: %w", fieldName, err)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func extraHeadersAsStrings(headers map[string]SecretVar) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		out[name] = SecretVarAsString(&value)
	}
	return out
}

func (nc NetworkConfig) MarshalJSON() ([]byte, error) {

	type NetworkConfigAlias struct {
		BaseURL                        string            `json:"base_url,omitempty"`
		ExtraHeaders                   map[string]string `json:"extra_headers,omitempty"`
		DefaultRequestTimeoutInSeconds int               `json:"default_request_timeout_in_seconds"`
		MaxRetries                     int               `json:"max_retries"`
		RetryBackoffInitial            int64             `json:"retry_backoff_initial"`
		RetryBackoffMax                int64             `json:"retry_backoff_max"`
		InsecureSkipVerify             bool              `json:"insecure_skip_verify,omitempty"`
		CACertPEM                      string            `json:"ca_cert_pem,omitempty"`
		StreamIdleTimeoutInSeconds     int               `json:"stream_idle_timeout_in_seconds,omitempty"`
		KeepAliveTimeoutInSeconds      int               `json:"keep_alive_timeout_in_seconds,omitempty"`
		MaxConnsPerHost                int               `json:"max_conns_per_host,omitempty"`
		EnforceHTTP2                   bool              `json:"enforce_http2,omitempty"`
		HTTP2PingIntervalInSeconds     int               `json:"http2_ping_interval_in_seconds,omitempty"`
		BetaHeaderOverrides            map[string]bool   `json:"beta_header_overrides,omitempty"`
		AllowPrivateNetwork            bool              `json:"allow_private_network,omitempty"`
	}

	alias := NetworkConfigAlias{
		BaseURL:                        nc.BaseURL,
		ExtraHeaders:                   extraHeadersAsStrings(nc.ExtraHeaders),
		DefaultRequestTimeoutInSeconds: nc.DefaultRequestTimeoutInSeconds,
		MaxRetries:                     nc.MaxRetries,

		RetryBackoffInitial:        int64(nc.RetryBackoffInitial / time.Millisecond),
		RetryBackoffMax:            int64(nc.RetryBackoffMax / time.Millisecond),
		InsecureSkipVerify:         nc.InsecureSkipVerify,
		StreamIdleTimeoutInSeconds: nc.StreamIdleTimeoutInSeconds,
		KeepAliveTimeoutInSeconds:  nc.KeepAliveTimeoutInSeconds,
		MaxConnsPerHost:            nc.MaxConnsPerHost,
		EnforceHTTP2:               nc.EnforceHTTP2,
		HTTP2PingIntervalInSeconds: nc.HTTP2PingIntervalInSeconds,
		BetaHeaderOverrides:        nc.BetaHeaderOverrides,
		AllowPrivateNetwork:        nc.AllowPrivateNetwork,
	}
	if nc.CACertPEM != nil {
		alias.CACertPEM = SecretVarAsString(nc.CACertPEM)
	}

	return json.Marshal(alias)
}

const (
	NoProxy ProxyType = "none"

	HTTPProxy ProxyType = "http"

	Socks5Proxy ProxyType = "socks5"

	EnvProxy ProxyType = "environment"
)

func (ar *AllowedRequests) IsOperationAllowed(operation RequestType) bool {
	if ar == nil {
		return true
	}

	switch operation {
	case ListModelsRequest:
		return ar.ListModels
	case ModelRetrieveRequest:
		return ar.ModelRetrieve
	case TextCompletionRequest:
		return ar.TextCompletion
	case TextCompletionStreamRequest:
		return ar.TextCompletionStream
	case ChatCompletionRequest:
		return ar.ChatCompletion
	case ChatCompletionStreamRequest:
		return ar.ChatCompletionStream
	case ResponsesRequest:
		return ar.Responses
	case ResponsesStreamRequest:
		return ar.ResponsesStream
	case ResponsesRetrieveRequest, ResponsesRetrieveStreamRequest:
		return ar.ResponsesRetrieve
	case ResponsesDeleteRequest:
		return ar.ResponsesDelete
	case ResponsesCancelRequest:
		return ar.ResponsesCancel
	case ResponsesInputItemsRequest:
		return ar.ResponsesInputItems
	case CountTokensRequest:
		return ar.CountTokens
	case CompactionRequest:
		return ar.Compaction
	case EmbeddingRequest:
		return ar.Embedding
	case RerankRequest:
		return ar.Rerank
	case DecisionRequest:
		return ar.Decision
	case OCRRequest:
		return ar.OCR
	case SpeechRequest:
		return ar.Speech
	case SpeechStreamRequest:
		return ar.SpeechStream
	case TranscriptionRequest:
		return ar.Transcription
	case TranscriptionStreamRequest:
		return ar.TranscriptionStream
	case ImageGenerationRequest:
		return ar.ImageGeneration
	case ImageGenerationStreamRequest:
		return ar.ImageGenerationStream
	case ImageEditRequest:
		return ar.ImageEdit
	case ImageEditStreamRequest:
		return ar.ImageEditStream
	case ImageVariationRequest:
		return ar.ImageVariation
	case VideoGenerationRequest:
		return ar.VideoGeneration
	case VideoEditRequest:
		return ar.VideoEdit
	case VideoRetrieveRequest:
		return ar.VideoRetrieve
	case VideoDownloadRequest:
		return ar.VideoDownload
	case VideoDeleteRequest:
		return ar.VideoDelete
	case VideoListRequest:
		return ar.VideoList
	case VideoRemixRequest:
		return ar.VideoRemix
	case BatchCreateRequest:
		return ar.BatchCreate
	case BatchListRequest:
		return ar.BatchList
	case BatchRetrieveRequest:
		return ar.BatchRetrieve
	case BatchCancelRequest:
		return ar.BatchCancel
	case BatchDeleteRequest:
		return ar.BatchDelete
	case BatchResultsRequest:
		return ar.BatchResults
	case FileUploadRequest:
		return ar.FileUpload
	case FileListRequest:
		return ar.FileList
	case FileRetrieveRequest:
		return ar.FileRetrieve
	case FileDeleteRequest:
		return ar.FileDelete
	case FileContentRequest:
		return ar.FileContent
	case ContainerCreateRequest:
		return ar.ContainerCreate
	case ContainerListRequest:
		return ar.ContainerList
	case ContainerRetrieveRequest:
		return ar.ContainerRetrieve
	case ContainerDeleteRequest:
		return ar.ContainerDelete
	case ContainerFileCreateRequest:
		return ar.ContainerFileCreate
	case ContainerFileListRequest:
		return ar.ContainerFileList
	case ContainerFileRetrieveRequest:
		return ar.ContainerFileRetrieve
	case ContainerFileContentRequest:
		return ar.ContainerFileContent
	case ContainerFileDeleteRequest:
		return ar.ContainerFileDelete
	case PassthroughRequest:
		return ar.Passthrough
	case PassthroughStreamRequest:
		return ar.PassthroughStream
	case WebSocketResponsesRequest:
		return ar.WebSocketResponses
	case RealtimeRequest:
		return ar.Realtime
	case LiveRequest:
		return ar.Live
	case LiveContentRequest:
		return ar.Live
	case LiveSidebandRequest:
		return ar.Live
	case CachedContentCreateRequest:
		return ar.CachedContentCreate
	case CachedContentListRequest:
		return ar.CachedContentList
	case CachedContentRetrieveRequest:
		return ar.CachedContentRetrieve
	case CachedContentUpdateRequest:
		return ar.CachedContentUpdate
	case CachedContentDeleteRequest:
		return ar.CachedContentDelete
	default:
		return false
	}
}

func (cpc *CustomProviderConfig) IsOperationAllowed(operation RequestType) bool {
	if cpc == nil || cpc.AllowedRequests == nil {
		return true
	}
	return cpc.AllowedRequests.IsOperationAllowed(operation)
}

const CacheControlInjectionLocationMessage = "message"

func (config *ProviderConfig) CheckAndSetDefaults() {
	if config.ConcurrencyAndBufferSize.Concurrency == 0 {
		config.ConcurrencyAndBufferSize.Concurrency = DefaultConcurrency
	}

	if config.ConcurrencyAndBufferSize.BufferSize == 0 {
		config.ConcurrencyAndBufferSize.BufferSize = DefaultBufferSize
	}

	if config.NetworkConfig.DefaultRequestTimeoutInSeconds <= 0 {
		config.NetworkConfig.DefaultRequestTimeoutInSeconds = DefaultRequestTimeoutInSeconds
	}

	if config.NetworkConfig.MaxRetries == 0 {
		config.NetworkConfig.MaxRetries = DefaultMaxRetries
	}

	if config.NetworkConfig.RetryBackoffInitial == 0 {
		config.NetworkConfig.RetryBackoffInitial = DefaultRetryBackoffInitial
	}

	if config.NetworkConfig.RetryBackoffMax == 0 {
		config.NetworkConfig.RetryBackoffMax = DefaultRetryBackoffMax
	}

	if config.NetworkConfig.StreamIdleTimeoutInSeconds <= 0 {
		config.NetworkConfig.StreamIdleTimeoutInSeconds = DefaultStreamIdleTimeoutInSeconds
	}

	if config.NetworkConfig.KeepAliveTimeoutInSeconds <= 0 {
		config.NetworkConfig.KeepAliveTimeoutInSeconds = DefaultKeepAliveTimeoutInSeconds
	}

	if config.NetworkConfig.MaxConnsPerHost <= 0 {
		config.NetworkConfig.MaxConnsPerHost = DefaultMaxConnsPerHost
	} else if config.NetworkConfig.MaxConnsPerHost > MaxConnsPerHostUpperBound {
		config.NetworkConfig.MaxConnsPerHost = MaxConnsPerHostUpperBound
	}

	if config.NetworkConfig.HTTP2PingIntervalInSeconds > HTTP2PingIntervalUpperBoundSeconds {
		config.NetworkConfig.HTTP2PingIntervalInSeconds = HTTP2PingIntervalUpperBoundSeconds
	}

	if config.NetworkConfig.ExtraHeaders != nil {
		headersCopy := make(map[string]SecretVar, len(config.NetworkConfig.ExtraHeaders))
		maps.Copy(headersCopy, config.NetworkConfig.ExtraHeaders)
		config.NetworkConfig.ExtraHeaders = headersCopy
	}

	if config.NetworkConfig.BetaHeaderOverrides != nil {
		overridesCopy := make(map[string]bool, len(config.NetworkConfig.BetaHeaderOverrides))
		maps.Copy(overridesCopy, config.NetworkConfig.BetaHeaderOverrides)
		config.NetworkConfig.BetaHeaderOverrides = overridesCopy
	}
}
