package schemas

import (
	"strings"
	"sync"
	"time"
)

type SpanEvent struct {
	Name       string
	Timestamp  time.Time
	Attributes map[string]any
}
type SpanKind string
type Span struct {
	SpanID     string
	ParentID   string
	TraceID    string
	Name       string
	Kind       SpanKind
	StartTime  time.Time
	EndTime    time.Time
	Status     SpanStatus
	StatusMsg  string
	Attributes map[string]any
	LLM        *LLMSpanData `json:"-"`
	A2A        *A2ASpanData `json:"-"`
	Enrichment *SpanEnrichment
	Events     []SpanEvent
	mu         sync.Mutex
}
type Trace struct {
	RequestID             string
	TraceID               string
	InternalID            string
	ParentID              string
	RootSpan              *Span
	Spans                 []*Span
	StartTime             time.Time
	EndTime               time.Time
	Attributes            map[string]any
	RequestHeaders        map[string]string
	PluginLogs            []PluginLogEntry
	redactionReplacements RedactionMapsByPhase
	mu                    sync.Mutex
}
type SpanStatus string

func (t *Trace) SetAttribute(key string, value any) {
	if value == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Attributes == nil {
		t.Attributes = make(map[string]any)
	}
	t.Attributes[key] = value
}

func (t *Trace) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.RequestID = ""
	t.TraceID = ""
	t.InternalID = ""
	t.ParentID = ""
	t.RootSpan = nil
	for i := range t.Spans {
		t.Spans[i] = nil
	}
	t.Spans = t.Spans[:0]
	t.StartTime = time.Time{}
	t.EndTime = time.Time{}
	t.Attributes = nil
	t.RequestHeaders = nil
	t.redactionReplacements = RedactionMapsByPhase{}
	for i := range t.PluginLogs {
		t.PluginLogs[i] = PluginLogEntry{}
	}
	t.PluginLogs = t.PluginLogs[:0]
}

func (s *Span) SetAttribute(key string, value any) {
	if s == nil || value == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	s.Attributes[key] = value
}

func (s *Span) AddEvent(event SpanEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, event)
}

func (s *Span) End(status SpanStatus, statusMsg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EndTime = time.Now()
	s.Status = status
	s.StatusMsg = statusMsg
}

func (s *Span) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SpanID = ""
	s.ParentID = ""
	s.TraceID = ""
	s.Name = ""
	s.Kind = SpanKindUnspecified
	s.StartTime = time.Time{}
	s.EndTime = time.Time{}
	s.Status = SpanStatusUnset
	s.StatusMsg = ""
	s.LLM = nil
	s.A2A = nil
	s.Enrichment = nil

	if len(s.Attributes) > 64 {
		s.Attributes = nil
	} else {
		clear(s.Attributes)
	}

	clear(s.Events)
	s.Events = s.Events[:0]
}

const (
	SpanKindUnspecified SpanKind = ""

	SpanKindLLMCall SpanKind = "llm.call"

	SpanKindPlugin SpanKind = "plugin"

	SpanKindMCPTool SpanKind = "mcp.tool"

	SpanKindA2AOperation SpanKind = "a2a.operation"

	SpanKindMCPClient SpanKind = "mcp.client"

	SpanKindRetry SpanKind = "retry"

	SpanKindFallback SpanKind = "fallback"

	SpanKindHTTPRequest SpanKind = "http.request"

	SpanKindEmbedding SpanKind = "embedding"

	SpanKindSpeech SpanKind = "speech"

	SpanKindTranscription SpanKind = "transcription"

	SpanKindInternal SpanKind = "internal"

	SpanKindRelay SpanKind = "relay.call"
)

const (
	SpanStatusUnset SpanStatus = "unset"

	SpanStatusOk SpanStatus = "ok"

	SpanStatusError SpanStatus = "error"
)

const (
	AttrProviderName  = "gen_ai.provider.name"
	AttrRequestModel  = "gen_ai.request.model"
	AttrOperationName = "gen_ai.operation.name"

	AttrMaxTokens        = "gen_ai.request.max_tokens"
	AttrTemperature      = "gen_ai.request.temperature"
	AttrTopP             = "gen_ai.request.top_p"
	AttrStopSequences    = "gen_ai.request.stop_sequences"
	AttrPresencePenalty  = "gen_ai.request.presence_penalty"
	AttrFrequencyPenalty = "gen_ai.request.frequency_penalty"
	AttrParallelToolCall = "gen_ai.request.parallel_tool_calls"
	AttrRequestUser      = "gen_ai.request.user"
	AttrBestOf           = "gen_ai.request.best_of"
	AttrEcho             = "gen_ai.request.echo"
	AttrLogitBias        = "gen_ai.request.logit_bias"
	AttrLogProbs         = "gen_ai.request.logprobs"
	AttrChoiceCount      = "gen_ai.request.choice.count"

	AttrEmbeddingsDimensionCount = "gen_ai.embeddings.dimension.count"
	AttrSeed                     = "gen_ai.request.seed"
	AttrSuffix                   = "gen_ai.request.suffix"
	AttrEncodingFormats          = "gen_ai.request.encoding_formats"
	AttrLanguage                 = "gen_ai.request.language"
	AttrPrompt                   = "gen_ai.request.prompt"
	AttrResponseFormat           = "gen_ai.request.response_format"
	AttrFormat                   = "gen_ai.request.format"
	AttrVoice                    = "gen_ai.request.voice"
	AttrMultiVoiceConfig         = "gen_ai.request.multi_voice_config"
	AttrInstructions             = "gen_ai.request.instructions"
	AttrSpeed                    = "gen_ai.request.speed"
	AttrMessageCount             = "gen_ai.request.message_count"

	AttrResponseID       = "gen_ai.response.id"
	AttrResponseModel    = "gen_ai.response.model"
	AttrFinishReason     = "gen_ai.response.finish_reason"
	AttrFinishReasons    = "gen_ai.response.finish_reasons"
	AttrSystemFprint     = "gen_ai.response.system_fingerprint"
	AttrServiceTier      = "gen_ai.response.service_tier"
	AttrCreated          = "gen_ai.response.created"
	AttrObject           = "gen_ai.response.object"
	AttrTimeToFirstChunk = "gen_ai.response.time_to_first_chunk"
	AttrTotalChunks      = "gen_ai.response.total_chunks"

	AttrPluginInvocations     = "plugin.invocation_count"
	AttrPluginAvgDurationMs   = "plugin.avg_duration_ms"
	AttrPluginTotalDurationMs = "plugin.total_duration_ms"
	AttrPluginErrorCount      = "plugin.error_count"

	AttrTotalTokens  = "gen_ai.usage.total_tokens"
	AttrInputTokens  = "gen_ai.usage.input_tokens"
	AttrOutputTokens = "gen_ai.usage.output_tokens"
	AttrUsageCost    = "gen_ai.usage.cost"

	AttrBifrostCostInput      = "bifrost.cost.input"
	AttrBifrostCostOutput     = "bifrost.cost.output"
	AttrBifrostCostAdditional = "bifrost.cost.additional"

	AttrBifrostCostInputText        = "bifrost.cost.input.text"
	AttrBifrostCostInputAudio       = "bifrost.cost.input.audio"
	AttrBifrostCostInputImage       = "bifrost.cost.input.image"
	AttrBifrostCostInputCachedRead  = "bifrost.cost.input.cached_read"
	AttrBifrostCostInputCachedWrite = "bifrost.cost.input.cached_write"
	AttrBifrostCostInputRequest     = "bifrost.cost.input.request"

	AttrBifrostCostOutputText      = "bifrost.cost.output.text"
	AttrBifrostCostOutputAudio     = "bifrost.cost.output.audio"
	AttrBifrostCostOutputImage     = "bifrost.cost.output.image"
	AttrBifrostCostOutputReasoning = "bifrost.cost.output.reasoning"
	AttrBifrostCostOutputCitation  = "bifrost.cost.output.citation"
	AttrBifrostCostOutputSearch    = "bifrost.cost.output.search_queries"

	AttrBifrostCostGuardrail     = "bifrost.cost.additional.guardrail"
	AttrBifrostCostMCP           = "bifrost.cost.additional.mcp"
	AttrBifrostCostSemanticCache = "bifrost.cost.additional.semantic_cache"
	AttrBifrostCostRouting       = "bifrost.cost.additional.routing"

	AttrUsageCacheReadInputTokens     = "gen_ai.usage.cache_read.input_tokens"
	AttrUsageCacheCreationInputTokens = "gen_ai.usage.cache_creation.input_tokens"

	AttrUsageReasoningOutputTokens = "gen_ai.usage.reasoning.output_tokens"

	AttrPromptTokenDetailsText          = "gen_ai.usage.prompt_token_details.text_tokens"
	AttrPromptTokenDetailsAudio         = "gen_ai.usage.prompt_token_details.audio_tokens"
	AttrPromptTokenDetailsImage         = "gen_ai.usage.prompt_token_details.image_tokens"
	AttrPromptTokenDetailsCachedWrite5m = "gen_ai.usage.prompt_token_details.cached_write_tokens_5m"
	AttrPromptTokenDetailsCachedWrite1h = "gen_ai.usage.prompt_token_details.cached_write_tokens_1h"
	AttrCompletionTokenDetailsText      = "gen_ai.usage.completion_token_details.text_tokens"
	AttrCompletionTokenDetailsAudio     = "gen_ai.usage.completion_token_details.audio_tokens"
	AttrCompletionTokenDetailsImage     = "gen_ai.usage.completion_token_details.image_tokens"
	AttrCompletionTokenDetailsAccept    = "gen_ai.usage.completion_token_details.accepted_prediction_tokens"
	AttrCompletionTokenDetailsReject    = "gen_ai.usage.completion_token_details.rejected_prediction_tokens"
	AttrCompletionTokenDetailsCite      = "gen_ai.usage.completion_token_details.citation_tokens"
	AttrCompletionTokenDetailsSearch    = "gen_ai.usage.completion_token_details.num_search_queries"

	AttrError     = "gen_ai.error"
	AttrErrorCode = "gen_ai.error.code"

	AttrHTTPResponseStatusCode = "http.response.status_code"

	AttrInputText      = "gen_ai.input.text"
	AttrInputMessages  = "gen_ai.input.messages"
	AttrInputSpeech    = "gen_ai.input.speech"
	AttrInputEmbedding = "gen_ai.input.embedding"
	AttrOutputMessages = "gen_ai.output.messages"

	AttrExtraHeaderPrefix = "gen_ai.request.extra_header."

	AttrPromptCacheKey      = "gen_ai.request.prompt_cache_key"
	AttrReasoningEffort     = "gen_ai.request.reasoning_effort"
	AttrReasoningSummary    = "gen_ai.request.reasoning_summary"
	AttrReasoningGenSummary = "gen_ai.request.reasoning_generate_summary"
	AttrSafetyIdentifier    = "gen_ai.request.safety_identifier"
	AttrStore               = "gen_ai.request.store"
	AttrTextVerbosity       = "gen_ai.request.text_verbosity"
	AttrTextFormatType      = "gen_ai.request.text_format_type"
	AttrTopLogProbs         = "gen_ai.request.top_logprobs"
	AttrToolChoiceType      = "gen_ai.request.tool_choice_type"
	AttrToolChoiceName      = "gen_ai.request.tool_choice_name"
	AttrTools               = "gen_ai.request.tools"
	AttrTruncation          = "gen_ai.request.truncation"

	AttrRespInclude          = "gen_ai.responses.include"
	AttrRespMaxOutputTokens  = "gen_ai.responses.max_output_tokens"
	AttrRespMaxToolCalls     = "gen_ai.responses.max_tool_calls"
	AttrRespMetadata         = "gen_ai.responses.metadata"
	AttrRespPreviousRespID   = "gen_ai.responses.previous_response_id"
	AttrRespPromptCacheKey   = "gen_ai.responses.prompt_cache_key"
	AttrRespReasoningText    = "gen_ai.responses.reasoning"
	AttrRespReasoningEffort  = "gen_ai.responses.reasoning_effort"
	AttrRespReasoningGenSum  = "gen_ai.responses.reasoning_generate_summary"
	AttrRespSafetyIdentifier = "gen_ai.responses.safety_identifier"
	AttrRespStore            = "gen_ai.responses.store"
	AttrRespTemperature      = "gen_ai.responses.temperature"
	AttrRespTextVerbosity    = "gen_ai.responses.text_verbosity"
	AttrRespTextFormatType   = "gen_ai.responses.text_format_type"
	AttrRespTopLogProbs      = "gen_ai.responses.top_logprobs"
	AttrRespTopP             = "gen_ai.responses.top_p"
	AttrRespToolChoiceType   = "gen_ai.responses.tool_choice_type"
	AttrRespToolChoiceName   = "gen_ai.responses.tool_choice_name"
	AttrRespTruncation       = "gen_ai.responses.truncation"
	AttrRespTools            = "gen_ai.responses.tools"

	AttrBatchID             = "gen_ai.batch.id"
	AttrBatchStatus         = "gen_ai.batch.status"
	AttrBatchObject         = "gen_ai.batch.object"
	AttrBatchEndpoint       = "gen_ai.batch.endpoint"
	AttrBatchInputFileID    = "gen_ai.batch.input_file_id"
	AttrBatchOutputFileID   = "gen_ai.batch.output_file_id"
	AttrBatchErrorFileID    = "gen_ai.batch.error_file_id"
	AttrBatchCompletionWin  = "gen_ai.batch.completion_window"
	AttrBatchCreatedAt      = "gen_ai.batch.created_at"
	AttrBatchExpiresAt      = "gen_ai.batch.expires_at"
	AttrBatchRequestsCount  = "gen_ai.batch.requests_count"
	AttrBatchDataCount      = "gen_ai.batch.data_count"
	AttrBatchResultsCount   = "gen_ai.batch.results_count"
	AttrBatchHasMore        = "gen_ai.batch.has_more"
	AttrBatchMetadata       = "gen_ai.batch.metadata"
	AttrBatchLimit          = "gen_ai.batch.limit"
	AttrBatchAfter          = "gen_ai.batch.after"
	AttrBatchBeforeID       = "gen_ai.batch.before_id"
	AttrBatchAfterID        = "gen_ai.batch.after_id"
	AttrBatchPageToken      = "gen_ai.batch.page_token"
	AttrBatchPageSize       = "gen_ai.batch.page_size"
	AttrBatchCountTotal     = "gen_ai.batch.request_counts.total"
	AttrBatchCountCompleted = "gen_ai.batch.request_counts.completed"
	AttrBatchCountFailed    = "gen_ai.batch.request_counts.failed"
	AttrBatchFirstID        = "gen_ai.batch.first_id"
	AttrBatchLastID         = "gen_ai.batch.last_id"
	AttrBatchInProgressAt   = "gen_ai.batch.in_progress_at"
	AttrBatchFinalizingAt   = "gen_ai.batch.finalizing_at"
	AttrBatchCompletedAt    = "gen_ai.batch.completed_at"
	AttrBatchFailedAt       = "gen_ai.batch.failed_at"
	AttrBatchExpiredAt      = "gen_ai.batch.expired_at"
	AttrBatchCancellingAt   = "gen_ai.batch.cancelling_at"
	AttrBatchCancelledAt    = "gen_ai.batch.cancelled_at"
	AttrBatchNextCursor     = "gen_ai.batch.next_cursor"

	AttrInputTokenDetailsText  = "gen_ai.usage.input_token_details.text_tokens"
	AttrInputTokenDetailsAudio = "gen_ai.usage.input_token_details.audio_tokens"

	AttrInputTokenDetailsImage         = "gen_ai.usage.input_token_details.image_tokens"
	AttrInputTokenDetailsCachedWrite   = "gen_ai.usage.input_token_details.cached_write_tokens"
	AttrInputTokenDetailsCachedWrite5m = "gen_ai.usage.input_token_details.cached_write_tokens_5m"
	AttrInputTokenDetailsCachedWrite1h = "gen_ai.usage.input_token_details.cached_write_tokens_1h"
	AttrOutputTokenDetailsText         = "gen_ai.usage.output_token_details.text_tokens"
	AttrOutputTokenDetailsAudio        = "gen_ai.usage.output_token_details.audio_tokens"
	AttrOutputTokenDetailsImage        = "gen_ai.usage.output_token_details.image_tokens"
	AttrOutputTokenDetailsAccept       = "gen_ai.usage.output_token_details.accepted_prediction_tokens"
	AttrOutputTokenDetailsReject       = "gen_ai.usage.output_token_details.rejected_prediction_tokens"
	AttrOutputTokenDetailsCite         = "gen_ai.usage.output_token_details.citation_tokens"
	AttrOutputTokenDetailsSearch       = "gen_ai.usage.output_token_details.num_search_queries"

	AttrToolName          = "gen_ai.tool.name"
	AttrToolCallID        = "gen_ai.tool.call.id"
	AttrToolCallArguments = "gen_ai.tool.call.arguments"
	AttrToolCallResult    = "gen_ai.tool.call.result"
	AttrToolType          = "gen_ai.tool.type"

	AttrMCPMethodName    = "mcp.method.name"
	AttrNetworkTransport = "network.transport"

	AttrBifrostMCPToolDurationMs = "bifrost.mcp.tool.duration_ms"

	AttrBifrostMCPClientName = "bifrost.mcp.client.name"

	AttrBifrostMCPToolIsError = "bifrost.mcp.tool.is_error"

	AttrBifrostA2AOperationName = "bifrost.a2a.operation.name"
	AttrA2AMethodName           = "a2a.method.name"
	AttrA2ATaskID               = "a2a.task.id"
	AttrA2ATaskState            = "a2a.task.state"
	AttrA2AMessageID            = "a2a.message.id"
	AttrA2AArtifactID           = "a2a.artifact.id"
	AttrAgentName               = "gen_ai.agent.name"
	AttrConversationID          = "gen_ai.conversation.id"

	AttrBifrostA2AOperationDurationMs = "bifrost.a2a.operation.duration_ms"

	AttrBifrostA2AUpstreamTransport = "bifrost.a2a.upstream.transport"

	AttrHTTPRequestMethod              = "http.request.method"
	AttrBifrostRelayName               = "bifrost.relay.name"
	AttrBifrostRelayRoutePath          = "bifrost.relay.route.path"
	AttrBifrostRelayBillable           = "bifrost.relay.billable"
	AttrBifrostRelayCounted            = "bifrost.relay.counted"
	AttrBifrostRelayStreamed           = "bifrost.relay.streamed"
	AttrBifrostRelayCost               = "bifrost.relay.cost"
	AttrBifrostRelayBytesOut           = "bifrost.relay.bytes_out"
	AttrBifrostRelayUpstreamDurationMs = "bifrost.relay.upstream.duration_ms"

	RelayMethodLabelOther = "_OTHER"

	AttrBifrostUpstreamDurationMs = "bifrost.upstream.duration_ms"

	AttrBifrostOverheadDurationMs = "bifrost.overhead.duration_ms"

	AttrBifrostStreamParseMs        = "bifrost.stream.parse_ms"
	AttrBifrostStreamConvertMs      = "bifrost.stream.convert_ms"
	AttrBifrostStreamBackpressureMs = "bifrost.stream.backpressure_ms"

	AttrBifrostStreamTransportCPUMs = "bifrost.stream.transport_cpu_ms"
	AttrBifrostStreamClientWriteMs  = "bifrost.stream.client_write_ms"

	AttrBifrostWorkerHandoffMs = "bifrost.worker.handoff_ms"

	AttrBifrostBackground = "bifrost.background"

	AttrBifrostTimeToFirstTokenMs = "bifrost.response.time_to_first_token_ms"

	AttrBifrostContentLoggingDisabled = "bifrost.content_logging.disabled"

	AttrBifrostProviderName        = "bifrost.provider.name"
	AttrBifrostRequestID           = "bifrost.request.id"
	AttrBifrostVirtualKeyID        = "bifrost.virtual_key.id"
	AttrBifrostVirtualKeyName      = "bifrost.virtual_key.name"
	AttrBifrostSelectedKeyID       = "bifrost.selected_key.id"
	AttrBifrostSelectedKeyName     = "bifrost.selected_key.name"
	AttrBifrostRoutingRuleID       = "bifrost.routing_rule.id"
	AttrBifrostRoutingRuleName     = "bifrost.routing_rule.name"
	AttrBifrostTeamID              = "bifrost.team.id"
	AttrBifrostTeamName            = "bifrost.team.name"
	AttrBifrostCustomerID          = "bifrost.customer.id"
	AttrBifrostCustomerName        = "bifrost.customer.name"
	AttrBifrostBusinessUnitID      = "bifrost.business_unit.id"
	AttrBifrostBusinessUnitName    = "bifrost.business_unit.name"
	AttrBifrostProjectID           = "bifrost.project.id"
	AttrBifrostProjectName         = "bifrost.project.name"
	AttrBifrostTeamIDs             = "bifrost.team.ids"
	AttrBifrostTeamNames           = "bifrost.team.names"
	AttrBifrostCustomerIDs         = "bifrost.customer.ids"
	AttrBifrostCustomerNames       = "bifrost.customer.names"
	AttrBifrostBusinessUnitIDs     = "bifrost.business_unit.ids"
	AttrBifrostBusinessUnitNames   = "bifrost.business_unit.names"
	AttrBifrostUserID              = "bifrost.user.id"
	AttrBifrostUserName            = "bifrost.user.name"
	AttrBifrostUserEmail           = "bifrost.user.email"
	AttrBifrostApp                 = "bifrost.app"
	AttrBifrostRawRequest          = "bifrost.raw_request"
	AttrBifrostRawResponse         = "bifrost.raw_response"
	AttrBifrostRetries             = "bifrost.retries"
	AttrBifrostFallbackIndex       = "bifrost.fallback_index"
	AttrBifrostAlias               = "bifrost.alias"
	AttrBifrostRoutingEngineUsed   = "bifrost.routing_engine_used"
	AttrBifrostComplexityTier      = "bifrost.complexity_tier"
	AttrBifrostComplexityMechanism = "bifrost.complexity_mechanism"
	AttrBifrostComplexityScore     = "bifrost.complexity_score"
	AttrBifrostStopSequencesJoined = "bifrost.request.stop_sequences"

	AttrBifrostPromptID      = "bifrost.prompt.id"
	AttrBifrostPromptName    = "bifrost.prompt.name"
	AttrBifrostPromptVersion = "bifrost.prompt.version"

	AttrBifrostErrorType = "bifrost.error.type"

	AttrErrorTypeSpec = "error.type"

	AttrLegacyRequestType = "request.type"

	AttrFileID             = "gen_ai.file.id"
	AttrFileObject         = "gen_ai.file.object"
	AttrFileFilename       = "gen_ai.file.filename"
	AttrFilePurpose        = "gen_ai.file.purpose"
	AttrFileBytes          = "gen_ai.file.bytes"
	AttrFileCreatedAt      = "gen_ai.file.created_at"
	AttrFileStatus         = "gen_ai.file.status"
	AttrFileStorageBackend = "gen_ai.file.storage_backend"
	AttrFileDataCount      = "gen_ai.file.data_count"
	AttrFileHasMore        = "gen_ai.file.has_more"
	AttrFileDeleted        = "gen_ai.file.deleted"
	AttrFileContentType    = "gen_ai.file.content_type"
	AttrFileContentBytes   = "gen_ai.file.content_bytes"
	AttrFileLimit          = "gen_ai.file.limit"
	AttrFileAfter          = "gen_ai.file.after"
	AttrFileOrder          = "gen_ai.file.order"
)

const RedactedAttrValue = "REDACTED"

func IsSensitiveHeader(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))

	switch normalized {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",

		"x-bf-vk",

		"ocp-apim-subscription-key":
		return true
	}

	return strings.Contains(normalized, "api-key") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "assertion") ||
		strings.Contains(normalized, "jwt") ||
		strings.HasPrefix(normalized, "cf-access-") ||
		strings.HasPrefix(normalized, "x-amzn-oidc-") ||
		strings.HasSuffix(normalized, "-token") ||
		strings.HasSuffix(normalized, "_token")
}
