// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

var anthropicMessageResponsePool = sync.Pool{
	New: func() interface{} {
		return &AnthropicMessageResponse{}
	},
}
var anthropicTextResponsePool = sync.Pool{
	New: func() interface{} {
		return &AnthropicTextResponse{}
	},
}

type AnthropicProvider struct {
	logger               schemas.Logger
	client               *fasthttp.Client
	streamingClient      *fasthttp.Client
	apiVersion           string
	networkConfig        schemas.NetworkConfig
	sendBackRawRequest   bool
	sendBackRawResponse  bool
	customProviderConfig *schemas.CustomProviderConfig
}

func AcquireAnthropicMessageResponse() *AnthropicMessageResponse {
	resp := anthropicMessageResponsePool.Get().(*AnthropicMessageResponse)
	*resp = AnthropicMessageResponse{}
	return resp
}

func ReleaseAnthropicMessageResponse(resp *AnthropicMessageResponse) {
	if resp != nil {
		anthropicMessageResponsePool.Put(resp)
	}
}

func acquireAnthropicTextResponse() *AnthropicTextResponse {
	resp := anthropicTextResponsePool.Get().(*AnthropicTextResponse)
	*resp = AnthropicTextResponse{}
	return resp
}

func releaseAnthropicTextResponse(resp *AnthropicTextResponse) {
	if resp != nil {
		anthropicTextResponsePool.Put(resp)
	}
}

func NewAnthropicProvider(config *schemas.ProviderConfig, logger schemas.Logger) *AnthropicProvider {
	config.CheckAndSetDefaults()

	requestTimeout := time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds)
	client := &fasthttp.Client{
		ReadTimeout:         requestTimeout,
		WriteTimeout:        requestTimeout,
		MaxConnsPerHost:     config.NetworkConfig.MaxConnsPerHost,
		MaxIdleConnDuration: time.Second * time.Duration(config.NetworkConfig.KeepAliveTimeoutInSeconds),
		MaxConnWaitTimeout:  requestTimeout,
		MaxConnDuration:     time.Second * time.Duration(schemas.DefaultMaxConnDurationInSeconds),
		ConnPoolStrategy:    fasthttp.FIFO,
	}

	for i := 0; i < config.ConcurrencyAndBufferSize.Concurrency; i++ {
		anthropicTextResponsePool.Put(&AnthropicTextResponse{})
		anthropicMessageResponsePool.Put(&AnthropicMessageResponse{})
	}

	client = providerUtils.ConfigureProxy(client, config.ProxyConfig, logger)
	client = providerUtils.ConfigureDialer(client, config.NetworkConfig.AllowPrivateNetwork)
	client = providerUtils.ConfigureTLS(client, config.NetworkConfig, logger)
	streamingClient := providerUtils.BuildStreamingClient(client)

	if config.NetworkConfig.BaseURL == "" {
		config.NetworkConfig.BaseURL = "https://api.anthropic.com"
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")

	return &AnthropicProvider{
		logger:               logger,
		client:               client,
		streamingClient:      streamingClient,
		apiVersion:           "2023-06-01",
		networkConfig:        config.NetworkConfig,
		sendBackRawRequest:   config.SendBackRawRequest,
		sendBackRawResponse:  config.SendBackRawResponse,
		customProviderConfig: config.CustomProviderConfig,
	}
}

func (provider *AnthropicProvider) GetProviderKey() schemas.ModelProvider {
	return providerUtils.GetProviderName(schemas.Anthropic, provider.customProviderConfig)
}

func (provider *AnthropicProvider) buildRequestURL(ctx *schemas.BifrostContext, defaultPath string, requestType schemas.RequestType) string {
	path, isCompleteURL := providerUtils.GetRequestPath(ctx, defaultPath, provider.customProviderConfig, requestType)
	if isCompleteURL {
		return path
	}
	return provider.networkConfig.BaseURL + path
}

func setAnthropicRequestBody(ctx *schemas.BifrostContext, req *fasthttp.Request, body []byte) bool {

	usedLargePayloadBody := providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, schemas.Anthropic)
	if !usedLargePayloadBody {

		mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
		req.SetBody(body)
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusOk, "")
		}
	}
	return usedLargePayloadBody
}

func extractAnthropicResponsesUsageFromPrefetch(data []byte) *schemas.ResponsesResponseUsage {
	node, err := sonic.Get(data, "usage")
	if err != nil {
		return nil
	}
	raw, _ := node.Raw()
	if raw == "" {
		return nil
	}
	var usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	if err := sonic.UnmarshalString(raw, &usage); err != nil {
		return nil
	}
	return &schemas.ResponsesResponseUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.InputTokens + usage.OutputTokens,
	}
}

func (provider *AnthropicProvider) anthropicRequestHeaders(ctx *schemas.BifrostContext, key schemas.Key) map[string]string {
	headers := map[string]string{
		"anthropic-version": provider.apiVersion,
	}
	if key.Value.GetValue() != "" && !IsClaudeCodeMaxMode(ctx) {
		headers["x-api-key"] = key.Value.GetValue()
	}
	return headers
}

func completeRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	jsonBody []byte,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	betaHeaderOverrides map[string]bool,
	providerName schemas.ModelProvider,
	requestType schemas.RequestType,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) ([]byte, time.Duration, map[string]string, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, []string{AnthropicBetaHeader})

	providerUtils.SetPassthroughHeaders(ctx, req, providerName, []string{AnthropicBetaHeader})

	req.Header.SetContentType("application/json")

	if betaHeaders := FilterBetaHeadersForProvider(MergeBetaHeaders(ctx, extraHeaders), providerName, betaHeaderOverrides); len(betaHeaders) > 0 {
		req.Header.Set(AnthropicBetaHeader, strings.Join(betaHeaders, ","))
	} else {
		req.Header.Del(AnthropicBetaHeader)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	usedLargePayloadBody := setAnthropicRequestBody(ctx, req, jsonBody)

	if signer != nil {

		signedBody := jsonBody
		if usedLargePayloadBody {
			signedBody = req.Body()
		}
		sigHeaders, bErr := signer(signedBody)
		if bErr != nil {
			return nil, 0, nil, bErr
		}
		for k, v := range sigHeaders {
			req.Header.Set(k, v)
		}
	}

	requestClient := client
	responseThreshold, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64)
	isCountTokens := requestType == schemas.CountTokensRequest

	if responseThreshold > 0 && !isCountTokens {
		resp.StreamBody = true
		requestClient = providerUtils.BuildLargeResponseClient(client, responseThreshold)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, requestClient, req, resp)
	defer wait()
	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}
	if bifrostErr != nil {
		return nil, latency, nil, bifrostErr
	}

	ft, fh := providerUtils.StartPhaseSpan(ctx, "response-finalize")
	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	if ft != nil {
		ft.EndSpan(fh, schemas.SpanStatusOk, "")
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, latency, providerResponseHeaders, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	if isCountTokens {
		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			return nil, latency, providerResponseHeaders, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
		}
		return body, latency, providerResponseHeaders, nil
	}

	body, isLarge, respErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, logger)
	if respErr != nil {
		return nil, latency, providerResponseHeaders, respErr
	}
	if isLarge {
		respOwned = false
		return nil, latency, providerResponseHeaders, nil
	}
	return body, latency, providerResponseHeaders, nil
}

func (provider *AnthropicProvider) listModelsByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.buildRequestURL(ctx, fmt.Sprintf("/v1/models?limit=%d", schemas.DefaultPageSize), schemas.ListModelsRequest))
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	var anthropicResponse AnthropicListModelsResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(resp.Body(), &anthropicResponse, nil, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	response := anthropicResponse.ToBifrostListModelsResponse(provider.GetProviderKey(), key.Models, key.BlacklistedModels, key.Aliases, request.Unfiltered)
	response.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *AnthropicProvider) ListModels(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ListModelsRequest); err != nil {
		return nil, err
	}
	if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
		return providerUtils.HandleKeylessListModelsRequest(schemas.Anthropic, func() (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
			return provider.listModelsByKey(ctx, schemas.Key{Models: schemas.WhiteList{"*"}}, request)
		})
	}
	return providerUtils.HandleMultipleListModelsRequests(
		ctx,
		keys,
		request,
		provider.listModelsByKey,
	)
}

func (provider *AnthropicProvider) ModelRetrieve(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostModelRetrieveRequest) (*schemas.BifrostModelRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ModelRetrieveRequest); err != nil {
		return nil, err
	}
	if request == nil || request.Model == "" {
		return nil, providerUtils.NewBifrostOperationError("model is required", nil)
	}
	escapedModel, idErr := providerUtils.EscapeResourceID(request.Model, "model")
	if idErr != nil {
		return nil, idErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/models/"+escapedModel, schemas.ModelRetrieveRequest))
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var anthropicModel AnthropicModel
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(resp.Body(), &anthropicModel, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	response := anthropicModel.ToBifrostModelRetrieveResponse(provider.GetProviderKey())
	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *AnthropicProvider) TextCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.TextCompletionRequest); err != nil {
		return nil, err
	}

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToAnthropicTextCompletionRequest(request), nil
		})
	if err != nil {
		return nil, err
	}

	responseBody, latency, providerResponseHeaders, err := completeRequest(ctx, provider.client, provider.buildRequestURL(ctx, "/v1/complete", schemas.TextCompletionRequest), jsonData, provider.anthropicRequestHeaders(ctx, key), provider.networkConfig.ExtraHeaders, provider.networkConfig.BetaHeaderOverrides, provider.GetProviderKey(), schemas.TextCompletionRequest, nil, provider.logger)
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, err, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostTextCompletionResponse{
			Model: request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response := acquireAnthropicTextResponse()
	defer releaseAnthropicTextResponse(response)

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, responseBody, response, jsonData, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, responseBody, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	convTracer, convHandle := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := response.ToBifrostTextCompletionResponse()
	if convTracer != nil {
		convTracer.EndSpan(convHandle, schemas.SpanStatusOk, "")
	}

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResponse, nil
}

func (provider *AnthropicProvider) TextCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.TextCompletionStreamRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ChatCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ChatCompletionRequest); err != nil {
		return nil, err
	}
	return HandleAnthropicChatCompletionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/messages", schemas.ChatCompletionRequest),
		request,
		AnthropicRequestBuildConfig{
			Provider:                  schemas.Anthropic,
			IsStreaming:               false,
			BetaHeaderOverrides:       provider.networkConfig.BetaHeaderOverrides,
			ShouldSendBackRawRequest:  provider.sendBackRawRequest,
			ShouldSendBackRawResponse: provider.sendBackRawResponse,
		},
		provider.anthropicRequestHeaders(ctx, key),
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.logger,
	)
}

func HandleAnthropicChatCompletionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostChatRequest,
	config AnthropicRequestBuildConfig,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	jsonBody, bifrostErr := BuildAnthropicChatRequestBody(ctx, request, config)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	responseBody, latency, providerResponseHeaders, bifrostErr := completeRequest(ctx, client, url, jsonBody, headers, extraHeaders, config.BetaHeaderOverrides, config.Provider, schemas.ChatCompletionRequest, signer, logger)
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostChatResponse{
			Model: request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response := AcquireAnthropicMessageResponse()
	defer ReleaseAnthropicMessageResponse(response)

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, responseBody, response, jsonBody, providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse))
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, responseBody, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	convTracer, convHandle := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := response.ToBifrostChatResponse(ctx)
	if convTracer != nil {
		convTracer.EndSpan(convHandle, schemas.SpanStatusOk, "")
	}

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest) {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}

func (provider *AnthropicProvider) ChatCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ChatCompletionStreamRequest); err != nil {
		return nil, err
	}

	jsonData, bifrostErr := BuildAnthropicChatRequestBody(ctx, request, AnthropicRequestBuildConfig{
		Provider:                  schemas.Anthropic,
		IsStreaming:               true,
		ShouldSendBackRawRequest:  provider.sendBackRawRequest,
		ShouldSendBackRawResponse: provider.sendBackRawResponse,
	})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": provider.apiVersion,
		"Accept":            "text/event-stream",
		"Cache-Control":     "no-cache",
	}

	if key.Value.GetValue() != "" && !IsClaudeCodeMaxMode(ctx) {
		headers["x-api-key"] = key.Value.GetValue()
	}

	return HandleAnthropicChatCompletionStreaming(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/messages", schemas.ChatCompletionStreamRequest),
		jsonData,
		headers,
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		provider.networkConfig.BetaHeaderOverrides,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func normalizeCachedUsage(usage *schemas.BifrostLLMUsage) {
	if usage == nil || usage.PromptTokensDetails == nil {
		return
	}
	cached := usage.PromptTokensDetails.CachedReadTokens + usage.PromptTokensDetails.CachedWriteTokens
	usage.PromptTokens += cached
	usage.TotalTokens += cached
}

func accumulateAnthropicResponsesUsage(usage *schemas.ResponsesResponseUsage, billedUsage *schemas.BifrostLLMUsage, usageToProcess *AnthropicUsage) {
	if usage == nil || usageToProcess == nil {
		return
	}

	if len(usageToProcess.Iterations) > 0 {
		usage.Iterations = make([]schemas.ResponsesResponseUsage, len(usageToProcess.Iterations))
		for i := range usageToProcess.Iterations {
			if converted := ConvertAnthropicUsageToBifrostUsage(&usageToProcess.Iterations[i]); converted != nil {
				usage.Iterations[i] = *converted
			}
		}

		if billedUsage != nil {
			billedUsage.Iterations = slices.Clone(usage.Iterations)
		}
	}
	usageToProcess = billableAnthropicUsage(usageToProcess)

	if usageToProcess.ServerToolUse != nil && usageToProcess.ServerToolUse.WebSearchRequests > 0 {
		n := usageToProcess.ServerToolUse.WebSearchRequests
		if usage.OutputTokensDetails == nil {
			usage.OutputTokensDetails = &schemas.ResponsesResponseOutputTokens{}
		}
		if usage.OutputTokensDetails.NumSearchQueries == nil || n > *usage.OutputTokensDetails.NumSearchQueries {
			usage.OutputTokensDetails.NumSearchQueries = schemas.Ptr(n)
		}
		if usage.ToolUsage == nil {
			usage.ToolUsage = &schemas.ToolUsage{}
		}
		if usage.ToolUsage.WebSearch == nil || n > usage.ToolUsage.WebSearch.NumRequests {
			usage.ToolUsage.WebSearch = &schemas.WebSearchToolUsage{NumRequests: n}
		}
		if billedUsage != nil {
			if billedUsage.CompletionTokensDetails == nil {
				billedUsage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
			}
			if billedUsage.CompletionTokensDetails.NumSearchQueries == nil || n > *billedUsage.CompletionTokensDetails.NumSearchQueries {
				billedUsage.CompletionTokensDetails.NumSearchQueries = schemas.Ptr(n)
			}
			if billedUsage.ToolUsage == nil {
				billedUsage.ToolUsage = &schemas.ToolUsage{}
			}
			if billedUsage.ToolUsage.WebSearch == nil || n > billedUsage.ToolUsage.WebSearch.NumRequests {
				billedUsage.ToolUsage.WebSearch = &schemas.WebSearchToolUsage{NumRequests: n}
			}
		}
	}

	if usageToProcess.OutputTokensDetails != nil {
		t := usageToProcess.OutputTokensDetails.ThinkingTokens
		if usage.OutputTokensDetails == nil {
			usage.OutputTokensDetails = &schemas.ResponsesResponseOutputTokens{}
		}
		if t > usage.OutputTokensDetails.ReasoningTokens {
			usage.OutputTokensDetails.ReasoningTokens = t
		}
		if billedUsage != nil && t > 0 {
			if billedUsage.CompletionTokensDetails == nil {
				billedUsage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
			}
			if t > billedUsage.CompletionTokensDetails.ReasoningTokens {
				billedUsage.CompletionTokensDetails.ReasoningTokens = t
			}
		}
	}
	if usageToProcess.InputTokens > usage.InputTokens {
		usage.InputTokens = usageToProcess.InputTokens
		if billedUsage != nil {
			billedUsage.PromptTokens = usageToProcess.InputTokens
		}
	}
	if usageToProcess.OutputTokens > usage.OutputTokens {
		usage.OutputTokens = usageToProcess.OutputTokens
		if billedUsage != nil {
			billedUsage.CompletionTokens = usageToProcess.OutputTokens
		}
	}
	calculatedTotal := usage.InputTokens + usage.OutputTokens
	if calculatedTotal > usage.TotalTokens {
		usage.TotalTokens = calculatedTotal
		if billedUsage != nil {
			billedUsage.TotalTokens = calculatedTotal
		}
	}

	if usageToProcess.CacheReadInputTokens > 0 {
		if usage.InputTokensDetails == nil {
			usage.InputTokensDetails = &schemas.ResponsesResponseInputTokens{}
		}
		if billedUsage != nil && billedUsage.PromptTokensDetails == nil {
			billedUsage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{}
		}
		if usageToProcess.CacheReadInputTokens > usage.InputTokensDetails.CachedReadTokens {
			usage.InputTokensDetails.CachedReadTokens = usageToProcess.CacheReadInputTokens
			if billedUsage != nil {
				billedUsage.PromptTokensDetails.CachedReadTokens = usageToProcess.CacheReadInputTokens
			}
		}
	}

	if usageToProcess.CacheCreationInputTokens > 0 {
		if usage.InputTokensDetails == nil {
			usage.InputTokensDetails = &schemas.ResponsesResponseInputTokens{}
		}
		if billedUsage != nil && billedUsage.PromptTokensDetails == nil {
			billedUsage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{}
		}
		if usageToProcess.CacheCreationInputTokens > usage.InputTokensDetails.CachedWriteTokens {
			usage.InputTokensDetails.CachedWriteTokens = usageToProcess.CacheCreationInputTokens
			if billedUsage != nil {
				billedUsage.PromptTokensDetails.CachedWriteTokens = usageToProcess.CacheCreationInputTokens
			}
		}
		if usageToProcess.CacheCreation.Ephemeral5mInputTokens > 0 || usageToProcess.CacheCreation.Ephemeral1hInputTokens > 0 {
			if usage.InputTokensDetails.CachedWriteTokenDetails == nil {
				usage.InputTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{}
			}
			if billedUsage != nil && billedUsage.PromptTokensDetails.CachedWriteTokenDetails == nil {
				billedUsage.PromptTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{}
			}
			if usageToProcess.CacheCreation.Ephemeral5mInputTokens > usage.InputTokensDetails.CachedWriteTokenDetails.CachedWriteTokens5m {
				usage.InputTokensDetails.CachedWriteTokenDetails.CachedWriteTokens5m = usageToProcess.CacheCreation.Ephemeral5mInputTokens
				if billedUsage != nil {
					billedUsage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens5m = usageToProcess.CacheCreation.Ephemeral5mInputTokens
				}
			}
			if usageToProcess.CacheCreation.Ephemeral1hInputTokens > usage.InputTokensDetails.CachedWriteTokenDetails.CachedWriteTokens1h {
				usage.InputTokensDetails.CachedWriteTokenDetails.CachedWriteTokens1h = usageToProcess.CacheCreation.Ephemeral1hInputTokens
				if billedUsage != nil {
					billedUsage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens1h = usageToProcess.CacheCreation.Ephemeral1hInputTokens
				}
			}
		}
	}
}

func HandleAnthropicChatCompletionStreaming(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	jsonBody []byte,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	betaHeaderOverrides map[string]bool,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	postResponseConverter func(*schemas.BifrostChatResponse) *schemas.BifrostChatResponse,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, []string{AnthropicBetaHeader})

	providerUtils.SetPassthroughHeadersForStreaming(ctx, req, providerName, []string{AnthropicBetaHeader})

	if betaHeaders := FilterBetaHeadersForProvider(MergeBetaHeaders(ctx, extraHeaders), providerName, betaHeaderOverrides); len(betaHeaders) > 0 {
		req.Header.Set(AnthropicBetaHeader, strings.Join(betaHeaders, ","))
	} else {
		req.Header.Del(AnthropicBetaHeader)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	usedLargePayloadBody := setAnthropicRequestBody(ctx, req, jsonBody)

	if signer != nil {
		sigHeaders, bErr := signer(jsonBody)
		if bErr != nil {
			defer providerUtils.ReleaseStreamingResponse(ctx, resp)
			return nil, bErr
		}
		for k, v := range sigHeaders {
			req.Header.Set(k, v)
		}
	}

	req.Header.Set("Connection", "close")

	requestClient := providerUtils.BuildStreamingClient(client)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, requestClient, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
	latency := time.Since(startTime)
	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		if errors.Is(err, context.Canceled) {
			return nil, providerUtils.EnrichError(ctx, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
					Error:   err,
				},
			}, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}

		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseAnthropicError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	if providerUtils.SetupStreamingPassthrough(ctx, resp) {
		responseChan := make(chan *schemas.BifrostStreamChunk)
		providerUtils.CloseStream(ctx, responseChan)
		return responseChan, nil
	}

	responseChan := make(chan *schemas.BifrostStreamChunk, schemas.DefaultStreamBufferSize)

	go func() {
		defer providerUtils.EnsureStreamFinalizerCalled(ctx, postHookSpanFinalizer)
		defer func() {
			if ctx.Err() == context.Canceled {
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		if resp.BodyStream() == nil {
			bifrostErr := providerUtils.NewBifrostOperationError(
				"Provider returned an empty response",
				fmt.Errorf("provider returned an empty response"),
			)
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
			return
		}

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		sseReader := providerUtils.GetSSEEventReader(ctx, reader)

		chunkIndex := 0

		lastChunkTime := startTime

		var messageID string
		var modelName string
		var finishReason *string

		usage := &schemas.BifrostLLMUsage{}

		var servedServiceTier *schemas.BifrostServiceTier
		var servedSpeed *string
		var servedInferenceGeo *string

		var servedFallbackModel *string

		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, usage)

		usageNormalized := false
		normalizeUsage := func() {
			if usageNormalized {
				return
			}
			usageNormalized = true
			normalizeCachedUsage(usage)
		}
		defer func() {
			if ctx.Err() != nil {
				normalizeUsage()
			}
		}()

		var structuredOutputToolName string
		var isAccumulatingStructuredOutput bool
		var consumedStructuredOutput bool
		if toolName, ok := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string); ok {
			structuredOutputToolName = toolName
		}

		streamState := NewAnthropicStreamState()

		sawTerminalEvent := false

		for {

			if ctx.Err() != nil {
				return
			}
			eventType, eventDataBytes, readErr := sseReader.ReadEvent()
			if readErr != nil {

				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading %s stream: %v", providerName, readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
					return
				}
				break
			}
			eventData := string(eventDataBytes)
			if eventType == "" || eventData == "" {
				continue
			}
			var event AnthropicStreamEvent

			parseStart := time.Now()
			umErr := sonic.Unmarshal([]byte(eventData), &event)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if umErr != nil {
				logger.Warn("Failed to parse message_start event: %v", umErr)
				continue
			}
			if event.Type == AnthropicStreamEventTypeMessageStart && event.Message != nil && event.Message.ID != "" {
				messageID = event.Message.ID
			}

			var usageToProcess *AnthropicUsage
			if event.Usage != nil {
				usageToProcess = event.Usage
			} else if event.Message != nil && event.Message.Usage != nil {
				usageToProcess = event.Message.Usage
			}
			if usageToProcess != nil {
				if served := usageToProcess.ServerSideFallbackModel(); served != nil {
					servedFallbackModel = served
					usage.ServerSideFallbackModel = served
				}
				usageToProcess = billableAnthropicUsage(usageToProcess)

				if usageToProcess.ServerToolUse != nil && usageToProcess.ServerToolUse.WebSearchRequests > 0 {
					if usage.CompletionTokensDetails == nil {
						usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
					}
					if n := usageToProcess.ServerToolUse.WebSearchRequests; usage.CompletionTokensDetails.NumSearchQueries == nil || n > *usage.CompletionTokensDetails.NumSearchQueries {
						usage.CompletionTokensDetails.NumSearchQueries = &n
					}
					if usage.ToolUsage == nil {
						usage.ToolUsage = &schemas.ToolUsage{}
					}
					if n := usageToProcess.ServerToolUse.WebSearchRequests; usage.ToolUsage.WebSearch == nil || n > usage.ToolUsage.WebSearch.NumRequests {
						usage.ToolUsage.WebSearch = &schemas.WebSearchToolUsage{NumRequests: n}
					}
				}

				if usageToProcess.OutputTokensDetails != nil && usageToProcess.OutputTokensDetails.ThinkingTokens > 0 {
					if usage.CompletionTokensDetails == nil {
						usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
					}
					if t := usageToProcess.OutputTokensDetails.ThinkingTokens; t > usage.CompletionTokensDetails.ReasoningTokens {
						usage.CompletionTokensDetails.ReasoningTokens = t
					}
				}

				if usageToProcess.ServiceTier != nil {
					mapped := MapAnthropicServiceTierToBifrost(*usageToProcess.ServiceTier)
					servedServiceTier = &mapped
				}
				if usageToProcess.Speed != nil {
					servedSpeed = usageToProcess.Speed
					usage.Speed = usageToProcess.Speed
				}
				if usageToProcess.InferenceGeo != nil {
					servedInferenceGeo = usageToProcess.InferenceGeo
					usage.InferenceGeo = usageToProcess.InferenceGeo
				}

				if usageToProcess.InputTokens > usage.PromptTokens {
					usage.PromptTokens = usageToProcess.InputTokens
				}
				if usageToProcess.OutputTokens > usage.CompletionTokens {
					usage.CompletionTokens = usageToProcess.OutputTokens
				}
				calculatedTotal := usage.PromptTokens + usage.CompletionTokens
				if calculatedTotal > usage.TotalTokens {
					usage.TotalTokens = calculatedTotal
				}

				if usageToProcess.CacheReadInputTokens > 0 {
					if usage.PromptTokensDetails == nil {
						usage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{}
					}
					if usageToProcess.CacheReadInputTokens > usage.PromptTokensDetails.CachedReadTokens {
						usage.PromptTokensDetails.CachedReadTokens = usageToProcess.CacheReadInputTokens
					}
				}
				if usageToProcess.CacheCreationInputTokens > 0 {
					if usage.PromptTokensDetails == nil {
						usage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{}
					}
					if usageToProcess.CacheCreationInputTokens > usage.PromptTokensDetails.CachedWriteTokens {
						usage.PromptTokensDetails.CachedWriteTokens = usageToProcess.CacheCreationInputTokens
					}
					if usageToProcess.CacheCreation.Ephemeral5mInputTokens > 0 || usageToProcess.CacheCreation.Ephemeral1hInputTokens > 0 {
						if usage.PromptTokensDetails.CachedWriteTokenDetails == nil {
							usage.PromptTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{}
						}
						if usageToProcess.CacheCreation.Ephemeral5mInputTokens > usage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens5m {
							usage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens5m = usageToProcess.CacheCreation.Ephemeral5mInputTokens
						}
						if usageToProcess.CacheCreation.Ephemeral1hInputTokens > usage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens1h {
							usage.PromptTokensDetails.CachedWriteTokenDetails.CachedWriteTokens1h = usageToProcess.CacheCreation.Ephemeral1hInputTokens
						}
					}
				}
			}
			if event.Message != nil {
				modelName = event.Message.Model
			}

			if event.Delta != nil && event.Delta.StopReason != nil {
				mappedReason := ConvertAnthropicFinishReasonToBifrost(*event.Delta.StopReason)
				finishReason = &mappedReason

				if consumedStructuredOutput && streamState.nextToolCallIndex == 0 &&
					*finishReason == string(schemas.BifrostFinishReasonToolCalls) {
					stopReason := string(schemas.BifrostFinishReasonStop)
					finishReason = &stopReason
				}
			}

			if structuredOutputToolName != "" {

				if event.Type == AnthropicStreamEventTypeContentBlockStart {
					if event.ContentBlock != nil && event.ContentBlock.Type == AnthropicContentBlockTypeToolUse {
						if event.ContentBlock.Name != nil && *event.ContentBlock.Name == structuredOutputToolName {
							isAccumulatingStructuredOutput = true
							continue
						}
					}
				}

				if event.Type == AnthropicStreamEventTypeContentBlockDelta && isAccumulatingStructuredOutput {
					if event.Delta != nil && event.Delta.Type == AnthropicStreamDeltaTypeInputJSON && event.Delta.PartialJSON != nil {

						content := *event.Delta.PartialJSON

						streamState.contentLen += len(content)
						response := &schemas.BifrostChatResponse{
							ID:     messageID,
							Object: "chat.completion.chunk",
							Choices: []schemas.BifrostResponseChoice{
								{
									Index: 0,
									ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
										Delta: &schemas.ChatStreamResponseChoiceDelta{
											Content: &content,
										},
									},
								},
							},
							ExtraFields: schemas.BifrostResponseExtraFields{
								ChunkIndex: chunkIndex,
								Latency:    time.Since(lastChunkTime).Milliseconds(),
							},
						}
						lastChunkTime = time.Now()
						chunkIndex++

						if sendBackRawResponse {
							response.ExtraFields.RawResponse = eventData
						}

						providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
						continue
					}
				}

				if event.Type == AnthropicStreamEventTypeContentBlockStop && isAccumulatingStructuredOutput {
					isAccumulatingStructuredOutput = false
					consumedStructuredOutput = true
					continue
				}
			}

			convStart := time.Now()
			response, bifrostErr, isLastChunk := event.ToBifrostChatCompletionStream(ctx, structuredOutputToolName, streamState)
			schemas.AddStreamConvert(ctx, time.Since(convStart))
			if bifrostErr != nil {
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, logger, postHookSpanFinalizer)

				return
			}
			if response != nil {
				response.ExtraFields = schemas.BifrostResponseExtraFields{
					ChunkIndex: chunkIndex,
					Latency:    time.Since(lastChunkTime).Milliseconds(),
				}
				if postResponseConverter != nil {
					response = postResponseConverter(response)
					if response == nil {
						logger.Warn("postResponseConverter returned nil; skipping chunk")
						continue
					}
				}
				response.ID = messageID
				lastChunkTime = time.Now()
				chunkIndex++

				if sendBackRawResponse {
					response.ExtraFields.RawResponse = eventData
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
			}
			if isLastChunk {
				sawTerminalEvent = true
				break
			}
		}

		if !sawTerminalEvent {

			normalizeUsage()
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			return
		}

		normalizeUsage()
		response := providerUtils.CreateBifrostChatCompletionChunkResponse(messageID, usage, finishReason, chunkIndex, modelName, 0)
		if postResponseConverter != nil {
			response = postResponseConverter(response)
			if response == nil {
				logger.Warn("postResponseConverter returned nil; skipping chunk")

				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				return
			}
		}

		if servedServiceTier != nil {
			response.ServiceTier = servedServiceTier
		}
		if servedSpeed != nil {
			response.Speed = servedSpeed
		}
		if servedInferenceGeo != nil {
			response.InferenceGeo = servedInferenceGeo
		}
		if servedFallbackModel != nil {
			response.ExtraFields.RoutingInfo.ServerSideFallbackModel = servedFallbackModel
		}

		if sendBackRawRequest {
			providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
		}
		response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
		ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
		providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
	}()

	return responseChan, nil
}

func (provider *AnthropicProvider) Responses(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ResponsesRequest); err != nil {
		return nil, err
	}
	return HandleAnthropicResponsesRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/messages", schemas.ResponsesRequest),
		request,
		AnthropicRequestBuildConfig{
			Provider:                  schemas.Anthropic,
			IsStreaming:               false,
			BetaHeaderOverrides:       provider.networkConfig.BetaHeaderOverrides,
			ShouldSendBackRawRequest:  provider.sendBackRawRequest,
			ShouldSendBackRawResponse: provider.sendBackRawResponse,
		},
		provider.anthropicRequestHeaders(ctx, key),
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.logger,
	)
}

func HandleAnthropicResponsesRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostResponsesRequest,
	config AnthropicRequestBuildConfig,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	jsonBody, bifrostErr := BuildAnthropicResponsesRequestBody(ctx, request, config)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	responseBody, latency, providerResponseHeaders, bifrostErr := completeRequest(ctx, client, url, jsonBody, headers, extraHeaders, config.BetaHeaderOverrides, config.Provider, schemas.ResponsesRequest, signer, logger)
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		preview, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadResponsePreview).(string)
		return &schemas.BifrostResponsesResponse{
			ID:        schemas.Ptr("resp_" + schemas.GetRandomString(50)),
			Object:    "response",
			CreatedAt: int(time.Now().Unix()),
			Model:     request.Model,
			Usage:     extractAnthropicResponsesUsageFromPrefetch([]byte(preview)),
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response := AcquireAnthropicMessageResponse()
	defer ReleaseAnthropicMessageResponse(response)

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, responseBody, response, jsonBody, providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse))
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, responseBody, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	convTracer, convHandle := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := response.ToBifrostResponsesResponse(ctx)
	if convTracer != nil {
		convTracer.EndSpan(convHandle, schemas.SpanStatusOk, "")
	}

	if config.Provider == schemas.Bedrock {
		action, trace := bedrockInvokeGuardrailOutcome(responseBody)
		setBedrockInvokeGuardrailOutcome(bifrostResponse, action, trace)
	}

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest) {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}

func (provider *AnthropicProvider) ResponsesStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.ResponsesStreamRequest); err != nil {
		return nil, err
	}

	jsonBody, err := BuildAnthropicResponsesRequestBody(ctx, request, AnthropicRequestBuildConfig{
		Provider:                  schemas.Anthropic,
		IsStreaming:               true,
		ShouldSendBackRawRequest:  provider.sendBackRawRequest,
		ShouldSendBackRawResponse: provider.sendBackRawResponse,
	})
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"anthropic-version": provider.apiVersion,
		"Accept":            "text/event-stream",
		"Cache-Control":     "no-cache",
	}

	if key.Value.GetValue() != "" && !IsClaudeCodeMaxMode(ctx) {
		headers["x-api-key"] = key.Value.GetValue()
	}

	return HandleAnthropicResponsesStream(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/messages", schemas.ResponsesStreamRequest),
		jsonBody,
		headers,
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		provider.networkConfig.BetaHeaderOverrides,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleAnthropicResponsesStream(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	jsonBody []byte,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	betaHeaderOverrides map[string]bool,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	postResponseConverter func(*schemas.BifrostResponsesStreamResponse) *schemas.BifrostResponsesStreamResponse,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, []string{AnthropicBetaHeader})

	providerUtils.SetPassthroughHeadersForStreaming(ctx, req, providerName, []string{AnthropicBetaHeader})

	if betaHeaders := FilterBetaHeadersForProvider(MergeBetaHeaders(ctx, extraHeaders), providerName, betaHeaderOverrides); len(betaHeaders) > 0 {
		req.Header.Set(AnthropicBetaHeader, strings.Join(betaHeaders, ","))
	} else {
		req.Header.Del(AnthropicBetaHeader)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	usedLargePayloadBody := setAnthropicRequestBody(ctx, req, jsonBody)

	if signer != nil {
		sigHeaders, bErr := signer(jsonBody)
		if bErr != nil {
			defer providerUtils.ReleaseStreamingResponse(ctx, resp)
			return nil, bErr
		}
		for k, v := range sigHeaders {
			req.Header.Set(k, v)
		}
	}

	req.Header.Set("Connection", "close")

	requestClient := providerUtils.BuildStreamingClient(client)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, requestClient, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
	latency := time.Since(startTime)
	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		if errors.Is(err, context.Canceled) {
			return nil, providerUtils.EnrichError(ctx, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
					Error:   err,
				},
			}, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}

		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseAnthropicError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	if providerUtils.SetupStreamingPassthrough(ctx, resp) {
		responseChan := make(chan *schemas.BifrostStreamChunk)
		providerUtils.CloseStream(ctx, responseChan)
		return responseChan, nil
	}

	responseChan := make(chan *schemas.BifrostStreamChunk, schemas.DefaultStreamBufferSize)

	go func() {
		defer providerUtils.EnsureStreamFinalizerCalled(ctx, postHookSpanFinalizer)
		defer func() {
			if ctx.Err() == context.Canceled {
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		if resp.BodyStream() == nil {
			bifrostErr := providerUtils.NewBifrostOperationError(
				"Provider returned an empty response",
				fmt.Errorf("provider returned an empty response"),
			)
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse), responseChan, logger, postHookSpanFinalizer)
			return
		}

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		sseReader := providerUtils.GetSSEEventReader(ctx, reader)
		chunkIndex := 0

		openAnthropicStreamDeltaPromptUsageLedger(ctx)

		lastChunkTime := startTime

		usage := &schemas.ResponsesResponseUsage{}
		billedUsage := &schemas.BifrostLLMUsage{}

		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, billedUsage)

		usageNormalized := false
		normalizeBilledUsage := func() {
			if usageNormalized {
				return
			}
			usageNormalized = true
			normalizeCachedUsage(billedUsage)
		}
		defer func() {
			if ctx.Err() != nil {
				normalizeBilledUsage()
			}
		}()

		streamState := AcquireAnthropicResponsesStreamState()
		defer ReleaseAnthropicResponsesStreamState(streamState)

		if toolName, ok := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string); ok {
			streamState.StructuredOutputToolName = toolName
		}

		var modelName string
		var servedServiceTier *schemas.BifrostServiceTier
		var servedSpeed *string
		var servedInferenceGeo *string

		var servedFallbackModel *string

		var invokeGuardrailAction string
		var invokeGuardrailTrace json.RawMessage

		for {

			if ctx.Err() != nil {
				return
			}
			eventType, eventDataBytes, readErr := sseReader.ReadEvent()
			if readErr != nil {

				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading %s stream: %v", providerName, readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)

					return
				}
				break
			}
			eventData := string(eventDataBytes)
			if eventType == "" || eventData == "" {
				continue
			}
			if bytes.Contains(eventDataBytes, []byte(`"amazon-bedrock-`)) {
				if action, trace := bedrockInvokeGuardrailOutcome(eventDataBytes); action != "" || len(trace) > 0 {
					invokeGuardrailAction, invokeGuardrailTrace = action, trace
				}
			}
			var event AnthropicStreamEvent
			parseStart := time.Now()
			umErr := sonic.Unmarshal([]byte(eventData), &event)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if umErr != nil {
				logger.Warn("Failed to parse message_start event: %v", umErr)
				continue
			}
			if event.Message != nil && modelName == "" {
				modelName = event.Message.Model
			}

			if event.Type == AnthropicStreamEventTypeMessageDelta {
				recordAnthropicStreamDeltaPromptUsage(ctx, chunkIndex, eventDataBytes)
			}

			var usageToProcess *AnthropicUsage
			if event.Usage != nil {
				usageToProcess = event.Usage
			} else if event.Message != nil && event.Message.Usage != nil {
				usageToProcess = event.Message.Usage
			}

			if usageToProcess != nil {

				accumulateAnthropicResponsesUsage(usage, billedUsage, usageToProcess)

				if usageToProcess.ServiceTier != nil {
					mapped := MapAnthropicServiceTierToBifrost(*usageToProcess.ServiceTier)
					servedServiceTier = &mapped
				}
				if usageToProcess.Speed != nil {
					servedSpeed = usageToProcess.Speed
					if billedUsage != nil {
						billedUsage.Speed = usageToProcess.Speed
					}
				}
				if usageToProcess.InferenceGeo != nil {
					servedInferenceGeo = usageToProcess.InferenceGeo
					if billedUsage != nil {
						billedUsage.InferenceGeo = usageToProcess.InferenceGeo
					}
				}
				if served := usageToProcess.ServerSideFallbackModel(); served != nil {
					servedFallbackModel = served
					if billedUsage != nil {
						billedUsage.ServerSideFallbackModel = served
					}
				}
			}

			convCallStart := time.Now()
			responses, bifrostErr, isLastChunk := event.ToBifrostResponsesStream(ctx, chunkIndex, streamState)
			schemas.AddStreamConvert(ctx, time.Since(convCallStart))

			if streamState.HasEmittedMessageDelta {
				ctx.SetValue(schemas.BifrostContextKeyHasEmittedMessageDelta, true)
			}
			if bifrostErr != nil {

				if ctx.Err() != nil {
					return
				}
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, logger, postHookSpanFinalizer)

				return
			}

			rawIdx := len(responses) - 1
			for j, r := range responses {
				if r != nil && r.Type == schemas.ResponsesStreamResponseTypeCreated {
					rawIdx = j
					break
				}
			}

			for i, response := range responses {
				if response != nil {
					response.ExtraFields = schemas.BifrostResponseExtraFields{
						ChunkIndex: chunkIndex,
						Latency:    time.Since(lastChunkTime).Milliseconds(),
					}
					if postResponseConverter != nil {
						response = postResponseConverter(response)
						if response == nil {
							logger.Warn("postResponseConverter returned nil; skipping chunk")
							continue
						}
					}
					lastChunkTime = time.Now()
					chunkIndex++

					if providerUtils.ShouldSendBackRawResponse(ctx, sendBackRawResponse) && i == rawIdx {
						response.ExtraFields.RawResponse = eventData
					}

					if i == rawIdx && len(event.SafeguardResults) > 0 {
						response.SafeguardResults = event.SafeguardResults
					}

					if isLastChunk && i == len(responses)-1 {
						if response.Response == nil {
							response.Response = &schemas.BifrostResponsesResponse{}
						}
						if usage.InputTokensDetails != nil {
							usage.InputTokens = usage.InputTokens + usage.InputTokensDetails.CachedReadTokens + usage.InputTokensDetails.CachedWriteTokens
							usage.TotalTokens = usage.TotalTokens + usage.InputTokensDetails.CachedReadTokens + usage.InputTokensDetails.CachedWriteTokens
						}
						setBedrockInvokeGuardrailOutcome(response.Response, invokeGuardrailAction, invokeGuardrailTrace)
						response.Response.Usage = usage
						if servedServiceTier != nil {
							response.Response.ServiceTier = servedServiceTier
						}
						if servedSpeed != nil {
							response.Response.Speed = servedSpeed
						}
						if servedInferenceGeo != nil {
							response.Response.InferenceGeo = servedInferenceGeo
						}

						if servedFallbackModel != nil {
							response.ExtraFields.RoutingInfo.ServerSideFallbackModel = servedFallbackModel
						}

						if sendBackRawRequest {
							providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
						}
						response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, response, nil, nil, nil), responseChan, postHookSpanFinalizer)
						return
					}
					providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, response, nil, nil, nil), responseChan, postHookSpanFinalizer)
				}
			}

		}

		normalizeBilledUsage()
		providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
	}()

	return responseChan, nil
}

func (provider *AnthropicProvider) BatchCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.BatchCreateRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	if len(request.Requests) == 0 {
		return nil, providerUtils.NewBifrostOperationError("requests array is required for Anthropic batch API", nil)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/messages/batches", schemas.BatchCreateRequest))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)

	anthropicReq := &AnthropicBatchCreateRequest{
		Requests: make([]AnthropicBatchRequestItem, len(request.Requests)),
	}

	for i, r := range request.Requests {
		anthropicReq.Requests[i] = AnthropicBatchRequestItem{
			CustomID: r.CustomID,
			Params:   r.Params,
		}

		if anthropicReq.Requests[i].Params == nil && r.Body != nil {
			anthropicReq.Requests[i].Params = r.Body
		}
	}

	jsonData, err := providerUtils.MarshalSorted(anthropicReq)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}
	usedLargePayloadBody := setAnthropicRequestBody(ctx, req, jsonData)

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	var anthropicResp AnthropicBatchResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	return anthropicResp.ToBifrostBatchCreateResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse), nil
}

func (provider *AnthropicProvider) BatchList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.BatchListRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	helper, err := providerUtils.NewSerialListHelper(keys, request.AfterID, provider.logger, true)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("invalid pagination cursor", err)
	}

	key, nativeCursor, ok := helper.GetCurrentKey()
	if !ok {

		return &schemas.BifrostBatchListResponse{
			Object:  "list",
			Data:    []schemas.BifrostBatchRetrieveResponse{},
			HasMore: false,
		}, nil
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	baseURL := provider.buildRequestURL(ctx, "/v1/messages/batches", schemas.BatchListRequest)
	values := url.Values{}
	if request.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", request.Limit))
	}
	if request.BeforeID != nil && *request.BeforeID != "" {
		values.Set("before_id", *request.BeforeID)
	}

	if nativeCursor != "" {
		values.Set("after_id", nativeCursor)
	}
	requestURL := baseURL
	if encodedValues := values.Encode(); encodedValues != "" {
		requestURL += "?" + encodedValues
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	body, decodeErr := providerUtils.CheckAndDecodeBody(resp)
	if decodeErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decodeErr)
	}

	var anthropicResp AnthropicBatchListResponse
	_, _, bifrostErr = providerUtils.HandleProviderResponse(body, &anthropicResp, nil, false, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	batches := make([]schemas.BifrostBatchRetrieveResponse, 0, len(anthropicResp.Data))
	var lastBatchID string
	for _, batch := range anthropicResp.Data {
		batches = append(batches, *batch.ToBifrostBatchRetrieveResponse(latency, false, false, nil, nil))
		lastBatchID = batch.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(anthropicResp.HasMore, lastBatchID)

	bifrostResp := &schemas.BifrostBatchListResponse{
		Object:  "list",
		Data:    batches,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}
	if nextCursor != "" {
		bifrostResp.NextCursor = &nextCursor
	}

	return bifrostResp, nil
}

func (provider *AnthropicProvider) BatchRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.BatchRetrieveRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}
	escapedBatchID, idErr := providerUtils.EscapeResourceID(request.BatchID, "batch_id")
	if idErr != nil {
		return nil, idErr
	}

	providerName := provider.GetProviderKey()
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.buildRequestURL(
			ctx,
			"/v1/messages/batches/"+escapedBatchID,
			schemas.BatchRetrieveRequest,
		))
		req.Header.SetMethod(http.MethodGet)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var anthropicResp AnthropicBatchResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := anthropicResp.ToBifrostBatchRetrieveResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse)
		return result, nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) BatchCancel(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.BatchCancelRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}
	escapedBatchID, idErr := providerUtils.EscapeResourceID(request.BatchID, "batch_id")
	if idErr != nil {
		return nil, idErr
	}

	providerName := provider.GetProviderKey()
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/messages/batches/" + escapedBatchID + "/cancel")
		req.Header.SetMethod(http.MethodPost)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var anthropicResp AnthropicBatchResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := &schemas.BifrostBatchCancelResponse{
			ID:     anthropicResp.ID,
			Object: anthropicResp.Type,
			Status: ToBifrostBatchStatus(anthropicResp.ProcessingStatus),
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if sendBackRawRequest {
			result.ExtraFields.RawRequest = rawRequest
		}

		if anthropicResp.CancelInitiatedAt != nil {
			cancellingAt := parseAnthropicTimestamp(*anthropicResp.CancelInitiatedAt)
			result.CancellingAt = &cancellingAt
		}

		if anthropicResp.RequestCounts != nil {
			result.RequestCounts = schemas.BatchRequestCounts{
				Total:     anthropicResp.RequestCounts.Processing + anthropicResp.RequestCounts.Succeeded + anthropicResp.RequestCounts.Errored + anthropicResp.RequestCounts.Canceled + anthropicResp.RequestCounts.Expired,
				Completed: anthropicResp.RequestCounts.Succeeded,
				Failed:    anthropicResp.RequestCounts.Errored,
			}
		}

		if sendBackRawResponse {
			result.ExtraFields.RawResponse = rawResponse
		}

		return result, nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) BatchDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.BatchDeleteRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) BatchResults(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.BatchResultsRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}
	escapedBatchID, idErr := providerUtils.EscapeResourceID(request.BatchID, "batch_id")
	if idErr != nil {
		return nil, idErr
	}

	providerName := provider.GetProviderKey()

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/messages/batches/" + escapedBatchID + "/results")
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		var results []schemas.BatchResultItem

		parseResult := providerUtils.ParseJSONL(body, func(line []byte) error {
			var anthropicResult AnthropicBatchResultItem
			if err := sonic.Unmarshal(line, &anthropicResult); err != nil {
				provider.logger.Warn("failed to parse batch result line: %v", err)
				return err
			}

			resultItem := schemas.BatchResultItem{
				CustomID: anthropicResult.CustomID,
				Result: &schemas.BatchResultData{
					Type:    anthropicResult.Result.Type,
					Message: anthropicResult.Result.Message,
				},
			}

			if anthropicResult.Result.Error != nil {
				resultItem.Error = &schemas.BatchResultError{
					Code:    anthropicResult.Result.Error.Type,
					Message: anthropicResult.Result.Error.Message,
				}
			}

			results = append(results, resultItem)
			return nil
		})

		batchResultsResp := &schemas.BifrostBatchResultsResponse{
			BatchID:  request.BatchID,
			Endpoint: schemas.BatchEndpointMessages,
			Results:  results,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
			batchResultsResp.ExtraFields.RawResponse = results
		}

		if len(parseResult.Errors) > 0 {
			batchResultsResp.ExtraFields.ParseErrors = parseResult.Errors
		}

		return batchResultsResp, nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) Embedding(ctx *schemas.BifrostContext, key schemas.Key, input *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.EmbeddingRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) Speech(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.SpeechRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) SpeechStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.SpeechStreamRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) Transcription(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.TranscriptionRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) TranscriptionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.TranscriptionStreamRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ImageGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageGenerationRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ImageGenerationStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageGenerationRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageGenerationStreamRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ImageEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageEditRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ImageEditStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageEditStreamRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ImageVariation(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageVariationRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) Rerank(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.RerankRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) Decision(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostDecisionRequest) (*schemas.BifrostDecisionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.DecisionRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) OCR(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.OCRRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) FileUpload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.FileUploadRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	if len(request.File) == 0 {
		return nil, providerUtils.NewBifrostOperationError("file content is required", nil)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	filename := request.Filename
	if filename == "" {
		filename = "file"
	}
	contentType := ""
	if request.ContentType != nil {
		contentType = strings.TrimSpace(*request.ContentType)
	}
	if request.ContentType != nil {
		ct := strings.TrimSpace(*request.ContentType)
		if strings.ContainsAny(ct, "\r\n") {
			return nil, providerUtils.NewBifrostOperationError("invalid content type: %s", fmt.Errorf("contains CR or LF characters"))
		}
		contentType = ct
	}
	var part io.Writer
	var err error
	if contentType != "" {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", multipart.FileContentDisposition("file", filename))
		header.Set("Content-Type", contentType)
		part, err = writer.CreatePart(header)
	} else {
		part, err = writer.CreateFormFile("file", filename)
	}
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to create form file", err)
	}
	if _, err := part.Write(request.File); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to write file content", err)
	}

	if err := writer.Close(); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/files", schemas.FileUploadRequest))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType(writer.FormDataContentType())

	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)
	appendBetaHeader(req, AnthropicFilesAPIBetaHeader)
	req.SetBody(buf.Bytes())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusCreated {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var anthropicResp AnthropicFileResponse
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	return anthropicResp.ToBifrostFileUploadResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse), nil
}

func (provider *AnthropicProvider) FileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.FileListRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	helper, err := providerUtils.NewSerialListHelper(keys, request.After, provider.logger, true)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("invalid pagination cursor", err)
	}

	key, nativeCursor, ok := helper.GetCurrentKey()
	if !ok {

		return &schemas.BifrostFileListResponse{
			Object:  "list",
			Data:    []schemas.FileObject{},
			HasMore: false,
		}, nil
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	requestURL := provider.buildRequestURL(ctx, "/v1/files", schemas.FileListRequest)
	values := url.Values{}
	if request.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", request.Limit))
	}

	if nativeCursor != "" {
		values.Set("after_id", nativeCursor)
	}
	if encodedValues := values.Encode(); encodedValues != "" {
		requestURL += "?" + encodedValues
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	req.Header.Set("anthropic-version", provider.apiVersion)
	appendBetaHeader(req, AnthropicFilesAPIBetaHeader)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseAnthropicError(resp), latency)
	}

	body, decodeErr := providerUtils.CheckAndDecodeBody(resp)
	if decodeErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decodeErr)
	}

	var anthropicResp AnthropicFileListResponse
	_, _, bifrostErr = providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	files := make([]schemas.FileObject, 0, len(anthropicResp.Data))
	var lastFileID string
	for _, file := range anthropicResp.Data {
		files = append(files, schemas.FileObject{
			ID:           file.ID,
			Object:       file.Type,
			Bytes:        file.SizeBytes,
			CreatedAt:    parseAnthropicFileTimestamp(file.CreatedAt),
			Filename:     file.Filename,
			ContentType:  file.MimeType,
			Downloadable: file.Downloadable,
			Purpose:      schemas.FilePurposeBatch,
			Status:       schemas.FileStatusProcessed,
		})
		lastFileID = file.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(anthropicResp.HasMore, lastFileID)

	bifrostResp := &schemas.BifrostFileListResponse{
		Object:  "list",
		Data:    files,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}
	if nextCursor != "" {
		bifrostResp.After = &nextCursor
	}

	return bifrostResp, nil
}

func (provider *AnthropicProvider) FileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.FileRetrieveRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("file_id is required", nil)
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.buildRequestURL(
			ctx,
			"/v1/files/"+escapedFileID,
			schemas.FileRetrieveRequest,
		))
		req.Header.SetMethod(http.MethodGet)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)
		appendBetaHeader(req, AnthropicFilesAPIBetaHeader)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var anthropicResp AnthropicFileResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		return anthropicResp.ToBifrostFileRetrieveResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse), nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) FileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.FileDeleteRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("file_id is required", nil)
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedFileID)
		req.Header.SetMethod(http.MethodDelete)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)
		appendBetaHeader(req, AnthropicFilesAPIBetaHeader)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusNoContent {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		if resp.StatusCode() == fasthttp.StatusNoContent {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			return &schemas.BifrostFileDeleteResponse{
				ID:      request.FileID,
				Object:  "file",
				Deleted: true,
				ExtraFields: schemas.BifrostResponseExtraFields{
					Latency: latency.Milliseconds(),
				},
			}, nil
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var anthropicResp AnthropicFileDeleteResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &anthropicResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := &schemas.BifrostFileDeleteResponse{
			ID:      anthropicResp.ID,
			Object:  "file",
			Deleted: anthropicResp.Type == "file_deleted",
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if sendBackRawRequest {
			result.ExtraFields.RawRequest = rawRequest
		}

		if sendBackRawResponse {
			result.ExtraFields.RawResponse = rawResponse
		}

		return result, nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) FileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.FileContentRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("file_id is required", nil)
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedFileID + "/content")
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("x-api-key", key.Value.GetValue())
		}
		req.Header.Set("anthropic-version", provider.apiVersion)
		appendBetaHeader(req, AnthropicFilesAPIBetaHeader)

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		if bifrostErr != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseAnthropicError(resp)
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			wait()
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		contentType := string(resp.Header.ContentType())
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		content := append([]byte(nil), body...)
		wait()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		return &schemas.BifrostFileContentResponse{
			FileID:      request.FileID,
			Content:     content,
			ContentType: contentType,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}, nil
	}

	return nil, lastErr
}

func (provider *AnthropicProvider) CountTokens(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.CountTokensRequest); err != nil {
		return nil, err
	}
	return HandleAnthropicCountTokensRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/messages/count_tokens", schemas.CountTokensRequest),
		request,
		AnthropicRequestBuildConfig{
			Provider:                  schemas.Anthropic,
			Model:                     request.Model,
			IsCountTokens:             true,
			IsStreaming:               false,
			BetaHeaderOverrides:       provider.networkConfig.BetaHeaderOverrides,
			ShouldSendBackRawRequest:  provider.sendBackRawRequest,
			ShouldSendBackRawResponse: provider.sendBackRawResponse,
		},
		provider.anthropicRequestHeaders(ctx, key),
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.logger,
	)
}

func HandleAnthropicCountTokensRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostResponsesRequest,
	config AnthropicRequestBuildConfig,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	config.IsStreaming = false
	config.ExcludeFields = append(append([]string{}, config.ExcludeFields...), "max_tokens", "temperature")

	jsonBody, err := BuildAnthropicResponsesRequestBody(ctx, request, config)
	if err != nil {
		return nil, err
	}

	responseBody, latency, providerResponseHeaders, bifrostErr := completeRequest(ctx, client, url, jsonBody, headers, extraHeaders, config.BetaHeaderOverrides, config.Provider, schemas.CountTokensRequest, signer, logger)
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, responseBody, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	anthropicResponse := &AnthropicCountTokensResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(
		responseBody,
		anthropicResponse,
		jsonBody,
		providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse),
	)

	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, responseBody, config.ShouldSendBackRawRequest, config.ShouldSendBackRawResponse, latency)
	}

	response := anthropicResponse.ToBifrostCountTokensResponse(request.Model)
	response.Model = request.Model

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, config.ShouldSendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, config.ShouldSendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *AnthropicProvider) Compaction(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCompactionRequest) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.CompactionRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoGeneration(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoGenerationRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoRetrieve(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoRetrieveRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoDownload(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoDownloadRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoDelete(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoDeleteRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoList(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoListRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoEdit(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoEditRequest) (*schemas.BifrostVideoEditResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoEditRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) VideoRemix(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoRemixRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerCreateRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerListRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerRetrieveRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerDeleteRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerFileCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileCreateRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerFileList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileListRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerFileRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileRetrieveRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerFileContent(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileContentRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) ContainerFileDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileDeleteRequest, provider.GetProviderKey())
}

func (provider *AnthropicProvider) Passthrough(
	ctx *schemas.BifrostContext,
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.PassthroughRequest); err != nil {
		return nil, err
	}

	url, err := providerUtils.BuildPassthroughURL(provider.networkConfig.BaseURL, req.Path, req.RawQuery)
	if err != nil {
		return nil, providerUtils.NewBifrostBadRequestError(err.Error())
	}

	fasthttpReq := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	defer fasthttp.ReleaseRequest(fasthttpReq)

	fasthttpReq.Header.SetMethod(req.Method)
	fasthttpReq.SetRequestURI(url)

	providerUtils.SetExtraHeaders(ctx, fasthttpReq, provider.networkConfig.ExtraHeaders, nil)

	providerUtils.StripCallerAuthForInsecureURL(url, req.SafeHeaders)
	for k, v := range req.SafeHeaders {
		fasthttpReq.Header.Set(k, v)
	}

	if key.Value.GetValue() != "" {
		fasthttpReq.Header.Set("x-api-key", key.Value.GetValue())
	}
	fasthttpReq.Header.Set("anthropic-version", provider.apiVersion)

	fasthttpReq.SetBody(req.Body)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, fasthttpReq, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	headers := providerUtils.ExtractPassthroughProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to decode response body", err)
	}

	var passthroughUsage *schemas.BifrostPassthroughUsage
	if resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
		passthroughUsage = ExtractAnthropicPassthroughUsage(req.Path, req.Body, body)
	}

	bifrostResponse := &schemas.BifrostPassthroughResponse{
		StatusCode: resp.StatusCode(),
		Headers:    headers,
		Body:       body,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:                 latency.Milliseconds(),
			ProviderResponseHeaders: headers,
			PassthroughPath:         req.Path,
		},
		PassthroughUsage: passthroughUsage,
	}

	return bifrostResponse, nil
}

func (provider *AnthropicProvider) PassthroughStream(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Anthropic, provider.customProviderConfig, schemas.PassthroughStreamRequest); err != nil {
		return nil, err
	}

	url, err := providerUtils.BuildPassthroughURL(provider.networkConfig.BaseURL, req.Path, req.RawQuery)
	if err != nil {
		return nil, providerUtils.NewBifrostBadRequestError(err.Error())
	}

	startTime := time.Now()

	fasthttpReq := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(fasthttpReq)

	fasthttpReq.Header.SetMethod(req.Method)
	fasthttpReq.SetRequestURI(url)

	providerUtils.SetExtraHeaders(ctx, fasthttpReq, provider.networkConfig.ExtraHeaders, nil)

	providerUtils.StripCallerAuthForInsecureURL(url, req.SafeHeaders)
	for k, v := range req.SafeHeaders {
		fasthttpReq.Header.Set(k, v)
	}

	fasthttpReq.Header.Set("Connection", "close")

	if key.Value.GetValue() != "" {
		fasthttpReq.Header.Set("x-api-key", key.Value.GetValue())
	}
	fasthttpReq.Header.Set("anthropic-version", provider.apiVersion)

	fasthttpReq.SetBody(req.Body)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.streamingClient, resp)
	err = providerUtils.DoStreamingRequest(ctx, activeClient, fasthttpReq, resp)
	latency := time.Since(startTime)
	if err != nil {
		providerUtils.ReleaseStreamingResponse(ctx, resp)
		if errors.Is(err, context.Canceled) {
			return nil, providerUtils.SetErrorLatency(&schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
					Error:   err,
				},
			}, latency)
		}
		if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), latency)
		}
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), latency)
	}

	headers := providerUtils.ExtractPassthroughProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)

	bodyStream := resp.BodyStream()
	if bodyStream == nil {
		providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.NewBifrostOperationError(
			"provider returned an empty stream body",
			fmt.Errorf("provider returned an empty stream body"),
		)
	}

	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, provider.networkConfig.StreamIdleTimeoutInSeconds)
	strippedPath := req.Path
	if idx := strings.IndexByte(strippedPath, '?'); idx >= 0 {
		strippedPath = strippedPath[:idx]
	}
	var messagesUsage *AnthropicPassthroughStreamUsage
	if strings.HasSuffix(strippedPath, "/messages") {
		messagesUsage = &AnthropicPassthroughStreamUsage{}
	}
	return providerUtils.StreamPassthrough(
		ctx, postHookRunner, postHookSpanFinalizer, resp, bodyStream,
		providerUtils.PassthroughStreamParams{
			StatusCode:       resp.StatusCode(),
			Headers:          headers,
			Path:             req.Path,
			RawRequest:       req.Body,
			CancellationBody: req.Body,
			StartTime:        startTime,
			Logger:           provider.logger,
			HasUsage:         HasAnthropicPassthroughUsage,
			Observe: func(event []byte) *schemas.BifrostPassthroughUsage {
				if messagesUsage != nil {
					return messagesUsage.ObserveEvent(event)
				}
				return ExtractAnthropicPassthroughUsage(req.Path, req.Body, event)
			},
		},
	), nil
}
