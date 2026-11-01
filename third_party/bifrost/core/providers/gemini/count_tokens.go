package gemini

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

var geminiCountTokensUnsupportedFields = []string{
	"labels",
	"fallbacks",
}

const countTokensEnvelopeField = "generateContentRequest"

func wrapGeminiCountTokensBody(jsonData []byte, model string) []byte {
	if len(jsonData) == 0 {
		return jsonData
	}

	inner := jsonData
	if envelope := providerUtils.GetJSONField(jsonData, countTokensEnvelopeField); envelope.Exists() {
		inner = []byte(envelope.Raw)
	}

	for _, field := range geminiCountTokensUnsupportedFields {
		if updated, err := providerUtils.DeleteJSONField(inner, field); err == nil {
			inner = updated
		}
	}

	if model = NormalizeModelName(model); model != "" {
		if updated, err := providerUtils.SetJSONField(inner, "model", "models/"+strings.TrimPrefix(model, "models/")); err == nil {
			inner = updated
		}
	}

	wrapped, err := providerUtils.SetRawJSONField([]byte(`{}`), countTokensEnvelopeField, inner)
	if err != nil {
		return jsonData
	}
	return wrapped
}

func (resp *GeminiCountTokensResponse) ToBifrostCountTokensResponse(model string) *schemas.BifrostCountTokensResponse {
	if resp == nil {
		return nil
	}

	inputTokens := 0
	inputDetails := &schemas.ResponsesResponseInputTokens{}

	for _, m := range resp.PromptTokensDetails {
		if m == nil {
			continue
		}
		inputTokens += int(m.TokenCount)
		AddModalityTokens(inputDetails, string(m.Modality), int(m.TokenCount))
	}

	if resp.CachedContentTokenCount != 0 {
		inputDetails.CachedReadTokens = int(resp.CachedContentTokenCount)
	} else {
		cachedSum := 0
		for _, m := range resp.CacheTokensDetails {
			if m == nil {
				continue
			}
			cachedSum += int(m.TokenCount)
		}
		inputDetails.CachedReadTokens = cachedSum
	}

	total := int(resp.TotalTokens)

	return &schemas.BifrostCountTokensResponse{
		Model:              model,
		Object:             "response.input_tokens",
		InputTokens:        inputTokens,
		InputTokensDetails: inputDetails,
		TotalTokens:        &total,
		ExtraFields:        schemas.BifrostResponseExtraFields{},
	}
}

func AddModalityTokens(details *schemas.ResponsesResponseInputTokens, modality string, count int) {
	switch mod := strings.ToLower(modality); {
	case strings.Contains(mod, "audio"):
		details.AudioTokens += count
	case strings.Contains(mod, "image"), strings.Contains(mod, "video"):
		details.ImageTokens += count
	default:
		details.TextTokens += count
	}
}
