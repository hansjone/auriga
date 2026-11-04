// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package bifrost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/maximhq/bifrost/core/keyselectors"

	"github.com/maximhq/bifrost/core/providers/anthropic"

	"github.com/maximhq/bifrost/core/providers/deepseek"

	"github.com/maximhq/bifrost/core/providers/gemini"

	"github.com/maximhq/bifrost/core/providers/minimax"

	"github.com/maximhq/bifrost/core/providers/openai"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

var moonshotSchemaFormats = []string{"date", "date-time", "time", "duration", "email", "hostname", "ipv4", "ipv6", "uri", "uuid"}
var errAllKeysDead = errors.New("all configured keys returned permanent per-key errors")

type pluginTimingAccumulator struct {
	totalDuration time.Duration
	invocations   int
	errors        int
	span          schemas.SpanHandle
}
type ProviderQueue struct {
	queue      chan *ChannelMessage
	done       chan struct{}
	closing    uint32
	signalOnce sync.Once
}

var errAllKeysFiltered = errors.New("all eligible keys are temporarily suppressed by the key pool filter")

type Bifrost struct {
	ctx        *schemas.BifrostContext
	cancel     context.CancelFunc
	account    schemas.Account
	llmPlugins atomic.Pointer[[]schemas.LLMPlugin]

	providers           atomic.Pointer[[]schemas.Provider]
	requestQueues       sync.Map
	waitGroups          sync.Map
	oldWorkerCleanups   sync.WaitGroup
	retiredWorkerWaits  sync.Map
	providerLifecycleMu sync.RWMutex
	providerMutexes     sync.Map
	channelMessagePool  sync.Pool
	responseChannelPool sync.Pool
	errorChannelPool    sync.Pool
	responseStreamPool  sync.Pool
	pluginPipelinePool  sync.Pool
	bifrostRequestPool  sync.Pool
	logger              schemas.Logger
	tracer              atomic.Value
	modelCatalog        schemas.ModelInfoProvider

	dropExcessRequests atomic.Bool
	keySelector        schemas.KeySelector
	keyPoolFilter      schemas.KeyPoolFilter
	kvStore            schemas.KVStore
	sessionAffinity    schemas.SessionAffinity
}
type tracerWrapper struct {
	tracer schemas.Tracer
}
type PluginPipeline struct {
	llmPlugins []schemas.LLMPlugin

	logger schemas.Logger
	tracer schemas.Tracer

	executedPreHooks int

	preHookErrors  []error
	postHookErrors []error

	streamingMu         sync.Mutex
	postHookTimings     map[string]*pluginTimingAccumulator
	postHookPluginOrder []string
	chunkCount          int

	streamScopedCtxs map[string]*schemas.BifrostContext
	streamSpanParent context.Context
}
type ChannelMessage struct {
	schemas.BifrostRequest
	Context        *schemas.BifrostContext
	Response       chan *schemas.BifrostResponse
	ResponseStream chan chan *schemas.BifrostStreamChunk
	Err            chan schemas.BifrostError
	queueSpan      schemas.SpanHandle
	sentAt         time.Time

	firstTokenTimeout  time.Duration
	streamPostHooksRan bool

	handoff atomic.Int32
}
type coreSpan struct {
	h    schemas.SpanHandle
	ctx  *schemas.BifrostContext
	prev any
}
type providerModelLister interface {
	GetModelsForProvider(provider schemas.ModelProvider) []string
	GetUnfilteredModelsForProvider(provider schemas.ModelProvider) []string
}
type listModelsContextKey string
type streamPostHookSpan struct {
	span          schemas.SpanHandle
	totalDuration time.Duration
	invocations   int
	errors        int
}

const (
	handoffOpen int32 = iota
	handoffClaimed
	handoffAbandoned
)

func (m *ChannelMessage) claimDelivery() bool {
	return m.handoff.CompareAndSwap(handoffOpen, handoffClaimed)
}

func (m *ChannelMessage) abandonDelivery() bool {
	ctx := m.Context
	if !m.handoff.CompareAndSwap(handoffOpen, handoffAbandoned) {
		return false
	}

	if ctx != nil {
		if tracer, traceID, err := GetTracerFromContext(ctx); err == nil {
			tracer.DeferTraceCompletion(traceID)
		}
	}
	return true
}

func isLargePayloadPassthrough(ctx *schemas.BifrostContext) bool {
	if ctx == nil {
		return false
	}

	isLargePayload, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool)
	if !isLargePayload {
		return false
	}

	reader := ctx.Value(schemas.BifrostContextKeyLargePayloadReader)
	return reader != nil
}

func (pq *ProviderQueue) signalClosing() {
	pq.signalOnce.Do(func() {
		atomic.StoreUint32(&pq.closing, 1)
		close(pq.done)
	})
}

func (pq *ProviderQueue) isClosing() bool {
	return atomic.LoadUint32(&pq.closing) == 1
}

func Init(ctx context.Context, config schemas.BifrostConfig) (*Bifrost, error) {
	if config.Account == nil {
		return nil, fmt.Errorf("account is required to initialize Bifrost")
	}

	if config.Logger == nil {
		config.Logger = NewDefaultLogger(schemas.LogLevelInfo)
	}
	providerUtils.SetLogger(config.Logger)

	tracer := config.Tracer
	if tracer == nil {
		tracer = schemas.DefaultTracer()
	}

	bifrostCtx, cancel := schemas.NewBifrostContextWithCancel(ctx)
	bifrost := &Bifrost{
		ctx:        bifrostCtx,
		cancel:     cancel,
		account:    config.Account,
		llmPlugins: atomic.Pointer[[]schemas.LLMPlugin]{},

		requestQueues: sync.Map{},
		waitGroups:    sync.Map{},
		keySelector:   config.KeySelector,
		keyPoolFilter: config.KeyPoolFilter,

		logger:       config.Logger,
		kvStore:      config.KVStore,
		modelCatalog: config.ModelCatalog,
	}
	bifrost.tracer.Store(&tracerWrapper{tracer: tracer})
	if config.LLMPlugins == nil {
		config.LLMPlugins = make([]schemas.LLMPlugin, 0)
	}

	bifrost.llmPlugins.Store(&config.LLMPlugins)

	bifrost.providers.Store(&[]schemas.Provider{})

	bifrost.dropExcessRequests.Store(config.DropExcessRequests)

	if bifrost.keySelector == nil {
		bifrost.keySelector = keyselectors.WeightedRandom
	}

	bifrost.sessionAffinity = config.SessionAffinity
	if bifrost.sessionAffinity == nil {
		bifrost.sessionAffinity = NewSessionAffinity(bifrost.kvStore, bifrost.logger)
	}

	bifrost.channelMessagePool = sync.Pool{
		New: func() interface{} {
			return &ChannelMessage{}
		},
	}
	bifrost.responseChannelPool = sync.Pool{
		New: func() interface{} {
			return make(chan *schemas.BifrostResponse, 1)
		},
	}
	bifrost.errorChannelPool = sync.Pool{
		New: func() interface{} {
			return make(chan schemas.BifrostError, 1)
		},
	}
	bifrost.responseStreamPool = sync.Pool{
		New: func() interface{} {
			return make(chan chan *schemas.BifrostStreamChunk, 1)
		},
	}
	bifrost.pluginPipelinePool = sync.Pool{
		New: func() interface{} {
			return &PluginPipeline{
				preHookErrors:  make([]error, 0),
				postHookErrors: make([]error, 0),
			}
		},
	}
	bifrost.bifrostRequestPool = sync.Pool{
		New: func() interface{} {
			return &schemas.BifrostRequest{}
		},
	}

	for range config.InitialPoolSize {

		bifrost.channelMessagePool.Put(&ChannelMessage{})
		bifrost.responseChannelPool.Put(make(chan *schemas.BifrostResponse, 1))
		bifrost.errorChannelPool.Put(make(chan schemas.BifrostError, 1))
		bifrost.responseStreamPool.Put(make(chan chan *schemas.BifrostStreamChunk, 1))
		bifrost.pluginPipelinePool.Put(&PluginPipeline{
			preHookErrors:  make([]error, 0),
			postHookErrors: make([]error, 0),
		})
		bifrost.bifrostRequestPool.Put(&schemas.BifrostRequest{})
	}

	providerKeys, err := bifrost.account.GetConfiguredProviders()
	if err != nil {
		return nil, err
	}

	for _, providerKey := range providerKeys {
		if strings.TrimSpace(string(providerKey)) == "" {
			bifrost.logger.Warn("provider key is empty, skipping init")
			continue
		}

		config, err := bifrost.account.GetConfigForProvider(providerKey)
		if err != nil {
			bifrost.logger.Warn("failed to get config for provider %s, skipping init: %v", providerKey, err)
			continue
		}
		if config == nil {
			bifrost.logger.Warn("config is nil for provider %s, skipping init", providerKey)
			continue
		}

		providerMutex := bifrost.getProviderMutex(providerKey)
		providerMutex.Lock()
		err = bifrost.prepareProvider(providerKey, config)
		providerMutex.Unlock()

		if err != nil {
			bifrost.logger.Warn("failed to prepare provider %s: %v", providerKey, err)
		}
	}
	return bifrost, nil
}

func (bifrost *Bifrost) getTracer() schemas.Tracer {
	return bifrost.tracer.Load().(*tracerWrapper).tracer
}

func (bifrost *Bifrost) getModelCatalog() schemas.ModelInfoProvider {
	return bifrost.modelCatalog
}

func (bifrost *Bifrost) setModelCatalogOnContext(ctx *schemas.BifrostContext) {
	if ctx == nil {
		return
	}
	catalog := bifrost.getModelCatalog()
	if catalog == nil {
		ctx.ClearValue(schemas.BifrostContextKeyModelCatalog)
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyModelCatalog, catalog)
}

func (bifrost *Bifrost) ListModelsRequest(ctx *schemas.BifrostContext, req *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	if req == nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "list models request is nil",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ListModelsRequest,
			},
		}
	}
	if req.Provider == "" {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "provider is required for list models request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ListModelsRequest,
			},
		}
	}
	if ctx == nil {
		ctx = bifrost.ctx
	}

	reqCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
	reqCtx.SetValue(schemas.BifrostContextKeySkipBudgetAndRateLimits, true)

	bifrostReq := bifrost.getBifrostRequest()
	bifrostReq.RequestType = schemas.ListModelsRequest
	bifrostReq.ListModelsRequest = req

	resp, err := bifrost.handleRequest(reqCtx, bifrostReq)
	if err != nil {
		return nil, err
	}

	return resp.ListModelsResponse, nil
}

func (bifrost *Bifrost) lastKnownModelsFromCatalog(providerKey schemas.ModelProvider, unfiltered bool) []schemas.Model {
	lister, ok := bifrost.getModelCatalog().(providerModelLister)
	if !ok {
		return nil
	}
	var names []string
	if unfiltered {
		names = lister.GetUnfilteredModelsForProvider(providerKey)
	} else {
		names = lister.GetModelsForProvider(providerKey)
	}
	if len(names) == 0 {
		return nil
	}
	models := make([]schemas.Model, 0, len(names))
	for _, name := range names {
		models = append(models, schemas.Model{ID: string(providerKey) + "/" + name})
	}
	return models
}

const listAllModelsFallbackKey listModelsContextKey = "list-all-models-catalog-fallback"

func isExpectedListModelsError(err *schemas.BifrostError) bool {
	if err == nil {
		return false
	}
	if err.Type != nil && *err.Type == "provider_blocked" {
		return true
	}
	if err.Error == nil {
		return false
	}
	return strings.Contains(err.Error.Message, "no keys found") || strings.Contains(err.Error.Message, "not supported")
}

func (bifrost *Bifrost) lastKnownListModelsResponse(ctx *schemas.BifrostContext, providerKey schemas.ModelProvider, req *schemas.BifrostListModelsRequest, err *schemas.BifrostError) *schemas.BifrostResponse {
	if req == nil || req.PageToken != "" || isExpectedListModelsError(err) {
		return nil
	}
	if fallback, _ := ctx.Value(listAllModelsFallbackKey).(bool); !fallback {
		return nil
	}
	models := bifrost.lastKnownModelsFromCatalog(providerKey, req.Unfiltered)
	if len(models) == 0 {
		return nil
	}
	bifrost.logger.Warn("serving %d last-known models for provider %s from the model catalog after its live poll failed: %s", len(models), providerKey, err.GetErrorString())
	return &schemas.BifrostResponse{ListModelsResponse: &schemas.BifrostListModelsResponse{
		Data:        models,
		KeyStatuses: err.ExtraFields.KeyStatuses,
	}}
}

func (bifrost *Bifrost) ModelRetrieveRequest(ctx *schemas.BifrostContext, req *schemas.BifrostModelRetrieveRequest) (*schemas.BifrostModelRetrieveResponse, *schemas.BifrostError) {
	if req == nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "model retrieve request is nil",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ModelRetrieveRequest,
			},
		}
	}
	if req.Provider == "" {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "provider is required for model retrieve request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ModelRetrieveRequest,
			},
		}
	}
	if req.Model == "" {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "model is required for model retrieve request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				Provider:    req.Provider,
				RequestType: schemas.ModelRetrieveRequest,
			},
		}
	}
	if ctx == nil {
		ctx = bifrost.ctx
	}

	reqCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)

	reqCtx.SetValue(schemas.BifrostContextKeySkipBudgetAndRateLimits, true)

	bifrostReq := bifrost.getBifrostRequest()
	bifrostReq.RequestType = schemas.ModelRetrieveRequest
	bifrostReq.ModelRetrieveRequest = req

	resp, err := bifrost.handleRequest(reqCtx, bifrostReq)
	if err != nil {
		return nil, err
	}

	return resp.ModelRetrieveResponse, nil
}

func (bifrost *Bifrost) makeChatCompletionRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	if req == nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "chat completion request is nil",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ChatCompletionRequest,
			},
		}
	}
	if req.Input == nil && !isLargePayloadPassthrough(ctx) {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "chats not provided for chat completion request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            schemas.ChatCompletionRequest,
				Provider:               req.Provider,
				OriginalModelRequested: req.Model,
				ResolvedModelUsed:      req.Model,
			},
		}
	}

	bifrostReq := bifrost.getBifrostRequest()
	bifrostReq.RequestType = schemas.ChatCompletionRequest
	bifrostReq.ChatRequest = req

	response, err := bifrost.handleRequest(ctx, bifrostReq)
	if err != nil {
		return nil, err
	}

	return response.ChatResponse, nil
}

func (bifrost *Bifrost) ChatCompletionRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {

	if ctx == nil {
		ctx = bifrost.ctx
	}

	response, err := bifrost.makeChatCompletionRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (bifrost *Bifrost) ChatCompletionStreamRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if req == nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "chat completion stream request is nil",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.ChatCompletionStreamRequest,
			},
		}
	}
	if req.Input == nil && !isLargePayloadPassthrough(ctx) {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "chats not provided for chat completion request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            schemas.ChatCompletionStreamRequest,
				Provider:               req.Provider,
				OriginalModelRequested: req.Model,
				ResolvedModelUsed:      req.Model,
			},
		}
	}

	bifrostReq := bifrost.getBifrostRequest()
	bifrostReq.RequestType = schemas.ChatCompletionStreamRequest
	bifrostReq.ChatRequest = req

	return bifrost.handleStreamRequest(ctx, bifrostReq)
}

func (bifrost *Bifrost) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	providers := bifrost.providers.Load()
	if providers == nil {
		return nil, fmt.Errorf("no providers configured")
	}
	modelProviders := make([]schemas.ModelProvider, len(*providers))
	for i, provider := range *providers {
		modelProviders[i] = provider.GetProviderKey()
	}
	return modelProviders, nil
}

func (bifrost *Bifrost) getProviderMutex(providerKey schemas.ModelProvider) *sync.RWMutex {
	mutexValue, _ := bifrost.providerMutexes.LoadOrStore(providerKey, &sync.RWMutex{})
	return mutexValue.(*sync.RWMutex)
}

func (bifrost *Bifrost) createBaseProvider(providerKey schemas.ModelProvider, config *schemas.ProviderConfig) (schemas.Provider, error) {

	targetProviderKey := providerKey

	if config.CustomProviderConfig != nil {

		if config.CustomProviderConfig.BaseProviderType == "" {
			return nil, fmt.Errorf("custom provider config missing base provider type")
		}

		if !IsSupportedBaseProvider(config.CustomProviderConfig.BaseProviderType) {
			return nil, fmt.Errorf("unsupported base provider type: %s", config.CustomProviderConfig.BaseProviderType)
		}

		config.CustomProviderConfig.CustomProviderKey = string(providerKey)

		targetProviderKey = config.CustomProviderConfig.BaseProviderType
	}

	switch targetProviderKey {
	case schemas.OpenAI:
		return openai.NewOpenAIProvider(config, bifrost.logger), nil
	case schemas.Anthropic:
		return anthropic.NewAnthropicProvider(config, bifrost.logger), nil

	case schemas.DeepSeek:
		return deepseek.NewDeepSeekProvider(config, bifrost.logger)

	case schemas.MiniMax:
		return minimax.NewMiniMaxProvider(config, bifrost.logger)

	case schemas.Gemini:
		return gemini.NewGeminiProvider(config, bifrost.logger), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s", targetProviderKey)
	}
}

func (bifrost *Bifrost) prepareProvider(providerKey schemas.ModelProvider, config *schemas.ProviderConfig) error {

	pq := &ProviderQueue{
		queue:      make(chan *ChannelMessage, config.ConcurrencyAndBufferSize.BufferSize),
		done:       make(chan struct{}),
		signalOnce: sync.Once{},
	}

	bifrost.requestQueues.Store(providerKey, pq)

	bifrost.waitGroups.Store(providerKey, &sync.WaitGroup{})

	provider, err := bifrost.createBaseProvider(providerKey, config)
	if err != nil {
		return fmt.Errorf("failed to create provider for the given key: %v", err)
	}

	waitGroupValue, _ := bifrost.waitGroups.Load(providerKey)
	currentWaitGroup := waitGroupValue.(*sync.WaitGroup)

	for {
		oldPtr := bifrost.providers.Load()
		var oldSlice []schemas.Provider
		if oldPtr != nil {
			oldSlice = *oldPtr
		}
		newSlice := make([]schemas.Provider, len(oldSlice)+1)
		copy(newSlice, oldSlice)
		newSlice[len(oldSlice)] = provider
		if bifrost.providers.CompareAndSwap(oldPtr, &newSlice) {
			break
		}
	}

	schemas.RegisterKnownProvider(providerKey)

	for range config.ConcurrencyAndBufferSize.Concurrency {
		currentWaitGroup.Add(1)
		go bifrost.requestWorker(provider, config, pq, currentWaitGroup)
	}

	return nil
}

func (bifrost *Bifrost) getProviderQueue(providerKey schemas.ModelProvider) (*ProviderQueue, error) {

	providerMutex := bifrost.getProviderMutex(providerKey)
	providerMutex.RLock()

	if pqValue, exists := bifrost.requestQueues.Load(providerKey); exists {
		pq := pqValue.(*ProviderQueue)
		providerMutex.RUnlock()
		return pq, nil
	}

	providerMutex.RUnlock()
	providerMutex.Lock()
	defer providerMutex.Unlock()

	if pqValue, exists := bifrost.requestQueues.Load(providerKey); exists {
		pq := pqValue.(*ProviderQueue)
		return pq, nil
	}
	bifrost.logger.Debug("Creating new request queue for provider %s at runtime", providerKey)
	config, err := bifrost.account.GetConfigForProvider(providerKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get config for provider %s: %v", providerKey, err)
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil for provider %s", providerKey)
	}
	if err := bifrost.prepareProvider(providerKey, config); err != nil {
		return nil, err
	}
	pqValue, ok := bifrost.requestQueues.Load(providerKey)
	if !ok {
		return nil, fmt.Errorf("request queue not found for provider %s", providerKey)
	}
	pq := pqValue.(*ProviderQueue)
	return pq, nil
}

func (bifrost *Bifrost) baseProviderType(providerKey schemas.ModelProvider) schemas.ModelProvider {
	if config, err := bifrost.account.GetConfigForProvider(providerKey); err == nil && config != nil &&
		config.CustomProviderConfig != nil && config.CustomProviderConfig.BaseProviderType != "" {
		return config.CustomProviderConfig.BaseProviderType
	}
	return providerKey
}

func (bifrost *Bifrost) RunPreRequestHooks(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) {
	if ctx == nil {
		ctx = bifrost.ctx
	}

	if _, ok := ctx.Value(schemas.BifrostContextKeyRequestID).(string); !ok {
		ctx.SetValue(schemas.BifrostContextKeyRequestID, uuid.New().String())
	}
	bifrost.setModelCatalogOnContext(ctx)

	pipeline := bifrost.getPluginPipeline()
	defer bifrost.releasePluginPipeline(pipeline)
	pipeline.RunPreRequestHooks(ctx, req)

	flushPluginLogs(ctx)
}

func (bifrost *Bifrost) shouldTryFallbacks(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, primaryErr *schemas.BifrostError) bool {

	if primaryErr == nil {
		bifrost.logger.Debug("no primary error, we should not try fallbacks")
		return false
	}

	if primaryErr.Error != nil && primaryErr.Error.Type != nil && *primaryErr.Error.Type == schemas.RequestCancelled {
		bifrost.logger.Debug("request cancelled, we should not try fallbacks")
		return false
	}

	if primaryErr.AllowFallbacks != nil && !*primaryErr.AllowFallbacks {
		bifrost.logger.Debug("allowFallbacks is false, we should not try fallbacks")
		return false
	}

	_, _, fallbacks := req.GetRequestFields()
	if len(fallbacks) == 0 {
		bifrost.logger.Debug("no fallbacks configured, we should not try fallbacks")
		return false
	}

	if fallbacks[0].Inferred && !isNoKeySupportsModel(primaryErr) && ClassifyFailure(primaryErr) == schemas.FailureClassCallerFault {
		bifrost.logger.Debug("primary refused the request itself, not trying %d inferred fallbacks", len(fallbacks))
		if ctx != nil {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Primary refused the request itself (%s); skipping %d model-catalog fallback(s) the caller did not name", routingErrorSummary(primaryErr), len(fallbacks)))
		}
		return false
	}

	return true
}

func isNoKeySupportsModel(err *schemas.BifrostError) bool {
	return err.Type != nil && *err.Type == schemas.NoKeySupportsModel
}

func (bifrost *Bifrost) prepareFallbackRequest(req *schemas.BifrostRequest, fallback schemas.Fallback) *schemas.BifrostRequest {

	_, err := bifrost.account.GetConfigForProvider(fallback.Provider)
	if err != nil {
		bifrost.logger.Warn("config not found for provider %s, skipping fallback: %v", fallback.Provider, err)
		return nil
	}

	fallbackReq := *req

	if req.TextCompletionRequest != nil {
		tmp := *req.TextCompletionRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.TextCompletionRequest = &tmp
	}

	if req.ChatRequest != nil {
		tmp := *req.ChatRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.ChatRequest = &tmp
	}

	if req.ResponsesRequest != nil {
		tmp := *req.ResponsesRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.ResponsesRequest = &tmp
	}

	if req.CountTokensRequest != nil {
		tmp := *req.CountTokensRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.CountTokensRequest = &tmp
	}

	if req.CompactionRequest != nil {
		tmp := *req.CompactionRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.CompactionRequest = &tmp
	}

	if req.EmbeddingRequest != nil {
		tmp := *req.EmbeddingRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.EmbeddingRequest = &tmp
	}
	if req.RerankRequest != nil {
		tmp := *req.RerankRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.RerankRequest = &tmp
	}
	if req.DecisionRequest != nil {
		tmp := *req.DecisionRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.DecisionRequest = &tmp
	}
	if req.OCRRequest != nil {
		tmp := *req.OCRRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.OCRRequest = &tmp
	}

	if req.SpeechRequest != nil {
		tmp := *req.SpeechRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.SpeechRequest = &tmp
	}

	if req.TranscriptionRequest != nil {
		tmp := *req.TranscriptionRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.TranscriptionRequest = &tmp
	}
	if req.ImageGenerationRequest != nil {
		tmp := *req.ImageGenerationRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.ImageGenerationRequest = &tmp
	}
	if req.VideoGenerationRequest != nil {
		tmp := *req.VideoGenerationRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.VideoGenerationRequest = &tmp
	}
	if req.VideoEditRequest != nil {
		tmp := *req.VideoEditRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.VideoEditRequest = &tmp
	}
	if req.ImageEditRequest != nil {
		tmp := *req.ImageEditRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.ImageEditRequest = &tmp
	}
	if req.ImageVariationRequest != nil {
		tmp := *req.ImageVariationRequest
		tmp.Provider = fallback.Provider
		tmp.Model = fallback.Model
		fallbackReq.ImageVariationRequest = &tmp
	}

	if provider, model, _ := fallbackReq.GetRequestFields(); provider != fallback.Provider || model != fallback.Model {
		bifrost.logger.Warn("fallback %s/%s skipped: request type has no arm in prepareFallbackRequest (request still targets %s/%s)", fallback.Provider, fallback.Model, provider, model)
		return nil
	}
	return &fallbackReq
}

func (bifrost *Bifrost) shouldContinueWithFallbacks(fallback schemas.Fallback, fallbackErr *schemas.BifrostError) bool {
	if fallbackErr == nil {
		return false
	}

	if fallbackErr.Error != nil && fallbackErr.Error.Type != nil && *fallbackErr.Error.Type == schemas.RequestCancelled {
		return false
	}

	if fallbackErr.AllowFallbacks != nil && !*fallbackErr.AllowFallbacks {
		return false
	}

	bifrost.logger.Debug("Fallback provider %s failed: %s", fallback.Provider, fallbackErr.GetErrorString())
	return true
}

func populateLatencyExtraFields(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse) {
	if resp == nil {
		return
	}
	resp.PopulateUpstreamLatency(ctx)
	if start, ok := ctx.Value(schemas.BifrostContextKeyRequestStartTime).(time.Time); ok {
		resp.PopulateOverheadLatency(ctx, time.Since(start))
	}
}

func (bifrost *Bifrost) handleRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) {
	defer bifrost.releaseBifrostRequest(req)
	provider, model, fallbacks := req.GetRequestFields()

	if ctx == nil {
		ctx = bifrost.ctx
	}

	requested := schemas.Route{Provider: provider, Model: model}

	continuation := agentContinuationRoute(ctx)
	if continuation != nil {
		requested = requestedRouteFromContext(ctx)
	}

	_, directKey := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
	var served *schemas.Route
	servedFallback := false
	defer func() {
		bifrost.observeSessionOutcome(ctx, requested, served, servedFallback, directKey, bifrostErr)
	}()
	if continuation == nil {
		stampRequestedRoute(ctx, requested)
	}

	if continuation == nil {
		ctx.ResetUpstreamLatency()

		ctx.SetValue(schemas.BifrostContextKeyRequestStartTime, time.Now())
	}

	defer func() {
		ctx.StampUpstreamLatency()
		populateLatencyExtraFields(ctx, resp)
	}()

	if continuation != nil && continuation.Provider != "" && continuation.Model != "" {
		resp, bifrostErr = bifrost.continueOnServedRoute(ctx, req, continuation)
		if bifrostErr == nil {
			served = &schemas.Route{Provider: continuation.Provider, Model: continuation.Model}
			servedFallback = continuation.IsFallback
		}
		return resp, bifrostErr
	}

	ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 0)

	if _, ok := ctx.Value(schemas.BifrostContextKeyRequestID).(string); !ok {
		requestID := uuid.New().String()
		ctx.SetValue(schemas.BifrostContextKeyRequestID, requestID)
	}

	setupSpan := bifrost.startCoreSpan(ctx, "handle-setup")

	bifrost.setModelCatalogOnContext(ctx)

	preReqPipeline := bifrost.getPluginPipeline()
	preReqPipeline.RunPreRequestHooks(ctx, req)
	bifrost.releasePluginPipeline(preReqPipeline)

	bifrost.resolveSessionRoute(ctx, requested, req)
	bifrost.endCoreSpan(setupSpan)

	preDispatchSpan := bifrost.startCoreSpan(ctx, "miscellaneous")

	provider, model, fallbacks = req.GetRequestFields()

	validateErr := validateRequestAfterPreRequestHooks(req)
	bifrost.endCoreSpan(preDispatchSpan)
	if validateErr != nil {

		flushPluginLogs(ctx)
		validateErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, validateErr
	}

	bifrost.logger.Debug("primary provider %s with model %s and %d fallbacks", provider, model, len(fallbacks))

	primaryResult, primaryErr := bifrost.tryRequest(ctx, req)
	primaryHeaders, _ := ctx.Value(schemas.BifrostContextKeyProviderResponseHeaders).(map[string]string)
	if primaryErr != nil {

		bifrost.logger.Debug("primary provider %s with model %s returned error: %s", provider, model, primaryErr.GetErrorString())
		if len(fallbacks) > 0 {
			bifrost.logger.Debug("check if we should try %d fallbacks", len(fallbacks))
		}
	}

	shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
	if !shouldTryFallbacks {
		if primaryErr == nil {
			served = &schemas.Route{Provider: provider, Model: model}
		}
		return primaryResult, primaryErr
	}

	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCore)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Primary %s/%s failed (%s); evaluating %d configured fallback(s)", provider, model, routingErrorSummary(primaryErr), len(fallbacks)))

	lastErr := primaryErr

	for i, fallback := range fallbacks {
		ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, i+1)
		bifrost.logger.Debug("trying fallback provider %s with model %s", fallback.Provider, fallback.Model)
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Trying fallback %d/%d: %s/%s (previous attempt failed: %s)", i+1, len(fallbacks), fallback.Provider, fallback.Model, routingErrorSummary(lastErr)))
		ctx.SetValue(schemas.BifrostContextKeyFallbackRequestID, uuid.New().String())
		clearCtxForFallback(ctx)

		if keyID := strings.TrimSpace(fallback.KeyID); keyID != "" {
			ctx.SetFallbackPinnedAPIKeyID(keyID)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Fallback %d/%d pinned to provider key %s", i+1, len(fallbacks), keyID))
		}

		tracer := bifrost.getTracer()
		spanCtx, handle := tracer.StartSpan(ctx, fmt.Sprintf("fallback.%s.%s", fallback.Provider, fallback.Model), schemas.SpanKindFallback)
		tracer.SetAttribute(handle, schemas.AttrProviderName, schemas.OTelProviderName(fallback.Provider))
		tracer.SetAttribute(handle, schemas.AttrBifrostProviderName, string(fallback.Provider))
		tracer.SetAttribute(handle, schemas.AttrRequestModel, fallback.Model)
		tracer.SetAttribute(handle, "fallback.index", i+1)
		ctx.SetValue(schemas.BifrostContextKeySpanID, spanCtx.Value(schemas.BifrostContextKeySpanID))

		fallbackReq := bifrost.prepareFallbackRequest(req, fallback)
		if fallbackReq == nil {
			bifrost.logger.Debug("fallback provider %s with model %s is nil", fallback.Provider, fallback.Model)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelWarn, fmt.Sprintf("Fallback %s/%s skipped: missing provider config or unsupported request type", fallback.Provider, fallback.Model))
			tracer.SetAttribute(handle, "error", "fallback request preparation failed")
			tracer.EndSpan(handle, schemas.SpanStatusError, "fallback request preparation failed")
			continue
		}

		result, fallbackErr := bifrost.tryRequest(ctx, fallbackReq)

		result.SetFallbackRoutingInfo(provider, model)
		fallbackErr.SetFallbackRoutingInfo(provider, model)
		if fallbackErr == nil {
			bifrost.logger.Debug("successfully used fallback provider %s with model %s", fallback.Provider, fallback.Model)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Request served by fallback %s/%s (attempt %d/%d)", fallback.Provider, fallback.Model, i+1, len(fallbacks)))
			tracer.EndSpan(handle, schemas.SpanStatusOk, "")
			served = &schemas.Route{Provider: fallback.Provider, Model: fallback.Model}
			servedFallback = true
			return result, nil
		}

		if fallbackErr.Error != nil {
			tracer.SetAttribute(handle, "error", fallbackErr.Error.Message)
		}
		tracer.EndSpan(handle, schemas.SpanStatusError, "fallback failed")

		if !bifrost.shouldContinueWithFallbacks(fallback, fallbackErr) {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Fallback %s/%s failed (%s); halting further fallbacks", fallback.Provider, fallback.Model, routingErrorSummary(fallbackErr)))
			return nil, fallbackErr
		}

		lastErr = fallbackErr
	}

	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("All %d fallback(s) exhausted; returning primary error (%s)", len(fallbacks), routingErrorSummary(primaryErr)))

	restoreProviderResponseHeaders(ctx, primaryHeaders)
	return nil, primaryErr
}

func (bifrost *Bifrost) continueOnServedRoute(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, servedRoute *schemas.RoutingInfo) (*schemas.BifrostResponse, *schemas.BifrostError) {
	if !servedRoute.IsFallback {
		ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 0)
	} else {
		if fallbackIndex, _ := ctx.Value(schemas.BifrostContextKeyFallbackIndex).(int); fallbackIndex <= 0 {
			ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 1)
		}
		ctx.SetValue(schemas.BifrostContextKeyFallbackRequestID, uuid.New().String())
	}
	if _, ok := ctx.Value(schemas.BifrostContextKeyRequestID).(string); !ok {
		ctx.SetValue(schemas.BifrostContextKeyRequestID, uuid.New().String())
	}
	bifrost.setModelCatalogOnContext(ctx)
	req.SetProvider(servedRoute.Provider)
	req.SetModel(servedRoute.Model)
	req.SetFallbacks(nil)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("MCP agent continuation pinned to %s/%s, the route that served the first turn", servedRoute.Provider, servedRoute.Model))

	result, bifrostErr := bifrost.tryRequest(ctx, req)
	if servedRoute.IsFallback {
		var primaryProvider schemas.ModelProvider
		var primaryModel string
		if servedRoute.PrimaryProvider != nil {
			primaryProvider = *servedRoute.PrimaryProvider
		}
		if servedRoute.PrimaryModel != nil {
			primaryModel = *servedRoute.PrimaryModel
		}
		result.SetFallbackRoutingInfo(primaryProvider, primaryModel)
		bifrostErr.SetFallbackRoutingInfo(primaryProvider, primaryModel)
	}
	return result, bifrostErr
}

func (bifrost *Bifrost) handleStreamRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (stream chan *schemas.BifrostStreamChunk, bifrostErr *schemas.BifrostError) {
	defer bifrost.releaseBifrostRequest(req)
	provider, model, fallbacks := req.GetRequestFields()

	if ctx == nil {
		ctx = bifrost.ctx
	}

	requested := schemas.Route{Provider: provider, Model: model}

	_, directKey := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
	var served *schemas.Route
	servedFallback := false
	defer func() {
		bifrost.observeSessionOutcome(ctx, requested, served, servedFallback, directKey, bifrostErr)
	}()
	stampRequestedRoute(ctx, requested)

	ctx.ResetUpstreamLatency()
	ctx.ResetStreamOverhead()

	ctx.SetValue(schemas.BifrostContextKeyRequestStartTime, time.Now())

	ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, 0)

	if _, ok := ctx.Value(schemas.BifrostContextKeyRequestID).(string); !ok {
		requestID := uuid.New().String()
		ctx.SetValue(schemas.BifrostContextKeyRequestID, requestID)
	}

	bifrost.setModelCatalogOnContext(ctx)

	preReqPipeline := bifrost.getPluginPipeline()
	preReqPipeline.RunPreRequestHooks(ctx, req)
	bifrost.releasePluginPipeline(preReqPipeline)

	bifrost.resolveSessionRoute(ctx, requested, req)

	preDispatchSpan := bifrost.startCoreSpan(ctx, "miscellaneous")

	provider, model, fallbacks = req.GetRequestFields()

	validateErr := validateRequestAfterPreRequestHooks(req)
	bifrost.endCoreSpan(preDispatchSpan)
	if validateErr != nil {

		flushPluginLogs(ctx)
		validateErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, validateErr
	}

	bifrost.logger.Debug("primary provider %s with model %s and %d fallbacks", provider, model, len(fallbacks))

	firstTokenTimeout, _ := ctx.Value(schemas.BifrostContextKeyStreamFirstTokenTimeout).(time.Duration)
	attemptFirstTokenTimeout := func(hasLaterFallback bool) time.Duration {
		if !hasLaterFallback || firstTokenTimeout <= 0 {
			return 0
		}
		return firstTokenTimeout
	}

	primaryResult, primaryErr := bifrost.tryStreamRequest(ctx, req, attemptFirstTokenTimeout(len(fallbacks) > 0))
	primaryHeaders, _ := ctx.Value(schemas.BifrostContextKeyProviderResponseHeaders).(map[string]string)
	if primaryErr != nil {

		bifrost.logger.Debug("primary provider %s with model %s returned error: %s", provider, model, primaryErr.GetErrorString())
		if len(fallbacks) > 0 {
			bifrost.logger.Debug("check if we should try %d fallbacks", len(fallbacks))
		}
	}

	shouldTryFallbacks := bifrost.shouldTryFallbacks(ctx, req, primaryErr)
	if !shouldTryFallbacks {
		if primaryErr == nil {
			served = &schemas.Route{Provider: provider, Model: model}
		}
		return primaryResult, primaryErr
	}

	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCore)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Primary %s/%s failed (%s); evaluating %d configured fallback(s)", provider, model, routingErrorSummary(primaryErr), len(fallbacks)))

	lastErr := primaryErr

	for i, fallback := range fallbacks {
		ctx.SetValue(schemas.BifrostContextKeyFallbackIndex, i+1)
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Trying fallback %d/%d: %s/%s (previous attempt failed: %s)", i+1, len(fallbacks), fallback.Provider, fallback.Model, routingErrorSummary(lastErr)))
		ctx.SetValue(schemas.BifrostContextKeyFallbackRequestID, uuid.New().String())
		clearCtxForFallback(ctx)

		if keyID := strings.TrimSpace(fallback.KeyID); keyID != "" {
			ctx.SetFallbackPinnedAPIKeyID(keyID)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Fallback %d/%d pinned to provider key %s", i+1, len(fallbacks), keyID))
		}

		tracer := bifrost.getTracer()
		spanCtx, handle := tracer.StartSpan(ctx, fmt.Sprintf("fallback.%s.%s", fallback.Provider, fallback.Model), schemas.SpanKindFallback)
		tracer.SetAttribute(handle, schemas.AttrProviderName, schemas.OTelProviderName(fallback.Provider))
		tracer.SetAttribute(handle, schemas.AttrBifrostProviderName, string(fallback.Provider))
		tracer.SetAttribute(handle, schemas.AttrRequestModel, fallback.Model)
		tracer.SetAttribute(handle, "fallback.index", i+1)
		ctx.SetValue(schemas.BifrostContextKeySpanID, spanCtx.Value(schemas.BifrostContextKeySpanID))

		fallbackReq := bifrost.prepareFallbackRequest(req, fallback)
		if fallbackReq == nil {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelWarn, fmt.Sprintf("Fallback %s/%s skipped: missing provider config or unsupported request type", fallback.Provider, fallback.Model))
			tracer.SetAttribute(handle, "error", "fallback request preparation failed")
			tracer.EndSpan(handle, schemas.SpanStatusError, "fallback request preparation failed")
			continue
		}

		result, fallbackErr := bifrost.tryStreamRequest(ctx, fallbackReq, attemptFirstTokenTimeout(i < len(fallbacks)-1))

		fallbackErr.SetFallbackRoutingInfo(provider, model)
		if fallbackErr == nil {

			if ri, ok := ctx.Value(schemas.BifrostContextKeyRoutingInfo).(schemas.RoutingInfo); ok {
				ri.IsFallback = true
				if provider != "" {
					ri.PrimaryProvider = Ptr(provider)
				}
				if model != "" {
					ri.PrimaryModel = Ptr(model)
				}
				ctx.SetRoutingInfoSnapshot(ri)
			}
			bifrost.logger.Debug("successfully used fallback provider %s with model %s", fallback.Provider, fallback.Model)
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Request served by fallback %s/%s (attempt %d/%d)", fallback.Provider, fallback.Model, i+1, len(fallbacks)))
			tracer.EndSpan(handle, schemas.SpanStatusOk, "")
			served = &schemas.Route{Provider: fallback.Provider, Model: fallback.Model}
			servedFallback = true
			return result, nil
		}

		if fallbackErr.Error != nil {
			tracer.SetAttribute(handle, "error", fallbackErr.Error.Message)
		}
		tracer.EndSpan(handle, schemas.SpanStatusError, "fallback failed")

		if !bifrost.shouldContinueWithFallbacks(fallback, fallbackErr) {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Fallback %s/%s failed (%s); halting further fallbacks", fallback.Provider, fallback.Model, routingErrorSummary(fallbackErr)))
			return nil, fallbackErr
		}

		lastErr = fallbackErr
	}

	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("All %d fallback(s) exhausted; returning primary error (%s)", len(fallbacks), routingErrorSummary(primaryErr)))

	restoreProviderResponseHeaders(ctx, primaryHeaders)
	return nil, primaryErr
}

func (bifrost *Bifrost) tryRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostResponse, *schemas.BifrostError) {

	ctx.MarkAttemptStart()
	provider, model, _ := req.GetRequestFields()
	pq, err := bifrost.getProviderQueue(provider)
	if err != nil {
		bifrostErr := newBifrostError(err)
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	tracer := bifrost.getTracer()
	if tracer == nil {
		bifrostErr := newBifrostErrorFromMsg("tracer not found in context")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	ctx.SetValue(schemas.BifrostContextKeyTracer, tracer)

	prePipeSpan := bifrost.startCoreSpan(ctx, "pipeline-pre")
	pipeline := bifrost.getPluginPipeline()
	defer bifrost.releasePluginPipeline(pipeline)

	preReq, shortCircuit, preCount := pipeline.RunLLMPreHooks(ctx, req)
	bifrost.endCoreSpan(prePipeSpan)
	if shortCircuit != nil {

		if shortCircuit.Response != nil {
			shortCircuit.Response.PopulateExtraFields(req.RequestType, provider, model, model)

			populateLatencyExtraFields(ctx, shortCircuit.Response)
			resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, shortCircuit.Response, nil, preCount)
			if bifrostErr != nil {
				bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			} else if resp != nil {
				resp.PopulateExtraFields(req.RequestType, provider, model, model)
			}
			drainAndAttachPluginLogs(ctx)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			return resp, nil
		}

		if shortCircuit.Error != nil {
			shortCircuit.Error.PopulateExtraFields(req.RequestType, provider, model, model)
			resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, nil, shortCircuit.Error, preCount)
			if bifrostErr != nil {
				bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			} else if resp != nil {
				resp.PopulateExtraFields(req.RequestType, provider, model, model)
			}
			drainAndAttachPluginLogs(ctx)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			return resp, nil
		}
	}
	if preReq == nil {
		bifrostErr := newBifrostErrorFromMsg("bifrost request after plugin hooks cannot be nil")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	preEnqueueSpan := bifrost.startCoreSpan(ctx, "miscellaneous")
	provider, model, _ = preReq.GetRequestFields()

	msg := bifrost.getChannelMessage(*preReq)
	msg.Context = ctx
	bifrost.endCoreSpan(preEnqueueSpan)

	startQueueWaitSpan(ctx, tracer, msg)

	if pq.isClosing() {
		var reroutedPq *ProviderQueue
		if val, ok := bifrost.requestQueues.Load(provider); ok {
			if candidate := val.(*ProviderQueue); candidate != pq && !candidate.isClosing() {
				reroutedPq = candidate
			}
		}
		if reroutedPq == nil {
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
		pq = reroutedPq
	}

	select {
	case pq.queue <- msg:

	case <-pq.done:
		bifrost.releaseChannelMessage(msg)
		bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	case <-ctx.Done():
		bifrost.releaseChannelMessage(msg)
		bifrostErr := newBifrostCtxDoneError(ctx, "while waiting for queue space")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	default:
		if bifrost.dropExcessRequests.Load() {
			bifrost.releaseChannelMessage(msg)
			bifrost.logger.Warn("request dropped: queue is full, please increase the queue size or set dropExcessRequests to false")
			bifrostErr := newBifrostQueueFullError()
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}

		if pq.isClosing() {
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
		select {
		case pq.queue <- msg:

		case <-pq.done:
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		case <-ctx.Done():
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostCtxDoneError(ctx, "while waiting for queue space")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
	}

	var result *schemas.BifrostResponse
	var resp *schemas.BifrostResponse
	pluginCount := len(*bifrost.llmPlugins.Load())

	onResult := func(result *schemas.BifrostResponse) (*schemas.BifrostResponse, *schemas.BifrostError) {

		if !msg.sentAt.IsZero() {
			msg.Context.StampWorkerHandoff(time.Since(msg.sentAt))
		}

		if ph := bifrost.startCoreSpan(msg.Context, "pipeline-post"); ph.h != nil {
			defer bifrost.endCoreSpan(ph)
		}

		populateLatencyExtraFields(msg.Context, result)
		resp, bifrostErr := pipeline.RunPostLLMHooks(msg.Context, result, nil, pluginCount)
		if bifrostErr != nil {
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		} else if resp != nil {
			resp.PopulateExtraFields(req.RequestType, provider, model, model)
		}
		drainAndAttachPluginLogs(msg.Context)
		if bifrostErr != nil {
			bifrost.releaseChannelMessage(msg)
			return nil, bifrostErr
		}
		bifrost.releaseChannelMessage(msg)

		if resp != nil {
			dropReq, _ := ctx.Value(schemas.BifrostContextKeyDropRawRequestFromClient).(bool)
			dropResp, _ := ctx.Value(schemas.BifrostContextKeyDropRawResponseFromClient).(bool)
			if dropReq || dropResp {
				extraField := resp.GetExtraFields()
				if dropReq {
					extraField.RawRequest = nil
				}
				if dropResp {
					extraField.RawResponse = nil
				}
			}
		}
		return resp, nil
	}
	onError := func(bifrostErrVal schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		bifrostErrPtr := &bifrostErrVal

		if !msg.sentAt.IsZero() {
			msg.Context.StampWorkerHandoff(time.Since(msg.sentAt))
		}

		if ph := bifrost.startCoreSpan(msg.Context, "pipeline-post"); ph.h != nil {
			defer bifrost.endCoreSpan(ph)
		}
		resp, bifrostErrPtr = pipeline.RunPostLLMHooks(msg.Context, nil, bifrostErrPtr, pluginCount)
		if bifrostErrPtr != nil {
			bifrostErrPtr.PopulateExtraFields(req.RequestType, provider, model, model)
		} else if resp != nil {
			resp.PopulateExtraFields(req.RequestType, provider, model, model)
		}
		drainAndAttachPluginLogs(msg.Context)
		bifrost.releaseChannelMessage(msg)

		dropReq, _ := ctx.Value(schemas.BifrostContextKeyDropRawRequestFromClient).(bool)
		dropResp, _ := ctx.Value(schemas.BifrostContextKeyDropRawResponseFromClient).(bool)
		if dropReq || dropResp {
			if bifrostErrPtr != nil {
				if dropReq {
					bifrostErrPtr.ExtraFields.RawRequest = nil
				}
				if dropResp {
					bifrostErrPtr.ExtraFields.RawResponse = nil
				}
			}
			if resp != nil {
				extraField := resp.GetExtraFields()
				if dropReq {
					extraField.RawRequest = nil
				}
				if dropResp {
					extraField.RawResponse = nil
				}
			}
		}
		if bifrostErrPtr != nil {
			return nil, bifrostErrPtr
		}
		return resp, nil
	}
	select {
	case result = <-msg.Response:
		return onResult(result)
	case bifrostErrVal := <-msg.Err:
		return onError(bifrostErrVal)
	case <-ctx.Done():
		if !msg.abandonDelivery() {

			select {
			case result = <-msg.Response:
				return onResult(result)
			case bifrostErrVal := <-msg.Err:
				return onError(bifrostErrVal)
			}
		}

		provider, model, _ := req.GetRequestFields()
		bifrostErr := newBifrostCtxDoneError(ctx, "waiting for provider response")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}
}

func (bifrost *Bifrost) tryStreamRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, firstTokenTimeout time.Duration) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

	ctx.MarkAttemptStart()
	ctx.MarkStreamAttemptStart()
	provider, model, _ := req.GetRequestFields()
	pq, err := bifrost.getProviderQueue(provider)
	if err != nil {
		bifrostErr := newBifrostError(err)
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	tracer := bifrost.getTracer()
	if tracer == nil {
		bifrostErr := newBifrostErrorFromMsg("tracer not found in context")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	ctx.SetValue(schemas.BifrostContextKeyTracer, tracer)

	if _, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); !ok {
		traceID := tracer.CreateTrace("")
		if traceID != "" {
			ctx.SetValue(schemas.BifrostContextKeyTraceID, traceID)

			ctx.SetValue(schemas.BifrostContextKeyExportTraceID, traceID)
		}
	}

	pipeline := bifrost.getPluginPipeline()
	releasePipeline := true
	defer func() {
		if releasePipeline {
			bifrost.releasePluginPipeline(pipeline)
		}
	}()

	prePipeSpan := bifrost.startCoreSpan(ctx, "pipeline-pre")

	preReq, shortCircuit, preCount := pipeline.RunLLMPreHooks(ctx, req)
	bifrost.endCoreSpan(prePipeSpan)
	if shortCircuit != nil {

		if shortCircuit.Response != nil || shortCircuit.Stream != nil {
			if fallbackIndex, _ := ctx.Value(schemas.BifrostContextKeyFallbackIndex).(int); fallbackIndex > 0 {
				answered := schemas.RoutingInfo{Provider: provider, Model: model}
				if preReq != nil {
					answered.Provider, answered.Model, _ = preReq.GetRequestFields()
				}
				answered.ApplyRequestRouting(ctx)
				layerFallbackIdentity(ctx, &answered)
				ctx.SetRoutingInfoSnapshot(answered)
			}
		}

		if shortCircuit.Response != nil {
			shortCircuit.Response.PopulateExtraFields(req.RequestType, provider, model, model)

			populateLatencyExtraFields(ctx, shortCircuit.Response)
			resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, shortCircuit.Response, nil, preCount)
			if bifrostErr != nil {
				bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			} else if resp != nil {
				resp.PopulateExtraFields(req.RequestType, provider, model, model)
			}
			drainAndAttachPluginLogs(ctx)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			return newBifrostMessageChan(resp), nil
		}

		if shortCircuit.Stream != nil {
			outputStream := make(chan *schemas.BifrostStreamChunk)
			releasePipeline = false

			shortCircuitRequestType := req.RequestType

			pipelinePostHookRunner := func(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
				if result != nil {
					result.PopulateExtraFields(shortCircuitRequestType, provider, model, model)
				}
				if err != nil {
					err.PopulateExtraFields(shortCircuitRequestType, provider, model, model)
				}
				resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, result, err, preCount)
				if IsFinalChunk(ctx) {
					drainAndAttachPluginLogs(ctx)
				}
				if bifrostErr != nil {
					bifrostErr.PopulateExtraFields(shortCircuitRequestType, provider, model, model)
					return nil, bifrostErr
				} else if resp != nil {
					resp.PopulateExtraFields(shortCircuitRequestType, provider, model, model)
				}
				return resp, nil
			}

			go func() {
				defer func() {
					drainAndAttachPluginLogs(ctx)
					pipeline.FinalizeStreamingPostHookSpans(ctx)
					bifrost.releasePluginPipeline(pipeline)
				}()
				defer providerUtils.CloseStream(ctx, outputStream)

				for streamMsg := range shortCircuit.Stream {
					if streamMsg == nil {
						continue
					}

					bifrostResponse := &schemas.BifrostResponse{}
					if streamMsg.BifrostTextCompletionResponse != nil {
						bifrostResponse.TextCompletionResponse = streamMsg.BifrostTextCompletionResponse
					}
					if streamMsg.BifrostChatResponse != nil {
						bifrostResponse.ChatResponse = streamMsg.BifrostChatResponse
					}
					if streamMsg.BifrostResponsesStreamResponse != nil {
						bifrostResponse.ResponsesStreamResponse = streamMsg.BifrostResponsesStreamResponse
					}
					if streamMsg.BifrostSpeechStreamResponse != nil {
						bifrostResponse.SpeechStreamResponse = streamMsg.BifrostSpeechStreamResponse
					}
					if streamMsg.BifrostTranscriptionStreamResponse != nil {
						bifrostResponse.TranscriptionStreamResponse = streamMsg.BifrostTranscriptionStreamResponse
					}
					if streamMsg.BifrostImageGenerationStreamResponse != nil {
						bifrostResponse.ImageGenerationStreamResponse = streamMsg.BifrostImageGenerationStreamResponse
					}

					processedResponse, processedError := pipelinePostHookRunner(ctx, bifrostResponse, streamMsg.BifrostError)

					streamResponse := providerUtils.BuildClientStreamChunk(ctx, processedResponse, processedError)

					if !providerUtils.GateSendChunk(ctx, streamResponse, outputStream) {
						for range shortCircuit.Stream {
						}
						return
					}

				}
			}()

			return outputStream, nil
		}

		if shortCircuit.Error != nil {
			shortCircuit.Error.PopulateExtraFields(req.RequestType, provider, model, model)
			resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, nil, shortCircuit.Error, preCount)
			if bifrostErr != nil {
				bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			} else if resp != nil {
				resp.PopulateExtraFields(req.RequestType, provider, model, model)
			}
			drainAndAttachPluginLogs(ctx)
			if bifrostErr != nil {
				return nil, bifrostErr
			}
			return newBifrostMessageChan(resp), nil
		}
	}
	if preReq == nil {
		bifrostErr := newBifrostErrorFromMsg("bifrost request after plugin hooks cannot be nil")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	}

	preEnqueueSpan := bifrost.startCoreSpan(ctx, "miscellaneous")
	provider, model, _ = preReq.GetRequestFields()

	msg := bifrost.getChannelMessage(*preReq)
	msg.Context = ctx
	msg.firstTokenTimeout = firstTokenTimeout
	bifrost.endCoreSpan(preEnqueueSpan)

	startQueueWaitSpan(ctx, tracer, msg)

	if pq.isClosing() {
		var reroutedPq *ProviderQueue
		if val, ok := bifrost.requestQueues.Load(provider); ok {
			if candidate := val.(*ProviderQueue); candidate != pq && !candidate.isClosing() {
				reroutedPq = candidate
			}
		}
		if reroutedPq == nil {
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
		pq = reroutedPq
	}

	select {
	case pq.queue <- msg:

	case <-pq.done:
		bifrost.releaseChannelMessage(msg)
		bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	case <-ctx.Done():
		bifrost.releaseChannelMessage(msg)
		bifrostErr := newBifrostCtxDoneError(ctx, "while waiting for queue space")
		bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
		return nil, bifrostErr
	default:
		if bifrost.dropExcessRequests.Load() {
			bifrost.releaseChannelMessage(msg)
			bifrost.logger.Warn("request dropped: queue is full, please increase the queue size or set dropExcessRequests to false")
			bifrostErr := newBifrostQueueFullError()
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}

		if pq.isClosing() {
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
		select {
		case pq.queue <- msg:

		case <-pq.done:
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostErrorFromMsg("provider is shutting down")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		case <-ctx.Done():
			bifrost.releaseChannelMessage(msg)
			bifrostErr := newBifrostCtxDoneError(ctx, "while waiting for queue space")
			bifrostErr.PopulateExtraFields(req.RequestType, provider, model, model)
			return nil, bifrostErr
		}
	}

	select {
	case stream := <-msg.ResponseStream:
		bifrost.releaseChannelMessage(msg)
		return stream, nil
	case bifrostErrVal := <-msg.Err:
		if bifrostErrVal.Error != nil {
			bifrost.logger.Debug("error while executing stream request: %s", bifrostErrVal.Error.Message)
		} else {
			bifrost.logger.Debug("error while executing stream request: %+v", bifrostErrVal)
		}
		if msg.streamPostHooksRan {
			bifrostErrVal.PopulateExtraFields(req.RequestType, provider, model, model)
			bifrost.releaseChannelMessage(msg)
			return nil, &bifrostErrVal
		}

		ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)

		recoveredResp, recoveredErr := pipeline.RunPostLLMHooks(ctx, nil, &bifrostErrVal, len(*bifrost.llmPlugins.Load()))
		if recoveredErr != nil {
			recoveredErr.PopulateExtraFields(req.RequestType, provider, model, model)
		} else if recoveredResp != nil {
			recoveredResp.PopulateExtraFields(req.RequestType, provider, model, model)
		}
		drainAndAttachPluginLogs(ctx)
		bifrost.releaseChannelMessage(msg)
		if recoveredErr != nil {
			return nil, recoveredErr
		}
		if recoveredResp != nil {
			return newBifrostMessageChan(recoveredResp), nil
		}
		return nil, &bifrostErrVal
	case <-ctx.Done():

		return nil, newBifrostCtxDoneError(ctx, "while waiting for stream response")
	}
}

func (bifrost *Bifrost) pickFromRotatingPool(ctx *schemas.BifrostContext, pool []schemas.Key, provider schemas.ModelProvider, model string, usedKeyIDs, deadKeyIDs map[string]bool) (schemas.Key, error) {
	available := make([]schemas.Key, 0, len(pool))
	for _, k := range pool {
		if deadKeyIDs[k.ID] || usedKeyIDs[k.ID] {
			continue
		}
		available = append(available, k)
	}
	if bifrost.keyPoolFilter != nil {
		if filtered, err := bifrost.keyPoolFilter(ctx, provider, model, available); err != nil {
			bifrost.logger.Warn("key pool filter failed for provider %s, using unfiltered keys: %v", provider, err)
		} else {
			available = filtered
		}
	}
	if len(available) == 0 {

		for _, k := range pool {
			if !deadKeyIDs[k.ID] {
				available = append(available, k)
			}
		}
		liveCount := len(available)
		if bifrost.keyPoolFilter != nil {
			if filtered, err := bifrost.keyPoolFilter(ctx, provider, model, available); err != nil {
				bifrost.logger.Warn("key pool filter failed for provider %s, using unfiltered keys: %v", provider, err)
			} else {
				available = filtered
			}
		}
		if len(available) == 0 {
			if liveCount > 0 {
				return schemas.Key{}, fmt.Errorf("%w: provider %s", errAllKeysFiltered, provider)
			}
			return schemas.Key{}, fmt.Errorf("%w: provider %s", errAllKeysDead, provider)
		}
		for id := range usedKeyIDs {
			delete(usedKeyIDs, id)
		}
	}
	return bifrost.keySelector(ctx, available, provider, model)
}

func settleAttemptAbort(ctx *schemas.BifrostContext, abort *providerUtils.AttemptAbort, bifrostError *schemas.BifrostError, providerKey schemas.ModelProvider, model string) *schemas.BifrostError {
	abort.Disarm()
	ctx.ClearValue(schemas.BifrostContextKeyStreamAttemptAbort)
	if bifrostError == nil || !abort.Fired() {
		return bifrostError
	}
	if !isFirstTokenTimeoutError(bifrostError) {
		bifrostError = providerUtils.NewFirstTokenTimeoutError(abort.Timeout())
	}
	if trail, ok := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord); ok && len(trail) > 0 {
		reason := schemas.FirstTokenTimeoutErrorCode
		trail[len(trail)-1].FailReason = &reason
		ctx.SetValue(schemas.BifrostContextKeyAttemptTrail, trail)
	}
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelWarn, fmt.Sprintf("TTFT timeout: %s/%s produced no first token within %s; cutting off the attempt", providerKey, model, abort.Timeout()))
	return bifrostError
}

func isFirstTokenTimeoutError(err *schemas.BifrostError) bool {
	return err != nil && err.Error != nil && err.Error.Code != nil && *err.Error.Code == schemas.FirstTokenTimeoutErrorCode
}

func stampEndedAttempt(ctx *schemas.BifrostContext, bifrostError *schemas.BifrostError) {
	class := ClassifyFailure(bifrostError)
	status := 0
	switch class {
	case schemas.FailureClassTimeout:
		status = 504
	case schemas.FailureClassCancelled:
		status = 499
	default:
		return
	}
	trail, ok := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord)
	if !ok || len(trail) == 0 {
		return
	}
	last := &trail[len(trail)-1]
	if last.FailureClass != "" {
		return
	}
	last.FailureClass = class
	if bifrostError.StatusCode != nil {
		status = *bifrostError.StatusCode
	}
	last.StatusCode = &status
	if class == schemas.FailureClassTimeout && last.FailReason == nil {
		reason := schemas.RequestTimedOut
		last.FailReason = &reason
	}
	ctx.SetValue(schemas.BifrostContextKeyAttemptTrail, trail)
}

func stampEndedStreamAttempt(ctx *schemas.BifrostContext, bifrostError *schemas.BifrostError) {
	if ctx.Value(schemas.BifrostContextKeyStreamAttemptAbort) != nil {
		return
	}
	stampEndedAttempt(ctx, bifrostError)
}

func readyHeldStreamCommit(ctx *schemas.BifrostContext) {
	if gated, _ := ctx.Value(schemas.BifrostContextKeyStreamGated).(bool); gated {
		ctx.ReadyStreamCommitWatch()
	}
}

func executeRequestWithRetries[T any](
	ctx *schemas.BifrostContext,
	config *schemas.ProviderConfig,
	requestHandler func(key schemas.Key) (T, *schemas.BifrostError),
	keyProvider func(usedKeyIDs, deadKeyIDs map[string]bool) (schemas.Key, error),
	requestType schemas.RequestType,
	providerKey schemas.ModelProvider,
	model string,
	req *schemas.BifrostRequest,
	logger schemas.Logger,
) (result T, bifrostError *schemas.BifrostError) {
	var attempts int
	isStreamRequest := IsStreamRequestType(requestType)

	defer func() {
		if attempts <= 0 {
			return
		}
		schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCore)
		switch {
		case bifrostError == nil:
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Request to %s/%s succeeded after %d retry attempt(s)", providerKey, model, attempts))

		case bifrostError.Error != nil && bifrostError.Error.Type != nil && *bifrostError.Error.Type == schemas.RequestCancelled:
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Request to %s/%s cancelled after %d attempt(s)", providerKey, model, attempts))
		case bifrostError.Error != nil && bifrostError.Error.Type != nil && *bifrostError.Error.Type == schemas.RequestTimedOut:
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Request to %s/%s timed out after %d attempt(s)", providerKey, model, attempts))
		case bifrostError.IsBifrostError:
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Retries halted for %s/%s after %d attempt(s): internal Bifrost error (%s)", providerKey, model, attempts, routingErrorSummary(bifrostError)))
		default:
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelError, fmt.Sprintf("Retries exhausted for %s/%s after %d attempt(s); last error: %s", providerKey, model, attempts, routingErrorSummary(bifrostError)))
		}
	}()

	var currentKey schemas.Key
	var usedKeyIDs map[string]bool
	var deadKeyIDs map[string]bool

	var deadKeyClasses map[string]schemas.FailureClass
	var lastModelOrRegionError *schemas.BifrostError

	permanentGrants := 0
	lastWasPerKeyFailure := false

	lastWasPermanentKeyFailure := false

	previousKeyID := ""

	pendingRotationAttemptIdx := -1

	extraAttempts := 0

	strippedEncryptedContent := false

	lastWasEncryptedContentStrip := false

	for attempts = 0; attempts <= config.NetworkConfig.MaxRetries+extraAttempts; attempts++ {
		ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, attempts)

		if keyProvider != nil && attempts == 0 {
			ctx.SetValue(schemas.BifrostContextKeyAttemptTrail, []schemas.KeyAttemptRecord{})
		}

		if keyProvider != nil && (attempts == 0 || lastWasPerKeyFailure) {
			if usedKeyIDs == nil {
				usedKeyIDs = make(map[string]bool)
			}

			keyTracer, _ := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
			var keySpanCtx context.Context
			var keyHandle schemas.SpanHandle
			if keyTracer != nil {
				keySpanCtx, keyHandle = keyTracer.StartSpan(ctx, "key.selection", schemas.SpanKindInternal)
				keyTracer.SetAttribute(keyHandle, schemas.AttrProviderName, schemas.OTelProviderName(providerKey))
				keyTracer.SetAttribute(keyHandle, schemas.AttrBifrostProviderName, string(providerKey))
				keyTracer.SetAttribute(keyHandle, schemas.AttrRequestModel, model)
				if attempts > 0 {
					keyTracer.SetAttribute(keyHandle, schemas.AttrBifrostRetries, attempts)
				}
			}

			selectedKey, err := keyProvider(usedKeyIDs, deadKeyIDs)

			if keyTracer != nil {
				if err != nil {
					keyTracer.SetAttribute(keyHandle, "error", err.Error())
					keyTracer.EndSpan(keyHandle, schemas.SpanStatusError, err.Error())
				} else {
					keyTracer.SetAttribute(keyHandle, "key.id", selectedKey.ID)
					keyTracer.SetAttribute(keyHandle, "key.name", selectedKey.Name)
					keyTracer.EndSpan(keyHandle, schemas.SpanStatusOk, "")

					ctx.SetValue(schemas.BifrostContextKeySpanID, keySpanCtx.Value(schemas.BifrostContextKeySpanID))
				}
			}

			if err != nil {
				var zero T

				ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, "")
				ctx.SetValue(schemas.BifrostContextKeySelectedKeyName, "")

				ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, max(attempts-1, 0))

				grantedAttempt := attempts > config.NetworkConfig.MaxRetries+extraAttempts-permanentGrants

				if errors.Is(err, errAllKeysFiltered) {

					if lastModelOrRegionError != nil {
						return zero, lastModelOrRegionError
					}
					if grantedAttempt && bifrostError != nil {
						return zero, bifrostError
					}
					statusCode := 503
					errType := "no_eligible_keys"
					return zero, &schemas.BifrostError{
						IsBifrostError: false,
						StatusCode:     &statusCode,
						Type:           &errType,
						Error: &schemas.ErrorField{
							Type:    &errType,
							Message: err.Error(),
						},
					}
				}
				if errors.Is(err, errAllKeysDead) {

					if lastModelOrRegionError != nil {
						return zero, lastModelOrRegionError
					}
					_, callerKey := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
					if (grantedAttempt || callerKey) && bifrostError != nil {
						return zero, bifrostError
					}
					statusCode := 502
					errType := "upstream_credentials_exhausted"
					return zero, &schemas.BifrostError{
						IsBifrostError: false,
						StatusCode:     &statusCode,
						Type:           &errType,
						Error: &schemas.ErrorField{
							Type:    &errType,
							Message: err.Error(),
						},
						ExtraFields: schemas.BifrostErrorExtraFields{
							ErrorType: schemas.ErrorTypeProviderCredentialsExhausted,
						},
					}
				}
				statusCode := 500
				return zero, &schemas.BifrostError{
					IsBifrostError: true,
					StatusCode:     &statusCode,
					Error: &schemas.ErrorField{
						Message: err.Error(),
					},
					ExtraFields: schemas.BifrostErrorExtraFields{
						ErrorType: schemas.ErrorTypeBifrostInternal,
					},
				}
			}
			currentKey = selectedKey
			ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, currentKey.ID)
			ctx.SetValue(schemas.BifrostContextKeySelectedKeyName, currentKey.Name)
		}

		if attempts > 0 {

			var retryMsg string
			if bifrostError != nil && bifrostError.Error != nil {
				retryMsg = bifrostError.Error.Message
			} else if bifrostError != nil && bifrostError.StatusCode != nil {
				retryMsg = fmt.Sprintf("status=%d", *bifrostError.StatusCode)
				if bifrostError.Type != nil {
					retryMsg += ", type=" + *bifrostError.Type
				}
			}

			logger.Debug("retrying request (attempt %d/%d) for model %s: %s", attempts, config.NetworkConfig.MaxRetries+extraAttempts, model, retryMsg)

			keyChanged := keyProvider != nil && currentKey.ID != previousKeyID

			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineCore)

			keyNote := ""
			if keyProvider != nil {
				rotationNote := "same key"
				if keyChanged {
					rotationNote = "rotated key"
				}
				keyNote = fmt.Sprintf("; %s=%s", rotationNote, currentKey.Name)
			}
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelInfo, fmt.Sprintf("Retry %d/%d for %s/%s (previous attempt failed: %s%s)", attempts, config.NetworkConfig.MaxRetries+extraAttempts, providerKey, model, routingErrorSummary(bifrostError), keyNote))

			ctxEnded := false
			stage := "before retry dispatch"
			if !((lastWasPermanentKeyFailure && keyChanged) || lastWasEncryptedContentStrip) {
				backoff := calculateBackoff(attempts-1, config)
				logger.Debug("sleeping for %s before retry", backoff)
				ctxEnded = waitRetryBackoff(ctx, backoff)
				stage = "during retry backoff"
			} else {
				ctxEnded = ctx.Err() != nil
			}
			if ctxEnded {

				ctx.SetValue(schemas.BifrostContextKeyNumberOfRetries, attempts-1)
				bifrostError = newBifrostCtxDoneError(ctx, stage)
				break
			}
		}

		if keyProvider != nil {

			if pendingRotationAttemptIdx >= 0 {
				if trail, ok := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord); ok &&
					pendingRotationAttemptIdx < len(trail) &&
					trail[pendingRotationAttemptIdx].KeyID != currentKey.ID {
					trail[pendingRotationAttemptIdx].TriggeredRotation = true
					ctx.SetValue(schemas.BifrostContextKeyAttemptTrail, trail)
				}
				pendingRotationAttemptIdx = -1
			}
			schemas.AppendToContextList(ctx, schemas.BifrostContextKeyAttemptTrail, schemas.KeyAttemptRecord{
				Attempt: attempts,
				KeyID:   currentKey.ID,
				KeyName: currentKey.Name,
			})
		}

		logger.Debug("attempting %s request for provider %s", requestType, providerKey)

		tracer, ok := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
		if !ok || tracer == nil {
			logger.Error("tracer not found in context of executeRequestWithRetries")
			return result, newBifrostErrorFromMsg("tracer not found in context")
		}
		var spanName string
		var spanKind schemas.SpanKind
		otelOp := schemas.OTelOperationName(requestType)
		if attempts > 0 {
			spanName = fmt.Sprintf("retry.attempt.%d", attempts)
			spanKind = schemas.SpanKindRetry
		} else {

			spanName = fmt.Sprintf("%s %s", otelOp, model)
			spanKind = schemas.SpanKindLLMCall
		}
		spanID, handle := tracer.StartSpanID(ctx, spanName, spanKind)

		span := tracer.SpanFromHandle(handle)
		span.SetAttribute(schemas.AttrProviderName, schemas.OTelProviderName(providerKey))
		span.SetAttribute(schemas.AttrBifrostProviderName, string(providerKey))
		span.SetAttribute(schemas.AttrRequestModel, model)
		span.SetAttribute(schemas.AttrOperationName, otelOp)
		span.SetAttribute(schemas.AttrLegacyRequestType, string(requestType))

		applyContextSpanAttributes(span, ctx)
		span.SetRetries(attempts)

		exportHeaderAttributes := func(headers map[string][]string) {
			for name, values := range headers {
				if value, export := extraHeaderSpanAttribute(name, values); export {

					span.SetAttribute(schemas.AttrExtraHeaderPrefix+name, value)
				}
			}
		}
		if extraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string); ok {
			exportHeaderAttributes(extraHeaders)
		}

		if passthrough, ok := ctx.Value(schemas.BifrostContextKeyPassthroughHeaders).(map[string][]string); ok {
			exportHeaderAttributes(passthrough)
		}

		if spanID != "" {
			ctx.SetValue(schemas.BifrostContextKeySpanID, spanID)
		}

		if req != nil {
			_, attrHandle := tracer.StartSpanID(ctx, "attribute-population", schemas.SpanKindInternal)
			tracer.PopulateLLMRequestAttributes(handle, req)
			tracer.EndSpan(attrHandle, schemas.SpanStatusOk, "")
		}

		if IsStreamRequestType(requestType) {
			streamStartTime := time.Now()
			ctx.SetValue(schemas.BifrostContextKeyStreamStartTime, streamStartTime)
		}

		if isStreamRequest && attempts > 0 {
			ctx.ClearValue(schemas.BifrostContextKeyStreamEndIndicator)
			ctx.ClearValue(schemas.BifrostContextKeyStreamBodyExhausted)
			ctx.ClearValue(schemas.BifrostContextKeyStreamParkedAfterFinish)
			if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
				tracer.ResetStreamAccumulator(traceID)
			}
		}

		if isStreamRequest && handle != nil {
			if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
				tracer.StoreDeferredSpan(traceID, handle)
			}
		}

		result, bifrostError = requestHandler(currentKey)

		attemptAbort := providerUtils.AttemptAbortFromContext(ctx)

		emptyStream := false
		if bifrostError == nil {
			if streamChan, ok := any(result).(chan *schemas.BifrostStreamChunk); ok {
				var (
					checkedStream chan *schemas.BifrostStreamChunk
					drainDone     <-chan struct{}
					firstChunkErr *schemas.BifrostError
				)

				checkedStream, drainDone, firstChunkErr = providerUtils.CheckFirstStreamChunkForError(ctx, streamChan)

				if firstChunkErr != nil {
					<-drainDone

					ctx.ClearValue(schemas.BifrostContextKeyConnectionClosed)
					bifrostError = firstChunkErr
				} else if checkedStream == nil {

					emptyStream = true
					closedCh := make(chan *schemas.BifrostStreamChunk)
					close(closedCh)
					result = any(closedCh).(T)
				} else {
					result = any(checkedStream).(T)
				}
			}
		}
		if attemptAbort != nil {
			bifrostError = settleAttemptAbort(ctx, attemptAbort, bifrostError, providerKey, model)
		}

		isStreamChan := false
		if bifrostError == nil && !emptyStream {
			if ch, ok := any(result).(chan *schemas.BifrostStreamChunk); ok && ch != nil {
				isStreamChan = true
			}
		}
		if !isStreamChan {

			spanEnded := false
			if isStreamRequest {
				if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
					spanEnded = tracer.GetDeferredSpanHandle(traceID) == nil
					tracer.ClearDeferredSpan(traceID)
				}
			}

			if !spanEnded {

				if resp, ok := any(result).(*schemas.BifrostResponse); ok {

					resolvedModelUsed := model
					if req != nil {
						if _, rm, _ := req.GetRequestFields(); rm != "" {
							resolvedModelUsed = rm
						}
					}
					resp.PopulateExtraFields(requestType, providerKey, model, resolvedModelUsed)
					_, attrHandle := tracer.StartSpanID(ctx, "attribute-population", schemas.SpanKindInternal)
					tracer.PopulateLLMResponseAttributes(ctx, handle, resp, bifrostError)
					tracer.EndSpan(attrHandle, schemas.SpanStatusOk, "")
				} else if bifrostError != nil {

					_, attrHandle := tracer.StartSpanID(ctx, "attribute-population", schemas.SpanKindInternal)
					tracer.PopulateLLMResponseAttributes(ctx, handle, nil, bifrostError)
					tracer.EndSpan(attrHandle, schemas.SpanStatusOk, "")
				}

				if bifrostError != nil {
					tracer.EndSpan(handle, schemas.SpanStatusError, "request failed")
				} else {
					tracer.EndSpan(handle, schemas.SpanStatusOk, "")
				}
			}
		}

		logger.Debug("request %s for provider %s completed", requestType, providerKey)

		if bifrostError == nil ||
			bifrostError.IsBifrostError ||
			(bifrostError.Error != nil && bifrostError.Error.Type != nil && *bifrostError.Error.Type == schemas.RequestCancelled) {

			if keyProvider != nil {
				stampEndedAttempt(ctx, bifrostError)
			}
			break
		}

		if executed, _ := ctx.Value(schemas.BifrostContextKeyInjectedToolsExecuted).(bool); executed {
			break
		}

		class := ClassifyFailure(bifrostError)
		isPerKeyFailure := class.IsPerKey()

		shouldRetry := class == schemas.FailureClassTransient || isPerKeyFailure
		if keyProvider == nil {
			shouldRetry = retriesWithoutRotation(bifrostError, class)
		}

		errMessage := bifrostError.GetErrorString()
		if shouldRetry {
			logger.Debug("attempt failed with a %s error, will retry: %s", class, errMessage)
		}

		if trail, ok := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord); ok && len(trail) > 0 {
			last := &trail[len(trail)-1]
			last.FailureClass = class
			if bifrostError.StatusCode != nil {
				status := *bifrostError.StatusCode
				last.StatusCode = &status
			}

			last.RetryAfter = bifrostError.ExtraFields.RetryAfter
			reason := class.FailReason()
			if reason == "" {
				reason = "unknown"
				if bifrostError.Error != nil && bifrostError.Error.Type != nil && *bifrostError.Error.Type != "" {
					reason = *bifrostError.Error.Type
				}
			}
			last.FailReason = &reason
			ctx.SetValue(schemas.BifrostContextKeyAttemptTrail, trail)
		}

		lastWasEncryptedContentStrip = false
		if !shouldRetry && !strippedEncryptedContent && shouldStripReasoningAfterClientError(bifrostError) &&
			stripUnverifiableReasoning(ctx, req) {
			strippedEncryptedContent = true
			lastWasEncryptedContentStrip = true
			extraAttempts++
			shouldRetry = true
			if isEncryptedReasoningRejection(bifrostError) {
				logger.Warn("upstream rejected replayed encrypted reasoning content for %s/%s; retrying once without it: %s", providerKey, model, errMessage)
			} else {
				logger.Warn("upstream returned 400 for a request to %s/%s that replays encrypted reasoning; retrying once without it in case the token is the cause: %s", providerKey, model, errMessage)
			}
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineCore, schemas.LogLevelWarn, fmt.Sprintf("Stripped unverifiable encrypted reasoning content from the request to %s/%s and retrying once", providerKey, model))
		}

		if !shouldRetry {
			break
		}

		isPermanentKeyFailure := false
		if isPerKeyFailure && keyProvider != nil {
			isPermanentKeyFailure = class.IsPermanentPerKey()
			if isPermanentKeyFailure {
				if deadKeyIDs == nil {
					deadKeyIDs = make(map[string]bool)
					deadKeyClasses = make(map[string]schemas.FailureClass)
				}

				if !deadKeyIDs[currentKey.ID] {
					deadKeyIDs[currentKey.ID] = true
					deadKeyClasses[currentKey.ID] = class
					extraAttempts++
					permanentGrants++
				}
				if class == schemas.FailureClassModelAccess || class == schemas.FailureClassModelGone || class == schemas.FailureClassRegionBlocked {
					lastModelOrRegionError = bifrostError
				}
			} else {
				if usedKeyIDs == nil {
					usedKeyIDs = make(map[string]bool)
				}
				usedKeyIDs[currentKey.ID] = true
			}
		}
		lastWasPerKeyFailure = isPerKeyFailure
		lastWasPermanentKeyFailure = isPermanentKeyFailure

		previousKeyID = currentKey.ID

		if lastWasPerKeyFailure && keyProvider != nil && attempts < config.NetworkConfig.MaxRetries+extraAttempts {
			if trail, ok := ctx.Value(schemas.BifrostContextKeyAttemptTrail).([]schemas.KeyAttemptRecord); ok && len(trail) > 0 {
				pendingRotationAttemptIdx = len(trail) - 1
			}
		}
	}

	if attempts > 0 {
		logger.Debug("request failed after %d %s", attempts, map[bool]string{true: "attempts", false: "attempt"}[attempts > 1])
	}

	if bifrostError != nil && keyProvider != nil {
		ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, "")
		ctx.SetValue(schemas.BifrostContextKeySelectedKeyName, "")
	}

	return result, bifrostError
}

func clearAnthropicPassthroughForNonNativeProvider(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, model string) {
	if integrationType, _ := ctx.Value(schemas.BifrostContextKeyIntegrationType).(string); integrationType != "anthropic" {
		return
	}
	if baseProvider == schemas.Anthropic {
		return
	}
	if baseProvider == schemas.Vertex && schemas.GetVertexEndpoint(ctx) != nil {
		disableAnthropicPassthrough(ctx)
		return
	}
	if (baseProvider == schemas.Vertex ||
		baseProvider == schemas.Azure ||
		baseProvider == schemas.BedrockMantle) &&
		schemas.IsAnthropicModelFamily(ctx, model) {
		return
	}
	disableAnthropicPassthrough(ctx)
}

func disableAnthropicPassthrough(ctx *schemas.BifrostContext) {

	ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, false)
	ctx.ClearValue(schemas.BifrostContextKeyRawRequestBodyTextRewriter)
	ctx.ClearValue(schemas.BifrostContextKeyRawStreamTextCodec)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawResponse, false)
	ctx.SetValue(schemas.BifrostContextKeyPassthroughOverridesPresent, false)
	ctx.ClearValue(schemas.BifrostContextKeyURLPath)
}

func clearGenAIPassthroughForNonGoogleProvider(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, requestType schemas.RequestType, model string) {
	if integrationType, _ := ctx.Value(schemas.BifrostContextKeyIntegrationType).(string); integrationType != "genai" {
		return
	}
	if baseProvider == schemas.Gemini {
		return
	}
	if baseProvider == schemas.Vertex && schemas.GetVertexEndpoint(ctx) != nil {
		ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, false)
		ctx.ClearValue(schemas.BifrostContextKeyRawRequestBodyTextRewriter)
		return
	}
	if baseProvider == schemas.Vertex &&
		(isBatchRequestType(requestType) || isFileRequestType(requestType) || isCachedContentRequestType(requestType) ||
			schemas.IsGeminiModelFamily(ctx, model) || schemas.IsGemmaModelFamily(ctx, model) ||
			schemas.IsImagenModelFamily(ctx, model) || schemas.IsVeoModelFamily(ctx, model) ||
			schemas.IsAllDigitsASCII(model)) {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, false)
	ctx.ClearValue(schemas.BifrostContextKeyRawRequestBodyTextRewriter)
}

func clearAnthropicPassthroughForUnsupportedStructuredOutput(ctx *schemas.BifrostContext, baseProvider schemas.ModelProvider, req *schemas.BifrostRequest) {
	if integrationType, _ := ctx.Value(schemas.BifrostContextKeyIntegrationType).(string); integrationType != "anthropic" {
		return
	}
	if useRawBody, _ := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); !useRawBody {
		return
	}
	if !anthropic.ProviderRequiresSyntheticStructuredOutput(baseProvider) {
		return
	}

	if req == nil || req.ResponsesRequest == nil {
		return
	}
	rawBody := req.ResponsesRequest.GetRawRequestBody()
	if !providerUtils.JSONFieldExists(rawBody, "output_config.format") && !providerUtils.JSONFieldExists(rawBody, "output_format") {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, false)
}

func applyRawCaptureSignals(ctx *schemas.BifrostContext, config *schemas.ProviderConfig) {

	effectiveSendBackReq := config.SendBackRawRequest
	allowRawOverride, _ := ctx.Value(schemas.BifrostContextKeyAllowPerRequestRawOverride).(bool)
	passthroughOverridePresent, _ := ctx.Value(schemas.BifrostContextKeyPassthroughOverridesPresent).(bool)

	if allowRawOverride || passthroughOverridePresent {
		if override, ok := ctx.Value(schemas.BifrostContextKeySendBackRawRequest).(bool); ok {
			effectiveSendBackReq = override
		}
	}
	effectiveSendBackResp := config.SendBackRawResponse
	if allowRawOverride || passthroughOverridePresent {
		if override, ok := ctx.Value(schemas.BifrostContextKeySendBackRawResponse).(bool); ok {
			effectiveSendBackResp = override
		}
	}
	effectiveStore := config.StoreRawRequestResponse
	allowStorageOverride, _ := ctx.Value(schemas.BifrostContextKeyAllowPerRequestStorageOverride).(bool)
	if allowStorageOverride {
		if override, ok := ctx.Value(schemas.BifrostContextKeyStoreRawRequestResponse).(bool); ok {
			effectiveStore = override
		}
	}

	captureReq := effectiveSendBackReq || effectiveStore
	captureResp := effectiveSendBackResp || effectiveStore

	dropReq := effectiveStore && !effectiveSendBackReq
	dropResp := effectiveStore && !effectiveSendBackResp

	ctx.SetValue(schemas.BifrostContextKeyCaptureRawRequest, captureReq)
	ctx.SetValue(schemas.BifrostContextKeyCaptureRawResponse, captureResp)
	ctx.SetValue(schemas.BifrostContextKeyDropRawRequestFromClient, dropReq)
	ctx.SetValue(schemas.BifrostContextKeyDropRawResponseFromClient, dropResp)

	ctx.SetValue(schemas.BifrostContextKeyShouldStoreRawInLogs, effectiveStore)
}

func applyProviderProxySignal(ctx *schemas.BifrostContext, config *schemas.ProviderConfig) {
	var proxyConfig *schemas.ProxyConfig
	if config != nil {
		proxyConfig = config.ProxyConfig
	}
	ctx.SetValue(schemas.BifrostContextKeyProviderProxyConfig, proxyConfig)
}

func (bifrost *Bifrost) requestWorker(provider schemas.Provider, config *schemas.ProviderConfig, pq *ProviderQueue, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	deliveryTimer := time.NewTimer(time.Hour)
	deliveryTimer.Stop()
	defer deliveryTimer.Stop()

	for {
		var req *ChannelMessage
		select {
		case r := <-pq.queue:
			req = r
			bifrost.endQueueWaitSpan(req)
		case <-pq.done:

			for {
				select {
				case r := <-pq.queue:

					bifrost.endQueueWaitSpan(r)
					provKey, mod, _ := r.GetRequestFields()
					bifrost.sendWorkerError(r, schemas.BifrostError{
						IsBifrostError: false,
						Error: &schemas.ErrorField{
							Message: "provider is shutting down",
						},
						ExtraFields: schemas.BifrostErrorExtraFields{
							RequestType:            r.RequestType,
							Provider:               provKey,
							OriginalModelRequested: mod,
						},
					})
				default:
					return
				}
			}
		}

		workerSetupSpan := bifrost.startCoreSpan(req.Context, "worker-setup")
		_, model, _ := req.BifrostRequest.GetRequestFields()

		var result *schemas.BifrostResponse
		var stream chan *schemas.BifrostStreamChunk
		var bifrostError *schemas.BifrostError
		var err error

		baseProvider := provider.GetProviderKey()
		if cfg := config.CustomProviderConfig; cfg != nil && cfg.BaseProviderType != "" {
			baseProvider = cfg.BaseProviderType
		}
		req.Context.SetValue(schemas.BifrostContextKeyIsCustomProvider, !IsStandardProvider(baseProvider))

		req.Context.SetValue(schemas.BifrostContextKeyBaseProviderType, baseProvider)

		if config.CustomProviderConfig != nil && config.CustomProviderConfig.DoesNotSendDoneMarker {
			req.Context.SetValue(schemas.BifrostContextKeyDoesNotSendDoneMarker, true)
		} else {
			req.Context.ClearValue(schemas.BifrostContextKeyDoesNotSendDoneMarker)
		}

		if config.CustomProviderConfig != nil && config.CustomProviderConfig.WaitForUsage {
			req.Context.SetValue(schemas.BifrostContextKeyWaitForUsage, true)
		} else {
			req.Context.ClearValue(schemas.BifrostContextKeyWaitForUsage)
		}

		bifrost.endCoreSpan(workerSetupSpan)

		if config.Passthrough != nil && (req.RequestType == schemas.PassthroughRequest || req.RequestType == schemas.PassthroughStreamRequest) &&
			!passthroughRouteAllowed(config.Passthrough, req.BifrostRequest.PassthroughRequest) {
			bifrost.sendWorkerError(req, newPassthroughRouteNotAllowedError(provider.GetProviderKey(), req.RequestType, req.BifrostRequest.PassthroughRequest))
			continue
		}

		var keys []schemas.Key

		var keyProvider func(usedKeyIDs, deadKeyIDs map[string]bool) (schemas.Key, error)

		if providerRequiresKey(config.CustomProviderConfig) {

			if req.RequestType == schemas.ListModelsRequest {
				keys, err = bifrost.getAllSupportedKeys(req.Context, provider.GetProviderKey(), baseProvider)
				if err != nil {
					bifrost.logger.Debug("error getting supported keys for list models: %v", err)
					bifrost.sendWorkerError(req, schemas.BifrostError{
						IsBifrostError: false,
						Error: &schemas.ErrorField{
							Message: err.Error(),
							Error:   err,
						},
						ExtraFields: schemas.BifrostErrorExtraFields{
							Provider:               provider.GetProviderKey(),
							RequestType:            req.RequestType,
							OriginalModelRequested: model,
							ResolvedModelUsed:      model,
						},
					})
					continue
				}

				if lmr := req.BifrostRequest.ListModelsRequest; lmr != nil && lmr.KeyID != nil {
					target := *lmr.KeyID
					keys = filterKeysByID(keys, target)
					if len(keys) == 0 {
						bifrost.sendWorkerError(req, schemas.BifrostError{
							IsBifrostError: false,
							Error: &schemas.ErrorField{
								Message: fmt.Sprintf("no key found with id %q for provider %s", target, provider.GetProviderKey()),
							},
							ExtraFields: schemas.BifrostErrorExtraFields{
								Provider:               provider.GetProviderKey(),
								RequestType:            req.RequestType,
								OriginalModelRequested: model,
								ResolvedModelUsed:      model,
							},
						})
						continue
					}
				}
			} else {

				isMultiKeyBatchOp := isBatchRequestType(req.RequestType) && req.RequestType != schemas.BatchCreateRequest
				isMultiKeyFileOp := isFileRequestType(req.RequestType) && req.RequestType != schemas.FileUploadRequest
				isMultiKeyContainerOp := isContainerRequestType(req.RequestType) && req.RequestType != schemas.ContainerCreateRequest && req.RequestType != schemas.ContainerFileCreateRequest
				isMultiKeyCachedContentOp := isCachedContentRequestType(req.RequestType) && req.RequestType != schemas.CachedContentCreateRequest

				if isMultiKeyBatchOp || isMultiKeyFileOp || isMultiKeyContainerOp || isMultiKeyCachedContentOp {
					var modelPtr *string
					if model != "" {
						modelPtr = &model
					}
					keys, err = bifrost.getKeysForBatchAndFileOps(req.Context, provider.GetProviderKey(), baseProvider, modelPtr, isMultiKeyBatchOp)
					if err != nil {
						bifrost.logger.Debug("error getting keys for batch/file operation: %v", err)
						bifrost.sendWorkerError(req, schemas.BifrostError{
							IsBifrostError: false,
							Error: &schemas.ErrorField{
								Message: err.Error(),
								Error:   err,
							},
							ExtraFields: schemas.BifrostErrorExtraFields{
								Provider:               provider.GetProviderKey(),
								RequestType:            req.RequestType,
								OriginalModelRequested: model,
								ResolvedModelUsed:      model,
							},
						})
						continue
					}
				} else {

					keyPoolSpan := bifrost.startCoreSpan(req.Context, "key-pool")
					supportedKeys, canRotate, keyPoolErr := bifrost.selectKeyFromProviderForModelWithPool(req.Context, req.RequestType, provider.GetProviderKey(), model, baseProvider)
					bifrost.endCoreSpan(keyPoolSpan)
					if keyPoolErr != nil {
						bifrost.logger.Debug("error building key pool for model %s: %v", model, keyPoolErr)
						bifrost.sendWorkerError(req, schemas.BifrostError{
							IsBifrostError: false,
							Type:           schemas.Ptr(schemas.NoKeySupportsModel),
							StatusCode:     schemas.Ptr(400),
							Error: &schemas.ErrorField{
								Message: keyPoolErr.Error(),
								Error:   keyPoolErr,
								Type:    schemas.Ptr(schemas.NoKeySupportsModel),
							},
							ExtraFields: schemas.BifrostErrorExtraFields{
								Provider:               provider.GetProviderKey(),
								RequestType:            req.RequestType,
								OriginalModelRequested: model,
								ResolvedModelUsed:      model,
								ErrorType:              schemas.ErrorTypeCallerModelNotAvailable,
							},
						})
						continue
					}

					if len(supportedKeys) == 0 {

					} else if !canRotate {

						fixedKey := supportedKeys[0]
						rest := supportedKeys[1:]
						provKey := provider.GetProviderKey()
						mdl := model
						keyProvider = func(usedKeyIDs, deadKeyIDs map[string]bool) (schemas.Key, error) {
							if !deadKeyIDs[fixedKey.ID] {
								return fixedKey, nil
							}
							return bifrost.pickFromRotatingPool(req.Context, rest, provKey, mdl, usedKeyIDs, deadKeyIDs)
						}
					} else {

						pool := supportedKeys
						provKey := provider.GetProviderKey()
						mdl := model
						keyProvider = func(usedKeyIDs, deadKeyIDs map[string]bool) (schemas.Key, error) {
							return bifrost.pickFromRotatingPool(req.Context, pool, provKey, mdl, usedKeyIDs, deadKeyIDs)
						}
					}
				}
			}
		}

		originalModelRequested := model

		var resolvedModel string

		attemptRoutingInfo := schemas.RoutingInfo{
			Provider: provider.GetProviderKey(),
			Model:    originalModelRequested,
		}

		attemptRoutingInfo.ApplyRequestRouting(req.Context)

		var lastAttemptFinalizer func(context.Context)

		var heldBackError atomic.Bool

		if IsStreamRequestType(req.RequestType) {
			stream, bifrostError = executeRequestWithRetries(req.Context, config, func(k schemas.Key) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {

				if abort := providerUtils.NewAttemptAbort(req.firstTokenTimeout); abort != nil {
					req.Context.SetValue(schemas.BifrostContextKeyStreamAttemptAbort, abort)
				}
				if aliasConfig := k.Aliases.ResolveConfig(originalModelRequested); aliasConfig != nil {
					resolvedModel = aliasConfig.ModelID
					req.Context.SetValue(schemas.BifrostContextKeyResolvedAlias, &schemas.ResolvedAlias{Key: originalModelRequested, Config: aliasConfig})
				} else {
					resolvedModel = originalModelRequested
					req.Context.SetValue(schemas.BifrostContextKeyResolvedAlias, nil)
				}
				req.SetModel(resolvedModel)

				clearAnthropicPassthroughForNonNativeProvider(req.Context, baseProvider, resolvedModel)

				clearAnthropicPassthroughForUnsupportedStructuredOutput(req.Context, baseProvider, &req.BifrostRequest)

				clearGenAIPassthroughForNonGoogleProvider(req.Context, baseProvider, req.RequestType, resolvedModel)

				applyRawCaptureSignals(req.Context, config)
				applyProviderProxySignal(req.Context, config)

				attemptResolvedModel := resolvedModel
				attemptRoutingInfo = schemas.BuildRoutingInfo(req.Context, provider.GetProviderKey(), originalModelRequested, k)

				layerFallbackIdentity(req.Context, &attemptRoutingInfo)

				req.Context.SetValue(schemas.BifrostContextKeyRoutingInfo, attemptRoutingInfo)

				perAttemptRoutingInfo := attemptRoutingInfo

				attemptRequestType := req.RequestType
				pipeline := bifrost.getPluginPipeline()

				var isPreamble func(*schemas.BifrostStreamChunk) bool

				startup := providerUtils.NewStreamStartupState(isPreamble, providerUtils.AttemptAbortFromContext(req.Context) != nil)
				postHookRunner := func(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {

					if result != nil {
						result.PopulateExtraFields(attemptRequestType, provider.GetProviderKey(), originalModelRequested, attemptResolvedModel)
						result.PopulateRoutingInfo(perAttemptRoutingInfo)
					}
					if err != nil {
						err.PopulateExtraFields(attemptRequestType, provider.GetProviderKey(), originalModelRequested, attemptResolvedModel)
						err.PopulateRoutingInfo(perAttemptRoutingInfo)

						if keyProvider != nil {
							stampEndedStreamAttempt(ctx, err)
						}
					}

					if result == nil && startup.IsStartupError(err) && err.ExtraFields.BilledUsage == nil && ctx.Err() == nil {
						heldBackError.Store(true)
						return nil, err
					}
					resp, bifrostErr := pipeline.RunPostLLMHooks(ctx, result, err, len(*bifrost.llmPlugins.Load()))
					if IsFinalChunk(ctx) {
						drainAndAttachPluginLogs(ctx)
						heldBackError.Store(false)
					}
					if bifrostErr != nil {
						bifrostErr.PopulateExtraFields(attemptRequestType, provider.GetProviderKey(), originalModelRequested, attemptResolvedModel)
						bifrostErr.PopulateRoutingInfo(perAttemptRoutingInfo)
						resp = nil
					} else if resp != nil {
						resp.PopulateExtraFields(attemptRequestType, provider.GetProviderKey(), originalModelRequested, attemptResolvedModel)
						resp.PopulateRoutingInfo(perAttemptRoutingInfo)
					}
					if startup.ObserveResult(ctx, resp, bifrostErr) {
						readyHeldStreamCommit(ctx)
					}
					return resp, bifrostErr
				}

				var finalizerOnce sync.Once
				postHookSpanFinalizer := func(ctx context.Context) {
					finalizerOnce.Do(func() {
						pipeline.FinalizeStreamingPostHookSpans(ctx)
						bifrost.releasePluginPipeline(pipeline)
					})
				}
				lastAttemptFinalizer = postHookSpanFinalizer
				var streamCh chan *schemas.BifrostStreamChunk
				var streamErr *schemas.BifrostError
				switch {

				default:
					streamCh, streamErr = bifrost.handleProviderStreamRequest(provider, config, req, k, postHookRunner, postHookSpanFinalizer)
				}

				if streamErr != nil && streamCh == nil {
					finalizerOnce.Do(func() {
						bifrost.releasePluginPipeline(pipeline)
					})
				}
				return streamCh, streamErr
			}, keyProvider, req.RequestType, provider.GetProviderKey(), model, &req.BifrostRequest, bifrost.logger)
		} else {
			result, bifrostError = executeRequestWithRetries(req.Context, config, func(k schemas.Key) (*schemas.BifrostResponse, *schemas.BifrostError) {
				if aliasConfig := k.Aliases.ResolveConfig(originalModelRequested); aliasConfig != nil {
					resolvedModel = aliasConfig.ModelID
					req.Context.SetValue(schemas.BifrostContextKeyResolvedAlias, &schemas.ResolvedAlias{Key: originalModelRequested, Config: aliasConfig})
				} else {
					resolvedModel = originalModelRequested
					req.Context.SetValue(schemas.BifrostContextKeyResolvedAlias, nil)
				}
				req.SetModel(resolvedModel)

				req.Context.SetValue(schemas.BifrostContextKeyInjectedToolsExecuted, false)

				clearAnthropicPassthroughForNonNativeProvider(req.Context, baseProvider, resolvedModel)

				clearAnthropicPassthroughForUnsupportedStructuredOutput(req.Context, baseProvider, &req.BifrostRequest)

				clearGenAIPassthroughForNonGoogleProvider(req.Context, baseProvider, req.RequestType, resolvedModel)

				applyRawCaptureSignals(req.Context, config)
				applyProviderProxySignal(req.Context, config)
				attemptRoutingInfo = schemas.BuildRoutingInfo(req.Context, provider.GetProviderKey(), originalModelRequested, k)

				return bifrost.handleProviderRequest(provider, config, req, k, keys)
			}, keyProvider, req.RequestType, provider.GetProviderKey(), model, &req.BifrostRequest, bifrost.logger)
			if bifrostError != nil && req.RequestType == schemas.ListModelsRequest {
				if fallback := bifrost.lastKnownListModelsResponse(req.Context, provider.GetProviderKey(), req.BifrostRequest.ListModelsRequest, bifrostError); fallback != nil {
					result, bifrostError = fallback, nil
				}
			}
		}

		if IsStreamRequestType(req.RequestType) && bifrostError != nil {
			if lastAttemptFinalizer != nil {
				lastAttemptFinalizer(req.Context)
			}
		}

		if IsStreamRequestType(req.RequestType) && bifrostError != nil && heldBackError.Load() {
			bifrostError.PopulateExtraFields(req.RequestType, provider.GetProviderKey(), originalModelRequested, resolvedModel)
			bifrostError.PopulateRoutingInfo(attemptRoutingInfo)
			if recovered, recoveredErr := bifrost.runStreamErrorPostHooks(req.Context, bifrostError); recoveredErr != nil {
				bifrostError = recoveredErr
				req.streamPostHooksRan = true
			} else {
				recovered.PopulateExtraFields(req.RequestType, provider.GetProviderKey(), originalModelRequested, resolvedModel)
				stream, bifrostError = newBifrostMessageChan(recovered), nil
			}
		}

		if bifrostError != nil {

			miscSpan := bifrost.startCoreSpan(req.Context, "miscellaneous")
			bifrostError.PopulateExtraFields(req.RequestType, provider.GetProviderKey(), originalModelRequested, resolvedModel)
			bifrostError.PopulateRoutingInfo(attemptRoutingInfo)
			bifrost.endCoreSpan(miscSpan)
			req.sentAt = time.Now()

			if IsStreamRequestType(req.RequestType) {

				deliveryTimer.Reset(5 * time.Second)
				select {
				case req.Err <- *bifrostError:

				case <-req.Context.Done():

					bifrost.logger.Debug("Client context cancelled while sending error response")
				case <-deliveryTimer.C:

					bifrost.logger.Warn("Timeout while sending error response, client may have disconnected")
				}
				deliveryTimer.Stop()
			} else if !req.claimDelivery() {

				bifrost.logger.Debug("Client context cancelled before error handoff")
				bifrost.billAbandonedTerminal(req, nil, bifrostError)
				bifrost.releaseChannelMessage(req)
			} else {

				req.Err <- *bifrostError
			}
		} else {

			miscSpan := bifrost.startCoreSpan(req.Context, "miscellaneous")
			if result != nil {
				result.PopulateExtraFields(req.RequestType, provider.GetProviderKey(), originalModelRequested, resolvedModel)
				result.PopulateRoutingInfo(attemptRoutingInfo)
			}
			bifrost.endCoreSpan(miscSpan)
			req.sentAt = time.Now()
			if IsStreamRequestType(req.RequestType) {

				deliveryTimer.Reset(5 * time.Second)
				select {
				case req.ResponseStream <- stream:

				case <-req.Context.Done():

					bifrost.logger.Debug("Client context cancelled while sending stream response")
					drainAbandonedStream(stream)
				case <-deliveryTimer.C:

					bifrost.logger.Warn("Timeout while sending stream response, client may have disconnected")
					drainAbandonedStream(stream)
				}
				deliveryTimer.Stop()
			} else if !req.claimDelivery() {

				bifrost.logger.Debug("Client context cancelled before response handoff")
				bifrost.billAbandonedTerminal(req, result, nil)
				bifrost.releaseChannelMessage(req)
			} else {

				req.Response <- result
			}
		}
	}

}

func (bifrost *Bifrost) runStreamErrorPostHooks(ctx *schemas.BifrostContext, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
	ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
	pipeline := bifrost.getPluginPipeline()
	defer bifrost.releasePluginPipeline(pipeline)
	resp, recoveredErr := pipeline.RunPostLLMHooks(ctx, nil, bifrostErr, len(*bifrost.llmPlugins.Load()))
	pipeline.FinalizeStreamingPostHookSpans(ctx)
	drainAndAttachPluginLogs(ctx)
	if recoveredErr == nil && resp == nil {
		return nil, bifrostErr
	}
	return resp, recoveredErr
}

func (bifrost *Bifrost) sendWorkerError(req *ChannelMessage, bifrostError schemas.BifrostError) {
	if IsStreamRequestType(req.RequestType) {
		select {
		case req.Err <- bifrostError:
		case <-req.Context.Done():
		}
		return
	}
	if !req.claimDelivery() {
		bifrost.billAbandonedTerminal(req, nil, &bifrostError)
		bifrost.releaseChannelMessage(req)
		return
	}

	req.Err <- bifrostError
}

func (bifrost *Bifrost) billAbandonedTerminal(req *ChannelMessage, result *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) {
	if req == nil || req.Context == nil || IsStreamRequestType(req.RequestType) {
		return
	}
	pipeline := bifrost.getPluginPipeline()
	defer bifrost.releasePluginPipeline(pipeline)
	pluginCount := len(*bifrost.llmPlugins.Load())
	if bifrostErr != nil {
		_, _ = pipeline.RunPostLLMHooks(req.Context, nil, bifrostErr, pluginCount)
	} else if result != nil {
		_, _ = pipeline.RunPostLLMHooks(req.Context, result, nil, pluginCount)
	}
	drainAndAttachPluginLogs(req.Context)

	bifrost.completeAbandonedTrace(req.Context)
}

func (bifrost *Bifrost) completeAbandonedTrace(ctx *schemas.BifrostContext) {
	tracer, traceID, err := GetTracerFromContext(ctx)
	if err != nil || tracer == nil {
		return
	}

	tracer.AwaitTransportHandoff(traceID)
	if rootHandle := tracer.GetSpanHandleByID(traceID, nil); rootHandle != nil {
		tracer.EndSpan(rootHandle, schemas.SpanStatusError, "client disconnected before response")
	}
	tracer.ClearTraceCompletionDeferral(traceID)
	tracer.CompleteAndFlushTrace(traceID)
}

func drainAbandonedStream(stream chan *schemas.BifrostStreamChunk) {
	if stream == nil {
		return
	}
	go func() {
		for range stream {
		}
	}()
}

func promptCacheResponsesRequest(ctx *schemas.BifrostContext, config *schemas.ProviderConfig, provider schemas.ModelProvider, r *schemas.BifrostResponsesRequest) *schemas.BifrostResponsesRequest {
	if r == nil || config == nil {
		return r
	}
	promptCache := providerUtils.ResolvePromptCacheConfig(ctx, config.PromptCache)
	if !providerUtils.PromptCacheInjectionEnabled(promptCache, schemas.ResolveBaseProvider(ctx, provider), r.Model) {
		return r
	}
	cp := *r
	cp.Input = providerUtils.InjectResponsesCacheBreakpointsSkipping(promptCache, r.Input, injectedMCPInstructionsResponsesIndex(ctx, r.Input))
	return &cp
}

func routeChatReasoningMode(ctx *schemas.BifrostContext, provider schemas.Provider, req *schemas.BifrostChatRequest) *schemas.BifrostError {
	if req == nil || req.Params == nil || req.Params.Reasoning == nil || req.Params.Reasoning.Mode == nil {
		return nil
	}

	if useRawBody, _ := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); useRawBody {
		return nil
	}
	if schemas.ChatReasoningModeRequiresResponses(schemas.ResolveBaseProvider(ctx, provider.GetProviderKey())) {

		if unsupported := chatParamsWithoutResponsesEquivalent(req.Params); len(unsupported) > 0 {
			sc := fasthttp.StatusBadRequest
			return &schemas.BifrostError{
				IsBifrostError: false,
				StatusCode:     &sc,
				Error: &schemas.ErrorField{
					Type:    new("invalid_request_error"),
					Message: "reasoning.mode is served by the Responses API, which does not support: " + strings.Join(unsupported, ", ") + "; remove them or drop reasoning.mode",
				},
			}
		}
		ctx.SetValue(schemas.BifrostContextKeyChangeRequestType, schemas.ResponsesRequest)
		return nil
	}
	prior, _ := ctx.Value(schemas.BifrostContextKeyCompatDroppedParams).([]string)
	if !slices.Contains(prior, "reasoning.mode") {
		ctx.SetValue(schemas.BifrostContextKeyCompatDroppedParams, append(slices.Clip(prior), "reasoning.mode"))
	}
	return nil
}

func chatParamsWithoutResponsesEquivalent(p *schemas.ChatParameters) []string {
	var out []string
	if p.N != nil && *p.N > 1 {
		out = append(out, "n > 1")
	}
	if len(p.Stop) > 0 {
		out = append(out, "stop")
	}
	if p.Seed != nil {
		out = append(out, "seed")
	}
	if p.LogitBias != nil && len(*p.LogitBias) > 0 {
		out = append(out, "logit_bias")
	}
	if p.PresencePenalty != nil {
		out = append(out, "presence_penalty")
	}
	if p.FrequencyPenalty != nil {
		out = append(out, "frequency_penalty")
	}
	if p.LogProbs != nil && *p.LogProbs {
		out = append(out, "logprobs")
	}
	if p.Audio != nil || slices.Contains(p.Modalities, "audio") {
		out = append(out, "audio")
	}
	if p.Prediction != nil {
		out = append(out, "prediction")
	}
	if p.WebSearchOptions != nil {
		out = append(out, "web_search_options")
	}
	return out
}

func prepareResponsesRequest(ctx *schemas.BifrostContext, config *schemas.ProviderConfig, provider schemas.Provider, key schemas.Key, r *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesRequest, *schemas.BifrostError) {
	r = restoreResponsesBillingHeader(ctx, provider.GetProviderKey(), r)
	r = promptCacheResponsesRequest(ctx, config, provider.GetProviderKey(), r)
	if r == nil {
		return nil, nil
	}
	var supported bool
	if capable, ok := provider.(schemas.ResponsesNamespaceToolProvider); ok {
		supported = capable.SupportsResponsesNamespaceTools(ctx, key, r.Model)
	} else {
		supported = providerUtils.ResponsesNamespaceToolsSupported(ctx, schemas.ResolveBaseProvider(ctx, provider.GetProviderKey()), r.Model)
	}
	if !supported {
		var bifrostErr *schemas.BifrostError
		r, bifrostErr = hoistResponsesAdditionalTools(r)
		if bifrostErr != nil {
			return nil, bifrostErr
		}
	}

	r, bifrostErr := providerUtils.UnwrapDefaultNamespaceTools(r)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if !supported {
		if r, bifrostErr = providerUtils.FlattenResponsesNamespaceTools(ctx, r); bifrostErr != nil {
			return nil, bifrostErr
		}
	}

	return normalizeResponsesToolSchemas(ctx, provider.GetProviderKey(), r), nil
}

func normalizeResponsesToolSchemas(ctx *schemas.BifrostContext, provider schemas.ModelProvider, r *schemas.BifrostResponsesRequest) *schemas.BifrostResponsesRequest {
	if r == nil || r.Params == nil || len(r.Params.Tools) == 0 {
		return r
	}
	policy := toolSchemaPolicy(schemas.ResolveModelCaps(schemas.ResolveBaseProvider(ctx, provider), schemas.ResolveCanonicalModel(ctx, r.Model)))
	if policy.IsZero() {
		return r
	}
	tools, changed := providerUtils.ApplyResponsesToolSchemaPolicy(r.Params.Tools, policy)
	if !changed {
		return r
	}
	params := *r.Params
	params.Tools = tools
	cp := *r
	cp.Params = &params
	return &cp
}

func normalizeChatToolSchemas(ctx *schemas.BifrostContext, provider schemas.ModelProvider, r *schemas.BifrostChatRequest) *schemas.BifrostChatRequest {
	if r == nil || r.Params == nil || len(r.Params.Tools) == 0 {
		return r
	}
	policy := toolSchemaPolicy(schemas.ResolveModelCaps(schemas.ResolveBaseProvider(ctx, provider), schemas.ResolveCanonicalModel(ctx, r.Model)))
	if policy.IsZero() {
		return r
	}
	tools, changed := providerUtils.ApplyChatToolSchemaPolicy(r.Params.Tools, policy)
	if !changed {
		return r
	}
	params := *r.Params
	params.Tools = tools
	cp := *r
	cp.Params = &params
	return &cp
}

func toolSchemaPolicy(caps schemas.ModelCaps) providerUtils.ToolSchemaPolicy {
	moonshot := schemas.IsMoonshotModel(caps.Model())
	relaxed := moonshot || schemas.IsDeepSeekModel(caps.Model())
	var formats []string
	if moonshot {
		formats = moonshotSchemaFormats
	}
	return providerUtils.ToolSchemaPolicy{
		NormalizeNULEscape: !caps.SupportsRegexNULEscape(!relaxed),
		StripLookaround:    !caps.SupportsRegexLookaround(!relaxed),
		DropPattern:        !caps.SupportsSchemaPattern(true),
		StringFormats:      caps.SupportedSchemaFormats(formats),
	}
}

func prepareChatRequest(ctx *schemas.BifrostContext, config *schemas.ProviderConfig, provider schemas.ModelProvider, r *schemas.BifrostChatRequest) *schemas.BifrostChatRequest {
	r = normalizeChatToolSchemas(ctx, provider, promptCacheChatRequest(ctx, config, provider, r))
	if r != nil && config != nil && config.PreferResponses && schemas.ResolveBaseProvider(ctx, provider) == schemas.OpenAI {
		// The provider/key routing identity stays on the original request. Only
		// serialization needs the native base: an alias would take OpenAI's generic
		// compatibility filter and discard Chat-only prediction/search parameters.
		cp := *r
		cp.Provider = schemas.OpenAI
		return &cp
	}
	return r
}

func promptCacheChatRequest(ctx *schemas.BifrostContext, config *schemas.ProviderConfig, provider schemas.ModelProvider, r *schemas.BifrostChatRequest) *schemas.BifrostChatRequest {
	if r == nil {
		return r
	}
	baseProvider := schemas.ResolveBaseProvider(ctx, provider)
	if !providerUtils.ChatCachePointsSupported(baseProvider, schemas.ResolveCanonicalModel(ctx, r.Model)) {
		if input, stripped := providerUtils.StripChatCachePoints(r.Input); stripped {
			cp := *r
			cp.Input = input
			r = &cp
		}
	}
	if config == nil {
		return r
	}
	promptCache := providerUtils.ResolvePromptCacheConfig(ctx, config.PromptCache)
	if !providerUtils.PromptCacheInjectionEnabled(promptCache, baseProvider, r.Model) {
		return r
	}
	cp := *r
	cp.Input = providerUtils.InjectChatCacheBreakpointsSkipping(promptCache, r.Input, injectedMCPInstructionsChatIndex(ctx, r.Input))
	return &cp
}

func injectedMCPInstructionsChatIndex(ctx *schemas.BifrostContext, input []schemas.ChatMessage) int {
	instructions := injectedMCPInstructions(ctx)
	if instructions == nil {
		return -1
	}
	for i := range input {
		msg := &input[i]
		if msg.Role == schemas.ChatMessageRoleSystem && msg.Content != nil && msg.Content.ContentStr != nil &&
			(msg.Content.ContentStr == instructions || *msg.Content.ContentStr == *instructions) {
			return i
		}
	}
	return -1
}

func injectedMCPInstructionsResponsesIndex(ctx *schemas.BifrostContext, input []schemas.ResponsesMessage) int {
	instructions := injectedMCPInstructions(ctx)
	if instructions == nil {
		return -1
	}
	for i := range input {
		msg := &input[i]
		if msg.Role == nil || *msg.Role != schemas.ResponsesInputMessageRoleSystem || msg.Content == nil {
			continue
		}
		if s := msg.Content.ContentStr; s != nil && *s == *instructions {
			return i
		}
		if blocks := msg.Content.ContentBlocks; len(blocks) == 1 && blocks[0].Text != nil && *blocks[0].Text == *instructions {
			return i
		}
	}
	return -1
}

func injectedMCPInstructions(ctx *schemas.BifrostContext) *string {
	if ctx == nil {
		return nil
	}
	instructions, _ := ctx.Value(schemas.BifrostContextKeyMCPInstructionsText).(*string)
	return instructions
}

// preferResponsesForChat is a per-attempt preference, never a context signal:
// a fallback provider must choose its own protocol from the original chat input.
func preferResponsesForChat(ctx *schemas.BifrostContext, provider schemas.Provider, config *schemas.ProviderConfig, req *schemas.BifrostChatRequest) bool {
	if config == nil || !config.PreferResponses || req == nil || schemas.ResolveBaseProvider(ctx, provider.GetProviderKey()) != schemas.OpenAI {
		return false
	}
	if useRawBody, _ := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); useRawBody {
		return false
	}
	p := req.Params
	if p == nil {
		return true
	}
	if len(chatParamsWithoutResponsesEquivalent(p)) > 0 {
		return false
	}
	// These typed extensions are not copied by ToResponsesRequest. Keep them on
	// Chat rather than silently discarding them merely because of the preference.
	if p.TopK != nil || p.Speed != nil || p.InferenceGeo != nil || len(p.MCPServers) > 0 || p.Container != nil || p.CacheControl != nil || p.TaskBudget != nil || len(p.ContextManagement) > 0 || p.IncludeServerSideToolInvocations != nil {
		return false
	}
	if p.Reasoning != nil && p.Reasoning.Display != nil {
		return false
	}
	if p.ResponseFormat != nil && schemas.ResponsesTextConfigFormatFromChatResponseFormat(p.ResponseFormat) == nil {
		return false
	}
	return true
}

func (bifrost *Bifrost) dispatchChat(ctx *schemas.BifrostContext, provider schemas.Provider, config *schemas.ProviderConfig, key schemas.Key, chatRequest, original *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	if bifrostError := routeChatReasoningMode(ctx, provider, chatRequest); bifrostError != nil {
		return nil, bifrostError
	}
	changeType, _ := ctx.Value(schemas.BifrostContextKeyChangeRequestType).(schemas.RequestType)
	if changeType == schemas.ResponsesRequest || preferResponsesForChat(ctx, provider, config, chatRequest) {
		responsesRequest := chatRequest.ToResponsesRequest()
		if responsesRequest != nil {
			responsesRequest, bifrostError := prepareResponsesRequest(ctx, config, provider, key, responsesRequest)
			if bifrostError != nil {
				return nil, bifrostError
			}
			responsesResponse, bifrostError := provider.Responses(ctx, key, responsesRequest)
			if bifrostError != nil {
				return nil, bifrostError
			}
			chatResponse := responsesResponse.ToBifrostChatResponse()
			if chatResponse != nil {

				chatResponse.ExtraFields.ConvertedRequestType = schemas.ResponsesRequest
			}
			return chatResponse, nil
		}
	}
	chatCompletionResponse, bifrostError := provider.ChatCompletion(ctx, key, prepareChatRequest(ctx, config, provider.GetProviderKey(), chatRequest))
	if bifrostError != nil {
		return nil, bifrostError
	}
	chatCompletionResponse.BackfillParams(original)
	return chatCompletionResponse, nil
}

// Responses output indices include reasoning and message items. Compact only
// tool indices after conversion, with state owned by this one stream attempt.
func wrapChatToolCallIndices(postHookRunner schemas.PostHookRunner) schemas.PostHookRunner {
	indices := make(map[uint16]uint16)
	return func(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, failure *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
		if result != nil && result.ChatResponse != nil {
			for _, choice := range result.ChatResponse.Choices {
				if choice.ChatStreamResponseChoice == nil || choice.Delta == nil {
					continue
				}
				for i := range choice.Delta.ToolCalls {
					call := &choice.Delta.ToolCalls[i]
					index, exists := indices[call.Index]
					if !exists {
						index = uint16(len(indices))
						indices[call.Index] = index
					}
					call.Index = index
				}
			}
		}
		return postHookRunner(ctx, result, failure)
	}
}

func (bifrost *Bifrost) dispatchChatStream(ctx *schemas.BifrostContext, provider schemas.Provider, config *schemas.ProviderConfig, key schemas.Key, chatRequest *schemas.BifrostChatRequest, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context)) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	if bifrostError := routeChatReasoningMode(ctx, provider, chatRequest); bifrostError != nil {
		return nil, bifrostError
	}
	changeType, _ := ctx.Value(schemas.BifrostContextKeyChangeRequestType).(schemas.RequestType)
	if changeType == schemas.ResponsesRequest || preferResponsesForChat(ctx, provider, config, chatRequest) {
		responsesRequest := chatRequest.ToResponsesRequest()
		if responsesRequest != nil {
			responsesRequest, bifrostError := prepareResponsesRequest(ctx, config, provider, key, responsesRequest)
			if bifrostError != nil {
				return nil, bifrostError
			}
			return provider.ResponsesStream(ctx, wrapConvertedStreamPostHookRunner(wrapChatToolCallIndices(postHookRunner), schemas.ResponsesRequest), postHookSpanFinalizer, key, responsesRequest)
		}
	}
	return provider.ChatCompletionStream(ctx, postHookRunner, postHookSpanFinalizer, key, prepareChatRequest(ctx, config, provider.GetProviderKey(), chatRequest))
}

func (bifrost *Bifrost) handleProviderRequest(provider schemas.Provider, config *schemas.ProviderConfig, req *ChannelMessage, key schemas.Key, keys []schemas.Key) (*schemas.BifrostResponse, *schemas.BifrostError) {
	response := &schemas.BifrostResponse{}
	switch req.RequestType {
	case schemas.ListModelsRequest:
		listModelsResponse, bifrostError := provider.ListModels(req.Context, keys, req.BifrostRequest.ListModelsRequest)
		if bifrostError != nil {
			return nil, bifrostError
		}
		response.ListModelsResponse = listModelsResponse

	case schemas.ChatCompletionRequest:
		chatResponse, bifrostError := bifrost.dispatchChat(req.Context, provider, config, key, req.BifrostRequest.ChatRequest, req.BifrostRequest.ChatRequest)
		if bifrostError != nil {
			return nil, bifrostError
		}
		response.ChatResponse = chatResponse

	default:
		_, model, _ := req.BifrostRequest.GetRequestFields()
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: fmt.Sprintf("unsupported request type: %s", req.RequestType),
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            req.RequestType,
				Provider:               provider.GetProviderKey(),
				OriginalModelRequested: model,
				ResolvedModelUsed:      model,
			},
		}
	}
	return response, nil
}

func (bifrost *Bifrost) handleProviderStreamRequest(provider schemas.Provider, config *schemas.ProviderConfig, req *ChannelMessage, key schemas.Key, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context)) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	switch req.RequestType {

	case schemas.ChatCompletionStreamRequest:
		return bifrost.dispatchChatStream(req.Context, provider, config, key, req.BifrostRequest.ChatRequest, postHookRunner, postHookSpanFinalizer)

	default:
		_, model, _ := req.BifrostRequest.GetRequestFields()
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: fmt.Sprintf("unsupported request type: %s", req.RequestType),
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            req.RequestType,
				Provider:               provider.GetProviderKey(),
				OriginalModelRequested: model,
				ResolvedModelUsed:      model,
			},
		}
	}
}

func (p *PluginPipeline) RunLLMPreHooks(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, int) {

	if skipPluginPipeline, ok := ctx.Value(schemas.BifrostContextKeySkipPluginPipeline).(bool); ok && skipPluginPipeline {
		return req, nil, 0
	}
	var shortCircuit *schemas.LLMPluginShortCircuit
	var err error
	ctx.BlockRestrictedWrites()
	defer ctx.UnblockRestrictedWrites()
	for i, plugin := range p.llmPlugins {
		pluginName := plugin.GetName()
		p.logger.Debug("running pre-hook for plugin %s", pluginName)

		prevSpanID := ctx.Value(schemas.BifrostContextKeySpanID)
		spanID, handle := p.tracer.StartSpanID(ctx, pluginSpanNamesFor(pluginName).prehook, schemas.SpanKindPlugin)

		if spanID != "" {
			ctx.SetValue(schemas.BifrostContextKeySpanID, spanID)
		}

		pluginCtx := ctx.WithPluginScope(&pluginName)
		req, shortCircuit, err = plugin.PreLLMHook(pluginCtx, req)
		pluginCtx.ReleasePluginScope()

		if err != nil {
			p.tracer.SetAttribute(handle, "error", err.Error())
			p.tracer.EndSpan(handle, schemas.SpanStatusError, err.Error())
			p.preHookErrors = append(p.preHookErrors, err)
			p.logger.Warn("error in PreLLMHook for plugin %s: %s", pluginName, err.Error())
		} else if shortCircuit != nil {
			p.tracer.SetAttribute(handle, "short_circuit", true)
			p.tracer.EndSpan(handle, schemas.SpanStatusOk, "short-circuit")
		} else {
			p.tracer.EndSpan(handle, schemas.SpanStatusOk, "")
		}

		ctx.SetValue(schemas.BifrostContextKeySpanID, prevSpanID)

		p.executedPreHooks = i + 1
		if shortCircuit != nil {
			return req, shortCircuit, p.executedPreHooks
		}
	}
	return req, nil, p.executedPreHooks
}

func (p *PluginPipeline) RunPreRequestHooks(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) {

	if skipPluginPipeline, ok := ctx.Value(schemas.BifrostContextKeySkipPluginPipeline).(bool); ok && skipPluginPipeline {
		return
	}
	ctx.BlockRestrictedWrites()
	for _, plugin := range p.llmPlugins {
		pluginName := plugin.GetName()
		p.logger.Debug("running pre-request hook for plugin %s", pluginName)

		prevSpanID := ctx.Value(schemas.BifrostContextKeySpanID)
		spanID, handle := p.tracer.StartSpanID(ctx, pluginSpanNamesFor(pluginName).prerequesthook, schemas.SpanKindPlugin)
		if spanID != "" {
			ctx.SetValue(schemas.BifrostContextKeySpanID, spanID)
		}

		pluginCtx := ctx.WithPluginScope(&pluginName)
		err := plugin.PreRequestHook(pluginCtx, req)
		pluginCtx.ReleasePluginScope()

		if err != nil {
			p.tracer.SetAttribute(handle, "error", err.Error())
			p.tracer.EndSpan(handle, schemas.SpanStatusError, err.Error())
			p.preHookErrors = append(p.preHookErrors, err)
			p.logger.Warn("error in PreRequestHook for plugin %s: %s", pluginName, err.Error())
		} else {
			p.tracer.EndSpan(handle, schemas.SpanStatusOk, "")
		}

		ctx.SetValue(schemas.BifrostContextKeySpanID, prevSpanID)
	}
	ctx.UnblockRestrictedWrites()

	if pin, ok := ctx.Value(schemas.BifrostContextKeyRoutingPinnedAPIKeyID).(string); ok {
		if pin = strings.TrimSpace(pin); pin != "" {
			ctx.SetValue(schemas.BifrostContextKeyAPIKeyID, pin)
		}
	}
}

func (p *PluginPipeline) RunPostLLMHooks(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError, runFrom int) (*schemas.BifrostResponse, *schemas.BifrostError) {

	if skipPluginPipeline, ok := ctx.Value(schemas.BifrostContextKeySkipPluginPipeline).(bool); ok && skipPluginPipeline {
		return resp, bifrostErr
	}

	if runFrom < 0 {
		runFrom = 0
	}
	if runFrom > len(p.llmPlugins) {
		runFrom = len(p.llmPlugins)
	}
	requestType, _, _, _ := GetResponseFields(resp, bifrostErr)

	isStreaming := ctx.Value(schemas.BifrostContextKeyStreamStartTime) != nil && requestType != schemas.RealtimeRequest
	isFinalChunk := isStreaming && IsFinalChunk(ctx)
	ctx.BlockRestrictedWrites()
	defer ctx.UnblockRestrictedWrites()
	var err error
	for i := runFrom - 1; i >= 0; i-- {
		plugin := p.llmPlugins[i]
		pluginName := plugin.GetName()
		p.logger.Debug("running post-hook for plugin %s", pluginName)
		if isStreaming {

			pluginCtx, ok := p.streamScopedCtxs[pluginName]
			if !ok {
				pluginCtx = p.openStreamPostHook(ctx, pluginName)
			}
			start := time.Now()
			resp, bifrostErr, err = plugin.PostLLMHook(pluginCtx, resp, bifrostErr)
			duration := time.Since(start)

			p.accumulatePluginTiming(pluginName, duration, err != nil)
			if isFinalChunk {
				p.endStreamPostHook(pluginName)
			}
			if err != nil {
				p.postHookErrors = append(p.postHookErrors, err)
				p.logger.Warn("error in PostLLMHook for plugin %s: %v", pluginName, err)
			}
		} else {

			prevSpanID := ctx.Value(schemas.BifrostContextKeySpanID)
			spanID, handle := p.tracer.StartSpanID(ctx, pluginSpanNamesFor(pluginName).posthook, schemas.SpanKindPlugin)

			if spanID != "" {
				ctx.SetValue(schemas.BifrostContextKeySpanID, spanID)
			}
			pluginCtx := ctx.WithPluginScope(&pluginName)
			resp, bifrostErr, err = plugin.PostLLMHook(pluginCtx, resp, bifrostErr)
			pluginCtx.ReleasePluginScope()

			if err != nil {
				p.tracer.SetAttribute(handle, "error", err.Error())
				p.tracer.EndSpan(handle, schemas.SpanStatusError, err.Error())
				p.postHookErrors = append(p.postHookErrors, err)
				p.logger.Warn("error in PostLLMHook for plugin %s: %v", pluginName, err)
			} else {
				p.tracer.EndSpan(handle, schemas.SpanStatusOk, "")
			}

			ctx.SetValue(schemas.BifrostContextKeySpanID, prevSpanID)
		}

	}

	if isStreaming {
		p.streamingMu.Lock()
		p.chunkCount++
		p.streamingMu.Unlock()
	}

	if bifrostErr != nil {
		if resp != nil && bifrostErr.StatusCode == nil && bifrostErr.Error != nil && bifrostErr.Error.Type == nil &&
			bifrostErr.Error.Message == "" && bifrostErr.Error.Error == nil {

			return resp, nil
		}
		return resp, bifrostErr
	}
	return resp, nil
}

func (p *PluginPipeline) resetPluginPipeline() {

	p.llmPlugins = nil

	p.executedPreHooks = 0
	clear(p.preHookErrors)
	p.preHookErrors = p.preHookErrors[:0]
	clear(p.postHookErrors)
	p.postHookErrors = p.postHookErrors[:0]

	p.streamingMu.Lock()

	if p.tracer != nil {
		for _, timing := range p.postHookTimings {
			if timing.span != nil {
				p.tracer.EndSpan(timing.span, schemas.SpanStatusError, "stream released before finalizing")
			}
		}
	}
	p.logger = nil
	p.tracer = nil
	p.chunkCount = 0
	if p.postHookTimings != nil {

		clear(p.postHookTimings)
	}

	clear(p.postHookPluginOrder)
	p.postHookPluginOrder = p.postHookPluginOrder[:0]

	for _, scopedCtx := range p.streamScopedCtxs {
		scopedCtx.ReleasePluginScope()
	}
	p.streamScopedCtxs = nil
	p.streamSpanParent = nil
	p.streamingMu.Unlock()
}

func flushPluginLogs(ctx *schemas.BifrostContext) {
	logs := ctx.DrainPluginLogs()
	if len(logs) == 0 {
		return
	}
	tracer, traceID, err := GetTracerFromContext(ctx)
	if err != nil || tracer == nil || traceID == "" {
		return
	}
	tracer.AttachPluginLogs(traceID, logs)
}

func drainAndAttachPluginLogs(ctx *schemas.BifrostContext) {
	if !ctx.HasPluginLogs() {
		return
	}
	tracer, traceID, err := GetTracerFromContext(ctx)
	if err != nil || tracer == nil || traceID == "" {
		return
	}
	logs := ctx.DrainPluginLogs()
	if len(logs) == 0 {
		return
	}
	tracer.AttachPluginLogs(traceID, logs)
}

func (p *PluginPipeline) accumulatePluginTiming(pluginName string, duration time.Duration, hasError bool) {
	p.streamingMu.Lock()
	defer p.streamingMu.Unlock()
	if p.postHookTimings == nil {
		p.postHookTimings = make(map[string]*pluginTimingAccumulator)
	}
	timing, ok := p.postHookTimings[pluginName]
	if !ok {
		timing = &pluginTimingAccumulator{}
		p.postHookTimings[pluginName] = timing

		p.postHookPluginOrder = append(p.postHookPluginOrder, pluginName)
	}
	timing.totalDuration += duration
	timing.invocations++
	if hasError {
		timing.errors++
	}
}

func (p *PluginPipeline) openStreamPostHook(ctx *schemas.BifrostContext, pluginName string) *schemas.BifrostContext {
	if p.streamScopedCtxs == nil {
		p.streamScopedCtxs = make(map[string]*schemas.BifrostContext, len(p.llmPlugins))
	}
	if p.streamSpanParent == nil {

		p.streamSpanParent = ctx
		if traceID, ok := ctx.Value(schemas.BifrostContextKeyTraceID).(string); ok && traceID != "" {
			if id := p.tracer.GetDeferredSpanID(traceID); id != "" {
				p.streamSpanParent = context.WithValue(ctx, schemas.BifrostContextKeySpanID, id)
			}
		}
	}
	spanID, handle := p.tracer.StartSpanID(p.streamSpanParent, pluginSpanNamesFor(pluginName).posthook, schemas.SpanKindPlugin)
	name := pluginName
	pluginCtx := ctx.WithPluginScopeSpan(&name, spanID)
	p.streamScopedCtxs[pluginName] = pluginCtx

	p.streamingMu.Lock()
	if p.postHookTimings == nil {
		p.postHookTimings = make(map[string]*pluginTimingAccumulator)
	}
	if _, ok := p.postHookTimings[pluginName]; !ok {
		p.postHookTimings[pluginName] = &pluginTimingAccumulator{span: handle}
		p.postHookPluginOrder = append(p.postHookPluginOrder, pluginName)
	}
	p.streamingMu.Unlock()
	return pluginCtx
}

func (t *pluginTimingAccumulator) take() (streamPostHookSpan, bool) {
	if t == nil || t.span == nil {
		return streamPostHookSpan{}, false
	}
	s := streamPostHookSpan{span: t.span, totalDuration: t.totalDuration, invocations: t.invocations, errors: t.errors}
	t.span = nil
	return s, true
}

func (s streamPostHookSpan) end(tracer schemas.Tracer, end time.Time) {
	tracer.SetAttribute(s.span, schemas.AttrPluginInvocations, s.invocations)
	if s.invocations > 0 {
		tracer.SetAttribute(s.span, schemas.AttrPluginAvgDurationMs, float64(s.totalDuration.Milliseconds())/float64(s.invocations))
	}
	tracer.SetAttribute(s.span, schemas.AttrPluginTotalDurationMs, s.totalDuration.Milliseconds())
	tracer.SetSpanStartTime(s.span, end.Add(-s.totalDuration))
	if s.errors > 0 {
		tracer.SetAttribute(s.span, schemas.AttrPluginErrorCount, s.errors)
		tracer.EndSpan(s.span, schemas.SpanStatusError, "some invocations failed")
	} else {
		tracer.EndSpan(s.span, schemas.SpanStatusOk, "")
	}
}

func (p *PluginPipeline) endStreamPostHook(pluginName string) {
	p.streamingMu.Lock()
	tracer := p.tracer
	s, ok := p.postHookTimings[pluginName].take()
	p.streamingMu.Unlock()
	if ok && tracer != nil {
		s.end(tracer, time.Now())
	}
}

func (p *PluginPipeline) FinalizeStreamingPostHookSpans(_ context.Context) {

	p.streamingMu.Lock()
	tracer := p.tracer
	if tracer == nil || len(p.postHookPluginOrder) == 0 {
		p.streamingMu.Unlock()
		return
	}
	open := make([]streamPostHookSpan, 0, len(p.postHookPluginOrder))
	for _, pluginName := range p.postHookPluginOrder {
		if s, ok := p.postHookTimings[pluginName].take(); ok {
			open = append(open, s)
		}
	}
	p.streamingMu.Unlock()

	now := time.Now()
	for _, s := range open {
		s.end(tracer, now)
	}
}

func (bifrost *Bifrost) getPluginPipeline() *PluginPipeline {
	pipeline := bifrost.pluginPipelinePool.Get().(*PluginPipeline)
	pipeline.llmPlugins = *bifrost.llmPlugins.Load()

	pipeline.logger = bifrost.logger
	pipeline.tracer = bifrost.getTracer()
	return pipeline
}

func (bifrost *Bifrost) releasePluginPipeline(pipeline *PluginPipeline) {
	pipeline.resetPluginPipeline()
	bifrost.pluginPipelinePool.Put(pipeline)
}

func (bifrost *Bifrost) startCoreSpan(ctx *schemas.BifrostContext, name string) coreSpan {
	t := bifrost.getTracer()
	if t == nil {
		return coreSpan{}
	}
	prev := ctx.Value(schemas.BifrostContextKeySpanID)
	id, h := t.StartSpanID(ctx, name, schemas.SpanKindInternal)
	if h == nil {
		return coreSpan{}
	}
	ctx.SetValue(schemas.BifrostContextKeySpanID, id)
	return coreSpan{h: h, ctx: ctx, prev: prev}
}

func (bifrost *Bifrost) endCoreSpan(cs coreSpan) {
	if cs.h == nil {
		return
	}
	cs.ctx.SetValue(schemas.BifrostContextKeySpanID, cs.prev)
	bifrost.getTracer().EndSpan(cs.h, schemas.SpanStatusOk, "")
}

func startQueueWaitSpan(ctx *schemas.BifrostContext, tracer schemas.Tracer, msg *ChannelMessage) {
	if tracer == nil {
		return
	}
	_, msg.queueSpan = tracer.StartSpanID(ctx, "queue-wait", schemas.SpanKindInternal)
}

func (bifrost *Bifrost) endQueueWaitSpan(msg *ChannelMessage) {
	if msg == nil || msg.queueSpan == nil {
		return
	}
	bifrost.getTracer().EndSpan(msg.queueSpan, schemas.SpanStatusOk, "")
	msg.queueSpan = nil
}

func (bifrost *Bifrost) getChannelMessage(req schemas.BifrostRequest) *ChannelMessage {

	responseChan := bifrost.responseChannelPool.Get().(chan *schemas.BifrostResponse)
	errorChan := bifrost.errorChannelPool.Get().(chan schemas.BifrostError)

	select {
	case <-responseChan:
	default:
	}
	select {
	case <-errorChan:
	default:
	}

	msg := bifrost.channelMessagePool.Get().(*ChannelMessage)
	msg.BifrostRequest = req
	msg.Response = responseChan
	msg.Err = errorChan
	msg.handoff.Store(handoffOpen)

	msg.sentAt = time.Time{}

	if IsStreamRequestType(req.RequestType) {
		responseStreamChan := bifrost.responseStreamPool.Get().(chan chan *schemas.BifrostStreamChunk)

		select {
		case <-responseStreamChan:
		default:
		}
		msg.ResponseStream = responseStreamChan
	}

	return msg
}

func (bifrost *Bifrost) drainQueueWithErrors(pq *ProviderQueue) {
	for {
		select {
		case r := <-pq.queue:

			bifrost.endQueueWaitSpan(r)
			provKey, mod, _ := r.GetRequestFields()

			bifrost.sendWorkerError(r, schemas.BifrostError{
				IsBifrostError: false,
				Error:          &schemas.ErrorField{Message: "provider is shutting down"},
				ExtraFields: schemas.BifrostErrorExtraFields{
					RequestType:            r.RequestType,
					Provider:               provKey,
					OriginalModelRequested: mod,
				},
			})
		default:
			return
		}
	}
}

func (bifrost *Bifrost) releaseChannelMessage(msg *ChannelMessage) {

	bifrost.endQueueWaitSpan(msg)

	select {
	case <-msg.Response:
	default:
	}
	select {
	case <-msg.Err:
	default:
	}

	bifrost.responseChannelPool.Put(msg.Response)
	bifrost.errorChannelPool.Put(msg.Err)

	if msg.ResponseStream != nil {

		select {
		case <-msg.ResponseStream:
		default:
		}
		bifrost.responseStreamPool.Put(msg.ResponseStream)
	}

	msg.BifrostRequest = schemas.BifrostRequest{}
	msg.Context = nil
	msg.Response = nil
	msg.ResponseStream = nil
	msg.Err = nil
	msg.queueSpan = nil
	msg.firstTokenTimeout = 0
	msg.streamPostHooksRan = false
	bifrost.channelMessagePool.Put(msg)
}

func resetBifrostRequest(req *schemas.BifrostRequest) {
	req.RequestType = ""
	req.ListModelsRequest = nil
	req.ModelRetrieveRequest = nil
	req.TextCompletionRequest = nil
	req.ChatRequest = nil
	req.ResponsesRequest = nil
	req.ResponsesRetrieveRequest = nil
	req.ResponsesDeleteRequest = nil
	req.ResponsesCancelRequest = nil
	req.ResponsesInputItemsRequest = nil
	req.CountTokensRequest = nil
	req.CompactionRequest = nil
	req.EmbeddingRequest = nil
	req.RerankRequest = nil
	req.DecisionRequest = nil
	req.OCRRequest = nil
	req.SpeechRequest = nil
	req.TranscriptionRequest = nil
	req.ImageGenerationRequest = nil
	req.ImageEditRequest = nil
	req.ImageVariationRequest = nil
	req.VideoGenerationRequest = nil
	req.VideoEditRequest = nil
	req.VideoRetrieveRequest = nil
	req.VideoDownloadRequest = nil
	req.VideoListRequest = nil
	req.VideoRemixRequest = nil
	req.VideoDeleteRequest = nil
	req.FileUploadRequest = nil
	req.FileListRequest = nil
	req.FileRetrieveRequest = nil
	req.FileDeleteRequest = nil
	req.FileContentRequest = nil
	req.LiveContentRequest = nil
	req.CachedContentCreateRequest = nil
	req.CachedContentListRequest = nil
	req.CachedContentRetrieveRequest = nil
	req.CachedContentUpdateRequest = nil
	req.CachedContentDeleteRequest = nil
	req.BatchCreateRequest = nil
	req.BatchListRequest = nil
	req.BatchRetrieveRequest = nil
	req.BatchCancelRequest = nil
	req.BatchDeleteRequest = nil
	req.BatchResultsRequest = nil
	req.ContainerCreateRequest = nil
	req.ContainerListRequest = nil
	req.ContainerRetrieveRequest = nil
	req.ContainerDeleteRequest = nil
	req.ContainerFileCreateRequest = nil
	req.ContainerFileListRequest = nil
	req.ContainerFileRetrieveRequest = nil
	req.ContainerFileContentRequest = nil
	req.ContainerFileDeleteRequest = nil
	req.PassthroughRequest = nil
}

func (bifrost *Bifrost) getBifrostRequest() *schemas.BifrostRequest {
	req := bifrost.bifrostRequestPool.Get().(*schemas.BifrostRequest)
	return req
}

func (bifrost *Bifrost) releaseBifrostRequest(req *schemas.BifrostRequest) {
	resetBifrostRequest(req)
	bifrost.bifrostRequestPool.Put(req)
}

func filterKeysByID(keys []schemas.Key, target string) []schemas.Key {
	out := make([]schemas.Key, 0, len(keys))
	for _, k := range keys {
		if k.ID == target {
			out = append(out, k)
		}
	}
	return out
}

func (bifrost *Bifrost) getAllSupportedKeys(ctx *schemas.BifrostContext, providerKey schemas.ModelProvider, baseProviderType schemas.ModelProvider) ([]schemas.Key, error) {
	if ctx != nil {
		if key, ok := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key); ok {
			return []schemas.Key{key}, nil
		}
	}

	keys, err := bifrost.account.GetKeysForProvider(ctx, providerKey)
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys found for provider: %v", providerKey)
	}

	var supportedKeys []schemas.Key
	for _, key := range keys {

		if key.Enabled != nil && !*key.Enabled {
			continue
		}
		if err := validateKey(baseProviderType, &key); err != nil {
			bifrost.logger.Warn("error validating key %s (%s) for provider %s: %s, skipping key", key.Name, key.ID, providerKey, err.Error())
			continue
		}
		if strings.TrimSpace(key.Value.GetValue()) != "" || keyValueMayBeEmpty(baseProviderType, &key) {
			supportedKeys = append(supportedKeys, key)
		}
	}

	bifrost.logger.Debug("[Bifrost] Provider %s: %d valid keys found", providerKey, len(supportedKeys))

	if len(supportedKeys) == 0 {
		return nil, fmt.Errorf("no valid keys found for provider: %v", providerKey)
	}

	return supportedKeys, nil
}

func (bifrost *Bifrost) getKeysForBatchAndFileOps(ctx *schemas.BifrostContext, providerKey schemas.ModelProvider, baseProviderType schemas.ModelProvider, model *string, isBatchOp bool) ([]schemas.Key, error) {
	if ctx != nil {
		if key, ok := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key); ok {
			return []schemas.Key{key}, nil
		}
	}

	keys, err := bifrost.account.GetKeysForProvider(ctx, providerKey)
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys found for provider: %v", providerKey)
	}

	var filteredKeys []schemas.Key
	for _, k := range keys {

		if k.Enabled != nil && !*k.Enabled {
			continue
		}

		if isBatchOp && (k.UseForBatchAPI == nil || !*k.UseForBatchAPI) {
			continue
		}

		if err := validateKey(baseProviderType, &k); err != nil {
			bifrost.logger.Warn("error validating key %s (%s) for provider %s: %s, skipping key", k.Name, k.ID, providerKey, err.Error())
			continue
		}

		if model != nil && *model != "" {
			if k.BlacklistedModels.IsBlocked(*model) || !k.Models.IsAllowed(*model) {
				continue
			}
		}

		if strings.TrimSpace(k.Value.GetValue()) != "" || keyValueMayBeEmpty(baseProviderType, &k) {
			filteredKeys = append(filteredKeys, k)
		}
	}

	if len(filteredKeys) == 0 {
		modelStr := ""
		if model != nil {
			modelStr = *model
		}
		if isBatchOp {
			return nil, fmt.Errorf("no batch-enabled keys found for provider: %v and model: %s", providerKey, modelStr)
		}
		return nil, fmt.Errorf("no keys found for provider: %v and model: %s", providerKey, modelStr)
	}

	sort.Slice(filteredKeys, func(i, j int) bool {
		return filteredKeys[i].ID < filteredKeys[j].ID
	})

	return filteredKeys, nil
}

func (bifrost *Bifrost) selectKeyFromProviderForModelWithPool(ctx *schemas.BifrostContext, requestType schemas.RequestType, providerKey schemas.ModelProvider, model string, baseProviderType schemas.ModelProvider, additionalModels ...string) ([]schemas.Key, bool, error) {

	if ctx != nil {
		if key, ok := ctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key); ok {
			return []schemas.Key{key}, false, nil
		}
	}

	if skipKeySelection, ok := ctx.Value(schemas.BifrostContextKeySkipKeySelection).(bool); ok && skipKeySelection && isKeySkippingAllowed(baseProviderType, requestType) {
		return []schemas.Key{}, false, nil
	}

	keys, err := bifrost.account.GetKeysForProvider(ctx, providerKey)
	if err != nil {
		return nil, false, err
	}
	if len(keys) == 0 {
		return nil, false, fmt.Errorf("no keys found for provider: %v and model: %s", providerKey, model)
	}

	if isBatchRequestType(requestType) || isFileRequestType(requestType) {
		var batchEnabledKeys []schemas.Key
		for _, k := range keys {
			if k.UseForBatchAPI != nil && *k.UseForBatchAPI {
				batchEnabledKeys = append(batchEnabledKeys, k)
			}
		}
		if len(batchEnabledKeys) == 0 {
			return nil, false, fmt.Errorf("no config found for batch apis; enable 'Use for Batch APIs' on at least one key for provider: %v", providerKey)
		}
		keys = batchEnabledKeys
	}

	var supportedKeys []schemas.Key

	skipModelCheck := (model == "" && (isFileRequestType(requestType) || isBatchRequestType(requestType) || isContainerRequestType(requestType) || isCachedContentRequestType(requestType) || isModellessVideoRequestType(requestType) || isPassthroughRequestType(requestType) || requestType == schemas.LiveRequest || requestType == schemas.LiveContentRequest)) || requestType == schemas.ListModelsRequest || isResponsesLifecycleRequestType(requestType)
	if skipModelCheck {

		for _, key := range keys {

			if key.Enabled != nil && !*key.Enabled {
				continue
			}
			if err := validateKey(baseProviderType, &key); err != nil {
				bifrost.logger.Warn("error validating key %s (%s) for provider %s: %s, skipping key", key.Name, key.ID, providerKey, err.Error())
				continue
			}
			if strings.TrimSpace(key.Value.GetValue()) != "" || keyValueMayBeEmpty(baseProviderType, &key) {
				supportedKeys = append(supportedKeys, key)
			}
		}
	} else {

		for _, key := range keys {

			if key.Enabled != nil && !*key.Enabled {
				continue
			}
			if err := validateKey(baseProviderType, &key); err != nil {
				bifrost.logger.Warn("error validating key %s (%s) for provider %s: %s, skipping key", key.Name, key.ID, providerKey, err.Error())
				continue
			}
			hasValue := strings.TrimSpace(key.Value.GetValue()) != "" || keyValueMayBeEmpty(baseProviderType, &key)

			if hasValue && keySupportsModel(baseProviderType, &key, model) {
				supportedKeys = append(supportedKeys, key)
			}
		}
	}
	if len(supportedKeys) == 0 {
		return nil, false, fmt.Errorf("no keys found that support model: %s", model)
	}
	for _, additionalModel := range additionalModels {
		if additionalModel == "" {
			continue
		}
		supportedKeys = slices.DeleteFunc(supportedKeys, func(key schemas.Key) bool {
			return !keySupportsModel(baseProviderType, &key, additionalModel)
		})
		if len(supportedKeys) == 0 {
			return nil, false, fmt.Errorf("no keys found for provider %s that support both model %s and model %s", providerKey, model, additionalModel)
		}
	}

	if ctx != nil {
		if keyID, ok := ctx.Value(schemas.BifrostContextKeyAPIKeyID).(string); ok {
			if keyID = strings.TrimSpace(keyID); keyID != "" {
				for _, key := range supportedKeys {
					if key.ID == keyID {
						return []schemas.Key{key}, false, nil
					}
				}
				return nil, false, fmt.Errorf("no supported key found with id %q for provider: %v and model: %s", keyID, providerKey, model)
			}
		}
		if keyName, ok := ctx.Value(schemas.BifrostContextKeyAPIKeyName).(string); ok {
			if keyName = strings.TrimSpace(keyName); keyName != "" {
				for _, key := range supportedKeys {
					if key.Name == keyName {
						return []schemas.Key{key}, false, nil
					}
				}
				return nil, false, fmt.Errorf("no supported key found with name %q for provider: %v and model: %s", keyName, providerKey, model)
			}
		}
	}

	if len(supportedKeys) == 1 {
		if len(additionalModels) == 0 &&
			requestType != schemas.RealtimeRequest &&
			requestType != schemas.WebSocketResponsesRequest &&
			!isBatchRequestType(requestType) &&
			!isFileRequestType(requestType) &&
			schemas.IsSessionAffinityActive(ctx) {
			bifrost.sessionAffinity.ResolveKey(ctx, providerKey, model, supportedKeys)
		}
		return []schemas.Key{supportedKeys[0]}, false, nil
	}

	if schemas.IsSessionAffinityActive(ctx) {
		if key, ok := bifrost.sessionAffinity.ResolveKey(ctx, providerKey, model, supportedKeys); ok {
			for _, eligible := range supportedKeys {
				if eligible.ID == key.ID {

					pool := []schemas.Key{eligible}
					for _, other := range supportedKeys {
						if other.ID != eligible.ID {
							pool = append(pool, other)
						}
					}
					return pool, false, nil
				}
			}
			bifrost.logger.Warn("session affinity named key %s for provider %s, which is not in the eligible pool; selecting a key normally", key.ID, providerKey)
		}
	}

	return supportedKeys, true, nil
}

func keySupportsModel(baseProviderType schemas.ModelProvider, key *schemas.Key, model string) bool {
	if !key.Models.IsAllowed(model) || key.BlacklistedModels.IsBlocked(model) {
		return false
	}
	if baseProviderType == schemas.VLLM && key.VLLMKeyConfig != nil && key.VLLMKeyConfig.ModelName != "" {
		return key.VLLMKeyConfig.ModelName == key.Aliases.Resolve(model)
	}
	return true
}

func (bifrost *Bifrost) Shutdown() {
	bifrost.providerLifecycleMu.Lock()
	defer bifrost.providerLifecycleMu.Unlock()

	bifrost.logger.Info("closing all request channels...")

	if bifrost.ctx.Err() == nil && bifrost.cancel != nil {
		bifrost.cancel()
	}

	bifrost.requestQueues.Range(func(key, value interface{}) bool {
		pq := value.(*ProviderQueue)
		pq.signalClosing()
		return true
	})

	bifrost.waitGroups.Range(func(key, value interface{}) bool {
		waitGroup := value.(*sync.WaitGroup)
		waitGroup.Wait()
		return true
	})

	bifrost.oldWorkerCleanups.Wait()

	bifrost.requestQueues.Range(func(key, value interface{}) bool {
		bifrost.drainQueueWithErrors(value.(*ProviderQueue))
		return true
	})

	if tracerWrapper := bifrost.tracer.Load().(*tracerWrapper); tracerWrapper != nil && tracerWrapper.tracer != nil {
		tracerWrapper.tracer.Stop()
	}

	if llmPlugins := bifrost.llmPlugins.Load(); llmPlugins != nil {
		for _, plugin := range *llmPlugins {
			err := plugin.Cleanup()
			if err != nil {
				bifrost.logger.Warn(fmt.Sprintf("Error cleaning up LLM plugin: %s", err.Error()))
			}
		}
	}

	bifrost.logger.Info("all request channels closed")
}
