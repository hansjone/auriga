// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func ToGeminiBatchGenerateContentRequest(body map[string]interface{}) (GeminiBatchGenerateContentRequest, error) {
	var geminiReq GeminiBatchGenerateContentRequest

	requestBytes, err := providerUtils.MarshalSorted(body)
	if err != nil {
		return geminiReq, fmt.Errorf("failed to marshal gemini request: %w", err)
	}
	if err := sonic.Unmarshal(requestBytes, &geminiReq); err != nil {
		return geminiReq, fmt.Errorf("failed to unmarshal gemini request: %w", err)
	}

	if rawMessages, ok := body["messages"]; ok {
		messagesBytes, err := providerUtils.MarshalSorted(rawMessages)
		if err != nil {
			return geminiReq, fmt.Errorf("failed to marshal messages: %w", err)
		}
		var chatMessages []schemas.ChatMessage
		if err := sonic.Unmarshal(messagesBytes, &chatMessages); err != nil {
			return geminiReq, fmt.Errorf("failed to unmarshal messages: %w", err)
		}
		contents, systemInstruction, err := convertBifrostMessagesToGemini(chatMessages)
		if err != nil {
			return geminiReq, fmt.Errorf("failed to convert messages: %w", err)
		}
		geminiReq.Contents = contents
		geminiReq.SystemInstruction = systemInstruction
	}

	return geminiReq, nil
}

func ToBifrostBatchStatus(geminiState string) schemas.BatchStatus {
	switch geminiState {
	case GeminiBatchStatePending, GeminiBatchStateRunning:
		return schemas.BatchStatusInProgress
	case GeminiBatchStateSucceeded:
		return schemas.BatchStatusCompleted
	case GeminiBatchStateFailed:
		return schemas.BatchStatusFailed
	case GeminiBatchStateCancelling:
		return schemas.BatchStatusCancelling
	case GeminiBatchStateCancelled:
		return schemas.BatchStatusCancelled
	case GeminiBatchStateExpired:
		return schemas.BatchStatusExpired
	default:
		return schemas.BatchStatus(geminiState)
	}
}

func parseGeminiTimestamp(timestamp string) int64 {
	if timestamp == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func (provider *GeminiProvider) downloadBatchResultsFile(ctx context.Context, key schemas.Key, fileName string) ([]schemas.BatchResultItem, []schemas.BatchError, *schemas.BifrostError) {

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	baseURL := strings.Replace(provider.networkConfig.BaseURL, "/v1beta", "/download/v1beta", 1)

	filePath, bifrostErr := geminiResourcePath(fileName, "files", "file_id")
	if bifrostErr != nil {
		return nil, nil, bifrostErr
	}

	url := fmt.Sprintf("%s/%s:download?alt=media", baseURL, filePath)

	provider.logger.Debug("gemini batch results file download url: " + url)
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(url)
	req.Header.SetMethod(http.MethodGet)
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, nil, bifrostErr
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	body, err := providerUtils.CheckAndDecodeBody(resp)
	if err != nil {
		return nil, nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, err)
	}

	results, parseErrors := parseGeminiBatchResultsJSONL(body, provider.logger.Warn)
	return results, parseErrors, nil
}

func parseGeminiBatchResultsJSONL(body []byte, warnf func(string, ...any)) ([]schemas.BatchResultItem, []schemas.BatchError) {
	results := make([]schemas.BatchResultItem, 0)
	lineIndex := 0
	parseResult := providerUtils.ParseJSONL(body, func(line []byte) error {
		sourceIndex := lineIndex
		lineIndex++

		var resultLine GeminiBatchFileResultLine
		if err := sonic.Unmarshal(line, &resultLine); err != nil {
			if warnf != nil {
				warnf("gemini batch results file parse error: %s", strconv.QuoteToASCII(err.Error()))
			}
			return err
		}

		resultItem, err := geminiFileResultToBatchResultItem(resultLine, fmt.Sprintf("request-%d", sourceIndex))
		if err != nil {
			if warnf != nil {
				warnf("gemini batch results file response parse error: %s", strconv.QuoteToASCII(err.Error()))
			}
			return err
		}
		results = append(results, resultItem)
		return nil
	})

	return results, parseResult.Errors
}

func geminiFileResultToBatchResultItem(resultLine GeminiBatchFileResultLine, fallbackID string) (schemas.BatchResultItem, error) {
	customID := resultLine.CustomID
	if customID == "" {
		customID = resultLine.Key
	}
	if customID == "" {
		customID = fallbackID
	}
	item := schemas.BatchResultItem{CustomID: customID}
	if resultLine.Error != nil {
		item.Error = &schemas.BatchResultError{
			Code:    fmt.Sprintf("%d", resultLine.Error.Code),
			Message: resultLine.Error.Message,
		}
		return item, nil
	}
	if len(resultLine.Response) == 0 || strings.TrimSpace(string(resultLine.Response)) == "null" {
		return schemas.BatchResultItem{}, fmt.Errorf("gemini batch result line has neither response nor error")
	}

	var envelope GeminiFileResponseLine
	if err := sonic.Unmarshal(resultLine.Response, &envelope); err != nil {
		return item, err
	}
	if envelope.StatusCode != 0 || envelope.Body != nil {
		item.Response = &schemas.BatchResultResponse{
			StatusCode: envelope.StatusCode,
			Body:       normalizeBatchBody(envelope.Body),
		}
		return item, nil
	}

	var native GenerateContentResponse
	if err := sonic.Unmarshal(resultLine.Response, &native); err != nil {
		return item, err
	}
	item.Response = &schemas.BatchResultResponse{
		StatusCode: http.StatusOK,
		Body:       geminiGenerateContentToBatchResultBody(&native),
	}
	return item, nil
}

func geminiBatchOutput(resp *GeminiBatchJobResponse) (fileName string, inlined []GeminiInlinedResponse) {
	if resp == nil {
		return "", nil
	}
	if resp.Response != nil {
		fileName = resp.Response.ResponsesFile
		if resp.Response.InlinedResponses != nil {
			inlined = resp.Response.InlinedResponses.InlinedResponses
		}
	}
	if fileName == "" && len(inlined) == 0 && resp.Metadata != nil && resp.Metadata.Output != nil {
		fileName = resp.Metadata.Output.ResponsesFile
		if resp.Metadata.Output.InlinedResponses != nil {
			inlined = resp.Metadata.Output.InlinedResponses.InlinedResponses
		}
	}
	return fileName, inlined
}

func geminiGenerateContentToBatchResultBody(resp *GenerateContentResponse) map[string]interface{} {
	body := make(map[string]interface{})
	if resp == nil {
		return body
	}
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			var textParts []string
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			if len(textParts) > 0 {
				body["text"] = strings.Join(textParts, "")
			}
		}
		body["finish_reason"] = string(candidate.FinishReason)
	}
	if resp.UsageMetadata != nil {
		completionTokens := resp.UsageMetadata.CandidatesTokenCount + resp.UsageMetadata.ThoughtsTokenCount
		usage := map[string]interface{}{
			"prompt_tokens":     resp.UsageMetadata.PromptTokenCount,
			"completion_tokens": completionTokens,
			"total_tokens":      resp.UsageMetadata.TotalTokenCount,
		}
		if resp.UsageMetadata.ThoughtsTokenCount > 0 {
			usage["completion_tokens_details"] = map[string]interface{}{
				"reasoning_tokens": resp.UsageMetadata.ThoughtsTokenCount,
			}
		}

		if resp.UsageMetadata.CachedContentTokenCount > 0 {
			usage["prompt_tokens_details"] = map[string]interface{}{
				"cached_tokens": resp.UsageMetadata.CachedContentTokenCount,
			}
		}
		body["usage"] = usage
	}
	return body
}

func geminiInlineResponseToBatchResultItem(inlineResp GeminiInlinedResponse, customIDFallback string) schemas.BatchResultItem {
	customID := customIDFallback
	if inlineResp.Metadata != nil && inlineResp.Metadata.Key != "" {
		customID = inlineResp.Metadata.Key
	}

	resultItem := schemas.BatchResultItem{CustomID: customID}
	if inlineResp.Error != nil {
		resultItem.Error = &schemas.BatchResultError{
			Code:    fmt.Sprintf("%d", inlineResp.Error.Code),
			Message: inlineResp.Error.Message,
		}
	} else if inlineResp.Response != nil {
		resultItem.Response = &schemas.BatchResultResponse{
			StatusCode: 200,
			Body:       geminiGenerateContentToBatchResultBody(inlineResp.Response),
		}
	}
	return resultItem
}

func normalizeBatchBody(body map[string]interface{}) map[string]interface{} {
	if body == nil {
		return nil
	}
	out := make(map[string]interface{}, len(body))
	for k, v := range body {
		out[k] = v
	}
	if raw, ok := body["usage"]; ok {
		if usage, ok := raw.(map[string]interface{}); ok {
			normalized := make(map[string]interface{}, len(usage))
			for k, v := range usage {
				switch k {
				case "promptTokens":
					normalized["prompt_tokens"] = v
				case "completionTokens":
					normalized["completion_tokens"] = v
				case "totalTokens":
					normalized["total_tokens"] = v
				default:
					normalized[k] = v
				}
			}
			out["usage"] = normalized
		}
	}
	return out
}

func extractGeminiUsageMetadata(geminiResponse *GenerateContentResponse) (int, int, int) {
	var inputTokens, outputTokens, totalTokens int
	if geminiResponse.UsageMetadata != nil {
		usageMetadata := geminiResponse.UsageMetadata
		inputTokens = int(usageMetadata.PromptTokenCount)
		outputTokens = int(usageMetadata.CandidatesTokenCount)
		totalTokens = int(usageMetadata.TotalTokenCount)
	}
	return inputTokens, outputTokens, totalTokens
}
