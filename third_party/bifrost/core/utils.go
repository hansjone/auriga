package bifrost

import (
	"context"

	"errors"
	"fmt"
	"math/rand"

	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

var pluginSpanNameCache sync.Map
var standardProvidersSet = func() map[schemas.ModelProvider]struct{} {
	m := make(map[schemas.ModelProvider]struct{}, len(schemas.StandardProviders))
	for _, p := range schemas.StandardProviders {
		m[p] = struct{}{}
	}
	return m
}()
var supportedBaseProvidersSet = func() map[schemas.ModelProvider]struct{} {
	m := make(map[schemas.ModelProvider]struct{}, len(schemas.SupportedBaseProviders))
	for _, p := range schemas.SupportedBaseProviders {
		m[p] = struct{}{}
	}
	return m
}()

type chatStreamIdentity struct {
	id      string
	model   string
	created int
}
type pluginSpanNameSet struct {
	prehook            string
	prerequesthook     string
	posthook           string
	mcpPrehook         string
	mcpPosthook        string
	mcpConnectPrehook  string
	mcpConnectPosthook string
}

const (
	ProviderAutoResolveErrorMessage = schemas.ProviderAutoResolveErrorMessage
	ModelAutoResolveErrorMessage    = schemas.ModelAutoResolveErrorMessage
)

func isModelRequired(reqType schemas.RequestType) bool {
	return reqType == schemas.TextCompletionRequest || reqType == schemas.TextCompletionStreamRequest || reqType == schemas.ChatCompletionRequest || reqType == schemas.ChatCompletionStreamRequest || reqType == schemas.ResponsesRequest || reqType == schemas.ResponsesStreamRequest || reqType == schemas.SpeechRequest || reqType == schemas.SpeechStreamRequest || reqType == schemas.TranscriptionRequest || reqType == schemas.TranscriptionStreamRequest || reqType == schemas.EmbeddingRequest || reqType == schemas.ImageGenerationRequest || reqType == schemas.ImageGenerationStreamRequest || reqType == schemas.VideoGenerationRequest
}

func Ptr[T any](v T) *T {
	return &v
}

func providerRequiresKey(customConfig *schemas.CustomProviderConfig) bool {

	if customConfig != nil && customConfig.IsKeyLess && customConfig.BaseProviderType != schemas.Bedrock {
		return false
	}
	return true
}

func CanProviderKeyValueBeEmpty(providerKey schemas.ModelProvider) bool {
	return providerKey == schemas.Vertex || providerKey == schemas.Bedrock || providerKey == schemas.BedrockMantle || providerKey == schemas.SageMaker || providerKey == schemas.VLLM || providerKey == schemas.Azure || providerKey == schemas.Ollama || providerKey == schemas.SGL || providerKey == schemas.Databricks
}

func isKeySkippingAllowed(baseProvider schemas.ModelProvider, requestType schemas.RequestType) bool {
	if baseProvider == schemas.Anthropic {
		return true
	}
	return baseProvider == schemas.OpenAI && (requestType == schemas.PassthroughRequest || requestType == schemas.PassthroughStreamRequest)
}

func calculateBackoff(attempt int, config *schemas.ProviderConfig) time.Duration {

	backoff := min(config.NetworkConfig.RetryBackoffInitial*time.Duration(1<<uint(attempt)), config.NetworkConfig.RetryBackoffMax)

	jitter := float64(backoff) * (0.8 + 0.4*rand.Float64())
	result := time.Duration(jitter)

	return min(result, config.NetworkConfig.RetryBackoffMax)
}

func waitRetryBackoff(ctx context.Context, backoff time.Duration) (cancelled bool) {
	backoffTimer := time.NewTimer(backoff)
	select {
	case <-backoffTimer.C:
	case <-ctx.Done():
		backoffTimer.Stop()
	}
	return ctx.Err() != nil
}

func validateRequestAfterPreRequestHooks(req *schemas.BifrostRequest) *schemas.BifrostError {
	if req == nil {
		return newBifrostErrorFromMsg("bifrost request cannot be nil")
	}
	provider, model, _ := req.GetRequestFields()
	if provider == "" {
		return newBifrostErrorFromMsg(ProviderAutoResolveErrorMessage)
	}
	if isModelRequired(req.RequestType) && model == "" {
		return newBifrostErrorFromMsg(ModelAutoResolveErrorMessage)
	}
	return nil
}

func validateKey(providerKey schemas.ModelProvider, key *schemas.Key) error {

	switch providerKey {

	}

	if key.OAuthKeyConfig != nil {
		if !ProviderSupportsOAuthKeyConfig(providerKey) {
			return fmt.Errorf("oauth_key_config is only supported on openai and on custom providers whose base_provider_type is openai")
		}
		if strings.TrimSpace(key.Value.GetValue()) == "" {
			if err := key.OAuthKeyConfig.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func ProviderSupportsOAuthKeyConfig(providerKey schemas.ModelProvider) bool {
	return providerKey == schemas.OpenAI
}

func keyValueMayBeEmpty(providerKey schemas.ModelProvider, key *schemas.Key) bool {
	return CanProviderKeyValueBeEmpty(providerKey) || (key.OAuthKeyConfig != nil && ProviderSupportsOAuthKeyConfig(providerKey))
}

func routingErrorSummary(e *schemas.BifrostError) string {
	if e == nil {
		return "unknown error"
	}
	parts := make([]string, 0, 2)
	if e.Error != nil && e.Error.Type != nil && *e.Error.Type != "" {
		parts = append(parts, *e.Error.Type)
	} else if e.Type != nil && *e.Type != "" {
		parts = append(parts, *e.Type)
	}
	if e.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("HTTP %d", *e.StatusCode))
	}
	if len(parts) == 0 {
		return "request failed"
	}
	return strings.Join(parts, " ")
}

func newBifrostError(err error) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: err.Error(),
			Error:   err,
		},
	}
}

func newBifrostErrorFromMsg(message string) *schemas.BifrostError {
	return &schemas.BifrostError{
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: message,
		},
	}
}

func newBifrostCtxDoneError(ctx *schemas.BifrostContext, stage string) *schemas.BifrostError {
	var statusCode int
	var errorType string
	var message string

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		statusCode = 504
		errorType = schemas.RequestTimedOut
		message = fmt.Sprintf("request timed out %s: %v", stage, ctx.Err())
	} else {
		statusCode = 499
		errorType = schemas.RequestCancelled
		message = fmt.Sprintf("request cancelled %s: %v", stage, ctx.Err())
	}

	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		AllowFallbacks: new(false),
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Message: message,
			Error:   ctx.Err(),
		},
	}
}

func newBifrostQueueFullError() *schemas.BifrostError {
	statusCode := 503
	errorType := schemas.RequestDropped
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Message: "request dropped: queue is full",
		},
	}
}

func newBifrostMessageChan(message *schemas.BifrostResponse) chan *schemas.BifrostStreamChunk {

	ch := make(chan *schemas.BifrostStreamChunk, 1)
	ch <- &schemas.BifrostStreamChunk{
		BifrostTextCompletionResponse:      message.TextCompletionResponse,
		BifrostChatResponse:                message.ChatResponse,
		BifrostResponsesStreamResponse:     message.ResponsesStreamResponse,
		BifrostSpeechStreamResponse:        message.SpeechStreamResponse,
		BifrostTranscriptionStreamResponse: message.TranscriptionStreamResponse,
	}
	close(ch)
	return ch
}

func stampRequestedRoute(ctx *schemas.BifrostContext, requested schemas.Route) {
	model := requested.Model
	if model == "" {
		if metadata, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMetadata).(*schemas.LargePayloadMetadata); metadata != nil {
			model = metadata.Model
		}
	}
	ctx.SetValue(schemas.BifrostContextKeyRequestedProvider, requested.Provider)
	ctx.SetValue(schemas.BifrostContextKeyRequestedModel, model)
}

func agentContinuationRoute(ctx *schemas.BifrostContext) *schemas.RoutingInfo {
	route, _ := ctx.Value(schemas.BifrostContextKeyMCPAgentServedRoute).(*schemas.RoutingInfo)
	return route
}

func requestedRouteFromContext(ctx *schemas.BifrostContext) schemas.Route {
	provider, _ := ctx.Value(schemas.BifrostContextKeyRequestedProvider).(schemas.ModelProvider)
	model, _ := ctx.Value(schemas.BifrostContextKeyRequestedModel).(string)
	return schemas.Route{Provider: provider, Model: model}
}

func layerFallbackIdentity(ctx *schemas.BifrostContext, info *schemas.RoutingInfo) {
	if fallbackIndex, _ := ctx.Value(schemas.BifrostContextKeyFallbackIndex).(int); fallbackIndex <= 0 {
		return
	}
	info.IsFallback = true
	prev, ok := ctx.Value(schemas.BifrostContextKeyRoutingInfo).(schemas.RoutingInfo)
	if !ok {
		return
	}
	if prev.IsFallback && prev.PrimaryProvider != nil {
		info.PrimaryProvider = prev.PrimaryProvider
		info.PrimaryModel = prev.PrimaryModel
	} else if prev.Provider != "" {
		primaryProvider, primaryModel := prev.Provider, prev.Model
		info.PrimaryProvider = &primaryProvider
		info.PrimaryModel = &primaryModel
	}
}

func clearCtxForFallback(ctx *schemas.BifrostContext) {
	ctx.ClearValue(schemas.BifrostContextKeyAPIKeyID)
	ctx.ClearValue(schemas.BifrostContextKeyAPIKeyName)
	ctx.ClearValue(schemas.BifrostContextKeyDirectKey)
	ctx.ClearValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID)
	ctx.ClearValue(schemas.BifrostContextKeyGovernanceIncludeOnlyKeys)
	ctx.ClearValue(schemas.BifrostContextKeyChangeRequestType)
	ctx.ClearValue(schemas.BifrostContextKeyAttemptTrail)
	ctx.ClearValue(schemas.BifrostContextKeyLoadBalancerAttempt)
	ctx.ClearValue(schemas.BifrostContextKeyStreamEndIndicator)
	ctx.ClearValue(schemas.BifrostContextKeyConnectionClosed)
	ctx.ClearValue(schemas.BifrostContextKeyStreamBodyExhausted)
	ctx.ClearValue(schemas.BifrostContextKeyStreamParkedAfterFinish)
	ctx.ClearValue(schemas.BifrostContextKeyStreamAttemptAbort)
	ctx.ClearValue(schemas.BifrostContextKeySupportsAssistantPrefill)
	ctx.ClearValue(schemas.BifrostContextKeyDisableStore)

	ctx.ClearValue(schemas.BifrostContextKeyProviderResponseHeaders)

	ctx.ClearValue(schemas.BifrostContextKeySSEReaderFactory)
}

func restoreProviderResponseHeaders(ctx *schemas.BifrostContext, headers map[string]string) {
	if headers == nil {
		ctx.ClearValue(schemas.BifrostContextKeyProviderResponseHeaders)
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)
}

const virtualKeyHeader = "x-bf-vk"

func extraHeaderSpanAttribute(name string, values []string) (any, bool) {
	if name == "" || len(values) == 0 {
		return nil, false
	}
	if strings.EqualFold(name, virtualKeyHeader) {
		return nil, false
	}
	if schemas.IsSensitiveHeader(name) {
		return schemas.RedactedAttrValue, true
	}
	if len(values) == 1 {
		return values[0], true
	}
	return values, true
}

func IsSupportedBaseProvider(providerKey schemas.ModelProvider) bool {
	_, ok := supportedBaseProvidersSet[providerKey]
	return ok
}

func IsStandardProvider(providerKey schemas.ModelProvider) bool {
	_, ok := standardProvidersSet[providerKey]
	return ok
}

func IsStreamRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.TextCompletionStreamRequest || reqType == schemas.ChatCompletionStreamRequest || reqType == schemas.ResponsesStreamRequest || reqType == schemas.ResponsesRetrieveStreamRequest || reqType == schemas.SpeechStreamRequest || reqType == schemas.TranscriptionStreamRequest || reqType == schemas.ImageGenerationStreamRequest || reqType == schemas.ImageEditStreamRequest || reqType == schemas.PassthroughStreamRequest || reqType == schemas.WebSocketResponsesRequest || reqType == schemas.RealtimeRequest
}

func GetTracerFromContext(ctx *schemas.BifrostContext) (schemas.Tracer, string, error) {
	tracer, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
	if !ok || tracer == nil {
		return nil, "", fmt.Errorf("tracer not found in context")
	}
	traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string)
	if !ok || traceID == "" {
		return nil, "", fmt.Errorf("traceID not found in context")
	}
	return tracer, traceID, nil
}

func isBatchRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.BatchCreateRequest || reqType == schemas.BatchListRequest || reqType == schemas.BatchRetrieveRequest || reqType == schemas.BatchCancelRequest || reqType == schemas.BatchDeleteRequest || reqType == schemas.BatchResultsRequest
}

func isFileRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.FileUploadRequest || reqType == schemas.FileListRequest || reqType == schemas.FileRetrieveRequest || reqType == schemas.FileDeleteRequest || reqType == schemas.FileContentRequest
}

func isCachedContentRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.CachedContentCreateRequest || reqType == schemas.CachedContentListRequest ||
		reqType == schemas.CachedContentRetrieveRequest || reqType == schemas.CachedContentUpdateRequest ||
		reqType == schemas.CachedContentDeleteRequest
}

func isContainerRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.ContainerCreateRequest || reqType == schemas.ContainerListRequest ||
		reqType == schemas.ContainerRetrieveRequest || reqType == schemas.ContainerDeleteRequest ||
		reqType == schemas.ContainerFileCreateRequest || reqType == schemas.ContainerFileListRequest ||
		reqType == schemas.ContainerFileRetrieveRequest || reqType == schemas.ContainerFileContentRequest ||
		reqType == schemas.ContainerFileDeleteRequest
}

func isModellessVideoRequestType(reqType schemas.RequestType) bool {
	switch reqType {
	case schemas.VideoRetrieveRequest, schemas.VideoDownloadRequest, schemas.VideoListRequest,
		schemas.VideoDeleteRequest, schemas.VideoRemixRequest, schemas.VideoEditRequest:
		return true
	default:
		return false
	}
}

func isPassthroughRequestType(reqType schemas.RequestType) bool {
	return reqType == schemas.PassthroughRequest || reqType == schemas.PassthroughStreamRequest
}

func isResponsesLifecycleRequestType(reqType schemas.RequestType) bool {
	switch reqType {
	case schemas.ResponsesRetrieveRequest, schemas.ResponsesRetrieveStreamRequest, schemas.ResponsesDeleteRequest, schemas.ResponsesCancelRequest, schemas.ResponsesInputItemsRequest:
		return true
	default:
		return false
	}
}

func IsFinalChunk(ctx *schemas.BifrostContext) bool {
	if ctx == nil {
		return false
	}

	isStreamEndIndicator := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator)
	if isStreamEndIndicator == nil {
		return false
	}

	if f, ok := isStreamEndIndicator.(bool); ok {
		return f
	}

	return false
}

func GetResponseFields(result *schemas.BifrostResponse, err *schemas.BifrostError) (requestType schemas.RequestType, provider schemas.ModelProvider, originalModel string, resolvedModel string) {
	if result != nil {
		extraFields := result.GetExtraFields()
		return extraFields.RequestType, extraFields.Provider, extraFields.OriginalModelRequested, extraFields.ResolvedModelUsed
	}
	if err != nil {
		return err.ExtraFields.RequestType, err.ExtraFields.Provider, err.ExtraFields.OriginalModelRequested, err.ExtraFields.ResolvedModelUsed
	}
	return
}

func sanitizeSpanName(name string) string {
	return schemas.SanitizePluginSpanName(name)
}

func pluginSpanNamesFor(name string) *pluginSpanNameSet {
	if v, ok := pluginSpanNameCache.Load(name); ok {
		return v.(*pluginSpanNameSet)
	}
	s := sanitizeSpanName(name)
	set := &pluginSpanNameSet{
		prehook:            "plugin." + s + ".prehook",
		prerequesthook:     "plugin." + s + ".prerequesthook",
		posthook:           "plugin." + s + ".posthook",
		mcpPrehook:         "plugin." + s + ".mcp_prehook",
		mcpPosthook:        "plugin." + s + ".mcp_posthook",
		mcpConnectPrehook:  "plugin." + s + ".mcp_connect_prehook",
		mcpConnectPosthook: "plugin." + s + ".mcp_connect_posthook",
	}
	if v, loaded := pluginSpanNameCache.LoadOrStore(name, set); loaded {
		return v.(*pluginSpanNameSet)
	}
	return set
}

func wrapConvertedStreamPostHookRunner(postHookRunner schemas.PostHookRunner, targetType schemas.RequestType) schemas.PostHookRunner {
	roleSent := false
	var identity chatStreamIdentity
	return func(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		if result != nil {
			switch targetType {
			case schemas.ChatCompletionRequest:

				if result.ChatResponse != nil {
					if converted := result.ChatResponse.ToBifrostTextCompletionResponse(); converted != nil {
						converted.ExtraFields.ConvertedRequestType = targetType
						result = &schemas.BifrostResponse{TextCompletionResponse: converted}
					}
				}
			case schemas.ResponsesRequest:

				if result.ResponsesStreamResponse != nil {
					if converted := result.ResponsesStreamResponse.ToBifrostChatResponse(); converted != nil {
						roleSent = setAssistantRoleOnce(converted, roleSent)
						identity.apply(converted)
						converted.ExtraFields.ConvertedRequestType = targetType
						result = &schemas.BifrostResponse{ChatResponse: converted}
					}
				}
			}
		}
		return postHookRunner(ctx, result, bifrostErr)
	}
}

func (s *chatStreamIdentity) apply(resp *schemas.BifrostChatResponse) {
	if resp.ID == "" {
		resp.ID = s.id
	} else {
		s.id = resp.ID
	}
	if resp.Model == "" {
		resp.Model = s.model
	} else {
		s.model = resp.Model
	}
	if resp.Created == 0 {
		resp.Created = s.created
	} else {
		s.created = resp.Created
	}
}

func setAssistantRoleOnce(resp *schemas.BifrostChatResponse, roleSent bool) bool {
	if len(resp.Choices) == 0 || resp.Choices[0].ChatStreamResponseChoice == nil || resp.Choices[0].Delta == nil {
		return roleSent
	}
	if roleSent {
		resp.Choices[0].Delta.Role = nil
	} else {
		resp.Choices[0].Delta.Role = schemas.Ptr(string(schemas.ChatMessageRoleAssistant))
	}
	return true
}
