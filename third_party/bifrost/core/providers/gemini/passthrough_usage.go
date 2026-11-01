// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type geminiInteractionsWrapper struct {
	Usage       *geminiInteractionsUsage `json:"usage"`
	ServiceTier *string                  `json:"service_tier"`

	Interaction *struct {
		Usage       *geminiInteractionsUsage `json:"usage"`
		ServiceTier *string                  `json:"service_tier"`
	} `json:"interaction"`
}
type geminiPassthroughResp struct {
	UsageMetadata *GenerateContentResponseUsageMetadata `json:"usageMetadata"`
}
type geminiPredictResponse struct {
	Predictions []struct {
		Embeddings *struct {
			Statistics *struct {
				TokenCount int `json:"token_count"`
			} `json:"statistics"`
		} `json:"embeddings"`

		TextEmbedding   []float64 `json:"textEmbedding"`
		ImageEmbedding  []float64 `json:"imageEmbedding"`
		VideoEmbeddings []any     `json:"videoEmbeddings"`
	} `json:"predictions"`
}
type geminiInteractionsUsage struct {
	TotalTokens   int `json:"total_tokens"`
	InputTokens   int `json:"total_input_tokens"`
	OutputTokens  int `json:"total_output_tokens"`
	ThoughtTokens int `json:"total_thought_tokens"`
	CachedTokens  int `json:"total_cached_tokens"`
}

func IsInteractionsPassthroughPath(path string) bool {
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	return strings.Contains(path, "/interactions")
}

func ClassifyPassthroughPath(path string) schemas.RequestType {
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	if strings.Contains(path, "/interactions") {
		return ""
	}

	action := passthroughAction(path)
	switch action {
	case "generateContent", "streamGenerateContent":
		return schemas.ResponsesRequest
	case "countTokens":
		return schemas.CountTokensRequest
	case "embedContent", "batchEmbedContents":
		return schemas.EmbeddingRequest
	case "predict":

		if strings.Contains(strings.ToLower(passthroughModelSegment(path)), "embedding") {
			return schemas.EmbeddingRequest
		}
		return schemas.ImageGenerationRequest
	case "predictLongRunning":
		return schemas.VideoGenerationRequest
	}
	return ""
}

func passthroughAction(path string) string {
	if colonIdx := strings.LastIndexByte(path, ':'); colonIdx >= 0 {
		return path[colonIdx+1:]
	}
	return ""
}

func passthroughModelSegment(path string) string {
	if colonIdx := strings.LastIndexByte(path, ':'); colonIdx >= 0 {
		path = path[:colonIdx]
	}
	return path[strings.LastIndexByte(path, '/')+1:]
}

func ExtractGeminiPassthroughUsage(path string, reqBody, body []byte) *schemas.BifrostPassthroughUsage {
	if IsInteractionsPassthroughPath(path) {
		return extractGeminiInteractionsUsage(body)
	}

	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	if passthroughAction(path) == "predict" {
		return extractGeminiPredictUsage(reqBody, body)
	}

	switch ClassifyPassthroughPath(path) {
	case schemas.ResponsesRequest:
		return extractGeminiGenerateContentUsage(body)
	case schemas.EmbeddingRequest:
		return extractGeminiEmbeddingUsage(body)
	case schemas.VideoGenerationRequest:

		return extractGeminiVeoUsage(reqBody)
	}

	return extractGeminiGenerateContentUsage(body)
}

func HasGeminiPassthroughUsage(event []byte) bool {
	return providerUtils.GetJSONField(event, "usageMetadata").Exists() ||
		providerUtils.GetJSONField(event, "usage").Exists() ||
		providerUtils.GetJSONField(event, "interaction.usage").Exists()
}

func extractGeminiGenerateContentUsage(body []byte) *schemas.BifrostPassthroughUsage {
	if len(body) == 0 {
		return nil
	}

	var resp geminiPassthroughResp
	if err := sonic.Unmarshal(body, &resp); err != nil || resp.UsageMetadata == nil {
		return nil
	}

	ru := ConvertGeminiUsageMetadataToResponsesUsage(resp.UsageMetadata)
	if ru == nil || ru.TotalTokens == 0 {
		return nil
	}

	if ru.OutputTokensDetails != nil && ru.OutputTokensDetails.ImageTokens != nil && *ru.OutputTokensDetails.ImageTokens > 0 {
		imageUsage := &schemas.ImageUsage{
			InputTokens:  ru.InputTokens,
			OutputTokens: ru.OutputTokens,
			TotalTokens:  ru.TotalTokens,
			OutputTokensDetails: &schemas.ImageTokenDetails{
				ImageTokens:     *ru.OutputTokensDetails.ImageTokens,
				TextTokens:      ru.OutputTokensDetails.TextTokens,
				ReasoningTokens: ru.OutputTokensDetails.ReasoningTokens,
			},
		}
		if ru.InputTokensDetails != nil && (ru.InputTokensDetails.TextTokens > 0 || ru.InputTokensDetails.ImageTokens > 0) {
			imageUsage.InputTokensDetails = &schemas.ImageTokenDetails{
				TextTokens:  ru.InputTokensDetails.TextTokens,
				ImageTokens: ru.InputTokensDetails.ImageTokens,
			}
		}
		return &schemas.BifrostPassthroughUsage{
			ImageUsage: imageUsage,
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens:     ru.InputTokens,
				CompletionTokens: ru.OutputTokens,
				TotalTokens:      ru.TotalTokens,
			},
		}
	}

	usage := &schemas.BifrostLLMUsage{
		PromptTokens:     ru.InputTokens,
		CompletionTokens: ru.OutputTokens,
		TotalTokens:      ru.TotalTokens,
	}
	if ru.InputTokensDetails != nil {
		usage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{
			CachedReadTokens: ru.InputTokensDetails.CachedReadTokens,
			TextTokens:       ru.InputTokensDetails.TextTokens,
			AudioTokens:      ru.InputTokensDetails.AudioTokens,
			ImageTokens:      ru.InputTokensDetails.ImageTokens,
		}
	}
	if ru.OutputTokensDetails != nil {
		usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{
			ReasoningTokens: ru.OutputTokensDetails.ReasoningTokens,
			AudioTokens:     ru.OutputTokensDetails.AudioTokens,
		}
	}

	return &schemas.BifrostPassthroughUsage{LLMUsage: usage}
}

func extractGeminiEmbeddingUsage(body []byte) *schemas.BifrostPassthroughUsage {

	if len(body) == 0 {
		return nil
	}

	var resp geminiPassthroughResp
	if err := sonic.Unmarshal(body, &resp); err != nil || resp.UsageMetadata == nil {
		return nil
	}

	m := resp.UsageMetadata
	total := int(m.TotalTokenCount)
	prompt := int(m.PromptTokenCount)
	if total == 0 && prompt == 0 {
		return nil
	}
	if total == 0 {
		total = prompt
	}
	return &schemas.BifrostPassthroughUsage{
		LLMUsage: &schemas.BifrostLLMUsage{
			PromptTokens: prompt,
			TotalTokens:  total,
		},
	}
}

func extractGeminiInteractionsUsage(body []byte) *schemas.BifrostPassthroughUsage {
	if len(body) == 0 {
		return nil
	}

	var w geminiInteractionsWrapper
	if err := sonic.Unmarshal(body, &w); err != nil {
		return nil
	}

	u, tier := w.Usage, w.ServiceTier
	if w.Interaction != nil && w.Interaction.Usage != nil && w.Interaction.Usage.TotalTokens > 0 {
		u, tier = w.Interaction.Usage, w.Interaction.ServiceTier
	}
	if u == nil || u.TotalTokens == 0 {
		return nil
	}

	usage := &schemas.BifrostLLMUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens + u.ThoughtTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CachedTokens > 0 {
		usage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{
			CachedReadTokens: u.CachedTokens,
		}
	}
	if u.ThoughtTokens > 0 {
		usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{
			ReasoningTokens: u.ThoughtTokens,
		}
	}

	result := &schemas.BifrostPassthroughUsage{LLMUsage: usage}

	if tier != nil && *tier != "" && *tier != "standard" {
		t := schemas.BifrostServiceTier(*tier)
		result.ServiceTier = &t
	}
	return result
}

func extractGeminiVeoUsage(reqBody []byte) *schemas.BifrostPassthroughUsage {

	secs := 8
	if d, err := strconv.Atoi(schemas.DefaultVideoDuration); err == nil {
		secs = d
	}
	if len(reqBody) > 0 {
		if d := providerUtils.GetJSONField(reqBody, "parameters.durationSeconds"); d.Exists() && d.Int() > 0 {
			secs = int(d.Int())
		}
	}
	return &schemas.BifrostPassthroughUsage{VideoSeconds: &secs}
}

func extractGeminiPredictUsage(reqBody, body []byte) *schemas.BifrostPassthroughUsage {
	if len(body) > 0 {
		var resp geminiPredictResponse
		if err := sonic.Unmarshal(body, &resp); err == nil {
			if u := resp.embeddingUsage(); u != nil {
				return u
			}
		}
	}

	return extractGeminiImagenUsage(reqBody, body)
}

func (r *geminiPredictResponse) embeddingUsage() *schemas.BifrostPassthroughUsage {
	total, isEmbedding := 0, false
	for i := range r.Predictions {
		p := r.Predictions[i]
		if p.Embeddings != nil {
			isEmbedding = true
			if p.Embeddings.Statistics != nil {
				total += p.Embeddings.Statistics.TokenCount
			}
		}
		if len(p.TextEmbedding) > 0 || len(p.ImageEmbedding) > 0 || len(p.VideoEmbeddings) > 0 {
			isEmbedding = true
		}
	}
	if !isEmbedding {
		return nil
	}
	return &schemas.BifrostPassthroughUsage{
		LLMUsage: &schemas.BifrostLLMUsage{PromptTokens: total, TotalTokens: total},
	}
}

func extractGeminiImagenUsage(reqBody, body []byte) *schemas.BifrostPassthroughUsage {
	u := &schemas.BifrostPassthroughUsage{
		ImageUsage: &schemas.ImageUsage{},
	}

	if len(reqBody) > 0 {
		var req GeminiImagenRequest
		if err := sonic.Unmarshal(reqBody, &req); err == nil &&
			req.Parameters.SampleCount != nil && *req.Parameters.SampleCount > 0 {
			if u.ImageUsage.OutputTokensDetails == nil {
				u.ImageUsage.OutputTokensDetails = &schemas.ImageTokenDetails{}
			}
			u.ImageUsage.OutputTokensDetails.NImages = *req.Parameters.SampleCount
		}
	}

	if len(body) > 0 {
		var resp GeminiImagenResponse
		if err := sonic.Unmarshal(body, &resp); err == nil && len(resp.Predictions) > 0 {
			if u.ImageUsage.OutputTokensDetails == nil {
				u.ImageUsage.OutputTokensDetails = &schemas.ImageTokenDetails{}
			}
			u.ImageUsage.OutputTokensDetails.NImages = len(resp.Predictions)
		}
	}

	if u.ImageUsage.OutputTokensDetails == nil || u.ImageUsage.OutputTokensDetails.NImages == 0 {
		return nil
	}
	return u
}
