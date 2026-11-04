// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import (
	"bytes"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type LargeResponseReader struct {
	io.Reader
	Resp     *fasthttp.Response
	ctx      *schemas.BifrostContext
	cleanup  func()
	consumed bool
}

func (r *LargeResponseReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.consumed = true
	}
	return n, err
}

func (r *LargeResponseReader) Close() error {
	if r == nil || r.Resp == nil {
		return nil
	}

	if r.cleanup != nil {
		r.cleanup()
		r.cleanup = nil
	}
	if r.ctx != nil {
		if closed, ok := r.ctx.Value(schemas.BifrostContextKeyConnectionClosed).(bool); ok && closed {
			r.Resp = nil
			return nil
		}
	}
	if !r.consumed {
		if bodyStream := r.Resp.BodyStream(); bodyStream != nil {
			_, _ = io.Copy(io.Discard, bodyStream)
			if closer, ok := bodyStream.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}
	fasthttp.ReleaseResponse(r.Resp)
	r.Resp = nil
	return nil
}

func BuildLargeResponseClient(base *fasthttp.Client, responseThreshold int64) *fasthttp.Client {
	client := CloneFastHTTPClientConfig(base)
	if responseThreshold > 0 && responseThreshold <= int64(math.MaxInt) {
		client.MaxResponseBodySize = int(responseThreshold)
	}
	client.StreamResponseBody = true
	client.MaxConnDuration = 0
	if client.Transport == nil {
		client.Transport = NewContextTransport()
	}
	return client
}

func PrepareResponseStreaming(ctx *schemas.BifrostContext, client *fasthttp.Client, resp *fasthttp.Response) *fasthttp.Client {
	responseThreshold, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64)
	if responseThreshold <= 0 {
		return client
	}
	resp.StreamBody = true
	return BuildLargeResponseClient(client, responseThreshold)
}

func MaterializeStreamErrorBody(ctx *schemas.BifrostContext, resp *fasthttp.Response) {
	responseThreshold, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64)
	if responseThreshold <= 0 {
		return
	}
	if bodyStream := resp.BodyStream(); bodyStream != nil {
		gz, reader, wasGzip := decompressBodyStreamIfGzip(resp, bodyStream)
		if wasGzip {
			defer ReleaseGzipReader(gz)
		}
		bodyBytes, readErr := io.ReadAll(io.LimitReader(reader, 512*1024))
		if readErr != nil {
			return
		}
		resp.SetBody(bodyBytes)
	}
}

func FinalizeResponseWithLargeDetection(
	ctx *schemas.BifrostContext,
	resp *fasthttp.Response,
	logger schemas.Logger,
) (body []byte, isLarge bool, finalizeErr *schemas.BifrostError) {

	if ft, fh := startPhaseSpan(ctx, "response-finalize"); ft != nil {
		defer func() {
			if finalizeErr != nil {
				ft.EndSpan(fh, schemas.SpanStatusError, "response finalize failed")
			} else {
				ft.EndSpan(fh, schemas.SpanStatusOk, "")
			}
		}()
	}

	responseThreshold, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64)

	if responseThreshold <= 0 {
		body, err := CheckAndDecodeBody(resp)
		if err != nil {
			return nil, false, NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
		}

		return body, false, nil
	}

	contentLength := resp.Header.ContentLength()

	bodyStream := resp.BodyStream()
	if bodyStream == nil {

		if logger != nil && contentLength > 0 && int64(contentLength) > responseThreshold {
			logger.Warn("large-response fallback to buffered path: content_length=%d threshold=%d body_stream_nil=true", contentLength, responseThreshold)
		}
		body, err := CheckAndDecodeBody(resp)
		if err != nil {
			return nil, false, NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
		}
		return body, false, nil
	}

	gz, reader, wasGzip := decompressBodyStreamIfGzip(resp, bodyStream)
	if wasGzip {

		contentLength = -1
	}
	reader, stopIdleTimeout := NewIdleTimeoutReader(reader, bodyStream, GetStreamIdleTimeout(ctx), ctx)
	stopCancellation := SetupStreamCancellation(ctx, bodyStream, logger)
	cleanup := func() {
		stopCancellation()
		stopIdleTimeout()
		if wasGzip {
			ReleaseGzipReader(gz)
		}
	}

	if contentLength > 0 && int64(contentLength) <= responseThreshold {
		bodyBytes, readErr := io.ReadAll(reader)
		cleanup()
		if readErr != nil {
			return nil, false, largeResponseReadError(readErr)
		}
		return bodyBytes, false, nil
	}

	if contentLength <= 0 {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(reader, responseThreshold+1))
		if readErr != nil {
			cleanup()
			return nil, false, largeResponseReadError(readErr)
		}
		if int64(len(bodyBytes)) <= responseThreshold {
			cleanup()
			return bodyBytes, false, nil
		}

		combinedReader := io.MultiReader(bytes.NewReader(bodyBytes), reader)
		closableReader := &LargeResponseReader{
			Reader:  combinedReader,
			Resp:    resp,
			ctx:     ctx,
			cleanup: cleanup,
		}
		ctx.SetValue(schemas.BifrostContextKeyLargeResponseMode, true)
		ctx.SetValue(schemas.BifrostContextKeyLargeResponseReader, closableReader)
		ctx.SetValue(schemas.BifrostContextKeyLargeResponseContentLength, contentLength)
		if ct := string(resp.Header.ContentType()); ct != "" {
			ctx.SetValue(schemas.BifrostContextKeyLargeResponseContentType, ct)
		}
		previewLen := min(len(bodyBytes), 1048576)
		ctx.SetValue(schemas.BifrostContextKeyLargePayloadResponsePreview, string(trimIncompleteRune(bodyBytes[:previewLen])))
		return nil, true, nil
	}

	prefetchSize := 64 * 1024
	if ps, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadPrefetchSize).(int); ok && ps > 0 {
		prefetchSize = ps
	}
	prefetchBuf := make([]byte, prefetchSize)
	n, readErr := io.ReadFull(reader, prefetchBuf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		cleanup()
		return nil, false, largeResponseReadError(readErr)
	}
	prefetchBuf = prefetchBuf[:n]

	combinedReader := io.MultiReader(bytes.NewReader(prefetchBuf), reader)
	closableReader := &LargeResponseReader{
		Reader:  combinedReader,
		Resp:    resp,
		ctx:     ctx,
		cleanup: cleanup,
	}

	ctx.SetValue(schemas.BifrostContextKeyLargeResponseMode, true)
	ctx.SetValue(schemas.BifrostContextKeyLargeResponseReader, closableReader)
	ctx.SetValue(schemas.BifrostContextKeyLargeResponseContentLength, contentLength)
	if ct := string(resp.Header.ContentType()); ct != "" {
		ctx.SetValue(schemas.BifrostContextKeyLargeResponseContentType, ct)
	}
	previewLen := min(n, 1048576)
	ctx.SetValue(schemas.BifrostContextKeyLargePayloadResponsePreview, string(trimIncompleteRune(prefetchBuf[:previewLen])))

	return nil, true, nil
}

func largeResponseReadError(readErr error) *schemas.BifrostError {
	if errors.Is(readErr, ErrStreamIdleTimeout) {
		return NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, readErr)
	}
	return NewBifrostOperationError(schemas.ErrProviderResponseDecode, readErr)
}

func ParseOpenAIUsageFromBytes(data []byte) *schemas.BifrostLLMUsage {
	var usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`

		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	if err := sonic.Unmarshal(data, &usage); err != nil {
		return nil
	}

	result := &schemas.BifrostLLMUsage{}
	if usage.PromptTokens > 0 {
		result.PromptTokens = usage.PromptTokens
	} else if usage.InputTokens > 0 {
		result.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens > 0 {
		result.CompletionTokens = usage.CompletionTokens
	} else if usage.OutputTokens > 0 {
		result.CompletionTokens = usage.OutputTokens
	}
	if usage.TotalTokens > 0 {
		result.TotalTokens = usage.TotalTokens
	} else {
		result.TotalTokens = result.PromptTokens + result.CompletionTokens
	}

	if result.TotalTokens == 0 {
		return nil
	}
	return result
}

func SetupStreamingPassthrough(ctx *schemas.BifrostContext, resp *fasthttp.Response) bool {
	isLargePayload, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool)
	if !isLargePayload {
		return false
	}

	reader, releaseGzip := DecompressStreamBody(resp)

	reader, stopIdleTimeout := NewIdleTimeoutReader(reader, resp.BodyStream(), GetStreamIdleTimeout(ctx), ctx)

	stopCancellation := SetupStreamCancellation(ctx, resp.BodyStream(), nil)

	closableReader := &LargeResponseReader{
		Reader: reader,
		Resp:   resp,
		ctx:    ctx,
		cleanup: func() {
			stopCancellation()
			stopIdleTimeout()
			releaseGzip()
		},
	}

	ctx.SetValue(schemas.BifrostContextKeyLargeResponseMode, true)
	ctx.SetValue(schemas.BifrostContextKeyLargeResponseReader, closableReader)
	if ct := string(resp.Header.ContentType()); ct != "" {
		ctx.SetValue(schemas.BifrostContextKeyLargeResponseContentType, ct)
	}
	return true
}

func trimIncompleteRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			return b
		}
	}
	return b
}
