// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/valyala/fasthttp"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

type largePayloadResult struct {
	Usage        *schemas.BifrostLLMUsage
	Latency      int64
	ResponseBody []byte
}

func setStreamingRequestBody(ctx *schemas.BifrostContext, req *fasthttp.Request, jsonBody []byte, providerName schemas.ModelProvider) {
	if !providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, providerName) {
		req.SetBody(jsonBody)
	}
}

func handleOpenAILargePayloadPassthrough(
	ctx *schemas.BifrostContext,
	client *fasthttp.Client,
	url string,
	authHeader map[string]string,
	extraHeaders map[string]schemas.SecretVar,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) (*largePayloadResult, *schemas.BifrostError, bool) {
	isLargePayload, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool)
	if !isLargePayload {
		return nil, nil, false
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)

	providerUtils.SetExtraHeaders(ctx, req, extraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodPost)
	for k, v := range authHeader {
		req.Header.Set(k, v)
	}

	if !providerUtils.ApplyLargePayloadRequestBodyWithModelNormalization(ctx, req, providerName) {
		fasthttp.ReleaseResponse(resp)
		return nil, nil, false
	}
	if len(req.Header.ContentType()) == 0 {
		req.Header.SetContentType("application/json")
	}

	activeClient := providerUtils.PrepareResponseStreaming(ctx, client, resp)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, activeClient, req, resp)
	wait()
	if bifrostErr != nil {
		fasthttp.ReleaseResponse(resp)
		return nil, bifrostErr, true
	}

	if headers := providerUtils.ExtractProviderResponseHeaders(resp); headers != nil {
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, headers)
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		providerUtils.MaterializeStreamErrorBody(ctx, resp)
		parsedErr := ParseOpenAIError(resp)
		fasthttp.ReleaseResponse(resp)
		return nil, parsedErr, true
	}

	body, result, respErr := finalizeOpenAIResponse(ctx, resp, latency, providerName, logger)
	if respErr != nil {
		return nil, respErr, true
	}
	if result != nil {
		return result, nil, true
	}

	usage := extractOpenAIUsageFromBytes(body)
	return &largePayloadResult{Usage: usage, Latency: latency.Milliseconds(), ResponseBody: body}, nil, true
}

func finalizeOpenAIResponse(
	ctx *schemas.BifrostContext,
	resp *fasthttp.Response,
	latency time.Duration,
	providerName schemas.ModelProvider,
	logger schemas.Logger,
) ([]byte, *largePayloadResult, *schemas.BifrostError) {
	body, isLarge, bifrostErr := providerUtils.FinalizeResponseWithLargeDetection(ctx, resp, logger)
	if bifrostErr != nil {
		fasthttp.ReleaseResponse(resp)
		return nil, nil, bifrostErr
	}
	if isLarge {

		preview, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadResponsePreview).(string)
		usage := extractOpenAIUsageFromBytes([]byte(preview))

		return nil, &largePayloadResult{Usage: usage, Latency: latency.Milliseconds()}, nil
	}

	fasthttp.ReleaseResponse(resp)
	return body, nil, nil
}

func extractOpenAIUsageFromBytes(data []byte) *schemas.BifrostLLMUsage {
	node, err := sonic.Get(data, "usage")
	if err != nil {
		return nil
	}
	raw, err := node.Raw()
	if err != nil || raw == "" {
		return nil
	}
	return providerUtils.ParseOpenAIUsageFromBytes([]byte(raw))
}
