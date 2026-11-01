// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"context"
	"time"
)

var _ Tracer = (*NoOpTracer)(nil)

type Tracer interface {
	CreateTrace(parentID string, requestID ...string) string

	EndTrace(traceID string) *Trace

	ReleaseTrace(trace *Trace)

	StartSpan(ctx context.Context, name string, kind SpanKind) (context.Context, SpanHandle)

	StartSpanID(ctx context.Context, name string, kind SpanKind) (string, SpanHandle)

	EndSpan(handle SpanHandle, status SpanStatus, statusMsg string)

	SetAttribute(handle SpanHandle, key string, value any)

	SetSpanStartTime(handle SpanHandle, start time.Time)

	MergeTraceDimensions(traceID string, dims map[string]string)

	SpanFromHandle(handle SpanHandle) *Span

	GetSpanHandleByID(traceID string, spanID *string) SpanHandle

	AddEvent(handle SpanHandle, name string, attrs map[string]any)

	PopulateLLMRequestAttributes(handle SpanHandle, req *BifrostRequest)

	PopulateLLMResponseAttributes(ctx *BifrostContext, handle SpanHandle, resp *BifrostResponse, err *BifrostError)

	DeferTraceCompletion(traceID string)

	ClearTraceCompletionDeferral(traceID string)

	AwaitTransportHandoff(traceID string)

	HoldTraceFlush(traceID string) (release func())

	StoreDeferredSpan(traceID string, handle SpanHandle)

	GetDeferredSpanHandle(traceID string) SpanHandle

	ClearDeferredSpan(traceID string)

	GetDeferredSpanID(traceID string) string

	AddStreamingChunk(traceID string, response *BifrostResponse)

	GetAccumulatedChunks(traceID string) (response *BifrostResponse, ttftNs int64, chunkCount int)

	CreateStreamAccumulator(traceID string, startTime time.Time)

	CleanupStreamAccumulator(traceID string)

	ResetStreamAccumulator(traceID string)

	ProcessStreamingChunk(ctx *BifrostContext, traceID string, isFinalChunk bool, result *BifrostResponse, err *BifrostError) *StreamAccumulatorResult

	PauseStream(traceID string)

	ResumeStream(traceID string)

	EndStream(traceID string, err *BifrostError)

	WaitForFlusher(traceID string)

	IsStreamEnded(traceID string) bool

	IsStreamPaused(traceID string) bool

	GetAccumulatedResponse(traceID string) *BifrostResponse

	GateSend(traceID string, chunk *BifrostStreamChunk, isFinal, isHardErr bool, ch chan *BifrostStreamChunk, ctx *BifrostContext) bool

	AttachPluginLogs(traceID string, logs []PluginLogEntry)

	SetTraceRedactionReplacements(traceID string, phase RedactionPhase, replacements map[string]string)

	CompleteAndFlushTrace(traceID string)

	Stop()
}
type StreamAccumulatorResult struct {
	RequestID        string
	RequestedModel   string
	ResolvedModel    string
	Provider         ModelProvider
	Status           string
	Latency          int64
	TimeToFirstToken int64
	OutputMessage    *ChatMessage
	OutputMessages   []ResponsesMessage
	TokenUsage       *BifrostLLMUsage
	ServiceTier      *BifrostServiceTier
	Cost             *float64

	CacheDebug            *BifrostCacheMetadata
	GuardrailDebug        *BifrostGuardrailMetadata
	ErrorDetails          *BifrostError
	AudioOutput           *BifrostSpeechResponse
	TranscriptionOutput   *BifrostTranscriptionResponse
	ImageGenerationOutput *BifrostImageGenerationResponse
	PassthroughOutput     *BifrostPassthroughResponse
	FinishReason          *string
	RawResponse           *string
	RawRequest            interface{}
}
type SpanHandle interface{}
type NoOpTracer struct{}

func (n *NoOpTracer) CreateTrace(_ string, _ ...string) string { return "" }

func (n *NoOpTracer) EndTrace(_ string) *Trace { return nil }

func (n *NoOpTracer) ReleaseTrace(_ *Trace) {}

func (n *NoOpTracer) StartSpan(ctx context.Context, _ string, _ SpanKind) (context.Context, SpanHandle) {
	return ctx, nil
}

func (n *NoOpTracer) StartSpanID(_ context.Context, _ string, _ SpanKind) (string, SpanHandle) {
	return "", nil
}

func (n *NoOpTracer) EndSpan(_ SpanHandle, _ SpanStatus, _ string) {}

func (n *NoOpTracer) SetAttribute(_ SpanHandle, _ string, _ any) {}

func (n *NoOpTracer) SetSpanStartTime(_ SpanHandle, _ time.Time) {}

func (n *NoOpTracer) MergeTraceDimensions(_ string, _ map[string]string) {}

func (n *NoOpTracer) SpanFromHandle(_ SpanHandle) *Span { return nil }

func (n *NoOpTracer) GetSpanHandleByID(_ string, _ *string) SpanHandle { return nil }

func (n *NoOpTracer) AddEvent(_ SpanHandle, _ string, _ map[string]any) {}

func (n *NoOpTracer) PopulateLLMRequestAttributes(_ SpanHandle, _ *BifrostRequest) {}

func (n *NoOpTracer) PopulateLLMResponseAttributes(_ *BifrostContext, _ SpanHandle, _ *BifrostResponse, _ *BifrostError) {
}

func (n *NoOpTracer) StoreDeferredSpan(_ string, _ SpanHandle) {}

func (n *NoOpTracer) DeferTraceCompletion(_ string) {}

func (n *NoOpTracer) ClearTraceCompletionDeferral(_ string) {}

func (n *NoOpTracer) AwaitTransportHandoff(_ string) {}

func (n *NoOpTracer) HoldTraceFlush(_ string) func() { return func() {} }

func (n *NoOpTracer) GetDeferredSpanHandle(_ string) SpanHandle { return nil }

func (n *NoOpTracer) ClearDeferredSpan(_ string) {}

func (n *NoOpTracer) GetDeferredSpanID(_ string) string { return "" }

func (n *NoOpTracer) AddStreamingChunk(_ string, _ *BifrostResponse) {}

func (n *NoOpTracer) GetAccumulatedChunks(_ string) (*BifrostResponse, int64, int) { return nil, 0, 0 }

func (n *NoOpTracer) CreateStreamAccumulator(_ string, _ time.Time) {}

func (n *NoOpTracer) CleanupStreamAccumulator(_ string) {}

func (n *NoOpTracer) ResetStreamAccumulator(_ string) {}

func (n *NoOpTracer) ProcessStreamingChunk(_ *BifrostContext, _ string, _ bool, _ *BifrostResponse, _ *BifrostError) *StreamAccumulatorResult {
	return nil
}

func (n *NoOpTracer) PauseStream(_ string) {}

func (n *NoOpTracer) ResumeStream(_ string) {}

func (n *NoOpTracer) EndStream(_ string, _ *BifrostError) {}

func (n *NoOpTracer) WaitForFlusher(_ string) {}

func (n *NoOpTracer) IsStreamEnded(_ string) bool { return false }

func (n *NoOpTracer) IsStreamPaused(_ string) bool { return false }

func (n *NoOpTracer) GetAccumulatedResponse(_ string) *BifrostResponse { return nil }

func (n *NoOpTracer) GateSend(_ string, chunk *BifrostStreamChunk, _ bool, _ bool, ch chan *BifrostStreamChunk, ctx *BifrostContext) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if ctx == nil {
		ch <- chunk
		return true
	}
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

func (n *NoOpTracer) AttachPluginLogs(_ string, _ []PluginLogEntry) {}

func (n *NoOpTracer) SetTraceRedactionReplacements(_ string, _ RedactionPhase, _ map[string]string) {}

func (n *NoOpTracer) CompleteAndFlushTrace(_ string) {}

func (n *NoOpTracer) Stop() {}

func DefaultTracer() Tracer {
	return &NoOpTracer{}
}
