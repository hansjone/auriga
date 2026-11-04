// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type PassthroughStreamParams struct {
	StatusCode int
	Headers    map[string]string
	Path       string

	RawRequest []byte

	CancellationBody []byte
	StartTime        time.Time

	UseTerminalDetector bool

	EndsStream func(event []byte) bool

	SkipFraming bool
	Logger      schemas.Logger

	HasUsage func(event []byte) bool

	Observe func(event []byte) *schemas.BifrostPassthroughUsage
}

func StreamPassthrough(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	resp *fasthttp.Response,
	rawBodyStream io.Reader,
	params PassthroughStreamParams,
) chan *schemas.BifrostStreamChunk {
	return streamPassthrough(ctx, postHookRunner, postHookSpanFinalizer, func() { ReleaseStreamingResponse(ctx, resp) }, rawBodyStream, params)
}

func streamPassthrough(
	ctx *schemas.BifrostContext,
	postHookRunner schemas.PostHookRunner,
	postHookSpanFinalizer func(context.Context),
	release func(),
	rawBodyStream io.Reader,
	params PassthroughStreamParams,
) chan *schemas.BifrostStreamChunk {

	bodyStream, stopIdleTimeout := NewIdleTimeoutReader(rawBodyStream, rawBodyStream, GetStreamIdleTimeout(ctx), ctx)

	stopCancellation := SetupStreamCancellation(ctx, rawBodyStream, params.Logger)

	extraFields := schemas.BifrostResponseExtraFields{
		ProviderResponseHeaders: params.Headers,
		PassthroughPath:         params.Path,
	}

	ch := make(chan *schemas.BifrostStreamChunk, schemas.DefaultStreamBufferSize)
	go func() {
		defer EnsureStreamFinalizerCalled(ctx, postHookSpanFinalizer)
		defer func() {
			if ctx.Err() == context.Canceled {
				HandleStreamCancellation(ctx, postHookRunner, ch, params.Logger, postHookSpanFinalizer, params.CancellationBody)
			} else if ctx.Err() == context.DeadlineExceeded {
				HandleStreamTimeout(ctx, postHookRunner, ch, params.Logger, postHookSpanFinalizer, params.CancellationBody)
			}
			CloseStream(ctx, ch)
		}()
		defer release()
		defer stopIdleTimeout()
		defer stopCancellation()

		var pending bytes.Buffer
		var usage *schemas.BifrostPassthroughUsage

		success := params.StatusCode >= 200 && params.StatusCode < 300

		observe := func(payload []byte) (terminal bool) {
			if len(bytes.TrimSpace(payload)) == 0 {
				return false
			}

			if success && params.Observe != nil && (params.HasUsage == nil || params.HasUsage(payload)) {
				if u := params.Observe(payload); u != nil {
					usage = u
				}
			}
			if params.EndsStream != nil && params.EndsStream(payload) {

				ctx.SetValue(schemas.BifrostContextKeyStreamParkedAfterFinish, true)
				return true
			}
			return params.UseTerminalDetector && isTerminalSSEPayload(payload)
		}

		observeUndelimited := func(payload []byte) bool {
			p := bytes.TrimSpace(payload)
			if len(p) == 0 {
				return false
			}

			if last := p[len(p)-1]; last != ']' && last != '}' {
				return false
			}
			if p[0] == '[' {
				var elems []json.RawMessage
				if sonic.Unmarshal(p, &elems) != nil {
					return false
				}
				terminal := false
				for _, elem := range elems {
					if observe(elem) {
						terminal = true
					}
				}
				return terminal
			}
			var obj map[string]any
			if sonic.Unmarshal(p, &obj) != nil {
				return false
			}
			return observe(p)
		}

		drainFrames := func() bool {
			for {
				data := pending.Bytes()
				idx, delimLen := findFirstSSEFrameDelimiter(data)
				if idx < 0 {
					break
				}
				frame := append([]byte(nil), data[:idx]...)
				pending.Next(idx + delimLen)
				if observe(extractSSEDataPayload(frame)) {
					return true
				}
			}
			if params.UseTerminalDetector && observeUndelimited(pending.Bytes()) {
				return true
			}

			if pending.Len() > sseMaxBufSize {
				drain := pending.Bytes()
				if idx, delimLen := findLastSSEFrameDelimiter(drain); idx >= 0 {
					pending.Next(idx + delimLen)
				} else {
					pending.Reset()
				}
			}
			return false
		}

		finalize := func() {
			ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
			extraFields.Latency = time.Since(params.StartTime).Milliseconds()
			extraFields.RawRequest = params.RawRequest
			ProcessAndSendResponse(ctx, postHookRunner, &schemas.BifrostResponse{
				PassthroughResponse: &schemas.BifrostPassthroughResponse{
					StatusCode:       params.StatusCode,
					Headers:          params.Headers,
					ExtraFields:      extraFields,
					PassthroughUsage: usage,
				},
			}, ch, postHookSpanFinalizer)
		}

		buf := make([]byte, 4096)
		for {
			n, readErr := bodyStream.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])

				ProcessAndSendResponse(ctx, postHookRunner, &schemas.BifrostResponse{
					PassthroughResponse: &schemas.BifrostPassthroughResponse{
						StatusCode:  params.StatusCode,
						Headers:     params.Headers,
						Body:        chunk,
						ExtraFields: extraFields,
					},
				}, ch, postHookSpanFinalizer)

				if !params.SkipFraming {
					pending.Write(chunk)
					if drainFrames() {
						finalize()
						return
					}
				}
			}
			if readErr == io.EOF {

				if pending.Len() > 0 {
					observe(extractSSEDataPayload(pending.Bytes()))
				}
				finalize()
				return
			}
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}
				ctx.SetValue(schemas.BifrostContextKeyStreamEndIndicator, true)
				extraFields.Latency = time.Since(params.StartTime).Milliseconds()
				ProcessAndSendError(ctx, postHookRunner, readErr, ch, params.Logger, postHookSpanFinalizer)
				return
			}
		}
	}()

	return ch
}

func isTerminalSSEPayload(payload []byte) bool {
	p := bytes.TrimSpace(payload)
	if len(p) == 0 {
		return false
	}
	if bytes.Equal(p, sseDoneMarker) {
		return true
	}
	return hasFinishReasonMarker(p)
}
