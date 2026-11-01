package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type GeminiProvider struct {
	logger               schemas.Logger
	client               *fasthttp.Client
	streamingClient      *fasthttp.Client
	networkConfig        schemas.NetworkConfig
	sendBackRawRequest   bool
	sendBackRawResponse  bool
	customProviderConfig *schemas.CustomProviderConfig
}

const (
	BifrostContextKeyResponseFormat schemas.BifrostContextKey = "bifrost_context_key_response_format"
)

func setGeminiAuthHeader(req *fasthttp.Request, apiKey string) {
	if apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}

	if len(req.Header.Peek("x-goog-api-key")) > 0 {
		req.Header.Del("Authorization")
	}
}

func setGeminiRequestBody(req *fasthttp.Request, bodyReader io.Reader, bodySize int, jsonData []byte) {

	if bodyReader != nil {
		req.SetBodyStream(bodyReader, bodySize)
		return
	}
	req.SetBody(jsonData)
}

func normalizeRawGenerateContentBody(ctx *schemas.BifrostContext, body []byte, providerName schemas.ModelProvider, model string) []byte {
	if rawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && rawBody {
		body = NormalizeRawGenerateContentRequestForCompatibility(ctx, body, providerName, model)
	}
	if updated, err := providerUtils.DeleteJSONField(body, "fallbacks"); err == nil {
		body = updated
	}
	return body
}

func NewGeminiProvider(config *schemas.ProviderConfig, logger schemas.Logger) *GeminiProvider {
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

	client = providerUtils.ConfigureProxy(client, config.ProxyConfig, logger)
	client = providerUtils.ConfigureDialer(client, config.NetworkConfig.AllowPrivateNetwork)
	client = providerUtils.ConfigureTLS(client, config.NetworkConfig, logger)
	streamingClient := providerUtils.BuildStreamingClient(client)

	if config.NetworkConfig.BaseURL == "" {
		config.NetworkConfig.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")

	return &GeminiProvider{
		logger:               logger,
		client:               client,
		streamingClient:      streamingClient,
		networkConfig:        config.NetworkConfig,
		customProviderConfig: config.CustomProviderConfig,
		sendBackRawRequest:   config.SendBackRawRequest,
		sendBackRawResponse:  config.SendBackRawResponse,
	}
}

func (provider *GeminiProvider) GetProviderKey() schemas.ModelProvider {
	return providerUtils.GetProviderName(schemas.Gemini, provider.customProviderConfig)
}

func (provider *GeminiProvider) completeRequest(ctx *schemas.BifrostContext, model string, key schemas.Key, jsonBody []byte, endpoint string) (*GenerateContentResponse, interface{}, time.Duration, map[string]string, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+model+endpoint))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	usedLargePayloadBody := providerUtils.ApplyLargePayloadRequestBody(ctx, req)
	if !usedLargePayloadBody {
		jsonBody = normalizeRawGenerateContentBody(ctx, jsonBody, provider.GetProviderKey(), model)
		req.SetBody(jsonBody)
	}

	activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.client, resp)
	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, nil, latency, nil, bifrostErr
	}
	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, nil, latency, providerResponseHeaders, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, isLargeResp, decodeErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, provider.logger)
	if decodeErr != nil {
		return nil, nil, latency, providerResponseHeaders, decodeErr
	}
	if isLargeResp {
		respOwned = false
		return nil, nil, latency, providerResponseHeaders, nil
	}

	var geminiResponse GenerateContentResponse
	pt, ph := providerUtils.StartResponseParseSpan(ctx)
	umErr := sonic.Unmarshal(body, &geminiResponse)
	if pt != nil {
		if umErr != nil {
			pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
		} else {
			pt.EndSpan(ph, schemas.SpanStatusOk, "")
		}
	}
	if umErr != nil {
		return nil, nil, latency, providerResponseHeaders, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, umErr)
	}

	var rawResponse interface{}
	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		if err := sonic.Unmarshal(body, &rawResponse); err != nil {
			return nil, nil, latency, providerResponseHeaders, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
		}
	}

	return &geminiResponse, rawResponse, latency, providerResponseHeaders, nil
}

func (provider *GeminiProvider) listModelsByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	providerName := provider.GetProviderKey()

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, fmt.Sprintf("/models?pageSize=%d", schemas.DefaultPageSize)))
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	var geminiResponse GeminiListModelsResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(resp.Body(), &geminiResponse, nil, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if len(geminiResponse.Models) == 0 {
		var singleModel GeminiModel
		if err := sonic.Unmarshal(resp.Body(), &singleModel); err == nil && singleModel.Name != "" {
			geminiResponse.Models = []GeminiModel{singleModel}
		}
	}

	response := geminiResponse.ToBifrostListModelsResponse(providerName, key.Models, key.BlacklistedModels, key.Aliases, request.Unfiltered)

	response.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) ListModels(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ListModelsRequest); err != nil {
		return nil, err
	}
	if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
		return providerUtils.HandleKeylessListModelsRequest(provider.GetProviderKey(), func() (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
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

func (provider *GeminiProvider) ModelRetrieve(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostModelRetrieveRequest) (*schemas.BifrostModelRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ModelRetrieveRequest); err != nil {
		return nil, err
	}
	if request == nil || request.Model == "" {
		return nil, providerUtils.NewBifrostOperationError("model is required", nil)
	}

	escapedModel, idErr := providerUtils.EscapeResourceID(strings.TrimPrefix(request.Model, "models/"), "model")
	if idErr != nil {
		return nil, idErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+escapedModel))
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var geminiModel GeminiModel
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(resp.Body(), &geminiModel, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	response := geminiModel.ToBifrostModelRetrieveResponse(provider.GetProviderKey())
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

func (provider *GeminiProvider) TextCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.TextCompletionRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) TextCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.TextCompletionStreamRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ChatCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ChatCompletionRequest); err != nil {
		return nil, err
	}

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiChatCompletionRequest(ctx, request)
		})
	if err != nil {
		return nil, err
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
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

	convTracer, convHandle := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := geminiResponse.ToBifrostChatResponse()
	if convTracer != nil {
		convTracer.EndSpan(convHandle, schemas.SpanStatusOk, "")
	}

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&bifrostResponse.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResponse, nil
}

func (provider *GeminiProvider) ChatCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ChatCompletionStreamRequest); err != nil {
		return nil, err
	}

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody, err := ToGeminiChatCompletionRequest(ctx, request)
			if err != nil {
				return nil, err
			}
			if reqBody == nil {
				return nil, fmt.Errorf("chat completion request is not provided or could not be converted to gemini format")
			}
			return reqBody, nil
		})
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}
	if key.Value.GetValue() != "" {
		headers["x-goog-api-key"] = key.Value.GetValue()
	}

	return HandleGeminiChatCompletionStream(
		ctx,
		provider.streamingClient,
		provider.networkConfig.BaseURL+providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":streamGenerateContent?alt=sse"),
		jsonData,
		headers,
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		request.Model,
		postHookRunner,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleGeminiChatCompletionStream(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	jsonBody []byte,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	model string,
	postHookRunner schemas.PostHookRunner,
	postResponseConverter func(*schemas.BifrostChatResponse) *schemas.BifrostChatResponse,
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
	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	if len(req.Header.Peek("x-goog-api-key")) > 0 {
		req.Header.Del("Authorization")
	}

	if !providerUtils.ApplyLargePayloadRequestBody(ctx, req) {
		jsonBody = normalizeRawGenerateContentBody(ctx, jsonBody, providerName, model)
		req.SetBody(jsonBody)
	}

	startTime := time.Now()

	doErr := providerUtils.DoStreamingRequest(ctx, client, req, resp)
	latency := time.Since(startTime)
	if doErr != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		if errors.Is(doErr, context.Canceled) {
			return nil, providerUtils.EnrichError(ctx, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
					Error:   doErr,
				},
			}, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		if errors.Is(doErr, fasthttp.ErrTimeout) || errors.Is(doErr, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, doErr), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, doErr), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		respBody := append([]byte(nil), resp.Body()...)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonBody, respBody, sendBackRawRequest, sendBackRawResponse, latency)
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

		decompressedReader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		decompressedReader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(decompressedReader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		skipInlineData := shouldSkipInlineDataForStreamingContext(ctx)
		var lineReader *bufio.Reader
		var sseReader providerUtils.SSEDataReader
		if skipInlineData {
			lineReader = bufio.NewReaderSize(decompressedReader, 64*1024)
		} else {
			sseReader = providerUtils.GetSSEDataReader(ctx, decompressedReader)
		}

		chunkIndex := 0
		lastChunkTime := startTime

		var responseID string
		var modelName string

		streamState := NewGeminiStreamState()

		streamUsage := &schemas.BifrostLLMUsage{}
		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, streamUsage)

	readLoop:
		for {

			if ctx.Err() != nil {
				return
			}
			var (
				eventData []byte
				readErr   error
			)
			if skipInlineData {
				eventData, readErr = readNextSSEDataLine(lineReader, true)
			} else {
				eventData, readErr = sseReader.ReadDataLine()
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {

				if ctx.Err() != nil {
					return
				}
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				logger.Warn("Error reading stream: %v", readErr)
				providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
				return
			}

			parseStart := time.Now()
			geminiResponse, err := processGeminiStreamChunk(eventData)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if err != nil {
				if strings.Contains(err.Error(), "gemini api error") {

					bifrostErr := toGeminiStreamBifrostError(err)
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
					return
				}
				logger.Warn("Failed to process chunk: %v", err)
				continue
			}

			if geminiResponse.ResponseID != "" && responseID == "" {
				responseID = geminiResponse.ResponseID
			}
			if geminiResponse.ModelVersion != "" && modelName == "" {
				modelName = geminiResponse.ModelVersion
			}

			convStart := time.Now()
			responses, bifrostErr, isLastChunk := geminiResponse.ToBifrostChatCompletionStream(streamState)
			schemas.AddStreamConvert(ctx, time.Since(convStart))
			if bifrostErr != nil {
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
				return
			}

			for i, response := range responses {
				isLastDelta := isLastChunk && i == len(responses)-1
				response.ID = responseID
				if modelName != "" {
					response.Model = modelName
				}

				if response.Usage != nil {
					*streamUsage = *response.Usage
				}
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

				if sendBackRawResponse && i == len(responses)-1 {
					response.ExtraFields.RawResponse = string(eventData)
				}

				lastChunkTime = time.Now()
				chunkIndex++

				if isLastDelta {
					if sendBackRawRequest {
						providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
					}
					response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
					break readLoop
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
			}
		}
	}()

	return responseChan, nil
}

func (provider *GeminiProvider) Responses(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ResponsesRequest); err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	bodySize := -1
	var jsonData []byte

	if isLargePayload, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool); ok && isLargePayload {
		if reader, readerOk := ctx.Value(schemas.BifrostContextKeyLargePayloadReader).(io.Reader); readerOk && reader != nil {
			bodyReader = reader
			if contentLength, lenOk := ctx.Value(schemas.BifrostContextKeyLargePayloadContentLength).(int); lenOk {
				bodySize = contentLength
			}
		}
	}

	if bodyReader == nil {
		var err *schemas.BifrostError
		jsonData, err = providerUtils.CheckContextAndGetRequestBody(
			ctx,
			request,
			func() (providerUtils.RequestBodyWithExtraParams, error) {
				reqBody, err := ToGeminiResponsesRequest(ctx, request)
				if err != nil {
					return nil, err
				}
				if reqBody == nil {
					return nil, fmt.Errorf("responses input is not provided or could not be converted to gemini format")
				}
				return reqBody, nil
			})
		if err != nil {
			return nil, err
		}
	}

	if responseThreshold, ok := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64); ok && responseThreshold > 0 {
		return provider.responsesWithLargeResponseDetection(ctx, key, request, jsonData, responseThreshold, bodyReader, bodySize)
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostResponsesResponse{
			Model: request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	bifrostResponse := geminiResponse.ToResponsesBifrostResponsesResponse()

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&bifrostResponse.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResponse, nil
}

func (provider *GeminiProvider) responsesWithLargeResponseDetection(
	ctx *schemas.BifrostContext,
	key schemas.Key,
	request *schemas.BifrostResponsesRequest,
	jsonData []byte,
	responseThreshold int64,
	bodyReader io.Reader,
	bodySize int,
) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	resp.StreamBody = true

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":generateContent"))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	if bodyReader == nil {
		jsonData = normalizeRawGenerateContentBody(ctx, jsonData, provider.GetProviderKey(), request.Model)
	}
	setGeminiRequestBody(req, bodyReader, bodySize, jsonData)

	streamingClient := providerUtils.BuildLargeResponseClient(provider.client, responseThreshold)
	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, streamingClient, req, resp)
	if bifrostErr != nil {
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		bifrostErr := parseGeminiError(resp)
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	responseBody, isLarge, respErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, provider.logger)
	if respErr != nil {
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, providerUtils.EnrichError(ctx, respErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}
	if isLarge {

		preview, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadResponsePreview).(string)
		usage := extractUsageFromResponsePrefetch([]byte(preview))
		bifrostResponse := &schemas.BifrostResponsesResponse{
			ID:        schemas.Ptr("resp_" + schemas.GetRandomString(50)),
			CreatedAt: int(time.Now().Unix()),
			Model:     request.Model,
			Usage:     usage,
		}
		bifrostResponse.ExtraFields.Latency = latency.Milliseconds()

		wait()
		return bifrostResponse, nil
	}
	wait()
	fasthttp.ReleaseResponse(resp)

	var geminiResponse GenerateContentResponse
	pt, ph := providerUtils.StartResponseParseSpan(ctx)
	unmarshalErr := sonic.Unmarshal(responseBody, &geminiResponse)
	if pt != nil {
		if unmarshalErr != nil {
			pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
		} else {
			pt.EndSpan(ph, schemas.SpanStatusOk, "")
		}
	}
	if unmarshalErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, unmarshalErr)
	}
	ct, ch := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := geminiResponse.ToResponsesBifrostResponsesResponse()
	if ct != nil {
		ct.EndSpan(ch, schemas.SpanStatusOk, "")
	}
	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&bifrostResponse.ExtraFields, jsonData)
	}
	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		var rawResponse interface{}
		sonic.Unmarshal(responseBody, &rawResponse)
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}

func extractUsageFromResponsePrefetch(data []byte) *schemas.ResponsesResponseUsage {
	node, err := sonic.Get(data, "usageMetadata")
	if err != nil {
		return nil
	}
	raw, _ := node.Raw()
	if raw == "" {
		return nil
	}

	var usageMeta GenerateContentResponseUsageMetadata
	if err := sonic.UnmarshalString(raw, &usageMeta); err != nil {
		return nil
	}

	return ConvertGeminiUsageMetadataToResponsesUsage(&usageMeta)
}

func (provider *GeminiProvider) ResponsesStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ResponsesStreamRequest); err != nil {
		return nil, err
	}

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody, err := ToGeminiResponsesRequest(ctx, request)
			if err != nil {
				return nil, err
			}
			if reqBody == nil {
				return nil, fmt.Errorf("responses input is not provided or could not be converted to gemini format")
			}
			return reqBody, nil
		})
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}
	if key.Value.GetValue() != "" {
		headers["x-goog-api-key"] = key.Value.GetValue()
	}

	return HandleGeminiResponsesStream(
		ctx,
		provider.streamingClient,
		provider.networkConfig.BaseURL+providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":streamGenerateContent?alt=sse"),
		jsonData,
		headers,
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		request.Model,
		postHookRunner,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleGeminiResponsesStream(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	jsonBody []byte,
	headers map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	model string,
	postHookRunner schemas.PostHookRunner,
	postResponseConverter func(*schemas.BifrostResponsesStreamResponse) *schemas.BifrostResponsesStreamResponse,
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
	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	if len(req.Header.Peek("x-goog-api-key")) > 0 {
		req.Header.Del("Authorization")
	}

	if !providerUtils.ApplyLargePayloadRequestBody(ctx, req) {
		jsonBody = normalizeRawGenerateContentBody(ctx, jsonBody, providerName, model)
		req.SetBody(jsonBody)
	}

	startTime := time.Now()

	doErr := providerUtils.DoStreamingRequest(ctx, client, req, resp)
	latency := time.Since(startTime)
	if doErr != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		if errors.Is(doErr, context.Canceled) {
			return nil, providerUtils.EnrichError(ctx, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Type:    schemas.Ptr(schemas.RequestCancelled),
					Message: schemas.ErrRequestCancelled,
					Error:   doErr,
				},
			}, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		if errors.Is(doErr, fasthttp.ErrTimeout) || errors.Is(doErr, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, doErr), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, doErr), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
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
			providerUtils.ProcessAndSendBifrostError(
				ctx,
				postHookRunner,
				providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse),
				responseChan,
				logger,
				postHookSpanFinalizer,
			)
			return
		}

		decompressedReader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		decompressedReader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(decompressedReader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		skipInlineData := shouldSkipInlineDataForStreamingContext(ctx)
		var lineReader *bufio.Reader
		var sseReader providerUtils.SSEDataReader
		if skipInlineData {
			lineReader = bufio.NewReaderSize(decompressedReader, 64*1024)
		} else {
			sseReader = providerUtils.GetSSEDataReader(ctx, decompressedReader)
		}

		chunkIndex := 0
		sequenceNumber := 0
		lastChunkTime := startTime

		streamState := acquireGeminiResponsesStreamState()
		defer releaseGeminiResponsesStreamState(streamState)

		var lastUsageMetadata *GenerateContentResponseUsageMetadata

		streamUsage := &schemas.BifrostLLMUsage{}
		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, streamUsage)

		for {

			if ctx.Err() != nil {
				return
			}
			var (
				eventData []byte
				readErr   error
			)
			if skipInlineData {
				eventData, readErr = readNextSSEDataLine(lineReader, true)
			} else {
				eventData, readErr = sseReader.ReadDataLine()
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				logger.Warn("Error reading stream: %v", readErr)
				providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
				return
			}

			parseStart := time.Now()
			geminiResponse, err := processGeminiStreamChunk(eventData)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if err != nil {
				if strings.Contains(err.Error(), "gemini api error") {

					bifrostErr := toGeminiStreamBifrostError(err)
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse), responseChan, logger, postHookSpanFinalizer)
					return
				}
				logger.Warn("Failed to process chunk: %v", err)
				continue
			}

			if geminiResponse.UsageMetadata != nil {
				lastUsageMetadata = geminiResponse.UsageMetadata

				if converted := ConvertGeminiUsageMetadataToChatUsage(lastUsageMetadata); converted != nil {
					*streamUsage = *converted
				}
			}

			convStart := time.Now()
			responses, bifrostErr := geminiResponse.ToBifrostResponsesStream(sequenceNumber, streamState)
			schemas.AddStreamConvert(ctx, time.Since(convStart))
			if bifrostErr != nil {
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse), responseChan, logger, postHookSpanFinalizer)
				return
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

					if sendBackRawResponse && i == len(responses)-1 {
						response.ExtraFields.RawResponse = string(eventData)
					}

					chunkIndex++
					sequenceNumber++

					isLastChunk := false
					if response.Type == schemas.ResponsesStreamResponseTypeCompleted || response.Type == schemas.ResponsesStreamResponseTypeIncomplete ||
						response.Type == schemas.ResponsesStreamResponseTypeFailed {
						isLastChunk = true
					}

					if isLastChunk {
						if sendBackRawRequest {
							providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
						}
						response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, response, nil, nil, nil), responseChan, postHookSpanFinalizer)
						return
					}

					if i == len(responses)-1 {
						lastChunkTime = time.Now()
					}

					providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, response, nil, nil, nil), responseChan, postHookSpanFinalizer)
				}
			}
		}

		if streamState.Model == nil {
			streamState.Model = &model
		}
		finalResponses := FinalizeGeminiResponsesStream(streamState, lastUsageMetadata, sequenceNumber)
		for i, finalResponse := range finalResponses {
			if finalResponse == nil {
				logger.Warn("FinalizeGeminiResponsesStream returned nil; skipping final response")
				continue
			}
			finalResponse.ExtraFields = schemas.BifrostResponseExtraFields{
				ChunkIndex: chunkIndex,
				Latency:    time.Since(lastChunkTime).Milliseconds(),
			}

			if postResponseConverter != nil {
				finalResponse = postResponseConverter(finalResponse)
				if finalResponse == nil {
					logger.Warn("postResponseConverter returned nil; skipping final response")
					continue
				}
			}

			chunkIndex++
			sequenceNumber++

			if sendBackRawResponse {
				finalResponse.ExtraFields.RawResponse = "{}"
			}
			isLast := i == len(finalResponses)-1

			if isLast {
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				finalResponse.ExtraFields.Latency = time.Since(startTime).Milliseconds()
			}
			providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, finalResponse, nil, nil, nil), responseChan, postHookSpanFinalizer)
		}
	}()

	return responseChan, nil
}

func (provider *GeminiProvider) Embedding(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.EmbeddingRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiEmbeddingRequest(request)
		})
	if err != nil {
		return nil, err
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	endpoint := "/models/" + request.Model + ":batchEmbedContents"
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, endpoint))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	usedLargePayloadBody := providerUtils.ApplyLargePayloadRequestBody(ctx, req)
	if !usedLargePayloadBody {
		req.SetBody(jsonData)
	}

	activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.client, resp)
	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	if bifrostErr != nil {
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		provider.logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		parsedErr := providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, parsedErr
	}

	body, isLargeResp, decodeErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, provider.logger)
	if decodeErr != nil {
		wait()
		fasthttp.ReleaseResponse(resp)
		return nil, providerUtils.EnrichError(ctx, decodeErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}
	if isLargeResp {

		wait()
		return &schemas.BifrostEmbeddingResponse{
			Model: request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}
	wait()
	fasthttp.ReleaseResponse(resp)

	var geminiResponse GeminiEmbeddingResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, body, &geminiResponse, jsonData,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	ct, ch := providerUtils.StartResponseConvertorSpan(ctx)
	bifrostResponse := ToBifrostEmbeddingResponse(&geminiResponse, request.Model)
	if ct != nil {
		if bifrostResponse == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "failed to convert Gemini embedding response to Bifrost format")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if bifrostResponse == nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal,
			fmt.Errorf("failed to convert Gemini embedding response to Bifrost format"))
	}

	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResponse, nil
}

func (provider *GeminiProvider) Speech(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.SpeechRequest); err != nil {
		return nil, err
	}

	jsonData, err := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiSpeechRequest(request)
		})
	if err != nil {
		return nil, err
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostSpeechResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	if request.Params != nil {
		ctx.SetValue(BifrostContextKeyResponseFormat, request.Params.ResponseFormat)
	}
	response, convErr := geminiResponse.ToBifrostSpeechResponse(ctx)
	if convErr != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, convErr), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) Rerank(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.RerankRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) Decision(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostDecisionRequest) (*schemas.BifrostDecisionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.DecisionRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) OCR(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.OCRRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) SpeechStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.SpeechStreamRequest); err != nil {
		return nil, err
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiSpeechRequest(request)
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":streamGenerateContent?alt=sse"))
	req.Header.SetContentType("application/json")

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	if !providerUtils.ApplyLargePayloadRequestBody(ctx, req) {
		req.SetBody(jsonBody)
	}

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, provider.streamingClient, req, resp)
	latency := time.Since(startTime)
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
			}, jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		}
		if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		}

		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, provider.networkConfig.StreamIdleTimeoutInSeconds)

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
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, provider.logger, postHookSpanFinalizer, jsonBody)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, provider.logger, postHookSpanFinalizer, jsonBody)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()

		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), provider.logger)
		defer stopCancellation()

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)
		chunkIndex := -1
		usage := &schemas.SpeechUsage{}
		lastChunkTime := startTime

		for {

			if ctx.Err() != nil {
				return
			}

			data, readErr := sseReader.ReadDataLine()
			if readErr != nil {

				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					provider.logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, provider.logger, postHookSpanFinalizer)
					return
				}
				break
			}

			jsonData := data

			parseStart := time.Now()
			geminiResponse, err := processGeminiStreamChunk(jsonData)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if err != nil {
				if strings.Contains(err.Error(), "gemini api error") {

					bifrostErr := toGeminiStreamBifrostError(err)
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, provider.logger, postHookSpanFinalizer)
					return
				}
				provider.logger.Warn("Failed to process chunk: %v", err)
				continue
			}

			var audioChunk []byte
			if len(geminiResponse.Candidates) > 0 {
				candidate := geminiResponse.Candidates[0]
				if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
					var buf []byte
					for _, part := range candidate.Content.Parts {
						if part.InlineData != nil && len(part.InlineData.Data) > 0 {

							decodedData, err := decodeBase64StringToBytes(part.InlineData.Data)
							if err != nil {
								provider.logger.Warn("Failed to decode base64 audio data: %v", err)
								continue
							}
							buf = append(buf, decodedData...)
						}
					}
					if len(buf) > 0 {
						audioChunk = buf
					}
				}
			}

			if len(geminiResponse.Candidates) > 0 && (geminiResponse.Candidates[0].FinishReason != "" || geminiResponse.UsageMetadata != nil) {

				inputTokens, outputTokens, totalTokens := extractGeminiUsageMetadata(geminiResponse)
				usage.InputTokens = inputTokens
				usage.OutputTokens = outputTokens
				usage.TotalTokens = totalTokens
			}

			if len(audioChunk) > 0 {
				chunkIndex++

				response := &schemas.BifrostSpeechStreamResponse{
					Type:  schemas.SpeechStreamResponseTypeDelta,
					Audio: audioChunk,
					ExtraFields: schemas.BifrostResponseExtraFields{
						ChunkIndex: chunkIndex,
						Latency:    time.Since(lastChunkTime).Milliseconds(),
					},
				}
				lastChunkTime = time.Now()

				if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
					response.ExtraFields.RawResponse = jsonData
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, response, nil, nil), responseChan, postHookSpanFinalizer)
			}
		}
		response := &schemas.BifrostSpeechStreamResponse{
			Type:  schemas.SpeechStreamResponseTypeDone,
			Usage: usage,
			ExtraFields: schemas.BifrostResponseExtraFields{
				ChunkIndex: chunkIndex + 1,
				Latency:    time.Since(startTime).Milliseconds(),
			},
		}
		response.BackfillParams(request)

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
		}
		ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
		providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, response, nil, nil), responseChan, postHookSpanFinalizer)
	}()

	return responseChan, nil
}

func (provider *GeminiProvider) Transcription(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.TranscriptionRequest); err != nil {
		return nil, err
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiTranscriptionRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostTranscriptionResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response := geminiResponse.ToBifrostTranscriptionResponse()

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) TranscriptionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.TranscriptionStreamRequest); err != nil {
		return nil, err
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiTranscriptionRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":streamGenerateContent?alt=sse"))
	req.Header.SetContentType("application/json")

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	if !providerUtils.ApplyLargePayloadRequestBody(ctx, req) {
		req.SetBody(jsonBody)
	}

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, provider.streamingClient, req, resp)
	latency := time.Since(startTime)
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
			}, jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		}
		if errors.Is(err, fasthttp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, err), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		}

		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostUpstreamConnectionError(schemas.ErrProviderDoRequest, err), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonBody, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, provider.networkConfig.StreamIdleTimeoutInSeconds)

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
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, provider.logger, postHookSpanFinalizer, jsonBody)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, provider.logger, postHookSpanFinalizer, jsonBody)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), provider.logger)
		defer stopCancellation()

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)
		chunkIndex := -1
		usage := &schemas.TranscriptionUsage{}
		lastChunkTime := startTime

		var fullTranscriptionText string

		for {

			if ctx.Err() != nil {
				return
			}

			data, readErr := sseReader.ReadDataLine()
			if readErr != nil {

				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					provider.logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, provider.logger, postHookSpanFinalizer)
					return
				}
				break
			}

			jsonData := data

			parseStart := time.Now()
			geminiResponse, err := processGeminiStreamChunk(jsonData)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if err != nil {
				if strings.Contains(err.Error(), "gemini api error") {
					bifrostErr := toGeminiStreamBifrostError(err)
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, provider.logger, postHookSpanFinalizer)
					return
				}
				provider.logger.Warn("Failed to process chunk: %v", err)
				continue
			}

			var deltaText string
			if len(geminiResponse.Candidates) > 0 && geminiResponse.Candidates[0].Content != nil {
				if len(geminiResponse.Candidates[0].Content.Parts) > 0 {
					var sb strings.Builder
					for _, p := range geminiResponse.Candidates[0].Content.Parts {
						if p.Text != "" {
							sb.WriteString(p.Text)
						}
					}
					if sb.Len() > 0 {
						deltaText = sb.String()
						fullTranscriptionText += deltaText
					}
				}
			}

			if len(geminiResponse.Candidates) > 0 && (geminiResponse.Candidates[0].FinishReason != "" || geminiResponse.UsageMetadata != nil) {

				inputTokens, outputTokens, totalTokens := extractGeminiUsageMetadata(geminiResponse)
				usage.InputTokens = schemas.Ptr(inputTokens)
				usage.OutputTokens = schemas.Ptr(outputTokens)
				usage.TotalTokens = schemas.Ptr(totalTokens)
			}

			if deltaText != "" {
				chunkIndex++

				response := &schemas.BifrostTranscriptionStreamResponse{
					Type:  schemas.TranscriptionStreamResponseTypeDelta,
					Delta: &deltaText,
					ExtraFields: schemas.BifrostResponseExtraFields{
						ChunkIndex: chunkIndex,
						Latency:    time.Since(lastChunkTime).Milliseconds(),
					},
				}
				lastChunkTime = time.Now()

				if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
					response.ExtraFields.RawResponse = jsonData
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, response, nil), responseChan, postHookSpanFinalizer)
			}
		}
		response := &schemas.BifrostTranscriptionStreamResponse{
			Type: schemas.TranscriptionStreamResponseTypeDone,
			Text: fullTranscriptionText,
			Usage: &schemas.TranscriptionUsage{
				Type:         "tokens",
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				TotalTokens:  usage.TotalTokens,
			},
			ExtraFields: schemas.BifrostResponseExtraFields{
				ChunkIndex: chunkIndex + 1,
				Latency:    time.Since(startTime).Milliseconds(),
			},
		}

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
		}
		ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
		providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, response, nil), responseChan, postHookSpanFinalizer)

	}()

	return responseChan, nil
}

func (provider *GeminiProvider) ImageGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ImageGenerationRequest); err != nil {
		return nil, err
	}

	if schemas.IsImagenModel(request.Model) {
		return provider.handleImagenImageGeneration(ctx, key, request)
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiImageGenerationRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response, bifrostErr := geminiResponse.ToBifrostImageGenerationResponse()
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if response == nil {
		return nil, providerUtils.NewBifrostOperationError(
			"failed to convert Gemini image generation response",
			fmt.Errorf("ToBifrostImageGenerationResponse returned nil response"),
		)
	}

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) handleImagenImageGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToImagenImageGenerationRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	baseURL := provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":predict")

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(baseURL)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	req.SetBody(jsonData)

	setGeminiAuthHeader(req, key.Value.GetValue())

	activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.client, resp)
	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	body, isLargeResp, decodeErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, provider.logger)
	if decodeErr != nil {
		return nil, decodeErr
	}
	if isLargeResp {
		respOwned = false
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}, nil
	}

	imagenResponse := GeminiImagenResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, body, &imagenResponse, jsonData, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	response := imagenResponse.ToBifrostImageGenerationResponse()
	response.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) ImageGenerationStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageGenerationRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageGenerationStreamRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ImageEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.ImageEditRequest); err != nil {
		return nil, err
	}

	if schemas.IsImagenModel(request.Model) {
		jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
			ctx,
			request,
			func() (providerUtils.RequestBodyWithExtraParams, error) {
				return ToImagenImageEditRequest(request), nil
			})
		if bifrostErr != nil {
			return nil, bifrostErr
		}

		baseURL := provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+request.Model+":predict")
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		imagenRespOwned := true
		defer func() {
			if imagenRespOwned {
				fasthttp.ReleaseResponse(resp)
			}
		}()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(baseURL)
		req.Header.SetMethod(http.MethodPost)
		req.Header.SetContentType("application/json")
		req.SetBody(jsonData)

		setGeminiAuthHeader(req, key.Value.GetValue())

		activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.client, resp)
		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
		defer wait()
		if bifrostErr != nil {
			return nil, bifrostErr
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			providerUtils.MaterializeStreamErrorBody(ctx, resp)
			return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
		}

		body, isLargeResp, decodeErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, provider.logger)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if isLargeResp {
			imagenRespOwned = false
			return &schemas.BifrostImageGenerationResponse{
				ExtraFields: schemas.BifrostResponseExtraFields{
					Latency: latency.Milliseconds(),
				},
			}, nil
		}

		imagenResponse := GeminiImagenResponse{}
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, body, &imagenResponse, jsonData, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
		if bifrostErr != nil {
			return nil, bifrostErr
		}

		response := imagenResponse.ToBifrostImageGenerationResponse()
		response.ExtraFields.Latency = latency.Milliseconds()

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			response.ExtraFields.RawRequest = rawRequest
		}
		if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
			response.ExtraFields.RawResponse = rawResponse
		}

		return response, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiImageEditRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	geminiResponse, rawResponse, latency, providerResponseHeaders, bifrostErr := provider.completeRequest(ctx, request.Model, key, jsonData, ":generateContent")
	if providerResponseHeaders != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)
	}
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if isLargeResp, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); isLargeResp {
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency:                 latency.Milliseconds(),
				ProviderResponseHeaders: providerResponseHeaders,
			},
		}, nil
	}

	response, bifrostErr := geminiResponse.ToBifrostImageGenerationResponse()
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if response == nil {
		return nil, providerUtils.NewBifrostOperationError(
			"failed to convert Gemini image edit response",
			fmt.Errorf("ToBifrostImageGenerationResponse returned nil response"),
		)
	}

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonData)
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *GeminiProvider) ImageEditStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageEditStreamRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ImageVariation(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ImageVariationRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) VideoGeneration(ctx *schemas.BifrostContext, key schemas.Key, bifrostReq *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.VideoGenerationRequest); err != nil {
		return nil, err
	}

	model := bifrostReq.Model

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		bifrostReq,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToGeminiVideoGenerationRequest(bifrostReq)
		},
	)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	jsonData = normalizeRawGenerateContentBody(ctx, jsonData, provider.GetProviderKey(), model)

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/models/"+model+":predictLongRunning"))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	req.SetBody(jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	var operation GenerateVideosOperation
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &operation, jsonData, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	bifrostResp, bifrostErr := ToBifrostVideoGenerationResponse(&operation, model)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	bifrostResp.ID = providerUtils.AddVideoIDProviderSuffix(bifrostResp.ID, provider.GetProviderKey())

	bifrostResp.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		bifrostResp.ExtraFields.RawRequest = rawRequest
	}
	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResp.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResp, nil
}

func geminiVideoOperationPath(operationID string) (string, *schemas.BifrostError) {
	parts := strings.Split(operationID, "/")
	var modelID, operationName string
	switch {
	case len(parts) == 2 && parts[0] == "operations":
		operationName = parts[1]
	case len(parts) == 4 && parts[0] == "models" && parts[2] == "operations":
		modelID = parts[1]
		operationName = parts[3]
	default:
		return "", providerUtils.NewBifrostBadRequestError("invalid video_id: expected a Gemini operation resource name")
	}
	escapedOperation, bifrostErr := providerUtils.EscapeResourceID(operationName, "video_id")
	if bifrostErr != nil {
		return "", bifrostErr
	}
	if modelID == "" {
		return "operations/" + escapedOperation, nil
	}
	escapedModel, bifrostErr := providerUtils.EscapeResourceID(modelID, "video_id")
	if bifrostErr != nil {
		return "", bifrostErr
	}
	return "models/" + escapedModel + "/operations/" + escapedOperation, nil
}

func (provider *GeminiProvider) VideoRetrieve(ctx *schemas.BifrostContext, key schemas.Key, bifrostReq *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.VideoRetrieveRequest); err != nil {
		return nil, err
	}

	operationID := bifrostReq.ID

	operationID = providerUtils.StripVideoIDProviderSuffix(operationID, provider.GetProviderKey())
	operationPath, idErr := geminiVideoOperationPath(operationID)
	if idErr != nil {
		return nil, idErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, "/"+operationPath))
	req.Header.SetMethod(http.MethodGet)
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		respBody := append([]byte(nil), resp.Body()...)
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), nil, respBody, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	var operation GenerateVideosOperation
	_, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(resp.Body(), &operation, nil, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	bifrostResp, bifrostErr := ToBifrostVideoGenerationResponse(&operation, "")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	bifrostResp.ID = providerUtils.AddVideoIDProviderSuffix(bifrostResp.ID, provider.GetProviderKey())

	bifrostResp.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResp.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResp, nil
}

func (provider *GeminiProvider) VideoDownload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.VideoDownloadRequest); err != nil {
		return nil, err
	}
	if request == nil || request.ID == "" {
		return nil, providerUtils.NewBifrostOperationError("video_id is required", nil)
	}

	bifrostVideoRetrieveRequest := &schemas.BifrostVideoRetrieveRequest{
		Provider: request.Provider,
		ID:       request.ID,
	}
	videoResp, bifrostErr := provider.VideoRetrieve(ctx, key, bifrostVideoRetrieveRequest)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if videoResp.Status != schemas.VideoStatusCompleted {
		return nil, providerUtils.NewBifrostOperationError(
			fmt.Sprintf("video not ready, current status: %s", videoResp.Status),
			nil,
		)
	}
	if len(videoResp.Videos) == 0 {
		return nil, providerUtils.NewBifrostOperationError("video URL not available", nil)
	}
	var content []byte
	contentType := "video/mp4"
	var latency time.Duration

	if videoResp.Videos[0].Type == schemas.VideoOutputTypeBase64 && videoResp.Videos[0].Base64Data != nil {

		startTime := time.Now()
		decoded, err := base64.StdEncoding.DecodeString(*videoResp.Videos[0].Base64Data)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to decode base64 video data", err)
		}
		content = decoded
		latency = time.Since(startTime)
		contentType = videoResp.Videos[0].ContentType
	} else if videoResp.Videos[0].Type == schemas.VideoOutputTypeURL && videoResp.Videos[0].URL != nil {

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(*videoResp.Videos[0].URL)
		req.Header.SetMethod(http.MethodGet)
		setGeminiAuthHeader(req, key.Value.GetValue())
		var bifrostErr *schemas.BifrostError
		var wait func()
		latency, bifrostErr, wait = providerUtils.MakeRequestWithContextFollowRedirects(ctx, provider.client, req, resp, 5)
		defer wait()
		if bifrostErr != nil {
			return nil, bifrostErr
		}
		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Error("failed to download video: status %d", resp.StatusCode())
			return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(
				fmt.Sprintf("failed to download video: HTTP %d", resp.StatusCode()),
				nil,
			), latency)
		}
		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
		}
		contentType = string(resp.Header.ContentType())
		content = append([]byte(nil), body...)
	} else {
		return nil, providerUtils.NewBifrostOperationError("invalid video output type", nil)
	}
	bifrostResp := &schemas.BifrostVideoDownloadResponse{
		VideoID:     request.ID,
		Content:     content,
		ContentType: contentType,
	}

	bifrostResp.ExtraFields.Latency = latency.Milliseconds()

	return bifrostResp, nil
}

func (provider *GeminiProvider) VideoDelete(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoDeleteRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) VideoList(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoListRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) VideoEdit(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoEditRequest) (*schemas.BifrostVideoEditResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoEditRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) VideoRemix(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.VideoRemixRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) BatchCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchCreateRequest); err != nil {
		return nil, err
	}

	var model string
	if request.Model != nil {
		_, model = schemas.ParseModelString(*request.Model, schemas.Gemini)
	}

	var jsonData []byte
	if rawBody, ok := providerUtils.CheckAndGetRawRequestBody(ctx, request); ok && len(rawBody) > 0 {
		jsonData = NormalizeRawGenerateContentRequestForCompatibility(ctx, rawBody, provider.GetProviderKey(), model)
	}

	hasFileInput := request.InputFileID != ""
	hasInlineRequests := len(request.Requests) > 0

	if len(jsonData) == 0 && !hasFileInput && !hasInlineRequests {
		return nil, providerUtils.NewBifrostOperationError("either input_file_id or requests must be provided", nil)
	}

	if hasFileInput && hasInlineRequests {
		return nil, providerUtils.NewBifrostOperationError("cannot specify both input_file_id and requests", nil)
	}

	if len(jsonData) == 0 {

		displayName := fmt.Sprintf("bifrost-batch-%d", time.Now().UnixNano())
		if request.DisplayName != nil && *request.DisplayName != "" {
			displayName = *request.DisplayName
		}
		batchReq := &GeminiBatchCreateRequest{
			Batch: GeminiBatchConfig{
				DisplayName: displayName,
			},
		}

		if hasFileInput {

			fileID := request.InputFileID

			if !strings.HasPrefix(fileID, "files/") {
				fileID = "files/" + fileID
			}
			batchReq.Batch.InputConfig = GeminiBatchInputConfig{
				FileName: fileID,
			}
		} else {

			geminiRequests := make([]GeminiBatchRequestItem, len(request.Requests))
			for i, bifrostItem := range request.Requests {
				geminiReq, err := ToGeminiBatchGenerateContentRequest(bifrostItem.Body)
				if err != nil {
					return nil, providerUtils.NewBifrostOperationError("failed to convert batch request to gemini format", err)
				}

				geminiRequests[i] = GeminiBatchRequestItem{
					Request: geminiReq,
				}
				if bifrostItem.CustomID != "" {
					geminiRequests[i].Metadata = &GeminiBatchMetadata{
						Key: bifrostItem.CustomID,
					}
				}
			}

			batchReq.Batch.InputConfig = GeminiBatchInputConfig{
				Requests: &GeminiBatchRequestsWrapper{
					Requests: geminiRequests,
				},
			}
		}

		var err error
		jsonData, err = providerUtils.MarshalSorted(batchReq)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
		}
	}

	if updated, err := providerUtils.DeleteJSONField(jsonData, "fallbacks"); err == nil {
		jsonData = updated
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	if model == "" {
		model = "gemini-2.5-flash"
	}
	url := fmt.Sprintf("%s/models/%s:batchGenerateContent", provider.networkConfig.BaseURL, model)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.SetContentType("application/json")
	req.SetBody(jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	var geminiResp GeminiBatchJobResponse
	rawRequest, rawResponse, bifrostErr2 := providerUtils.HandleProviderResponse(body, &geminiResp, jsonData, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr2 != nil {
		provider.logger.Error("gemini batch create unmarshal error: " + bifrostErr2.Error.Message)
		return nil, providerUtils.SetErrorLatency(bifrostErr2, latency)
	}

	if geminiResp.Metadata == nil {
		return nil, providerUtils.NewBifrostOperationError("gemini batch response missing metadata", nil)
	}

	if geminiResp.Metadata.BatchStats == nil {
		return nil, providerUtils.NewBifrostOperationError("gemini batch response missing batch stats", nil)
	}

	totalRequests := geminiResp.Metadata.BatchStats.RequestCount
	completedCount := 0
	failedCount := 0

	outputFile, inlinedResponses := geminiBatchOutput(&geminiResp)
	if len(inlinedResponses) > 0 {
		for _, inlineResp := range inlinedResponses {
			if inlineResp.Error != nil {
				failedCount++
			} else if inlineResp.Response != nil {
				completedCount++
			}
		}
	} else {
		completedCount = geminiResp.Metadata.BatchStats.RequestCount - geminiResp.Metadata.BatchStats.PendingRequestCount
	}

	status := ToBifrostBatchStatus(geminiResp.Metadata.State)

	if geminiResp.Metadata.State == "" && len(inlinedResponses) > 0 {
		status = schemas.BatchStatusCompleted
		completedCount = len(inlinedResponses) - failedCount
	}

	result := &schemas.BifrostBatchCreateResponse{
		ID:            geminiResp.Metadata.Name,
		Object:        "batch",
		Endpoint:      string(request.Endpoint),
		Status:        status,
		CreatedAt:     parseGeminiTimestamp(geminiResp.Metadata.CreateTime),
		OperationName: &geminiResp.Metadata.Name,
		RequestCounts: schemas.BatchRequestCounts{
			Total:     totalRequests,
			Completed: completedCount,
			Failed:    failedCount,
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:     latency.Milliseconds(),
			RawRequest:  rawRequest,
			RawResponse: rawResponse,
		},
	}

	if hasFileInput {
		result.InputFileID = request.InputFileID
	}

	if outputFile != "" {
		result.OutputFileID = &outputFile
	}

	return result, nil
}

func (provider *GeminiProvider) batchListByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, time.Duration, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	baseURL := fmt.Sprintf("%s/batches", provider.networkConfig.BaseURL)
	values := url.Values{}
	if request.PageSize > 0 {
		values.Set("pageSize", fmt.Sprintf("%d", request.PageSize))
	} else if request.Limit > 0 {
		values.Set("pageSize", fmt.Sprintf("%d", request.Limit))
	}
	if request.PageToken != nil && *request.PageToken != "" {
		values.Set("pageToken", *request.PageToken)
	}
	requestURL := baseURL
	if encodedValues := values.Encode(); encodedValues != "" {
		requestURL += "?" + encodedValues
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.SetContentType("application/json")

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {

		if resp.StatusCode() == fasthttp.StatusNotFound || resp.StatusCode() == fasthttp.StatusMethodNotAllowed {
			provider.logger.Debug("gemini batch list not available, returning empty list")
			return &schemas.BifrostBatchListResponse{
				Object:  "list",
				Data:    []schemas.BifrostBatchRetrieveResponse{},
				HasMore: false,
				ExtraFields: schemas.BifrostResponseExtraFields{
					Latency: latency.Milliseconds(),
				},
			}, latency, nil
		}
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var geminiResp GeminiBatchListResponse
	if err := sonic.Unmarshal(body, &geminiResp); err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	data := make([]schemas.BifrostBatchRetrieveResponse, 0, len(geminiResp.Operations))
	for _, batch := range geminiResp.Operations {
		var state, createTime string
		if batch.Metadata != nil {
			state, createTime = batch.Metadata.State, batch.Metadata.CreateTime
		}
		data = append(data, schemas.BifrostBatchRetrieveResponse{

			ID:            batch.Name,
			Object:        "batch",
			Status:        ToBifrostBatchStatus(state),
			CreatedAt:     parseGeminiTimestamp(createTime),
			OperationName: &batch.Name,
			ExtraFields:   schemas.BifrostResponseExtraFields{},
		})
	}

	hasMore := geminiResp.NextPageToken != ""
	var nextCursor *string
	if hasMore {
		nextCursor = &geminiResp.NextPageToken
	}

	return &schemas.BifrostBatchListResponse{
		Object:     "list",
		Data:       data,
		HasMore:    hasMore,
		NextCursor: nextCursor,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, latency, nil
}

func (provider *GeminiProvider) BatchList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchListRequest); err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for batch list", nil)
	}

	helper, err := providerUtils.NewSerialListHelper(keys, request.PageToken, provider.logger, true)
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

	modifiedRequest := *request
	if nativeCursor != "" {
		modifiedRequest.PageToken = &nativeCursor
	} else {
		modifiedRequest.PageToken = nil
	}

	resp, latency, bifrostErr := provider.batchListByKey(ctx, key, &modifiedRequest)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	nativeNextCursor := ""
	if resp.NextCursor != nil {
		nativeNextCursor = *resp.NextCursor
	}

	nextCursor, hasMore := helper.BuildNextCursor(resp.HasMore, nativeNextCursor)

	result := &schemas.BifrostBatchListResponse{
		Object:  "list",
		Data:    resp.Data,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}
	if nextCursor != "" {
		result.NextCursor = &nextCursor
	}

	return result, nil
}

func geminiResourcePath(resourceID, collection, field string) (string, *schemas.BifrostError) {
	resourceID = strings.TrimPrefix(resourceID, collection+"/")
	escapedID, bifrostErr := providerUtils.EscapeResourceID(resourceID, field)
	if bifrostErr != nil {
		return "", bifrostErr
	}
	return collection + "/" + escapedID, nil
}

func (provider *GeminiProvider) batchRetrieveByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	batchPath, bifrostErr := geminiResourcePath(request.BatchID, "batches", "batch_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, batchPath)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.SetContentType("application/json")

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var geminiResp GeminiBatchJobResponse
	_, rawResponse, bifrostErr2 := providerUtils.HandleProviderResponse(body, &geminiResp, nil, false, providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr2 != nil {
		return nil, bifrostErr2
	}

	if geminiResp.Metadata == nil {
		return nil, providerUtils.NewBifrostOperationError("gemini batch response missing metadata", nil)
	}

	batchStats := geminiResp.Metadata.BatchStats
	if batchStats == nil {
		batchStats = &GeminiBatchStats{}
	}

	var completedCount, failedCount int

	completedCount = batchStats.RequestCount - batchStats.PendingRequestCount
	failedCount = completedCount - batchStats.SuccessfulRequestCount

	isDone := geminiResp.Metadata.State == GeminiBatchStateSucceeded ||
		geminiResp.Metadata.State == GeminiBatchStateFailed ||
		geminiResp.Metadata.State == GeminiBatchStateCancelled ||
		geminiResp.Metadata.State == GeminiBatchStateExpired

	result := &schemas.BifrostBatchRetrieveResponse{
		ID:            geminiResp.Metadata.Name,
		Object:        "batch",
		Status:        ToBifrostBatchStatus(geminiResp.Metadata.State),
		CreatedAt:     parseGeminiTimestamp(geminiResp.Metadata.CreateTime),
		OperationName: &geminiResp.Metadata.Name,
		Done:          &isDone,
		RequestCounts: schemas.BatchRequestCounts{
			Completed: completedCount,
			Total:     batchStats.RequestCount,
			Succeeded: batchStats.SuccessfulRequestCount,
			Pending:   batchStats.PendingRequestCount,
			Failed:    failedCount,
		},
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:     latency.Milliseconds(),
			RawResponse: rawResponse,
		},
	}

	if outputFile, _ := geminiBatchOutput(&geminiResp); outputFile != "" {
		result.OutputFileID = &outputFile
	}

	return result, nil
}

func (provider *GeminiProvider) BatchRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchRetrieveRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for batch retrieve", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.batchRetrieveByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
	}

	return nil, lastError
}

func (provider *GeminiProvider) batchCancelByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	batchPath, bifrostErr := geminiResourcePath(request.BatchID, "batches", "batch_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s:cancel", provider.networkConfig.BaseURL, batchPath)

	provider.logger.Debug("gemini batch cancel url: " + requestURL)
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodPost)
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.SetContentType("application/json")

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {

		if resp.StatusCode() == fasthttp.StatusNotFound || resp.StatusCode() == fasthttp.StatusMethodNotAllowed {

			return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
		}
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	now := time.Now().Unix()
	return &schemas.BifrostBatchCancelResponse{
		ID:           request.BatchID,
		Object:       "batch",
		Status:       schemas.BatchStatusCancelling,
		CancellingAt: &now,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) BatchCancel(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchCancelRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for batch cancel", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.batchCancelByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
		provider.logger.Debug("BatchCancel failed for key %s: %v", key.Name, err.Error)
	}

	return nil, lastError
}

func (provider *GeminiProvider) batchDeleteByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	batchPath, bifrostErr := geminiResourcePath(request.BatchID, "batches", "batch_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, batchPath)

	provider.logger.Debug("gemini batch delete url: " + requestURL)
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodDelete)
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusNoContent {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	return &schemas.BifrostBatchDeleteResponse{
		ID:     request.BatchID,
		Object: "batch",
		Status: schemas.BatchStatusDeleted,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) BatchDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchDeleteRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for batch delete", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.batchDeleteByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
		provider.logger.Debug("BatchDelete failed for key %s: %v", key.Name, err.Error)
	}

	return nil, lastError
}

func processGeminiStreamChunk(jsonData []byte) (*GenerateContentResponse, error) {

	if bytes.Contains(jsonData, []byte(`"error"`)) {
		var errorResp GeminiGenerationError
		if err := sonic.Unmarshal(jsonData, &errorResp); err == nil && errorResp.Error != nil {
			return nil, &GeminiStreamAPIError{Err: errorResp.Error}
		}
		var errorCheck map[string]interface{}
		if err := sonic.Unmarshal(jsonData, &errorCheck); err == nil {
			if errValue, hasError := errorCheck["error"]; hasError {
				return nil, fmt.Errorf("gemini api error: %v", errValue)
			}
		}
	}

	var geminiResponse GenerateContentResponse
	if err := sonic.Unmarshal(jsonData, &geminiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse Gemini stream response: %v", err)
	}

	return &geminiResponse, nil
}

func shouldSkipInlineDataForStreamingContext(ctx *schemas.BifrostContext) bool {
	if ctx == nil {
		return false
	}
	if isLargePayload, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool); ok && isLargePayload {
		return true
	}
	if responseThreshold, ok := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64); ok && responseThreshold > 0 {
		return true
	}
	return false
}

func readNextSSEDataLine(reader *bufio.Reader, skipInlineData bool) ([]byte, error) {
	for {
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}

		trimmed := bytes.TrimSpace(fragment)
		if len(trimmed) == 0 || trimmed[0] == ':' || !bytes.HasPrefix(trimmed, []byte("data:")) {
			for isPrefix {
				_, isPrefix, err = reader.ReadLine()
				if err != nil {
					return nil, err
				}
			}
			continue
		}

		data := bytes.TrimLeft(trimmed[len("data:"):], " \t")
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			for isPrefix {
				_, isPrefix, err = reader.ReadLine()
				if err != nil {
					return nil, err
				}
			}
			continue
		}

		if skipInlineData && bytes.Contains(data, []byte(`"inlineData"`)) {
			for isPrefix {
				_, isPrefix, err = reader.ReadLine()
				if err != nil {
					return nil, err
				}
			}
			continue
		}

		if !isPrefix {
			return append([]byte(nil), data...), nil
		}

		const maxJSONLineBytes = 512 * 1024
		collected := make([]byte, 0, len(data)+1024)
		collected = append(collected, data...)
		dropLine := false

		for isPrefix {
			fragment, isPrefix, err = reader.ReadLine()
			if err != nil {
				return nil, err
			}
			if skipInlineData && bytes.Contains(fragment, []byte(`"inlineData"`)) {
				dropLine = true
				continue
			}
			if dropLine || len(collected)+len(fragment) > maxJSONLineBytes {
				dropLine = true
				continue
			}
			collected = append(collected, fragment...)
		}

		if dropLine {
			continue
		}
		collected = bytes.TrimSpace(collected)
		if len(collected) == 0 || bytes.Equal(collected, []byte("[DONE]")) {
			continue
		}
		return collected, nil
	}
}

func (provider *GeminiProvider) batchResultsByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	batchPath, bifrostErr := geminiResourcePath(request.BatchID, "batches", "batch_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, batchPath)

	provider.logger.Debug("gemini batch results url: " + requestURL)
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.Header.SetContentType("application/json")

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var geminiResp GeminiBatchJobResponse
	if err := sonic.Unmarshal(body, &geminiResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	if geminiResp.Metadata == nil {
		return nil, providerUtils.NewBifrostOperationError("gemini batch response missing metadata", nil)
	}

	if geminiResp.Metadata.State == GeminiBatchStatePending || geminiResp.Metadata.State == GeminiBatchStateRunning {
		return nil, providerUtils.NewBifrostOperationError(
			fmt.Sprintf("batch %s is still processing (state: %s), results not yet available", request.BatchID, geminiResp.Metadata.State),
			nil,
		)
	}

	var results []schemas.BatchResultItem
	var parseErrors []schemas.BatchError

	outputFile, inlinedResponses := geminiBatchOutput(&geminiResp)
	if outputFile != "" {

		provider.logger.Debug("gemini batch results in file: " + outputFile)
		fileResults, fileParseErrors, bifrostErr := provider.downloadBatchResultsFile(ctx, key, outputFile)
		if bifrostErr != nil {
			return nil, bifrostErr
		}
		results = fileResults
		parseErrors = fileParseErrors
	} else if len(inlinedResponses) > 0 {

		results = make([]schemas.BatchResultItem, 0, len(inlinedResponses))
		for i, inlineResp := range inlinedResponses {
			results = append(results, geminiInlineResponseToBatchResultItem(inlineResp, fmt.Sprintf("request-%d", i)))
		}
	}

	if len(results) == 0 && (geminiResp.Metadata.State == GeminiBatchStateSucceeded || geminiResp.Metadata.State == GeminiBatchStateFailed) {
		results = []schemas.BatchResultItem{{
			CustomID: "info",
			Response: &schemas.BatchResultResponse{
				StatusCode: 200,
				Body: map[string]interface{}{
					"message": fmt.Sprintf("Batch completed with state: %s. No results available.", geminiResp.Metadata.State),
				},
			},
		}}
	}

	batchResultsResp := &schemas.BifrostBatchResultsResponse{
		BatchID:  request.BatchID,
		Endpoint: schemas.BatchEndpointChatCompletions,
		Results:  results,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		batchResultsResp.ExtraFields.RawResponse = results
	}
	if len(parseErrors) > 0 {
		batchResultsResp.ExtraFields.ParseErrors = parseErrors
	}

	return batchResultsResp, nil
}

func (provider *GeminiProvider) BatchResults(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.BatchResultsRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for batch results", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.batchResultsByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
		provider.logger.Debug("BatchResults failed for key %s: %v", key.Name, err.Error.Message)
	}

	return nil, lastError
}

func (provider *GeminiProvider) FileUpload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.FileUploadRequest); err != nil {
		return nil, err
	}

	if len(request.File) == 0 {
		return nil, providerUtils.NewBifrostOperationError("file content is required", nil)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	metadataField, err := writer.CreateFormField("metadata")
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to create metadata field", err)
	}
	metadataJSON, err := providerUtils.SetJSONField([]byte(`{}`), "file.displayName", request.Filename)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to marshal metadata", err)
	}
	contentType := ""
	if request.ContentType != nil {
		contentType = strings.TrimSpace(*request.ContentType)
	}
	if contentType != "" {
		metadataJSON, err = providerUtils.SetJSONField(metadataJSON, "file.mimeType", contentType)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to marshal metadata", err)
		}
	}
	if _, err := metadataField.Write(metadataJSON); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to write metadata", err)
	}

	filename := request.Filename
	if filename == "" {
		filename = "file.bin"
	}
	var part io.Writer
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

	baseURL := strings.Replace(provider.networkConfig.BaseURL, "/v1beta", "/upload/v1beta", 1)
	requestURL := fmt.Sprintf("%s/files", baseURL)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType(writer.FormDataContentType())
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.SetBody(buf.Bytes())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusCreated {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var responseWrapper struct {
		File GeminiFileResponse `json:"file"`
	}
	if err := sonic.Unmarshal(body, &responseWrapper); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	geminiResp := responseWrapper.File

	var sizeBytes int64
	fmt.Sscanf(geminiResp.SizeBytes, "%d", &sizeBytes)

	var createdAt int64
	if t, err := time.Parse(time.RFC3339, geminiResp.CreateTime); err == nil {
		createdAt = t.Unix()
	}

	var expiresAt *int64
	if geminiResp.ExpirationTime != "" {
		if t, err := time.Parse(time.RFC3339, geminiResp.ExpirationTime); err == nil {
			exp := t.Unix()
			expiresAt = &exp
		}
	}

	return &schemas.BifrostFileUploadResponse{
		ID:             geminiResp.Name,
		Object:         "file",
		Bytes:          sizeBytes,
		CreatedAt:      createdAt,
		Filename:       geminiResp.DisplayName,
		ContentType:    geminiResp.MimeType,
		Purpose:        request.Purpose,
		Status:         ToBifrostFileStatus(geminiResp.State),
		StorageBackend: schemas.FileStorageAPI,
		StorageURI:     geminiResp.URI,
		ExpiresAt:      expiresAt,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) fileListByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, time.Duration, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	requestURL := fmt.Sprintf("%s/files", provider.networkConfig.BaseURL)
	values := url.Values{}
	if request.Limit > 0 {
		values.Set("pageSize", fmt.Sprintf("%d", request.Limit))
	}
	if request.After != nil && *request.After != "" {
		values.Set("pageToken", *request.After)
	}
	if encodedValues := values.Encode(); encodedValues != "" {
		requestURL += "?" + encodedValues
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var geminiResp GeminiFileListResponse
	if err := sonic.Unmarshal(body, &geminiResp); err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	bifrostResp := &schemas.BifrostFileListResponse{
		Object:  "list",
		Data:    make([]schemas.FileObject, len(geminiResp.Files)),
		HasMore: geminiResp.NextPageToken != "",
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if geminiResp.NextPageToken != "" {
		bifrostResp.After = &geminiResp.NextPageToken
	}

	for i, file := range geminiResp.Files {
		var sizeBytes int64
		fmt.Sscanf(file.SizeBytes, "%d", &sizeBytes)

		var createdAt int64
		if t, err := time.Parse(time.RFC3339, file.CreateTime); err == nil {
			createdAt = t.Unix()
		}
		var updatedAt int64
		if t, err := time.Parse(time.RFC3339, file.UpdateTime); err == nil {
			updatedAt = t.Unix()
		}

		var expiresAt *int64
		if file.ExpirationTime != "" {
			if t, err := time.Parse(time.RFC3339, file.ExpirationTime); err == nil {
				exp := t.Unix()
				expiresAt = &exp
			}
		}

		bifrostResp.Data[i] = schemas.FileObject{
			ID:          file.Name,
			Object:      "file",
			Bytes:       sizeBytes,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Filename:    file.DisplayName,
			ContentType: file.MimeType,
			Purpose:     schemas.FilePurposeVision,
			Status:      ToBifrostFileStatus(file.State),
			ExpiresAt:   expiresAt,
		}
	}

	return bifrostResp, latency, nil
}

func (provider *GeminiProvider) FileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.FileListRequest); err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for file list", nil)
	}

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

	modifiedRequest := *request
	if nativeCursor != "" {
		modifiedRequest.After = &nativeCursor
	} else {
		modifiedRequest.After = nil
	}

	resp, latency, bifrostErr := provider.fileListByKey(ctx, key, &modifiedRequest)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	nativeNextCursor := ""
	if resp.After != nil {
		nativeNextCursor = *resp.After
	}

	nextCursor, hasMore := helper.BuildNextCursor(resp.HasMore, nativeNextCursor)

	result := &schemas.BifrostFileListResponse{
		Object:  "list",
		Data:    resp.Data,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}
	if nextCursor != "" {
		result.After = &nextCursor
	}

	return result, nil
}

func (provider *GeminiProvider) fileRetrieveByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	filePath, bifrostErr := geminiResourcePath(request.FileID, "files", "file_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, filePath)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var geminiResp GeminiFileResponse
	if err := sonic.Unmarshal(body, &geminiResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	var sizeBytes int64
	fmt.Sscanf(geminiResp.SizeBytes, "%d", &sizeBytes)

	var createdAt int64
	if t, err := time.Parse(time.RFC3339, geminiResp.CreateTime); err == nil {
		createdAt = t.Unix()
	}

	var updatedAt int64
	if t, err := time.Parse(time.RFC3339, geminiResp.UpdateTime); err == nil {
		updatedAt = t.Unix()
	}

	var expiresAt *int64
	if geminiResp.ExpirationTime != "" {
		if t, err := time.Parse(time.RFC3339, geminiResp.ExpirationTime); err == nil {
			exp := t.Unix()
			expiresAt = &exp
		}
	}

	return &schemas.BifrostFileRetrieveResponse{
		ID:             geminiResp.Name,
		Object:         "file",
		Bytes:          sizeBytes,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		Filename:       geminiResp.DisplayName,
		ContentType:    geminiResp.MimeType,
		Purpose:        schemas.FilePurposeVision,
		Status:         ToBifrostFileStatus(geminiResp.State),
		StorageBackend: schemas.FileStorageAPI,
		StorageURI:     geminiResp.URI,
		ExpiresAt:      expiresAt,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) FileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.FileRetrieveRequest); err != nil {
		return nil, err
	}

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("file_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for file retrieve", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.fileRetrieveByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
		provider.logger.Debug("FileRetrieve failed for key %s: %v", key.Name, err.Error)
	}

	return nil, lastError
}

func (provider *GeminiProvider) fileDeleteByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	filePath, bifrostErr := geminiResourcePath(request.FileID, "files", "file_id")
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, filePath)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodDelete)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusNoContent {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	return &schemas.BifrostFileDeleteResponse{
		ID:      request.FileID,
		Object:  "file",
		Deleted: true,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) FileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.FileDeleteRequest); err != nil {
		return nil, err
	}

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("file_id is required", nil)
	}

	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for file delete", nil)
	}

	var lastError *schemas.BifrostError
	for _, key := range keys {
		resp, err := provider.fileDeleteByKey(ctx, key, request)
		if err == nil {
			return resp, nil
		}
		lastError = err
		provider.logger.Debug("FileDelete failed for key %s: %v", key.Name, err.Error)
	}

	return nil, lastError
}

func (provider *GeminiProvider) FileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.FileContentRequest); err != nil {
		return nil, err
	}

	return nil, providerUtils.NewUnsupportedOperationError(schemas.FileContentRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) CountTokens(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CountTokensRequest); err != nil {
		return nil, err
	}

	isLargePayload := false
	if v, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool); ok && v {
		isLargePayload = true
	}

	if strings.TrimSpace(request.Model) == "" {
		return nil, providerUtils.NewBifrostOperationError("model is required for Gemini count tokens request", fmt.Errorf("missing model"))
	}

	_, model := schemas.ParseModelString(request.Model, schemas.Gemini)

	var (
		jsonData   []byte
		bifrostErr *schemas.BifrostError
	)
	if !isLargePayload {

		jsonData, bifrostErr = providerUtils.CheckContextAndGetRequestBody(
			ctx,
			request,
			func() (providerUtils.RequestBodyWithExtraParams, error) {
				return ToGeminiResponsesRequest(ctx, request)
			},
		)
		if bifrostErr != nil {
			return nil, bifrostErr
		}

		jsonData = normalizeRawGenerateContentBody(ctx, jsonData, provider.GetProviderKey(), model)
		jsonData = wrapGeminiCountTokensBody(jsonData, model)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	path := fmt.Sprintf("/models/%s:countTokens", model)
	req.SetRequestURI(provider.networkConfig.BaseURL + providerUtils.GetPathFromContext(ctx, path))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())
	usedLargePayloadBody := providerUtils.ApplyLargePayloadRequestBody(ctx, req)
	if !usedLargePayloadBody {
		req.SetBody(jsonData)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	if usedLargePayloadBody {
		providerUtils.DrainLargePayloadRemainder(ctx)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.EnrichError(ctx, parseGeminiError(resp), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	responseBody := append([]byte(nil), body...)

	geminiResponse := &GeminiCountTokensResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(
		responseBody,
		geminiResponse,
		jsonData,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
	)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, responseBody, provider.sendBackRawRequest, provider.sendBackRawResponse, latency)
	}

	response := geminiResponse.ToBifrostCountTokensResponse(request.Model)

	response.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	return response, nil
}

func (provider *GeminiProvider) Compaction(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCompactionRequest) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.CompactionRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerCreateRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerListRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerRetrieveRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerDeleteRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerFileCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileCreateRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerFileList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileListRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerFileRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileRetrieveRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerFileContent(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileContentRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) ContainerFileDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.ContainerFileDeleteRequest, provider.GetProviderKey())
}

func (provider *GeminiProvider) Passthrough(
	ctx *schemas.BifrostContext,
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.PassthroughRequest); err != nil {
		return nil, err
	}
	url, err := providerUtils.BuildPassthroughURL(provider.networkConfig.BaseURL, req.Path, req.RawQuery)
	if err != nil {
		return nil, providerUtils.NewBifrostBadRequestError(err.Error())
	}

	url = strings.Replace(url, "v1beta/upload/v1beta", "upload/v1beta", 1)

	fasthttpReq := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	defer fasthttp.ReleaseRequest(fasthttpReq)

	fasthttpReq.Header.SetMethod(req.Method)
	fasthttpReq.SetRequestURI(url)

	providerUtils.SetExtraHeaders(ctx, fasthttpReq, provider.networkConfig.ExtraHeaders, nil)

	for k, v := range req.SafeHeaders {
		fasthttpReq.Header.Set(k, v)
	}

	setGeminiAuthHeader(fasthttpReq, key.Value.GetValue())

	fasthttpReq.SetBody(req.Body)

	var latency time.Duration
	var bifrostErr *schemas.BifrostError
	var wait func()

	if req.Method == http.MethodGet && strings.Contains(req.Path, ":download") {
		latency, bifrostErr, wait = providerUtils.MakeRequestWithContextFollowRedirects(ctx, provider.client, fasthttpReq, resp, 5)
	} else {
		latency, bifrostErr, wait = providerUtils.MakeRequestWithContext(ctx, provider.client, fasthttpReq, resp)
	}
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
		passthroughUsage = ExtractGeminiPassthroughUsage(req.Path, req.Body, body)
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

func (provider *GeminiProvider) PassthroughStream(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.PassthroughStreamRequest); err != nil {
		return nil, err
	}

	url, err := providerUtils.BuildPassthroughURL(provider.networkConfig.BaseURL, req.Path, req.RawQuery)
	if err != nil {
		return nil, providerUtils.NewBifrostBadRequestError(err.Error())
	}

	url = strings.Replace(url, "v1beta/upload/v1beta", "upload/v1beta", 1)

	startTime := time.Now()

	fasthttpReq := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(fasthttpReq)

	fasthttpReq.Header.SetMethod(req.Method)
	fasthttpReq.SetRequestURI(url)

	providerUtils.SetExtraHeaders(ctx, fasthttpReq, provider.networkConfig.ExtraHeaders, nil)
	for k, v := range req.SafeHeaders {
		fasthttpReq.Header.Set(k, v)
	}

	setGeminiAuthHeader(fasthttpReq, key.Value.GetValue())

	fasthttpReq.Header.Set("Connection", "close")

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
	return providerUtils.StreamPassthrough(
		ctx, postHookRunner, postHookSpanFinalizer, resp, bodyStream,
		providerUtils.PassthroughStreamParams{
			StatusCode:          resp.StatusCode(),
			Headers:             headers,
			Path:                req.Path,
			RawRequest:          req.Body,
			CancellationBody:    providerUtils.PassthroughJSONBody(fasthttpReq, req.Body),
			StartTime:           startTime,
			UseTerminalDetector: true,
			Logger:              provider.logger,
			HasUsage:            HasGeminiPassthroughUsage,
			Observe: func(event []byte) *schemas.BifrostPassthroughUsage {
				return ExtractGeminiPassthroughUsage(req.Path, req.Body, event)
			},
		},
	), nil
}
