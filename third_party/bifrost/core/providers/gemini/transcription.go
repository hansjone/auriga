package gemini

import (
	"maps"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func ToGeminiTranscriptionRequest(bifrostReq *schemas.BifrostTranscriptionRequest) *GeminiGenerationRequest {
	if bifrostReq == nil {
		return nil
	}

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	if bifrostReq.Params != nil {
		geminiReq.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)

		if bifrostReq.Params.ExtraParams != nil {

			if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safety_settings"); ok {
				delete(geminiReq.ExtraParams, "safety_settings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			}

			if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cached_content"]); ok {
				delete(geminiReq.ExtraParams, "cached_content")
				geminiReq.CachedContent = cachedContent
			}

			if labels, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "labels"); ok {
				if labelMap, ok := schemas.SafeExtractStringMap(labels); ok {
					delete(geminiReq.ExtraParams, "labels")
					geminiReq.Labels = labelMap
				}
			}
		}
	}

	var prompt string
	if bifrostReq.Params != nil && bifrostReq.Params.Prompt != nil {
		prompt = *bifrostReq.Params.Prompt
	} else {
		prompt = "Generate a transcript of the speech."
	}

	parts := []*Part{
		{
			Text: prompt,
		},
	}

	if len(bifrostReq.Input.File) > 0 {
		parts = append(parts, &Part{
			InlineData: &Blob{
				MIMEType: utils.DetectAudioMimeType(bifrostReq.Input.File),
				Data:     encodeBytesToBase64String(bifrostReq.Input.File),
			},
		})
	}

	geminiReq.Contents = []Content{
		{
			Parts: parts,
		},
	}

	return geminiReq
}

func (response *GenerateContentResponse) ToBifrostTranscriptionResponse() *schemas.BifrostTranscriptionResponse {
	bifrostResp := &schemas.BifrostTranscriptionResponse{}

	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			var textContent string

			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					textContent += part.Text
				}
			}

			if textContent != "" {
				bifrostResp.Text = textContent
				bifrostResp.Task = schemas.Ptr("transcribe")
			}
		}
	}

	bifrostResp.Usage = convertGeminiUsageMetadataToTranscriptionUsage(response.UsageMetadata)

	return bifrostResp
}
