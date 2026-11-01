package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/providers/utils/tokencache"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type OpenAIProvider struct {
	logger               schemas.Logger
	client               *fasthttp.Client
	streamingClient      *fasthttp.Client
	networkConfig        schemas.NetworkConfig
	sendBackRawRequest   bool
	sendBackRawResponse  bool
	customProviderConfig *schemas.CustomProviderConfig
	disableStore         bool
	authHTTPClient       *http.Client
	tokens               *tokencache.Cache[string]
}
type VideoDownloadHandler func(ctx *schemas.BifrostContext, key schemas.Key, req *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError)
type openAIDiarizedTranscriptionResponse struct {
	Duration *float64                               `json:"duration,omitempty"`
	Segments []schemas.TranscriptionDiarizedSegment `json:"segments"`
	Task     *string                                `json:"task,omitempty"`
	Text     string                                 `json:"text"`
	Usage    *schemas.TranscriptionUsage            `json:"usage,omitempty"`
}

func NewOpenAIProvider(config *schemas.ProviderConfig, logger schemas.Logger) *OpenAIProvider {
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
		config.NetworkConfig.BaseURL = "https://api.openai.com"
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")

	return &OpenAIProvider{
		logger:               logger,
		client:               client,
		streamingClient:      streamingClient,
		networkConfig:        config.NetworkConfig,
		sendBackRawRequest:   config.SendBackRawRequest,
		sendBackRawResponse:  config.SendBackRawResponse,
		customProviderConfig: config.CustomProviderConfig,
		disableStore:         config.OpenAIConfig != nil && config.OpenAIConfig.DisableStore,
		authHTTPClient:       providerUtils.NewProviderHTTPClient(config.ProxyConfig, config.NetworkConfig, logger),
		tokens:               tokencache.New[string](tokencache.Options{}),
	}
}

func (provider *OpenAIProvider) resolveKey(ctx context.Context, key schemas.Key) (schemas.Key, *schemas.BifrostError) {

	if key.OAuthKeyConfig == nil || strings.TrimSpace(key.Value.GetValue()) != "" {
		return key, nil
	}
	headers, bErr := tokencache.ResolveBearer(ctx, provider.tokens, "", key.OAuthKeyConfig, provider.authHTTPClient)
	if bErr != nil {
		return key, bErr
	}
	key.Value = *schemas.NewSecretVar(strings.TrimPrefix(headers["Authorization"], "Bearer "))
	return key, nil
}

func (provider *OpenAIProvider) GetProviderKey() schemas.ModelProvider {
	return providerUtils.GetProviderName(schemas.OpenAI, provider.customProviderConfig)
}

func (provider *OpenAIProvider) buildRequestURL(ctx *schemas.BifrostContext, defaultPath string, requestType schemas.RequestType) string {
	path, isCompleteURL := providerUtils.GetRequestPath(ctx, defaultPath, provider.customProviderConfig, requestType)
	if isCompleteURL {
		return path
	}
	return provider.networkConfig.BaseURL + path
}

func (provider *OpenAIProvider) ListModels(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ListModelsRequest); err != nil {
		return nil, err
	}
	providerName := provider.GetProviderKey()

	if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
		return providerUtils.HandleKeylessListModelsRequest(providerName, func() (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
			return ListModelsByKey(
				ctx,
				provider.client,
				provider.buildRequestURL(ctx, "/v1/models", schemas.ListModelsRequest),
				schemas.Key{Models: schemas.WhiteList{"*"}},
				request.Unfiltered,
				provider.networkConfig.ExtraHeaders,
				providerName,
				providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
				providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
			)
		})
	}

	url := provider.buildRequestURL(ctx, "/v1/models", schemas.ListModelsRequest)
	sendRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	if len(keys) == 0 {
		return ListModelsByKey(ctx, provider.client, url, schemas.Key{}, request.Unfiltered, provider.networkConfig.ExtraHeaders, providerName, sendRawRequest, sendRawResponse)
	}

	return providerUtils.HandleMultipleListModelsRequests(ctx, keys, request,
		func(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
			key, keyErr := provider.resolveKey(ctx, key)
			if keyErr != nil {
				return nil, keyErr
			}
			return ListModelsByKey(ctx, provider.client, url, key, request.Unfiltered, provider.networkConfig.ExtraHeaders, providerName, sendRawRequest, sendRawResponse)
		})
}

func ListModelsByKey(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	key schemas.Key,
	unfiltered bool,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		bifrostErr := ParseOpenAIError(resp)
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	responseBody := append([]byte(nil), resp.Body()...)

	openaiResponse := &OpenAIListModelsResponse{}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, openaiResponse, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	response := openaiResponse.ToBifrostListModelsResponse(providerName, key.Models, key.BlacklistedModels, key.Aliases, unfiltered)

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func BearerAuthHeader(key schemas.Key) map[string]string {
	headers := map[string]string{}
	if key.Value.GetValue() != "" {
		headers["Authorization"] = "Bearer " + key.Value.GetValue()
	}
	return headers
}

func HandleOpenAIListModelsRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	request *schemas.BifrostListModelsRequest,
	url string,
	keys []schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	if len(keys) == 0 {
		return ListModelsByKey(ctx, client, url, schemas.Key{}, request.Unfiltered, extraHeaders, providerName, sendBackRawRequest, sendBackRawResponse)
	}
	listModelsByKeyWrapper := func(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
		return ListModelsByKey(ctx, client, url, key, request.Unfiltered, extraHeaders, providerName, sendBackRawRequest, sendBackRawResponse)
	}
	return providerUtils.HandleMultipleListModelsRequests(
		ctx,
		keys,
		request,
		listModelsByKeyWrapper,
	)
}

func (provider *OpenAIProvider) ModelRetrieve(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostModelRetrieveRequest) (*schemas.BifrostModelRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ModelRetrieveRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}
	if request == nil || request.Model == "" {
		return nil, providerUtils.NewBifrostOperationError("model is required", nil)
	}
	escapedModel, idErr := providerUtils.EscapeResourceID(request.Model, "model")
	if idErr != nil {
		return nil, idErr
	}

	return HandleOpenAIModelRetrieveRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/models/"+escapedModel, schemas.ModelRetrieveRequest),
		key,
		provider.networkConfig.ExtraHeaders,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
	)
}

func HandleOpenAIModelRetrieveRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
) (*schemas.BifrostModelRetrieveResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody := append([]byte(nil), resp.Body()...)

	openaiModel := &OpenAIModel{}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, openaiModel, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	response := openaiModel.ToBifrostModelRetrieveResponse(providerName)

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

func (provider *OpenAIProvider) TextCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.TextCompletionRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}
	return HandleOpenAITextCompletionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/completions", schemas.TextCompletionRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		nil,
		nil,
		provider.logger,
	)
}

func HandleOpenAITextCompletionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostTextCompletionRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	customResponseHandler responseHandler[schemas.BifrostTextCompletionResponse],
	customErrorConverter ErrorConverter,
	logger schemas.Logger,
) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeader, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostTextCompletionResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostTextCompletionResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAITextCompletionRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostTextCompletionResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostTextCompletionResponse{}

	var rawRequest, rawResponse interface{}

	if customResponseHandler != nil {

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		rawRequest, rawResponse, bifrostErr = customResponseHandler(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
		if pt != nil {
			if bifrostErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
	} else {
		rawRequest, rawResponse, bifrostErr = providerUtils.HandleProviderResponseCtx(ctx, body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	}

	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) TextCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.TextCompletionStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}
	return HandleOpenAITextCompletionStreaming(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/completions", schemas.TextCompletionStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		nil,
		postHookRunner,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAITextCompletionStreaming(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostTextCompletionRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	customErrorConverter ErrorConverter,
	postHookRunner schemas.PostHookRunner,
	customResponseHandler responseHandler[schemas.BifrostTextCompletionResponse],
	postResponseConverter func(*schemas.BifrostTextCompletionResponse) *schemas.BifrostTextCompletionResponse,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {
		maps.Copy(headers, authHeader)
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody := ToOpenAITextCompletionRequest(request)
			if reqBody != nil {
				reqBody.Stream = schemas.Ptr(true)
				reqBody.StreamOptions = &schemas.ChatStreamOptions{
					IncludeUsage: schemas.Ptr(true),
				}
			}
			return reqBody, nil
		})

	if bifrostErr != nil {
		return nil, bifrostErr
	}

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

	setStreamingRequestBody(ctx, req, jsonBody, providerName)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		latency := time.Since(startTime)
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
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		latency := time.Since(startTime)
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	latency := time.Since(startTime)

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

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)

		chunkIndex := -1
		usage := &schemas.BifrostLLMUsage{}

		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, usage)

		trailingUsageSeen := false

		var finishReason *string
		var messageID string
		var created int
		modelName := request.Model
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

				if errors.Is(readErr, providerUtils.ErrStreamIdleTimeout) && finishReason != nil {
					ctx.SetValue(schemas.BifrostContextKeyStreamParkedAfterFinish, true)
					if usage.TotalTokens == 0 {
						logger.Warn("provider %s went silent after finish_reason without sending usage; token counts and cost are unavailable for this request", providerName)
					}
					break
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
					return
				}
				if providerUtils.SSEEndedOnComment(sseReader) {

					ctx.SetValue(schemas.BifrostContextKeyStreamParkedAfterFinish, true)

					if usage.TotalTokens == 0 {
						logger.Warn("provider %s ended the stream on heartbeats after finish_reason without sending usage; token counts and cost are unavailable for this request", providerName)
					}
				} else {

					ctx.SetValue(schemas.BifrostContextKeyStreamBodyExhausted, true)
				}
				break
			}
			jsonData := string(data)
			var response schemas.BifrostTextCompletionResponse
			if customResponseHandler != nil {

				parseStart := time.Now()
				rawRequest, rawResponse, handlerErr := customResponseHandler([]byte(jsonData), &response, nil, sendBackRawRequest, sendBackRawResponse)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if handlerErr != nil {

					if sendBackRawRequest {
						handlerErr.ExtraFields.RawRequest = rawRequest
					}
					if sendBackRawResponse {
						handlerErr.ExtraFields.RawResponse = rawResponse
					}
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, handlerErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
					return
				}
			} else {

				if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

					var bifrostErr schemas.BifrostError
					if err := sonic.UnmarshalString(jsonData, &bifrostErr); err == nil {
						if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
							applyStreamErrorStatus(&bifrostErr)
							ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
							providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
							return
						}
					}
				}

				parseStart := time.Now()
				umErr := sonic.UnmarshalString(jsonData, &response)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if umErr != nil {
					logger.Warn("Failed to parse stream response: %v", umErr)
					continue
				}
			}

			if response.Choices == nil {
				response.Choices = []schemas.BifrostResponseChoice{}
			}

			if postResponseConverter != nil {

				convStart := time.Now()
				converted := postResponseConverter(&response)
				schemas.AddStreamConvert(ctx, time.Since(convStart))
				if converted != nil {
					response = *converted
				} else {
					logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				}
			}

			if response.Model != "" {
				modelName = response.Model
			}

			finishInThisFrame := len(response.Choices) > 0 &&
				response.Choices[0].FinishReason != nil &&
				*response.Choices[0].FinishReason != ""

			if response.Usage != nil {

				if response.Usage.PromptTokens > usage.PromptTokens {
					usage.PromptTokens = response.Usage.PromptTokens
				}
				if response.Usage.CompletionTokens > usage.CompletionTokens {
					usage.CompletionTokens = response.Usage.CompletionTokens
				}
				if response.Usage.TotalTokens > usage.TotalTokens {
					usage.TotalTokens = response.Usage.TotalTokens
				}
				calculatedTotal := usage.PromptTokens + usage.CompletionTokens
				if calculatedTotal > usage.TotalTokens {
					usage.TotalTokens = calculatedTotal
				}
				if response.Usage.CompletionTokensDetails != nil {
					usage.CompletionTokensDetails = response.Usage.CompletionTokensDetails
				}
				if response.Usage.PromptTokensDetails != nil {
					usage.PromptTokensDetails = response.Usage.PromptTokensDetails
				}
				if finishReason != nil || finishInThisFrame {
					trailingUsageSeen = true
				}
				response.Usage = nil
			}

			if len(response.Choices) == 0 {
				if !providerUtils.ProviderSendsDoneMarker(ctx, providerName) && finishReason != nil &&
					providerUtils.WaitForStreamUsage(ctx) && trailingUsageSeen {
					break
				}
				continue
			}

			choice := response.Choices[0]
			if choice.FinishReason != nil && *choice.FinishReason != "" {

				finishReason = choice.FinishReason
				response.Choices[0].FinishReason = nil

				providerUtils.SSEEndOnCommentAfterFinish(sseReader)
			}

			if response.ID != "" && messageID == "" {
				messageID = response.ID
			}
			if response.Created != 0 && created == 0 {
				created = response.Created
			}

			if choice.TextCompletionResponseChoice != nil && choice.TextCompletionResponseChoice.Text != nil {
				chunkIndex++

				response.ExtraFields.ChunkIndex = chunkIndex
				response.ExtraFields.Latency = time.Since(lastChunkTime).Milliseconds()
				lastChunkTime = time.Now()

				if sendBackRawResponse {
					response.ExtraFields.RawResponse = jsonData
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(&response, nil, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
			}

			if !providerUtils.ProviderSendsDoneMarker(ctx, providerName) && finishReason != nil &&
				(!providerUtils.WaitForStreamUsage(ctx) || trailingUsageSeen) {
				break
			}
		}

		if !providerUtils.SSEStreamEndedOnMarker(sseReader) && finishReason == nil {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			return
		}

		response := providerUtils.CreateBifrostTextCompletionChunkResponse(messageID, usage, finishReason, chunkIndex, schemas.TextCompletionStreamRequest, modelName, created)
		if postResponseConverter != nil {
			response = postResponseConverter(response)
			if response == nil {
				logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				return
			}
		}

		if sendBackRawRequest {
			providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
		}
		response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
		ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
		providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(response, nil, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) ChatCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ChatCompletionRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	if provider.disableStore {
		ctx.SetValue(schemas.BifrostContextKeyDisableStore, true)
	}

	return HandleOpenAIChatCompletionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/chat/completions", schemas.ChatCompletionRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		nil,
		nil,
		nil,
		provider.logger,
	)
}

func timedSetBody(ctx *schemas.BifrostContext, req *fasthttp.Request, body []byte) {
	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	req.SetBody(body)
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}
}

func timedExtractResponseHeaders(ctx *schemas.BifrostContext, resp *fasthttp.Response) map[string]string {
	ft, fh := providerUtils.StartPhaseSpan(ctx, "response-finalize")
	headers := providerUtils.ExtractProviderResponseHeaders(resp)
	if ft != nil {
		ft.EndSpan(fh, schemas.SpanStatusOk, "")
	}
	return headers
}

func HandleOpenAIChatCompletionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostChatRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	customResponseHandler responseHandler[schemas.BifrostChatResponse],
	customErrorConverter ErrorConverter,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) (*schemas.BifrostChatResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeader, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostChatResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostChatResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody := ToOpenAIChatRequest(ctx, request)

			if err := ResolveChatFileURLs(ctx, request.Provider, reqBody); err != nil {
				return nil, err
			}
			return reqBody, nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if signer != nil {
		sigHeaders, bErr := signer(jsonData)
		if bErr != nil {
			return nil, bErr
		}
		for k, v := range sigHeaders {
			req.Header.Set(k, v)
		}
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostChatResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}
	response := &schemas.BifrostChatResponse{}
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	var rawRequest, rawResponse interface{}

	if customResponseHandler != nil {

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		rawRequest, rawResponse, bifrostErr = customResponseHandler(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
		if pt != nil {
			if bifrostErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
	} else {
		rawRequest, rawResponse, bifrostErr = providerUtils.HandleProviderResponseCtx(ctx, body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	}

	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}
	response.Usage.NormalizeReasoningTokens()

	var inBandErr *schemas.BifrostError
	if customResponseHandler != nil {
		inBandErr = ErrorInSuccessfulChatBody(body)
	} else {
		inBandErr = errorInValidatedChatBody(body)
	}
	if inBandErr != nil {
		logger.Debug("in-band error on a 200 from %s provider: %s", providerName, inBandErr.Error.Message)
		return nil, providerUtils.EnrichError(ctx, inBandErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response.ExtraFields.Latency = latency.Milliseconds()

	if providerUtils.ShouldSendBackRawRequest(ctx, sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) ChatCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ChatCompletionStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}
	if provider.disableStore {
		ctx.SetValue(schemas.BifrostContextKeyDisableStore, true)
	}

	return HandleOpenAIChatCompletionStreaming(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/chat/completions", schemas.ChatCompletionStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAIChatCompletionStreaming(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostChatRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	customRequestConverter func(*schemas.BifrostChatRequest) (providerUtils.RequestBodyWithExtraParams, error),
	customResponseHandler responseHandler[schemas.BifrostChatResponse],
	customErrorConverter ErrorConverter,
	postRequestConverter func(*OpenAIChatRequest) *OpenAIChatRequest,
	postResponseConverter func(*schemas.BifrostChatResponse) *schemas.BifrostChatResponse,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	isResponsesToChatCompletionsFallback := false
	var responsesStreamState *schemas.ChatToResponsesStreamState
	if ctx.Value(schemas.BifrostContextKeyIsResponsesToChatCompletionFallback) != nil {
		isResponsesToChatCompletionsFallbackValue, ok := ctx.Value(schemas.BifrostContextKeyIsResponsesToChatCompletionFallback).(bool)
		if ok && isResponsesToChatCompletionsFallbackValue {
			isResponsesToChatCompletionsFallback = true
			responsesStreamState = schemas.AcquireChatToResponsesStreamState()
		}
	}

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {

		maps.Copy(headers, authHeader)
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			if customRequestConverter != nil {
				return customRequestConverter(request)
			}
			reqBody := ToOpenAIChatRequest(ctx, request)

			if err := ResolveChatFileURLs(ctx, request.Provider, reqBody); err != nil {
				return nil, err
			}
			if reqBody != nil {
				reqBody.Stream = new(true)
				reqBody.StreamOptions = &schemas.ChatStreamOptions{
					IncludeUsage: new(true),
				}
				if postRequestConverter != nil {
					reqBody = postRequestConverter(reqBody)
				}
			}
			return reqBody, nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

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

	setStreamingRequestBody(ctx, req, jsonBody, providerName)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
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
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
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

			schemas.ReleaseChatToResponsesStreamState(responsesStreamState)
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)

		chunkIndex := -1
		usage := &schemas.BifrostLLMUsage{}

		ctx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage, usage)

		lastChunkTime := startTime

		var finishReason *string
		var messageID string
		var modelName string
		var created int

		var serviceTier *schemas.BifrostServiceTier
		forwardedTerminalFinishReason := false

		var pendingRawFrames []string
		pendingRawBytes := 0
		rawCaptureTruncated := false
		const maxPendingRawBytes = 1 << 20
		queueRawFrame := func(frame string) {
			if pendingRawBytes+len(frame) > maxPendingRawBytes {
				if !rawCaptureTruncated {
					rawCaptureTruncated = true
					logger.Warn("provider %s streamed over %d bytes of frames that produce no client chunk; raw response capture is truncated for this request", providerName, maxPendingRawBytes)
				}
				return
			}
			pendingRawFrames = append(pendingRawFrames, frame)
			pendingRawBytes += len(frame)
		}
		drainPendingRawFrames := func() string {
			joined := strings.Join(pendingRawFrames, "\n\n")
			pendingRawFrames = pendingRawFrames[:0]
			pendingRawBytes = 0
			return joined
		}

		var pendingFinalEvent *schemas.BifrostResponsesStreamResponse
		usageSeen := false

		trailingUsageSeen := false

		fallbackFinishReasonSeen := false

		for {

			if ctx.Err() != nil {
				return
			}
			data, readErr := sseReader.ReadDataLine()
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}

				if errors.Is(readErr, providerUtils.ErrStreamIdleTimeout) && (finishReason != nil || pendingFinalEvent != nil) {
					ctx.SetValue(schemas.BifrostContextKeyStreamParkedAfterFinish, true)
					if usage.TotalTokens == 0 {
						logger.Warn("provider %s went silent after finish_reason without sending usage; token counts and cost are unavailable for this request", providerName)
					}
					break
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
					return
				}
				if providerUtils.SSEEndedOnComment(sseReader) {

					ctx.SetValue(schemas.BifrostContextKeyStreamParkedAfterFinish, true)

					if usage.TotalTokens == 0 {
						logger.Warn("provider %s ended the stream on heartbeats after finish_reason without sending usage; token counts and cost are unavailable for this request", providerName)
					}
				} else {

					ctx.SetValue(schemas.BifrostContextKeyStreamBodyExhausted, true)
				}
				break
			}
			jsonData := string(data)

			if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

				var bifrostErr schemas.BifrostError
				if err := sonic.UnmarshalString(jsonData, &bifrostErr); err == nil {
					if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
						applyStreamErrorStatus(&bifrostErr)
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
						return
					}
				}
			}

			var response schemas.BifrostChatResponse
			if customResponseHandler != nil {

				parseStart := time.Now()
				rawRequest, rawResponse, handlerErr := customResponseHandler([]byte(jsonData), &response, nil, sendBackRawRequest, sendBackRawResponse)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if handlerErr != nil {
					if sendBackRawRequest {
						handlerErr.ExtraFields.RawRequest = rawRequest
					}
					if sendBackRawResponse {
						handlerErr.ExtraFields.RawResponse = rawResponse
					}
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, handlerErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
					return
				}
			} else {

				parseStart := time.Now()
				umErr := sonic.UnmarshalString(jsonData, &response)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if umErr != nil {
					logger.Warn("Failed to parse stream response: %v", umErr)
					continue
				}
			}

			if response.Choices == nil {
				response.Choices = []schemas.BifrostResponseChoice{}
			}
			response.Usage.NormalizeReasoningTokens()

			for i := range response.Choices {
				if response.Choices[i].ChatStreamResponseChoice != nil {
					response.Choices[i].ChatNonStreamResponseChoice = nil
				}
			}

			if sendBackRawResponse {
				queueRawFrame(jsonData)
			}

			if isResponsesToChatCompletionsFallback {
				if len(response.Choices) > 0 && response.Choices[0].FinishReason != nil && *response.Choices[0].FinishReason != "" {
					fallbackFinishReasonSeen = true
				}

				if response.Usage != nil {
					usageSeen = true
					if response.Usage.PromptTokens > usage.PromptTokens {
						usage.PromptTokens = response.Usage.PromptTokens
					}
					if response.Usage.CompletionTokens > usage.CompletionTokens {
						usage.CompletionTokens = response.Usage.CompletionTokens
					}
					if response.Usage.TotalTokens > usage.TotalTokens {
						usage.TotalTokens = response.Usage.TotalTokens
					}
					if calculatedTotal := usage.PromptTokens + usage.CompletionTokens; calculatedTotal > usage.TotalTokens {
						usage.TotalTokens = calculatedTotal
					}
					if response.Usage.PromptTokensDetails != nil {
						usage.PromptTokensDetails = response.Usage.PromptTokensDetails
					}
					if response.Usage.CompletionTokensDetails != nil {
						usage.CompletionTokensDetails = response.Usage.CompletionTokensDetails
					}
					if response.Usage.Cost != nil {
						usage.Cost = response.Usage.Cost
					}
				}

				convStart := time.Now()
				spreadResponses := response.ToBifrostResponsesStreamResponse(responsesStreamState)
				schemas.AddStreamConvert(ctx, time.Since(convStart))
				for i, response := range spreadResponses {
					if response.Type == schemas.ResponsesStreamResponseTypeError {
						bifrostErr := &schemas.BifrostError{
							Type:           schemas.Ptr(string(schemas.ResponsesStreamResponseTypeError)),
							IsBifrostError: false,
							Error:          &schemas.ErrorField{},
						}

						if response.Message != nil {
							bifrostErr.Error.Message = *response.Message
						}
						if response.Param != nil {
							bifrostErr.Error.Param = *response.Param
						}
						if response.Code != nil {
							bifrostErr.Error.Code = response.Code
						}

						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
						return
					}

					response.ExtraFields.ChunkIndex = response.SequenceNumber

					response.ExtraFields.ConvertedRequestType = schemas.ChatCompletionRequest

					if sendBackRawResponse && i == 0 {
						response.ExtraFields.RawResponse = drainPendingRawFrames()
					}

					if response.Type == schemas.ResponsesStreamResponseTypeCompleted || response.Type == schemas.ResponsesStreamResponseTypeIncomplete {

						pendingFinalEvent = response
						continue
					}

					response.ExtraFields.Latency = time.Since(lastChunkTime).Milliseconds()
					lastChunkTime = time.Now()

					providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, response, nil, nil, nil), responseChan, postHookSpanFinalizer)
				}

				if fallbackFinishReasonSeen && usageSeen &&
					(providerName == schemas.BedrockMantle || providerName == schemas.Bedrock) {
					break
				}
			} else {
				if postResponseConverter != nil {

					convStart := time.Now()
					converted := postResponseConverter(&response)
					schemas.AddStreamConvert(ctx, time.Since(convStart))
					if converted != nil {
						response = *converted
					} else {
						logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
					}
				}

				if response.ServiceTier != nil {
					serviceTier = response.ServiceTier
				}

				finishInThisFrame := len(response.Choices) > 0 &&
					response.Choices[0].FinishReason != nil &&
					*response.Choices[0].FinishReason != ""

				if response.Usage != nil {

					if response.Usage.PromptTokens > usage.PromptTokens {
						usage.PromptTokens = response.Usage.PromptTokens
					}
					if response.Usage.CompletionTokens > usage.CompletionTokens {
						usage.CompletionTokens = response.Usage.CompletionTokens
					}
					if response.Usage.TotalTokens > usage.TotalTokens {
						usage.TotalTokens = response.Usage.TotalTokens
					}
					calculatedTotal := usage.PromptTokens + usage.CompletionTokens
					if calculatedTotal > usage.TotalTokens {
						usage.TotalTokens = calculatedTotal
					}
					if response.Usage.PromptTokensDetails != nil {
						usage.PromptTokensDetails = response.Usage.PromptTokensDetails
					}
					if response.Usage.CompletionTokensDetails != nil {
						usage.CompletionTokensDetails = response.Usage.CompletionTokensDetails
					}
					if response.Usage.Cost != nil {
						usage.Cost = response.Usage.Cost
					}

					if finishReason != nil || finishInThisFrame {
						trailingUsageSeen = true
					}
					response.Usage = nil
				}

				if response.Model != "" {
					modelName = response.Model
				}

				if len(response.Choices) == 0 {
					if !providerUtils.ProviderSendsDoneMarker(ctx, providerName) && finishReason != nil &&
						providerUtils.WaitForStreamUsage(ctx) && trailingUsageSeen {
						break
					}
					continue
				}

				choice := response.Choices[0]
				if choice.FinishReason != nil && *choice.FinishReason != "" {

					finishReason = choice.FinishReason

					providerUtils.SSEEndOnCommentAfterFinish(sseReader)
				}

				if response.ID != "" && messageID == "" {
					messageID = response.ID
				}
				if response.Created != 0 && created == 0 {
					created = response.Created
				}

				if choice.ChatStreamResponseChoice != nil &&
					choice.ChatStreamResponseChoice.Delta != nil &&
					(choice.ChatStreamResponseChoice.Delta.Role != nil ||
						(choice.ChatStreamResponseChoice.Delta.Content != nil && *choice.ChatStreamResponseChoice.Delta.Content != "") ||
						(choice.ChatStreamResponseChoice.Delta.Refusal != nil && *choice.ChatStreamResponseChoice.Delta.Refusal != "") ||
						len(choice.ChatStreamResponseChoice.Delta.Annotations) > 0 ||
						choice.ChatStreamResponseChoice.Delta.Reasoning != nil ||
						len(choice.ChatStreamResponseChoice.Delta.ReasoningDetails) > 0 ||
						choice.ChatStreamResponseChoice.Delta.Audio != nil ||
						len(choice.ChatStreamResponseChoice.Delta.Images) > 0 ||
						len(choice.ChatStreamResponseChoice.Delta.ToolCalls) > 0 ||
						len(choice.ChatStreamResponseChoice.Delta.ExtraContent) > 0) {
					if choice.FinishReason != nil && *choice.FinishReason != "" {
						forwardedTerminalFinishReason = true
					}
					chunkIndex++

					response.ExtraFields.ChunkIndex = chunkIndex
					response.ExtraFields.Latency = time.Since(lastChunkTime).Milliseconds()
					lastChunkTime = time.Now()

					if sendBackRawResponse {
						response.ExtraFields.RawResponse = drainPendingRawFrames()
					}

					providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, &response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
				}

				if !providerUtils.ProviderSendsDoneMarker(ctx, providerName) && finishReason != nil &&
					(!providerUtils.WaitForStreamUsage(ctx) || trailingUsageSeen) {
					break
				}
			}
		}

		terminalSignalSeen := providerUtils.SSEStreamEndedOnMarker(sseReader)
		if isResponsesToChatCompletionsFallback {

			terminalSignalSeen = pendingFinalEvent != nil || (terminalSignalSeen && !fallbackFinishReasonSeen)
		} else {
			terminalSignalSeen = terminalSignalSeen || finishReason != nil
		}
		if !terminalSignalSeen {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
			return
		}

		if isResponsesToChatCompletionsFallback {
			if pendingFinalEvent != nil {
				if usageSeen && pendingFinalEvent.Response != nil {
					pendingFinalEvent.Response.Usage = usage.ToResponsesResponseUsage()
				}
				if sendBackRawRequest {
					providerUtils.ParseAndSetRawRequest(&pendingFinalEvent.ExtraFields, jsonBody)
				}

				if sendBackRawResponse && len(pendingRawFrames) > 0 {
					trailing := drainPendingRawFrames()
					if existing, ok := pendingFinalEvent.ExtraFields.RawResponse.(string); ok && existing != "" {
						trailing = existing + "\n\n" + trailing
					}
					pendingFinalEvent.ExtraFields.RawResponse = trailing
				}
				pendingFinalEvent.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, pendingFinalEvent, nil, nil, nil), responseChan, postHookSpanFinalizer)
			}
		} else {
			finalFinishReason := finishReason
			if forwardedTerminalFinishReason {
				finalFinishReason = nil
			}
			response := providerUtils.CreateBifrostChatCompletionChunkResponse(messageID, usage, finalFinishReason, chunkIndex, modelName, created)
			if postResponseConverter != nil {
				response = postResponseConverter(response)
			}

			if serviceTier != nil {
				response.ServiceTier = serviceTier
			}

			if sendBackRawRequest {
				providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
			}

			if sendBackRawResponse && len(pendingRawFrames) > 0 {
				response.ExtraFields.RawResponse = drainPendingRawFrames()
			}
			response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, response, nil, nil, nil, nil), responseChan, postHookSpanFinalizer)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) Responses(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	if provider.shouldFallbackResponsesToChat(schemas.ResponsesRequest, schemas.ChatCompletionRequest) {
		chatResponse, err := provider.ChatCompletion(ctx, key, request.ToChatRequest())
		if err != nil {
			return nil, err
		}
		responsesResponse := chatResponse.ToBifrostResponsesResponse()

		responsesResponse.ExtraFields.ConvertedRequestType = schemas.ChatCompletionRequest
		return responsesResponse, nil
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ResponsesRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	if provider.disableStore {
		ctx.SetValue(schemas.BifrostContextKeyDisableStore, true)
	}

	return HandleOpenAIResponsesRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/responses", schemas.ResponsesRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		nil,
		nil,
		nil,
		provider.logger,
	)
}

func HandleOpenAIResponsesRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostResponsesRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	customResponseHandler responseHandler[schemas.BifrostResponsesResponse],
	customErrorConverter ErrorConverter,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeader, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostResponsesResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostResponsesResponse{
			Model:       request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIResponsesRequest(ctx, request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if signer != nil {
		sigHeaders, bErr := signer(jsonData)
		if bErr != nil {
			return nil, bErr
		}
		for k, v := range sigHeaders {
			req.Header.Set(k, v)
		}
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostResponsesResponse{
			Model:       request.Model,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostResponsesResponse{}

	var rawRequest, rawResponse interface{}

	if customResponseHandler != nil {

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		rawRequest, rawResponse, bifrostErr = customResponseHandler(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
		if pt != nil {
			if bifrostErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
	} else {
		rawRequest, rawResponse, bifrostErr = providerUtils.HandleProviderResponseCtx(ctx, body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	}

	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	if names := customToolNamesToRestore(ctx, request); names != nil {
		restoreCustomToolCalls(response.Output, names)
	}

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

func (provider *OpenAIProvider) ResponsesStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if provider.shouldFallbackResponsesToChat(schemas.ResponsesStreamRequest, schemas.ChatCompletionStreamRequest) {
		ctx.SetValue(schemas.BifrostContextKeyIsResponsesToChatCompletionFallback, true)
		return provider.ChatCompletionStream(ctx, postHookRunner, postHookSpanFinalizer, key, request.ToChatRequest())
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ResponsesStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}
	if provider.disableStore {
		ctx.SetValue(schemas.BifrostContextKeyDisableStore, true)
	}

	return HandleOpenAIResponsesStreaming(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/responses", schemas.ResponsesStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		nil,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAIResponsesStreaming(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostResponsesRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	customResponseHandler responseHandler[schemas.BifrostResponsesStreamResponse],
	customErrorConverter ErrorConverter,
	postRequestConverter func(*OpenAIResponsesRequest) *OpenAIResponsesRequest,
	postResponseConverter func(*schemas.BifrostResponsesStreamResponse) *schemas.BifrostResponsesStreamResponse,
	signer providerUtils.BodySigner,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {

		maps.Copy(headers, authHeader)
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody := ToOpenAIResponsesRequest(ctx, request)
			if reqBody != nil {
				reqBody.Stream = schemas.Ptr(true)
				if postRequestConverter != nil {
					reqBody = postRequestConverter(reqBody)
				}
			}
			return reqBody, nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	customToolRestorer := newCustomToolStreamRestorer(customToolNamesToRestore(ctx, request))

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

	setStreamingRequestBody(ctx, req, jsonBody, providerName)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
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
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
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

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)

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
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)

					return
				}
				break
			}
			jsonData := string(data)

			var response schemas.BifrostResponsesStreamResponse
			if customResponseHandler != nil {

				parseStart := time.Now()
				rawRequest, rawResponse, bifrostErr := customResponseHandler([]byte(jsonData), &response, nil, sendBackRawRequest, sendBackRawResponse)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if bifrostErr != nil {
					if sendBackRawRequest {
						bifrostErr.ExtraFields.RawRequest = rawRequest
					}
					if sendBackRawResponse {
						bifrostErr.ExtraFields.RawResponse = rawResponse
					}
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
					return
				}
				if sendBackRawResponse {
					response.ExtraFields.RawResponse = jsonData
				}
			} else {

				parseStart := time.Now()
				umErr := sonic.UnmarshalString(jsonData, &response)
				if umErr != nil {

					response = schemas.BifrostResponsesStreamResponse{}
					umErr = schemas.UnmarshalResponsesStreamObjectDelta([]byte(jsonData), &response)
				}
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if umErr != nil {
					logger.Warn("Failed to parse stream response: %v", umErr)
					continue
				}

				if postResponseConverter != nil {

					convStart := time.Now()
					converted := postResponseConverter(&response)
					schemas.AddStreamConvert(ctx, time.Since(convStart))
					if converted != nil {
						response = *converted
					} else {
						logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
					}
				}

				if sendBackRawResponse {
					response.ExtraFields.RawResponse = jsonData
				}
			}

			if prefix, drop := customToolRestorer.restore(&response); drop {
				continue
			} else if prefix != nil {
				prefix.ExtraFields.ChunkIndex = prefix.SequenceNumber
				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, prefix, nil, nil, nil), responseChan, postHookSpanFinalizer)
			}

			if response.Type == schemas.ResponsesStreamResponseTypeError {
				bifrostErr := responsesStreamError(&response)

				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, []byte(jsonData), sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
				return
			}

			if response.Type == schemas.ResponsesStreamResponseTypeFailed {
				bifrostErr := responsesStreamError(&response)
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, jsonBody, []byte(jsonData), sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
				return
			}

			response.ExtraFields.ChunkIndex = response.SequenceNumber
			if response.Type == schemas.ResponsesStreamResponseTypeCompleted || response.Type == schemas.ResponsesStreamResponseTypeIncomplete {

				if sendBackRawRequest {
					providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
				}
				response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, &response, nil, nil, nil), responseChan, postHookSpanFinalizer)
				return
			}

			response.ExtraFields.Latency = time.Since(lastChunkTime).Milliseconds()
			lastChunkTime = time.Now()

			providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, &response, nil, nil, nil), responseChan, postHookSpanFinalizer)
		}

		if !providerUtils.SSEStreamEndedOnMarker(sseReader) {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) Embedding(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.EmbeddingRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIEmbeddingRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/embeddings", schemas.EmbeddingRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		nil,
		nil,
		provider.logger,
	)
}

func HandleOpenAIEmbeddingRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostEmbeddingRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	customResponseHandler responseHandler[schemas.BifrostEmbeddingResponse],
	customErrorConverter ErrorConverter,
	logger schemas.Logger,
) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeader, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostEmbeddingResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostEmbeddingResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIEmbeddingRequest(request, providerName)
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		if customErrorConverter != nil {
			return nil, providerUtils.EnrichError(ctx, customErrorConverter(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
		}
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostEmbeddingResponse{
			Model:       request.Model,
			Usage:       lpResult.Usage,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostEmbeddingResponse{}

	var rawRequest, rawResponse interface{}

	if customResponseHandler != nil {

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		rawRequest, rawResponse, bifrostErr = customResponseHandler(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
		if pt != nil {
			if bifrostErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
	} else {
		rawRequest, rawResponse, bifrostErr = providerUtils.HandleProviderResponseCtx(ctx, body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	}

	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

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

func (provider *OpenAIProvider) shouldFallbackResponsesToChat(responsesOp, chatOp schemas.RequestType) bool {
	cfg := provider.customProviderConfig
	if cfg == nil || cfg.AllowedRequests == nil {
		return false
	}
	return !cfg.IsOperationAllowed(responsesOp) && cfg.IsOperationAllowed(chatOp)
}

func (provider *OpenAIProvider) Speech(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.SpeechRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAISpeechRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/audio/speech", schemas.SpeechRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		nil,
		provider.logger,
	)
}

func HandleOpenAISpeechRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostSpeechRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	customResponseHandler responseHandler[schemas.BifrostSpeechResponse],
	logger schemas.Logger,
) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeaders, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}

		return &schemas.BifrostSpeechResponse{
			Audio:       lpResult.ResponseBody,
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) { return ToOpenAISpeechRequest(request), nil })
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostSpeechResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	bifrostResponse := &schemas.BifrostSpeechResponse{
		Audio: body,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:                 latency.Milliseconds(),
			ProviderResponseHeaders: providerResponseHeaders,
		},
	}

	if sendBackRawRequest {
		providerUtils.ParseAndSetRawRequest(&bifrostResponse.ExtraFields, jsonData)
	}

	return bifrostResponse, nil
}

func (provider *OpenAIProvider) SpeechStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.SpeechStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	for _, model := range providerUtils.UnsupportedSpeechStreamModels {
		if model == request.Model {
			return nil, providerUtils.NewBifrostOperationError(fmt.Sprintf("model %s is not supported for streaming speech synthesis", model), nil)
		}
	}

	return HandleOpenAISpeechStreamRequest(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/audio/speech", schemas.SpeechStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
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

func HandleOpenAISpeechStreamRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostSpeechRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	postRequestConverter func(*OpenAISpeechRequest) *OpenAISpeechRequest,
	postResponseConverter func(*schemas.BifrostSpeechStreamResponse) *schemas.BifrostSpeechStreamResponse,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {
		maps.Copy(headers, authHeader)
	}

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			reqBody := ToOpenAISpeechRequest(request)
			if reqBody != nil {
				reqBody.StreamFormat = schemas.Ptr("sse")
				if postRequestConverter != nil {
					reqBody = postRequestConverter(reqBody)
				}
			}
			return reqBody, nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	setStreamingRequestBody(ctx, req, jsonBody, providerName)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
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
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
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

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)
		chunkIndex := -1

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
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
				}
				break
			}
			jsonData := string(data)

			if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

				var bifrostErr schemas.BifrostError
				if err := sonic.UnmarshalString(jsonData, &bifrostErr); err == nil {
					if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
						applyStreamErrorStatus(&bifrostErr)
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
						return
					}
				}
			}

			var response schemas.BifrostSpeechStreamResponse
			parseStart := time.Now()
			umErr := sonic.UnmarshalString(jsonData, &response)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if umErr != nil {
				logger.Warn("Failed to parse stream response: %v", umErr)
				continue
			}

			if postResponseConverter != nil {

				convStart := time.Now()
				converted := postResponseConverter(&response)
				schemas.AddStreamConvert(ctx, time.Since(convStart))
				if converted != nil {
					response = *converted
				} else {
					logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				}
			}

			chunkIndex++

			response.ExtraFields = schemas.BifrostResponseExtraFields{
				ChunkIndex: chunkIndex,
				Latency:    time.Since(lastChunkTime).Milliseconds(),
			}
			lastChunkTime = time.Now()

			if sendBackRawResponse {
				response.ExtraFields.RawResponse = jsonData
			}

			if response.Usage != nil {
				response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				if sendBackRawRequest {
					providerUtils.ParseAndSetRawRequest(&response.ExtraFields, jsonBody)
				}
				response.BackfillParams(request)
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, &response, nil, nil), responseChan, postHookSpanFinalizer)
				return
			}

			providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, &response, nil, nil), responseChan, postHookSpanFinalizer)
		}

		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); !ended && !providerUtils.SSEStreamEndedOnMarker(sseReader) {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) Transcription(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.TranscriptionRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAITranscriptionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/audio/transcriptions", schemas.TranscriptionRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		nil,
		provider.logger,
	)
}

func HandleOpenAITranscriptionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostTranscriptionRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawResponse bool,
	customResponseHandler responseHandler[schemas.BifrostTranscriptionResponse],
	logger schemas.Logger,
) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeaders, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}

		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostTranscriptionResponse{}
			if request.Params != nil && schemas.IsDiarizedTranscriptionFormat(request.Params.ResponseFormat) {
				var diarized openAIDiarizedTranscriptionResponse
				if err := sonic.Unmarshal(lpResult.ResponseBody, &diarized); err != nil {
					return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
				}
				response.Duration = diarized.Duration
				response.Task = diarized.Task
				response.Text = diarized.Text
				response.DiarizedSegments = diarized.Segments
				response.Usage = diarized.Usage
			} else if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostTranscriptionResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
	reqBody := ToOpenAITranscriptionRequest(request)
	if ct != nil {
		if reqBody == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "transcription input is not provided")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if reqBody == nil {
		return nil, providerUtils.NewBifrostOperationError("transcription input is not provided", nil)
	}

	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := ParseTranscriptionFormDataBodyFromRequest(writer, reqBody, providerName); err != nil {
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusError, "multipart encode failed")
		}
		return nil, err
	}
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}

	req.Header.SetContentType(writer.FormDataContentType())
	timedSetBody(ctx, req, body.Bytes())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.SetErrorLatency(finalErr, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostTranscriptionResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	trimmed := strings.TrimSpace(string(responseBody))
	if len(trimmed) == 0 {
		return nil, providerUtils.SetErrorLatency(&schemas.BifrostError{
			IsBifrostError: true,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseEmpty,
			},
		}, latency)
	}

	copiedResponseBody := append([]byte(nil), responseBody...)

	response := &schemas.BifrostTranscriptionResponse{}
	var rawResponse interface{}
	if request.Params != nil && schemas.IsPlainTextTranscriptionFormat(request.Params.ResponseFormat) {
		response.Text = string(copiedResponseBody)
		if sendBackRawResponse {
			rawResponse = string(copiedResponseBody)
		}
	} else if request.Params != nil && schemas.IsDiarizedTranscriptionFormat(request.Params.ResponseFormat) {
		var diarized openAIDiarizedTranscriptionResponse

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		umErr := sonic.Unmarshal(copiedResponseBody, &diarized)
		if pt != nil {
			if umErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
		if umErr != nil {
			if providerUtils.IsHTMLResponse(resp, copiedResponseBody) {
				return nil, providerUtils.SetErrorLatency(&schemas.BifrostError{
					IsBifrostError: false,
					Error: &schemas.ErrorField{
						Message: schemas.ErrProviderResponseHTML,
						Error:   errors.New(string(copiedResponseBody)),
					},
				}, latency)
			}
			return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, umErr), latency)
		}

		response.Duration = diarized.Duration
		response.Task = diarized.Task
		response.Text = diarized.Text
		response.DiarizedSegments = diarized.Segments
		response.Usage = diarized.Usage

		if sendBackRawResponse {
			if err := sonic.Unmarshal(copiedResponseBody, &rawResponse); err != nil {
				return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderRawResponseUnmarshal, err), latency)
			}
		}
	} else if customResponseHandler != nil {
		_, rawResponse, bifrostErr = customResponseHandler(copiedResponseBody, response, nil, false, sendBackRawResponse)
	} else {

		pt, ph := providerUtils.StartResponseParseSpan(ctx)
		umErr := sonic.Unmarshal(copiedResponseBody, response)
		if pt != nil {
			if umErr != nil {
				pt.EndSpan(ph, schemas.SpanStatusError, "response parse failed")
			} else {
				pt.EndSpan(ph, schemas.SpanStatusOk, "")
			}
		}
		if umErr != nil {

			if providerUtils.IsHTMLResponse(resp, copiedResponseBody) {
				return nil, providerUtils.SetErrorLatency(&schemas.BifrostError{
					IsBifrostError: false,
					Error: &schemas.ErrorField{
						Message: schemas.ErrProviderResponseHTML,
						Error:   errors.New(string(copiedResponseBody)),
					},
				}, latency)
			}
			return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, umErr), latency)
		}

		if sendBackRawResponse {
			if err := sonic.Unmarshal(copiedResponseBody, &rawResponse); err != nil {
				return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderRawResponseUnmarshal, err), latency)
			}
		}
	}

	if bifrostErr != nil {
		return nil, providerUtils.SetErrorLatency(bifrostErr, latency)
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) TranscriptionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.TranscriptionStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAITranscriptionStreamRequest(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/audio/transcriptions", schemas.TranscriptionStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		false,
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAITranscriptionStreamRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostTranscriptionRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawResponse bool,
	accumulateText bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	customResponseHandler responseHandler[schemas.BifrostTranscriptionStreamResponse],
	postRequestConverter func(*OpenAITranscriptionRequest) *OpenAITranscriptionRequest,
	postResponseConverter func(*schemas.BifrostTranscriptionStreamResponse) *schemas.BifrostTranscriptionStreamResponse,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
	reqBody := ToOpenAITranscriptionRequest(request)
	if ct != nil {
		if reqBody == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "transcription input is not provided")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if reqBody == nil {
		return nil, providerUtils.NewBifrostOperationError("transcription input is not provided", nil)
	}
	reqBody.Stream = schemas.Ptr(true)
	if postRequestConverter != nil {
		reqBody = postRequestConverter(reqBody)
	}

	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if bifrostErr := ParseTranscriptionFormDataBodyFromRequest(writer, reqBody, providerName); bifrostErr != nil {
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusError, "multipart encode failed")
		}
		return nil, bifrostErr
	}
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}

	headers := map[string]string{
		"Content-Type":  writer.FormDataContentType(),
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {
		maps.Copy(headers, authHeader)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	req.SetBody(body.Bytes())

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, client, req, resp)
	latency := time.Since(startTime)
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
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
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, err), latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
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
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)
		chunkIndex := -1

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
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)
				}
				break
			}
			jsonData := string(data)

			response := &schemas.BifrostTranscriptionStreamResponse{}
			var bifrostErr *schemas.BifrostError
			if customResponseHandler != nil {

				parseStart := time.Now()
				_, _, bifrostErr = customResponseHandler([]byte(jsonData), response, nil, false, false)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if bifrostErr != nil {
					if sendBackRawResponse {
						bifrostErr.ExtraFields.RawResponse = jsonData
					}
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, bifrostErr, body.Bytes(), []byte(jsonData), false, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
					return
				}
			} else {

				if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

					var bifrostErrVal schemas.BifrostError
					if err := sonic.UnmarshalString(jsonData, &bifrostErrVal); err == nil {
						if bifrostErrVal.Error != nil && bifrostErrVal.Error.Message != "" {
							applyStreamErrorStatus(&bifrostErrVal)
							ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
							respBody := append([]byte(nil), resp.Body()...)
							providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErrVal, body.Bytes(), respBody, false, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
							return
						}
					}
				}

				parseStart := time.Now()
				umErr := sonic.UnmarshalString(jsonData, response)
				schemas.AddStreamParse(ctx, time.Since(parseStart))
				if umErr != nil {
					logger.Warn("Failed to parse stream response: %v", umErr)
					continue

				}
			}

			if postResponseConverter != nil {

				convStart := time.Now()
				converted := postResponseConverter(response)
				schemas.AddStreamConvert(ctx, time.Since(convStart))
				if converted != nil {
					response = converted
				} else {
					logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				}
			}

			chunkIndex++

			response.ExtraFields = schemas.BifrostResponseExtraFields{
				ChunkIndex: chunkIndex,
				Latency:    time.Since(lastChunkTime).Milliseconds(),
			}
			lastChunkTime = time.Now()

			if sendBackRawResponse {
				response.ExtraFields.RawResponse = jsonData
			}

			if response.Usage != nil || response.Type == schemas.TranscriptionStreamResponseTypeDone {
				response.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)

				if accumulateText {
					response.Text = fullTranscriptionText
				}

				providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, response, nil), responseChan, postHookSpanFinalizer)
				return
			}

			providerUtils.ProcessAndSendResponse(ctx, postHookRunner, providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, response, nil), responseChan, postHookSpanFinalizer)
		}

		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); !ended && !providerUtils.SSEStreamEndedOnMarker(sseReader) {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) ImageGeneration(ctx *schemas.BifrostContext, key schemas.Key,
	req *schemas.BifrostImageGenerationRequest,
) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ImageGenerationRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIImageGenerationRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/images/generations", schemas.ImageGenerationRequest),
		req,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func HandleOpenAIImageGenerationRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostImageGenerationRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeaders, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostImageGenerationResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIImageGenerationRequest(request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	timedSetBody(ctx, req, jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, finalErr
	}
	if lpResult != nil {
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostImageGenerationResponse{}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

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

func (provider *OpenAIProvider) ImageGenerationStream(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	key schemas.Key,
	request *schemas.BifrostImageGenerationRequest,
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ImageGenerationStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIImageGenerationStreaming(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/images/generations", schemas.ImageGenerationStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAIImageGenerationStreaming(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostImageGenerationRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	customRequestConverter func(*schemas.BifrostImageGenerationRequest) (providerUtils.RequestBodyWithExtraParams, error),
	postRequestConverter func(*OpenAIImageGenerationRequest) *OpenAIImageGenerationRequest,
	postResponseConverter func(*schemas.BifrostImageGenerationStreamResponse) *schemas.BifrostImageGenerationStreamResponse,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {

		maps.Copy(headers, authHeader)
	}

	jsonBody, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			if customRequestConverter != nil {
				return customRequestConverter(request)
			}
			reqBody := ToOpenAIImageGenerationRequest(request)
			if reqBody != nil {
				reqBody.Stream = schemas.Ptr(true)
				if postRequestConverter != nil {
					reqBody = postRequestConverter(reqBody)
				}
			}
			return reqBody, nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

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

	setStreamingRequestBody(ctx, req, jsonBody, providerName)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, activeClient, req, resp)
	latency := time.Since(startTime)
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
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
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, err), latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency)
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

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)

		lastChunkTime := startTime

		imageChunkIndices := make(map[int]int)

		incompleteImages := make(map[int]bool)
		maxImageIndex := -1

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			data, readErr := sseReader.ReadDataLine()
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)

					return
				}
				break
			}
			jsonData := string(data)

			if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

				var bifrostErr schemas.BifrostError
				if err := sonic.UnmarshalString(jsonData, &bifrostErr); err == nil {
					if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
						applyStreamErrorStatus(&bifrostErr)
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErr, jsonBody, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
						return
					}
				}
			}

			var response OpenAIImageStreamResponse
			parseStart := time.Now()
			umErr := sonic.UnmarshalString(jsonData, &response)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if umErr != nil {
				logger.Warn("Failed to parse stream response: %v", umErr)
				continue
			}

			if response.Type == "error" {
				bifrostErr := &schemas.BifrostError{
					IsBifrostError: false,
					Error:          &schemas.ErrorField{},
				}

				if response.Error != nil {
					bifrostErr.Error.Message = response.Error.Message
					if response.Error.Code != nil {
						bifrostErr.Error.Code = response.Error.Code
					}
					if response.Error.Param != nil {
						bifrostErr.Error.Param = response.Error.Param
					}
					if response.Error.Type != nil {
						bifrostErr.Error.Type = response.Error.Type
					}
				}
				applyStreamErrorStatus(bifrostErr)
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, logger, postHookSpanFinalizer)
				return
			}

			isCompleted := response.Type == schemas.ImageGenerationEventTypeCompleted

			var imageIndex int
			if response.PartialImageIndex != nil {

				imageIndex = *response.PartialImageIndex
				if isCompleted {

					delete(incompleteImages, imageIndex)
				} else {

					incompleteImages[imageIndex] = true
				}
			} else {

				if isCompleted {

					if len(incompleteImages) == 0 {

						imageIndex = 0
						logger.Warn("Received completed event but no incomplete images tracked, defaulting to index 0")
					} else {

						minIndex := -1
						for idx := range incompleteImages {
							if minIndex == -1 || idx < minIndex {
								minIndex = idx
							}
						}
						imageIndex = minIndex

						delete(incompleteImages, imageIndex)
						logger.Warn("Completed event missing PartialImageIndex, using oldest incomplete image index %d", imageIndex)
					}
				} else {

					imageIndex = maxImageIndex + 1

					incompleteImages[imageIndex] = true
				}
			}

			if imageIndex > maxImageIndex {
				maxImageIndex = imageIndex
			}

			if _, exists := imageChunkIndices[imageIndex]; !exists {
				imageChunkIndices[imageIndex] = 0
			} else {
				imageChunkIndices[imageIndex]++
			}
			chunkIndex := imageChunkIndices[imageIndex]

			convStart := time.Now()
			chunk := &schemas.BifrostImageGenerationStreamResponse{
				Type:         response.Type,
				Index:        imageIndex,
				ChunkIndex:   chunkIndex,
				CreatedAt:    response.CreatedAt,
				Size:         response.Size,
				Quality:      response.Quality,
				Background:   response.Background,
				OutputFormat: response.OutputFormat,
				ExtraFields: schemas.BifrostResponseExtraFields{
					ChunkIndex: chunkIndex,
					Latency:    time.Since(lastChunkTime).Milliseconds(),
				},
			}
			if postResponseConverter != nil {
				if converted := postResponseConverter(chunk); converted != nil {
					chunk = converted
				} else {
					logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				}
			}
			schemas.AddStreamConvert(ctx, time.Since(convStart))

			if !isCompleted {
				chunk.PartialImageIndex = response.PartialImageIndex
			}

			if response.SequenceNumber != nil {
				chunk.SequenceNumber = *response.SequenceNumber
			}
			lastChunkTime = time.Now()

			if response.B64JSON != nil {
				chunk.B64JSON = *response.B64JSON
			}

			if sendBackRawResponse {
				chunk.ExtraFields.RawResponse = jsonData
			}

			if isCompleted {
				if response.Usage != nil && maxImageIndex >= 0 {
					if response.Usage.OutputTokensDetails == nil {
						response.Usage.OutputTokensDetails = &schemas.ImageTokenDetails{}
					}
					if response.Usage.OutputTokensDetails.NImages == 0 {
						response.Usage.OutputTokensDetails.NImages = maxImageIndex + 1
					}
				}
				chunk.Usage = response.Usage

				chunk.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				chunk.BackfillParams(&schemas.BifrostRequest{
					ImageGenerationRequest: request,
				})

				if sendBackRawRequest {
					providerUtils.ParseAndSetRawRequest(&chunk.ExtraFields, jsonBody)
				}
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			}

			providerUtils.ProcessAndSendResponse(ctx, postHookRunner,
				providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, nil, chunk),
				responseChan, postHookSpanFinalizer)

			if isCompleted {
				return
			}
		}

		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); !ended && !providerUtils.SSEStreamEndedOnMarker(sseReader) {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, jsonBody)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) Rerank(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	if provider.customProviderConfig == nil {
		return nil, providerUtils.NewUnsupportedOperationError(schemas.RerankRequest, schemas.OpenAI)
	}
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.RerankRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIRerankRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/rerank", schemas.RerankRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func (provider *OpenAIProvider) Decision(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostDecisionRequest) (*schemas.BifrostDecisionResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.DecisionRequest); err != nil {
		return nil, err
	}
	if provider.customProviderConfig == nil {
		capModel := schemas.ResolveCanonicalModel(ctx, request.Model)
		if !schemas.ResolveModelCaps(request.Provider, capModel).SupportsDecisions(schemas.DefaultSupportsDecisions(capModel)) {
			return nil, providerUtils.NewUnsupportedOperationError(schemas.DecisionRequest, provider.GetProviderKey())
		}
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIDecisionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, openAIDecisionsPath, schemas.DecisionRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func HandleOpenAIRerankRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostRerankRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()

	activeClient := client

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	for k, v := range BearerAuthHeader(key) {
		req.Header.Set(k, v)
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIRerankRequest(request), nil
		},
	)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if !providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, providerName) {
		req.SetBody(jsonData)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, _, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response := &OpenAIRerankResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	returnDocuments := request.Params != nil && request.Params.ReturnDocuments != nil && *request.Params.ReturnDocuments
	bifrostResponse := response.ToBifrostRerankResponse(request.Documents, returnDocuments)
	bifrostResponse.Model = request.Model
	bifrostResponse.ExtraFields.Latency = latency.Milliseconds()
	bifrostResponse.ExtraFields.ProviderResponseHeaders = providerResponseHeaders
	if sendBackRawRequest {
		bifrostResponse.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		bifrostResponse.ExtraFields.RawResponse = rawResponse
	}
	return bifrostResponse, nil
}

func (provider *OpenAIProvider) OCR(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.OCRRequest, provider.GetProviderKey())
}

func (provider *OpenAIProvider) VideoGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoGenerationRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIVideoGenerationRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/videos", schemas.VideoGenerationRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func (provider *OpenAIProvider) VideoRetrieve(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoRetrieveRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	providerName := provider.GetProviderKey()
	if request.ID == "" {
		return nil, providerUtils.NewBifrostOperationError("video_id is required", nil)
	}
	videoID := providerUtils.StripVideoIDProviderSuffix(request.ID, providerName)
	escapedVideoID, idErr := providerUtils.EscapeResourceID(videoID, "video_id")
	if idErr != nil {
		return nil, idErr
	}

	return HandleOpenAIVideoRetrieveRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/videos/"+escapedVideoID, schemas.VideoRetrieveRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		providerName,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.VideoDownload,
		provider.logger,
	)
}

func (provider *OpenAIProvider) VideoDownload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoDownloadRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	providerName := provider.GetProviderKey()

	if request.ID == "" {
		return nil, providerUtils.NewBifrostOperationError("video_id is required", nil)
	}
	videoID := providerUtils.StripVideoIDProviderSuffix(request.ID, providerName)
	escapedVideoID, idErr := providerUtils.EscapeResourceID(videoID, "video_id")
	if idErr != nil {
		return nil, idErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	requestURL := provider.buildRequestURL(ctx, "/v1/videos/"+escapedVideoID+"/content", schemas.VideoDownloadRequest)

	if request.Variant != nil && *request.Variant != "" {

		requestURL = fmt.Sprintf("%s?variant=%s", requestURL, url.QueryEscape(string(*request.Variant)))
	}

	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	contentType := string(resp.Header.ContentType())
	if contentType == "" {

		contentType = "video/mp4"
	}

	content := append([]byte(nil), body...)

	return &schemas.BifrostVideoDownloadResponse{
		VideoID:     providerUtils.AddVideoIDProviderSuffix(videoID, providerName),
		Content:     content,
		ContentType: contentType,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:                 latency.Milliseconds(),
			ProviderResponseHeaders: providerResponseHeaders,
		},
	}, nil
}

func (provider *OpenAIProvider) VideoDelete(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoDeleteRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	providerName := provider.GetProviderKey()

	if request.ID == "" {
		return nil, providerUtils.NewBifrostOperationError("video_id is required", nil)
	}
	videoID := providerUtils.StripVideoIDProviderSuffix(request.ID, providerName)
	escapedVideoID, idErr := providerUtils.EscapeResourceID(videoID, "video_id")
	if idErr != nil {
		return nil, idErr
	}

	return HandleOpenAIVideoDeleteRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/videos/"+escapedVideoID, schemas.VideoDeleteRequest),
		videoID,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		providerName,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func (provider *OpenAIProvider) VideoList(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoListRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIVideoListRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/videos", schemas.VideoListRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func HandleOpenAIVideoGenerationRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostVideoGenerationRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	reqBody, err := ToOpenAIVideoGenerationRequest(request)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert video generation request to openai format", err)
	}
	if reqBody == nil {
		return nil, providerUtils.NewBifrostOperationError("video generation input is not provided", nil)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := parseVideoGenerationFormDataBodyFromRequest(writer, reqBody, providerName); err != nil {
		return nil, err
	}

	req.Header.SetContentType(writer.FormDataContentType())
	req.SetBody(body.Bytes())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	trimmed := strings.TrimSpace(string(responseBody))
	if len(trimmed) == 0 {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseEmpty,
			},
		}
	}

	response := &schemas.BifrostVideoGenerationResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, response, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if response.ID != "" {
		response.ID = providerUtils.AddVideoIDProviderSuffix(response.ID, providerName)
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}

	return response, nil
}

func HandleOpenAIVideoEditRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostVideoEditRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostVideoEditResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}
	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}

	reqBody, err := ToOpenAIVideoEditRequest(request)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert video edit request to openai format", err)
	}

	if len(reqBody.Video.Bytes) > 0 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if bifrostErr := parseVideoEditFormDataBodyFromRequest(writer, reqBody); bifrostErr != nil {
			return nil, bifrostErr
		}
		req.Header.SetContentType(writer.FormDataContentType())
		req.SetBody(body.Bytes())
	} else {
		jsonBody, err := providerUtils.MarshalSorted(reqBody)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
		}
		req.Header.SetContentType("application/json")
		req.SetBody(jsonBody)
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	if len(strings.TrimSpace(string(responseBody))) == 0 {
		return nil, &schemas.BifrostError{
			IsBifrostError: true,
			Error: &schemas.ErrorField{
				Message: schemas.ErrProviderResponseEmpty,
			},
		}
	}

	response := &schemas.BifrostVideoEditResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, response, req.Body(), sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if response.ID != "" {
		response.ID = providerUtils.AddVideoIDProviderSuffix(response.ID, providerName)
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}

	return response, nil
}

func HandleOpenAIVideoRetrieveRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostVideoRetrieveRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	videoDownloaddHandler VideoDownloadHandler,
	logger schemas.Logger,
) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if len(authHeaders) > 0 {
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
	} else if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	response := &schemas.BifrostVideoGenerationResponse{}
	_, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if response.ID != "" {
		response.ID = providerUtils.AddVideoIDProviderSuffix(response.ID, providerName)
	}
	if response.RemixedFromVideoID != nil && *response.RemixedFromVideoID != "" {
		remixID := providerUtils.AddVideoIDProviderSuffix(*response.RemixedFromVideoID, providerName)
		response.RemixedFromVideoID = &remixID
	}

	if videoDownloaddHandler != nil {
		downloadVideo, ok := ctx.Value(schemas.BifrostContextKeyVideoOutputRequested).(bool)
		if ok && downloadVideo && response.Status == schemas.VideoStatusCompleted {
			videoDownloadRequest := &schemas.BifrostVideoDownloadRequest{
				Provider: providerName,
				ID:       response.ID,
			}
			videoDownloadResponse, bifrostErr := videoDownloaddHandler(ctx, key, videoDownloadRequest)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			if len(videoDownloadResponse.Content) > 0 {
				output := schemas.VideoOutput{
					Type:        schemas.VideoOutputTypeBase64,
					ContentType: videoDownloadResponse.ContentType,
				}
				base64Data := base64.StdEncoding.EncodeToString(videoDownloadResponse.Content)
				output.Base64Data = &base64Data
				response.Videos = append(response.Videos, output)
			} else {
				logger.Warn("no content found for video download request for %s video retrieve request", providerName)
			}
		}
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}
	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}
	return response, nil
}

func HandleOpenAIVideoDeleteRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	videoID string,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodDelete)
	req.Header.SetContentType("application/json")

	if len(authHeaders) > 0 {
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
	} else if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	response := &schemas.BifrostVideoDeleteResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if response.ID != "" {
		response.ID = providerUtils.AddVideoIDProviderSuffix(response.ID, providerName)
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}
	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}

	return response, nil
}

func HandleOpenAIVideoListRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	baseURL string,
	request *schemas.BifrostVideoListRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	providerName schemas.ModelProvider,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	logger schemas.Logger,
) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	values := url.Values{}
	if request.After != nil && *request.After != "" {
		values.Set("after", providerUtils.StripVideoIDProviderSuffix(*request.After, providerName))
	}
	if request.Limit != nil {
		values.Set("limit", fmt.Sprintf("%d", *request.Limit))
	}
	if request.Order != nil && *request.Order != "" {
		values.Set("order", *request.Order)
	}
	finalURL := baseURL
	if encoded := values.Encode(); encoded != "" {
		finalURL = baseURL + "?" + encoded
	}

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(finalURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if len(authHeaders) > 0 {
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
	} else if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	response := &schemas.BifrostVideoListResponse{}
	_, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	for i := range response.Data {
		if response.Data[i].ID != "" {
			response.Data[i].ID = providerUtils.AddVideoIDProviderSuffix(response.Data[i].ID, providerName)
		}
		if response.Data[i].RemixedFromVideoID != nil && *response.Data[i].RemixedFromVideoID != "" {
			remixID := providerUtils.AddVideoIDProviderSuffix(*response.Data[i].RemixedFromVideoID, providerName)
			response.Data[i].RemixedFromVideoID = &remixID
		}
	}
	if response.FirstID != nil && *response.FirstID != "" {
		firstID := providerUtils.AddVideoIDProviderSuffix(*response.FirstID, providerName)
		response.FirstID = &firstID
	}
	if response.LastID != nil && *response.LastID != "" {
		lastID := providerUtils.AddVideoIDProviderSuffix(*response.LastID, providerName)
		response.LastID = &lastID
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency:                 latency.Milliseconds(),
		ProviderResponseHeaders: providerResponseHeaders,
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) CountTokens(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.CountTokensRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAICountTokensRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/responses/input_tokens", schemas.CountTokensRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		provider.logger,
	)
}

func (provider *OpenAIProvider) Compaction(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCompactionRequest) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.CompactionRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAICompactionRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/responses/compact", schemas.CompactionRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		provider.logger,
	)
}

func HandleOpenAICompactionRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostCompactionRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeader, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostCompactionResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostCompactionResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAICompactionRequest(ctx, request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req.SetBody(jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug("error from %s provider with status %d", providerName, resp.StatusCode())
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, providerUtils.EnrichError(ctx, finalErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}
	if lpResult != nil {
		return &schemas.BifrostCompactionResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostCompactionResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}
	if providerUtils.ShouldSendBackRawResponse(ctx, sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func HandleOpenAICountTokensRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostResponsesRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, BearerAuthHeader(key), extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostCountTokensResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostCountTokensResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIResponsesRequest(ctx, request), nil
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req.SetBody(jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := providerUtils.ExtractProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		logger.Debug(fmt.Sprintf("error from %s provider: status %d", providerName, resp.StatusCode()))
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, finalErr
	}
	if lpResult != nil {
		return &schemas.BifrostCountTokensResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostCountTokensResponse{}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	response.Model = request.Model
	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if providerUtils.ShouldSendBackRawRequest(ctx, sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}

	if providerUtils.ShouldSendBackRawResponse(ctx, sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) ImageEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ImageEditRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIImageEditRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/images/edits", schemas.ImageEditRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		false,
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		provider.logger,
	)
}

func HandleOpenAIImageEditRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostImageEditRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	authHeaders map[string]string,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {

	if len(authHeaders) == 0 {
		authHeaders = BearerAuthHeader(key)
	}

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, authHeaders, extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostImageGenerationResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
	openaiReq := ToOpenAIImageEditRequest(request)
	if ct != nil {
		if openaiReq == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "failed to convert request to OpenAI format")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if openaiReq == nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert request to OpenAI format", nil)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)

	for k, v := range authHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "multipart/form-data")

	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := parseImageEditFormDataBodyFromRequest(writer, openaiReq, providerName); err != nil {
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusError, "multipart encode failed")
		}
		return nil, err
	}
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}

	req.Header.SetContentType(writer.FormDataContentType())
	bodyData := body.Bytes()
	timedSetBody(ctx, req, bodyData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, nil, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), nil, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	bodyBytes, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, finalErr
	}
	if lpResult != nil {
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostImageGenerationResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, bodyBytes, response, nil, false, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
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

func (provider *OpenAIProvider) ImageEditStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ImageEditStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIImageEditStreamRequest(
		ctx,
		provider.streamingClient,
		provider.buildRequestURL(ctx, "/v1/images/edits", schemas.ImageEditStreamRequest),
		request,
		BearerAuthHeader(key),
		provider.networkConfig.ExtraHeaders,
		provider.networkConfig.StreamIdleTimeoutInSeconds,
		false,
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		postHookRunner,
		nil,
		nil,
		nil,
		provider.logger,
		postHookSpanFinalizer,
	)
}

func HandleOpenAIImageEditStreamRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostImageEditRequest,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	streamIdleTimeoutInSeconds int,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	postHookRunner schemas.PostHookRunner,
	customRequestConverter func(*schemas.BifrostImageEditRequest) (providerUtils.RequestBodyWithExtraParams, error),
	postRequestConverter func(*OpenAIImageEditRequest) *OpenAIImageEditRequest,
	postResponseConverter func(*schemas.BifrostImageGenerationStreamResponse) *schemas.BifrostImageGenerationStreamResponse,
	logger schemas.Logger,
	postHookSpanFinalizer func(context.Context),
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, streamIdleTimeoutInSeconds)

	ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
	reqBody := ToOpenAIImageEditRequest(request)
	if ct != nil {
		if reqBody == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "image edit input is not provided")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if reqBody == nil {
		return nil, providerUtils.NewBifrostOperationError("image edit input is not provided", nil)
	}

	reqBody.Stream = schemas.Ptr(true)
	if postRequestConverter != nil {
		reqBody = postRequestConverter(reqBody)
	}

	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if bifrostErr := parseImageEditFormDataBodyFromRequest(writer, reqBody, providerName); bifrostErr != nil {
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusError, "multipart encode failed")
		}
		return nil, bifrostErr
	}
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}

	headers := map[string]string{
		"Content-Type":  writer.FormDataContentType(),
		"Accept":        "text/event-stream",
		"Cache-Control": "no-cache",
	}

	if authHeader != nil {
		maps.Copy(headers, authHeader)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	resp.StreamBody = true
	defer fasthttp.ReleaseRequest(req)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)

	req.Header.SetMethod(http.MethodPost)
	req.SetRequestURI(url)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	req.SetBody(body.Bytes())

	startTime := time.Now()

	err := providerUtils.DoStreamingRequest(ctx, client, req, resp)
	latency := time.Since(startTime)
	if err != nil {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
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
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, err), latency)
	}

	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerUtils.ExtractProviderResponseHeaders(resp))

	if resp.StatusCode() != fasthttp.StatusOK {
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), nil, nil, sendBackRawRequest, sendBackRawResponse, latency)
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
				providerUtils.HandleStreamCancellation(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
			} else if ctx.Err() == context.DeadlineExceeded {
				providerUtils.HandleStreamTimeout(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
			}
			providerUtils.CloseStream(ctx, responseChan)
		}()
		defer providerUtils.ReleaseStreamingResponse(ctx, resp)

		reader, releaseGzip := providerUtils.DecompressStreamBody(resp)
		defer releaseGzip()

		reader, stopIdleTimeout := providerUtils.NewIdleTimeoutReader(reader, resp.BodyStream(), providerUtils.GetStreamIdleTimeout(ctx), ctx)
		defer stopIdleTimeout()

		stopCancellation := providerUtils.SetupStreamCancellation(ctx, resp.BodyStream(), logger)
		defer stopCancellation()

		reader, nonSSE := providerUtils.DrainNonSSEStreamReader(resp, reader)
		if nonSSE != nil {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			providerUtils.ProcessAndSendNonSSEStreamError(ctx, postHookRunner, nonSSE, responseChan, logger, postHookSpanFinalizer)
			return
		}

		sseReader := providerUtils.GetSSEDataReader(ctx, reader)

		lastChunkTime := startTime

		imageChunkIndices := make(map[int]int)

		incompleteImages := make(map[int]bool)
		maxImageIndex := -1

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			data, readErr := sseReader.ReadDataLine()
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}
				if readErr != io.EOF {
					ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
					logger.Warn("Error reading stream: %v", readErr)
					providerUtils.ProcessAndSendError(ctx, postHookRunner, readErr, responseChan, logger, postHookSpanFinalizer)

					return
				}
				break
			}
			jsonData := string(data)

			if errorNode, _ := sonic.GetFromString(jsonData, "error"); errorNode.Exists() {

				var bifrostErr schemas.BifrostError
				if err := sonic.UnmarshalString(jsonData, &bifrostErr); err == nil {
					if bifrostErr.Error != nil && bifrostErr.Error.Message != "" {
						applyStreamErrorStatus(&bifrostErr)
						ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
						providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, providerUtils.EnrichError(ctx, &bifrostErr, nil, nil, sendBackRawRequest, sendBackRawResponse, latency), responseChan, logger, postHookSpanFinalizer)
						return
					}
				}
			}

			var response OpenAIImageStreamResponse
			parseStart := time.Now()
			umErr := sonic.UnmarshalString(jsonData, &response)
			schemas.AddStreamParse(ctx, time.Since(parseStart))
			if umErr != nil {
				logger.Warn("Failed to parse stream response: %v", umErr)
				continue
			}

			if response.Type == "error" {
				bifrostErr := &schemas.BifrostError{
					IsBifrostError: false,
					Error:          &schemas.ErrorField{},
				}

				if response.Error != nil {
					bifrostErr.Error.Message = response.Error.Message
					if response.Error.Code != nil {
						bifrostErr.Error.Code = response.Error.Code
					}
					if response.Error.Param != nil {
						bifrostErr.Error.Param = response.Error.Param
					}
					if response.Error.Type != nil {
						bifrostErr.Error.Type = response.Error.Type
					}
				}
				applyStreamErrorStatus(bifrostErr)
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				providerUtils.ProcessAndSendBifrostError(ctx, postHookRunner, bifrostErr, responseChan, logger, postHookSpanFinalizer)
				return
			}

			isCompleted := response.Type == schemas.ImageGenerationEventTypeCompleted || response.Type == schemas.ImageEditEventTypeCompleted

			var imageIndex int
			if response.PartialImageIndex != nil {

				imageIndex = *response.PartialImageIndex
				if isCompleted {

					delete(incompleteImages, imageIndex)
				} else {

					incompleteImages[imageIndex] = true
				}
			} else {

				if isCompleted {

					if len(incompleteImages) == 0 {

						imageIndex = 0
						logger.Warn("Received completed event but no incomplete images tracked, defaulting to index 0")
					} else {

						minIndex := -1
						for idx := range incompleteImages {
							if minIndex == -1 || idx < minIndex {
								minIndex = idx
							}
						}
						imageIndex = minIndex

						delete(incompleteImages, imageIndex)
						logger.Warn(fmt.Sprintf("Completed event missing PartialImageIndex, using oldest incomplete image index %d", imageIndex))
					}
				} else {

					imageIndex = maxImageIndex + 1

					incompleteImages[imageIndex] = true
				}
			}

			if imageIndex > maxImageIndex {
				maxImageIndex = imageIndex
			}

			if _, exists := imageChunkIndices[imageIndex]; !exists {
				imageChunkIndices[imageIndex] = 0
			} else {
				imageChunkIndices[imageIndex]++
			}
			chunkIndex := imageChunkIndices[imageIndex]

			convStart := time.Now()
			chunk := &schemas.BifrostImageGenerationStreamResponse{
				Type:         response.Type,
				Index:        imageIndex,
				ChunkIndex:   chunkIndex,
				CreatedAt:    response.CreatedAt,
				Size:         response.Size,
				Quality:      response.Quality,
				Background:   response.Background,
				OutputFormat: response.OutputFormat,
				ExtraFields: schemas.BifrostResponseExtraFields{
					ChunkIndex: chunkIndex,
					Latency:    time.Since(lastChunkTime).Milliseconds(),
				},
			}
			if postResponseConverter != nil {
				if converted := postResponseConverter(chunk); converted != nil {
					chunk = converted
				} else {
					logger.Warn("postResponseConverter returned nil; leaving chunk unmodified")
				}
			}
			schemas.AddStreamConvert(ctx, time.Since(convStart))

			if !isCompleted {
				chunk.PartialImageIndex = response.PartialImageIndex
			}

			if response.SequenceNumber != nil {
				chunk.SequenceNumber = *response.SequenceNumber
			}
			lastChunkTime = time.Now()

			if response.B64JSON != nil {
				chunk.B64JSON = *response.B64JSON
			}

			if sendBackRawResponse {
				chunk.ExtraFields.RawResponse = jsonData
			}

			if isCompleted {
				if response.Usage != nil && maxImageIndex >= 0 {
					if response.Usage.OutputTokensDetails == nil {
						response.Usage.OutputTokensDetails = &schemas.ImageTokenDetails{}
					}
					if response.Usage.OutputTokensDetails.NImages == 0 {
						response.Usage.OutputTokensDetails.NImages = maxImageIndex + 1
					}
				}
				chunk.Usage = response.Usage

				chunk.ExtraFields.Latency = time.Since(startTime).Milliseconds()
				chunk.BackfillParams(&schemas.BifrostRequest{
					ImageEditRequest: request,
				})
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			}

			providerUtils.ProcessAndSendResponse(ctx, postHookRunner,
				providerUtils.GetBifrostResponseForStreamResponse(nil, nil, nil, nil, nil, chunk),
				responseChan, postHookSpanFinalizer)

			if isCompleted {
				return
			}
		}

		if ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool); !ended && !providerUtils.SSEStreamEndedOnMarker(sseReader) {
			providerUtils.SendStreamTruncatedError(ctx, postHookRunner, responseChan, logger, postHookSpanFinalizer, nil)
		}
	}()

	return responseChan, nil
}

func (provider *OpenAIProvider) ImageVariation(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ImageVariationRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	response, err := HandleOpenAIImageVariationRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/images/variations", schemas.ImageVariationRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		false,
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.GetProviderKey(),
		provider.logger,
	)
	return response, err
}

func HandleOpenAIImageVariationRequest(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	request *schemas.BifrostImageVariationRequest,
	key schemas.Key,
	extraHeaders map[string]schemas.SecretVar,
	sendBackRawRequest bool,
	sendBackRawResponse bool,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {

	if lpResult, lpErr, handled := handleOpenAILargePayloadPassthrough(ctx, client, url, BearerAuthHeader(key), extraHeaders, providerName, logger); handled {
		if lpErr != nil {
			return nil, lpErr
		}
		if len(lpResult.ResponseBody) > 0 {
			response := &schemas.BifrostImageGenerationResponse{}
			if err := sonic.Unmarshal(lpResult.ResponseBody, response); err != nil {
				return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
			}
			response.ExtraFields = schemas.BifrostResponseExtraFields{Latency: lpResult.Latency}
			return response, nil
		}
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
	openaiReq := ToOpenAIImageVariationRequest(request)
	if ct != nil {
		if openaiReq == nil {
			ct.EndSpan(ch, schemas.SpanStatusError, "failed to convert request to OpenAI format")
		} else {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}
	}
	if openaiReq == nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert request to OpenAI format", nil)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	respOwned := true
	defer func() {
		if respOwned {
			fasthttp.ReleaseResponse(resp)
		}
	}()
	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := parseImageVariationFormDataBodyFromRequest(writer, openaiReq, providerName); err != nil {
		if mt != nil {
			mt.EndSpan(mh, schemas.SpanStatusError, "multipart encode failed")
		}
		return nil, err
	}
	if mt != nil {
		mt.EndSpan(mh, schemas.SpanStatusOk, "")
	}

	req.Header.SetContentType(writer.FormDataContentType())
	bodyData := body.Bytes()
	timedSetBody(ctx, req, bodyData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, nil, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	providerResponseHeaders := timedExtractResponseHeaders(ctx, resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, providerResponseHeaders)

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), nil, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	bodyBytes, lpResult, finalErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	respOwned = false
	if finalErr != nil {
		return nil, finalErr
	}
	if lpResult != nil {
		return &schemas.BifrostImageGenerationResponse{
			ExtraFields: schemas.BifrostResponseExtraFields{Latency: lpResult.Latency},
		}, nil
	}

	response := &schemas.BifrostImageGenerationResponse{}
	_, rawResponse, bifrostErr := providerUtils.HandleProviderResponseCtx(ctx, bodyBytes, response, nil, false, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	response.ExtraFields.Latency = latency.Milliseconds()
	response.ExtraFields.ProviderResponseHeaders = providerResponseHeaders

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}
	return response, nil
}

func (provider *OpenAIProvider) FileUpload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.FileUploadRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	if len(request.File) == 0 {
		return nil, providerUtils.NewBifrostOperationError("file content is required", nil)
	}

	if request.Purpose == "" {
		return nil, providerUtils.NewBifrostOperationError("purpose is required", nil)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("purpose", string(request.Purpose)); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to write purpose field", err)
	}

	if request.ExpiresAfter != nil {
		if err := writer.WriteField("expires_after[anchor]", request.ExpiresAfter.Anchor); err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to write expires_after[anchor] field", err)
		}
		if err := writer.WriteField("expires_after[seconds]", fmt.Sprintf("%d", request.ExpiresAfter.Seconds)); err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to write expires_after[seconds] field", err)
		}
	}

	filename := request.Filename
	if filename == "" {
		filename = "file.jsonl"
	}
	part, err := writer.CreateFormFile("file", filename)
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
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	req.SetBody(buf.Bytes())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", provider.GetProviderKey(), resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var openAIResp OpenAIFileResponse
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	fileResponse := openAIResp.ToBifrostFileUploadResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse)
	fileResponse.ExtraFields.ProviderResponseHeaders = providerUtils.ExtractProviderResponseHeaders(resp)
	return fileResponse, nil
}

func (provider *OpenAIProvider) FileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.FileListRequest); err != nil {
		return nil, err
	}

	providerName := provider.GetProviderKey()
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)

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

	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	requestURL := provider.buildRequestURL(ctx, "/v1/files", schemas.FileListRequest)
	values := url.Values{}
	if request.Purpose != "" {
		values.Set("purpose", string(request.Purpose))
	}
	if request.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", request.Limit))
	}

	if nativeCursor != "" {
		values.Set("after", nativeCursor)
	}
	if request.Order != nil && *request.Order != "" {
		values.Set("order", *request.Order)
	}
	if encoded := values.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, decodeErr := providerUtils.CheckAndDecodeBody(resp)
	if decodeErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decodeErr)
	}

	var openAIResp OpenAIFileListResponse
	_, _, bifrostErr = providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	files := make([]schemas.FileObject, 0, len(openAIResp.Data))
	var lastFileID string
	for _, file := range openAIResp.Data {
		files = append(files, schemas.FileObject{
			ID:            file.ID,
			Object:        file.Object,
			Bytes:         file.Bytes,
			CreatedAt:     file.CreatedAt,
			Filename:      file.Filename,
			Purpose:       schemas.FilePurpose(file.Purpose),
			Status:        ToBifrostFileStatus(file.Status),
			StatusDetails: file.StatusDetails,
		})
		lastFileID = file.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(openAIResp.HasMore, lastFileID)

	bifrostResp := &schemas.BifrostFileListResponse{
		Object:  "list",
		Data:    files,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency:                 latency.Milliseconds(),
			ProviderResponseHeaders: providerUtils.ExtractProviderResponseHeaders(resp),
		},
	}
	if nextCursor != "" {
		bifrostResp.After = &nextCursor
	}

	return bifrostResp, nil
}

func (provider *OpenAIProvider) FileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.FileRetrieveRequest); err != nil {
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

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedFileID)
		req.Header.SetMethod(http.MethodGet)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var openAIResp OpenAIFileResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		return openAIResp.ToBifrostFileRetrieveResponse(providerName, latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse), nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) FileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.FileDeleteRequest); err != nil {
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

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedFileID)
		req.Header.SetMethod(http.MethodDelete)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var openAIResp OpenAIFileDeleteResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := &schemas.BifrostFileDeleteResponse{
			ID:      openAIResp.ID,
			Object:  openAIResp.Object,
			Deleted: openAIResp.Deleted,
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

func (provider *OpenAIProvider) FileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.FileContentRequest); err != nil {
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

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedFileID + "/content")
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
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

func (provider *OpenAIProvider) VideoEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoEditRequest) (*schemas.BifrostVideoEditResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoEditRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	return HandleOpenAIVideoEditRequest(
		ctx,
		provider.client,
		provider.buildRequestURL(ctx, "/v1/videos/edits", schemas.VideoEditRequest),
		request,
		key,
		provider.networkConfig.ExtraHeaders,
		nil,
		provider.GetProviderKey(),
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
		provider.logger,
	)
}

func (provider *OpenAIProvider) VideoRemix(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.VideoRemixRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	providerName := provider.GetProviderKey()

	if request.ID == "" {
		return nil, providerUtils.NewBifrostOperationError("video_id is required", nil)
	}
	if request.Input == nil || request.Input.Prompt == "" {
		return nil, providerUtils.NewBifrostOperationError("prompt is required", nil)
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToOpenAIVideoRemixRequest(request)
		})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	videoID := providerUtils.StripVideoIDProviderSuffix(request.ID, providerName)
	escapedVideoID, idErr := providerUtils.EscapeResourceID(videoID, "video_id")
	if idErr != nil {
		return nil, idErr
	}

	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/videos/"+escapedVideoID+"/remix", schemas.VideoRemixRequest))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	req.SetBody(jsonData)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		provider.logger.Debug("error from %s provider: status %d", providerName, resp.StatusCode())
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	response := &schemas.BifrostVideoGenerationResponse{}
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, response, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if response.ID != "" {
		response.ID = providerUtils.AddVideoIDProviderSuffix(response.ID, providerName)
	}
	if response.RemixedFromVideoID != nil && *response.RemixedFromVideoID != "" {
		remixID := providerUtils.AddVideoIDProviderSuffix(*response.RemixedFromVideoID, providerName)
		response.RemixedFromVideoID = &remixID
	}

	response.ExtraFields = schemas.BifrostResponseExtraFields{
		Latency: latency.Milliseconds(),
	}

	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}
	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}

	return response, nil
}

func (provider *OpenAIProvider) BatchCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.BatchCreateRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	inputFileID := request.InputFileID

	if inputFileID == "" && len(request.Requests) > 0 {

		jsonlData, err := ConvertRequestsToJSONL(request.Requests)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to convert requests to JSONL", err)
		}

		uploadResp, bifrostErr := provider.FileUpload(ctx, key, &schemas.BifrostFileUploadRequest{
			Provider: schemas.OpenAI,
			File:     jsonlData,
			Filename: "batch_requests.jsonl",
			Purpose:  "batch",
		})
		if bifrostErr != nil {
			return nil, bifrostErr
		}

		inputFileID = uploadResp.ID
	}

	if inputFileID == "" {
		return nil, providerUtils.NewBifrostOperationError("either input_file_id or requests array is required for OpenAI batch API", nil)
	}

	if request.Endpoint == "" {
		return nil, providerUtils.NewBifrostOperationError("endpoint is required for OpenAI batch API", nil)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/batches", schemas.BatchCreateRequest))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	openAIReq := &OpenAIBatchRequest{
		InputFileID:        schemas.Ptr(inputFileID),
		Endpoint:           string(request.Endpoint),
		CompletionWindow:   request.CompletionWindow,
		Metadata:           request.Metadata,
		OutputExpiresAfter: request.OutputExpiresAfter,
	}

	if openAIReq.CompletionWindow == "" {
		openAIReq.CompletionWindow = "24h"
	}

	jsonData, err := providerUtils.MarshalSorted(openAIReq)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}
	req.SetBody(jsonData)

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.EnrichError(ctx, ParseOpenAIError(resp), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.EnrichError(ctx, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err), jsonData, nil, sendBackRawRequest, sendBackRawResponse, latency)
	}

	var openAIResp OpenAIBatchResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, jsonData, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, providerUtils.EnrichError(ctx, bifrostErr, jsonData, body, sendBackRawRequest, sendBackRawResponse, latency)
	}

	return openAIResp.ToBifrostBatchCreateResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse), nil
}

func (provider *OpenAIProvider) BatchList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.BatchListRequest); err != nil {
		return nil, err
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	helper, err := providerUtils.NewSerialListHelper(keys, request.After, provider.logger, true)
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

	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	baseURL := provider.buildRequestURL(ctx, "/v1/batches", schemas.BatchListRequest)
	values := url.Values{}
	if request.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", request.Limit))
	}

	if nativeCursor != "" {
		values.Set("after", nativeCursor)
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
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	body, decodeErr := providerUtils.CheckAndDecodeBody(resp)
	if decodeErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decodeErr)
	}

	var openAIResp OpenAIBatchListResponse
	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	batches := make([]schemas.BifrostBatchRetrieveResponse, 0, len(openAIResp.Data))
	var lastBatchID string
	for _, batch := range openAIResp.Data {
		batches = append(batches, *batch.ToBifrostBatchRetrieveResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse))
		lastBatchID = batch.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(openAIResp.HasMore, lastBatchID)

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

func (provider *OpenAIProvider) BatchRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.BatchRetrieveRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}
	escapedBatchID, idErr := providerUtils.EscapeResourceID(request.BatchID, "batch_id")
	if idErr != nil {
		return nil, idErr
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/batches/" + escapedBatchID)
		req.Header.SetMethod(http.MethodGet)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var openAIResp OpenAIBatchResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := openAIResp.ToBifrostBatchRetrieveResponse(latency, sendBackRawRequest, sendBackRawResponse, rawRequest, rawResponse)
		return result, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) BatchCancel(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.BatchCancelRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}
	escapedBatchID, idErr := providerUtils.EscapeResourceID(request.BatchID, "batch_id")
	if idErr != nil {
		return nil, idErr
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/batches/" + escapedBatchID + "/cancel")
		req.Header.SetMethod(http.MethodPost)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		var openAIResp OpenAIBatchResponse
		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(body, &openAIResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		result := &schemas.BifrostBatchCancelResponse{
			ID:           openAIResp.ID,
			Object:       openAIResp.Object,
			Status:       ToBifrostBatchStatus(openAIResp.Status),
			CancellingAt: openAIResp.CancellingAt,
			CancelledAt:  openAIResp.CancelledAt,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if openAIResp.RequestCounts != nil {
			result.RequestCounts = schemas.BatchRequestCounts{
				Total:     openAIResp.RequestCounts.Total,
				Completed: openAIResp.RequestCounts.Completed,
				Failed:    openAIResp.RequestCounts.Failed,
			}
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

func (provider *OpenAIProvider) BatchDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.BatchDeleteRequest, provider.GetProviderKey())
}

func (provider *OpenAIProvider) BatchResults(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.BatchResultsRequest); err != nil {
		return nil, err
	}

	if request.BatchID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch_id is required", nil)
	}

	batchResp, bifrostErr := provider.BatchRetrieve(ctx, keys, &schemas.BifrostBatchRetrieveRequest{
		Provider: request.Provider,
		BatchID:  request.BatchID,
	})
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if batchResp.OutputFileID == nil || *batchResp.OutputFileID == "" {
		return nil, providerUtils.NewBifrostOperationError("batch results not available: output_file_id is empty (batch may not be completed)", nil)
	}
	escapedOutputFileID, idErr := providerUtils.EscapeResourceID(*batchResp.OutputFileID, "output_file_id")
	if idErr != nil {
		return nil, providerUtils.NewBifrostOperationError("provider returned an invalid output_file_id", nil)
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
		req.SetRequestURI(provider.networkConfig.BaseURL + "/v1/files/" + escapedOutputFileID + "/content")
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		var results []schemas.BatchResultItem

		parseResult := providerUtils.ParseJSONL(body, func(line []byte) error {
			var resultItem schemas.BatchResultItem
			if err := sonic.Unmarshal(line, &resultItem); err != nil {
				provider.logger.Warn("failed to parse batch result line: %v", err)
				return err
			}
			results = append(results, resultItem)
			return nil
		})

		batchResultsResp := &schemas.BifrostBatchResultsResponse{
			BatchID:  request.BatchID,
			Endpoint: schemas.BatchEndpoint(batchResp.Endpoint),
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

func (provider *OpenAIProvider) ContainerCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerCreateRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.Name == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: name is required", nil)
	}

	reqBody := map[string]interface{}{
		"name": request.Name,
	}

	if request.ExpiresAfter != nil {
		reqBody["expires_after"] = map[string]interface{}{
			"anchor":  request.ExpiresAfter.Anchor,
			"minutes": request.ExpiresAfter.Minutes,
		}
	}

	if len(request.FileIDs) > 0 {
		reqBody["file_ids"] = request.FileIDs
	}

	if request.MemoryLimit != "" {
		reqBody["memory_limit"] = request.MemoryLimit
	}

	if len(request.Metadata) > 0 {
		reqBody["metadata"] = request.Metadata
	}

	for k, v := range request.ExtraParams {
		if _, exists := reqBody[k]; !exists {
			reqBody[k] = v
		}
	}

	jsonBody, err := providerUtils.MarshalSorted(reqBody)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/containers", schemas.ContainerCreateRequest))
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	req.SetBody(jsonBody)

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK && resp.StatusCode() != fasthttp.StatusCreated {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody := append([]byte(nil), resp.Body()...)

	var containerResp struct {
		ID           string                         `json:"id"`
		Object       string                         `json:"object"`
		Name         string                         `json:"name"`
		CreatedAt    int64                          `json:"created_at"`
		Status       schemas.ContainerStatus        `json:"status"`
		ExpiresAfter *schemas.ContainerExpiresAfter `json:"expires_after"`
		LastActiveAt *int64                         `json:"last_active_at"`
		MemoryLimit  string                         `json:"memory_limit"`
		Metadata     map[string]string              `json:"metadata"`
	}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &containerResp, jsonBody, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	response := &schemas.BifrostContainerCreateResponse{
		ID:           containerResp.ID,
		Object:       containerResp.Object,
		Name:         containerResp.Name,
		CreatedAt:    containerResp.CreatedAt,
		Status:       containerResp.Status,
		ExpiresAfter: containerResp.ExpiresAfter,
		LastActiveAt: containerResp.LastActiveAt,
		MemoryLimit:  containerResp.MemoryLimit,
		Metadata:     containerResp.Metadata,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		response.ExtraFields.RawRequest = rawRequest
	}
	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) ContainerList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("provider config not found", nil)
		}
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerListRequest); err != nil {
		return nil, err
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	helper, err := providerUtils.NewSerialListHelper(keys, request.After, provider.logger, true)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("invalid pagination cursor", err)
	}

	key, nativeCursor, ok := helper.GetCurrentKey()
	if !ok {

		return &schemas.BifrostContainerListResponse{
			Object:  "list",
			Data:    []schemas.ContainerObject{},
			HasMore: false,
		}, nil
	}

	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	queryParams := url.Values{}
	if request.Limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", request.Limit))
	}

	if nativeCursor != "" {
		queryParams.Set("after", nativeCursor)
	}
	if request.Order != nil {
		queryParams.Set("order", *request.Order)
	}

	requestURL := provider.buildRequestURL(ctx, "/v1/containers", schemas.ContainerListRequest)
	if len(queryParams) > 0 {
		requestURL += "?" + queryParams.Encode()
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody := append([]byte(nil), resp.Body()...)

	var listResp struct {
		Object  string                    `json:"object"`
		Data    []schemas.ContainerObject `json:"data"`
		FirstID *string                   `json:"first_id"`
		LastID  *string                   `json:"last_id"`
		HasMore bool                      `json:"has_more"`
	}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &listResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	var lastContainerID string
	for _, container := range listResp.Data {
		lastContainerID = container.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(listResp.HasMore, lastContainerID)

	response := &schemas.BifrostContainerListResponse{
		Object:  listResp.Object,
		Data:    listResp.Data,
		FirstID: listResp.FirstID,
		LastID:  listResp.LastID,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if nextCursor != "" {
		response.After = &nextCursor
	}

	if sendBackRawRequest {
		response.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		response.ExtraFields.RawResponse = rawResponse
	}

	return response, nil
}

func (provider *OpenAIProvider) ContainerRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("provider config not found", nil)
		}
	}
	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("container_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerRetrieveRequest); err != nil {
		return nil, err
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

		req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/containers/"+escapedContainerID, schemas.ContainerRetrieveRequest))
		req.Header.SetMethod(http.MethodGet)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		responseBody := append([]byte(nil), resp.Body()...)

		var containerResp struct {
			ID           string                         `json:"id"`
			Object       string                         `json:"object"`
			Name         string                         `json:"name"`
			CreatedAt    int64                          `json:"created_at"`
			Status       schemas.ContainerStatus        `json:"status"`
			ExpiresAfter *schemas.ContainerExpiresAfter `json:"expires_after"`
			LastActiveAt *int64                         `json:"last_active_at"`
			MemoryLimit  string                         `json:"memory_limit"`
			Metadata     map[string]string              `json:"metadata"`
		}

		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &containerResp, nil, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		response := &schemas.BifrostContainerRetrieveResponse{
			ID:           containerResp.ID,
			Object:       containerResp.Object,
			Name:         containerResp.Name,
			CreatedAt:    containerResp.CreatedAt,
			Status:       containerResp.Status,
			ExpiresAfter: containerResp.ExpiresAfter,
			LastActiveAt: containerResp.LastActiveAt,
			MemoryLimit:  containerResp.MemoryLimit,
			Metadata:     containerResp.Metadata,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			response.ExtraFields.RawRequest = rawRequest
		}
		if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
			response.ExtraFields.RawResponse = rawResponse
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return response, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) ContainerDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("provider config not found", nil)
		}
	}
	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("container_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerDeleteRequest); err != nil {
		return nil, err
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

		req.SetRequestURI(provider.buildRequestURL(ctx, "/v1/containers/"+escapedContainerID, schemas.ContainerDeleteRequest))
		req.Header.SetMethod(http.MethodDelete)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		if resp.StatusCode() != fasthttp.StatusOK {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		responseBody := append([]byte(nil), resp.Body()...)

		var deleteResp struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Deleted bool   `json:"deleted"`
		}

		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &deleteResp, nil, providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest), providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse))
		if bifrostErr != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = bifrostErr
			continue
		}

		response := &schemas.BifrostContainerDeleteResponse{
			ID:      deleteResp.ID,
			Object:  deleteResp.Object,
			Deleted: deleteResp.Deleted,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			response.ExtraFields.RawRequest = rawRequest
		}
		if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
			response.ExtraFields.RawResponse = rawResponse
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return response, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) ContainerFileCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerFileCreateRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: container_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	endpoint := fmt.Sprintf("/v1/containers/%s/files", escapedContainerID)
	req.SetRequestURI(provider.buildRequestURL(ctx, endpoint, schemas.ContainerFileCreateRequest))
	req.Header.SetMethod(http.MethodPost)

	if len(request.File) == 0 {
		return nil, providerUtils.NewBifrostOperationError("invalid request: file is required", nil)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "file")
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to create multipart form", err)
	}
	if _, err = part.Write(request.File); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to write file to multipart form", err)
	}
	if err := writer.Close(); err != nil {
		return nil, providerUtils.NewBifrostOperationError("failed to close multipart form", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetBody(body.Bytes())

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() >= 400 {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}
	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	var fileResp struct {
		ID          string `json:"id"`
		Object      string `json:"object"`
		Bytes       int64  `json:"bytes"`
		CreatedAt   int64  `json:"created_at"`
		ContainerID string `json:"container_id"`
		Path        string `json:"path"`
		Source      string `json:"source"`
	}

	_, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &fileResp, nil, false, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	containerFileCreateResponse := &schemas.BifrostContainerFileCreateResponse{
		ID:          fileResp.ID,
		Object:      fileResp.Object,
		Bytes:       fileResp.Bytes,
		CreatedAt:   fileResp.CreatedAt,
		ContainerID: fileResp.ContainerID,
		Path:        fileResp.Path,
		Source:      fileResp.Source,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if sendBackRawRequest {
		containerFileCreateResponse.ExtraFields.RawRequest = "<REDACTED>"
	}
	if sendBackRawResponse {
		containerFileCreateResponse.ExtraFields.RawResponse = rawResponse
	}

	return containerFileCreateResponse, nil
}

func (provider *OpenAIProvider) ContainerFileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: container_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}

	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("no keys provided", nil)
		}
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerFileListRequest); err != nil {
		return nil, err
	}

	sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

	helper, err := providerUtils.NewSerialListHelper(keys, request.After, provider.logger, true)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError("invalid pagination cursor", err)
	}

	key, nativeCursor, ok := helper.GetCurrentKey()
	if !ok {

		return &schemas.BifrostContainerFileListResponse{
			Object:  "list",
			Data:    []schemas.ContainerFileObject{},
			HasMore: false,
		}, nil
	}

	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	endpoint := fmt.Sprintf("/v1/containers/%s/files", escapedContainerID)
	requestURL := provider.buildRequestURL(ctx, endpoint, schemas.ContainerFileListRequest)

	queryParams := url.Values{}
	if request.Limit > 0 {
		queryParams.Set("limit", fmt.Sprintf("%d", request.Limit))
	}

	if nativeCursor != "" {
		queryParams.Set("after", nativeCursor)
	}
	if request.Order != nil {
		queryParams.Set("order", *request.Order)
	}
	if len(queryParams) > 0 {
		requestURL = requestURL + "?" + queryParams.Encode()
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)

	if key.Value.GetValue() != "" {
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() >= 400 {
		return nil, providerUtils.SetErrorLatency(ParseOpenAIError(resp), latency)
	}

	responseBody, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	var listResp struct {
		Object  string                        `json:"object"`
		Data    []schemas.ContainerFileObject `json:"data"`
		FirstID *string                       `json:"first_id"`
		LastID  *string                       `json:"last_id"`
		HasMore bool                          `json:"has_more"`
	}

	rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &listResp, nil, sendBackRawRequest, sendBackRawResponse)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	var lastFileID string
	for _, file := range listResp.Data {
		lastFileID = file.ID
	}

	nextCursor, hasMore := helper.BuildNextCursor(listResp.HasMore, lastFileID)

	containerFileListResponse := &schemas.BifrostContainerFileListResponse{
		Object:  listResp.Object,
		Data:    listResp.Data,
		FirstID: listResp.FirstID,
		LastID:  listResp.LastID,
		HasMore: hasMore,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if nextCursor != "" {
		containerFileListResponse.After = &nextCursor
	}

	if sendBackRawRequest {
		containerFileListResponse.ExtraFields.RawRequest = rawRequest
	}
	if sendBackRawResponse {
		containerFileListResponse.ExtraFields.RawResponse = rawResponse
	}

	return containerFileListResponse, nil
}

func (provider *OpenAIProvider) ContainerFileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("no keys provided", nil)
		}
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerFileRetrieveRequest); err != nil {
		return nil, err
	}

	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: container_id is required", nil)
	}

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: file_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

		endpoint := fmt.Sprintf("/v1/containers/%s/files/%s", escapedContainerID, escapedFileID)
		req.SetRequestURI(provider.buildRequestURL(ctx, endpoint, schemas.ContainerFileRetrieveRequest))
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			lastErr = bifrostErr
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		if resp.StatusCode() >= 400 {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		responseBody, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}
		sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
		sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

		var fileResp struct {
			ID          string `json:"id"`
			Object      string `json:"object"`
			Bytes       int64  `json:"bytes"`
			CreatedAt   int64  `json:"created_at"`
			ContainerID string `json:"container_id"`
			Path        string `json:"path"`
			Source      string `json:"source"`
		}

		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &fileResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			lastErr = bifrostErr
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		containerFileRetrieveResponse := &schemas.BifrostContainerFileRetrieveResponse{
			ID:          fileResp.ID,
			Object:      fileResp.Object,
			Bytes:       fileResp.Bytes,
			CreatedAt:   fileResp.CreatedAt,
			ContainerID: fileResp.ContainerID,
			Path:        fileResp.Path,
			Source:      fileResp.Source,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if sendBackRawRequest {
			containerFileRetrieveResponse.ExtraFields.RawRequest = rawRequest
		}
		if sendBackRawResponse {
			containerFileRetrieveResponse.ExtraFields.RawResponse = rawResponse
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return containerFileRetrieveResponse, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) ContainerFileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("no keys provided", nil)
		}
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerFileContentRequest); err != nil {
		return nil, err
	}

	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: container_id is required", nil)
	}

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: file_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

		endpoint := fmt.Sprintf("/v1/containers/%s/files/%s/content", escapedContainerID, escapedFileID)
		req.SetRequestURI(provider.buildRequestURL(ctx, endpoint, schemas.ContainerFileContentRequest))
		req.Header.SetMethod(http.MethodGet)

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			lastErr = bifrostErr
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		if resp.StatusCode() >= 400 {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		contentType := string(resp.Header.ContentType())
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		body, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			continue
		}
		content := append([]byte(nil), body...)

		containerFileContentResponse := &schemas.BifrostContainerFileContentResponse{
			Content:     content,
			ContentType: contentType,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			containerFileContentResponse.ExtraFields.RawRequest = map[string]string{
				"container_id": request.ContainerID,
				"file_id":      request.FileID,
			}
		}
		if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
			containerFileContentResponse.ExtraFields.RawResponse = "<REDACTED>"
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return containerFileContentResponse, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) ContainerFileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	if len(keys) == 0 {
		if provider.customProviderConfig != nil && provider.customProviderConfig.IsKeyLess {
			keys = []schemas.Key{{}}
		} else {
			return nil, providerUtils.NewBifrostOperationError("no keys provided", nil)
		}
	}

	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.ContainerFileDeleteRequest); err != nil {
		return nil, err
	}

	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("invalid request: nil", nil)
	}

	if request.ContainerID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: container_id is required", nil)
	}

	if request.FileID == "" {
		return nil, providerUtils.NewBifrostOperationError("invalid request: file_id is required", nil)
	}
	escapedContainerID, idErr := providerUtils.EscapeResourceID(request.ContainerID, "container_id")
	if idErr != nil {
		return nil, idErr
	}
	escapedFileID, idErr := providerUtils.EscapeResourceID(request.FileID, "file_id")
	if idErr != nil {
		return nil, idErr
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {

		key, keyErr := provider.resolveKey(ctx, key)
		if keyErr != nil {
			lastErr = keyErr
			continue
		}
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

		endpoint := fmt.Sprintf("/v1/containers/%s/files/%s", escapedContainerID, escapedFileID)
		req.SetRequestURI(provider.buildRequestURL(ctx, endpoint, schemas.ContainerFileDeleteRequest))
		req.Header.SetMethod(http.MethodDelete)
		req.Header.SetContentType("application/json")

		if key.Value.GetValue() != "" {
			req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
		}

		latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
		wait()
		if bifrostErr != nil {
			lastErr = bifrostErr
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		if resp.StatusCode() >= 400 {
			lastErr = ParseOpenAIError(resp)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		responseBody, err := providerUtils.CheckAndDecodeBody(resp)
		if err != nil {
			lastErr = providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}
		sendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
		sendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)

		var deleteResp struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Deleted bool   `json:"deleted"`
		}

		rawRequest, rawResponse, bifrostErr := providerUtils.HandleProviderResponse(responseBody, &deleteResp, nil, sendBackRawRequest, sendBackRawResponse)
		if bifrostErr != nil {
			lastErr = bifrostErr
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			continue
		}

		containerFileDeleteResponse := &schemas.BifrostContainerFileDeleteResponse{
			ID:      deleteResp.ID,
			Object:  deleteResp.Object,
			Deleted: deleteResp.Deleted,
			ExtraFields: schemas.BifrostResponseExtraFields{
				Latency: latency.Milliseconds(),
			},
		}

		if sendBackRawRequest {
			containerFileDeleteResponse.ExtraFields.RawRequest = rawRequest
		}
		if sendBackRawResponse {
			containerFileDeleteResponse.ExtraFields.RawResponse = rawResponse
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return containerFileDeleteResponse, nil
	}

	return nil, lastErr
}

func (provider *OpenAIProvider) Passthrough(
	ctx *schemas.BifrostContext,
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.PassthroughRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	url, err := provider.buildPassthroughURL(req)
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
		fasthttpReq.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

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
		passthroughUsage = ExtractOpenAIPassthroughUsage(req.Method, req.Path, req.Body, body)
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

func (provider *OpenAIProvider) buildPassthroughURL(req *schemas.BifrostPassthroughRequest) (string, error) {
	path := req.Path
	baseURL := provider.networkConfig.BaseURL
	if req.UpstreamURL != "" {
		baseURL = req.UpstreamURL
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return providerUtils.BuildPassthroughURL(baseURL, path, req.RawQuery)
	}

	if after, ok := strings.CutPrefix(path, "/v1"); ok {
		path = after
	}

	return providerUtils.BuildPassthroughURL(baseURL, "/v1"+path, req.RawQuery)
}

func (provider *OpenAIProvider) PassthroughStream(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	key schemas.Key,
	req *schemas.BifrostPassthroughRequest,
) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.OpenAI, provider.customProviderConfig, schemas.PassthroughStreamRequest); err != nil {
		return nil, err
	}
	key, keyErr := provider.resolveKey(ctx, key)
	if keyErr != nil {
		return nil, keyErr
	}

	providerUtils.SetStreamIdleTimeoutIfEmpty(ctx, provider.networkConfig.StreamIdleTimeoutInSeconds)
	url, err := provider.buildPassthroughURL(req)
	if err != nil {
		return nil, providerUtils.NewBifrostBadRequestError(err.Error())
	}

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
		fasthttpReq.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	}

	fasthttpReq.SetBody(req.Body)

	activeClient := providerUtils.PrepareResponseStreaming(ctx, provider.streamingClient, resp)

	startTime := time.Now()

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
		return nil, providerUtils.SetErrorLatency(providerUtils.NewBifrostOperationError(schemas.ErrProviderDoRequest, err), latency)
	}

	headers := providerUtils.ExtractPassthroughProviderResponseHeaders(resp)
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)

	rawBodyStream := resp.BodyStream()
	if rawBodyStream == nil {
		providerUtils.ReleaseStreamingResponse(ctx, resp)
		return nil, providerUtils.NewBifrostOperationError(
			"provider returned an empty stream body",
			fmt.Errorf("provider returned an empty stream body"))
	}

	return providerUtils.StreamPassthrough(
		ctx, postHookRunner, postHookSpanFinalizer, resp, rawBodyStream,
		providerUtils.PassthroughStreamParams{
			StatusCode:       resp.StatusCode(),
			Headers:          headers,
			Path:             req.Path,
			RawRequest:       req.Body,
			CancellationBody: providerUtils.PassthroughJSONBody(fasthttpReq, req.Body),
			StartTime:        startTime,
			Logger:           provider.logger,
			HasUsage:         HasOpenAIPassthroughUsage,
			EndsStream:       EndsOpenAIPassthroughStream,
			Observe: func(event []byte) *schemas.BifrostPassthroughUsage {
				return ExtractOpenAIPassthroughUsage(req.Method, req.Path, req.Body, event)
			},
		},
	), nil
}
